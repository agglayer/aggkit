package bridgesync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/polygonzkevmbridge"
	aggkitabi "github.com/agglayer/aggkit/abi"
	bridgetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync/migrations"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	dbtypes "github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	"github.com/agglayer/aggkit/tree/testvectors"
	"github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/russross/meddler"
	"github.com/stretchr/testify/require"
)

const dbQueryTimeout = 30 * time.Second

func TestBigIntString(t *testing.T) {
	globalIndex := GenerateGlobalIndex(true, 0, 1093)
	fmt.Println(globalIndex.String())

	_, ok := new(big.Int).SetString(globalIndex.String(), 10)
	require.True(t, ok)

	dbPath := filepath.Join(t.TempDir(), "bridgesyncTestBigIntString.sqlite")

	err := migrations.RunMigrations(dbPath)
	require.NoError(t, err)
	db, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)

	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)

	claim := &Claim{
		BlockNum:            1,
		BlockPos:            0,
		GlobalIndex:         GenerateGlobalIndex(true, 0, 1093),
		OriginNetwork:       11,
		Amount:              big.NewInt(11),
		OriginAddress:       common.HexToAddress("0x11"),
		DestinationAddress:  common.HexToAddress("0x11"),
		ProofLocalExitRoot:  types.Proof{},
		ProofRollupExitRoot: types.Proof{},
		MainnetExitRoot:     common.Hash{},
		RollupExitRoot:      common.Hash{},
		GlobalExitRoot:      common.Hash{},
		DestinationNetwork:  12,
		Type:                ClaimEvent,
	}

	_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, claim.BlockNum)
	require.NoError(t, err)
	require.NoError(t, meddler.Insert(tx, "claim", claim))

	require.NoError(t, tx.Commit())

	tx, err = db.BeginTx(ctx, nil)
	require.NoError(t, err)

	rows, err := tx.Query(`
		SELECT * FROM claim
		WHERE block_num >= $1 AND block_num <= $2;
	`, claim.BlockNum, claim.BlockNum)
	require.NoError(t, err)

	claimsFromDB := []*Claim{}
	require.NoError(t, meddler.ScanAll(rows, &claimsFromDB))
	require.Len(t, claimsFromDB, 1)
	require.Equal(t, claim, claimsFromDB[0])
}

func TestProcessor(t *testing.T) {
	path := path.Join(t.TempDir(), "bridgeSyncerProcessor.db")
	logger := log.WithFields("module", "bridge-syncer")
	p, err := newProcessor(path, "bridge-syncer", logger, dbQueryTimeout)
	require.NoError(t, err)
	actions := []processAction{
		// processed: ~
		&getLastProcessedBlockAction{
			p:                          p,
			description:                "on an empty processor",
			ctx:                        context.Background(),
			expectedLastProcessedBlock: 0,
			expectedErr:                nil,
		},
		&reorgAction{
			p:                 p,
			description:       "on an empty processor: firstReorgedBlock = 0",
			firstReorgedBlock: 0,
			expectedErr:       nil,
		},
		&reorgAction{
			p:                 p,
			description:       "on an empty processor: firstReorgedBlock = 1",
			firstReorgedBlock: 1,
			expectedErr:       nil,
		},
		&processBlockAction{
			p:           p,
			description: "block1",
			block:       block1,
			expectedErr: nil,
		},
		// processed: block1
		&getLastProcessedBlockAction{
			p:                          p,
			description:                "after block1",
			ctx:                        context.Background(),
			expectedLastProcessedBlock: 1,
			expectedErr:                nil,
		},
		&getClaims{
			p:              p,
			description:    "after block1: range 1, 1",
			ctx:            context.Background(),
			fromBlock:      1,
			toBlock:        1,
			expectedClaims: eventsToClaims(block1.Events),
			expectedErr:    nil,
		},
		&getBridges{
			p:               p,
			description:     "after block1: range 1, 1",
			ctx:             context.Background(),
			fromBlock:       1,
			toBlock:         1,
			expectedBridges: eventsToBridges(block1.Events),
			expectedErr:     nil,
		},
		&reorgAction{
			p:                 p,
			description:       "after block1",
			firstReorgedBlock: 1,
			expectedErr:       nil,
		},
		&processBlockAction{
			p:           p,
			description: "block1 (after it's reorged)",
			block:       block1,
			expectedErr: nil,
		},
		// processed: block3
		&processBlockAction{
			p:           p,
			description: "block3",
			block:       block3,
			expectedErr: nil,
		},
		// processed: block1, block3
		&getLastProcessedBlockAction{
			p:                          p,
			description:                "after block3",
			ctx:                        context.Background(),
			expectedLastProcessedBlock: 3,
			expectedErr:                nil,
		},
		&getClaims{
			p:              p,
			description:    "after block3: range 2, 2",
			ctx:            context.Background(),
			fromBlock:      2,
			toBlock:        2,
			expectedClaims: []Claim{},
			expectedErr:    nil,
		},
		&getClaims{
			p:           p,
			description: "after block3: range 1, 3",
			ctx:         context.Background(),
			fromBlock:   1,
			toBlock:     3,
			expectedClaims: append(
				eventsToClaims(block1.Events),
				eventsToClaims(block3.Events)...,
			),
			expectedErr: nil,
		},
		&getBridges{
			p:               p,
			description:     "after block3: range 2, 2",
			ctx:             context.Background(),
			fromBlock:       2,
			toBlock:         2,
			expectedBridges: []Bridge{},
			expectedErr:     nil,
		},
		&getBridges{
			p:           p,
			description: "after block3: range 1, 3",
			ctx:         context.Background(),
			fromBlock:   1,
			toBlock:     3,
			expectedBridges: append(
				eventsToBridges(block1.Events),
				eventsToBridges(block3.Events)...,
			),
			expectedErr: nil,
		},
		&reorgAction{
			p:                 p,
			description:       "after block3, with value 3",
			firstReorgedBlock: 3,
			expectedErr:       nil,
		},
		// processed: block1
		&getLastProcessedBlockAction{
			p:                          p,
			description:                "after block3 reorged",
			ctx:                        context.Background(),
			expectedLastProcessedBlock: 1,
			expectedErr:                nil,
		},
		&reorgAction{
			p:                 p,
			description:       "after block3, with value 2",
			firstReorgedBlock: 2,
			expectedErr:       nil,
		},
		&getLastProcessedBlockAction{
			p:                          p,
			description:                "after block2 reorged",
			ctx:                        context.Background(),
			expectedLastProcessedBlock: 1,
			expectedErr:                nil,
		},
		&processBlockAction{
			p:           p,
			description: "block3 after reorg",
			block:       block3,
			expectedErr: nil,
		},
		// processed: block1, block3
		&processBlockAction{
			p:           p,
			description: "block4",
			block:       block4,
			expectedErr: nil,
		},
		// processed: block1, block3, block4
		&processBlockAction{
			p:           p,
			description: "block5",
			block:       block5,
			expectedErr: nil,
		},
		// processed: block1, block3, block4, block5
		&getLastProcessedBlockAction{
			p:                          p,
			description:                "after block5",
			ctx:                        context.Background(),
			expectedLastProcessedBlock: 5,
			expectedErr:                nil,
		},
		&getClaims{
			p:           p,
			description: "after block5: range 1, 3",
			ctx:         context.Background(),
			fromBlock:   1,
			toBlock:     3,
			expectedClaims: append(
				eventsToClaims(block1.Events),
				eventsToClaims(block3.Events)...,
			),
			expectedErr: nil,
		},
		&getClaims{
			p:           p,
			description: "after block5: range 4, 5",
			ctx:         context.Background(),
			fromBlock:   4,
			toBlock:     5,
			expectedClaims: append(
				eventsToClaims(block4.Events),
				eventsToClaims(block5.Events)...,
			),
			expectedErr: nil,
		},
		&getClaims{
			p:           p,
			description: "after block5: range 0, 5",
			ctx:         context.Background(),
			fromBlock:   0,
			toBlock:     5,
			expectedClaims: slices.Concat(
				eventsToClaims(block1.Events),
				eventsToClaims(block3.Events),
				eventsToClaims(block4.Events),
				eventsToClaims(block5.Events),
			),
			expectedErr: nil,
		},
		&getTotalRecordsAction{
			p:           p,
			description: "get number of claims after block5",
			tableName:   claimTableName,
			expectedRecordsNum: len(
				slices.Concat(
					eventsToClaims(block1.Events),
					eventsToClaims(block3.Events),
					eventsToClaims(block4.Events),
					eventsToClaims(block5.Events),
				)),
		},
		&reorgAction{
			p:                 p,
			description:       "reorg the last block",
			firstReorgedBlock: 5,
		},
		&getLastProcessedBlockAction{
			p:                          p,
			description:                "after last block reorged",
			ctx:                        context.Background(),
			expectedLastProcessedBlock: 4,
			expectedErr:                nil,
		},
	}

	for _, a := range actions {
		log.Debugf("%s: %s", a.method(), a.desc())
		a.execute(t)
	}
}

// BOILERPLATE

// blocks

var (
	block1 = sync.Block{
		Num: 1,
		Events: []any{
			Event{Bridge: &Bridge{
				BlockNum:           1,
				BlockPos:           0,
				LeafType:           bridgesynctypes.LeafTypeAsset.Uint8(),
				OriginNetwork:      1,
				OriginAddress:      common.HexToAddress("1"),
				DestinationNetwork: 1,
				DestinationAddress: common.HexToAddress("1"),
				Amount:             big.NewInt(1),
				Metadata:           common.Hex2Bytes("1"),
				DepositCount:       0,
			}},
			Event{Claim: &Claim{
				BlockNum:           1,
				BlockPos:           1,
				GlobalIndex:        big.NewInt(1),
				OriginNetwork:      1,
				OriginAddress:      common.HexToAddress("1"),
				DestinationAddress: common.HexToAddress("1"),
				Amount:             big.NewInt(1),
				MainnetExitRoot:    common.Hash{},
				Type:               DetailedClaimEvent,
			}},
			Event{TokenMapping: &TokenMapping{
				BlockNum:            1,
				BlockPos:            2,
				OriginNetwork:       1,
				OriginTokenAddress:  common.HexToAddress("0x2"),
				WrappedTokenAddress: common.HexToAddress("0x5"),
				Metadata:            common.Hex2Bytes("0x56789"),
			}},
			Event{TokenMapping: &TokenMapping{
				BlockNum:            1,
				BlockPos:            3,
				OriginNetwork:       15,
				OriginTokenAddress:  common.HexToAddress("0x6"),
				WrappedTokenAddress: common.HexToAddress("0x8"),
				Metadata:            []byte{},
			}},
			Event{TokenMapping: &TokenMapping{
				BlockNum:            1,
				BlockPos:            4,
				OriginNetwork:       5,
				OriginTokenAddress:  common.HexToAddress("0x3"),
				WrappedTokenAddress: common.HexToAddress("0x7"),
				Metadata:            nil,
			}},
		},
	}
	block3 = sync.Block{
		Num: 3,
		Events: []any{
			Event{Bridge: &Bridge{
				BlockNum:           3,
				BlockPos:           0,
				LeafType:           bridgesynctypes.LeafTypeAsset.Uint8(),
				OriginNetwork:      2,
				OriginAddress:      common.HexToAddress("2"),
				DestinationNetwork: 2,
				DestinationAddress: common.HexToAddress("2"),
				Amount:             big.NewInt(2),
				Metadata:           common.Hex2Bytes("2"),
				DepositCount:       1,
			}},
			Event{Bridge: &Bridge{
				BlockNum:           3,
				BlockPos:           1,
				LeafType:           bridgesynctypes.LeafTypeAsset.Uint8(),
				OriginNetwork:      3,
				OriginAddress:      common.HexToAddress("3"),
				DestinationNetwork: 3,
				DestinationAddress: common.HexToAddress("3"),
				Amount:             big.NewInt(0),
				Metadata:           common.Hex2Bytes("3"),
				DepositCount:       2,
			}},
			Event{Bridge: &Bridge{
				BlockNum:           3,
				BlockPos:           2,
				LeafType:           bridgesynctypes.LeafTypeAsset.Uint8(),
				OriginNetwork:      3,
				OriginAddress:      common.HexToAddress("4"),
				DestinationNetwork: 3,
				DestinationAddress: common.HexToAddress("4"),
				Amount:             big.NewInt(0),
				Metadata:           common.Hex2Bytes("4"),
				DepositCount:       3,
			}},
		},
	}
	block4 = sync.Block{
		Num:    4,
		Events: []any{},
	}
	block5 = sync.Block{
		Num: 5,
		Events: []any{
			Event{Claim: &Claim{
				BlockNum:           5,
				BlockPos:           0,
				GlobalIndex:        big.NewInt(4),
				OriginNetwork:      4,
				OriginAddress:      common.HexToAddress("04"),
				DestinationAddress: common.HexToAddress("04"),
				Amount:             big.NewInt(4),
				MainnetExitRoot:    common.Hash{},
			}},
			Event{Claim: &Claim{
				BlockNum:           5,
				BlockPos:           1,
				GlobalIndex:        big.NewInt(5),
				OriginNetwork:      5,
				OriginAddress:      common.HexToAddress("05"),
				DestinationAddress: common.HexToAddress("05"),
				Amount:             big.NewInt(5),
				MainnetExitRoot:    common.Hash{},
			}},
			Event{LegacyTokenMigration: &LegacyTokenMigration{
				BlockNum:            5,
				BlockPos:            2,
				Sender:              common.HexToAddress("0x10"),
				LegacyTokenAddress:  common.HexToAddress("0x11"),
				UpdatedTokenAddress: common.HexToAddress("0x12"),
				Amount:              big.NewInt(10),
			}},
			Event{RemoveLegacyToken: &RemoveLegacyToken{
				BlockNum:           5,
				BlockPos:           3,
				LegacyTokenAddress: common.HexToAddress("0x11"),
			}},
			Event{BackwardLET: &BackwardLET{
				BlockNum:             5,
				BlockPos:             4,
				PreviousDepositCount: big.NewInt(3),
				NewDepositCount:      big.NewInt(2),
				PreviousRoot:         common.HexToHash("0x15cd4b94cacc2cf50d055e1adb5fbfe5cd95485e121a5c411d73e263f2a66685"),
				NewRoot:              common.HexToHash("0x3edb955a657301c8007f91a0e8d2fcf7017f3dadd194aad8340018b5a5a580fa"),
			}},
		},
	}
)

// actions

type processAction interface {
	method() string
	desc() string
	execute(t *testing.T)
}

// GetClaims

type getClaims struct {
	p              *processor
	description    string
	ctx            context.Context
	fromBlock      uint64
	toBlock        uint64
	expectedClaims []Claim
	expectedErr    error
}

func (a *getClaims) method() string {
	return "GetClaims"
}

func (a *getClaims) desc() string {
	return a.description
}

func (a *getClaims) execute(t *testing.T) {
	t.Helper()
	actualEvents, actualErr := a.p.GetClaims(a.ctx, a.fromBlock, a.toBlock)
	require.Equal(t, a.expectedErr, actualErr)
	require.Equal(t, a.expectedClaims, actualEvents)
}

// GetBridges

type getBridges struct {
	p               *processor
	description     string
	ctx             context.Context
	fromBlock       uint64
	toBlock         uint64
	expectedBridges []Bridge
	expectedErr     error
}

func (a *getBridges) method() string {
	return "GetBridges"
}

func (a *getBridges) desc() string {
	return a.description
}

func (a *getBridges) execute(t *testing.T) {
	t.Helper()
	actualEvents, actualErr := a.p.GetBridges(a.ctx, a.fromBlock, a.toBlock)
	require.Equal(t, a.expectedBridges, actualEvents)
	require.Equal(t, a.expectedErr, actualErr)
}

// getLastProcessedBlock

type getLastProcessedBlockAction struct {
	p                          *processor
	description                string
	ctx                        context.Context
	expectedLastProcessedBlock uint64
	expectedErr                error
}

func (a *getLastProcessedBlockAction) method() string {
	return "getLastProcessedBlock"
}

func (a *getLastProcessedBlockAction) desc() string {
	return a.description
}

func (a *getLastProcessedBlockAction) execute(t *testing.T) {
	t.Helper()

	actualLastProcessedBlock, actualErr := a.p.GetLastProcessedBlock(a.ctx)
	require.Equal(t, a.expectedLastProcessedBlock, actualLastProcessedBlock)
	require.Equal(t, a.expectedErr, actualErr)
}

// reorg

type reorgAction struct {
	p                 *processor
	description       string
	firstReorgedBlock uint64
	expectedErr       error
}

func (a *reorgAction) method() string {
	return "reorg"
}

func (a *reorgAction) desc() string {
	return a.description
}

func (a *reorgAction) execute(t *testing.T) {
	t.Helper()

	actualErr := a.p.Reorg(context.Background(), a.firstReorgedBlock)
	require.Equal(t, a.expectedErr, actualErr)
}

// storeBridgeEvents

type processBlockAction struct {
	p           *processor
	description string
	block       sync.Block
	expectedErr error
}

func (a *processBlockAction) method() string {
	return "storeBridgeEvents"
}

func (a *processBlockAction) desc() string {
	return a.description
}

func (a *processBlockAction) execute(t *testing.T) {
	t.Helper()

	actualErr := a.p.ProcessBlock(context.Background(), a.block)
	require.Equal(t, a.expectedErr, actualErr)
}

// getTotalRecordsAction

type getTotalRecordsAction struct {
	p                  *processor
	description        string
	tableName          string
	expectedRecordsNum int
}

func (a *getTotalRecordsAction) method() string {
	return "getTotalRecordsAction"
}

func (a *getTotalRecordsAction) desc() string {
	return a.description
}

func (a *getTotalRecordsAction) execute(t *testing.T) {
	t.Helper()

	recordsNum, err := a.p.GetTotalNumberOfRecords(context.Background(), a.tableName, "")
	require.NoError(t, err)
	require.Equal(t, a.expectedRecordsNum, recordsNum)
}

func eventsToBridges(events []any) []Bridge {
	bridges := []Bridge{}
	for _, event := range events {
		e, ok := event.(Event)
		if !ok {
			panic("should be ok")
		}
		if e.Bridge != nil {
			bridges = append(bridges, *e.Bridge)
		}
	}
	return bridges
}

func eventsToClaims(events []any) []Claim {
	claims := []Claim{}
	for _, event := range events {
		e, ok := event.(Event)
		if !ok {
			panic("should be ok")
		}
		if e.Claim != nil {
			claims = append(claims, *e.Claim)
		}
	}
	return claims
}

func TestHashBridge(t *testing.T) {
	data, err := os.ReadFile("../tree/testvectors/leaf-vectors.json")
	require.NoError(t, err)

	var leafVectors []testvectors.DepositVectorRaw
	err = json.Unmarshal(data, &leafVectors)
	require.NoError(t, err)

	for ti, testVector := range leafVectors {
		t.Run(fmt.Sprintf("Test vector %d", ti), func(t *testing.T) {
			amount, err := big.NewInt(0).SetString(testVector.Amount, 0)
			require.True(t, err)

			bridge := Bridge{
				OriginNetwork:      testVector.OriginNetwork,
				OriginAddress:      common.HexToAddress(testVector.TokenAddress),
				Amount:             amount,
				DestinationNetwork: testVector.DestinationNetwork,
				DestinationAddress: common.HexToAddress(testVector.DestinationAddress),
				DepositCount:       uint32(ti + 1),
				Metadata:           common.FromHex(testVector.Metadata),
			}
			require.Equal(t, common.HexToHash(testVector.ExpectedHash), bridge.Hash())
		})
	}
}

func TestGenerateGlobalIndexForNetworkID(t *testing.T) {
	tests := []struct {
		name                string
		sourceNetworkID     uint32
		depositCount        uint32
		expectedGlobalIndex *big.Int
	}{
		{
			name:                "mainnet, deposit 0",
			sourceNetworkID:     0,
			depositCount:        0,
			expectedGlobalIndex: new(big.Int).Lsh(big.NewInt(1), mainnetFlagPosition),
		},
		{
			name:            "mainnet, deposit 3",
			sourceNetworkID: 0,
			depositCount:    3,
			expectedGlobalIndex: new(big.Int).Add(
				new(big.Int).Lsh(big.NewInt(1), mainnetFlagPosition),
				big.NewInt(3),
			),
		},
		{
			name:                "rollup 1, deposit 3",
			sourceNetworkID:     1,
			depositCount:        3,
			expectedGlobalIndex: big.NewInt(3), // (1-1)<<32 + 3 = 3
		},
		{
			name:                "rollup 2, deposit 0",
			sourceNetworkID:     2,
			depositCount:        0,
			expectedGlobalIndex: new(big.Int).Lsh(big.NewInt(1), rollupIndexPosition), // (2-1)<<32
		},
		{
			name:            "rollup 3, deposit 42",
			sourceNetworkID: 3,
			depositCount:    42,
			expectedGlobalIndex: new(big.Int).Add(
				new(big.Int).Lsh(big.NewInt(2), rollupIndexPosition),
				big.NewInt(42),
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectedIsMainnet := tt.sourceNetworkID == 0
			globalIndex := GenerateGlobalIndexForNetworkID(tt.sourceNetworkID, tt.depositCount)
			require.Equal(t, tt.expectedGlobalIndex, globalIndex)

			isMainnet, rollupIndex, localExitRootIndex, err := DecodeGlobalIndex(globalIndex)
			require.Equal(t, expectedIsMainnet, isMainnet)

			expectedRollupIndex := uint32(0)
			if !expectedIsMainnet {
				expectedRollupIndex = tt.sourceNetworkID - 1
			}
			require.Equal(t, expectedRollupIndex, rollupIndex)
			require.Equal(t, tt.depositCount, localExitRootIndex)
			require.NoError(t, err)
		})
	}
}

func TestDecodeGlobalIndex(t *testing.T) {
	t.Parallel()
	bigInt, ok := new(big.Int).SetString("3402823669209384634652192818391391666177", 10)
	require.True(t, ok)

	tests := []struct {
		name                string
		globalIndex         *big.Int
		expectedMainnetFlag bool
		expectedRollupIndex uint32
		expectedLocalIndex  uint32
		expectedErr         error
	}{
		{
			name:                "Mainnet flag true, rollup index 0",
			globalIndex:         GenerateGlobalIndex(true, 0, 2),
			expectedMainnetFlag: true,
			expectedRollupIndex: 0,
			expectedLocalIndex:  2,
			expectedErr:         nil,
		},
		{
			name:                "Mainnet flag true, indexes 0",
			globalIndex:         GenerateGlobalIndex(true, 0, 0),
			expectedMainnetFlag: true,
			expectedRollupIndex: 0,
			expectedLocalIndex:  0,
			expectedErr:         nil,
		},
		{
			name:                "Mainnet flag false, rollup index 0",
			globalIndex:         GenerateGlobalIndex(false, 0, 2),
			expectedMainnetFlag: false,
			expectedRollupIndex: 0,
			expectedLocalIndex:  2,
			expectedErr:         nil,
		},
		{
			name:                "Mainnet flag false, rollup index non-zero",
			globalIndex:         GenerateGlobalIndex(false, 11, 0),
			expectedMainnetFlag: false,
			expectedRollupIndex: 11,
			expectedLocalIndex:  0,
			expectedErr:         nil,
		},
		{
			name:                "Mainnet flag false, indexes 0",
			globalIndex:         GenerateGlobalIndex(false, 0, 0),
			expectedMainnetFlag: false,
			expectedRollupIndex: 0,
			expectedLocalIndex:  0,
			expectedErr:         nil,
		},
		{
			name:                "Mainnet flag false, indexes non zero",
			globalIndex:         GenerateGlobalIndex(false, 1231, 111234),
			expectedMainnetFlag: false,
			expectedRollupIndex: 1231,
			expectedLocalIndex:  111234,
			expectedErr:         nil,
		},
		{
			name:                "Big string",
			globalIndex:         bigInt,
			expectedMainnetFlag: true,
			expectedRollupIndex: 0,
			expectedLocalIndex:  1,
			expectedErr:         nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mainnetFlag, rollupIndex, localExitRootIndex, err := DecodeGlobalIndex(tt.globalIndex)
			if tt.expectedErr != nil {
				require.EqualError(t, err, tt.expectedErr.Error())
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.expectedMainnetFlag, mainnetFlag)
			require.Equal(t, tt.expectedRollupIndex, rollupIndex)
			require.Equal(t, tt.expectedLocalIndex, localExitRootIndex)
		})
	}
}

func TestInsertAndGetClaim(t *testing.T) {
	path := path.Join(t.TempDir(), "TestInsertAndGetClaim.sqlite")
	err := migrations.RunMigrations(path)
	require.NoError(t, err)
	logger := log.WithFields("bridge-syncer", "foo")
	p, err := newProcessor(path, "foo", logger, dbQueryTimeout)
	require.NoError(t, err)

	tx, err := p.db.BeginTx(context.Background(), nil)
	require.NoError(t, err)

	// insert test claim
	testClaim := Claim{
		BlockNum:            1,
		BlockPos:            0,
		GlobalIndex:         GenerateGlobalIndexForNetworkID(0, 1093),
		OriginNetwork:       11,
		OriginAddress:       common.HexToAddress("0x11"),
		DestinationAddress:  common.HexToAddress("0x11"),
		Amount:              big.NewInt(11),
		ProofLocalExitRoot:  types.Proof{},
		ProofRollupExitRoot: types.Proof{},
		MainnetExitRoot:     common.Hash{},
		RollupExitRoot:      common.Hash{},
		GlobalExitRoot:      common.Hash{},
		DestinationNetwork:  12,
		Metadata:            []byte("0x11"),
		IsMessage:           false,
		Type:                ClaimEvent,
	}

	_, err = tx.Exec(`INSERT INTO block (num, hash) VALUES ($1, $2)`, testClaim.BlockNum, fmt.Sprintf("0x%x", testClaim.BlockNum))
	require.NoError(t, err)
	require.NoError(t, meddler.Insert(tx, "claim", &testClaim))

	require.NoError(t, tx.Commit())

	// get test claim
	claims, err := p.GetClaims(context.Background(), 1, 1)
	require.NoError(t, err)
	require.Len(t, claims, 1)
	require.Equal(t, testClaim, claims[0])
}

func TestGetBridgesPublished(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                    string
		fromBlock               uint64
		toBlock                 uint64
		bridges                 []Bridge
		lastUpdatedDepositCount uint32
		expectedBridges         []Bridge
		expectedError           error
	}{
		{
			name:                    "no bridges",
			fromBlock:               1,
			toBlock:                 10,
			bridges:                 []Bridge{},
			lastUpdatedDepositCount: 0,
			expectedBridges:         []Bridge{},
			expectedError:           nil,
		},
		{
			name:      "bridges within deposit count",
			fromBlock: 1,
			toBlock:   10,
			bridges: []Bridge{
				{DepositCount: 1, BlockNum: 1, Amount: big.NewInt(1)},
				{DepositCount: 2, BlockNum: 2, Amount: big.NewInt(1)},
			},
			lastUpdatedDepositCount: 2,
			expectedBridges: []Bridge{
				{DepositCount: 1, BlockNum: 1, Amount: big.NewInt(1)},
				{DepositCount: 2, BlockNum: 2, Amount: big.NewInt(1)},
			},
			expectedError: nil,
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := path.Join(t.TempDir(), fmt.Sprintf("bridgesyncTestGetBridgesPublished_%s.sqlite", tc.name))
			require.NoError(t, migrations.RunMigrations(path))
			logger := log.WithFields("bridge-syncer", "foo")
			p, err := newProcessor(path, "foo", logger, dbQueryTimeout)
			require.NoError(t, err)

			tx, err := p.db.BeginTx(context.Background(), nil)
			require.NoError(t, err)

			for i := tc.fromBlock; i <= tc.toBlock; i++ {
				_, err = tx.Exec(`INSERT INTO block (num, hash) VALUES ($1, $2)`, i, fmt.Sprintf("0x%x", i))
				require.NoError(t, err)
			}

			for _, bridge := range tc.bridges {
				require.NoError(t, meddler.Insert(tx, "bridge", &bridge))
			}

			require.NoError(t, tx.Commit())

			ctx := context.Background()
			bridges, err := p.GetBridges(ctx, tc.fromBlock, tc.toBlock)

			if tc.expectedError != nil {
				require.Equal(t, tc.expectedError, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedBridges, bridges)
			}
		})
	}
}

func TestProcessBlockInvalidIndex(t *testing.T) {
	path := path.Join(t.TempDir(), "aggsenderTestProcessor.sqlite")
	logger := log.WithFields("bridge-syncer", "foo")
	p, err := newProcessor(path, "foo", logger, dbQueryTimeout)
	require.NoError(t, err)
	err = p.ProcessBlock(context.Background(), sync.Block{
		Num: 0,
		Events: []any{
			Event{Bridge: &Bridge{DepositCount: 5}},
		},
	})
	require.True(t, errors.Is(err, sync.ErrInconsistentState))
	require.True(t, p.halted)
	err = p.ProcessBlock(context.Background(), sync.Block{})
	require.True(t, errors.Is(err, sync.ErrInconsistentState))
}

func TestGetBridgesPaged(t *testing.T) {
	t.Parallel()
	fromBlock := uint64(1)
	toBlock := uint64(10)
	bridges := []*Bridge{
		{DepositCount: 0, BlockNum: 1, Amount: big.NewInt(1), DestinationNetwork: 10, FromAddress: func() *common.Address {
			addr := common.HexToAddress("0xE34aaF64b29273B7D567FCFc40544c014EEe9970")
			return &addr
		}()},
		{DepositCount: 1, BlockNum: 2, Amount: big.NewInt(1), DestinationNetwork: 10, FromAddress: func() *common.Address {
			addr := common.HexToAddress("0xE34aaF64b29273B7D567FCFc40544c014EEe9970")
			return &addr
		}()},
		{DepositCount: 2, BlockNum: 3, Amount: big.NewInt(1), DestinationNetwork: 20, FromAddress: func() *common.Address {
			addr := common.HexToAddress("0xE34aaF64b29273B7D567FCFc40544c014EEe9970")
			return &addr
		}()},
		{DepositCount: 3, BlockNum: 4, Amount: big.NewInt(1), DestinationNetwork: 30, FromAddress: func() *common.Address {
			addr := common.HexToAddress("0xE34aaF64b29273B7D567FCFc40544c014EEe9970")
			return &addr
		}()},
		{DepositCount: 4, BlockNum: 5, Amount: big.NewInt(1), DestinationNetwork: 30, FromAddress: func() *common.Address {
			addr := common.HexToAddress("0xE34aaF64b29273B7D567FCFc40544c014EEe9970")
			return &addr
		}()},
		{DepositCount: 5, BlockNum: 6, Amount: big.NewInt(1), DestinationNetwork: 30, FromAddress: func() *common.Address {
			addr := common.HexToAddress("0xE34aaF64b29273B7D567FCFc40544c014EEe9970")
			return &addr
		}()},
		{DepositCount: 6, BlockNum: 7, Amount: big.NewInt(1), DestinationNetwork: 50, FromAddress: func() *common.Address {
			addr := common.HexToAddress("0xd34aaF64b29273B7D567FCFc40544c014EEe9970")
			return &addr
		}()},
	}

	path := path.Join(t.TempDir(), "bridgesyncGetBridgesPaged.sqlite")
	require.NoError(t, migrations.RunMigrations(path))
	logger := log.WithFields("bridge-syncer", "foo")
	p, err := newProcessor(path, "bridge-syncer", logger, dbQueryTimeout)
	require.NoError(t, err)

	tx, err := p.db.BeginTx(context.Background(), nil)
	require.NoError(t, err)

	for i := fromBlock; i <= toBlock; i++ {
		_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, i)
		require.NoError(t, err)
	}

	for _, bridge := range bridges {
		require.NoError(t, meddler.Insert(tx, "bridge", bridge))
	}
	require.NoError(t, tx.Commit())

	testCases := []struct {
		name            string
		pageSize        uint32
		page            uint32
		depositCount    *uint64
		networkIDs      []uint32
		fromAddress     string
		expectedCount   int
		expectedBridges []*Bridge
		expectedError   string
	}{
		{
			name:            "t1",
			pageSize:        1,
			page:            1,
			depositCount:    nil,
			expectedCount:   len(bridges),
			expectedBridges: []*Bridge{bridges[6]},
			expectedError:   "",
		},
		{
			name:          "t2",
			pageSize:      20,
			page:          1,
			depositCount:  nil,
			expectedCount: len(bridges),
			expectedBridges: []*Bridge{
				bridges[6],
				bridges[5],
				bridges[4],
				bridges[3],
				bridges[2],
				bridges[1],
				bridges[0],
			},
			expectedError: "",
		},
		{
			name:          "t3",
			pageSize:      3,
			page:          2,
			depositCount:  nil,
			expectedCount: len(bridges),
			expectedBridges: []*Bridge{
				bridges[3],
				bridges[2],
				bridges[1],
			},
			expectedError: "",
		},
		{
			name:            "t4",
			pageSize:        1,
			page:            1,
			depositCount:    uint64Ptr(1),
			expectedCount:   1,
			expectedBridges: []*Bridge{bridges[1]},
			expectedError:   "",
		},
		{
			name:            "t5",
			pageSize:        3,
			page:            2,
			depositCount:    uint64Ptr(1),
			expectedCount:   0,
			expectedBridges: []*Bridge{},
			expectedError:   "invalid page number for given page size and total number of bridges",
		},
		{
			name:            "t6",
			pageSize:        2,
			page:            20,
			depositCount:    nil,
			expectedCount:   len(bridges),
			expectedBridges: []*Bridge{},
			expectedError:   "invalid page number for given page size and total number of bridges",
		},
		{
			name:            "t7",
			pageSize:        1,
			page:            1,
			depositCount:    uint64Ptr(0),
			expectedCount:   1,
			expectedBridges: []*Bridge{bridges[0]},
			expectedError:   "",
		},
		{
			name:         "t8",
			pageSize:     6,
			page:         1,
			depositCount: nil,
			networkIDs: []uint32{
				bridges[0].DestinationNetwork,
				bridges[2].DestinationNetwork,
				bridges[6].DestinationNetwork,
			},
			expectedCount: 4,
			expectedBridges: []*Bridge{
				bridges[6],
				bridges[2],
				bridges[1],
				bridges[0],
			},
			expectedError: "",
		},
		{
			name:         "t9",
			pageSize:     6,
			page:         1,
			depositCount: uint64Ptr(3),
			networkIDs: []uint32{
				bridges[0].DestinationNetwork,
				bridges[2].DestinationNetwork,
				bridges[6].DestinationNetwork,
			},
			expectedCount:   0,
			expectedBridges: []*Bridge{},
			expectedError:   "",
		},
		{
			name:            "t10",
			pageSize:        1,
			page:            1,
			fromAddress:     "0xE34aaF64b29273B7D567FCFc40544c014EEe9970",
			depositCount:    uint64Ptr(0),
			expectedCount:   1,
			expectedBridges: []*Bridge{bridges[0]},
			expectedError:   "",
		},
		{
			name:            "t11",
			pageSize:        1,
			page:            1,
			fromAddress:     "0xe34aaF64b29273B7D567FCFc40544c014EEe9970",
			depositCount:    uint64Ptr(0),
			expectedCount:   1,
			expectedBridges: []*Bridge{bridges[0]},
			expectedError:   "",
		},
		{
			name:            "t12",
			pageSize:        1,
			page:            1,
			fromAddress:     "0xf34aad64b29273B7D567FCFc40544c014EEe9970",
			depositCount:    nil,
			expectedCount:   0,
			expectedBridges: []*Bridge{},
			expectedError:   "",
		},
		{
			name:            "t13",
			pageSize:        10,
			page:            1,
			fromAddress:     "0xD34AAF64b29273B7D567FCFc40544c014EEe9970",
			depositCount:    nil,
			expectedCount:   1,
			expectedBridges: []*Bridge{bridges[6]},
			expectedError:   "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			bridges, count, err := p.GetBridgesPaged(ctx, tc.page, tc.pageSize, tc.depositCount, tc.networkIDs, tc.fromAddress)

			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedBridges, bridges)
				require.Equal(t, tc.expectedCount, count)
			}
		})
	}
}

func TestGetClaimsPaged(t *testing.T) {
	t.Parallel()
	fromBlock := uint64(1)
	toBlock := uint64(10)

	// Compute uint256 max: 2^256 - 1
	uint256Max := new(big.Int).Sub(new(big.Int).Exp(big.NewInt(2), big.NewInt(256), nil), big.NewInt(1))
	// Compute uint64 max: 2^64 - 1 = 18446744073709551615
	uint64Max := new(big.Int).Sub(new(big.Int).Exp(big.NewInt(2), big.NewInt(64), nil), big.NewInt(1))
	num1 := new(big.Int)
	num1.SetString("18446744073709551617", 10)
	num2 := new(big.Int)
	num2.SetString("18446744073709551618", 10)

	claims := []*Claim{
		{BlockNum: 1, GlobalIndex: num2, Amount: big.NewInt(1), OriginNetwork: 1, MainnetExitRoot: common.Hash{}},
		{BlockNum: 2, GlobalIndex: big.NewInt(2), Amount: big.NewInt(1), OriginNetwork: 1, MainnetExitRoot: common.Hash{}},
		{BlockNum: 3, GlobalIndex: uint64Max, Amount: big.NewInt(1), OriginNetwork: 2, MainnetExitRoot: common.Hash{}},
		{BlockNum: 4, GlobalIndex: num1, Amount: big.NewInt(1), OriginNetwork: 2, MainnetExitRoot: common.Hash{}},
		{BlockNum: 5, GlobalIndex: big.NewInt(5), Amount: big.NewInt(1), OriginNetwork: 3, MainnetExitRoot: common.Hash{}},
		{BlockNum: 6, GlobalIndex: uint256Max, Amount: big.NewInt(1), OriginNetwork: 4, MainnetExitRoot: common.Hash{}},
	}

	path := path.Join(t.TempDir(), "bridgesyncGetClaimsPaged.sqlite")
	require.NoError(t, migrations.RunMigrations(path))
	logger := log.WithFields("module", "bridge-syncer")
	p, err := newProcessor(path, "bridge-syncer", logger, dbQueryTimeout)
	require.NoError(t, err)

	tx, err := p.db.BeginTx(context.Background(), nil)
	require.NoError(t, err)

	for i := fromBlock; i <= toBlock; i++ {
		_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, i)
		require.NoError(t, err)
	}

	for _, claim := range claims {
		require.NoError(t, meddler.Insert(tx, "claim", claim))
	}
	require.NoError(t, tx.Commit())

	testCases := []struct {
		name           string
		pageSize       uint32
		page           uint32
		networkIDs     []uint32
		globalIndex    *big.Int
		expectedCount  int
		expectedClaims []*Claim
		expectedError  string
	}{
		{
			name:           "pagination: page 2, size 1",
			pageSize:       1,
			page:           2,
			expectedCount:  len(claims),
			expectedClaims: []*Claim{claims[4]},
			expectedError:  "",
		},
		{
			name:           "all results on the same page",
			pageSize:       20,
			page:           1,
			expectedCount:  len(claims),
			expectedClaims: []*Claim{claims[5], claims[4], claims[3], claims[2], claims[1], claims[0]},
			expectedError:  "",
		},
		{
			name:           "pagination: page 2, size 3",
			pageSize:       3,
			page:           2,
			expectedCount:  len(claims),
			expectedClaims: []*Claim{claims[2], claims[1], claims[0]},
			expectedError:  "",
		},
		{
			name:           "invalid page size",
			pageSize:       3,
			page:           4,
			expectedCount:  0,
			expectedClaims: []*Claim{},
			expectedError:  "invalid page number for given page size and total number of claims",
		},
		{
			name:           "filter by network ids (all results within the same page)",
			pageSize:       3,
			page:           1,
			networkIDs:     []uint32{claims[0].OriginNetwork, claims[4].OriginNetwork},
			expectedCount:  3,
			expectedClaims: []*Claim{claims[4], claims[1], claims[0]},
			expectedError:  "",
		},
		{
			name:           "filter by network ids (paginated results)",
			pageSize:       1,
			page:           2,
			networkIDs:     []uint32{claims[0].OriginNetwork, claims[4].OriginNetwork},
			expectedCount:  3,
			expectedClaims: []*Claim{claims[1]},
			expectedError:  "",
		},
		{
			name:           "filter by network ids (all results within the same page) and from address",
			pageSize:       3,
			page:           1,
			networkIDs:     []uint32{claims[0].OriginNetwork, claims[4].OriginNetwork},
			expectedCount:  3,
			expectedClaims: []*Claim{claims[4], claims[1], claims[0]},
			expectedError:  "",
		},
		{
			name:           "filter by global index",
			pageSize:       3,
			page:           1,
			globalIndex:    big.NewInt(5),
			expectedCount:  1,
			expectedClaims: []*Claim{claims[4]},
			expectedError:  "",
		},
		{
			name:           "filter by network ids and global index",
			pageSize:       3,
			page:           1,
			networkIDs:     []uint32{2, 3, 4},
			globalIndex:    uint64Max,
			expectedCount:  1,
			expectedClaims: []*Claim{claims[2]},
			expectedError:  "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			claims, count, err := p.GetClaimsPaged(ctx, tc.page, tc.pageSize,
				tc.networkIDs, tc.globalIndex)

			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedClaims, claims)
				require.Equal(t, tc.expectedCount, count)
			}
		})
	}
}

func TestProcessor_GetTokenMappings(t *testing.T) {
	t.Parallel()

	const tokenMappingsCount = 50

	path := path.Join(t.TempDir(), "tokenMapping.db")
	err := migrations.RunMigrations(path)
	require.NoError(t, err)

	logger := log.WithFields("module", "bridge-syncer")
	p, err := newProcessor(path, "bridge-syncer", logger, dbQueryTimeout)
	require.NoError(t, err)

	allTokenMappings := make([]*TokenMapping, 0, tokenMappingsCount)
	for i := tokenMappingsCount - 1; i >= 0; i-- {
		tokenMappingEvt := &TokenMapping{
			BlockNum:            uint64(i + 1),
			OriginNetwork:       uint32(i),
			OriginTokenAddress:  common.HexToAddress(fmt.Sprintf("%d", i)),
			WrappedTokenAddress: common.HexToAddress(fmt.Sprintf("%d", i+1)),
			Metadata:            common.Hex2Bytes(fmt.Sprintf("%x", i+1)),
		}

		if i%2 == 0 {
			tokenMappingEvt.Type = bridgetypes.WrappedToken
			tokenMappingEvt.IsNotMintable = false
		} else {
			tokenMappingEvt.Type = bridgetypes.SovereignToken
			tokenMappingEvt.IsNotMintable = true
		}

		block := sync.Block{
			Num:    uint64(i + 1),
			Events: []any{Event{TokenMapping: tokenMappingEvt}},
		}

		allTokenMappings = append(allTokenMappings, tokenMappingEvt)

		// insert TokenMapping event to the db
		err = p.ProcessBlock(context.Background(), block)
		require.NoError(t, err)
	}

	tests := []struct {
		name        string
		pageNumber  uint32
		pageSize    uint32
		expectedLen int
		expectedErr string
	}{
		{
			name:        "First page",
			pageNumber:  1,
			pageSize:    10,
			expectedLen: 10,
			expectedErr: "",
		},
		{
			name:        "Second page",
			pageNumber:  2,
			pageSize:    5,
			expectedLen: 5,
			expectedErr: "",
		},
		{
			name:        "Last page",
			pageNumber:  5,
			pageSize:    10,
			expectedLen: 10,
			expectedErr: "",
		},
		{
			name:        "Page out of range",
			pageNumber:  6,
			pageSize:    10,
			expectedLen: 0,
			expectedErr: "invalid page number for given page size and total number of token mappings",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, totalTokenMappings, err := p.GetTokenMappings(context.Background(), tt.pageNumber, tt.pageSize, "")
			if tt.expectedErr != "" {
				require.ErrorContains(t, err, tt.expectedErr)
			} else {
				require.NoError(t, err)
				require.Len(t, result, tt.expectedLen)
				require.Equal(t, tokenMappingsCount, totalTokenMappings)

				offset := (tt.pageNumber - 1) * tt.pageSize
				for i, mapping := range result {
					require.Equal(t, allTokenMappings[offset+uint32(i)], mapping)
				}
			}
		})
	}
}

func TestProcessor_GetLegacyTokenMigrations(t *testing.T) {
	t.Parallel()
	path := path.Join(t.TempDir(), "tokenMigrations.db")
	err := migrations.RunMigrations(path)
	require.NoError(t, err)

	logger := log.WithFields("module", "bridge-syncer")
	p, err := newProcessor(path, "bridge-syncer", logger, dbQueryTimeout)
	require.NoError(t, err)

	const (
		tokenMigrationsCount       = 50
		removeTokenMigrationsCount = 10
	)
	tokenMigrationEvents := make([]*LegacyTokenMigration, 0, tokenMigrationsCount)
	removeTokenMigrationEvents := make([]*RemoveLegacyToken, 0, removeTokenMigrationsCount)
	for i := range tokenMigrationsCount {
		e := &LegacyTokenMigration{
			BlockNum:            uint64(1),
			BlockPos:            uint64(i),
			LegacyTokenAddress:  common.HexToAddress(fmt.Sprintf("%d", i+1)),
			UpdatedTokenAddress: common.HexToAddress(fmt.Sprintf("%d", i+2)),
			Amount:              big.NewInt(int64(i + 1)),
		}
		tokenMigrationEvents = append(tokenMigrationEvents, e)
	}

	// Sort in descending order of block pos and block num
	sort.Slice(tokenMigrationEvents, func(i, j int) bool {
		prevTokenMig := tokenMigrationEvents[i]
		currentTokenMig := tokenMigrationEvents[j]
		if prevTokenMig.BlockPos > currentTokenMig.BlockPos {
			return true
		}

		if prevTokenMig.BlockNum > currentTokenMig.BlockNum {
			return true
		}

		return false
	})

	for i := range removeTokenMigrationsCount {
		e := &RemoveLegacyToken{
			BlockNum:           uint64(2),
			BlockPos:           uint64(i),
			LegacyTokenAddress: common.HexToAddress(fmt.Sprintf("%d", i+1)),
		}
		removeTokenMigrationEvents = append(removeTokenMigrationEvents, e)
	}

	block1 := sync.Block{
		Num:    uint64(1),
		Events: []any{},
	}

	for _, e := range tokenMigrationEvents {
		block1.Events = append(block1.Events, Event{LegacyTokenMigration: e})
	}

	block2 := sync.Block{
		Num:    uint64(2),
		Events: []any{},
	}

	for _, e := range removeTokenMigrationEvents {
		block2.Events = append(block2.Events, Event{RemoveLegacyToken: e})
	}

	// Insert all LegacyTokenMigration events
	err = p.ProcessBlock(context.Background(), block1)
	require.NoError(t, err)

	result, totalTokenMigrations, err := p.GetLegacyTokenMigrations(context.Background(), 1, tokenMigrationsCount)

	require.NoError(t, err)
	require.Len(t, result, tokenMigrationsCount)
	require.Equal(t, result, tokenMigrationEvents)
	require.Equal(t, tokenMigrationsCount, totalTokenMigrations)

	// Process block that contains RemoveLegacyToken events
	err = p.ProcessBlock(context.Background(), block2)
	require.NoError(t, err)

	finalTokenMigrationsCount := tokenMigrationsCount - removeTokenMigrationsCount
	result, totalTokenMigrations, err = p.GetLegacyTokenMigrations(context.Background(), 1, tokenMigrationsCount)
	require.NoError(t, err)
	require.Equal(t, totalTokenMigrations, finalTokenMigrationsCount)
	require.Len(t, result, finalTokenMigrationsCount)
	require.Equal(t, tokenMigrationEvents[:finalTokenMigrationsCount], result)
}

func TestDecodePreEtrogCalldata_Valid(t *testing.T) {
	bridgeV1ABI, err := polygonzkevmbridge.PolygonzkevmbridgeMetaData.GetAbi()
	require.NoError(t, err)

	globalIndex := uint32(10)
	originNetwork := uint32(5)
	originAddress := common.HexToAddress("0x0a0a")
	amount := big.NewInt(150)
	destinationAddr := common.HexToAddress("0x0b0b")

	proof := types.Proof{}
	for i := range types.DefaultHeight {
		for j := range common.HashLength {
			proof[i] = common.HexToHash(fmt.Sprintf("%x", (j+1)%common.HashLength))
		}
	}

	expectedClaim := &Claim{
		GlobalIndex:        new(big.Int).SetUint64(uint64(globalIndex)),
		MainnetExitRoot:    common.HexToHash("0xdead"),
		RollupExitRoot:     common.HexToHash("0xbeef"),
		DestinationNetwork: uint32(6),
		Metadata:           common.Hex2Bytes("c001"),
		ProofLocalExitRoot: proof,
	}

	expectedClaim.GlobalExitRoot = crypto.Keccak256Hash(expectedClaim.MainnetExitRoot.Bytes(), expectedClaim.RollupExitRoot.Bytes())

	claimAssetInput, err := bridgeV1ABI.Pack("claimAsset",
		expectedClaim.ProofLocalExitRoot,
		globalIndex,
		expectedClaim.MainnetExitRoot,
		expectedClaim.RollupExitRoot,
		originNetwork,
		originAddress,
		expectedClaim.DestinationNetwork,
		destinationAddr,
		amount,
		expectedClaim.Metadata,
	)
	require.NoError(t, err)

	actualClaim := &Claim{
		GlobalIndex: new(big.Int).SetUint64(uint64(globalIndex)),
	}
	method, err := bridgeV1ABI.MethodById(claimAssetPreEtrogMethodID)
	require.NoError(t, err)

	claimAssetData, err := method.Inputs.Unpack(claimAssetInput[4:])
	require.NoError(t, err)

	isFound, err := actualClaim.decodePreEtrogCalldata(claimAssetData)
	require.NoError(t, err)
	require.True(t, isFound)

	require.Equal(t, expectedClaim, actualClaim)
}

func TestTokenMappingTypeString(t *testing.T) {
	tests := []struct {
		name     string
		t        bridgetypes.TokenMappingType
		expected string
	}{
		{
			name:     "WrappedToken",
			t:        bridgetypes.WrappedToken,
			expected: "WrappedToken",
		},
		{
			name:     "SovereignToken",
			t:        bridgetypes.SovereignToken,
			expected: "SovereignToken",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.t.String())
		})
	}
}

func TestDecodePreEtrogCalldata(t *testing.T) {
	var (
		globalIndex            = uint32(12345)
		mainnetExitRoot        = common.HexToHash("0x11")
		rollupExitRoot         = common.HexToHash("0x22")
		metadata               = []byte("mock metadata")
		destinationNetwork     = uint32(1)
		invalidTypePlaceholder = "invalidType"
	)

	tests := []struct {
		name              string
		data              []any
		expectedIsDecoded bool
		expectError       bool
	}{
		{
			name: "Valid calldata",
			data: []any{
				[types.DefaultHeight][common.HashLength]byte{}, // Proof
				globalIndex, // GlobalIndex
				[common.HashLength]byte(mainnetExitRoot.Bytes()), // MainnetExitRoot
				[common.HashLength]byte(rollupExitRoot.Bytes()),  // RollupExitRoot
				uint32(1),          // OriginNetwork (not used)
				common.Address{},   // OriginTokenAddress (not used)
				destinationNetwork, // DestinationNetwork
				common.Address{},   // DestinationAddress (not used)
				big.NewInt(0),      // Amount (not used)
				metadata,           // Metadata
			},
			expectedIsDecoded: true,
			expectError:       false,
		},
		{
			name: "Mismatched GlobalIndex",
			data: []any{
				[types.DefaultHeight][common.HashLength]byte{}, // Proof
				uint32(99999), // Wrong GlobalIndex
				[common.HashLength]byte(mainnetExitRoot.Bytes()),
				[common.HashLength]byte(rollupExitRoot.Bytes()),
				uint32(1),
				common.Address{},
				destinationNetwork,
				common.Address{},
				big.NewInt(0),
				metadata,
			},
			expectedIsDecoded: false,
			expectError:       false, // No error, just a mismatch
		},
		{
			name: "Invalid GlobalIndex Type",
			data: []any{
				[types.DefaultHeight][common.HashLength]byte{},
				invalidTypePlaceholder, // Invalid GlobalIndex type
				[common.HashLength]byte(mainnetExitRoot.Bytes()),
				[common.HashLength]byte(rollupExitRoot.Bytes()),
				uint32(1),
				common.Address{},
				destinationNetwork,
				common.Address{},
				big.NewInt(0),
				metadata,
			},
			expectedIsDecoded: false,
			expectError:       true,
		},
		{
			name: "Invalid Proof Type",
			data: []any{
				invalidTypePlaceholder, // Invalid Proof type
				globalIndex,
				[common.HashLength]byte(mainnetExitRoot.Bytes()),
				[common.HashLength]byte(rollupExitRoot.Bytes()),
				uint32(1),
				common.Address{},
				destinationNetwork,
				common.Address{},
				big.NewInt(0),
				metadata,
			},
			expectedIsDecoded: false,
			expectError:       true,
		},
		{
			name: "Invalid MainnetExitRoot Type",
			data: []any{
				[types.DefaultHeight][common.HashLength]byte{},
				globalIndex,
				invalidTypePlaceholder, // Invalid MainnetExitRoot type
				[common.HashLength]byte(rollupExitRoot.Bytes()),
				uint32(1),
				common.Address{},
				destinationNetwork,
				common.Address{},
				big.NewInt(0),
				metadata,
			},
			expectedIsDecoded: false,
			expectError:       true,
		},
		{
			name: "Invalid RollupExitRoot Type",
			data: []any{
				[types.DefaultHeight][common.HashLength]byte{},
				globalIndex,
				[common.HashLength]byte(mainnetExitRoot.Bytes()),
				invalidTypePlaceholder, // Invalid RollupExitRoot type
				uint32(1),
				common.Address{},
				destinationNetwork,
				common.Address{},
				big.NewInt(0),
				metadata,
			},
			expectedIsDecoded: false,
			expectError:       true,
		},
		{
			name: "Invalid DestinationNetwork Type",
			data: []any{
				[types.DefaultHeight][common.HashLength]byte{},
				globalIndex,
				[common.HashLength]byte(mainnetExitRoot.Bytes()),
				[common.HashLength]byte(rollupExitRoot.Bytes()),
				uint32(1),
				common.Address{},
				invalidTypePlaceholder, // Invalid DestinationNetwork type
				common.Address{},
				big.NewInt(0),
				metadata,
			},
			expectedIsDecoded: false,
			expectError:       true,
		},
		{
			name: "Invalid Metadata Type",
			data: []any{
				[types.DefaultHeight][common.HashLength]byte{},
				globalIndex,
				[common.HashLength]byte(mainnetExitRoot.Bytes()),
				[common.HashLength]byte(rollupExitRoot.Bytes()),
				uint32(1),
				common.Address{},
				destinationNetwork,
				common.Address{},
				big.NewInt(0),
				123, // Invalid metadata type (should be []byte)
			},
			expectedIsDecoded: false,
			expectError:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claim := &Claim{
				GlobalIndex:        new(big.Int).SetUint64(uint64(globalIndex)),
				MainnetExitRoot:    common.Hash{},
				RollupExitRoot:     common.Hash{},
				DestinationNetwork: 0,
				Metadata:           nil,
			}

			match, err := claim.decodePreEtrogCalldata(tt.data)

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, tt.expectedIsDecoded, match)
		})
	}
}

func TestDecodeEtrogCalldata(t *testing.T) {
	var (
		globalIndex            = big.NewInt(12345)
		mainnetExitRoot        = common.HexToHash("0x11")
		rollupExitRoot         = common.HexToHash("0x22")
		metadata               = []byte("mock metadata")
		destinationNetwork     = uint32(1)
		invalidTypePlaceholder = "invalidType"
	)

	tests := []struct {
		name              string
		data              []any
		expectedIsDecoded bool
		expectError       bool
	}{
		{
			name: "Valid calldata",
			data: []any{
				[types.DefaultHeight][common.HashLength]byte{}, // ProofLocalExitRoot
				[types.DefaultHeight][common.HashLength]byte{}, // ProofRollupExitRoot
				globalIndex, // GlobalIndex
				[common.HashLength]byte(mainnetExitRoot.Bytes()), // MainnetExitRoot
				[common.HashLength]byte(rollupExitRoot.Bytes()),  // RollupExitRoot
				uint32(0),          // OriginNetwork (not used)
				common.Address{},   // OriginAddress (not used)
				destinationNetwork, // DestinationNetwork
				common.Address{},   // DestinationAddress (not used)
				big.NewInt(0),      // Amount (not used)
				metadata,           // Metadata
			},
			expectedIsDecoded: true,
			expectError:       false,
		},
		{
			name: "Mismatched GlobalIndex",
			data: []any{
				[types.DefaultHeight][common.HashLength]byte{}, // ProofLocalExitRoot
				[types.DefaultHeight][common.HashLength]byte{}, // ProofRollupExitRoot
				big.NewInt(99999), // Wrong GlobalIndex
				[common.HashLength]byte(mainnetExitRoot.Bytes()),
				[common.HashLength]byte(rollupExitRoot.Bytes()),
				uint32(0),
				common.Address{},
				destinationNetwork,
				common.Address{},
				big.NewInt(0),
				metadata,
			},
			expectedIsDecoded: false,
			expectError:       false, // No error, just a mismatch
		},
		{
			name: "Invalid GlobalIndex Type",
			data: []any{
				[types.DefaultHeight][common.HashLength]byte{},
				[types.DefaultHeight][common.HashLength]byte{},
				invalidTypePlaceholder, // Invalid GlobalIndex type
				mainnetExitRoot.Bytes(),
				rollupExitRoot.Bytes(),
				uint32(0),
				common.Address{},
				destinationNetwork,
				common.Address{},
				big.NewInt(0),
				metadata,
			},
			expectedIsDecoded: false,
			expectError:       true,
		},
		{
			name: "Invalid LocalExitRoot Proof Type",
			data: []any{
				invalidTypePlaceholder, // Invalid ProofLocalExitRoot type
				[types.DefaultHeight][common.HashLength]byte{},
				globalIndex,
				[common.HashLength]byte(mainnetExitRoot.Bytes()),
				[common.HashLength]byte(rollupExitRoot.Bytes()),
				uint32(0),
				common.Address{},
				destinationNetwork,
				common.Address{},
				big.NewInt(0),
				metadata,
			},
			expectedIsDecoded: false,
			expectError:       true,
		},
		{
			name: "Invalid RollupExitRoot Proof Type",
			data: []any{
				[types.DefaultHeight][common.HashLength]byte{},
				invalidTypePlaceholder, // Invalid RollupExitRoot proof type
				globalIndex,
				[common.HashLength]byte(mainnetExitRoot.Bytes()),
				[common.HashLength]byte(rollupExitRoot.Bytes()),
				uint32(0),
				common.Address{},
				destinationNetwork,
				common.Address{},
				big.NewInt(0),
				metadata,
			},
			expectedIsDecoded: false,
			expectError:       true,
		},
		{
			name: "Invalid MainnetExitRoot Type",
			data: []any{
				[types.DefaultHeight][common.HashLength]byte{}, // ProofLocalExitRoot
				[types.DefaultHeight][common.HashLength]byte{}, // ProofRollupExitRoot
				globalIndex,            // GlobalIndex
				invalidTypePlaceholder, // MainnetExitRoot
				[common.HashLength]byte(rollupExitRoot.Bytes()), // RollupExitRoot
				uint32(0),          // OriginNetwork (not used)
				common.Address{},   // OriginAddress (not used)
				destinationNetwork, // DestinationNetwork
				common.Address{},   // DestinationAddress (not used)
				big.NewInt(0),      // Amount (not used)
				metadata,           // Metadata
			},
			expectedIsDecoded: false,
			expectError:       true,
		},
		{
			name: "Invalid RollupExitRoot Type",
			data: []any{
				[types.DefaultHeight][common.HashLength]byte{}, // ProofLocalExitRoot
				[types.DefaultHeight][common.HashLength]byte{}, // ProofRollupExitRoot
				globalIndex, // GlobalIndex
				[common.HashLength]byte(mainnetExitRoot.Bytes()), // MainnetExitRoot
				invalidTypePlaceholder,                           // RollupExitRoot
				uint32(0),                                        // OriginNetwork (not used)
				common.Address{},                                 // OriginAddress (not used)
				destinationNetwork,                               // DestinationNetwork
				common.Address{},                                 // DestinationAddress (not used)
				big.NewInt(0),                                    // Amount (not used)
				metadata,                                         // Metadata
			},
			expectedIsDecoded: false,
			expectError:       true,
		},
		{
			name: "Invalid DestinationNetwork Type",
			data: []any{
				[types.DefaultHeight][common.HashLength]byte{}, // ProofLocalExitRoot
				[types.DefaultHeight][common.HashLength]byte{}, // ProofRollupExitRoot
				globalIndex, // GlobalIndex
				[common.HashLength]byte(mainnetExitRoot.Bytes()), // MainnetExitRoot
				[common.HashLength]byte(rollupExitRoot.Bytes()),  // RollupExitRoot
				uint32(0),              // OriginNetwork (not used)
				common.Address{},       // OriginAddress (not used)
				invalidTypePlaceholder, // DestinationNetwork
				common.Address{},       // DestinationAddress (not used)
				big.NewInt(0),          // Amount (not used)
				metadata,               // Metadata
			},
			expectedIsDecoded: false,
			expectError:       true,
		},
		{
			name: "Invalid Metadata Type",
			data: []any{
				[types.DefaultHeight][common.HashLength]byte{},
				[types.DefaultHeight][common.HashLength]byte{},
				globalIndex,
				[common.HashLength]byte(mainnetExitRoot.Bytes()),
				[common.HashLength]byte(rollupExitRoot.Bytes()),
				uint32(0),
				common.Address{},
				destinationNetwork,
				common.Address{},
				big.NewInt(0),
				123, // Invalid metadata type (should be []byte)
			},
			expectedIsDecoded: false,
			expectError:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claim := &Claim{GlobalIndex: globalIndex}

			isDecoded, err := claim.decodeEtrogCalldata(tt.data)
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, tt.expectedIsDecoded, isDecoded)
		})
	}
}

func TestQueryBlockRangeOrdering(t *testing.T) {
	path := path.Join(t.TempDir(), "bridgeSyncerProcessorOrdering.db")
	logger := log.WithFields("module", "bridge-syncer")
	p, err := newProcessor(path, "bridge-syncer", logger, dbQueryTimeout)
	require.NoError(t, err)

	// Create test data with events in different blocks and positions
	events := []Event{
		{
			Bridge: &Bridge{
				BlockNum:     1,
				BlockPos:     0,
				DepositCount: 0,
			},
		},
		{
			Bridge: &Bridge{
				BlockNum:     1,
				BlockPos:     1,
				DepositCount: 1,
			},
		},
		{
			Bridge: &Bridge{
				BlockNum:     1,
				BlockPos:     2,
				DepositCount: 2,
			},
		},
		{
			Bridge: &Bridge{
				BlockNum:     2,
				BlockPos:     0,
				DepositCount: 3,
			},
		},
	}

	// Process blocks with events
	block1 := sync.Block{
		Num:    1,
		Hash:   common.HexToHash("0x1"),
		Events: []any{events[0], events[1], events[2]},
	}
	block2 := sync.Block{
		Num:    2,
		Hash:   common.HexToHash("0x2"),
		Events: []any{events[3]},
	}

	err = p.ProcessBlock(context.Background(), block1)
	require.NoError(t, err)
	err = p.ProcessBlock(context.Background(), block2)
	require.NoError(t, err)

	// Test descending order
	bridges, err := p.GetBridges(context.Background(), 1, 2)
	require.NoError(t, err)
	require.Len(t, bridges, 4)

	// Verify ordering by block_num ASC, block_pos ASC
	require.Equal(t, uint64(1), bridges[0].BlockNum)
	require.Equal(t, uint64(0), bridges[0].BlockPos)
	require.Equal(t, uint64(1), bridges[1].BlockNum)
	require.Equal(t, uint64(1), bridges[1].BlockPos)
	require.Equal(t, uint64(1), bridges[2].BlockNum)
	require.Equal(t, uint64(2), bridges[2].BlockPos)
	require.Equal(t, uint64(2), bridges[3].BlockNum)
	require.Equal(t, uint64(0), bridges[3].BlockPos)
}

func TestBridgeSyncRuntimeData_IsCompatible(t *testing.T) {
	tests := []struct {
		name        string
		current     BridgeSyncRuntimeData
		storage     BridgeSyncRuntimeData
		expectError bool
		errorMsg    string
	}{
		{
			name: "compatible versions",
			current: BridgeSyncRuntimeData{
				ChainID:           1,
				Addresses:         []common.Address{common.HexToAddress("0x123")},
				DBVersion:         intPtr(1),
				SyncFromInBridges: boolPtr(true),
			},
			storage: BridgeSyncRuntimeData{
				ChainID:   1,
				Addresses: []common.Address{common.HexToAddress("0x123")},
				DBVersion: intPtr(1),
			},
			expectError: false,
		},
		{
			name: "incompatible versions - different DB versions",
			current: BridgeSyncRuntimeData{
				ChainID:           1,
				Addresses:         []common.Address{common.HexToAddress("0x123")},
				DBVersion:         intPtr(2),
				SyncFromInBridges: boolPtr(true),
			},
			storage: BridgeSyncRuntimeData{
				ChainID:   1,
				Addresses: []common.Address{common.HexToAddress("0x123")},
				DBVersion: intPtr(1),
			},
			expectError: true,
			errorMsg:    "database schema version mismatch",
		},
		{
			name: "incompatible versions - different chain IDs",
			current: BridgeSyncRuntimeData{
				ChainID:   1,
				Addresses: []common.Address{common.HexToAddress("0x123")},
				DBVersion: intPtr(1),
			},
			storage: BridgeSyncRuntimeData{
				ChainID:   2,
				Addresses: []common.Address{common.HexToAddress("0x123")},
				DBVersion: intPtr(1),
			},
			expectError: true,
			errorMsg:    "chain ID mismatch: 1 != 2",
		},
		{
			name: "incompatible versions - different addresses",
			current: BridgeSyncRuntimeData{
				ChainID:           1,
				Addresses:         []common.Address{common.HexToAddress("0x123")},
				DBVersion:         intPtr(1),
				SyncFromInBridges: boolPtr(true),
			},
			storage: BridgeSyncRuntimeData{
				ChainID:   1,
				Addresses: []common.Address{common.HexToAddress("0x456")},
				DBVersion: intPtr(1),
			},
			expectError: true,
			errorMsg:    "addresses[0] mismatch: 0x0000000000000000000000000000000000000123 != 0x0000000000000000000000000000000000000456",
		},
		{
			name: "storage no flag SyncFromInBridges -> false: ok",
			current: BridgeSyncRuntimeData{
				ChainID:           1,
				Addresses:         []common.Address{common.HexToAddress("0x123")},
				DBVersion:         intPtr(1),
				SyncFromInBridges: boolPtr(false),
			},
			storage: BridgeSyncRuntimeData{
				ChainID:           1,
				Addresses:         []common.Address{common.HexToAddress("0x123")},
				DBVersion:         intPtr(1),
				SyncFromInBridges: nil, // Previous DB to this version
			},
			expectError: false,
		},
		{
			name: "storage no flag SyncFromInBridges -> true: ok",
			current: BridgeSyncRuntimeData{
				ChainID:           1,
				Addresses:         []common.Address{common.HexToAddress("0x123")},
				DBVersion:         intPtr(1),
				SyncFromInBridges: boolPtr(true),
			},
			storage: BridgeSyncRuntimeData{
				ChainID:           1,
				Addresses:         []common.Address{common.HexToAddress("0x123")},
				DBVersion:         intPtr(1),
				SyncFromInBridges: nil, // Previous DB to this version
			},
			expectError: false,
		},
		{
			name: "storage SyncFromInBridges false -> true: ok",
			current: BridgeSyncRuntimeData{
				ChainID:           1,
				Addresses:         []common.Address{common.HexToAddress("0x123")},
				DBVersion:         intPtr(1),
				SyncFromInBridges: boolPtr(true),
			},
			storage: BridgeSyncRuntimeData{
				ChainID:           1,
				Addresses:         []common.Address{common.HexToAddress("0x123")},
				DBVersion:         intPtr(1),
				SyncFromInBridges: boolPtr(false),
			},
			expectError: false,
		},
		{
			name: "storage SyncFromInBridges true -> false: ok",
			current: BridgeSyncRuntimeData{
				ChainID:           1,
				Addresses:         []common.Address{common.HexToAddress("0x123")},
				DBVersion:         intPtr(1),
				SyncFromInBridges: boolPtr(false),
			},
			storage: BridgeSyncRuntimeData{
				ChainID:           1,
				Addresses:         []common.Address{common.HexToAddress("0x123")},
				DBVersion:         intPtr(1),
				SyncFromInBridges: boolPtr(true),
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.current.IsCompatible(tt.storage)

			if tt.expectError {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errorMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestGetClaimByGlobalIndex(t *testing.T) {
	path := path.Join(t.TempDir(), "bridgesyncTestGetClaimByGlobalIndex.sqlite")
	logger := log.WithFields("module", "bridge-syncer")
	p, err := newProcessor(path, "bridge-syncer", logger, dbQueryTimeout)
	require.NoError(t, err)

	ctx := context.Background()

	// Test case 1: Claim not found
	t.Run("claim not found", func(t *testing.T) {
		nonExistentGlobalIndex := big.NewInt(999999)
		claims, err := p.GetClaimsByGlobalIndex(ctx, nonExistentGlobalIndex)
		require.NoError(t, err)
		require.Empty(t, claims)
	})

	// Test case 2: Insert claims and retrieve them
	globalIndexToTest := GenerateGlobalIndex(true, 0, 2000)
	testClaims := []*Claim{
		{
			BlockNum:            1,
			BlockPos:            0,
			GlobalIndex:         big.NewInt(1000),
			OriginNetwork:       1,
			OriginAddress:       common.HexToAddress("0x11"),
			DestinationAddress:  common.HexToAddress("0x22"),
			Amount:              big.NewInt(100),
			ProofLocalExitRoot:  types.Proof{},
			ProofRollupExitRoot: types.Proof{},
			MainnetExitRoot:     common.HexToHash("0xmainnet"),
			RollupExitRoot:      common.HexToHash("0xrollup"),
			GlobalExitRoot:      common.HexToHash("0xglobal"),
			DestinationNetwork:  2,
			Metadata:            []byte("test metadata 1"),
			IsMessage:           false,
		},
		{
			BlockNum:            2,
			BlockPos:            1,
			GlobalIndex:         globalIndexToTest,
			OriginNetwork:       3,
			OriginAddress:       common.HexToAddress("0x33"),
			DestinationAddress:  common.HexToAddress("0x44"),
			Amount:              big.NewInt(200),
			ProofLocalExitRoot:  types.Proof{},
			ProofRollupExitRoot: types.Proof{},
			MainnetExitRoot:     common.HexToHash("0xmainnet2"),
			RollupExitRoot:      common.HexToHash("0xrollup2"),
			GlobalExitRoot:      common.HexToHash("0xglobal2"),
			DestinationNetwork:  4,
			Metadata:            []byte("test metadata 2"),
			IsMessage:           true,
		},
		{
			BlockNum:            3,
			BlockPos:            1,
			GlobalIndex:         globalIndexToTest, // same global index as previous claim
			OriginNetwork:       3,
			OriginAddress:       common.HexToAddress("0x33"),
			DestinationAddress:  common.HexToAddress("0x55"),
			Amount:              big.NewInt(200),
			ProofLocalExitRoot:  types.Proof{},
			ProofRollupExitRoot: types.Proof{},
			MainnetExitRoot:     common.HexToHash("0xmainnet2"),
			RollupExitRoot:      common.HexToHash("0xrollup2"),
			GlobalExitRoot:      common.HexToHash("0xglobal2"),
			DestinationNetwork:  4,
			Metadata:            []byte("test metadata 2"),
			IsMessage:           true,
		},
	}

	// Insert test claims
	tx, err := p.db.BeginTx(ctx, nil)
	require.NoError(t, err)

	// Insert blocks first
	for _, claim := range testClaims {
		_, err = tx.Exec(`INSERT INTO block (num, hash) VALUES ($1, $2)`,
			claim.BlockNum, fmt.Sprintf("0x%x", claim.BlockNum))
		require.NoError(t, err)
	}

	// Insert claims
	for _, claim := range testClaims {
		require.NoError(t, meddler.Insert(tx, "claim", claim))
	}

	require.NoError(t, tx.Commit())

	// Test case 3: Retrieve existing claims by global index
	t.Run("retrieve existing claims", func(t *testing.T) {
		claims, err := p.GetClaimsByGlobalIndex(ctx, globalIndexToTest)
		require.NoError(t, err)
		require.Len(t, claims, 1)
		// no unset claim, so the claims got compacted
		require.Equal(t, testClaims[1].BlockNum, claims[0].BlockNum)
		require.Equal(t, testClaims[1].BlockPos, claims[0].BlockPos)
		require.Equal(t, testClaims[1].GlobalIndex, claims[0].GlobalIndex)
		require.Equal(t, testClaims[1].OriginAddress, claims[0].OriginAddress)
		require.Equal(t, testClaims[1].DestinationAddress, claims[0].DestinationAddress)
		require.Equal(t, testClaims[1].Amount, claims[0].Amount)
		require.Equal(t, testClaims[1].Metadata, claims[0].Metadata)
		require.Equal(t, testClaims[1].IsMessage, claims[0].IsMessage)
		require.Equal(t, testClaims[2].MainnetExitRoot, claims[0].MainnetExitRoot)
		require.Equal(t, testClaims[2].RollupExitRoot, claims[0].RollupExitRoot)
		require.Equal(t, testClaims[2].GlobalExitRoot, claims[0].GlobalExitRoot)
		require.Equal(t, testClaims[2].ProofLocalExitRoot, claims[0].ProofLocalExitRoot)
		require.Equal(t, testClaims[2].ProofRollupExitRoot, claims[0].ProofRollupExitRoot)
	})

	// Test case 4: Test with very large global index
	t.Run("large global index", func(t *testing.T) {
		// Create a very large global index
		largeGlobalIndex := new(big.Int)
		largeGlobalIndex.SetString("340282366920938463463374607431768211455", 10) // 2^128 - 1

		largeClaim := &Claim{
			BlockNum:            4,
			BlockPos:            0,
			GlobalIndex:         largeGlobalIndex,
			OriginNetwork:       7,
			OriginAddress:       common.HexToAddress("0x77"),
			DestinationAddress:  common.HexToAddress("0x88"),
			Amount:              big.NewInt(400),
			ProofLocalExitRoot:  types.Proof{},
			ProofRollupExitRoot: types.Proof{},
			MainnetExitRoot:     common.HexToHash("0xmainnet4"),
			RollupExitRoot:      common.HexToHash("0xrollup4"),
			GlobalExitRoot:      common.HexToHash("0xglobal4"),
			DestinationNetwork:  8,
			Metadata:            []byte("large index test"),
			IsMessage:           false,
		}

		// Insert block and claim
		tx, err := p.db.BeginTx(ctx, nil)
		require.NoError(t, err)

		_, err = tx.Exec(`INSERT INTO block (num, hash) VALUES ($1, $2)`,
			largeClaim.BlockNum, fmt.Sprintf("0x%x", largeClaim.BlockNum))
		require.NoError(t, err)

		require.NoError(t, meddler.Insert(tx, "claim", largeClaim))
		require.NoError(t, tx.Commit())

		// Retrieve the claim
		retrievedClaims, err := p.GetClaimsByGlobalIndex(ctx, largeGlobalIndex)
		require.NoError(t, err)
		require.Len(t, retrievedClaims, 1) // Should return one claim
		require.Equal(t, *largeClaim, retrievedClaims[0])
	})

	// Test case 5: Test with zero global index
	t.Run("zero global index", func(t *testing.T) {
		zeroGlobalIndex := big.NewInt(0)

		zeroClaim := &Claim{
			BlockNum:            5,
			BlockPos:            0,
			GlobalIndex:         zeroGlobalIndex,
			OriginNetwork:       9,
			OriginAddress:       common.HexToAddress("0x99"),
			DestinationAddress:  common.HexToAddress("0xaa"),
			Amount:              big.NewInt(0),
			ProofLocalExitRoot:  types.Proof{},
			ProofRollupExitRoot: types.Proof{},
			MainnetExitRoot:     common.Hash{},
			RollupExitRoot:      common.Hash{},
			GlobalExitRoot:      common.Hash{},
			DestinationNetwork:  10,
			Metadata:            []byte{},
			IsMessage:           true,
		}

		// Insert block and claim
		tx, err := p.db.BeginTx(ctx, nil)
		require.NoError(t, err)

		_, err = tx.Exec(`INSERT INTO block (num, hash) VALUES ($1, $2)`,
			zeroClaim.BlockNum, fmt.Sprintf("0x%x", zeroClaim.BlockNum))
		require.NoError(t, err)

		require.NoError(t, meddler.Insert(tx, "claim", zeroClaim))
		require.NoError(t, tx.Commit())

		// Retrieve the claim
		retrievedClaims, err := p.GetClaimsByGlobalIndex(ctx, zeroGlobalIndex)
		require.NoError(t, err)
		require.Len(t, retrievedClaims, 1) // Should return one claim
		require.Equal(t, *zeroClaim, retrievedClaims[0])
	})

	// Test case 6: Test with nil global index (should handle gracefully)
	t.Run("nil global index", func(t *testing.T) {
		claims, err := p.GetClaimsByGlobalIndex(ctx, nil)
		require.ErrorContains(t, err, "global index parameter cannot be nil")
		require.Empty(t, claims)
	})

	// Test case 7: db returns error
	t.Run("db error", func(t *testing.T) {
		p.db.Close() // Close the processor's DB to simulate an error

		// Attempt to retrieve claims with the invalid processor
		claims, err := p.GetClaimsByGlobalIndex(ctx, globalIndexToTest)
		require.Error(t, err)
		require.Empty(t, claims)
	})
}

// TestGetClaimsByGlobalIndex_Compact tests the compaction behavior of GetClaimsByGlobalIndex
// It mirrors the test cases from TestGetClaims_Compact to ensure consistent behavior
//
//nolint:dupl
func TestGetClaimsByGlobalIndex_Compact(t *testing.T) {
	logger := log.WithFields("module", "bridge-syncer")
	ctx := context.Background()

	// Define test claims used across test cases
	oldProof := types.Proof{}
	oldProof[0] = common.HexToHash("0x01")

	testCases := []struct {
		name            string
		globalIndex     *big.Int
		setupBlocks     func() []sync.Block
		expectedCount   int
		validateResults func(t *testing.T, claims []Claim)
	}{
		{
			name:        "Case 1: don't compact if unset_claim exists for global_index",
			globalIndex: big.NewInt(100),
			setupBlocks: func() []sync.Block {
				return []sync.Block{
					{
						Num:  1,
						Hash: common.HexToHash("0x1"),
						Events: []any{
							Event{Claim: &Claim{
								BlockNum:            1,
								BlockPos:            0,
								TxHash:              common.HexToHash("0x111"),
								GlobalIndex:         big.NewInt(100),
								OriginNetwork:       1,
								OriginAddress:       common.HexToAddress("0xaaa"),
								DestinationAddress:  common.HexToAddress("0xbbb"),
								Amount:              big.NewInt(100),
								ProofLocalExitRoot:  types.Proof{common.HexToHash("0x1a")},
								ProofRollupExitRoot: types.Proof{common.HexToHash("0x1b")},
								MainnetExitRoot:     common.HexToHash("0x1c"),
								RollupExitRoot:      common.HexToHash("0x1d"),
								GlobalExitRoot:      common.HexToHash("0x1e"),
								DestinationNetwork:  2,
								Metadata:            []byte("original_metadata"),
								IsMessage:           false,
								BlockTimestamp:      1000,
							}},
						},
					},
					{
						Num:  2,
						Hash: common.HexToHash("0x2"),
						Events: []any{
							Event{UnsetClaim: &UnsetClaim{
								GlobalIndex: big.NewInt(100),
								BlockNum:    2,
								BlockPos:    0,
								TxHash:      common.Hash{},
							}},
						},
					},
					{
						Num:  3,
						Hash: common.HexToHash("0x3"),
						Events: []any{
							Event{Claim: &Claim{
								BlockNum:            3,
								BlockPos:            0,
								TxHash:              common.HexToHash("0x333"),
								GlobalIndex:         big.NewInt(100),
								OriginNetwork:       77,
								OriginAddress:       common.HexToAddress("0x999"),
								DestinationAddress:  common.HexToAddress("0x888"),
								Amount:              big.NewInt(777),
								ProofLocalExitRoot:  types.Proof{common.HexToHash("0x3a")},
								ProofRollupExitRoot: types.Proof{common.HexToHash("0x3b")},
								MainnetExitRoot:     common.HexToHash("0x3c"),
								RollupExitRoot:      common.HexToHash("0x3d"),
								GlobalExitRoot:      common.HexToHash("0x3e"),
								DestinationNetwork:  66,
								Metadata:            []byte("newest_metadata"),
								IsMessage:           true,
								BlockTimestamp:      3000,
							}},
						},
					},
				}
			},
			expectedCount: 2, // Should return all claims without compacting because unset_claim exists
			validateResults: func(t *testing.T, claims []Claim) {
				t.Helper()
				require.Len(t, claims, 2, "should not compact when unset claim exists")
				// Claims should be ordered by block_num ASC
				require.Equal(t, uint64(1), claims[0].BlockNum)
				require.Equal(t, []byte("original_metadata"), claims[0].Metadata)
				require.Equal(t, uint64(3), claims[1].BlockNum)
				require.Equal(t, []byte("newest_metadata"), claims[1].Metadata)
			},
		},
		{
			name:        "Case 2: compact if no unset_claim exists",
			globalIndex: big.NewInt(200),
			setupBlocks: func() []sync.Block {
				return []sync.Block{
					{
						Num:  1,
						Hash: common.HexToHash("0x1"),
						Events: []any{
							Event{Claim: &Claim{
								BlockNum:            1,
								BlockPos:            0,
								TxHash:              common.HexToHash("0x111"),
								GlobalIndex:         big.NewInt(200),
								OriginNetwork:       1,
								OriginAddress:       common.HexToAddress("0xaaa"),
								DestinationAddress:  common.HexToAddress("0xbbb"),
								Amount:              big.NewInt(100),
								ProofLocalExitRoot:  types.Proof{common.HexToHash("0x1a")},
								ProofRollupExitRoot: types.Proof{common.HexToHash("0x1b")},
								MainnetExitRoot:     common.HexToHash("0x1c"),
								RollupExitRoot:      common.HexToHash("0x1d"),
								GlobalExitRoot:      common.HexToHash("0x1e"),
								DestinationNetwork:  2,
								Metadata:            []byte("original_metadata"),
								IsMessage:           false,
								BlockTimestamp:      1000,
							}},
						},
					},
					{
						Num:  2,
						Hash: common.HexToHash("0x2"),
						Events: []any{
							Event{Claim: &Claim{
								BlockNum:            2,
								BlockPos:            0,
								TxHash:              common.HexToHash("0x222"),
								GlobalIndex:         big.NewInt(200),
								OriginNetwork:       99,
								OriginAddress:       common.HexToAddress("0xfff"),
								DestinationAddress:  common.HexToAddress("0xeee"),
								Amount:              big.NewInt(999),
								ProofLocalExitRoot:  types.Proof{common.HexToHash("0x2a")},
								ProofRollupExitRoot: types.Proof{common.HexToHash("0x2b")},
								MainnetExitRoot:     common.HexToHash("0x2c"),
								RollupExitRoot:      common.HexToHash("0x2d"),
								GlobalExitRoot:      common.HexToHash("0x2e"),
								DestinationNetwork:  88,
								Metadata:            []byte("middle_metadata"),
								IsMessage:           true,
								BlockTimestamp:      2000,
							}},
						},
					},
					{
						Num:  3,
						Hash: common.HexToHash("0x3"),
						Events: []any{
							Event{Claim: &Claim{
								BlockNum:            3,
								BlockPos:            0,
								TxHash:              common.HexToHash("0x333"),
								GlobalIndex:         big.NewInt(200),
								OriginNetwork:       77,
								OriginAddress:       common.HexToAddress("0x999"),
								DestinationAddress:  common.HexToAddress("0x888"),
								Amount:              big.NewInt(777),
								ProofLocalExitRoot:  types.Proof{common.HexToHash("0x3a")},
								ProofRollupExitRoot: types.Proof{common.HexToHash("0x3b")},
								MainnetExitRoot:     common.HexToHash("0x3c"),
								RollupExitRoot:      common.HexToHash("0x3d"),
								GlobalExitRoot:      common.HexToHash("0x3e"),
								DestinationNetwork:  66,
								Metadata:            []byte("newest_metadata"),
								IsMessage:           true,
								BlockTimestamp:      3000,
							}},
						},
					},
				}
			},
			expectedCount: 1, // Should return 1 compacted claim
			validateResults: func(t *testing.T, claims []Claim) {
				t.Helper()
				require.Len(t, claims, 1, "should compact when no unset claim exists")
				claim := claims[0]
				require.Equal(t, big.NewInt(200), claim.GlobalIndex)
				// Metadata from oldest (block 1)
				require.Equal(t, uint64(1), claim.BlockNum, "should preserve oldest block")
				require.Equal(t, uint64(0), claim.BlockPos, "should preserve oldest position")
				require.Equal(t, []byte("original_metadata"), claim.Metadata, "should preserve oldest metadata")
				require.Equal(t, big.NewInt(100), claim.Amount, "should preserve oldest amount")
				require.Equal(t, uint32(1), claim.OriginNetwork, "should preserve oldest origin network")
				// Proofs from newest (block 3)
				require.Equal(t, common.HexToHash("0x3a"), claim.ProofLocalExitRoot[0], "should use newest proof")
				require.Equal(t, common.HexToHash("0x3c"), claim.MainnetExitRoot, "should use newest MainnetExitRoot")
				require.Equal(t, common.HexToHash("0x3d"), claim.RollupExitRoot, "should use newest RollupExitRoot")
				require.Equal(t, common.HexToHash("0x3e"), claim.GlobalExitRoot, "should use newest GlobalExitRoot")
			},
		},
		{
			name:        "Single claim - no compaction needed",
			globalIndex: big.NewInt(300),
			setupBlocks: func() []sync.Block {
				return []sync.Block{
					{
						Num:  1,
						Hash: common.HexToHash("0x1"),
						Events: []any{
							Event{Claim: &Claim{
								BlockNum:            1,
								BlockPos:            0,
								TxHash:              common.HexToHash("0x111"),
								GlobalIndex:         big.NewInt(300),
								OriginNetwork:       5,
								OriginAddress:       common.HexToAddress("0x555"),
								DestinationAddress:  common.HexToAddress("0x666"),
								Amount:              big.NewInt(500),
								ProofLocalExitRoot:  oldProof,
								ProofRollupExitRoot: oldProof,
								MainnetExitRoot:     common.HexToHash("0xaaa"),
								RollupExitRoot:      common.HexToHash("0xbbb"),
								GlobalExitRoot:      common.HexToHash("0xccc"),
								DestinationNetwork:  6,
								Metadata:            []byte("single_claim"),
								IsMessage:           false,
								BlockTimestamp:      1000,
							}},
						},
					},
				}
			},
			expectedCount: 1,
			validateResults: func(t *testing.T, claims []Claim) {
				t.Helper()
				require.Len(t, claims, 1)
				require.Equal(t, big.NewInt(300), claims[0].GlobalIndex)
				require.Equal(t, []byte("single_claim"), claims[0].Metadata)
			},
		},
		{
			name:        "Non-existent global index",
			globalIndex: big.NewInt(999999),
			setupBlocks: func() []sync.Block {
				return []sync.Block{
					{
						Num:  1,
						Hash: common.HexToHash("0x1"),
						Events: []any{
							Event{Claim: &Claim{
								BlockNum:            1,
								BlockPos:            0,
								TxHash:              common.HexToHash("0x111"),
								GlobalIndex:         big.NewInt(400),
								OriginNetwork:       1,
								OriginAddress:       common.HexToAddress("0xaaa"),
								DestinationAddress:  common.HexToAddress("0xbbb"),
								Amount:              big.NewInt(100),
								ProofLocalExitRoot:  oldProof,
								ProofRollupExitRoot: oldProof,
								MainnetExitRoot:     common.HexToHash("0x1c"),
								RollupExitRoot:      common.HexToHash("0x1d"),
								GlobalExitRoot:      common.HexToHash("0x1e"),
								DestinationNetwork:  2,
								Metadata:            []byte("different_index"),
								IsMessage:           false,
								BlockTimestamp:      1000,
							}},
						},
					},
				}
			},
			expectedCount: 0,
			validateResults: func(t *testing.T, claims []Claim) {
				t.Helper()
				require.Empty(t, claims, "should return empty for non-existent global index")
			},
		},
		{
			name:        "Multiple claims same block - compact using position",
			globalIndex: big.NewInt(500),
			setupBlocks: func() []sync.Block {
				return []sync.Block{
					{
						Num:  1,
						Hash: common.HexToHash("0x1"),
						Events: []any{
							Event{Claim: &Claim{
								BlockNum:            1,
								BlockPos:            0,
								TxHash:              common.HexToHash("0x111"),
								GlobalIndex:         big.NewInt(500),
								OriginNetwork:       1,
								OriginAddress:       common.HexToAddress("0xaaa"),
								DestinationAddress:  common.HexToAddress("0xbbb"),
								Amount:              big.NewInt(100),
								ProofLocalExitRoot:  types.Proof{common.HexToHash("0x1a")},
								ProofRollupExitRoot: types.Proof{common.HexToHash("0x1b")},
								MainnetExitRoot:     common.HexToHash("0x1c"),
								RollupExitRoot:      common.HexToHash("0x1d"),
								GlobalExitRoot:      common.HexToHash("0x1e"),
								DestinationNetwork:  2,
								Metadata:            []byte("pos_0_metadata"),
								IsMessage:           false,
								BlockTimestamp:      1000,
							}},
							Event{Claim: &Claim{
								BlockNum:            1,
								BlockPos:            1,
								TxHash:              common.HexToHash("0x112"),
								GlobalIndex:         big.NewInt(500),
								OriginNetwork:       1,
								OriginAddress:       common.HexToAddress("0xaaa"),
								DestinationAddress:  common.HexToAddress("0xbbb"),
								Amount:              big.NewInt(100),
								ProofLocalExitRoot:  types.Proof{common.HexToHash("0x2a")},
								ProofRollupExitRoot: types.Proof{common.HexToHash("0x2b")},
								MainnetExitRoot:     common.HexToHash("0x2c"),
								RollupExitRoot:      common.HexToHash("0x2d"),
								GlobalExitRoot:      common.HexToHash("0x2e"),
								DestinationNetwork:  2,
								Metadata:            []byte("pos_1_metadata"),
								IsMessage:           false,
								BlockTimestamp:      1000,
							}},
						},
					},
				}
			},
			expectedCount: 1,
			validateResults: func(t *testing.T, claims []Claim) {
				t.Helper()
				require.Len(t, claims, 1, "should compact multiple claims in same block")
				claim := claims[0]
				require.Equal(t, uint64(1), claim.BlockNum)
				require.Equal(t, uint64(0), claim.BlockPos, "should use oldest position")
				require.Equal(t, []byte("pos_0_metadata"), claim.Metadata, "should use oldest metadata")
				require.Equal(t, common.HexToHash("0x2a"), claim.ProofLocalExitRoot[0], "should use newest proof")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a fresh database for each test case
			dbPath := filepath.Join(t.TempDir(), "testcase.sqlite")
			require.NoError(t, migrations.RunMigrations(dbPath))
			testP, err := newProcessor(dbPath, "bridge-syncer", logger, dbQueryTimeout)
			require.NoError(t, err)

			// Setup blocks
			blocks := tc.setupBlocks()
			for _, block := range blocks {
				require.NoError(t, testP.ProcessBlock(ctx, block))
			}

			// Execute test
			claims, err := testP.GetClaimsByGlobalIndex(ctx, tc.globalIndex)
			require.NoError(t, err)

			// Validate results
			require.Len(t, claims, tc.expectedCount)
			if tc.validateResults != nil {
				tc.validateResults(t, claims)
			}
		})
	}
}

func intPtr(i int) *int {
	return &i
}

func uint64Ptr(i uint64) *uint64 {
	return &i
}

func boolPtr(b bool) *bool {
	return &b
}

func TestProcessor_ErrorPathLogging(t *testing.T) {
	t.Parallel()

	t.Run("GetBridgesPaged error paths", func(t *testing.T) {
		t.Parallel()
		p := createTestProcessor(t, "GetBridgesPagedErrorPaths")

		testBlock := sync.Block{
			Num:  1,
			Hash: common.HexToHash("0x1"),
			Events: []any{
				Event{Bridge: createTestBridge(1, 0)},
			},
		}
		require.NoError(t, p.ProcessBlock(context.Background(), testBlock))

		// Test invalid page number (page 10 with only 1 record and page size 5)
		_, _, err := p.GetBridgesPaged(context.Background(), 10, 5, nil, nil, "")
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid page number")

		// Test successful case with valid page
		bridges, count, err := p.GetBridgesPaged(context.Background(), 1, 5, nil, nil, "")
		require.NoError(t, err)
		require.Len(t, bridges, 1)
		require.Equal(t, 1, count)
	})

	t.Run("GetClaimsPaged error paths", func(t *testing.T) {
		t.Parallel()
		p := createTestProcessor(t, "GetClaimsPagedErrorPaths")

		testBlock := sync.Block{
			Num:  1,
			Hash: common.HexToHash("0x1"),
			Events: []any{
				Event{Claim: &Claim{
					BlockNum:            1,
					BlockPos:            0,
					BlockTimestamp:      1234567890,
					TxHash:              common.HexToHash("0x1234567890123456789012345678901234567890123456789012345678901234"),
					GlobalIndex:         big.NewInt(1000000000000000000),
					OriginNetwork:       1,
					OriginAddress:       common.HexToAddress("0x1234567890123456789012345678901234567890"),
					DestinationAddress:  common.HexToAddress("0x1234567890123456789012345678901234567890"),
					Amount:              big.NewInt(1000000000000000000),
					ProofLocalExitRoot:  [common.HashLength]common.Hash{},
					ProofRollupExitRoot: [common.HashLength]common.Hash{},
					MainnetExitRoot:     common.HexToHash("0x1234567890123456789012345678901234567890123456789012345678901234"),
					RollupExitRoot:      common.HexToHash("0x1234567890123456789012345678901234567890123456789012345678901234"),
					GlobalExitRoot:      common.HexToHash("0x1234567890123456789012345678901234567890123456789012345678901234"),
					DestinationNetwork:  1,
					Metadata:            []byte{},
					IsMessage:           false,
				}},
			},
		}
		require.NoError(t, p.ProcessBlock(context.Background(), testBlock))

		// Test invalid page number (page 10 with only 1 record and page size 5)
		_, _, err := p.GetClaimsPaged(context.Background(), 10, 5, nil, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid page number")

		// Test successful case with valid page
		claims, count, err := p.GetClaimsPaged(context.Background(), 1, 5, nil, nil)
		require.NoError(t, err)
		require.Len(t, claims, 1)
		require.Equal(t, 1, count)
	})

	t.Run("GetLegacyTokenMigrations error paths", func(t *testing.T) {
		t.Parallel()
		p := createTestProcessor(t, "GetLegacyTokenMigrationsErrorPaths")

		testBlock := sync.Block{
			Num:  1,
			Hash: common.HexToHash("0x1"),
			Events: []any{
				Event{LegacyTokenMigration: &LegacyTokenMigration{
					BlockNum:            1,
					BlockPos:            0,
					BlockTimestamp:      1234567890,
					TxHash:              common.HexToHash("0x1234567890123456789012345678901234567890123456789012345678901234"),
					Sender:              common.HexToAddress("0x1234567890123456789012345678901234567890"),
					LegacyTokenAddress:  common.HexToAddress("0x1234567890123456789012345678901234567890"),
					UpdatedTokenAddress: common.HexToAddress("0x1234567890123456789012345678901234567890"),
					Amount:              big.NewInt(1000000000000000000),
				}},
			},
		}
		require.NoError(t, p.ProcessBlock(context.Background(), testBlock))

		// Test invalid page number (page 10 with only 1 record and page size 5)
		_, _, err := p.GetLegacyTokenMigrations(context.Background(), 10, 5)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid page number")

		// Test successful case with valid page
		migrations, count, err := p.GetLegacyTokenMigrations(context.Background(), 1, 5)
		require.NoError(t, err)
		require.Len(t, migrations, 1)
		require.Equal(t, 1, count)
	})

	t.Run("GetTokenMappings error paths", func(t *testing.T) {
		t.Parallel()
		p := createTestProcessor(t, "GetTokenMappingsErrorPaths")

		testBlock := sync.Block{
			Num:  1,
			Hash: common.HexToHash("0x1"),
			Events: []any{
				Event{TokenMapping: createTestTokenMapping(1, 0)},
			},
		}
		require.NoError(t, p.ProcessBlock(context.Background(), testBlock))

		// Test invalid page number (page 10 with only 1 record and page size 5)
		_, _, err := p.GetTokenMappings(context.Background(), 10, 5, "")
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid page number")

		// Test successful case with valid page
		mappings, count, err := p.GetTokenMappings(context.Background(), 1, 5, "")
		require.NoError(t, err)
		require.Len(t, mappings, 1)
		require.Equal(t, 1, count)
	})
}

func TestProcessor_DatabaseConnectionErrors(t *testing.T) {
	t.Parallel()

	t.Run("GetTotalNumberOfRecords with invalid table name", func(t *testing.T) {
		t.Parallel()
		p := createTestProcessor(t, "DatabaseConnectionErrors")

		// Test with invalid table name
		_, err := p.GetTotalNumberOfRecords(context.Background(), "invalid_table_name", "")
		require.Error(t, err)
		require.Contains(t, err.Error(), "no such table")
	})

	t.Run("fetchTokenMappings with database errors", func(t *testing.T) {
		t.Parallel()
		p := createTestProcessor(t, "DatabaseConnectionErrors2")

		testBlock := sync.Block{
			Num:  1,
			Hash: common.HexToHash("0x1"),
			Events: []any{
				Event{TokenMapping: createTestTokenMapping(1, 0)},
			},
		}
		require.NoError(t, p.ProcessBlock(context.Background(), testBlock))

		// Now test with an offset that would cause a database error
		p.db.Close()
		_, err := p.fetchTokenMappings(context.Background(), 5, 0, "")
		require.Error(t, err)
	})
}

func TestProcessor_CalculateOffsetErrors(t *testing.T) {
	t.Parallel()

	t.Run("GetTokenMappings with invalid offset calculation", func(t *testing.T) {
		t.Parallel()
		p := createTestProcessor(t, "CalculateOffsetErrors")

		testBlock := sync.Block{
			Num:  1,
			Hash: common.HexToHash("0x1"),
			Events: []any{
				Event{TokenMapping: createTestTokenMapping(1, 0)},
			},
		}
		require.NoError(t, p.ProcessBlock(context.Background(), testBlock))

		// Test with page number that would result in offset >= total records
		_, _, err := p.GetTokenMappings(context.Background(), 10, 5, "") // page 10 with only 1 record and page size 5
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid page number")
	})

	t.Run("GetBridgesPaged with invalid offset calculation", func(t *testing.T) {
		t.Parallel()
		p := createTestProcessor(t, "CalculateOffsetErrors2")

		testBlock := sync.Block{
			Num:  1,
			Hash: common.HexToHash("0x1"),
			Events: []any{
				Event{Bridge: createTestBridge(1, 0)},
			},
		}
		require.NoError(t, p.ProcessBlock(context.Background(), testBlock))

		// Test with page number that would result in offset >= total records
		_, _, err := p.GetBridgesPaged(context.Background(), 10, 5, nil, nil, "") // page 10 with only 1 record and page size 5
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid page number")
	})
}

// Helper functions to reduce test redundancy

// createTestProcessor creates a new processor for testing
func createTestProcessor(t *testing.T, dbName string) *processor {
	t.Helper()

	path := path.Join(t.TempDir(), dbName+".db")
	logger := log.WithFields("module", "bridge-syncer")
	p, err := newProcessor(path, "bridge-syncer", logger, dbQueryTimeout)
	require.NoError(t, err)
	return p
}

// createTestBridge creates a test Bridge event
func createTestBridge(blockNum uint64, blockPos int) *Bridge {
	return &Bridge{
		BlockNum:       blockNum,
		BlockPos:       uint64(blockPos),
		BlockTimestamp: 1234567890,
		TxHash:         common.HexToHash("0x1234567890123456789012345678901234567890123456789012345678901234"),
		FromAddress: func() *common.Address {
			addr := common.HexToAddress("0x1234567890123456789012345678901234567890")
			return &addr
		}(),
		LeafType:           1,
		OriginNetwork:      1,
		OriginAddress:      common.HexToAddress("0x1234567890123456789012345678901234567890"),
		DestinationNetwork: 1,
		DestinationAddress: common.HexToAddress("0x1234567890123456789012345678901234567890"),
		Amount:             big.NewInt(1000000000000000000),
		Metadata:           []byte{},
		DepositCount:       0,
	}
}

// createTestTokenMapping creates a test TokenMapping event
func createTestTokenMapping(blockNum uint64, blockPos int) *TokenMapping {
	return &TokenMapping{
		BlockNum:            blockNum,
		BlockPos:            uint64(blockPos),
		BlockTimestamp:      1234567890,
		TxHash:              common.HexToHash("0x1234567890123456789012345678901234567890123456789012345678901234"),
		OriginNetwork:       1,
		OriginTokenAddress:  common.HexToAddress("0x1234567890123456789012345678901234567890"),
		WrappedTokenAddress: common.HexToAddress("0x1234567890123456789012345678901234567890"),
		Metadata:            []byte{},
		IsNotMintable:       false,
		Type:                0,
	}
}

func TestGetUnsetClaimsPaged(t *testing.T) {
	t.Parallel()

	path := path.Join(t.TempDir(), "bridgesyncGetUnsetClaimsPaged.sqlite")
	logger := log.WithFields("module", "bridge-syncer")
	p, err := newProcessor(path, "bridge-syncer", logger, dbQueryTimeout)
	require.NoError(t, err)

	// Create test unset claims
	unsetClaims := []*UnsetClaim{
		{
			BlockNum:                  1,
			BlockPos:                  0,
			TxHash:                    common.HexToHash("0x123"),
			GlobalIndex:               big.NewInt(100),
			UnsetGlobalIndexHashChain: common.HexToHash("0xabc123"),
		},
		{
			BlockNum:                  2,
			BlockPos:                  0,
			TxHash:                    common.HexToHash("0x456"),
			GlobalIndex:               big.NewInt(200),
			UnsetGlobalIndexHashChain: common.HexToHash("0xdef456"),
		},
		{
			BlockNum:                  3,
			BlockPos:                  0,
			TxHash:                    common.HexToHash("0x789"),
			GlobalIndex:               big.NewInt(100), // Same global index as first
			UnsetGlobalIndexHashChain: common.HexToHash("0x987654"),
		},
	}

	// Insert test data by processing blocks
	for i, unsetClaim := range unsetClaims {
		block := sync.Block{
			Num:  uint64(i + 1),
			Hash: common.HexToHash(fmt.Sprintf("0x%d", i+1)),
			Events: []any{
				Event{UnsetClaim: unsetClaim},
			},
		}
		require.NoError(t, p.ProcessBlock(context.Background(), block))
	}

	testCases := []struct {
		name                string
		pageSize            uint32
		page                uint32
		globalIndex         *big.Int
		expectedCount       int
		expectedUnsetClaims []*UnsetClaim
		expectedError       string
	}{
		{
			name:                "all results on first page",
			pageSize:            10,
			page:                1,
			globalIndex:         nil,
			expectedCount:       3,
			expectedUnsetClaims: []*UnsetClaim{unsetClaims[2], unsetClaims[1], unsetClaims[0]}, // DESC order
			expectedError:       "",
		},
		{
			name:                "pagination: page 2, size 1",
			pageSize:            1,
			page:                2,
			globalIndex:         nil,
			expectedCount:       3,
			expectedUnsetClaims: []*UnsetClaim{unsetClaims[1]}, // Second item in DESC order
			expectedError:       "",
		},
		{
			name:                "filter by global index",
			pageSize:            10,
			page:                1,
			globalIndex:         big.NewInt(100),
			expectedCount:       2,
			expectedUnsetClaims: []*UnsetClaim{unsetClaims[2], unsetClaims[0]}, // DESC order, filtered by globalIndex=100
			expectedError:       "",
		},
		{
			name:                "filter by non-existent global index",
			pageSize:            10,
			page:                1,
			globalIndex:         big.NewInt(999),
			expectedCount:       0,
			expectedUnsetClaims: []*UnsetClaim{},
			expectedError:       "",
		},
		{
			name:                "invalid page number",
			pageSize:            3,
			page:                5,
			globalIndex:         nil,
			expectedCount:       0,
			expectedUnsetClaims: []*UnsetClaim{},
			expectedError:       "invalid page number for given page size and total number of unset_claim",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			unsetClaims, count, err := p.GetUnsetClaimsPaged(ctx, tc.page, tc.pageSize, tc.globalIndex)

			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedUnsetClaims, unsetClaims)
				require.Equal(t, tc.expectedCount, count)
			}
		})
	}
}

func TestGetSetClaimsPaged(t *testing.T) {
	t.Parallel()

	path := path.Join(t.TempDir(), "bridgesyncGetSetClaimsPaged.sqlite")
	logger := log.WithFields("module", "bridge-syncer")
	p, err := newProcessor(path, "bridge-syncer", logger, dbQueryTimeout)
	require.NoError(t, err)

	// Create test set claims
	setClaims := []*SetClaim{
		{
			BlockNum:    1,
			BlockPos:    0,
			TxHash:      common.HexToHash("0x111"),
			GlobalIndex: big.NewInt(100),
		},
		{
			BlockNum:    2,
			BlockPos:    0,
			TxHash:      common.HexToHash("0x222"),
			GlobalIndex: big.NewInt(200),
		},
		{
			BlockNum:    3,
			BlockPos:    0,
			TxHash:      common.HexToHash("0x333"),
			GlobalIndex: big.NewInt(100), // Same global index as first
		},
		{
			BlockNum:    4,
			BlockPos:    0,
			TxHash:      common.HexToHash("0x444"),
			GlobalIndex: big.NewInt(300),
		},
	}

	// Insert test data by processing blocks
	for i, setClaim := range setClaims {
		block := sync.Block{
			Num:  uint64(i + 1),
			Hash: common.HexToHash(fmt.Sprintf("0x%d", i+1)),
			Events: []any{
				Event{SetClaim: setClaim},
			},
		}
		require.NoError(t, p.ProcessBlock(context.Background(), block))
	}

	testCases := []struct {
		name           string
		pageSize       uint32
		page           uint32
		globalIndex    *big.Int
		expectedCount  int
		expectedClaims []*SetClaim
		expectedError  string
	}{
		{
			name:           "all results on first page",
			pageSize:       10,
			page:           1,
			globalIndex:    nil,
			expectedCount:  4,
			expectedClaims: []*SetClaim{setClaims[3], setClaims[2], setClaims[1], setClaims[0]}, // DESC order
			expectedError:  "",
		},
		{
			name:           "pagination: page 2, size 1",
			pageSize:       1,
			page:           2,
			globalIndex:    nil,
			expectedCount:  4,
			expectedClaims: []*SetClaim{setClaims[2]}, // Second item in DESC order
			expectedError:  "",
		},
		{
			name:           "filter by global index",
			pageSize:       10,
			page:           1,
			globalIndex:    big.NewInt(100),
			expectedCount:  2,
			expectedClaims: []*SetClaim{setClaims[2], setClaims[0]}, // DESC order, filtered by globalIndex=100
			expectedError:  "",
		},
		{
			name:           "filter by non-existent global index",
			pageSize:       10,
			page:           1,
			globalIndex:    big.NewInt(999),
			expectedCount:  0,
			expectedClaims: []*SetClaim{},
			expectedError:  "",
		},
		{
			name:           "invalid page number",
			pageSize:       4,
			page:           5,
			globalIndex:    nil,
			expectedCount:  0,
			expectedClaims: []*SetClaim{},
			expectedError:  "invalid page number for given page size and total number of set_claim",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			setClaims, count, err := p.GetSetClaimsPaged(ctx, tc.page, tc.pageSize, tc.globalIndex)

			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedClaims, setClaims)
				require.Equal(t, tc.expectedCount, count)
			}
		})
	}
}

func TestDatabaseQueryTimeout(t *testing.T) {
	normalTimeout := 100 * time.Millisecond
	shortTimeout := 1 * time.Nanosecond

	path := path.Join(t.TempDir(), "bridgeSyncerProcessorTimeout.db")
	logger := log.WithFields("module", "bridge-syncer-timeout")

	// Create processor with normal timeout for setup
	p, err := newProcessor(path, "bridge-syncer-timeout", logger, normalTimeout)
	require.NoError(t, err)

	// Insert some test data to ensure the database is working
	block := sync.Block{
		Num:    1,
		Hash:   common.HexToHash("0x123"),
		Events: []any{},
	}

	ctx := context.Background()
	err = p.ProcessBlock(ctx, block)
	require.NoError(t, err)

	// Create a new processor with short timeout for testing timeout behavior
	pShortTimeout, err := newProcessor(path, "bridge-syncer-short-timeout", logger, shortTimeout)
	require.NoError(t, err)

	// Test that operations timeout with short timeout
	_, err = pShortTimeout.GetLastProcessedBlock(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "context deadline exceeded")

	_, err = pShortTimeout.GetBridges(ctx, 1, 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "context deadline exceeded")

	_, err = pShortTimeout.GetClaims(ctx, 1, 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "context deadline exceeded")
}

//nolint:dupl
func TestGetClaims_Compact(t *testing.T) {
	// Define all claims used across test cases
	claims := []*Claim{
		// claims[0] - Basic claim with GlobalIndex=1
		{
			BlockNum:            1,
			BlockPos:            0,
			TxHash:              common.HexToHash("0x111"),
			GlobalIndex:         big.NewInt(1),
			OriginNetwork:       1,
			OriginAddress:       common.HexToAddress("0xaaa"),
			DestinationAddress:  common.HexToAddress("0xbbb"),
			Amount:              big.NewInt(100),
			ProofLocalExitRoot:  types.Proof{common.HexToHash("0x1a")},
			ProofRollupExitRoot: types.Proof{common.HexToHash("0x1b")},
			MainnetExitRoot:     common.HexToHash("0x1c"),
			RollupExitRoot:      common.HexToHash("0x1d"),
			GlobalExitRoot:      common.HexToHash("0x1e"),
			DestinationNetwork:  2,
			Metadata:            []byte("metadata1"),
			IsMessage:           false,
			BlockTimestamp:      1000,
			Type:                ClaimEvent,
		},
		// claims[1] - Basic claim with GlobalIndex=2
		{
			BlockNum:            2,
			BlockPos:            0,
			TxHash:              common.HexToHash("0x222"),
			GlobalIndex:         big.NewInt(2),
			OriginNetwork:       3,
			OriginAddress:       common.HexToAddress("0xccc"),
			DestinationAddress:  common.HexToAddress("0xddd"),
			Amount:              big.NewInt(200),
			ProofLocalExitRoot:  types.Proof{common.HexToHash("0x2a")},
			ProofRollupExitRoot: types.Proof{common.HexToHash("0x2b")},
			MainnetExitRoot:     common.HexToHash("0x2c"),
			RollupExitRoot:      common.HexToHash("0x2d"),
			GlobalExitRoot:      common.HexToHash("0x2e"),
			DestinationNetwork:  4,
			Metadata:            []byte("metadata2"),
			IsMessage:           true,
			BlockTimestamp:      2000,
			Type:                ClaimEvent,
		},
		// claims[2] - Oldest claim with GlobalIndex=100 (block 1)
		{
			BlockNum:            1,
			BlockPos:            0,
			TxHash:              common.HexToHash("0x111"),
			GlobalIndex:         big.NewInt(100),
			OriginNetwork:       1,
			OriginAddress:       common.HexToAddress("0xaaa"),
			DestinationAddress:  common.HexToAddress("0xbbb"),
			Amount:              big.NewInt(100),
			ProofLocalExitRoot:  types.Proof{common.HexToHash("0x1a")},
			ProofRollupExitRoot: types.Proof{common.HexToHash("0x1b")},
			MainnetExitRoot:     common.HexToHash("0x1c"),
			RollupExitRoot:      common.HexToHash("0x1d"),
			GlobalExitRoot:      common.HexToHash("0x1e"),
			DestinationNetwork:  2,
			Metadata:            []byte("original_metadata"),
			IsMessage:           false,
			BlockTimestamp:      1000,
			Type:                ClaimEvent,
		},
		// claims[3] - Middle claim with GlobalIndex=100 (block 2)
		{
			BlockNum:            2,
			BlockPos:            0,
			TxHash:              common.HexToHash("0x222"),
			GlobalIndex:         big.NewInt(100),
			OriginNetwork:       99,
			OriginAddress:       common.HexToAddress("0xfff"),
			DestinationAddress:  common.HexToAddress("0xeee"),
			Amount:              big.NewInt(999),
			ProofLocalExitRoot:  types.Proof{common.HexToHash("0x2a")},
			ProofRollupExitRoot: types.Proof{common.HexToHash("0x2b")},
			MainnetExitRoot:     common.HexToHash("0x2c"),
			RollupExitRoot:      common.HexToHash("0x2d"),
			GlobalExitRoot:      common.HexToHash("0x2e"),
			DestinationNetwork:  88,
			Metadata:            []byte("middle_metadata"),
			IsMessage:           true,
			BlockTimestamp:      2000,
			Type:                ClaimEvent,
		},
		// claims[4] - Newest claim with GlobalIndex=100 (block 3)
		{
			BlockNum:            3,
			BlockPos:            0,
			TxHash:              common.HexToHash("0x333"),
			GlobalIndex:         big.NewInt(100),
			OriginNetwork:       77,
			OriginAddress:       common.HexToAddress("0x999"),
			DestinationAddress:  common.HexToAddress("0x888"),
			Amount:              big.NewInt(777),
			ProofLocalExitRoot:  types.Proof{common.HexToHash("0x3a")},
			ProofRollupExitRoot: types.Proof{common.HexToHash("0x3b")},
			MainnetExitRoot:     common.HexToHash("0x3c"),
			RollupExitRoot:      common.HexToHash("0x3d"),
			GlobalExitRoot:      common.HexToHash("0x3e"),
			DestinationNetwork:  66,
			Metadata:            []byte("newest_metadata"),
			IsMessage:           true,
			BlockTimestamp:      3000,
			Type:                DetailedClaimEvent,
		},
		// claims[5] - Oldest claim with GlobalIndex=100 (block 1, pos 0) - for multiple groups test
		{
			BlockNum:            1,
			BlockPos:            0,
			TxHash:              common.HexToHash("0x111"),
			GlobalIndex:         big.NewInt(100),
			OriginNetwork:       1,
			OriginAddress:       common.HexToAddress("0xa1"),
			DestinationAddress:  common.HexToAddress("0xb1"),
			Amount:              big.NewInt(100),
			ProofLocalExitRoot:  types.Proof{common.HexToHash("0x1a")},
			ProofRollupExitRoot: types.Proof{common.HexToHash("0x1b")},
			MainnetExitRoot:     common.HexToHash("0x1c"),
			RollupExitRoot:      common.HexToHash("0x1d"),
			GlobalExitRoot:      common.HexToHash("0x1e"),
			DestinationNetwork:  2,
			Metadata:            []byte("index1_old"),
			IsMessage:           false,
			BlockTimestamp:      1000,
			Type:                ClaimEvent,
		},
		// claims[6] - Oldest claim with GlobalIndex=200 (block 1, pos 1)
		{
			BlockNum:            1,
			BlockPos:            1,
			TxHash:              common.HexToHash("0x112"),
			GlobalIndex:         big.NewInt(200),
			OriginNetwork:       3,
			OriginAddress:       common.HexToAddress("0xa2"),
			DestinationAddress:  common.HexToAddress("0xb2"),
			Amount:              big.NewInt(200),
			ProofLocalExitRoot:  types.Proof{common.HexToHash("0x2a")},
			ProofRollupExitRoot: types.Proof{common.HexToHash("0x2b")},
			MainnetExitRoot:     common.HexToHash("0x2c"),
			RollupExitRoot:      common.HexToHash("0x2d"),
			GlobalExitRoot:      common.HexToHash("0x2e"),
			DestinationNetwork:  4,
			Metadata:            []byte("index2_old"),
			IsMessage:           true,
			BlockTimestamp:      1001,
			Type:                ClaimEvent,
		},
		// claims[7] - Newest claim with GlobalIndex=100 (block 2, pos 0)
		{
			BlockNum:            2,
			BlockPos:            0,
			TxHash:              common.HexToHash("0x221"),
			GlobalIndex:         big.NewInt(100),
			OriginNetwork:       99,
			OriginAddress:       common.HexToAddress("0xc1"),
			DestinationAddress:  common.HexToAddress("0xd1"),
			Amount:              big.NewInt(999),
			ProofLocalExitRoot:  types.Proof{common.HexToHash("0x3a")},
			ProofRollupExitRoot: types.Proof{common.HexToHash("0x3b")},
			MainnetExitRoot:     common.HexToHash("0x3c"),
			RollupExitRoot:      common.HexToHash("0x3d"),
			GlobalExitRoot:      common.HexToHash("0x3e"),
			DestinationNetwork:  88,
			Metadata:            []byte("index1_new"),
			IsMessage:           true,
			BlockTimestamp:      2000,
			Type:                ClaimEvent,
		},
		// claims[8] - Newest claim with GlobalIndex=200 (block 2, pos 1)
		{
			BlockNum:            2,
			BlockPos:            1,
			TxHash:              common.HexToHash("0x222"),
			GlobalIndex:         big.NewInt(200),
			OriginNetwork:       77,
			OriginAddress:       common.HexToAddress("0xc2"),
			DestinationAddress:  common.HexToAddress("0xd2"),
			Amount:              big.NewInt(777),
			ProofLocalExitRoot:  types.Proof{common.HexToHash("0x4a")},
			ProofRollupExitRoot: types.Proof{common.HexToHash("0x4b")},
			MainnetExitRoot:     common.HexToHash("0x4c"),
			RollupExitRoot:      common.HexToHash("0x4d"),
			GlobalExitRoot:      common.HexToHash("0x4e"),
			DestinationNetwork:  66,
			Metadata:            []byte("index2_new"),
			IsMessage:           false,
			BlockTimestamp:      2001,
			Type:                DetailedClaimEvent,
		},
		// claims[9] - Same block, pos 0 with GlobalIndex=123
		{
			BlockNum:            1,
			BlockPos:            0,
			TxHash:              common.HexToHash("0x111"),
			GlobalIndex:         big.NewInt(123),
			OriginNetwork:       1,
			OriginAddress:       common.HexToAddress("0xaaa"),
			DestinationAddress:  common.HexToAddress("0xbbb"),
			Amount:              big.NewInt(100),
			ProofLocalExitRoot:  types.Proof{common.HexToHash("0x1a")},
			ProofRollupExitRoot: types.Proof{common.HexToHash("0x1b")},
			MainnetExitRoot:     common.HexToHash("0x1c"),
			RollupExitRoot:      common.HexToHash("0x1d"),
			GlobalExitRoot:      common.HexToHash("0x1e"),
			DestinationNetwork:  2,
			Metadata:            []byte("pos0"),
			IsMessage:           false,
			BlockTimestamp:      1000,
			Type:                ClaimEvent,
		},
		// claims[10] - Same block, pos 1 with GlobalIndex=123
		{
			BlockNum:            1,
			BlockPos:            1,
			TxHash:              common.HexToHash("0x112"),
			GlobalIndex:         big.NewInt(123),
			OriginNetwork:       99,
			OriginAddress:       common.HexToAddress("0xccc"),
			DestinationAddress:  common.HexToAddress("0xddd"),
			Amount:              big.NewInt(200),
			ProofLocalExitRoot:  types.Proof{common.HexToHash("0x2a")},
			ProofRollupExitRoot: types.Proof{common.HexToHash("0x2b")},
			MainnetExitRoot:     common.HexToHash("0x2c"),
			RollupExitRoot:      common.HexToHash("0x2d"),
			GlobalExitRoot:      common.HexToHash("0x2e"),
			DestinationNetwork:  88,
			Metadata:            []byte("pos1"),
			IsMessage:           true,
			BlockTimestamp:      1001,
			Type:                ClaimEvent,
		},
		// claims[11] - Same block, pos 2 with GlobalIndex=123
		{
			BlockNum:            1,
			BlockPos:            2,
			TxHash:              common.HexToHash("0x113"),
			GlobalIndex:         big.NewInt(123),
			OriginNetwork:       77,
			OriginAddress:       common.HexToAddress("0xeee"),
			DestinationAddress:  common.HexToAddress("0xfff"),
			Amount:              big.NewInt(300),
			ProofLocalExitRoot:  types.Proof{common.HexToHash("0x3a")},
			ProofRollupExitRoot: types.Proof{common.HexToHash("0x3b")},
			MainnetExitRoot:     common.HexToHash("0x3c"),
			RollupExitRoot:      common.HexToHash("0x3d"),
			GlobalExitRoot:      common.HexToHash("0x3e"),
			DestinationNetwork:  66,
			Metadata:            []byte("pos2"),
			IsMessage:           false,
			BlockTimestamp:      1002,
			Type:                ClaimEvent,
		},
		// claims[12] - Partial range GlobalIndex=456 (block 1)
		{
			BlockNum:            1,
			BlockPos:            0,
			TxHash:              common.HexToHash("0x111"),
			GlobalIndex:         big.NewInt(456),
			OriginNetwork:       1,
			OriginAddress:       common.HexToAddress("0xaaa"),
			DestinationAddress:  common.HexToAddress("0xbbb"),
			Amount:              big.NewInt(100),
			ProofLocalExitRoot:  types.Proof{common.HexToHash("0x1a")},
			ProofRollupExitRoot: types.Proof{common.HexToHash("0x1b")},
			MainnetExitRoot:     common.HexToHash("0x1c"),
			RollupExitRoot:      common.HexToHash("0x1d"),
			GlobalExitRoot:      common.HexToHash("0x1e"),
			DestinationNetwork:  2,
			Metadata:            []byte("block1"),
			IsMessage:           false,
			BlockTimestamp:      1000,
			Type:                ClaimEvent,
		},
		// claims[13] - Partial range GlobalIndex=456 (block 2)
		{
			BlockNum:            2,
			BlockPos:            0,
			TxHash:              common.HexToHash("0x222"),
			GlobalIndex:         big.NewInt(456),
			OriginNetwork:       99,
			OriginAddress:       common.HexToAddress("0xccc"),
			DestinationAddress:  common.HexToAddress("0xddd"),
			Amount:              big.NewInt(200),
			ProofLocalExitRoot:  types.Proof{common.HexToHash("0x2a")},
			ProofRollupExitRoot: types.Proof{common.HexToHash("0x2b")},
			MainnetExitRoot:     common.HexToHash("0x2c"),
			RollupExitRoot:      common.HexToHash("0x2d"),
			GlobalExitRoot:      common.HexToHash("0x2e"),
			DestinationNetwork:  88,
			Metadata:            []byte("block2"),
			IsMessage:           true,
			BlockTimestamp:      2000,
			Type:                ClaimEvent,
		},
		// claims[14] - Partial range GlobalIndex=456 (block 3)
		{
			BlockNum:            3,
			BlockPos:            0,
			TxHash:              common.HexToHash("0x333"),
			GlobalIndex:         big.NewInt(456),
			OriginNetwork:       77,
			OriginAddress:       common.HexToAddress("0xeee"),
			DestinationAddress:  common.HexToAddress("0xfff"),
			Amount:              big.NewInt(300),
			ProofLocalExitRoot:  types.Proof{common.HexToHash("0x3a")},
			ProofRollupExitRoot: types.Proof{common.HexToHash("0x3b")},
			MainnetExitRoot:     common.HexToHash("0x3c"),
			RollupExitRoot:      common.HexToHash("0x3d"),
			GlobalExitRoot:      common.HexToHash("0x3e"),
			DestinationNetwork:  66,
			Metadata:            []byte("block3"),
			IsMessage:           false,
			BlockTimestamp:      3000,
			Type:                ClaimEvent,
		},
		// claims[15] - Ordering test GlobalIndex=200 (block 1)
		{
			BlockNum:            1,
			BlockPos:            0,
			TxHash:              common.HexToHash("0x111"),
			GlobalIndex:         big.NewInt(200),
			OriginNetwork:       1,
			OriginAddress:       common.HexToAddress("0xa1"),
			DestinationAddress:  common.HexToAddress("0xb1"),
			Amount:              big.NewInt(100),
			ProofLocalExitRoot:  types.Proof{common.HexToHash("0x1a")},
			ProofRollupExitRoot: types.Proof{common.HexToHash("0x1b")},
			MainnetExitRoot:     common.HexToHash("0x1c"),
			RollupExitRoot:      common.HexToHash("0x1d"),
			GlobalExitRoot:      common.HexToHash("0x1e"),
			DestinationNetwork:  2,
			Metadata:            []byte("200"),
			IsMessage:           false,
			BlockTimestamp:      1000,
			Type:                ClaimEvent,
		},
		// claims[16] - Ordering test GlobalIndex=100 (block 2)
		{
			BlockNum:            2,
			BlockPos:            0,
			TxHash:              common.HexToHash("0x222"),
			GlobalIndex:         big.NewInt(100),
			OriginNetwork:       2,
			OriginAddress:       common.HexToAddress("0xa2"),
			DestinationAddress:  common.HexToAddress("0xb2"),
			Amount:              big.NewInt(200),
			ProofLocalExitRoot:  types.Proof{common.HexToHash("0x2a")},
			ProofRollupExitRoot: types.Proof{common.HexToHash("0x2b")},
			MainnetExitRoot:     common.HexToHash("0x2c"),
			RollupExitRoot:      common.HexToHash("0x2d"),
			GlobalExitRoot:      common.HexToHash("0x2e"),
			DestinationNetwork:  3,
			Metadata:            []byte("100"),
			IsMessage:           true,
			BlockTimestamp:      2000,
			Type:                ClaimEvent,
		},
		// claims[17] - Ordering test GlobalIndex=150 (block 3)
		{
			BlockNum:            3,
			BlockPos:            0,
			TxHash:              common.HexToHash("0x333"),
			GlobalIndex:         big.NewInt(150),
			OriginNetwork:       3,
			OriginAddress:       common.HexToAddress("0xa3"),
			DestinationAddress:  common.HexToAddress("0xb3"),
			Amount:              big.NewInt(300),
			ProofLocalExitRoot:  types.Proof{common.HexToHash("0x3a")},
			ProofRollupExitRoot: types.Proof{common.HexToHash("0x3b")},
			MainnetExitRoot:     common.HexToHash("0x3c"),
			RollupExitRoot:      common.HexToHash("0x3d"),
			GlobalExitRoot:      common.HexToHash("0x3e"),
			DestinationNetwork:  4,
			Metadata:            []byte("150"),
			IsMessage:           false,
			BlockTimestamp:      3000,
			Type:                ClaimEvent,
		},
		// claims[18] - block 3, pos 1 with GlobalIndex=200
		{
			BlockNum:            3,
			BlockPos:            1,
			TxHash:              common.HexToHash("0x112"),
			GlobalIndex:         big.NewInt(200),
			OriginNetwork:       3,
			OriginAddress:       common.HexToAddress("0xccc"),
			DestinationAddress:  common.HexToAddress("0xddd"),
			Amount:              big.NewInt(200),
			ProofLocalExitRoot:  types.Proof{common.HexToHash("0x2ab")},
			ProofRollupExitRoot: types.Proof{common.HexToHash("0x2bc")},
			MainnetExitRoot:     common.HexToHash("0x2ce"),
			RollupExitRoot:      common.HexToHash("0x2df"),
			GlobalExitRoot:      common.HexToHash("0x2ee"),
			DestinationNetwork:  88,
			Metadata:            []byte("block3pos1"),
			IsMessage:           true,
			BlockTimestamp:      3001,
			Type:                ClaimEvent,
		},
	}

	testCases := []struct {
		name            string
		setupBlocks     func() []sync.Block
		queryFrom       uint64
		queryTo         uint64
		expectedCount   int
		errorContains   string
		validateResults func(t *testing.T, claims []Claim)
	}{
		{
			name: "non-compacted mode with different claims",
			setupBlocks: func() []sync.Block {
				return []sync.Block{
					{
						Num:  1,
						Hash: common.HexToHash("0x1"),
						Events: []any{
							Event{Claim: claims[0]},
						},
					},
					{
						Num:  2,
						Hash: common.HexToHash("0x2"),
						Events: []any{
							Event{Claim: claims[1]},
						},
					},
				}
			},
			queryFrom:     1,
			queryTo:       2,
			expectedCount: 2,
			validateResults: func(t *testing.T, claims []Claim) {
				t.Helper()
				require.Len(t, claims, 2)
				require.Equal(t, big.NewInt(1), claims[0].GlobalIndex)
				require.Equal(t, big.NewInt(2), claims[1].GlobalIndex)
			},
		},
		{
			name: "compacted mode with no duplicates",
			setupBlocks: func() []sync.Block {
				return []sync.Block{
					{
						Num:  1,
						Hash: common.HexToHash("0x1"),
						Events: []any{
							Event{Claim: claims[0]},
						},
					},
					{
						Num:  2,
						Hash: common.HexToHash("0x2"),
						Events: []any{
							Event{Claim: claims[1]},
						},
					},
				}
			},
			queryFrom:     1,
			queryTo:       2,
			expectedCount: 2,
			validateResults: func(t *testing.T, claims []Claim) {
				t.Helper()
				require.Len(t, claims, 2)
				require.Equal(t, big.NewInt(1), claims[0].GlobalIndex)
				require.Equal(t, big.NewInt(2), claims[1].GlobalIndex)
			},
		},
		{
			name: "compacted mode with duplicates across blocks",
			setupBlocks: func() []sync.Block {
				return []sync.Block{
					{
						Num:  1,
						Hash: common.HexToHash("0x1"),
						Events: []any{
							Event{Claim: claims[2]},
						},
					},
					{
						Num:  2,
						Hash: common.HexToHash("0x2"),
						Events: []any{
							Event{Claim: claims[3]},
						},
					},
					{
						Num:  3,
						Hash: common.HexToHash("0x3"),
						Events: []any{
							Event{Claim: claims[4]},
						},
					},
				}
			},
			queryFrom:     1,
			queryTo:       3,
			expectedCount: 1,
			validateResults: func(t *testing.T, claims []Claim) {
				t.Helper()
				claim := claims[0]
				// Fields from oldest claim (block 1) - should be preserved
				require.Equal(t, uint64(1), claim.BlockNum, "BlockNum should be from oldest claim")
				require.Equal(t, uint64(0), claim.BlockPos, "BlockPos should be from oldest claim")
				require.Equal(t, common.HexToHash("0x111"), claim.TxHash, "TxHash should be from oldest claim")
				require.Equal(t, big.NewInt(100), claim.GlobalIndex)
				require.Equal(t, uint32(1), claim.OriginNetwork, "OriginNetwork should be from oldest claim")
				require.Equal(t, common.HexToAddress("0xaaa"), claim.OriginAddress, "OriginAddress should be from oldest claim")
				require.Equal(t, common.HexToAddress("0xbbb"), claim.DestinationAddress, "DestinationAddress should be from oldest claim")
				require.Equal(t, big.NewInt(100), claim.Amount, "Amount should be from oldest claim")
				require.Equal(t, uint32(2), claim.DestinationNetwork, "DestinationNetwork should be from oldest claim")
				require.Equal(t, []byte("original_metadata"), claim.Metadata, "Metadata should be from oldest claim")
				require.Equal(t, false, claim.IsMessage, "IsMessage should be from oldest claim")
				require.Equal(t, uint64(1000), claim.BlockTimestamp, "BlockTimestamp should be from oldest claim")
				// Fields from newest claim (block 3) - should be updated
				require.Equal(t, common.HexToHash("0x3a"), claim.ProofLocalExitRoot[0], "ProofLocalExitRoot should be from newest claim")
				require.Equal(t, common.HexToHash("0x3b"), claim.ProofRollupExitRoot[0], "ProofRollupExitRoot should be from newest claim")
				require.Equal(t, common.HexToHash("0x3c"), claim.MainnetExitRoot, "MainnetExitRoot should be from newest claim")
				require.Equal(t, common.HexToHash("0x3d"), claim.RollupExitRoot, "RollupExitRoot should be from newest claim")
				require.Equal(t, common.HexToHash("0x3e"), claim.GlobalExitRoot, "GlobalExitRoot should be from newest claim")
			},
		},
		{
			name: "compacted mode with multiple duplicate groups",
			setupBlocks: func() []sync.Block {
				return []sync.Block{
					{
						Num:  1,
						Hash: common.HexToHash("0x1"),
						Events: []any{
							Event{Claim: claims[5]}, // GlobalIndex=100, oldest
							Event{Claim: claims[6]}, // GlobalIndex=200, oldest
						},
					},
					{
						Num:  2,
						Hash: common.HexToHash("0x2"),
						Events: []any{
							Event{Claim: claims[7]}, // GlobalIndex=100, newest
							Event{Claim: claims[8]}, // GlobalIndex=200, newest
						},
					},
				}
			},
			queryFrom:     1,
			queryTo:       2,
			expectedCount: 2,
			validateResults: func(t *testing.T, claims []Claim) {
				t.Helper()
				// First claim (globalIndex1=100)
				claim1 := claims[0]
				require.Equal(t, big.NewInt(100), claim1.GlobalIndex)
				// Fields from oldest claim (block 1, pos 0) - should be preserved
				require.Equal(t, uint64(1), claim1.BlockNum, "Claim1: BlockNum should be from oldest")
				require.Equal(t, uint64(0), claim1.BlockPos, "Claim1: BlockPos should be from oldest")
				require.Equal(t, uint32(1), claim1.OriginNetwork, "Claim1: OriginNetwork should be from oldest")
				require.Equal(t, common.HexToAddress("0xa1"), claim1.OriginAddress, "Claim1: OriginAddress should be from oldest")
				require.Equal(t, common.HexToAddress("0xb1"), claim1.DestinationAddress, "Claim1: DestinationAddress should be from oldest")
				require.Equal(t, big.NewInt(100), claim1.Amount, "Claim1: Amount should be from oldest")
				require.Equal(t, uint32(2), claim1.DestinationNetwork, "Claim1: DestinationNetwork should be from oldest")
				require.Equal(t, []byte("index1_old"), claim1.Metadata, "Claim1: Metadata should be from oldest")
				require.Equal(t, false, claim1.IsMessage, "Claim1: IsMessage should be from oldest")
				require.Equal(t, uint64(1000), claim1.BlockTimestamp, "Claim1: BlockTimestamp should be from oldest")
				// Fields from newest claim (block 2, pos 0) - should be updated
				require.Equal(t, common.HexToHash("0x3a"), claim1.ProofLocalExitRoot[0], "Claim1: ProofLocalExitRoot should be from newest")
				require.Equal(t, common.HexToHash("0x3b"), claim1.ProofRollupExitRoot[0], "Claim1: ProofRollupExitRoot should be from newest")
				require.Equal(t, common.HexToHash("0x3c"), claim1.MainnetExitRoot, "Claim1: MainnetExitRoot should be from newest")
				require.Equal(t, common.HexToHash("0x3d"), claim1.RollupExitRoot, "Claim1: RollupExitRoot should be from newest")
				require.Equal(t, common.HexToHash("0x3e"), claim1.GlobalExitRoot, "Claim1: GlobalExitRoot should be from newest")

				// Second claim (globalIndex2=200)
				claim2 := claims[1]
				require.Equal(t, big.NewInt(200), claim2.GlobalIndex)
				// Fields from oldest claim (block 1, pos 1) - should be preserved
				require.Equal(t, uint64(1), claim2.BlockNum, "Claim2: BlockNum should be from oldest")
				require.Equal(t, uint64(1), claim2.BlockPos, "Claim2: BlockPos should be from oldest")
				require.Equal(t, uint32(3), claim2.OriginNetwork, "Claim2: OriginNetwork should be from oldest")
				require.Equal(t, common.HexToAddress("0xa2"), claim2.OriginAddress, "Claim2: OriginAddress should be from oldest")
				require.Equal(t, common.HexToAddress("0xb2"), claim2.DestinationAddress, "Claim2: DestinationAddress should be from oldest")
				require.Equal(t, big.NewInt(200), claim2.Amount, "Claim2: Amount should be from oldest")
				require.Equal(t, uint32(4), claim2.DestinationNetwork, "Claim2: DestinationNetwork should be from oldest")
				require.Equal(t, []byte("index2_old"), claim2.Metadata, "Claim2: Metadata should be from oldest")
				require.Equal(t, true, claim2.IsMessage, "Claim2: IsMessage should be from oldest")
				require.Equal(t, uint64(1001), claim2.BlockTimestamp, "Claim2: BlockTimestamp should be from oldest")
				// Fields from newest claim (block 2, pos 1) - should be updated
				require.Equal(t, common.HexToHash("0x4a"), claim2.ProofLocalExitRoot[0], "Claim2: ProofLocalExitRoot should be from newest")
				require.Equal(t, common.HexToHash("0x4b"), claim2.ProofRollupExitRoot[0], "Claim2: ProofRollupExitRoot should be from newest")
				require.Equal(t, common.HexToHash("0x4c"), claim2.MainnetExitRoot, "Claim2: MainnetExitRoot should be from newest")
				require.Equal(t, common.HexToHash("0x4d"), claim2.RollupExitRoot, "Claim2: RollupExitRoot should be from newest")
				require.Equal(t, common.HexToHash("0x4e"), claim2.GlobalExitRoot, "Claim2: GlobalExitRoot should be from newest")
			},
		},
		{
			name: "compacted mode same block multiple positions",
			setupBlocks: func() []sync.Block {
				return []sync.Block{
					{
						Num:  1,
						Hash: common.HexToHash("0x1"),
						Events: []any{
							Event{Claim: claims[9]},  // pos 0 - oldest
							Event{Claim: claims[10]}, // pos 1 - middle
							Event{Claim: claims[11]}, // pos 2 - newest
						},
					},
				}
			},
			queryFrom:     1,
			queryTo:       1,
			expectedCount: 1,
			validateResults: func(t *testing.T, claims []Claim) {
				t.Helper()
				claim := claims[0]
				// Fields from oldest claim (block 1, pos 0) - should be preserved
				require.Equal(t, uint64(1), claim.BlockNum, "BlockNum should be from oldest")
				require.Equal(t, uint64(0), claim.BlockPos, "BlockPos should be from oldest (pos 0)")
				require.Equal(t, uint32(1), claim.OriginNetwork, "OriginNetwork should be from oldest")
				require.Equal(t, common.HexToAddress("0xaaa"), claim.OriginAddress, "OriginAddress should be from oldest")
				require.Equal(t, common.HexToAddress("0xbbb"), claim.DestinationAddress, "DestinationAddress should be from oldest")
				require.Equal(t, big.NewInt(100), claim.Amount, "Amount should be from oldest")
				require.Equal(t, uint32(2), claim.DestinationNetwork, "DestinationNetwork should be from oldest")
				require.Equal(t, []byte("pos0"), claim.Metadata, "Metadata should be from oldest (pos0)")
				require.Equal(t, false, claim.IsMessage, "IsMessage should be from oldest")
				require.Equal(t, uint64(1000), claim.BlockTimestamp, "BlockTimestamp should be from oldest")
				// Fields from newest claim (block 1, pos 2) - should be updated
				require.Equal(t, common.HexToHash("0x3a"), claim.ProofLocalExitRoot[0], "ProofLocalExitRoot should be from newest (pos 2)")
				require.Equal(t, common.HexToHash("0x3b"), claim.ProofRollupExitRoot[0], "ProofRollupExitRoot should be from newest (pos 2)")
				require.Equal(t, common.HexToHash("0x3c"), claim.MainnetExitRoot, "MainnetExitRoot should be from newest (pos 2)")
				require.Equal(t, common.HexToHash("0x3d"), claim.RollupExitRoot, "RollupExitRoot should be from newest (pos 2)")
				require.Equal(t, common.HexToHash("0x3e"), claim.GlobalExitRoot, "GlobalExitRoot should be from newest (pos 2)")
			},
		},
		{
			name: "compacted mode empty range",
			setupBlocks: func() []sync.Block {
				return []sync.Block{
					{
						Num:    1,
						Hash:   common.HexToHash("0x1"),
						Events: []any{},
					},
				}
			},
			queryFrom:     1,
			queryTo:       1,
			expectedCount: 0,
		},
		{
			name: "compacted mode partial range",
			setupBlocks: func() []sync.Block {
				return []sync.Block{
					{
						Num:  1,
						Hash: common.HexToHash("0x1"),
						Events: []any{
							Event{Claim: claims[12]}, // block 1
						},
					},
					{
						Num:  2,
						Hash: common.HexToHash("0x2"),
						Events: []any{
							Event{Claim: claims[13]}, // block 2
						},
					},
					{
						Num:  3,
						Hash: common.HexToHash("0x3"),
						Events: []any{
							Event{Claim: claims[14]}, // block 3
						},
					},
				}
			},
			queryFrom:     2,
			queryTo:       3,
			expectedCount: 0, // Changed from 1 to 0: globally oldest claim (block 1) is outside query range
			validateResults: func(t *testing.T, claims []Claim) {
				t.Helper()
				// Case 3: Since globally oldest claim (block 1) is outside the query range (2-3),
				// we should not return anything for this global_index (no unset claim exists)
				require.Empty(t, claims, "should return no claims when globally oldest is outside range")
			},
		},
		{
			name: "ordering preserved by block number and position",
			setupBlocks: func() []sync.Block {
				return []sync.Block{
					{
						Num:  1,
						Hash: common.HexToHash("0x1"),
						Events: []any{
							Event{Claim: claims[15]}, // GlobalIndex=200
						},
					},
					{
						Num:  2,
						Hash: common.HexToHash("0x2"),
						Events: []any{
							Event{Claim: claims[16]}, // GlobalIndex=100
						},
					},
					{
						Num:  3,
						Hash: common.HexToHash("0x3"),
						Events: []any{
							Event{Claim: claims[17]}, // GlobalIndex=150
						},
					},
				}
			},
			queryFrom:     1,
			queryTo:       3,
			expectedCount: 3,
			validateResults: func(t *testing.T, claims []Claim) {
				t.Helper()
				// Should be ordered by block_num ASC, not by global_index value
				require.Equal(t, big.NewInt(200), claims[0].GlobalIndex)
				require.Equal(t, big.NewInt(100), claims[1].GlobalIndex)
				require.Equal(t, big.NewInt(150), claims[2].GlobalIndex)
			},
		},
		{
			name: "invalid block range - fromBlock greater than toBlock",
			setupBlocks: func() []sync.Block {
				return []sync.Block{
					{
						Num:  1,
						Hash: common.HexToHash("0x1"),
						Events: []any{
							Event{Claim: claims[0]},
						},
					},
					{
						Num:  2,
						Hash: common.HexToHash("0x2"),
						Events: []any{
							Event{Claim: claims[1]},
						},
					},
					{
						Num:    3,
						Hash:   common.HexToHash("0x3"),
						Events: []any{},
					},
					{
						Num:    4,
						Hash:   common.HexToHash("0x4"),
						Events: []any{},
					},
					{
						Num:    5,
						Hash:   common.HexToHash("0x5"),
						Events: []any{},
					},
				}
			},
			queryFrom:     5,
			queryTo:       3,
			expectedCount: 0,
			validateResults: func(t *testing.T, claims []Claim) {
				t.Helper()
				// Should return empty array for invalid range (WHERE block_num >= 5 AND block_num <= 3 returns nothing)
				require.Empty(t, claims)
			},
		},
		{
			name: "fromBlock = 0 edge case",
			setupBlocks: func() []sync.Block {
				return []sync.Block{
					{
						Num:  1,
						Hash: common.HexToHash("0x1"),
						Events: []any{
							Event{Claim: claims[0]},
						},
					},
					{
						Num:  2,
						Hash: common.HexToHash("0x2"),
						Events: []any{
							Event{Claim: claims[1]},
						},
					},
					{
						Num:  3,
						Hash: common.HexToHash("0x3"),
						Events: []any{
							Event{Claim: claims[4]}, // GlobalIndex=100, BlockNum=3
						},
					},
				}
			},
			queryFrom:     0,
			queryTo:       3,
			expectedCount: 3,
			validateResults: func(t *testing.T, claims []Claim) {
				t.Helper()
				// Should return all claims from block 1 onwards (block 0 doesn't exist, WHERE clause: block_num >= 0)
				require.Len(t, claims, 3)
				require.Equal(t, big.NewInt(1), claims[0].GlobalIndex)
				require.Equal(t, uint64(1), claims[0].BlockNum)
				require.Equal(t, big.NewInt(2), claims[1].GlobalIndex)
				require.Equal(t, uint64(2), claims[1].BlockNum)
				require.Equal(t, big.NewInt(100), claims[2].GlobalIndex)
				require.Equal(t, uint64(3), claims[2].BlockNum)
			},
		},
		{
			name: "claims at both fromBlock and toBlock boundaries with compaction",
			setupBlocks: func() []sync.Block {
				return []sync.Block{
					{
						Num:  1,
						Hash: common.HexToHash("0x1"),
						Events: []any{
							Event{Claim: claims[0]}, // GlobalIndex=1, outside range
						},
					},
					{
						Num:  2,
						Hash: common.HexToHash("0x2"),
						Events: []any{
							Event{Claim: claims[3]}, // GlobalIndex=100, at fromBlock boundary (oldest)
						},
					},
					{
						Num:  3,
						Hash: common.HexToHash("0x3"),
						Events: []any{
							Event{Claim: claims[4]}, // GlobalIndex=100, at toBlock boundary (newest, should provide proofs)
						},
					},
					{
						Num:    4,
						Hash:   common.HexToHash("0x4"),
						Events: []any{},
					},
				}
			},
			queryFrom:     2,
			queryTo:       3,
			expectedCount: 1,
			validateResults: func(t *testing.T, claims []Claim) {
				t.Helper()
				// Should compact GlobalIndex=100 appearing at both boundaries (blocks 2 and 3)
				require.Len(t, claims, 1)
				require.Equal(t, big.NewInt(100), claims[0].GlobalIndex)
				require.Equal(t, uint64(2), claims[0].BlockNum, "Should preserve oldest block (fromBlock boundary)")
				require.Equal(t, uint64(0), claims[0].BlockPos, "Should preserve oldest BlockPos")
				// Verify compaction: oldest metadata from block 2, newest proofs from block 3
				require.Equal(t, []byte("middle_metadata"), claims[0].Metadata, "Should preserve oldest metadata from block 2")
				require.Equal(t, common.HexToHash("0x3a"), claims[0].ProofLocalExitRoot[0], "Should use newest proof from block 3")
				require.Equal(t, common.HexToHash("0x3c"), claims[0].MainnetExitRoot, "Should use newest MainnetExitRoot from block 3")
			},
		},
		{
			name: "block range with gaps in processed blocks",
			setupBlocks: func() []sync.Block {
				return []sync.Block{
					{
						Num:  1,
						Hash: common.HexToHash("0x1"),
						Events: []any{
							Event{Claim: claims[0]}, // GlobalIndex=1
						},
					},
					{
						Num:    2,
						Hash:   common.HexToHash("0x2"),
						Events: []any{}, // Block 2 has no claims (processed but empty)
					},
					// Block 3 is completely skipped (not processed)
					{
						Num:  4,
						Hash: common.HexToHash("0x4"),
						Events: []any{
							Event{Claim: claims[1]}, // GlobalIndex=2
						},
					},
					{
						Num:    5,
						Hash:   common.HexToHash("0x5"),
						Events: []any{}, // Block 5 has no claims (processed but empty)
					},
				}
			},
			queryFrom:     1,
			queryTo:       5,
			expectedCount: 2,
			validateResults: func(t *testing.T, claims []Claim) {
				t.Helper()
				// Should return claims from blocks that exist in the range
				// Block 2 has no events, block 3 was not processed at all
				// But claims from blocks 1 and 4 should still be returned
				require.Len(t, claims, 2)
				require.Equal(t, big.NewInt(1), claims[0].GlobalIndex, "First claim from block 1")
				require.Equal(t, big.NewInt(2), claims[1].GlobalIndex, "Second claim - claims[1] was added to block 4 but has BlockNum=2 in its data")
				require.Equal(t, uint64(1), claims[0].BlockNum)
				require.Equal(t, uint64(2), claims[1].BlockNum, "BlockNum comes from claim data, not the block it was added to")
			},
		},
		{
			name: "Case 1: don't compact if unset claim exists for global_index",
			setupBlocks: func() []sync.Block {
				return []sync.Block{
					{
						Num:  1,
						Hash: common.HexToHash("0x1"),
						Events: []any{
							Event{Claim: claims[2]}, // GlobalIndex=100, block 1, pos 1
						},
					},
					{
						Num:  2,
						Hash: common.HexToHash("0x2"),
						Events: []any{
							Event{UnsetClaim: &UnsetClaim{ // Unset claim for GlobalIndex=1
								GlobalIndex:               big.NewInt(100),
								BlockNum:                  2,
								BlockPos:                  0,
								TxHash:                    common.Hash{},
								UnsetGlobalIndexHashChain: common.Hash{},
							}},
						},
					},
					{
						Num:  3,
						Hash: common.HexToHash("0x3"),
						Events: []any{
							Event{Claim: claims[4]}, // GlobalIndex=1, block 3, pos 0
						},
					},
				}
			},
			queryFrom:     1,
			queryTo:       3,
			expectedCount: 2, // Should return all claims without compacting GlobalIndex=100 due to unset claim
			validateResults: func(t *testing.T, resultClaims []Claim) {
				t.Helper()
				// Should return: claim (GI=100, block 1), claim (GI=100, block 3)
				require.Len(t, resultClaims, 2, "should not compact GlobalIndex=100 when unset claim exists")
				require.Equal(t, *claims[2], resultClaims[0])
				require.Equal(t, *claims[4], resultClaims[1])
			},
		},
		{
			name: "Case 2: compact if no unset claim exists",
			setupBlocks: func() []sync.Block {
				return []sync.Block{
					{
						Num:  1,
						Hash: common.HexToHash("0x1"),
						Events: []any{
							Event{Claim: claims[2]}, // GlobalIndex=100, block 1 (oldest)
						},
					},
					{
						Num:  2,
						Hash: common.HexToHash("0x2"),
						Events: []any{
							Event{Claim: claims[3]}, // GlobalIndex=100, block 2
						},
					},
					{
						Num:  3,
						Hash: common.HexToHash("0x3"),
						Events: []any{
							Event{Claim: claims[4]}, // GlobalIndex=100, block 3 (newest)
							// No unset claim - should compact
						},
					},
				}
			},
			queryFrom:     1,
			queryTo:       3,
			expectedCount: 1, // Should return 1 compacted claim
			validateResults: func(t *testing.T, claims []Claim) {
				t.Helper()
				// Should compact all 3 claims into 1
				require.Len(t, claims, 1, "should compact when no unset claim exists")
				claim := claims[0]
				require.Equal(t, big.NewInt(100), claim.GlobalIndex)
				// Metadata from oldest (block 1)
				require.Equal(t, uint64(1), claim.BlockNum, "should preserve oldest block")
				require.Equal(t, uint64(0), claim.BlockPos, "should preserve oldest position")
				require.Equal(t, []byte("original_metadata"), claim.Metadata, "should preserve oldest metadata")
				require.Equal(t, big.NewInt(100), claim.Amount, "should preserve oldest amount")
				require.Equal(t, uint32(1), claim.OriginNetwork, "should preserve oldest origin network")
				// Proofs from newest (block 3)
				require.Equal(t, common.HexToHash("0x3a"), claim.ProofLocalExitRoot[0], "should use newest proof")
				require.Equal(t, common.HexToHash("0x3c"), claim.MainnetExitRoot, "should use newest MainnetExitRoot")
				require.Equal(t, common.HexToHash("0x3d"), claim.RollupExitRoot, "should use newest RollupExitRoot")
				require.Equal(t, common.HexToHash("0x3e"), claim.GlobalExitRoot, "should use newest GlobalExitRoot")
			},
		},
		{
			name: "Case 3: don't return if globally oldest is outside query range",
			setupBlocks: func() []sync.Block {
				return []sync.Block{
					{
						Num:  1,
						Hash: common.HexToHash("0x1"),
						Events: []any{
							Event{Claim: claims[2]}, // GlobalIndex=100, block 1 (globally oldest)
						},
					},
					{
						Num:  2,
						Hash: common.HexToHash("0x2"),
						Events: []any{
							Event{Claim: claims[3]}, // GlobalIndex=100, block 2
						},
					},
					{
						Num:  3,
						Hash: common.HexToHash("0x3"),
						Events: []any{
							Event{Claim: claims[4]}, // GlobalIndex=100, block 3 (newest)
						},
					},
				}
			},
			queryFrom:     2, // Query starts at block 2, but globally oldest is at block 1
			queryTo:       3,
			expectedCount: 0, // Should return nothing because globally oldest (block 1) is outside range
			validateResults: func(t *testing.T, claims []Claim) {
				t.Helper()
				// Should return no claims because the globally oldest claim (block 1) is outside the query range (2-3)
				require.Empty(t, claims, "should not return claims when globally oldest is outside query range")
			},
		},
		{
			name: "Case 3 exception: return if unset claim exists even when globally oldest is outside range",
			setupBlocks: func() []sync.Block {
				return []sync.Block{
					{
						Num:  1,
						Hash: common.HexToHash("0x1"),
						Events: []any{
							Event{Claim: claims[0]}, // GlobalIndex=1, block 1, pos 0
						},
					},
					{
						Num:  2,
						Hash: common.HexToHash("0x2"),
						Events: []any{
							Event{UnsetClaim: &UnsetClaim{ // Unset claim for GlobalIndex=100
								GlobalIndex:               big.NewInt(1),
								BlockNum:                  1,
								BlockPos:                  1,
								TxHash:                    common.Hash{},
								UnsetGlobalIndexHashChain: common.Hash{},
							}},
						},
					},
					{
						Num:  3,
						Hash: common.HexToHash("0x3"),
						Events: []any{
							Event{Claim: claims[4]}, // GlobalIndex=1, block 3, pos 0
						},
					},
				}
			},
			queryFrom:     3, // Query starts at block 3, globally oldest is at block 1
			queryTo:       3,
			expectedCount: 1, // Should return claim from block 3 (uncompacted) because unset claim exists
			validateResults: func(t *testing.T, resultClaims []Claim) {
				t.Helper()
				// Should return claim from block 3 even though globally oldest is outside range
				// because an unset claim exists for this global_index
				require.Len(t, resultClaims, 1, "should return claims when unset claim exists, even if globally oldest is outside range")
				require.Equal(t, *claims[4], resultClaims[0])
			},
		},
		{
			name: "Multiple global_indexes with different compaction rules",
			setupBlocks: func() []sync.Block {
				return []sync.Block{
					{
						Num:  1,
						Hash: common.HexToHash("0x1"),
						Events: []any{
							Event{Claim: claims[2]}, // GlobalIndex=100, block 1, pos 0 (globally oldest)
							Event{Claim: claims[6]}, // GlobalIndex=200, block 1, pos 1 (globally oldest)
						},
					},
					{
						Num:  2,
						Hash: common.HexToHash("0x2"),
						Events: []any{
							Event{UnsetClaim: &UnsetClaim{ // Unset claim for GlobalIndex=100
								GlobalIndex: big.NewInt(100),
								BlockNum:    1,
								BlockPos:    1,
							}},
						},
					},
					{
						Num:  3,
						Hash: common.HexToHash("0x2"),
						Events: []any{
							Event{Claim: claims[4]},  // GlobalIndex=100, block 3, pos 0
							Event{Claim: claims[18]}, // GlobalIndex=200, block 3, pos 1
						},
					},
				}
			},
			queryFrom:     3, // Query block 3
			queryTo:       3,
			expectedCount: 1, // GlobalIndex=100: 1 claim (uncompacted, block 2 due to unset claim)
			// GlobalIndex=456: 0 claims (globally oldest is at block 1, outside range)
			// GlobalIndex=1: 0 claims (only exists at block 1, outside range)
			validateResults: func(t *testing.T, resultClaims []Claim) {
				t.Helper()
				require.Len(t, resultClaims, 1, "should apply different rules per global_index")
				// Should be GlobalIndex=100 (the one with unset claim)
				require.Equal(t, *claims[4], resultClaims[0])
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			dbPath := t.TempDir() + "/test.db"
			p, err := newProcessor(dbPath, "test", log.GetDefaultLogger(), time.Second*10)
			require.NoError(t, err)

			// Setup blocks
			blocks := tc.setupBlocks()
			for _, block := range blocks {
				err := p.ProcessBlock(ctx, block)
				require.NoError(t, err)
			}

			// Execute test
			claims, err := p.GetClaims(ctx, tc.queryFrom, tc.queryTo)

			// Validate error expectations
			if tc.errorContains != "" {
				require.ErrorContains(t, err, tc.errorContains)
			} else {
				// Validate success expectations
				require.NoError(t, err)
				require.Len(t, claims, tc.expectedCount)

				// Run custom validations if provided
				if tc.validateResults != nil {
					tc.validateResults(t, claims)
				}
			}
		})
	}
}

// TestGetClaimsPaged_CompactionAcrossPages tests the compaction behavior when
// claims with the same global_index span across multiple pages
func TestGetClaimsPaged_CompactionAcrossPages(t *testing.T) {
	path := path.Join(t.TempDir(), "claimsPaged_compaction.sqlite")
	require.NoError(t, migrations.RunMigrations(path))
	logger := log.WithFields("module", "bridge-syncer")
	p, err := newProcessor(path, "bridge-syncer", logger, dbQueryTimeout)
	require.NoError(t, err)

	ctx := context.Background()

	// Create test scenario:
	// - Global index 100: claims at blocks 10 (oldest), 20, 30 (newest with updated proofs)
	// - Global index 200: claims at blocks 15 (oldest), 25 (newest with updated proofs)
	// - Global index 300: single claim at block 35
	// - Global index 400: single claim at block 5
	//
	// When ordered DESC by block_num: [35, 30, 25, 20, 15, 10, 5]
	//
	// Page 1 (size 3): blocks [35, 30, 25]
	//   - Block 35: global_index=300 (newest) -> INCLUDE (compacted with itself)
	//   - Block 30: global_index=100 (newest) -> INCLUDE (compacted with block 10)
	//   - Block 25: global_index=200 (newest) -> INCLUDE (compacted with block 15)
	//
	// Page 2 (size 3): blocks [20, 15, 10]
	//   - Block 20: global_index=100 (NOT newest, 30 is newest) -> EXCLUDE
	//   - Block 15: global_index=200 (NOT newest, 25 is newest) -> EXCLUDE
	//   - Block 10: global_index=100 (NOT newest, 30 is newest) -> EXCLUDE
	//
	// Page 3 (size 3): blocks [5]
	//   - Block 5: global_index=400 (newest) -> INCLUDE (compacted with itself)

	oldProof := types.Proof{}
	oldProof[0] = common.HexToHash("0x01")

	newProof := types.Proof{}
	newProof[0] = common.HexToHash("0x02")

	claims := []*Claim{
		// Global index 100 - oldest (will be base for compaction)
		{
			BlockNum:            10,
			BlockPos:            0,
			TxHash:              common.HexToHash("0xa1"),
			GlobalIndex:         big.NewInt(100),
			OriginNetwork:       1,
			OriginAddress:       common.HexToAddress("0x1111"),
			DestinationAddress:  common.HexToAddress("0x2222"),
			Amount:              big.NewInt(1000),
			ProofLocalExitRoot:  oldProof,
			ProofRollupExitRoot: oldProof,
			MainnetExitRoot:     common.HexToHash("0x3333"),
			RollupExitRoot:      common.HexToHash("0x4444"),
			GlobalExitRoot:      common.HexToHash("0x5555"),
			DestinationNetwork:  2,
			Metadata:            []byte("old"),
			IsMessage:           false,
			BlockTimestamp:      1000,
		},
		// Global index 200 - oldest
		{
			BlockNum:            15,
			BlockPos:            0,
			TxHash:              common.HexToHash("0xa2"),
			GlobalIndex:         big.NewInt(200),
			OriginNetwork:       3,
			OriginAddress:       common.HexToAddress("0x3333"),
			DestinationAddress:  common.HexToAddress("0x4444"),
			Amount:              big.NewInt(2000),
			ProofLocalExitRoot:  oldProof,
			ProofRollupExitRoot: oldProof,
			MainnetExitRoot:     common.HexToHash("0x6666"),
			RollupExitRoot:      common.HexToHash("0x7777"),
			GlobalExitRoot:      common.HexToHash("0x8888"),
			DestinationNetwork:  4,
			Metadata:            []byte("metadata200"),
			IsMessage:           true,
			BlockTimestamp:      1500,
		},
		// Global index 100 - middle
		{
			BlockNum:            20,
			BlockPos:            0,
			TxHash:              common.HexToHash("0xa3"),
			GlobalIndex:         big.NewInt(100),
			OriginNetwork:       1,
			OriginAddress:       common.HexToAddress("0x1111"),
			DestinationAddress:  common.HexToAddress("0x2222"),
			Amount:              big.NewInt(1000),
			ProofLocalExitRoot:  oldProof,
			ProofRollupExitRoot: oldProof,
			MainnetExitRoot:     common.HexToHash("0x3333"),
			RollupExitRoot:      common.HexToHash("0x4444"),
			GlobalExitRoot:      common.HexToHash("0x5555"),
			DestinationNetwork:  2,
			Metadata:            []byte("should_not_matter"),
			IsMessage:           false,
			BlockTimestamp:      2000,
		},
		// Global index 200 - newest (has updated proofs)
		{
			BlockNum:            25,
			BlockPos:            0,
			TxHash:              common.HexToHash("0xa4"),
			GlobalIndex:         big.NewInt(200),
			OriginNetwork:       3,
			OriginAddress:       common.HexToAddress("0x3333"),
			DestinationAddress:  common.HexToAddress("0x4444"),
			Amount:              big.NewInt(2000),
			ProofLocalExitRoot:  newProof,                   // Updated proof
			ProofRollupExitRoot: newProof,                   // Updated proof
			MainnetExitRoot:     common.HexToHash("0x9999"), // Updated
			RollupExitRoot:      common.HexToHash("0xaaaa"), // Updated
			GlobalExitRoot:      common.HexToHash("0xbbbb"), // Updated
			DestinationNetwork:  4,
			Metadata:            []byte("should_not_matter"),
			IsMessage:           true,
			BlockTimestamp:      2500,
		},
		// Global index 100 - newest (has updated proofs)
		{
			BlockNum:            30,
			BlockPos:            0,
			TxHash:              common.HexToHash("0xa5"),
			GlobalIndex:         big.NewInt(100),
			OriginNetwork:       1,
			OriginAddress:       common.HexToAddress("0x1111"),
			DestinationAddress:  common.HexToAddress("0x2222"),
			Amount:              big.NewInt(1000),
			ProofLocalExitRoot:  newProof,                   // Updated proof
			ProofRollupExitRoot: newProof,                   // Updated proof
			MainnetExitRoot:     common.HexToHash("0xcccc"), // Updated
			RollupExitRoot:      common.HexToHash("0xdddd"), // Updated
			GlobalExitRoot:      common.HexToHash("0xeeee"), // Updated
			DestinationNetwork:  2,
			Metadata:            []byte("should_not_matter"),
			IsMessage:           false,
			BlockTimestamp:      3000,
		},
		// Global index 300 - single claim
		{
			BlockNum:            35,
			BlockPos:            0,
			TxHash:              common.HexToHash("0xa6"),
			GlobalIndex:         big.NewInt(300),
			OriginNetwork:       5,
			OriginAddress:       common.HexToAddress("0x5555"),
			DestinationAddress:  common.HexToAddress("0x6666"),
			Amount:              big.NewInt(3000),
			ProofLocalExitRoot:  newProof,
			ProofRollupExitRoot: newProof,
			MainnetExitRoot:     common.HexToHash("0xffff"),
			RollupExitRoot:      common.HexToHash("0x0000"),
			GlobalExitRoot:      common.HexToHash("0x1111"),
			DestinationNetwork:  6,
			Metadata:            []byte("metadata300"),
			IsMessage:           false,
			BlockTimestamp:      3500,
		},
		// Global index 400 - single claim
		{
			BlockNum:            5,
			BlockPos:            0,
			TxHash:              common.HexToHash("0xa7"),
			GlobalIndex:         big.NewInt(400),
			OriginNetwork:       7,
			OriginAddress:       common.HexToAddress("0x7777"),
			DestinationAddress:  common.HexToAddress("0x8888"),
			Amount:              big.NewInt(4000),
			ProofLocalExitRoot:  oldProof,
			ProofRollupExitRoot: oldProof,
			MainnetExitRoot:     common.HexToHash("0x2222"),
			RollupExitRoot:      common.HexToHash("0x3333"),
			GlobalExitRoot:      common.HexToHash("0x4444"),
			DestinationNetwork:  8,
			Metadata:            []byte("metadata400"),
			IsMessage:           true,
			BlockTimestamp:      500,
		},
	}

	// Insert all claims
	tx, err := p.db.BeginTx(ctx, nil)
	require.NoError(t, err)

	for i := uint64(1); i <= 40; i++ {
		_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, i)
		require.NoError(t, err)
	}

	for _, claim := range claims {
		require.NoError(t, meddler.Insert(tx, "claim", claim))
	}
	require.NoError(t, tx.Commit())

	// Test Page 1: Should return 3 compacted claims (300, 100 compacted, 200 compacted)
	t.Run("Page 1 - newest claims on page", func(t *testing.T) {
		result, count, err := p.GetClaimsPaged(ctx, 1, 3, nil, nil)
		require.NoError(t, err)
		require.Equal(t, 4, count) // Total compacted count: 4 distinct global_index values

		// Should get 3 claims: global_index 300, 100 (compacted), 200 (compacted)
		require.Len(t, result, 3)

		// Build a map for easier testing
		claimsByGlobalIndex := make(map[int64]*Claim)
		for _, claim := range result {
			claimsByGlobalIndex[claim.GlobalIndex.Int64()] = claim
		}

		// Verify we have the expected global indices
		require.Contains(t, claimsByGlobalIndex, int64(100))
		require.Contains(t, claimsByGlobalIndex, int64(200))
		require.Contains(t, claimsByGlobalIndex, int64(300))

		// Check global_index 300 (block 35)
		claim300 := claimsByGlobalIndex[300]
		require.Equal(t, uint64(35), claim300.BlockNum)
		require.Equal(t, []byte("metadata300"), claim300.Metadata)

		// Check global_index 100 (compacted: oldest block 10, newest proofs from block 30)
		claim100 := claimsByGlobalIndex[100]
		require.Equal(t, uint64(10), claim100.BlockNum)                        // Oldest claim's block
		require.Equal(t, common.HexToHash("0xa1"), claim100.TxHash)            // Oldest claim's tx
		require.Equal(t, []byte("old"), claim100.Metadata)                     // Oldest claim's metadata
		require.Equal(t, newProof, claim100.ProofLocalExitRoot)                // Newest claim's proof
		require.Equal(t, common.HexToHash("0xcccc"), claim100.MainnetExitRoot) // Newest claim's root

		// Check global_index 200 (compacted: oldest block 15, newest proofs from block 25)
		claim200 := claimsByGlobalIndex[200]
		require.Equal(t, uint64(15), claim200.BlockNum)                        // Oldest claim's block
		require.Equal(t, []byte("metadata200"), claim200.Metadata)             // Oldest claim's metadata
		require.Equal(t, newProof, claim200.ProofLocalExitRoot)                // Newest claim's proof
		require.Equal(t, common.HexToHash("0x9999"), claim200.MainnetExitRoot) // Newest claim's root
	})

	// Test Page 2: Should return 0 claims (all claims on this page are NOT the newest)
	t.Run("Page 2 - no newest claims on page", func(t *testing.T) {
		result, count, err := p.GetClaimsPaged(ctx, 2, 3, nil, nil)
		require.NoError(t, err)
		require.Equal(t, 4, count) // Total compacted count: 4 distinct global_index values

		// Should get 0 claims because blocks 20, 15, 10 are all older versions
		require.Len(t, result, 0)
	})

	// Test with larger page size that captures everything
	t.Run("Large page size - all newest claims", func(t *testing.T) {
		result, count, err := p.GetClaimsPaged(ctx, 1, 100, nil, nil)
		require.NoError(t, err)
		require.Equal(t, 4, count) // Total compacted count: 4 distinct global_index values

		// Should get 4 compacted claims: 300, 100, 200, 400
		require.Len(t, result, 4)

		globalIndices := make(map[int64]bool)
		for _, claim := range result {
			globalIndices[claim.GlobalIndex.Int64()] = true
		}

		require.True(t, globalIndices[100])
		require.True(t, globalIndices[200])
		require.True(t, globalIndices[300])
		require.True(t, globalIndices[400])
	})

	// Test with network IDs filter - should only return claims from networks 1 and 3
	t.Run("Filter by network IDs", func(t *testing.T) {
		networkIDs := []uint32{1, 3} // Only global_index 100 (network 1) and 200 (network 3)
		result, count, err := p.GetClaimsPaged(ctx, 1, 100, networkIDs, nil)
		require.NoError(t, err)
		require.Equal(t, 2, count) // 2 distinct global_index values (100 and 200) after compaction

		// Should get 2 compacted claims: 100 and 200
		require.Len(t, result, 2)

		globalIndices := make(map[int64]bool)
		for _, claim := range result {
			globalIndices[claim.GlobalIndex.Int64()] = true
		}

		require.True(t, globalIndices[100])
		require.True(t, globalIndices[200])
		require.False(t, globalIndices[300]) // Network 5 - excluded
		require.False(t, globalIndices[400]) // Network 7 - excluded
	})

	// Test with network IDs and specific global index filter
	t.Run("Filter by network IDs and global index", func(t *testing.T) {
		networkIDs := []uint32{1, 3, 5} // Networks that include our target global_index
		globalIndexFilter := big.NewInt(100)
		result, count, err := p.GetClaimsPaged(ctx, 1, 100, networkIDs, globalIndexFilter)
		require.NoError(t, err)
		require.Equal(t, 1, count) // 1 compacted claim with global_index 100

		// Should get 1 compacted claim: only global_index 100 (network 1 matches filter)
		require.Len(t, result, 1)
		require.Equal(t, big.NewInt(100), result[0].GlobalIndex)
		require.Equal(t, uint32(1), result[0].OriginNetwork)

		// Verify compaction: oldest metadata with newest proofs
		require.Equal(t, uint64(10), result[0].BlockNum)                        // Oldest claim's block
		require.Equal(t, common.HexToHash("0xa1"), result[0].TxHash)            // Oldest claim's tx
		require.Equal(t, []byte("old"), result[0].Metadata)                     // Oldest claim's metadata
		require.Equal(t, newProof, result[0].ProofLocalExitRoot)                // Newest claim's proof
		require.Equal(t, common.HexToHash("0xcccc"), result[0].MainnetExitRoot) // Newest claim's root
	})

	// Test with network IDs and global index that don't match
	t.Run("Filter by network IDs and global index - no match", func(t *testing.T) {
		networkIDs := []uint32{5, 7}         // Networks 5 and 7
		globalIndexFilter := big.NewInt(100) // But global_index 100 is on network 1
		result, count, err := p.GetClaimsPaged(ctx, 1, 100, networkIDs, globalIndexFilter)
		require.NoError(t, err)
		require.Equal(t, 0, count) // No claims match both filters

		require.Len(t, result, 0)
	})

	// ========== Additional comprehensive test cases (mirroring TestGetClaims_Compact) ==========

	// Test Case 1: Don't compact if unset_claim exists for global_index
	t.Run("Case 1: don't compact if unset_claim exists for global_index", func(t *testing.T) {
		// Create a new database for this test
		dbPath := filepath.Join(t.TempDir(), "case1.sqlite")
		require.NoError(t, migrations.RunMigrations(dbPath))
		testP, err := newProcessor(dbPath, "bridge-syncer", logger, dbQueryTimeout)
		require.NoError(t, err)

		// Setup: Insert 3 claims with same global_index and 1 unset_claim
		tx, err := testP.db.BeginTx(ctx, nil)
		require.NoError(t, err)

		for i := uint64(1); i <= 3; i++ {
			_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, i)
			require.NoError(t, err)
		}

		testClaims := []*Claim{
			{
				BlockNum:            1,
				BlockPos:            0,
				TxHash:              common.HexToHash("0x111"),
				GlobalIndex:         big.NewInt(1),
				OriginNetwork:       1,
				OriginAddress:       common.HexToAddress("0xaaa"),
				DestinationAddress:  common.HexToAddress("0xbbb"),
				Amount:              big.NewInt(100),
				ProofLocalExitRoot:  types.Proof{common.HexToHash("0x1a")},
				ProofRollupExitRoot: types.Proof{common.HexToHash("0x1b")},
				MainnetExitRoot:     common.HexToHash("0x1c"),
				RollupExitRoot:      common.HexToHash("0x1d"),
				GlobalExitRoot:      common.HexToHash("0x1e"),
				DestinationNetwork:  2,
				Metadata:            []byte("metadata1"),
				IsMessage:           false,
				BlockTimestamp:      1000,
			},
			{
				BlockNum:            2,
				BlockPos:            0,
				TxHash:              common.HexToHash("0x222"),
				GlobalIndex:         big.NewInt(1),
				OriginNetwork:       3,
				OriginAddress:       common.HexToAddress("0xccc"),
				DestinationAddress:  common.HexToAddress("0xddd"),
				Amount:              big.NewInt(200),
				ProofLocalExitRoot:  types.Proof{common.HexToHash("0x2a")},
				ProofRollupExitRoot: types.Proof{common.HexToHash("0x2b")},
				MainnetExitRoot:     common.HexToHash("0x2c"),
				RollupExitRoot:      common.HexToHash("0x2d"),
				GlobalExitRoot:      common.HexToHash("0x2e"),
				DestinationNetwork:  4,
				Metadata:            []byte("metadata2"),
				IsMessage:           true,
				BlockTimestamp:      2000,
			},
		}

		for _, claim := range testClaims {
			require.NoError(t, meddler.Insert(tx, "claim", claim))
		}

		// Insert unset_claim for global_index 1
		unsetClaim := &UnsetClaim{
			BlockNum:    1,
			BlockPos:    1,
			GlobalIndex: big.NewInt(1),
		}
		require.NoError(t, meddler.Insert(tx, "unset_claim", unsetClaim))
		require.NoError(t, tx.Commit())

		// Query: Should return all claims uncompacted because unset_claim exists
		result, count, err := testP.GetClaimsPaged(ctx, 1, 10, nil, nil)
		require.NoError(t, err)
		require.Equal(t, 2, count)
		require.Len(t, result, 2)
		require.Equal(t, result[0], testClaims[1]) // they are returned in DESC order
		require.Equal(t, result[1], testClaims[0])
	})

	// Test Case 2: Compact if no unset_claim exists
	t.Run("Case 2: compact if no unset_claim exists", func(t *testing.T) {
		// Create a new database for this test
		dbPath := filepath.Join(t.TempDir(), "case2.sqlite")
		require.NoError(t, migrations.RunMigrations(dbPath))
		testP, err := newProcessor(dbPath, "bridge-syncer", logger, dbQueryTimeout)
		require.NoError(t, err)

		// Setup: Insert 3 claims with same global_index, NO unset_claim
		tx, err := testP.db.BeginTx(ctx, nil)
		require.NoError(t, err)

		for i := uint64(1); i <= 3; i++ {
			_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, i)
			require.NoError(t, err)
		}

		testClaims := []*Claim{
			{
				BlockNum:            1,
				BlockPos:            0,
				TxHash:              common.HexToHash("0x111"),
				GlobalIndex:         big.NewInt(100),
				OriginNetwork:       1,
				OriginAddress:       common.HexToAddress("0xaaa"),
				DestinationAddress:  common.HexToAddress("0xbbb"),
				Amount:              big.NewInt(100),
				ProofLocalExitRoot:  types.Proof{common.HexToHash("0x1a")},
				ProofRollupExitRoot: types.Proof{common.HexToHash("0x1b")},
				MainnetExitRoot:     common.HexToHash("0x1c"),
				RollupExitRoot:      common.HexToHash("0x1d"),
				GlobalExitRoot:      common.HexToHash("0x1e"),
				DestinationNetwork:  2,
				Metadata:            []byte("original_metadata"),
				IsMessage:           false,
				BlockTimestamp:      1000,
			},
			{
				BlockNum:            2,
				BlockPos:            0,
				TxHash:              common.HexToHash("0x222"),
				GlobalIndex:         big.NewInt(100),
				OriginNetwork:       99,
				OriginAddress:       common.HexToAddress("0xfff"),
				DestinationAddress:  common.HexToAddress("0xeee"),
				Amount:              big.NewInt(999),
				ProofLocalExitRoot:  types.Proof{common.HexToHash("0x2a")},
				ProofRollupExitRoot: types.Proof{common.HexToHash("0x2b")},
				MainnetExitRoot:     common.HexToHash("0x2c"),
				RollupExitRoot:      common.HexToHash("0x2d"),
				GlobalExitRoot:      common.HexToHash("0x2e"),
				DestinationNetwork:  88,
				Metadata:            []byte("middle_metadata"),
				IsMessage:           true,
				BlockTimestamp:      2000,
			},
			{
				BlockNum:            3,
				BlockPos:            0,
				TxHash:              common.HexToHash("0x333"),
				GlobalIndex:         big.NewInt(100),
				OriginNetwork:       77,
				OriginAddress:       common.HexToAddress("0x999"),
				DestinationAddress:  common.HexToAddress("0x888"),
				Amount:              big.NewInt(777),
				ProofLocalExitRoot:  types.Proof{common.HexToHash("0x3a")},
				ProofRollupExitRoot: types.Proof{common.HexToHash("0x3b")},
				MainnetExitRoot:     common.HexToHash("0x3c"),
				RollupExitRoot:      common.HexToHash("0x3d"),
				GlobalExitRoot:      common.HexToHash("0x3e"),
				DestinationNetwork:  66,
				Metadata:            []byte("newest_metadata"),
				IsMessage:           true,
				BlockTimestamp:      3000,
			},
		}

		for _, claim := range testClaims {
			require.NoError(t, meddler.Insert(tx, "claim", claim))
		}
		require.NoError(t, tx.Commit())

		// Query: Should return 1 compacted claim (oldest metadata + newest proofs)
		result, count, err := testP.GetClaimsPaged(ctx, 1, 10, nil, nil)
		require.NoError(t, err)
		require.Equal(t, 1, count) // 1 compacted claim (3 raw claims compacted to 1)
		require.Len(t, result, 1)

		// Verify compaction: oldest claim's metadata with newest claim's proofs
		require.Equal(t, big.NewInt(100), result[0].GlobalIndex)
		require.Equal(t, uint64(1), result[0].BlockNum)                                       // Oldest claim's block
		require.Equal(t, common.HexToHash("0x111"), result[0].TxHash)                         // Oldest claim's tx
		require.Equal(t, []byte("original_metadata"), result[0].Metadata)                     // Oldest claim's metadata
		require.Equal(t, types.Proof{common.HexToHash("0x3a")}, result[0].ProofLocalExitRoot) // Newest claim's proof
		require.Equal(t, common.HexToHash("0x3c"), result[0].MainnetExitRoot)                 // Newest claim's root
	})

	// Test Case 3: Don't return if newest is not on the page
	// Original test intent: Verify that when the newest claim is not on the requested page,
	// we return 0 results (even though older claims for that global_index might be on the page)
	// Note: With compacted count, we need multiple global_indexes to create valid pagination
	t.Run("Case 3: don't return if newest is not on the page", func(t *testing.T) {
		// Create a new database for this test
		dbPath := filepath.Join(t.TempDir(), "case3.sqlite")
		require.NoError(t, migrations.RunMigrations(dbPath))
		testP, err := newProcessor(dbPath, "bridge-syncer", logger, dbQueryTimeout)
		require.NoError(t, err)

		// Setup: Insert claims with two global_indexes to create valid pagination
		// global_index 100: blocks 1 (oldest), 2, 3 (newest) - newest on page 1
		// global_index 200: blocks 4 (oldest), 5 (newest) - newest on page 2
		tx, err := testP.db.BeginTx(ctx, nil)
		require.NoError(t, err)

		for i := uint64(1); i <= 5; i++ {
			_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, i)
			require.NoError(t, err)
		}

		testClaims := []*Claim{
			// Global index 100 - oldest (block 1)
			{
				BlockNum:            1,
				BlockPos:            0,
				TxHash:              common.HexToHash("0x111"),
				GlobalIndex:         big.NewInt(100),
				OriginNetwork:       1,
				OriginAddress:       common.HexToAddress("0xaaa"),
				DestinationAddress:  common.HexToAddress("0xbbb"),
				Amount:              big.NewInt(100),
				ProofLocalExitRoot:  types.Proof{common.HexToHash("0x1a")},
				ProofRollupExitRoot: types.Proof{common.HexToHash("0x1b")},
				MainnetExitRoot:     common.HexToHash("0x1c"),
				RollupExitRoot:      common.HexToHash("0x1d"),
				GlobalExitRoot:      common.HexToHash("0x1e"),
				DestinationNetwork:  2,
				Metadata:            []byte("original_metadata"),
				IsMessage:           false,
				BlockTimestamp:      1000,
			},
			// Global index 100 - middle (block 2)
			{
				BlockNum:            2,
				BlockPos:            0,
				TxHash:              common.HexToHash("0x222"),
				GlobalIndex:         big.NewInt(100),
				OriginNetwork:       99,
				OriginAddress:       common.HexToAddress("0xfff"),
				DestinationAddress:  common.HexToAddress("0xeee"),
				Amount:              big.NewInt(999),
				ProofLocalExitRoot:  types.Proof{common.HexToHash("0x2a")},
				ProofRollupExitRoot: types.Proof{common.HexToHash("0x2b")},
				MainnetExitRoot:     common.HexToHash("0x2c"),
				RollupExitRoot:      common.HexToHash("0x2d"),
				GlobalExitRoot:      common.HexToHash("0x2e"),
				DestinationNetwork:  88,
				Metadata:            []byte("middle_metadata"),
				IsMessage:           true,
				BlockTimestamp:      2000,
			},
			// Global index 100 - newest (block 3)
			{
				BlockNum:            3,
				BlockPos:            0,
				TxHash:              common.HexToHash("0x333"),
				GlobalIndex:         big.NewInt(100),
				OriginNetwork:       77,
				OriginAddress:       common.HexToAddress("0x999"),
				DestinationAddress:  common.HexToAddress("0x888"),
				Amount:              big.NewInt(777),
				ProofLocalExitRoot:  types.Proof{common.HexToHash("0x3a")},
				ProofRollupExitRoot: types.Proof{common.HexToHash("0x3b")},
				MainnetExitRoot:     common.HexToHash("0x3c"),
				RollupExitRoot:      common.HexToHash("0x3d"),
				GlobalExitRoot:      common.HexToHash("0x3e"),
				DestinationNetwork:  66,
				Metadata:            []byte("newest_metadata"),
				IsMessage:           true,
				BlockTimestamp:      3000,
			},
			// Global index 200 - oldest (block 4)
			{
				BlockNum:            4,
				BlockPos:            0,
				TxHash:              common.HexToHash("0x444"),
				GlobalIndex:         big.NewInt(200),
				OriginNetwork:       2,
				OriginAddress:       common.HexToAddress("0xaaa"),
				DestinationAddress:  common.HexToAddress("0xbbb"),
				Amount:              big.NewInt(200),
				ProofLocalExitRoot:  types.Proof{common.HexToHash("0x4a")},
				ProofRollupExitRoot: types.Proof{common.HexToHash("0x4b")},
				MainnetExitRoot:     common.HexToHash("0x4c"),
				RollupExitRoot:      common.HexToHash("0x4d"),
				GlobalExitRoot:      common.HexToHash("0x4e"),
				DestinationNetwork:  3,
				Metadata:            []byte("index200_old"),
				IsMessage:           false,
				BlockTimestamp:      4000,
			},
			// Global index 200 - newest (block 5)
			{
				BlockNum:            5,
				BlockPos:            0,
				TxHash:              common.HexToHash("0x555"),
				GlobalIndex:         big.NewInt(200),
				OriginNetwork:       2,
				OriginAddress:       common.HexToAddress("0xccc"),
				DestinationAddress:  common.HexToAddress("0xddd"),
				Amount:              big.NewInt(200),
				ProofLocalExitRoot:  types.Proof{common.HexToHash("0x5a")},
				ProofRollupExitRoot: types.Proof{common.HexToHash("0x5b")},
				MainnetExitRoot:     common.HexToHash("0x5c"),
				RollupExitRoot:      common.HexToHash("0x5d"),
				GlobalExitRoot:      common.HexToHash("0x5e"),
				DestinationNetwork:  3,
				Metadata:            []byte("index200_new"),
				IsMessage:           false,
				BlockTimestamp:      5000,
			},
		}

		for _, claim := range testClaims {
			require.NoError(t, meddler.Insert(tx, "claim", claim))
		}
		require.NoError(t, tx.Commit())

		// Query page 1 (size 1): Contains block 5 (newest for global_index 200)
		// global_index 200's newest (block 5) is on page 1 → should return compacted claim
		// global_index 100's newest (block 3) is NOT on page 1 → should NOT return
		result, count, err := testP.GetClaimsPaged(ctx, 1, 1, nil, nil)
		require.NoError(t, err)
		require.Equal(t, 2, count) // 2 distinct global_index values (100 and 200)

		// Should return 1 claim: global_index 200 (its newest is on page 1)
		require.Len(t, result, 1)
		require.Equal(t, big.NewInt(200), result[0].GlobalIndex)

		// Query page 2 (size 1): Contains block 4 (oldest for global_index 200, not newest)
		// This tests the original Case 3 concept: when the newest is NOT on the page, return 0
		// global_index 200's newest (block 5) is NOT on page 2 → should NOT return
		// global_index 100's newest (block 3) is NOT on page 2 → should NOT return
		result2, count2, err := testP.GetClaimsPaged(ctx, 2, 1, nil, nil)
		require.NoError(t, err)
		require.Equal(t, 2, count2) // Same total count

		// Should return 0 claims because neither newest is on page 2
		// This preserves the original test's essence: don't return if newest is not on page
		require.Len(t, result2, 0)
	})

	// Test Case 3 Exception: Return if unset_claim exists even when globally oldest is outside range
	t.Run("Case 3 exception: return if unset_claim exists even when globally oldest is outside range", func(t *testing.T) {
		// Create a new database for this test
		dbPath := filepath.Join(t.TempDir(), "case3_exception.sqlite")
		require.NoError(t, migrations.RunMigrations(dbPath))
		testP, err := newProcessor(dbPath, "bridge-syncer", logger, dbQueryTimeout)
		require.NoError(t, err)

		// Setup: Insert claims + unset_claim
		tx, err := testP.db.BeginTx(ctx, nil)
		require.NoError(t, err)

		for i := uint64(1); i <= 3; i++ {
			_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, i)
			require.NoError(t, err)
		}

		testClaims := []*Claim{
			// Oldest claim (block 1)
			{
				BlockNum:            1,
				BlockPos:            0,
				TxHash:              common.HexToHash("0x111"),
				GlobalIndex:         big.NewInt(1),
				OriginNetwork:       1,
				OriginAddress:       common.HexToAddress("0xaaa"),
				DestinationAddress:  common.HexToAddress("0xbbb"),
				Amount:              big.NewInt(100),
				ProofLocalExitRoot:  types.Proof{common.HexToHash("0x1a")},
				ProofRollupExitRoot: types.Proof{common.HexToHash("0x1b")},
				MainnetExitRoot:     common.HexToHash("0x1c"),
				RollupExitRoot:      common.HexToHash("0x1d"),
				GlobalExitRoot:      common.HexToHash("0x1e"),
				DestinationNetwork:  2,
				Metadata:            []byte("metadata1"),
				IsMessage:           false,
				BlockTimestamp:      1000,
			},
			// Newest claim (block 3)
			{
				BlockNum:            3,
				BlockPos:            0,
				TxHash:              common.HexToHash("0x333"),
				GlobalIndex:         big.NewInt(100),
				OriginNetwork:       77,
				OriginAddress:       common.HexToAddress("0x999"),
				DestinationAddress:  common.HexToAddress("0x888"),
				Amount:              big.NewInt(777),
				ProofLocalExitRoot:  types.Proof{common.HexToHash("0x3a")},
				ProofRollupExitRoot: types.Proof{common.HexToHash("0x3b")},
				MainnetExitRoot:     common.HexToHash("0x3c"),
				RollupExitRoot:      common.HexToHash("0x3d"),
				GlobalExitRoot:      common.HexToHash("0x3e"),
				DestinationNetwork:  66,
				Metadata:            []byte("newest_metadata"),
				IsMessage:           true,
				BlockTimestamp:      3000,
			},
		}

		for _, claim := range testClaims {
			require.NoError(t, meddler.Insert(tx, "claim", claim))
		}

		// Insert unset_claim for global_index 1
		unsetClaim := &UnsetClaim{
			BlockNum:    1,
			BlockPos:    1,
			GlobalIndex: big.NewInt(1),
		}
		require.NoError(t, meddler.Insert(tx, "unset_claim", unsetClaim))
		require.NoError(t, tx.Commit())

		// Query: Even though oldest is outside page, should return claim because unset_claim exists
		result, count, err := testP.GetClaimsPaged(ctx, 1, 10, nil, nil)
		require.NoError(t, err)
		require.Equal(t, 2, count)

		// Should return both claims uncompacted
		require.Len(t, result, 2)
		require.Equal(t, result[0], testClaims[1]) // they are returned in DESC order
		require.Equal(t, result[1], testClaims[0])
	})

	// Test: Multiple global_indexes with different compaction rules
	t.Run("Multiple global_indexes with different compaction rules", func(t *testing.T) {
		// Create a new database for this test
		dbPath := filepath.Join(t.TempDir(), "multiple_indexes.sqlite")
		require.NoError(t, migrations.RunMigrations(dbPath))
		testP, err := newProcessor(dbPath, "bridge-syncer", logger, dbQueryTimeout)
		require.NoError(t, err)

		// Setup: Multiple global indexes with different scenarios
		tx, err := testP.db.BeginTx(ctx, nil)
		require.NoError(t, err)

		for i := uint64(1); i <= 3; i++ {
			_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, i)
			require.NoError(t, err)
		}

		testClaims := []*Claim{
			// Global index 100 - oldest (block 1, pos 0)
			{
				BlockNum:            1,
				BlockPos:            0,
				TxHash:              common.HexToHash("0x111"),
				GlobalIndex:         big.NewInt(100),
				OriginNetwork:       1,
				OriginAddress:       common.HexToAddress("0xa1"),
				DestinationAddress:  common.HexToAddress("0xb1"),
				Amount:              big.NewInt(100),
				ProofLocalExitRoot:  types.Proof{common.HexToHash("0x1a")},
				ProofRollupExitRoot: types.Proof{common.HexToHash("0x1b")},
				MainnetExitRoot:     common.HexToHash("0x1c"),
				RollupExitRoot:      common.HexToHash("0x1d"),
				GlobalExitRoot:      common.HexToHash("0x1e"),
				DestinationNetwork:  2,
				Metadata:            []byte("index1_old"),
				IsMessage:           false,
				BlockTimestamp:      1000,
			},
			// Global index 200 - oldest (block 1, pos 1)
			{
				BlockNum:            1,
				BlockPos:            1,
				TxHash:              common.HexToHash("0x112"),
				GlobalIndex:         big.NewInt(200),
				OriginNetwork:       3,
				OriginAddress:       common.HexToAddress("0xa2"),
				DestinationAddress:  common.HexToAddress("0xb2"),
				Amount:              big.NewInt(200),
				ProofLocalExitRoot:  types.Proof{common.HexToHash("0x2a")},
				ProofRollupExitRoot: types.Proof{common.HexToHash("0x2b")},
				MainnetExitRoot:     common.HexToHash("0x2c"),
				RollupExitRoot:      common.HexToHash("0x2d"),
				GlobalExitRoot:      common.HexToHash("0x2e"),
				DestinationNetwork:  4,
				Metadata:            []byte("index2_old"),
				IsMessage:           true,
				BlockTimestamp:      1001,
			},
			// Global index 200 - newest (block 3, pos 1)
			{
				BlockNum:            3,
				BlockPos:            1,
				TxHash:              common.HexToHash("0x112"),
				GlobalIndex:         big.NewInt(200),
				OriginNetwork:       3,
				OriginAddress:       common.HexToAddress("0xccc"),
				DestinationAddress:  common.HexToAddress("0xddd"),
				Amount:              big.NewInt(200),
				ProofLocalExitRoot:  types.Proof{common.HexToHash("0x2ab")},
				ProofRollupExitRoot: types.Proof{common.HexToHash("0x2bc")},
				MainnetExitRoot:     common.HexToHash("0x2ce"),
				RollupExitRoot:      common.HexToHash("0x2df"),
				GlobalExitRoot:      common.HexToHash("0x2ee"),
				DestinationNetwork:  88,
				Metadata:            []byte("block3pos1"),
				IsMessage:           true,
				BlockTimestamp:      3001,
			},
			// Global index 100 - newest (block 3, pos 0)
			{
				BlockNum:            3,
				BlockPos:            0,
				TxHash:              common.HexToHash("0x333"),
				GlobalIndex:         big.NewInt(100),
				OriginNetwork:       77,
				OriginAddress:       common.HexToAddress("0xc2"),
				DestinationAddress:  common.HexToAddress("0xd2"),
				Amount:              big.NewInt(777),
				ProofLocalExitRoot:  types.Proof{common.HexToHash("0x4a")},
				ProofRollupExitRoot: types.Proof{common.HexToHash("0x4b")},
				MainnetExitRoot:     common.HexToHash("0x4c"),
				RollupExitRoot:      common.HexToHash("0x4d"),
				GlobalExitRoot:      common.HexToHash("0x4e"),
				DestinationNetwork:  66,
				Metadata:            []byte("index1_new"),
				IsMessage:           false,
				BlockTimestamp:      2001,
			},
		}

		for _, claim := range testClaims {
			require.NoError(t, meddler.Insert(tx, "claim", claim))
		}

		// Insert unset_claim for global_index 100 only
		unsetClaim := &UnsetClaim{
			BlockNum:    1,
			BlockPos:    1,
			GlobalIndex: big.NewInt(100),
		}
		require.NoError(t, meddler.Insert(tx, "unset_claim", unsetClaim))
		require.NoError(t, tx.Commit())

		// Query: Should return:
		// - Global index 100: both claims uncompacted (because unset_claim exists) -> count as 2
		// - Global index 200: 1 compacted claim -> count as 1
		result, count, err := testP.GetClaimsPaged(ctx, 1, 10, nil, nil)
		require.NoError(t, err)
		require.Equal(t, 3, count) // 2 (unset_claim) + 1 (compacted) = 3
		require.Len(t, result, 3)  // 2 for index 100 (uncompacted) + 1 for index 200 (compacted)

		// Count claims by global index
		claimsByGlobalIndex := make(map[int64]int)
		for _, claim := range result {
			claimsByGlobalIndex[claim.GlobalIndex.Int64()]++
		}

		require.Equal(t, 2, claimsByGlobalIndex[100]) // Uncompacted
		require.Equal(t, 1, claimsByGlobalIndex[200]) // Compacted
	})
}

// TestClaimColumnsSQL_ReflectionCheck verifies that all meddler-tagged fields
// in the Claim struct are present in the claimColumnsSQL constant.
// This test uses reflection to ensure maintainability - if a new field is added
// to Claim with a meddler tag, this test will fail until claimColumnsSQL is updated.
func TestClaimColumnsSQL_ReflectionCheck(t *testing.T) {
	t.Parallel()

	claimType := reflect.TypeFor[Claim]()

	// Collect meddler-tagged column names
	var meddlerColumns []string
	for i := 0; i < claimType.NumField(); i++ {
		tag := claimType.Field(i).Tag.Get("meddler")
		if tag == "" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name != "" && name != "-" {
			meddlerColumns = append(meddlerColumns, name)
		}
	}

	require.NotEmpty(t, meddlerColumns, "Claim struct should have meddler-tagged fields")

	// Normalize whitespace and split columns
	ws := regexp.MustCompile(`\s+`)
	normalized := strings.TrimSpace(ws.ReplaceAllString(claimColumnsSQL, " "))

	var sqlColumns []string
	for col := range strings.SplitSeq(normalized, ",") {
		if col = strings.TrimSpace(col); col != "" {
			sqlColumns = append(sqlColumns, col)
		}
	}

	require.Equal(t, len(meddlerColumns), len(sqlColumns),
		"SQL column count must match meddler-tagged field count")

	// Turn SQL columns into a lookup set
	sqlSet := make(map[string]struct{}, len(sqlColumns))
	for _, col := range sqlColumns {
		sqlSet[col] = struct{}{}
	}

	// Ensure every struct tag column exists in SQL
	for _, col := range meddlerColumns {
		_, ok := sqlSet[col]
		require.True(t, ok, "Missing SQL column for meddler-tag '%s'", col)
	}
}

func TestProcessor_BackwardLET(t *testing.T) {
	buildBlocksWithSequentialBridges := func(blocksCount, bridgesPerBlock uint64,
		blockNumOffset uint64, depositCountOffset uint32) []sync.Block {
		blocks := make([]sync.Block, 0, blocksCount)
		depositCount := depositCountOffset
		for i := range blocksCount {
			blockNum := i + 1 + blockNumOffset
			block := sync.Block{
				Num:  blockNum,
				Hash: common.HexToHash(fmt.Sprintf("%x", blockNum)),
			}
			for blockPos := range bridgesPerBlock {
				block.Events = append(block.Events,
					Event{Bridge: &Bridge{
						BlockNum:     blockNum,
						BlockPos:     blockPos,
						DepositCount: depositCount,
					}})

				depositCount++
			}

			blocks = append(blocks, block)
		}
		return blocks
	}

	collectExpectedBridgesUpTo := func(t *testing.T, blocks []sync.Block,
		skipBlocks []uint64, targetDepositCount uint32) []Bridge {
		t.Helper()

		bridges := make([]Bridge, 0)
		for _, b := range blocks {
			if slices.Contains(skipBlocks, b.Num) {
				continue
			}

			for _, e := range b.Events {
				evt, ok := e.(Event)
				require.True(t, ok)
				if evt.Bridge != nil {
					bridges = append(bridges, *evt.Bridge)
					if evt.Bridge.DepositCount == targetDepositCount {
						return bridges
					}
				}
			}
		}
		return bridges
	}

	testCases := []struct {
		name                  string
		setupBlocks           func() []sync.Block
		firstReorgedBlock     *uint64
		targetDepositCount    uint32
		skipBlocks            []uint64
		archivedDepositCounts []uint32
		processBlockErrMsg    string
	}{
		{
			name: "backward let after a couple of bridges",
			setupBlocks: func() []sync.Block {
				blocks := buildBlocksWithSequentialBridges(3, 2, 0, 0)
				blocks = append(blocks, sync.Block{
					Num:  uint64(len(blocks) + 1),
					Hash: common.HexToHash(fmt.Sprintf("0x%x", len(blocks)+1)),
					Events: []any{
						Event{BackwardLET: &BackwardLET{
							BlockNum:             uint64(len(blocks) + 1),
							BlockPos:             0,
							PreviousDepositCount: big.NewInt(3),
							NewDepositCount:      big.NewInt(2),
							PreviousRoot:         common.HexToHash("0x9ba667158a062be548e5c1b2e8a9a2ad03b693e562535b0723880627c6664b02"),
							NewRoot:              common.HexToHash("0x0cc5d7d6281795bc0a4d3dff706ef63097c4eb288a311aa2b3098e838f9d9248"),
						}},
					},
				})

				return blocks
			},
			targetDepositCount:    1,
			archivedDepositCounts: []uint32{2, 3, 4, 5},
		},
		{
			name: "backward let event with all the bridges, except the first one",
			setupBlocks: func() []sync.Block {
				blocks := buildBlocksWithSequentialBridges(3, 2, 0, 0)
				blocks = append(blocks, sync.Block{
					Num:  uint64(len(blocks) + 1),
					Hash: common.HexToHash(fmt.Sprintf("0x%x", len(blocks)+1)),
					Events: []any{
						Event{BackwardLET: &BackwardLET{
							BlockNum:             uint64(len(blocks) + 1),
							BlockPos:             0,
							PreviousDepositCount: big.NewInt(5),
							NewDepositCount:      big.NewInt(1),
							PreviousRoot:         common.HexToHash("0x9ba667158a062be548e5c1b2e8a9a2ad03b693e562535b0723880627c6664b02"),
							NewRoot:              common.HexToHash("0x283c52c3d10a22d01f95f5bcab5e823675c9855bd40b1e82f32b0437b3b6a446"),
						}},
					},
				})

				return blocks
			},
			targetDepositCount:    0,
			archivedDepositCounts: []uint32{1, 2, 3, 4, 5},
		},
		{
			name: "backward let event (only the last bridge)",
			setupBlocks: func() []sync.Block {
				blocks := buildBlocksWithSequentialBridges(3, 2, 0, 0)
				backwardLETBlock := sync.Block{
					Num:  uint64(len(blocks) + 1),
					Hash: common.HexToHash(fmt.Sprintf("0x%x", len(blocks)+1)),
					Events: []any{
						Event{BackwardLET: &BackwardLET{
							BlockNum:             uint64(len(blocks) + 1),
							BlockPos:             0,
							PreviousDepositCount: big.NewInt(5),
							NewDepositCount:      big.NewInt(4),
							PreviousRoot:         common.HexToHash("0x9ba667158a062be548e5c1b2e8a9a2ad03b693e562535b0723880627c6664b02"),
							NewRoot:              common.HexToHash("0x7533c9ef58edd0bea7959a20c33ed47e5548d35f4ff140c5c915740fe6800fb8"),
						}},
					},
				}
				blocks = append(blocks, backwardLETBlock)

				return blocks
			},
			targetDepositCount:    3,
			archivedDepositCounts: []uint32{4, 5},
		},
		{
			name: "backward let event in the middle of bridges",
			setupBlocks: func() []sync.Block {
				blocks := buildBlocksWithSequentialBridges(3, 2, 0, 0)
				backwardLETBlock := sync.Block{
					Num:  uint64(len(blocks) + 1),
					Hash: common.HexToHash(fmt.Sprintf("0x%x", len(blocks)+1)),
					Events: []any{
						Event{BackwardLET: &BackwardLET{
							BlockNum:             uint64(len(blocks) + 1),
							BlockPos:             0,
							PreviousDepositCount: big.NewInt(5),
							NewDepositCount:      big.NewInt(2),
							PreviousRoot:         common.HexToHash("0x9ba667158a062be548e5c1b2e8a9a2ad03b693e562535b0723880627c6664b02"),
							NewRoot:              common.HexToHash("0x0cc5d7d6281795bc0a4d3dff706ef63097c4eb288a311aa2b3098e838f9d9248"),
						}},
					},
				}
				blocks = append(blocks, backwardLETBlock)
				blocks = append(blocks, buildBlocksWithSequentialBridges(3, 2, uint64(len(blocks)), 2)...)

				return blocks
			},
			targetDepositCount:    7,
			skipBlocks:            []uint64{2, 3}, // all the bridges from these blocks were backwarded
			archivedDepositCounts: []uint32{2, 3, 4, 5},
		},
		{
			name: "overlapping backward let events",
			setupBlocks: func() []sync.Block {
				blocks := buildBlocksWithSequentialBridges(3, 2, 0, 0)
				blocks = append(blocks, sync.Block{
					Num:  uint64(len(blocks) + 1),
					Hash: common.HexToHash(fmt.Sprintf("0x%x", len(blocks)+1)),
					Events: []any{
						Event{BackwardLET: &BackwardLET{
							BlockNum:             uint64(len(blocks) + 1),
							BlockPos:             0,
							PreviousDepositCount: big.NewInt(5),
							NewDepositCount:      big.NewInt(3),
							PreviousRoot:         common.HexToHash("0x9ba667158a062be548e5c1b2e8a9a2ad03b693e562535b0723880627c6664b02"),
							NewRoot:              common.HexToHash("0xa9d31ebbb97c7cd7c7103bee8af7d0b4c83771939baba0b415b0f94c4c39fd84"),
						}},
					},
				})
				blocks = append(blocks, sync.Block{
					Num:  uint64(len(blocks) + 2),
					Hash: common.HexToHash(fmt.Sprintf("0x%x", len(blocks)+2)),
					Events: []any{
						Event{BackwardLET: &BackwardLET{
							BlockNum:             uint64(len(blocks) + 2),
							BlockPos:             0,
							PreviousDepositCount: big.NewInt(4),
							NewDepositCount:      big.NewInt(3),
							PreviousRoot:         common.HexToHash("0xa9d31ebbb97c7cd7c7103bee8af7d0b4c83771939baba0b415b0f94c4c39fd84"),
							NewRoot:              common.HexToHash("0xa9d31ebbb97c7cd7c7103bee8af7d0b4c83771939baba0b415b0f94c4c39fd84"),
						}},
					},
				})

				return blocks
			},
			targetDepositCount:    2,
			archivedDepositCounts: []uint32{3, 4, 5},
		},
		{
			name: "backward let on empty bridge table",
			setupBlocks: func() []sync.Block {
				return []sync.Block{
					{
						Num:  1,
						Hash: common.HexToHash("0x1"),
						Events: []any{
							Event{BackwardLET: &BackwardLET{
								BlockNum:             1,
								BlockPos:             0,
								PreviousDepositCount: big.NewInt(6),
								NewDepositCount:      big.NewInt(3),
							}},
						},
					}}
			},
			targetDepositCount: 0,
		},
		{
			name: "backward let invalid new deposit count (outside of uint64 range)",
			setupBlocks: func() []sync.Block {
				return []sync.Block{
					{
						Num:  1,
						Hash: common.HexToHash("0x1"),
						Events: []any{
							Event{BackwardLET: &BackwardLET{
								BlockNum:             1,
								BlockPos:             0,
								PreviousDepositCount: big.NewInt(0),
								NewDepositCount:      big.NewInt(-3),
							}},
						},
					}}
			},
			processBlockErrMsg: "invalid deposit count: value=-3 does not fit in uint64",
		},
		{
			name: "backward let invalid new deposit count (outside of uint32 range)",
			setupBlocks: func() []sync.Block {
				return []sync.Block{
					{
						Num:  1,
						Hash: common.HexToHash("0x1"),
						Events: []any{
							Event{BackwardLET: &BackwardLET{
								BlockNum:             1,
								BlockPos:             0,
								PreviousDepositCount: big.NewInt(0),
								NewDepositCount:      new(big.Int).SetUint64(uint64(math.MaxUint32) + 1),
							}},
						},
					}}
			},
			processBlockErrMsg: "invalid deposit count: value=4294967296 exceeds uint32 max",
		},
		{
			name: "backward let after a couple of bridges + reorg backward let",
			setupBlocks: func() []sync.Block {
				blocks := buildBlocksWithSequentialBridges(3, 2, 0, 0)
				backwardLETBlock := sync.Block{
					Num:  uint64(len(blocks) + 1),
					Hash: common.HexToHash(fmt.Sprintf("0x%x", len(blocks)+1)),
					Events: []any{
						Event{BackwardLET: &BackwardLET{
							BlockNum:             4,
							BlockPos:             0,
							PreviousDepositCount: big.NewInt(5),
							NewDepositCount:      big.NewInt(2),
							PreviousRoot:         common.HexToHash("0x9ba667158a062be548e5c1b2e8a9a2ad03b693e562535b0723880627c6664b02"),
							NewRoot:              common.HexToHash("0x0cc5d7d6281795bc0a4d3dff706ef63097c4eb288a311aa2b3098e838f9d9248"),
						}},
					},
				}
				blocks = append(blocks, backwardLETBlock)

				return blocks
			},
			firstReorgedBlock:     uint64Ptr(3),
			targetDepositCount:    3,
			archivedDepositCounts: []uint32{2, 3, 4, 5},
		},
		{
			name: "backward let event in the middle of bridges + reorg backward let",
			setupBlocks: func() []sync.Block {
				blocks := buildBlocksWithSequentialBridges(3, 2, 0, 0)
				backwardLETBlock := sync.Block{
					Num:  uint64(len(blocks) + 1),
					Hash: common.HexToHash(fmt.Sprintf("0x%x", len(blocks)+1)),
					Events: []any{
						Event{BackwardLET: &BackwardLET{
							BlockNum:             uint64(len(blocks) + 1),
							BlockPos:             0,
							PreviousDepositCount: big.NewInt(5),
							NewDepositCount:      big.NewInt(2),
							PreviousRoot:         common.HexToHash("0x9ba667158a062be548e5c1b2e8a9a2ad03b693e562535b0723880627c6664b02"),
							NewRoot:              common.HexToHash("0x0cc5d7d6281795bc0a4d3dff706ef63097c4eb288a311aa2b3098e838f9d9248"),
						}},
					},
				}
				blocks = append(blocks, backwardLETBlock)
				blocks = append(blocks, buildBlocksWithSequentialBridges(3, 2, uint64(len(blocks)), 2)...)

				return blocks
			},
			firstReorgedBlock:     uint64Ptr(3),
			targetDepositCount:    3,
			archivedDepositCounts: []uint32{2, 3, 4, 5},
		},
	}

	for _, c := range testCases {
		t.Run(c.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "backward_let_cases.sqlite")
			require.NoError(t, migrations.RunMigrations(dbPath))
			p, err := newProcessor(dbPath, "bridge-syncer", log.GetDefaultLogger(), dbQueryTimeout)
			require.NoError(t, err)

			blocks := c.setupBlocks()
			for _, b := range blocks {
				err = p.ProcessBlock(t.Context(), b)
				if c.processBlockErrMsg != "" {
					require.ErrorContains(t, err, c.processBlockErrMsg)
				} else {
					require.NoError(t, err)
				}
			}

			if len(c.archivedDepositCounts) > 0 {
				archivedBridgeQuery := `
					SELECT * FROM bridge_archive
					WHERE deposit_count <= $1
					ORDER BY deposit_count ASC`

				maxDepositCount := slices.Max(c.archivedDepositCounts)
				var archivedBridges []*Bridge
				err = meddler.QueryAll(p.db, &archivedBridges, archivedBridgeQuery, maxDepositCount)
				require.NoError(t, err)

				require.Len(t, archivedBridges, len(c.archivedDepositCounts))
				for i, b := range archivedBridges {
					require.Equal(t, c.archivedDepositCounts[i], b.DepositCount)
					require.Equal(t, BridgeSourceBackwardLET, b.Source)
				}
			}

			if c.firstReorgedBlock != nil {
				err = p.Reorg(t.Context(), *c.firstReorgedBlock)
				require.NoError(t, err)
			}

			lastProcessedBlock, err := p.GetLastProcessedBlock(t.Context())
			require.NoError(t, err)
			expectedBridges := collectExpectedBridgesUpTo(t, blocks, c.skipBlocks, c.targetDepositCount)

			actualBridges, err := p.GetBridges(t.Context(), 0, lastProcessedBlock)
			require.NoError(t, err)
			require.Equal(t, expectedBridges, actualBridges)
		})
	}
}

func TestGetBoundaryBlock(t *testing.T) {
	insertBlockQuery := `INSERT INTO block (num, hash) VALUES ($1, $2) ON CONFLICT (num) DO UPDATE SET hash = $2`

	cases := []struct {
		name          string
		claims        []*Claim
		claimType     ClaimType
		expectedBlock uint64
		expectedErr   error
	}{
		{
			name:        "no claims, not found error",
			expectedErr: db.ErrNotFound,
		},
		{
			name: "detailed claim event exists, return its block",
			claims: []*Claim{
				{
					BlockNum:    1,
					BlockPos:    1,
					GlobalIndex: big.NewInt(100),
					Type:        DetailedClaimEvent,
				},
				{
					BlockNum:    6,
					BlockPos:    1,
					GlobalIndex: big.NewInt(101),
					Type:        DetailedClaimEvent,
				},
			},
			claimType:     DetailedClaimEvent,
			expectedBlock: 6,
		},
		{
			name: "mixed claim types exist, return detailed claim event block",
			claims: []*Claim{
				{
					BlockNum:    1,
					BlockPos:    1,
					GlobalIndex: big.NewInt(100),
					Type:        ClaimEvent,
				},
				{
					BlockNum:    100,
					BlockPos:    1,
					GlobalIndex: big.NewInt(101),
					Type:        DetailedClaimEvent,
				},
				{
					BlockNum:    101,
					BlockPos:    1,
					GlobalIndex: big.NewInt(102),
					Type:        DetailedClaimEvent,
				},
			},
			claimType:     DetailedClaimEvent,
			expectedBlock: 101,
		},
		{
			name: "no corresponding claim types exist",
			claims: []*Claim{
				{
					BlockNum:    1,
					BlockPos:    1,
					GlobalIndex: big.NewInt(100),
					Type:        ClaimEvent,
				},
				{
					BlockNum:    100,
					BlockPos:    1,
					GlobalIndex: big.NewInt(101),
					Type:        ClaimEvent,
				},
			},
			claimType:   DetailedClaimEvent,
			expectedErr: db.ErrNotFound,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "get_boundary_block.sqlite")
			require.NoError(t, migrations.RunMigrations(dbPath))
			p, err := newProcessor(dbPath, "bridge-syncer", log.GetDefaultLogger(), dbQueryTimeout)
			require.NoError(t, err)

			// Insert claims if any
			if len(tc.claims) > 0 {
				tx, err := p.db.BeginTx(t.Context(), nil)
				require.NoError(t, err)
				for _, claim := range tc.claims {
					_, err = tx.Exec(insertBlockQuery, claim.BlockNum, common.HexToHash("0x0"))
					require.NoError(t, err)
					require.NoError(t, meddler.Insert(tx, "claim", claim))
				}
				require.NoError(t, tx.Commit())
			}

			blockNum, err := p.GetBoundaryBlockForClaimType(t.Context(), tc.claimType)
			if tc.expectedErr != nil {
				require.ErrorIs(t, err, tc.expectedErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedBlock, blockNum)
			}
		})
	}
}

func TestHandleForwardLETEvent(t *testing.T) {
	t.Run("successfully process single leaf with no archived bridge", func(t *testing.T) {
		p, tx := setupProcessorWithTransaction(t)
		defer tx.Rollback() //nolint:errcheck

		// Setup: Insert initial leaves to establish previous root (indices 0-4)
		var initialRoot common.Hash
		var err error
		// Insert block rows for initial leaves
		for i := uint32(0); i <= 4; i++ {
			_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, 10+uint64(i))
			require.NoError(t, err)
		}
		for i := uint32(0); i <= 4; i++ {
			leaf := types.Leaf{Index: i, Hash: common.HexToHash(fmt.Sprintf("0x%d", i))}
			initialRoot, err = p.exitTree.PutLeaf(tx, 10+uint64(i), 0, leaf)
			require.NoError(t, err)
		}
		initialDepositCount := uint32(4) // Last index inserted

		// Insert block for the ForwardLET event
		_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, uint64(100))
		require.NoError(t, err)

		// Create forward LET event with one leaf
		leaves := []LeafData{
			{
				LeafType:           1,
				OriginNetwork:      5,
				OriginAddress:      common.HexToAddress("0x1234567890123456789012345678901234567890"),
				DestinationNetwork: 10,
				DestinationAddress: common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"),
				Amount:             big.NewInt(1000000),
				Metadata:           []byte("test metadata"),
			},
		}
		encodedLeaves := encodeLeafDataArrayForTest(t, leaves)

		event := &ForwardLET{
			BlockNum:             100,
			BlockPos:             5,
			BlockTimestamp:       1234567890,
			TxnHash:              common.HexToHash("0xabc123"),
			PreviousDepositCount: big.NewInt(int64(initialDepositCount + 1)),
			PreviousRoot:         initialRoot,
			NewDepositCount:      big.NewInt(int64(initialDepositCount + 1)),
			NewLeaves:            encodedLeaves,
		}

		// Calculate the expected root that will result from processing these leaves
		event.NewRoot = calculateExpectedRootAfterForwardLET(t, initialDepositCount, leaves, event)

		// Test: Process the forward LET event
		blockPos := event.BlockPos
		newBlockPos, err := p.handleForwardLETEvent(tx, event, &blockPos)
		require.NoError(t, err)
		require.Equal(t, event.BlockPos+1, newBlockPos)

		// Verify: Bridge was inserted
		var bridges []*Bridge
		err = meddler.QueryAll(tx, &bridges, "SELECT * FROM bridge WHERE block_num = $1", event.BlockNum)
		require.NoError(t, err)
		require.Len(t, bridges, 1)

		bridge := bridges[0]
		require.Equal(t, event.BlockNum, bridge.BlockNum)
		require.Equal(t, event.BlockPos, bridge.BlockPos)
		require.Equal(t, leaves[0].LeafType, bridge.LeafType)
		require.Equal(t, leaves[0].OriginNetwork, bridge.OriginNetwork)
		require.Equal(t, leaves[0].OriginAddress, bridge.OriginAddress)
		require.Equal(t, leaves[0].DestinationNetwork, bridge.DestinationNetwork)
		require.Equal(t, leaves[0].DestinationAddress, bridge.DestinationAddress)
		require.Equal(t, 0, leaves[0].Amount.Cmp(bridge.Amount))
		require.Equal(t, leaves[0].Metadata, bridge.Metadata)
		require.Equal(t, initialDepositCount+1, bridge.DepositCount)
		require.Equal(t, event.TxnHash, bridge.TxHash)
		require.Equal(t, aggkitcommon.ZeroAddress, bridge.TxnSender)
		require.Nil(t, bridge.FromAddress)
		require.Equal(t, BridgeSourceForwardLET, bridge.Source)

		// Verify: ForwardLET event was inserted
		var forwardLETs []*ForwardLET
		err = meddler.QueryAll(tx, &forwardLETs, "SELECT * FROM forward_let WHERE block_num = $1", event.BlockNum)
		require.NoError(t, err)
		require.Len(t, forwardLETs, 1)
		require.Equal(t, event.BlockNum, forwardLETs[0].BlockNum)
	})

	t.Run("successfully process multiple leaves", func(t *testing.T) {
		p, tx := setupProcessorWithTransaction(t)
		defer tx.Rollback() //nolint:errcheck

		// Setup: Insert initial leaves (indices 0-9)
		var initialRoot common.Hash
		var err error
		// Insert block rows for initial leaves
		for i := uint32(0); i <= 9; i++ {
			_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, 20+uint64(i))
			require.NoError(t, err)
		}
		for i := uint32(0); i <= 9; i++ {
			leaf := types.Leaf{Index: i, Hash: common.HexToHash(fmt.Sprintf("0x%d", i))}
			initialRoot, err = p.exitTree.PutLeaf(tx, 20+uint64(i), 0, leaf)
			require.NoError(t, err)
		}
		initialDepositCount := uint32(9) // Last index inserted

		// Insert block for the ForwardLET event
		_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, uint64(200))
		require.NoError(t, err)

		// Create forward LET event with three leaves
		leaves := []LeafData{
			{
				LeafType:           0,
				OriginNetwork:      1,
				OriginAddress:      common.HexToAddress("0x1111111111111111111111111111111111111111"),
				DestinationNetwork: 2,
				DestinationAddress: common.HexToAddress("0x2222222222222222222222222222222222222222"),
				Amount:             big.NewInt(100),
				Metadata:           []byte("first"),
			},
			{
				LeafType:           1,
				OriginNetwork:      3,
				OriginAddress:      common.HexToAddress("0x3333333333333333333333333333333333333333"),
				DestinationNetwork: 4,
				DestinationAddress: common.HexToAddress("0x4444444444444444444444444444444444444444"),
				Amount:             big.NewInt(200),
				Metadata:           []byte("second"),
			},
			{
				LeafType:           2,
				OriginNetwork:      5,
				OriginAddress:      common.HexToAddress("0x5555555555555555555555555555555555555555"),
				DestinationNetwork: 6,
				DestinationAddress: common.HexToAddress("0x6666666666666666666666666666666666666666"),
				Amount:             big.NewInt(300),
				Metadata:           []byte("third"),
			},
		}
		encodedLeaves := encodeLeafDataArrayForTest(t, leaves)

		event := &ForwardLET{
			BlockNum:             200,
			BlockPos:             10,
			BlockTimestamp:       1234567900,
			TxnHash:              common.HexToHash("0xdef456"),
			PreviousDepositCount: big.NewInt(int64(initialDepositCount + 1)),
			PreviousRoot:         initialRoot,
			NewDepositCount:      big.NewInt(int64(initialDepositCount + uint32(len(leaves)))),
			NewLeaves:            encodedLeaves,
		}

		// Calculate the expected root that will result from processing these leaves
		event.NewRoot = calculateExpectedRootAfterForwardLET(t, initialDepositCount, leaves, event)

		// Test: Process the forward LET event
		blockPos := event.BlockPos
		newBlockPos, err := p.handleForwardLETEvent(tx, event, &blockPos)
		require.NoError(t, err)
		require.Equal(t, event.BlockPos+uint64(len(leaves)), newBlockPos)

		// Verify: All bridges were inserted
		var bridges []*Bridge
		err = meddler.QueryAll(tx, &bridges, "SELECT * FROM bridge WHERE block_num = $1 ORDER BY block_pos", event.BlockNum)
		require.NoError(t, err)
		require.Len(t, bridges, 3)

		// Verify each bridge
		for i, bridge := range bridges {
			require.Equal(t, event.BlockNum, bridge.BlockNum)
			require.Equal(t, event.BlockPos+uint64(i), bridge.BlockPos)
			require.Equal(t, leaves[i].LeafType, bridge.LeafType)
			require.Equal(t, leaves[i].OriginNetwork, bridge.OriginNetwork)
			require.Equal(t, initialDepositCount+uint32(i)+1, bridge.DepositCount)
			require.Equal(t, BridgeSourceForwardLET, bridge.Source)
		}
	})

	t.Run("process leaf with matching archived bridge", func(t *testing.T) {
		p, tx := setupProcessorWithTransaction(t)
		defer tx.Rollback() //nolint:errcheck

		// Setup: Insert initial leaves (indices 0-14)
		var initialRoot common.Hash
		var err error
		// Insert block rows for initial leaves
		for i := uint32(0); i <= 14; i++ {
			_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, 30+uint64(i))
			require.NoError(t, err)
		}
		for i := uint32(0); i <= 14; i++ {
			leaf := types.Leaf{Index: i, Hash: common.HexToHash(fmt.Sprintf("0x%d", i))}
			initialRoot, err = p.exitTree.PutLeaf(tx, 30+uint64(i), 0, leaf)
			require.NoError(t, err)
		}
		initialDepositCount := uint32(14) // Last index inserted

		// Insert blocks for the archived bridge and ForwardLET event
		_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1), ($2)`, uint64(50), uint64(300))
		require.NoError(t, err)

		// Setup: Create and archive a bridge that will match the forward LET leaf
		archivedTxHash := common.HexToHash("0xoriginal123")
		archivedTxnSender := common.HexToAddress("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		archivedFromAddr := common.HexToAddress("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")

		archivedBridge := &Bridge{
			BlockNum:           50,
			BlockPos:           0,
			LeafType:           1,
			OriginNetwork:      7,
			OriginAddress:      common.HexToAddress("0x7777777777777777777777777777777777777777"),
			DestinationNetwork: 8,
			DestinationAddress: common.HexToAddress("0x8888888888888888888888888888888888888888"),
			Amount:             big.NewInt(500000),
			Metadata:           []byte("archived metadata"),
			DepositCount:       20,
			TxHash:             archivedTxHash,
			TxnSender:          archivedTxnSender,
			FromAddress:        &archivedFromAddr,
			// Don't set Source - bridge_archive table doesn't have this column
		}
		// Insert manually to avoid Source field
		err = meddler.Insert(tx, "bridge_archive", archivedBridge)
		require.NoError(t, err)

		// Create forward LET event with matching leaf
		leaves := []LeafData{
			{
				LeafType:           archivedBridge.LeafType,
				OriginNetwork:      archivedBridge.OriginNetwork,
				OriginAddress:      archivedBridge.OriginAddress,
				DestinationNetwork: archivedBridge.DestinationNetwork,
				DestinationAddress: archivedBridge.DestinationAddress,
				Amount:             archivedBridge.Amount,
				Metadata:           archivedBridge.Metadata,
			},
		}
		encodedLeaves := encodeLeafDataArrayForTest(t, leaves)

		event := &ForwardLET{
			BlockNum:             300,
			BlockPos:             20,
			BlockTimestamp:       1234567950,
			TxnHash:              common.HexToHash("0xforward789"),
			PreviousDepositCount: big.NewInt(int64(initialDepositCount + 1)),
			PreviousRoot:         initialRoot,
			NewDepositCount:      big.NewInt(int64(initialDepositCount + 1)),
			NewLeaves:            encodedLeaves,
		}

		// Calculate expected new root using helper (which will query for archived bridge)
		event.NewRoot = calculateExpectedRootAfterForwardLET(t, initialDepositCount, leaves, event, archivedBridge)

		// Test: Process the forward LET event
		blockPos := event.BlockPos
		newBlockPos, err := p.handleForwardLETEvent(tx, event, &blockPos)
		require.NoError(t, err)
		require.Equal(t, event.BlockPos+1, newBlockPos)

		// Verify: Bridge was inserted with archived tx info
		var bridges []*Bridge
		err = meddler.QueryAll(tx, &bridges, "SELECT * FROM bridge WHERE block_num = $1", event.BlockNum)
		require.NoError(t, err)
		require.Len(t, bridges, 1)

		bridge := bridges[0]
		require.Equal(t, archivedTxHash, bridge.TxHash, "Should use archived tx hash")
		require.Equal(t, archivedTxnSender, bridge.TxnSender, "Should use archived txn sender")
		require.NotNil(t, bridge.FromAddress)
		require.Equal(t, archivedFromAddr, *bridge.FromAddress, "Should use archived from address")
		require.Equal(t, BridgeSourceForwardLET, bridge.Source)
	})

	t.Run("process leaf with multiple matching archived bridges", func(t *testing.T) {
		p, tx := setupProcessorWithTransaction(t)
		defer tx.Rollback() //nolint:errcheck

		// Setup: Insert initial leaves (indices 0-24)
		var initialRoot common.Hash
		var err error
		// Insert block rows for initial leaves
		for i := uint32(0); i <= 24; i++ {
			_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, 40+uint64(i))
			require.NoError(t, err)
		}
		for i := uint32(0); i <= 24; i++ {
			leaf := types.Leaf{Index: i, Hash: common.HexToHash(fmt.Sprintf("0x%d", i))}
			initialRoot, err = p.exitTree.PutLeaf(tx, 40+uint64(i), 0, leaf)
			require.NoError(t, err)
		}
		initialDepositCount := uint32(24) // Last index inserted

		// Insert blocks for archived bridges (60, 61 already exist from initial leaves) and ForwardLET event
		_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, uint64(400))
		require.NoError(t, err)

		// Setup: Create two archived bridges with identical LeafData fields
		commonLeafData := LeafData{
			LeafType:           1,
			OriginNetwork:      9,
			OriginAddress:      common.HexToAddress("0x9999999999999999999999999999999999999999"),
			DestinationNetwork: 11,
			DestinationAddress: common.HexToAddress("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
			Amount:             big.NewInt(750000),
			Metadata:           []byte("duplicate metadata"),
		}

		archivedBridge1 := &Bridge{
			BlockNum:           60,
			BlockPos:           0,
			LeafType:           commonLeafData.LeafType,
			OriginNetwork:      commonLeafData.OriginNetwork,
			OriginAddress:      commonLeafData.OriginAddress,
			DestinationNetwork: commonLeafData.DestinationNetwork,
			DestinationAddress: commonLeafData.DestinationAddress,
			Amount:             commonLeafData.Amount,
			Metadata:           commonLeafData.Metadata,
			DepositCount:       30,
			TxHash:             common.HexToHash("0xfirst111"),
			TxnSender:          common.HexToAddress("0x1111111111111111111111111111111111111111"),
			FromAddress: func() *common.Address {
				addr := common.HexToAddress("0x2222222222222222222222222222222222222222")
				return &addr
			}(),
		}

		archivedBridge2 := &Bridge{
			BlockNum:           61,
			BlockPos:           0,
			LeafType:           commonLeafData.LeafType,
			OriginNetwork:      commonLeafData.OriginNetwork,
			OriginAddress:      commonLeafData.OriginAddress,
			DestinationNetwork: commonLeafData.DestinationNetwork,
			DestinationAddress: commonLeafData.DestinationAddress,
			Amount:             commonLeafData.Amount,
			Metadata:           commonLeafData.Metadata,
			DepositCount:       31,
			TxHash:             common.HexToHash("0xsecond222"),
			TxnSender:          common.HexToAddress("0x3333333333333333333333333333333333333333"),
			FromAddress: func() *common.Address {
				addr := common.HexToAddress("0x4444444444444444444444444444444444444444")
				return &addr
			}(),
		}

		// Insert both archived bridges manually (to avoid Source column)
		for _, archived := range []*Bridge{archivedBridge1, archivedBridge2} {
			err = meddler.Insert(tx, "bridge_archive", archived)
			require.NoError(t, err)
		}

		// Create forward LET event with the common leaf
		leaves := []LeafData{commonLeafData}
		encodedLeaves := encodeLeafDataArrayForTest(t, leaves)

		event := &ForwardLET{
			BlockNum:             400,
			BlockPos:             30,
			BlockTimestamp:       1234567999,
			TxnHash:              common.HexToHash("0xforward999"),
			PreviousDepositCount: big.NewInt(int64(initialDepositCount + 1)),
			PreviousRoot:         initialRoot,
			NewDepositCount:      big.NewInt(int64(initialDepositCount + 1)),
			NewLeaves:            encodedLeaves,
		}

		// Calculate expected new root using helper (with no archived bridge info since multiple matches)
		event.NewRoot = calculateExpectedRootAfterForwardLET(t, initialDepositCount, leaves, event)

		// Test: Process the forward LET event
		blockPos := event.BlockPos
		newBlockPos, err := p.handleForwardLETEvent(tx, event, &blockPos)
		require.NoError(t, err)
		require.Equal(t, event.BlockPos+1, newBlockPos)

		// Verify: Bridge was inserted with event's tx hash and empty addresses
		var bridges []*Bridge
		err = meddler.QueryAll(tx, &bridges, "SELECT * FROM bridge WHERE block_num = $1", event.BlockNum)
		require.NoError(t, err)
		require.Len(t, bridges, 1)

		bridge := bridges[0]
		require.Equal(t, event.TxnHash, bridge.TxHash, "Should use event's tx hash when multiple archived bridges match")
		require.Equal(t, common.Address{}, bridge.TxnSender, "TxnSender should be empty with multiple matches")
		require.Nil(t, bridge.FromAddress, "FromAddress should be nil with multiple matches")
		require.Equal(t, BridgeSourceForwardLET, bridge.Source)
	})

	t.Run("error on previous root mismatch", func(t *testing.T) {
		p, tx := setupProcessorWithTransaction(t)
		defer tx.Rollback() //nolint:errcheck

		// Setup: Insert initial leaves (indices 0-4)
		var initialRoot common.Hash
		var err error
		for i := uint32(0); i <= 4; i++ {
			leaf := types.Leaf{Index: i, Hash: common.HexToHash(fmt.Sprintf("0x%d", i))}
			initialRoot, err = p.exitTree.PutLeaf(tx, 10+uint64(i), 0, leaf)
			require.NoError(t, err)
		}
		initialDepositCount := uint32(4) // Last index inserted

		// Create forward LET event with WRONG previous root
		leaves := []LeafData{
			{
				LeafType:           1,
				OriginNetwork:      5,
				OriginAddress:      common.HexToAddress("0x1234567890123456789012345678901234567890"),
				DestinationNetwork: 10,
				DestinationAddress: common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"),
				Amount:             big.NewInt(1000000),
				Metadata:           []byte("test"),
			},
		}
		encodedLeaves := encodeLeafDataArrayForTest(t, leaves)

		event := &ForwardLET{
			BlockNum:             100,
			BlockPos:             5,
			BlockTimestamp:       1234567890,
			TxnHash:              common.HexToHash("0xabc123"),
			PreviousDepositCount: big.NewInt(int64(initialDepositCount + 1)),
			PreviousRoot:         common.HexToHash("0xWRONG"), // Wrong root
			NewDepositCount:      big.NewInt(int64(initialDepositCount + 1)),
			NewRoot:              common.HexToHash("0x999"),
			NewLeaves:            encodedLeaves,
		}

		// Test: Should fail with root mismatch
		blockPos := event.BlockPos
		_, err = p.handleForwardLETEvent(tx, event, &blockPos)
		require.Error(t, err)
		require.Contains(t, err.Error(), "local exit root mismatch")
		require.Contains(t, err.Error(), initialRoot.String())
	})

	t.Run("error on new root mismatch", func(t *testing.T) {
		p, tx := setupProcessorWithTransaction(t)
		defer tx.Rollback() //nolint:errcheck

		// Setup: Insert initial leaves (indices 0-4)
		var initialRoot common.Hash
		var err error
		// Insert block rows for initial leaves
		for i := uint32(0); i <= 4; i++ {
			_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, 10+uint64(i))
			require.NoError(t, err)
		}
		for i := uint32(0); i <= 4; i++ {
			leaf := types.Leaf{Index: i, Hash: common.HexToHash(fmt.Sprintf("0x%d", i))}
			initialRoot, err = p.exitTree.PutLeaf(tx, 10+uint64(i), 0, leaf)
			require.NoError(t, err)
		}
		initialDepositCount := uint32(4) // Last index inserted

		// Insert block for the ForwardLET event
		_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, uint64(100))
		require.NoError(t, err)

		// Create forward LET event
		leaves := []LeafData{
			{
				LeafType:           1,
				OriginNetwork:      5,
				OriginAddress:      common.HexToAddress("0x1234567890123456789012345678901234567890"),
				DestinationNetwork: 10,
				DestinationAddress: common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"),
				Amount:             big.NewInt(1000000),
				Metadata:           []byte("test"),
			},
		}
		encodedLeaves := encodeLeafDataArrayForTest(t, leaves)

		event := &ForwardLET{
			BlockNum:             100,
			BlockPos:             5,
			BlockTimestamp:       1234567890,
			TxnHash:              common.HexToHash("0xabc123"),
			PreviousDepositCount: big.NewInt(int64(initialDepositCount + 1)),
			PreviousRoot:         initialRoot,
			NewDepositCount:      big.NewInt(int64(initialDepositCount + 1)),
			NewRoot:              common.HexToHash("0xWRONG"), // Wrong new root
			NewLeaves:            encodedLeaves,
		}

		// Test: Should fail with new root mismatch after processing
		blockPos := event.BlockPos
		_, err = p.handleForwardLETEvent(tx, event, &blockPos)
		require.Error(t, err)
		require.Contains(t, err.Error(), "local exit root mismatch")
	})

	t.Run("error on invalid encoded leaves", func(t *testing.T) {
		p, tx := setupProcessorWithTransaction(t)
		defer tx.Rollback() //nolint:errcheck

		// Setup: Insert initial leaves (indices 0-4)
		var initialRoot common.Hash
		var err error
		for i := uint32(0); i <= 4; i++ {
			leaf := types.Leaf{Index: i, Hash: common.HexToHash(fmt.Sprintf("0x%d", i))}
			initialRoot, err = p.exitTree.PutLeaf(tx, 10+uint64(i), 0, leaf)
			require.NoError(t, err)
		}
		initialDepositCount := uint32(4) // Last index inserted

		event := &ForwardLET{
			BlockNum:             100,
			BlockPos:             5,
			BlockTimestamp:       1234567890,
			TxnHash:              common.HexToHash("0xabc123"),
			PreviousDepositCount: big.NewInt(int64(initialDepositCount + 1)),
			PreviousRoot:         initialRoot,
			NewDepositCount:      big.NewInt(int64(initialDepositCount + 1)),
			NewRoot:              common.Hash{},
			NewLeaves:            []byte("invalid data"), // Invalid encoding
		}

		// Test: Should fail to decode leaves
		blockPos := event.BlockPos
		_, err = p.handleForwardLETEvent(tx, event, &blockPos)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to decode new leaves")
	})

	t.Run("process with nil blockPos parameter", func(t *testing.T) {
		p, tx := setupProcessorWithTransaction(t)
		defer tx.Rollback() //nolint:errcheck

		// Setup: Insert initial leaves (indices 0-4)
		var initialRoot common.Hash
		var err error
		// Insert block rows for initial leaves
		for i := uint32(0); i <= 4; i++ {
			_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, 10+uint64(i))
			require.NoError(t, err)
		}
		for i := uint32(0); i <= 4; i++ {
			leaf := types.Leaf{Index: i, Hash: common.HexToHash(fmt.Sprintf("0x%d", i))}
			initialRoot, err = p.exitTree.PutLeaf(tx, 10+uint64(i), 0, leaf)
			require.NoError(t, err)
		}
		initialDepositCount := uint32(4) // Last index inserted

		// Insert block for the ForwardLET event
		_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, uint64(100))
		require.NoError(t, err)

		leaves := []LeafData{
			{
				LeafType:           1,
				OriginNetwork:      5,
				OriginAddress:      common.HexToAddress("0x1234567890123456789012345678901234567890"),
				DestinationNetwork: 10,
				DestinationAddress: common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"),
				Amount:             big.NewInt(1000000),
				Metadata:           []byte("test"),
			},
		}
		encodedLeaves := encodeLeafDataArrayForTest(t, leaves)

		event := &ForwardLET{
			BlockNum:             100,
			BlockPos:             5,
			BlockTimestamp:       1234567890,
			TxnHash:              common.HexToHash("0xabc123"),
			PreviousDepositCount: big.NewInt(int64(initialDepositCount + 1)),
			PreviousRoot:         initialRoot,
			NewDepositCount:      big.NewInt(int64(initialDepositCount + 1)),
			NewLeaves:            encodedLeaves,
		}

		// Calculate expected root using helper
		event.NewRoot = calculateExpectedRootAfterForwardLET(t, initialDepositCount, leaves, event)

		// Test: Process with nil blockPos (should use event.BlockPos)
		newBlockPos, err := p.handleForwardLETEvent(tx, event, nil)
		require.NoError(t, err)
		require.Equal(t, event.BlockPos+1, newBlockPos)

		// Verify: Bridge uses event.BlockPos
		var bridges []*Bridge
		err = meddler.QueryAll(tx, &bridges, "SELECT * FROM bridge WHERE block_num = $1", event.BlockNum)
		require.NoError(t, err)
		require.Len(t, bridges, 1)
		require.Equal(t, event.BlockPos, bridges[0].BlockPos)
	})

	t.Run("ForwardLET after genesis assigns deposit_count starting at 0", func(t *testing.T) {
		// Covers the EmptyLER branch: when the tree is empty (PreviousRoot == EmptyLER),
		// newDepositCount must start at 0 (the Go zero value), independent of PreviousDepositCount.
		p, tx := setupProcessorWithTransaction(t)
		defer tx.Rollback() //nolint:errcheck

		// Insert block for the ForwardLET event (no prior leaves — tree is empty)
		_, err := tx.Exec(`INSERT INTO block (num) VALUES ($1)`, uint64(200))
		require.NoError(t, err)

		leaves := []LeafData{
			{
				LeafType:           0,
				OriginNetwork:      1,
				OriginAddress:      common.HexToAddress("0x1111111111111111111111111111111111111111"),
				DestinationNetwork: 2,
				DestinationAddress: common.HexToAddress("0x2222222222222222222222222222222222222222"),
				Amount:             big.NewInt(500),
				Metadata:           []byte("genesis leaf"),
			},
			{
				LeafType:           0,
				OriginNetwork:      1,
				OriginAddress:      common.HexToAddress("0x3333333333333333333333333333333333333333"),
				DestinationNetwork: 2,
				DestinationAddress: common.HexToAddress("0x4444444444444444444444444444444444444444"),
				Amount:             big.NewInt(750),
				Metadata:           []byte("genesis leaf 2"),
			},
		}
		encodedLeaves := encodeLeafDataArrayForTest(t, leaves)

		event := &ForwardLET{
			BlockNum:             200,
			BlockPos:             0,
			BlockTimestamp:       9999999,
			TxnHash:              common.HexToHash("0xgenesis"),
			PreviousDepositCount: big.NewInt(0),
			PreviousRoot:         bridgesynctypes.EmptyLER, // tree is empty
			NewDepositCount:      big.NewInt(2),
			NewLeaves:            encodedLeaves,
		}

		// Compute expected root by inserting leaves into a temp tree starting at index 0
		tempDBPath := filepath.Join(t.TempDir(), "temp_genesis.db")
		err = migrations.RunMigrations(tempDBPath)
		require.NoError(t, err)
		tempP, err := newProcessor(tempDBPath, "test-genesis", log.WithFields("module", "test-genesis"), dbQueryTimeout)
		require.NoError(t, err)
		tempTx, err := db.NewTx(t.Context(), tempP.db)
		require.NoError(t, err)
		defer tempTx.Rollback() //nolint:errcheck
		_, err = tempTx.Exec(`INSERT INTO block (num) VALUES ($1)`, uint64(200))
		require.NoError(t, err)
		var expectedRoot common.Hash
		for i, leaf := range leaves {
			bridge := leaf.ToBridge(200, uint64(i), 9999999, uint32(i), event.TxnHash, common.Address{}, nil)
			expectedRoot, err = tempP.exitTree.PutLeaf(tempTx, 200, uint64(i), types.Leaf{
				Index: uint32(i),
				Hash:  bridge.Hash(),
			})
			require.NoError(t, err)
		}
		event.NewRoot = expectedRoot

		blockPos := event.BlockPos
		newBlockPos, err := p.handleForwardLETEvent(tx, event, &blockPos)
		require.NoError(t, err)
		require.Equal(t, uint64(len(leaves)), newBlockPos)

		var bridges []*Bridge
		err = meddler.QueryAll(tx, &bridges, "SELECT * FROM bridge WHERE block_num = $1 ORDER BY deposit_count", event.BlockNum)
		require.NoError(t, err)
		require.Len(t, bridges, 2)

		// First leaf must get deposit_count=0, second must get deposit_count=1
		require.Equal(t, uint32(0), bridges[0].DepositCount)
		require.Equal(t, uint32(1), bridges[1].DepositCount)
	})
}

// setupProcessorWithTransaction creates a processor and begins a transaction for testing
func setupProcessorWithTransaction(t *testing.T) (*processor, dbtypes.Txer) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test_forward_let.db")
	err := migrations.RunMigrations(dbPath)
	require.NoError(t, err)

	logger := log.WithFields("module", "test")
	p, err := newProcessor(dbPath, "test", logger, dbQueryTimeout)
	require.NoError(t, err)

	tx, err := db.NewTx(t.Context(), p.db)
	require.NoError(t, err)

	return p, tx
}

// calculateExpectedRootAfterForwardLET calculates what the tree root will be after processing ForwardLET leaves
// It does this using a completely separate processor to avoid affecting the test state
// archivedBridges: optional map from leaf index (in leaves slice) to archived bridge info
func calculateExpectedRootAfterForwardLET(t *testing.T, initialDepositCount uint32,
	leaves []LeafData, event *ForwardLET, archivedBridges ...*Bridge) common.Hash {
	t.Helper()

	// Build a map for quick lookup of archived bridge info by leaf data
	archivedByLeaf := make(map[int]*Bridge)
	for i, archived := range archivedBridges {
		if archived != nil {
			archivedByLeaf[i] = archived
		}
	}

	// Create a temporary processor with its own database
	tempDBPath := filepath.Join(t.TempDir(), "temp_calc.db")
	err := migrations.RunMigrations(tempDBPath)
	require.NoError(t, err)

	logger := log.WithFields("module", "test-calc")
	tempP, err := newProcessor(tempDBPath, "test-calc", logger, dbQueryTimeout)
	require.NoError(t, err)

	tempTx, err := db.NewTx(t.Context(), tempP.db)
	require.NoError(t, err)
	defer tempTx.Rollback() //nolint:errcheck

	// Insert block rows for the setup leaves
	for i := uint32(0); i <= initialDepositCount; i++ {
		_, err = tempTx.Exec(`INSERT INTO block (num) VALUES ($1)`, 10+uint64(i))
		require.NoError(t, err)
	}

	// Insert block row for the ForwardLET event
	_, err = tempTx.Exec(`INSERT INTO block (num) VALUES ($1)`, event.BlockNum)
	require.NoError(t, err)

	// Insert archived bridges if provided
	for _, archived := range archivedBridges {
		if archived != nil {
			_, err = tempTx.Exec(`INSERT INTO block (num) VALUES ($1)`, archived.BlockNum)
			require.NoError(t, err)

			_, err = tempTx.Exec(`
				INSERT INTO bridge_archive (
					block_num, block_pos, leaf_type, origin_network, origin_address,
					destination_network, destination_address, amount, metadata, deposit_count,
					tx_hash, block_timestamp, from_address, txn_sender
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 0, $12, $13)
			`, archived.BlockNum, archived.BlockPos, archived.LeafType,
				archived.OriginNetwork, archived.OriginAddress.Hex(),
				archived.DestinationNetwork, archived.DestinationAddress.Hex(),
				archived.Amount.String(), archived.Metadata, archived.DepositCount,
				archived.TxHash.Hex(), archived.FromAddress.Hex(), archived.TxnSender.Hex())
			require.NoError(t, err)
		}
	}

	// Rebuild tree state up to initialDepositCount
	for i := uint32(0); i <= initialDepositCount; i++ {
		leaf := types.Leaf{Index: i, Hash: common.HexToHash(fmt.Sprintf("0x%d", i))}
		_, err = tempP.exitTree.PutLeaf(tempTx, 10+uint64(i), 0, leaf)
		require.NoError(t, err)
	}

	// Now add the ForwardLET leaves (will query for archived bridges)
	currentDepositCount := initialDepositCount + 1
	var newRoot common.Hash
	for i, leaf := range leaves {
		// Try to get archived bridge info if available
		var txHash common.Hash
		var txnSender common.Address
		var fromAddr *common.Address
		if archived, found := archivedByLeaf[i]; found {
			txHash = archived.TxHash
			txnSender = archived.TxnSender
			fromAddr = archived.FromAddress
		} else {
			txHash = event.TxnHash
			// txnSender and fromAddr remain zero
		}

		bridge := leaf.ToBridge(
			event.BlockNum,
			event.BlockPos+uint64(i),
			event.BlockTimestamp,
			currentDepositCount,
			txHash,
			txnSender,
			fromAddr,
		)
		newRoot, err = tempP.exitTree.PutLeaf(tempTx, event.BlockNum, event.BlockPos+uint64(i), types.Leaf{
			Index: currentDepositCount,
			Hash:  bridge.Hash(),
		})
		require.NoError(t, err)
		currentDepositCount++
	}

	return newRoot
}

// encodeLeafDataArrayForTest encodes a slice of LeafData using ABI encoding
func encodeLeafDataArrayForTest(t *testing.T, leaves []LeafData) []byte {
	t.Helper()

	encodedBytes, err := aggkitabi.EncodeABIStructArray(leaves)
	require.NoError(t, err)

	return encodedBytes
}

func TestProcessor_GetClaimsByGER(t *testing.T) {
	t.Helper()

	ctx := context.Background()
	p := createTestProcessor(t, "test_get_claims_by_ger")

	gerHash := common.HexToHash("0xaabbccdd")
	otherGER := common.HexToHash("0x11223344")

	// Insert a block and two claims: one DetailedClaimEvent with gerHash, one ClaimEvent with gerHash,
	// and one DetailedClaimEvent with a different GER.
	tx, err := p.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, uint64(1))
	require.NoError(t, err)
	_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, uint64(2))
	require.NoError(t, err)
	_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, uint64(3))
	require.NoError(t, err)

	detailedClaim := &Claim{
		BlockNum:       1,
		BlockPos:       0,
		GlobalIndex:    big.NewInt(100),
		GlobalExitRoot: gerHash,
		Type:           DetailedClaimEvent,
		Amount:         big.NewInt(0),
	}
	require.NoError(t, meddler.Insert(tx, "claim", detailedClaim))

	// A ClaimEvent with the same GER — should NOT be returned
	claimEventSameGER := &Claim{
		BlockNum:       2,
		BlockPos:       0,
		GlobalIndex:    big.NewInt(200),
		GlobalExitRoot: gerHash,
		Type:           ClaimEvent,
		Amount:         big.NewInt(0),
	}
	require.NoError(t, meddler.Insert(tx, "claim", claimEventSameGER))

	// A DetailedClaimEvent with a different GER — should NOT be returned
	detailedOtherGER := &Claim{
		BlockNum:       3,
		BlockPos:       0,
		GlobalIndex:    big.NewInt(300),
		GlobalExitRoot: otherGER,
		Type:           DetailedClaimEvent,
		Amount:         big.NewInt(0),
	}
	require.NoError(t, meddler.Insert(tx, "claim", detailedOtherGER))
	require.NoError(t, tx.Commit())

	t.Run("returns only DetailedClaimEvent with matching GER", func(t *testing.T) {
		claims, err := p.GetClaimsByGER(ctx, gerHash)
		require.NoError(t, err)
		require.Len(t, claims, 1)
		require.Equal(t, int64(100), claims[0].GlobalIndex.Int64())
		require.Equal(t, DetailedClaimEvent, claims[0].Type)
	})

	t.Run("returns nil for unknown GER", func(t *testing.T) {
		claims, err := p.GetClaimsByGER(ctx, common.HexToHash("0xdeadbeef"))
		require.NoError(t, err)
		require.Empty(t, claims)
	})
}

func TestProcessor_GetBridgeByDepositCount(t *testing.T) {
	t.Helper()

	ctx := context.Background()
	p := createTestProcessor(t, "test_get_bridge_by_deposit_count")

	originAddr := common.HexToAddress("0x1111111111111111111111111111111111111111")
	destAddr := common.HexToAddress("0x2222222222222222222222222222222222222222")

	// Insert a block and a bridge with origin_network=0 and deposit_count=5.
	tx, err := p.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, uint64(1))
	require.NoError(t, err)

	bridge := &Bridge{
		BlockNum:           1,
		BlockPos:           0,
		DepositCount:       5,
		OriginNetwork:      0,
		OriginAddress:      originAddr,
		DestinationNetwork: 1,
		DestinationAddress: destAddr,
		Amount:             big.NewInt(1000),
		LeafType:           0,
	}
	require.NoError(t, meddler.Insert(tx, "bridge", bridge))
	require.NoError(t, tx.Commit())

	t.Run("found by deposit count", func(t *testing.T) {
		got, err := p.GetBridgeByDepositCount(ctx, 5)
		require.NoError(t, err)
		require.NotNil(t, got)
		require.Equal(t, uint32(5), got.DepositCount)
		require.Equal(t, uint32(0), got.OriginNetwork)
		require.Equal(t, originAddr, got.OriginAddress)
	})

	t.Run("not found returns ErrNotFound", func(t *testing.T) {
		got, err := p.GetBridgeByDepositCount(ctx, 999)
		require.ErrorIs(t, err, db.ErrNotFound)
		require.Nil(t, got)
	})
}

func TestProcessor_GetBridgesByContent(t *testing.T) {
	t.Helper()

	ctx := context.Background()
	p := createTestProcessor(t, "test_get_bridges_by_content")

	originAddr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	destAddr := common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
	amount := big.NewInt(500)
	metadata := []byte("testmeta")

	tx, err := p.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, uint64(1))
	require.NoError(t, err)
	_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, uint64(2))
	require.NoError(t, err)

	// Bridge matching the search criteria
	bridge1 := &Bridge{
		BlockNum:           1,
		BlockPos:           0,
		DepositCount:       10,
		OriginNetwork:      0,
		LeafType:           0,
		OriginAddress:      originAddr,
		DestinationNetwork: 2,
		DestinationAddress: destAddr,
		Amount:             new(big.Int).Set(amount),
		Metadata:           metadata,
	}
	require.NoError(t, meddler.Insert(tx, "bridge", bridge1))

	// Bridge with different amount — should NOT match
	bridge2 := &Bridge{
		BlockNum:           2,
		BlockPos:           0,
		DepositCount:       11,
		OriginNetwork:      0,
		LeafType:           0,
		OriginAddress:      originAddr,
		DestinationNetwork: 2,
		DestinationAddress: destAddr,
		Amount:             big.NewInt(999),
		Metadata:           metadata,
	}
	require.NoError(t, meddler.Insert(tx, "bridge", bridge2))
	require.NoError(t, tx.Commit())

	t.Run("returns bridge matching content", func(t *testing.T) {
		result, err := p.GetBridgesByContent(ctx, 0, originAddr, 2, destAddr, amount, metadata)
		require.NoError(t, err)
		require.Len(t, result, 1)
		require.Equal(t, uint32(10), result[0].DepositCount)
	})

	t.Run("no matches returns empty", func(t *testing.T) {
		result, err := p.GetBridgesByContent(ctx, 0, originAddr, 2, destAddr, big.NewInt(1), metadata)
		require.NoError(t, err)
		require.Empty(t, result)
	})

	t.Run("nil metadata matches bridges with null metadata", func(t *testing.T) {
		// Insert a bridge with nil metadata
		tx2, err := p.db.BeginTx(ctx, nil)
		require.NoError(t, err)
		_, err = tx2.Exec(`INSERT INTO block (num) VALUES ($1)`, uint64(3))
		require.NoError(t, err)
		nilMetaBridge := &Bridge{
			BlockNum:           3,
			BlockPos:           0,
			DepositCount:       20,
			OriginNetwork:      0,
			LeafType:           1,
			OriginAddress:      originAddr,
			DestinationNetwork: 3,
			DestinationAddress: destAddr,
			Amount:             big.NewInt(1),
			Metadata:           nil,
		}
		require.NoError(t, meddler.Insert(tx2, "bridge", nilMetaBridge))
		require.NoError(t, tx2.Commit())

		result, err := p.GetBridgesByContent(ctx, 1, originAddr, 3, destAddr, big.NewInt(1), nil)
		require.NoError(t, err)
		require.Len(t, result, 1)
		require.Equal(t, uint32(20), result[0].DepositCount)
	})
}
