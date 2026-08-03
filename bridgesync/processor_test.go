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
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/polygonzkevmbridge"
	aggkitabi "github.com/agglayer/aggkit/abi"
	bridgetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync/migrations"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
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

// errInvalidPageNumberForBridges is the error message returned by calculateOffset for the
// "bridges" table when the requested page is out of range; shared by paged-bridge query tests.
const errInvalidPageNumberForBridges = "invalid page number for given page size and total number of bridges"

// newTestProcessor is a test helper that creates a processor from a file path.
func newTestProcessor(dbPath string, syncerID string, logger *log.Logger, dbQueryTimeout time.Duration) (*processor, error) {
	database, err := newSqliteDB(dbPath)
	if err != nil {
		return nil, err
	}
	return newProcessor(database, syncerID, logger, dbQueryTimeout)
}

func TestProcessor(t *testing.T) {
	path := path.Join(t.TempDir(), "bridgeSyncerProcessor.db")
	logger := log.WithFields("module", "bridge-syncer")
	p, err := newTestProcessor(path, "bridge-syncer", logger, dbQueryTimeout)
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

	actualLastProcessedBlock, _, actualErr := a.p.GetLastProcessedBlock(a.ctx)
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
			p, err := newTestProcessor(path, "foo", logger, dbQueryTimeout)
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
	p, err := newTestProcessor(path, "foo", logger, dbQueryTimeout)
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
	p, err := newTestProcessor(path, "bridge-syncer", logger, dbQueryTimeout)
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
			expectedError:   errInvalidPageNumberForBridges,
		},
		{
			name:            "t6",
			pageSize:        2,
			page:            20,
			depositCount:    nil,
			expectedCount:   len(bridges),
			expectedBridges: []*Bridge{},
			expectedError:   errInvalidPageNumberForBridges,
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

func TestGetBridgesInDepositRange(t *testing.T) {
	t.Parallel()
	fromBlock := uint64(1)
	toBlock := uint64(10)
	bridges := []*Bridge{
		{DepositCount: 0, BlockNum: 1, Amount: big.NewInt(1), DestinationNetwork: 10},
		{DepositCount: 1, BlockNum: 2, Amount: big.NewInt(1), DestinationNetwork: 10},
		{DepositCount: 2, BlockNum: 3, Amount: big.NewInt(1), DestinationNetwork: 20},
		{DepositCount: 3, BlockNum: 4, Amount: big.NewInt(1), DestinationNetwork: 30},
		{DepositCount: 4, BlockNum: 5, Amount: big.NewInt(1), DestinationNetwork: 30},
		{DepositCount: 5, BlockNum: 6, Amount: big.NewInt(1), DestinationNetwork: 40},
		{DepositCount: 6, BlockNum: 7, Amount: big.NewInt(1), DestinationNetwork: 50},
	}
	// Bridge only present in the archive table (e.g. rolled back by a BackwardLET). Its
	// deposit_count falls inside every range used below, so it doubles as the
	// archive-exclusion check for all test cases.
	archivedBridge := &Bridge{DepositCount: 7, BlockNum: 8, Amount: big.NewInt(1), DestinationNetwork: 10}

	path := path.Join(t.TempDir(), "bridgesyncGetBridgesInDepositRange.sqlite")
	require.NoError(t, migrations.RunMigrations(path))
	logger := log.WithFields("bridge-syncer", "foo")
	p, err := newTestProcessor(path, "bridge-syncer", logger, dbQueryTimeout)
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
	require.NoError(t, meddler.Insert(tx, "bridge_archive", archivedBridge))
	require.NoError(t, tx.Commit())

	testCases := []struct {
		name             string
		pageSize         uint32
		page             uint32
		fromDepositCount *uint64
		toDepositCount   uint64
		networkIDs       []uint32
		expectedCount    int
		expectedBridges  []*Bridge
		expectedError    string
	}{
		{
			name:             "from omitted returns full history up to toDepositCount",
			pageSize:         20,
			page:             1,
			fromDepositCount: nil,
			toDepositCount:   6,
			expectedCount:    len(bridges),
			expectedBridges: []*Bridge{
				bridges[0], bridges[1], bridges[2], bridges[3], bridges[4], bridges[5], bridges[6],
			},
		},
		{
			name:             "from omitted, narrower toDepositCount",
			pageSize:         20,
			page:             1,
			fromDepositCount: nil,
			toDepositCount:   2,
			expectedCount:    3,
			expectedBridges:  []*Bridge{bridges[0], bridges[1], bridges[2]},
		},
		{
			name:             "empty range when from == to",
			pageSize:         20,
			page:             1,
			fromDepositCount: uint64Ptr(3),
			toDepositCount:   3,
			expectedCount:    0,
			expectedBridges:  []*Bridge{},
		},
		{
			name:             "empty range when no bridges fall within bounds",
			pageSize:         20,
			page:             1,
			fromDepositCount: uint64Ptr(100),
			toDepositCount:   200,
			expectedCount:    0,
			expectedBridges:  []*Bridge{},
		},
		{
			name:             "exclusive lower bound, inclusive upper bound",
			pageSize:         20,
			page:             1,
			fromDepositCount: uint64Ptr(1),
			toDepositCount:   4,
			expectedCount:    3,
			expectedBridges:  []*Bridge{bridges[2], bridges[3], bridges[4]},
		},
		{
			name:             "destination filtering",
			pageSize:         20,
			page:             1,
			fromDepositCount: nil,
			toDepositCount:   6,
			networkIDs:       []uint32{bridges[0].DestinationNetwork, bridges[6].DestinationNetwork},
			expectedCount:    3,
			expectedBridges:  []*Bridge{bridges[0], bridges[1], bridges[6]},
		},
		{
			name:             "destination filtering combined with range excludes out-of-range matches",
			pageSize:         20,
			page:             1,
			fromDepositCount: nil,
			toDepositCount:   1,
			networkIDs:       []uint32{bridges[6].DestinationNetwork},
			expectedCount:    0,
			expectedBridges:  []*Bridge{},
		},
		{
			name:             "pagination first page ordered ascending by deposit_count",
			pageSize:         2,
			page:             1,
			fromDepositCount: nil,
			toDepositCount:   6,
			expectedCount:    len(bridges),
			expectedBridges:  []*Bridge{bridges[0], bridges[1]},
		},
		{
			name:             "pagination second page ordered ascending by deposit_count",
			pageSize:         2,
			page:             2,
			fromDepositCount: nil,
			toDepositCount:   6,
			expectedCount:    len(bridges),
			expectedBridges:  []*Bridge{bridges[2], bridges[3]},
		},
		{
			name:             "pagination last (partial) page",
			pageSize:         2,
			page:             4,
			fromDepositCount: nil,
			toDepositCount:   6,
			expectedCount:    len(bridges),
			expectedBridges:  []*Bridge{bridges[6]},
		},
		{
			name:             "pagination out of range page errors",
			pageSize:         2,
			page:             20,
			fromDepositCount: nil,
			toDepositCount:   6,
			expectedCount:    len(bridges),
			expectedBridges:  []*Bridge{},
			expectedError:    errInvalidPageNumberForBridges,
		},
		{
			// archivedBridge (deposit_count=7) lives only in bridge_archive; it must never be
			// returned even though it falls within this range.
			name:             "archive table is excluded",
			pageSize:         20,
			page:             1,
			fromDepositCount: nil,
			toDepositCount:   7,
			expectedCount:    len(bridges),
			expectedBridges: []*Bridge{
				bridges[0], bridges[1], bridges[2], bridges[3], bridges[4], bridges[5], bridges[6],
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			bridges, count, err := p.GetBridgesInDepositRange(
				ctx, tc.page, tc.pageSize, tc.fromDepositCount, tc.toDepositCount, tc.networkIDs)

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

func TestProcessor_GetTokenMappings(t *testing.T) {
	t.Parallel()

	const tokenMappingsCount = 50

	path := path.Join(t.TempDir(), "tokenMapping.db")
	err := migrations.RunMigrations(path)
	require.NoError(t, err)

	logger := log.WithFields("module", "bridge-syncer")
	p, err := newTestProcessor(path, "bridge-syncer", logger, dbQueryTimeout)
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
	p, err := newTestProcessor(path, "bridge-syncer", logger, dbQueryTimeout)
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

	expectedClaim := &claimsynctypes.Claim{
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

	actualClaim := &claimsynctypes.Claim{
		GlobalIndex: new(big.Int).SetUint64(uint64(globalIndex)),
	}
	claimAssetPreEtrogMethodID := common.Hex2Bytes("2cffd02e")
	method, err := bridgeV1ABI.MethodById(claimAssetPreEtrogMethodID)
	require.NoError(t, err)

	claimAssetData, err := method.Inputs.Unpack(claimAssetInput[4:])
	require.NoError(t, err)

	isFound, err := actualClaim.DecodePreEtrogCalldata(claimAssetData)
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
			claim := &claimsynctypes.Claim{
				GlobalIndex:        new(big.Int).SetUint64(uint64(globalIndex)),
				MainnetExitRoot:    common.Hash{},
				RollupExitRoot:     common.Hash{},
				DestinationNetwork: 0,
				Metadata:           nil,
			}

			match, err := claim.DecodePreEtrogCalldata(tt.data)

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
			claim := &claimsynctypes.Claim{GlobalIndex: globalIndex}

			isDecoded, err := claim.DecodeEtrogCalldata(tt.data)
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
	p, err := newTestProcessor(path, "bridge-syncer", logger, dbQueryTimeout)
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
	p, err := newTestProcessor(path, "bridge-syncer", logger, dbQueryTimeout)
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

func TestDatabaseQueryTimeout(t *testing.T) {
	normalTimeout := 100 * time.Millisecond
	shortTimeout := 1 * time.Nanosecond

	path := path.Join(t.TempDir(), "bridgeSyncerProcessorTimeout.db")
	logger := log.WithFields("module", "bridge-syncer-timeout")

	// Create processor with normal timeout for setup
	p, err := newTestProcessor(path, "bridge-syncer-timeout", logger, normalTimeout)
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
	pShortTimeout, err := newTestProcessor(path, "bridge-syncer-short-timeout", logger, shortTimeout)
	require.NoError(t, err)

	// Test that operations timeout with short timeout
	_, _, err = pShortTimeout.GetLastProcessedBlock(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "context deadline exceeded")

	_, err = pShortTimeout.GetBridges(ctx, 1, 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "context deadline exceeded")
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
			p, err := newTestProcessor(dbPath, "bridge-syncer", log.GetDefaultLogger(), dbQueryTimeout)
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

			lastProcessedBlock, _, err := p.GetLastProcessedBlock(t.Context())
			require.NoError(t, err)
			expectedBridges := collectExpectedBridgesUpTo(t, blocks, c.skipBlocks, c.targetDepositCount)

			actualBridges, err := p.GetBridges(t.Context(), 0, lastProcessedBlock)
			require.NoError(t, err)
			require.Equal(t, expectedBridges, actualBridges)
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
		tempP, err := newTestProcessor(tempDBPath, "test-genesis", log.WithFields("module", "test-genesis"), dbQueryTimeout)
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
	p, err := newTestProcessor(dbPath, "test", logger, dbQueryTimeout)
	require.NoError(t, err)
	p.initialLER = bridgesynctypes.EmptyLER

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
	tempP, err := newTestProcessor(tempDBPath, "test-calc", logger, dbQueryTimeout)
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

// TestReorgUnhaltsWhenNoRowsAffected covers the recovery path for a halt caused by a block whose
// tx rolled back and left nothing persisted (same latent wedge as the l1infotreesync
// cardona-67-op incident of 2026-07-23): a recovery Reorg deletes 0 rows — it must still unhalt
// the processor.
func TestReorgUnhaltsWhenNoRowsAffected(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "bridgesyncTestReorgUnhaltsWhenNoRowsAffected.sqlite")
	logger := log.WithFields("module", "bridge-syncer")
	p, err := newTestProcessor(dbPath, "bridge-syncer", logger, dbQueryTimeout)
	require.NoError(t, err)

	p.halt("test: poisoned block never persisted")
	require.True(t, p.isHalted())

	require.NoError(t, p.Reorg(context.Background(), 100))
	require.False(t, p.isHalted(), "Reorg must unhalt even when it deleted 0 rows")
}

// TestReorgUnhaltsWhenRowsAffected guards the previously-working branch: a Reorg that actually
// purges committed rows keeps unhalting the processor.
func TestReorgUnhaltsWhenRowsAffected(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "bridgesyncTestReorgUnhaltsWhenRowsAffected.sqlite")
	logger := log.WithFields("module", "bridge-syncer")
	p, err := newTestProcessor(dbPath, "bridge-syncer", logger, dbQueryTimeout)
	require.NoError(t, err)
	ctx := context.Background()

	require.NoError(t, p.ProcessBlock(ctx, block1))

	p.halt("test: halted after committed block")
	require.True(t, p.isHalted())

	require.NoError(t, p.Reorg(ctx, 1))
	require.False(t, p.isHalted())
}

// TestProcessBlockWorksAfterUnhaltingReorg verifies the full recovery cycle: halt ->
// ProcessBlock short-circuits with ErrInconsistentState -> Reorg (0 rows) unhalts -> a valid
// block is processed and persisted.
func TestProcessBlockWorksAfterUnhaltingReorg(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "bridgesyncTestProcessBlockWorksAfterUnhaltingReorg.sqlite")
	logger := log.WithFields("module", "bridge-syncer")
	p, err := newTestProcessor(dbPath, "bridge-syncer", logger, dbQueryTimeout)
	require.NoError(t, err)
	ctx := context.Background()

	p.halt("test: poisoned block never persisted")
	err = p.ProcessBlock(ctx, block1)
	require.ErrorIs(t, err, sync.ErrInconsistentState)

	require.NoError(t, p.Reorg(ctx, 1))
	require.False(t, p.isHalted())

	require.NoError(t, p.ProcessBlock(ctx, block1))
	lastProcessed, _, err := p.GetLastProcessedBlock(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), lastProcessed)
}
