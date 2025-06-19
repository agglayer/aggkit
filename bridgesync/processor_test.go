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

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/fep/etrog/polygonzkevmbridge"
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
	p, err := newProcessor(path, "bridge-syncer", logger)
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
		Events: []interface{}{
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
		Events: []interface{}{
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
		Events: []interface{}{},
	}
	block5 = sync.Block{
		Num: 5,
		Events: []interface{}{
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

	recordsNum, err := a.p.GetTotalNumberOfRecords(a.tableName, "")
	require.NoError(t, err)
	require.Equal(t, a.expectedRecordsNum, recordsNum)
}

func eventsToBridges(events []interface{}) []Bridge {
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

func eventsToClaims(events []interface{}) []Claim {
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

func TestDecodeGlobalIndex(t *testing.T) {
	t.Parallel()

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
			name:                "Invalid global index length",
			globalIndex:         big.NewInt(0).SetBytes([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}),
			expectedMainnetFlag: false,
			expectedRollupIndex: 0,
			expectedLocalIndex:  0,
			expectedErr:         errors.New("invalid global index length"),
		},
	}

	for _, tt := range tests {
		tt := tt

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
	path := path.Join(t.TempDir(), "aggsenderTestInsertAndGetClaim.sqlite")
	log.Debugf("sqlite path: %s", path)
	err := migrations.RunMigrations(path)
	require.NoError(t, err)
	logger := log.WithFields("bridge-syncer", "foo")
	p, err := newProcessor(path, "foo", logger)
	require.NoError(t, err)

	tx, err := p.db.BeginTx(context.Background(), nil)
	require.NoError(t, err)

	// insert test claim
	testClaim := &Claim{
		BlockNum:            1,
		BlockPos:            0,
		GlobalIndex:         GenerateGlobalIndex(true, 0, 1093),
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
			p, err := newProcessor(path, "foo", logger)
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
	p, err := newProcessor(path, "foo", logger)
	require.NoError(t, err)
	err = p.ProcessBlock(context.Background(), sync.Block{
		Num: 0,
		Events: []interface{}{
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
	p, err := newProcessor(path, "bridge-syncer", logger)
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
			name:          "t1",
			pageSize:      1,
			page:          1,
			depositCount:  nil,
			expectedCount: len(bridges),
			expectedBridges: []*Bridge{
				bridges[6],
			},
			expectedError: "",
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
			name:          "t4",
			pageSize:      1,
			page:          1,
			depositCount:  depositCountPtr(1),
			expectedCount: 1,
			expectedBridges: []*Bridge{
				bridges[1],
			},
			expectedError: "",
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
			name:          "t7",
			pageSize:      1,
			page:          1,
			depositCount:  depositCountPtr(0),
			expectedCount: 1,
			expectedBridges: []*Bridge{
				bridges[0],
			},
			expectedError: "",
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
			name:          "t10",
			pageSize:      1,
			page:          1,
			fromAddress:   "0xE34aaF64b29273B7D567FCFc40544c014EEe9970",
			depositCount:  depositCountPtr(0),
			expectedCount: 1,
			expectedBridges: []*Bridge{
				bridges[0],
			},
			expectedError: "",
		},
		{
			name:          "t11",
			pageSize:      1,
			page:          1,
			fromAddress:   "0xe34aaF64b29273B7D567FCFc40544c014EEe9970",
			depositCount:  depositCountPtr(0),
			expectedCount: 1,
			expectedBridges: []*Bridge{
				bridges[0],
			},
			expectedError: "",
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
			name:          "t13",
			pageSize:      10,
			page:          1,
			fromAddress:   "0xD34AAF64b29273B7D567FCFc40544c014EEe9970",
			depositCount:  nil,
			expectedCount: 1,
			expectedBridges: []*Bridge{
				bridges[6],
			},
			expectedError: "",
		},
	}

	for _, tc := range testCases {
		tc := tc

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
		{BlockNum: 1, GlobalIndex: num2, Amount: big.NewInt(1), OriginNetwork: 1, FromAddress: common.HexToAddress("0xE34aaF64b29273B7D567FCFc40544c014EEe9970"), MainnetExitRoot: common.Hash{}},
		{BlockNum: 2, GlobalIndex: big.NewInt(2), Amount: big.NewInt(1), OriginNetwork: 1, FromAddress: common.HexToAddress("0xE34aaF64b29273B7D567FCFc40544c014EEe9970"), MainnetExitRoot: common.Hash{}},
		{BlockNum: 3, GlobalIndex: uint64Max, Amount: big.NewInt(1), OriginNetwork: 2, FromAddress: common.HexToAddress("0xE34aaF64b29273B7D567FCFc40544c014EEe9970"), MainnetExitRoot: common.Hash{}},
		{BlockNum: 4, GlobalIndex: num1, Amount: big.NewInt(1), OriginNetwork: 2, FromAddress: common.HexToAddress("0xE34aaF64b29273B7D567FCFc40544c014EEe9970"), MainnetExitRoot: common.Hash{}},
		{BlockNum: 5, GlobalIndex: big.NewInt(5), Amount: big.NewInt(1), OriginNetwork: 3, FromAddress: common.HexToAddress("0xE34aaF64b29273B7D567FCFc40544c014EEe9970"), MainnetExitRoot: common.Hash{}},
		{BlockNum: 6, GlobalIndex: uint256Max, Amount: big.NewInt(1), OriginNetwork: 4, FromAddress: common.HexToAddress("0xd34aaF64b29273B7D567FCFc40544c014EEe9970"), MainnetExitRoot: common.Hash{}},
	}

	path := path.Join(t.TempDir(), "bridgesyncGetClaimsPaged.sqlite")
	require.NoError(t, migrations.RunMigrations(path))
	logger := log.WithFields("module", "bridge-syncer")
	p, err := newProcessor(path, "bridge-syncer", logger)
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
		fromAddress    string
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
			fromAddress:    claims[0].FromAddress.String(),
			expectedCount:  3,
			expectedClaims: []*Claim{claims[4], claims[1], claims[0]},
			expectedError:  "",
		},
		{
			name:           "filter by network ids (all results within the same page) and from address case insensitive",
			pageSize:       3,
			page:           1,
			networkIDs:     []uint32{claims[0].OriginNetwork, claims[4].OriginNetwork},
			fromAddress:    "0xd34aaF64b29273B7D567FCFc40544c014EEe9970",
			expectedCount:  0,
			expectedClaims: []*Claim{},
			expectedError:  "",
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			claims, count, err := p.GetClaimsPaged(ctx, tc.page, tc.pageSize, tc.networkIDs, tc.fromAddress)

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
	p, err := newProcessor(path, "bridge-syncer", logger)
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
			Events: []interface{}{Event{TokenMapping: tokenMappingEvt}},
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

			result, totalTokenMappings, err := p.GetTokenMappings(context.Background(), tt.pageNumber, tt.pageSize)
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
	p, err := newProcessor(path, "bridge-syncer", logger)
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
	zeroAddr := common.HexToAddress("0x0")

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

	isFound, err := actualClaim.decodePreEtrogCalldata(zeroAddr, claimAssetData)
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
		zeroAddr               = common.HexToAddress("0x0")
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
				invalidTypePlaceholder, // Invalid GlobalIndex type (should be uint32)
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

			match, err := claim.decodePreEtrogCalldata(zeroAddr, tt.data)

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
		zeroAddr               = common.HexToAddress("0x0")
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

			isDecoded, err := claim.decodeEtrogCalldata(zeroAddr, tt.data)
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, tt.expectedIsDecoded, isDecoded)
		})
	}
}
