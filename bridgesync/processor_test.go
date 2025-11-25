package bridgesync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/tmp-detailed-claim-event/polygonzkevmbridge"
	bridgetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync/migrations"
	"github.com/agglayer/aggkit/db"
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

	dbPath := path.Join(t.TempDir(), "bridgesyncTestBigIntString.sqlite")

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
		&getClaims{
			p:              p,
			description:    "on an empty processor",
			ctx:            context.Background(),
			fromBlock:      0,
			toBlock:        2,
			expectedClaims: nil,
			expectedErr:    fmt.Errorf(errBlockNotProcessedFormat, 2, 0),
		},
		&getBridges{
			p:               p,
			description:     "on an empty processor",
			ctx:             context.Background(),
			fromBlock:       0,
			toBlock:         2,
			expectedBridges: nil,
			expectedErr:     fmt.Errorf(errBlockNotProcessedFormat, 2, 0),
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
			description:    "after block1: range 0, 2",
			ctx:            context.Background(),
			fromBlock:      0,
			toBlock:        2,
			expectedClaims: nil,
			expectedErr:    fmt.Errorf(errBlockNotProcessedFormat, 2, 1),
		},
		&getBridges{
			p:               p,
			description:     "after block1: range 0, 2",
			ctx:             context.Background(),
			fromBlock:       0,
			toBlock:         2,
			expectedBridges: nil,
			expectedErr:     fmt.Errorf(errBlockNotProcessedFormat, 2, 1),
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
		// processed: ~
		&getClaims{
			p:              p,
			description:    "after block1 reorged",
			ctx:            context.Background(),
			fromBlock:      0,
			toBlock:        2,
			expectedClaims: nil,
			expectedErr:    fmt.Errorf(errBlockNotProcessedFormat, 2, 0),
		},
		&getBridges{
			p:               p,
			description:     "after block1 reorged",
			ctx:             context.Background(),
			fromBlock:       0,
			toBlock:         2,
			expectedBridges: nil,
			expectedErr:     fmt.Errorf(errBlockNotProcessedFormat, 2, 0),
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
				LeafType:           1,
				OriginNetwork:      1,
				OriginAddress:      common.HexToAddress("01"),
				DestinationNetwork: 1,
				DestinationAddress: common.HexToAddress("01"),
				Amount:             big.NewInt(1),
				Metadata:           common.Hex2Bytes("01"),
				DepositCount:       0,
			}},
			Event{Claim: &Claim{
				BlockNum:           1,
				BlockPos:           1,
				GlobalIndex:        big.NewInt(1),
				OriginNetwork:      1,
				OriginAddress:      common.HexToAddress("01"),
				DestinationAddress: common.HexToAddress("01"),
				Amount:             big.NewInt(1),
				MainnetExitRoot:    common.Hash{},
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
				LeafType:           2,
				OriginNetwork:      2,
				OriginAddress:      common.HexToAddress("02"),
				DestinationNetwork: 2,
				DestinationAddress: common.HexToAddress("02"),
				Amount:             big.NewInt(2),
				Metadata:           common.Hex2Bytes("02"),
				DepositCount:       1,
			}},
			Event{Bridge: &Bridge{
				BlockNum:           3,
				BlockPos:           1,
				LeafType:           3,
				OriginNetwork:      3,
				OriginAddress:      common.HexToAddress("03"),
				DestinationNetwork: 3,
				DestinationAddress: common.HexToAddress("03"),
				Amount:             big.NewInt(0),
				Metadata:           common.Hex2Bytes("03"),
				DepositCount:       2,
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
	testClaim := &Claim{
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
	}

	_, err = tx.Exec(`INSERT INTO block (num, hash) VALUES ($1, $2)`, testClaim.BlockNum, fmt.Sprintf("0x%x", testClaim.BlockNum))
	require.NoError(t, err)
	require.NoError(t, meddler.Insert(tx, "claim", testClaim))

	require.NoError(t, tx.Commit())

	// get test claim
	claims, err := p.GetClaims(context.Background(), 1, 1)
	require.NoError(t, err)
	require.Len(t, claims, 1)
	require.Equal(t, testClaim, &claims[0])
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
		{DepositCount: 0, BlockNum: 1, Amount: big.NewInt(1), DestinationNetwork: 10, FromAddress: common.HexToAddress("0xE34aaF64b29273B7D567FCFc40544c014EEe9970")},
		{DepositCount: 1, BlockNum: 2, Amount: big.NewInt(1), DestinationNetwork: 10, FromAddress: common.HexToAddress("0xE34aaF64b29273B7D567FCFc40544c014EEe9970")},
		{DepositCount: 2, BlockNum: 3, Amount: big.NewInt(1), DestinationNetwork: 20, FromAddress: common.HexToAddress("0xE34aaF64b29273B7D567FCFc40544c014EEe9970")},
		{DepositCount: 3, BlockNum: 4, Amount: big.NewInt(1), DestinationNetwork: 30, FromAddress: common.HexToAddress("0xE34aaF64b29273B7D567FCFc40544c014EEe9970")},
		{DepositCount: 4, BlockNum: 5, Amount: big.NewInt(1), DestinationNetwork: 30, FromAddress: common.HexToAddress("0xE34aaF64b29273B7D567FCFc40544c014EEe9970")},
		{DepositCount: 5, BlockNum: 6, Amount: big.NewInt(1), DestinationNetwork: 30, FromAddress: common.HexToAddress("0xE34aaF64b29273B7D567FCFc40544c014EEe9970")},
		{DepositCount: 6, BlockNum: 7, Amount: big.NewInt(1), DestinationNetwork: 50, FromAddress: common.HexToAddress("0xd34aaF64b29273B7D567FCFc40544c014EEe9970")},
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

	depositCountPtr := func(i uint64) *uint64 {
		return &i
	}

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
			depositCount:    depositCountPtr(1),
			expectedCount:   1,
			expectedBridges: []*Bridge{bridges[1]},
			expectedError:   "",
		},
		{
			name:            "t5",
			pageSize:        3,
			page:            2,
			depositCount:    depositCountPtr(1),
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
			depositCount:    depositCountPtr(0),
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
			depositCount: depositCountPtr(3),
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
			depositCount:    depositCountPtr(0),
			expectedCount:   1,
			expectedBridges: []*Bridge{bridges[0]},
			expectedError:   "",
		},
		{
			name:            "t11",
			pageSize:        1,
			page:            1,
			fromAddress:     "0xe34aaF64b29273B7D567FCFc40544c014EEe9970",
			depositCount:    depositCountPtr(0),
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
				ChainID:   1,
				Addresses: []common.Address{common.HexToAddress("0x123")},
				DBVersion: intPtr(1),
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
				ChainID:   1,
				Addresses: []common.Address{common.HexToAddress("0x123")},
				DBVersion: intPtr(2),
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
				ChainID:   1,
				Addresses: []common.Address{common.HexToAddress("0x123")},
				DBVersion: intPtr(1),
			},
			storage: BridgeSyncRuntimeData{
				ChainID:   1,
				Addresses: []common.Address{common.HexToAddress("0x456")},
				DBVersion: intPtr(1),
			},
			expectError: true,
			errorMsg:    "addresses[0] mismatch: 0x0000000000000000000000000000000000000123 != 0x0000000000000000000000000000000000000456",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.current.IsCompatible(tt.storage)

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
		require.Len(t, claims, 2)                   // Two claims with the same global index
		require.Equal(t, *testClaims[1], claims[0]) // Check first claim
		require.Equal(t, *testClaims[2], claims[1]) // Check second claim
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

func intPtr(i int) *int {
	return &i
}

func TestProcessor_ErrorPathLogging(t *testing.T) {
	t.Parallel()

	t.Run("GetBridges error paths", func(t *testing.T) {
		t.Parallel()
		p := createTestProcessor(t, "GetBridgesErrorPaths")

		// Test queryBlockRange failure - block not processed
		_, err := p.GetBridges(context.Background(), 1, 5)
		require.Error(t, err)
		require.Contains(t, err.Error(), "block 5 not processed")

		// Test successful case with no bridges
		tx, err := p.db.BeginTx(context.Background(), nil)
		require.NoError(t, err)
		_, err = tx.Exec(`INSERT INTO block (num, hash) VALUES ($1, $2)`, 1, "0x1")
		require.NoError(t, err)
		require.NoError(t, tx.Commit())

		bridges, err := p.GetBridges(context.Background(), 1, 1)
		require.NoError(t, err)
		require.Empty(t, bridges)
	})

	t.Run("GetClaims error paths", func(t *testing.T) {
		t.Parallel()
		p := createTestProcessor(t, "GetClaimsErrorPaths")

		// Test queryBlockRange failure - block not processed
		_, err := p.GetClaims(context.Background(), 1, 5)
		require.Error(t, err)
		require.Contains(t, err.Error(), "block 5 not processed")

		// Test successful case with no claims
		tx, err := p.db.BeginTx(context.Background(), nil)
		require.NoError(t, err)
		_, err = tx.Exec(`INSERT INTO block (num, hash) VALUES ($1, $2)`, 1, "0x1")
		require.NoError(t, err)
		require.NoError(t, tx.Commit())

		claims, err := p.GetClaims(context.Background(), 1, 1)
		require.NoError(t, err)
		require.Empty(t, claims)
	})

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
		BlockNum:           blockNum,
		BlockPos:           uint64(blockPos),
		BlockTimestamp:     1234567890,
		TxHash:             common.HexToHash("0x1234567890123456789012345678901234567890123456789012345678901234"),
		FromAddress:        common.HexToAddress("0x1234567890123456789012345678901234567890"),
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

func TestProcessBlockWithClaims(t *testing.T) {
	path := path.Join(t.TempDir(), fmt.Sprintf("%s.db", t.Name()))
	p, err := newProcessor(path, "bridge-syncer", log.GetDefaultLogger(), dbQueryTimeout)
	require.NoError(t, err)

	newClaim := func(blockNum, pos uint64, gi int64, origin string, dest string, amount int64, mer string) *Claim {
		return &Claim{
			BlockNum:           blockNum,
			BlockPos:           pos,
			GlobalIndex:        big.NewInt(gi),
			OriginNetwork:      uint32(gi), // just distinct per test
			OriginAddress:      common.HexToAddress(origin),
			DestinationAddress: common.HexToAddress(dest),
			Amount:             big.NewInt(amount),
			MainnetExitRoot:    common.HexToHash(mer),
		}
	}

	block := func(num uint64, events ...any) sync.Block {
		return sync.Block{Num: num, Events: events}
	}

	// Initial claims (block 1)
	claim1 := newClaim(1, 0, 1, "1", "1", 1, "")
	claim2 := newClaim(1, 1, 2, "2", "2", 2, "5ca1e1")

	// Replacement claim (block 2)
	claim1Updated := newClaim(2, 0, 1, "3", "3", 10, "5ca1e")

	// Unset claim for claim2 (global index 2)
	unsetClaim2 := &UnsetClaim{
		BlockNum:    3,
		BlockPos:    0,
		GlobalIndex: claim2.GlobalIndex,
		CreatedAt:   uint64(time.Now().UTC().Unix()),
	}

	// Invalid claims
	invalidClaim1 := NewInvalidClaim(claim1, InvalidGERClaimCorrect.String())
	invalidClaim2 := NewInvalidClaim(claim2, InvalidGERClaimIncorrect.String())

	tests := []struct {
		name                  string
		blocks                []sync.Block
		expectedClaims        []*Claim
		expectedInvalidClaims []*InvalidClaim
	}{
		{
			name: "update claim with same global index",
			blocks: []sync.Block{
				block(1, Event{Claim: claim1}, Event{Claim: claim2}),
				block(2, Event{Claim: claim1Updated}),
			},
			expectedClaims: []*Claim{
				claim1Updated,
				claim2,
			},
			expectedInvalidClaims: []*InvalidClaim{invalidClaim1},
		},
		{
			name: "original claim remains in the db when unclaimed",
			blocks: []sync.Block{
				block(3, Event{UnsetClaim: unsetClaim2}),
			},
			expectedClaims: []*Claim{
				claim1Updated,
				claim2,
			},
			expectedInvalidClaims: []*InvalidClaim{
				invalidClaim1,
				invalidClaim2,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// process blocks
			for _, b := range tt.blocks {
				require.NoError(t, p.ProcessBlock(context.Background(), b))
			}

			// check final claims
			for _, expected := range tt.expectedClaims {
				dbClaims, err := p.GetClaimsByGlobalIndex(context.Background(), expected.GlobalIndex)
				require.NoError(t, err)
				require.Len(t, dbClaims, 1)
				require.Equal(t, expected, &dbClaims[0])
			}

			// check invalid_claim rows
			for _, expected := range tt.expectedInvalidClaims {
				dbInvalidClaims, err := p.getInvalidClaimsByGlobalIndex(expected.GlobalIndex)
				require.NoError(t, err)
				require.Len(t, dbInvalidClaims, 1)
				require.Equal(t, expected, dbInvalidClaims[0])
			}
		})
	}
}

func TestDeleteClaimReason_String(t *testing.T) {
	tests := []struct {
		name     string
		reason   DeleteClaimReason
		expected string
	}{
		{
			name:     "InvalidGERClaimCorrect",
			reason:   InvalidGERClaimCorrect,
			expected: "invalid_ger_claim_correct",
		},
		{
			name:     "InvalidGERClaimIncorrect",
			reason:   InvalidGERClaimIncorrect,
			expected: "invalid_ger_claim_incorrect",
		},
		{
			name:     "UnknownReason",
			reason:   DeleteClaimReason(999), // something outside defined range
			expected: "unknown",
		},
		{
			name:     "NegativeReason",
			reason:   DeleteClaimReason(-1),
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.reason.String()
			require.Equal(t, tt.expected, got)
		})
	}
}

func TestBackwardLETEvent_CombinedBridgeAndRootDeletion(t *testing.T) {
	t.Parallel()

	path := path.Join(t.TempDir(), "backwardlet_combined.db")
	err := migrations.RunMigrations(path)
	require.NoError(t, err)

	logger := log.WithFields("module", "bridge-syncer")
	p, err := newProcessor(path, "bridge-syncer", logger, dbQueryTimeout)
	require.NoError(t, err)

	ctx := context.Background()

	// Step 1: Insert initial bridges using ProcessBlock (this will also create roots via PutLeaf)
	initialBridges := []*Bridge{
		{BlockNum: 1, BlockPos: 0, DepositCount: 0, Amount: big.NewInt(100), OriginNetwork: 1, DestinationNetwork: 1, LeafType: 1, OriginAddress: common.HexToAddress("0x01"), DestinationAddress: common.HexToAddress("0x02"), Metadata: []byte{}},
		{BlockNum: 2, BlockPos: 0, DepositCount: 1, Amount: big.NewInt(200), OriginNetwork: 1, DestinationNetwork: 1, LeafType: 1, OriginAddress: common.HexToAddress("0x01"), DestinationAddress: common.HexToAddress("0x02"), Metadata: []byte{}},
		{BlockNum: 3, BlockPos: 0, DepositCount: 2, Amount: big.NewInt(300), OriginNetwork: 1, DestinationNetwork: 1, LeafType: 1, OriginAddress: common.HexToAddress("0x01"), DestinationAddress: common.HexToAddress("0x02"), Metadata: []byte{}},
		{BlockNum: 3, BlockPos: 1, DepositCount: 3, Amount: big.NewInt(350), OriginNetwork: 1, DestinationNetwork: 1, LeafType: 1, OriginAddress: common.HexToAddress("0x01"), DestinationAddress: common.HexToAddress("0x02"), Metadata: []byte{}}, // Same block, different position
		{BlockNum: 4, BlockPos: 0, DepositCount: 4, Amount: big.NewInt(400), OriginNetwork: 1, DestinationNetwork: 1, LeafType: 1, OriginAddress: common.HexToAddress("0x01"), DestinationAddress: common.HexToAddress("0x02"), Metadata: []byte{}},
		{BlockNum: 5, BlockPos: 0, DepositCount: 5, Amount: big.NewInt(500), OriginNetwork: 1, DestinationNetwork: 1, LeafType: 1, OriginAddress: common.HexToAddress("0x01"), DestinationAddress: common.HexToAddress("0x02"), Metadata: []byte{}},
	}

	// Group bridges by block number to process multiple bridges in the same block
	bridgesByBlock := make(map[uint64][]*Bridge)
	for _, bridge := range initialBridges {
		bridgesByBlock[bridge.BlockNum] = append(bridgesByBlock[bridge.BlockNum], bridge)
	}

	// Process each block with its bridges
	for blockNum, bridges := range bridgesByBlock {
		events := make([]any, 0, len(bridges))
		for _, bridge := range bridges {
			events = append(events, Event{Bridge: bridge})
		}

		block := sync.Block{
			Num:    blockNum,
			Hash:   common.HexToHash(fmt.Sprintf("0x%x", blockNum)),
			Events: events,
		}
		err = p.ProcessBlock(ctx, block)
		require.NoError(t, err, "Failed to process block %d with bridges", blockNum)
	}

	// Verify initial state: should have 6 bridges and 6 roots
	allBridges, err := p.GetBridges(ctx, 1, 5)
	require.NoError(t, err)
	require.Len(t, allBridges, 6, "Should have 6 bridges initially")

	rows, err := p.db.Query(`SELECT hash, position, block_num, block_position FROM root ORDER BY block_num, block_position`)
	require.NoError(t, err)
	var initialRoots []types.Root
	for rows.Next() {
		var root types.Root
		var hashStr string
		err := rows.Scan(&hashStr, &root.Index, &root.BlockNum, &root.BlockPosition)
		require.NoError(t, err)
		root.Hash = common.HexToHash(hashStr)
		initialRoots = append(initialRoots, root)
	}
	rows.Close()
	require.Len(t, initialRoots, 6, "Should have 6 roots initially")

	// Get the root at deposit_count 2 (NewDepositCount) and the last root (PreviousRoot)
	var newRootHash common.Hash
	rows, err = p.db.Query(`SELECT hash FROM root WHERE position = $1 ORDER BY block_num, block_position LIMIT 1`, 2)
	require.NoError(t, err)
	require.True(t, rows.Next(), "Should find root at position 2")
	var newRootHashStr string
	err = rows.Scan(&newRootHashStr)
	require.NoError(t, err)
	newRootHash = common.HexToHash(newRootHashStr)
	rows.Close()

	var previousRootHash common.Hash
	rows, err = p.db.Query(`SELECT hash FROM root ORDER BY block_num DESC, block_position DESC LIMIT 1`)
	require.NoError(t, err)
	require.True(t, rows.Next(), "Should find last root")
	var previousRootHashStr string
	err = rows.Scan(&previousRootHashStr)
	require.NoError(t, err)
	previousRootHash = common.HexToHash(previousRootHashStr)
	rows.Close()

	// Step 2: Process backwardLET event that should delete bridges with deposit_count > 2 and roots after NewRoot
	backwardLET := &BackwardLET{
		BlockNum:             10,
		BlockPos:             0,
		PreviousDepositCount: big.NewInt(5),
		PreviousRoot:         previousRootHash,
		NewDepositCount:      big.NewInt(2),
		NewRoot:              newRootHash,
	}

	block := sync.Block{
		Num:  backwardLET.BlockNum,
		Hash: common.HexToHash(fmt.Sprintf("0x%x", backwardLET.BlockNum)),
		Events: []any{
			Event{BackwardLET: backwardLET},
		},
	}

	err = p.ProcessBlock(ctx, block)
	require.NoError(t, err, "Failed to process backwardLET event")

	// Step 3: Verify correct deletions from bridge table
	allBridges, err = p.GetBridges(ctx, 1, 10)
	require.NoError(t, err)
	require.Len(t, allBridges, 3, "Should have 3 bridges after backwardLET (deposit_count 0, 1, 2)")

	// Verify that the bridge at block 3, position 1 (deposit_count 3) was deleted
	foundBlock3Pos1 := false
	for _, bridge := range allBridges {
		if bridge.BlockNum == 3 && bridge.BlockPos == 1 {
			foundBlock3Pos1 = true
			break
		}
	}
	require.False(t, foundBlock3Pos1, "Bridge at block 3, position 1 (deposit_count 3) should have been deleted")

	depositCounts := make(map[uint32]bool)
	for _, bridge := range allBridges {
		depositCounts[bridge.DepositCount] = true
		require.LessOrEqual(t, bridge.DepositCount, uint32(2), "Bridge with deposit_count %d should have been deleted", bridge.DepositCount)
	}
	require.True(t, depositCounts[0], "Bridge with deposit_count 0 should exist")
	require.True(t, depositCounts[1], "Bridge with deposit_count 1 should exist")
	require.True(t, depositCounts[2], "Bridge with deposit_count 2 should exist")

	// Step 4: Verify correct deletions from root table
	rows, err = p.db.Query(`SELECT hash, position, block_num, block_position FROM root ORDER BY block_num, block_position`)
	require.NoError(t, err)
	var remainingRoots []types.Root
	for rows.Next() {
		var root types.Root
		var hashStr string
		err := rows.Scan(&hashStr, &root.Index, &root.BlockNum, &root.BlockPosition)
		require.NoError(t, err)
		root.Hash = common.HexToHash(hashStr)
		remainingRoots = append(remainingRoots, root)
	}
	rows.Close()

	require.Len(t, remainingRoots, 3, "Should have 3 roots after backwardLET (position 0, 1, 2)")

	// Verify roots are in correct order and have correct indices
	for i, root := range remainingRoots {
		require.Equal(t, uint32(i), root.Index, "Root at position %d should have index %d", i, i)
		require.LessOrEqual(t, root.Index, uint32(2), "Root with index %d should have been deleted", root.Index)
	}

	// Verify the NewRoot is the last root
	require.Equal(t, newRootHash, remainingRoots[len(remainingRoots)-1].Hash, "Last root should match NewRoot")

	// Step 5: Insert bridges again to verify the system continues to work
	newBridges := []*Bridge{
		{BlockNum: 11, BlockPos: 0, DepositCount: 3, Amount: big.NewInt(600), OriginNetwork: 1, DestinationNetwork: 1, LeafType: 1, OriginAddress: common.HexToAddress("0x01"), DestinationAddress: common.HexToAddress("0x02"), Metadata: []byte{}},
		{BlockNum: 12, BlockPos: 0, DepositCount: 4, Amount: big.NewInt(700), OriginNetwork: 1, DestinationNetwork: 1, LeafType: 1, OriginAddress: common.HexToAddress("0x01"), DestinationAddress: common.HexToAddress("0x02"), Metadata: []byte{}},
	}

	for _, bridge := range newBridges {
		block := sync.Block{
			Num:    bridge.BlockNum,
			Hash:   common.HexToHash(fmt.Sprintf("0x%x", bridge.BlockNum)),
			Events: []any{Event{Bridge: bridge}},
		}
		err = p.ProcessBlock(ctx, block)
		require.NoError(t, err, "Failed to process block %d with new bridge", bridge.BlockNum)
	}

	// Step 6: Verify final state after inserting new bridges
	allBridges, err = p.GetBridges(ctx, 1, 12)
	require.NoError(t, err)
	require.Len(t, allBridges, 5, "Should have 5 bridges after inserting new ones (0, 1, 2, 3, 4)")

	finalDepositCounts := make(map[uint32]bool)
	for _, bridge := range allBridges {
		finalDepositCounts[bridge.DepositCount] = true
	}
	require.True(t, finalDepositCounts[0], "Bridge with deposit_count 0 should exist")
	require.True(t, finalDepositCounts[1], "Bridge with deposit_count 1 should exist")
	require.True(t, finalDepositCounts[2], "Bridge with deposit_count 2 should exist")
	require.True(t, finalDepositCounts[3], "Bridge with deposit_count 3 should exist")
	require.True(t, finalDepositCounts[4], "Bridge with deposit_count 4 should exist")

	// Verify roots: should have 5 roots (0, 1, 2, 3, 4)
	rows, err = p.db.Query(`SELECT hash, position, block_num, block_position FROM root ORDER BY block_num, block_position`)
	require.NoError(t, err)
	var finalRoots []types.Root
	for rows.Next() {
		var root types.Root
		var hashStr string
		err := rows.Scan(&hashStr, &root.Index, &root.BlockNum, &root.BlockPosition)
		require.NoError(t, err)
		root.Hash = common.HexToHash(hashStr)
		finalRoots = append(finalRoots, root)
	}
	rows.Close()

	require.Len(t, finalRoots, 5, "Should have 5 roots after inserting new bridges")

	// Verify roots are in correct order
	for i, root := range finalRoots {
		require.Equal(t, uint32(i), root.Index, "Root at position %d should have index %d", i, i)
	}

	// Verify backwardLET event was stored in database
	rows, err = p.db.Query(`SELECT block_num, block_pos, previous_deposit_count, previous_root, new_deposit_count, new_root FROM backward_let WHERE block_num = $1`, backwardLET.BlockNum)
	require.NoError(t, err)
	require.True(t, rows.Next(), "BackwardLET event should be stored in database")
	var (
		storedBlockNum, storedBlockPos uint64
		storedPreviousDepositCount     string
		storedPreviousRoot             common.Hash
		storedNewDepositCount          string
		storedNewRoot                  common.Hash
	)
	err = rows.Scan(&storedBlockNum, &storedBlockPos, &storedPreviousDepositCount, &storedPreviousRoot, &storedNewDepositCount, &storedNewRoot)
	require.NoError(t, err)
	require.Equal(t, backwardLET.BlockNum, storedBlockNum)
	require.Equal(t, backwardLET.BlockPos, storedBlockPos)
	require.Equal(t, backwardLET.PreviousDepositCount.String(), storedPreviousDepositCount)
	require.Equal(t, backwardLET.PreviousRoot.Hex(), storedPreviousRoot)
	require.Equal(t, backwardLET.NewDepositCount.String(), storedNewDepositCount)
	require.Equal(t, backwardLET.NewRoot.Hex(), storedNewRoot)
	require.False(t, rows.Next(), "Should have only one BackwardLET event")
	rows.Close()
}
