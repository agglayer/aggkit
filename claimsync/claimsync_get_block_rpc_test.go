package claimsync

import (
	"errors"
	"math/big"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/polygonzkevmbridge"
	logger "github.com/agglayer/aggkit/log"
	tree "github.com/agglayer/aggkit/tree/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/agglayer/aggkit/types/mocks"
	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// newTestClaimSyncForRPC builds the minimal ClaimSync needed by GetLatestBlockNumByGlobalIndexFromRPC.
func newTestClaimSyncForRPC(t *testing.T, ethClient aggkittypes.EthClienter) *ClaimSync {
	t.Helper()
	bridgeAddr := common.HexToAddress("0xBridge")
	return &ClaimSync{
		ethClient: ethClient,
		cfg: ConfigStandalone{
			ConfigEmbedded: ConfigEmbedded{BridgeAddr: bridgeAddr},
			BlockFinality:  *aggkittypes.NewBlockNumber(1000),
		},
		logger: logger.WithFields("module", "test"),
	}
}

// buildPreEtrogClaimEventLog packs a valid pre-Etrog ClaimEvent log.
func buildPreEtrogClaimEventLog(t *testing.T, index uint32, blockNum uint64) types.Log {
	t.Helper()
	legacyABI, err := polygonzkevmbridge.PolygonzkevmbridgeMetaData.GetAbi()
	require.NoError(t, err)
	event, err := legacyABI.EventByID(claimEventSignaturePreEtrog)
	require.NoError(t, err)
	data, err := event.Inputs.Pack(index, uint32(1), common.Address{}, common.Address{}, big.NewInt(10))
	require.NoError(t, err)
	return types.Log{
		Topics:      []common.Hash{claimEventSignaturePreEtrog},
		Data:        data,
		BlockNumber: blockNum,
	}
}

// buildDetailedClaimEventLog packs a valid DetailedClaimEvent log with globalIndex as an indexed topic.
func buildDetailedClaimEventLog(t *testing.T, globalIndex *big.Int, blockNum uint64) types.Log {
	t.Helper()
	l2ABI, err := agglayerbridgel2.Agglayerbridgel2MetaData.GetAbi()
	require.NoError(t, err)
	event, err := l2ABI.EventByID(detailedClaimEventSignature)
	require.NoError(t, err)

	var nonIndexed abi.Arguments
	for _, inp := range event.Inputs {
		if !inp.Indexed {
			nonIndexed = append(nonIndexed, inp)
		}
	}
	data, err := nonIndexed.Pack(
		[tree.DefaultHeight][common.HashLength]byte{},
		[tree.DefaultHeight][common.HashLength]byte{},
		[common.HashLength]byte{},
		[common.HashLength]byte{},
		uint8(0),
		uint32(1),
		common.Address{},
		uint32(0),
		big.NewInt(100),
		[]byte{},
	)
	require.NoError(t, err)
	return types.Log{
		Topics: []common.Hash{
			detailedClaimEventSignature,
			common.BigToHash(globalIndex), // globalIndex is an indexed topic
			common.BytesToHash(common.Address{}.Bytes()),
		},
		Data:        data,
		BlockNumber: blockNum,
	}
}

// expectedFilterQuery builds the FilterQuery that GetLatestBlockNumByGlobalIndexFromRPC uses.
func expectedFilterQuery(bridgeAddr common.Address, from, to uint64) ethereum.FilterQuery {
	return ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(from),
		ToBlock:   new(big.Int).SetUint64(to),
		Addresses: []common.Address{bridgeAddr},
		Topics: [][]common.Hash{{
			claimEventSignaturePreEtrog,
			claimEventSignature,
			detailedClaimEventSignature,
		}},
	}
}

// --- Tests ---

func TestGetLatestBlockNumByGlobalIndexFromRPC_FilterLogsError(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ethClient := mocks.NewEthClienter(t)
	c := newTestClaimSyncForRPC(t, ethClient)

	ethClient.EXPECT().FilterLogs(ctx, mock.Anything).Return(nil, errors.New("rpc unavailable"))

	_, found, err := c.GetLatestBlockNumByGlobalIndexFromRPC(ctx, big.NewInt(1), nil)
	require.False(t, found)
	require.ErrorContains(t, err, "rpc unavailable")
}

func TestGetLatestBlockNumByGlobalIndexFromRPC_NoMatchingLog(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ethClient := mocks.NewEthClienter(t)
	c := newTestClaimSyncForRPC(t, ethClient)

	// Return a log for a different globalIndex
	otherLog := buildClaimEventLog(t, big.NewInt(99), common.HexToHash("0x1"), 50)
	ethClient.EXPECT().FilterLogs(ctx, mock.Anything).Return([]types.Log{otherLog}, nil)

	blockNum, found, err := c.GetLatestBlockNumByGlobalIndexFromRPC(ctx, big.NewInt(1), nil)
	require.NoError(t, err)
	require.False(t, found)
	require.Equal(t, uint64(0), blockNum)
}

func TestGetLatestBlockNumByGlobalIndexFromRPC_MatchesClaimEventEtrog(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ethClient := mocks.NewEthClienter(t)
	c := newTestClaimSyncForRPC(t, ethClient)

	globalIndex := big.NewInt(42)
	log := buildClaimEventLog(t, globalIndex, common.HexToHash("0xabc"), 100)
	ethClient.EXPECT().FilterLogs(ctx, mock.Anything).Return([]types.Log{log}, nil)

	blockNum, found, err := c.GetLatestBlockNumByGlobalIndexFromRPC(ctx, globalIndex, nil)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(100), blockNum)
}

func TestGetLatestBlockNumByGlobalIndexFromRPC_MatchesDetailedClaimEvent(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ethClient := mocks.NewEthClienter(t)
	c := newTestClaimSyncForRPC(t, ethClient)

	globalIndex := big.NewInt(7)
	log := buildDetailedClaimEventLog(t, globalIndex, 200)
	ethClient.EXPECT().FilterLogs(ctx, mock.Anything).Return([]types.Log{log}, nil)

	blockNum, found, err := c.GetLatestBlockNumByGlobalIndexFromRPC(ctx, globalIndex, nil)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(200), blockNum)
}

func TestGetLatestBlockNumByGlobalIndexFromRPC_MatchesPreEtrogClaimEvent(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ethClient := mocks.NewEthClienter(t)
	c := newTestClaimSyncForRPC(t, ethClient)

	index := uint32(5)
	log := buildPreEtrogClaimEventLog(t, index, 300)
	ethClient.EXPECT().FilterLogs(ctx, mock.Anything).Return([]types.Log{log}, nil)

	blockNum, found, err := c.GetLatestBlockNumByGlobalIndexFromRPC(ctx, big.NewInt(int64(index)), nil)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(300), blockNum)
}

func TestGetLatestBlockNumByGlobalIndexFromRPC_ReturnsLatestOfMultipleLogs(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ethClient := mocks.NewEthClienter(t)
	c := newTestClaimSyncForRPC(t, ethClient)

	globalIndex := big.NewInt(10)
	// Two matching logs at different blocks; FilterLogs returns them in ascending order.
	// The function iterates in reverse so it should return the last (block 500).
	log1 := buildClaimEventLog(t, globalIndex, common.HexToHash("0x1"), 100)
	log2 := buildClaimEventLog(t, globalIndex, common.HexToHash("0x2"), 500)
	ethClient.EXPECT().FilterLogs(ctx, mock.Anything).Return([]types.Log{log1, log2}, nil)

	blockNum, found, err := c.GetLatestBlockNumByGlobalIndexFromRPC(ctx, globalIndex, nil)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(500), blockNum)
}

func TestGetLatestBlockNumByGlobalIndexFromRPC_ChunkedScan_Found(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ethClient := mocks.NewEthClienter(t)
	c := newTestClaimSyncForRPC(t, ethClient)
	bridgeAddr := c.cfg.BridgeAddr

	globalIndex := big.NewInt(3)
	matchingLog := buildClaimEventLog(t, globalIndex, common.HexToHash("0xdef"), 800)

	// First call: full range [0, 1000] fails with a max-range error → chunkSize = 500
	// Chunked scan goes backwards: [501, 1000] then [1, 500] (if needed)
	// The log is at block 800, so it's found in the first chunk [501, 1000].
	maxRangeErr := errors.New("block range too large, max range: 500")
	ethClient.EXPECT().FilterLogs(ctx, expectedFilterQuery(bridgeAddr, 0, 1000)).
		Return(nil, maxRangeErr).Once()
	ethClient.EXPECT().FilterLogs(ctx, expectedFilterQuery(bridgeAddr, 501, 1000)).
		Return([]types.Log{matchingLog}, nil).Once()

	blockNum, found, err := c.GetLatestBlockNumByGlobalIndexFromRPC(ctx, globalIndex, nil)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(800), blockNum)
}

func TestGetLatestBlockNumByGlobalIndexFromRPC_ChunkedScan_NotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ethClient := mocks.NewEthClienter(t)
	c := newTestClaimSyncForRPC(t, ethClient)
	bridgeAddr := c.cfg.BridgeAddr

	// Full range [0, 1000] fails with max-range error → chunkSize = 500
	// Two chunks cover the full range; neither has a match.
	maxRangeErr := errors.New("block range too large, max range: 500")
	ethClient.EXPECT().FilterLogs(ctx, expectedFilterQuery(bridgeAddr, 0, 1000)).
		Return(nil, maxRangeErr).Once()
	ethClient.EXPECT().FilterLogs(ctx, expectedFilterQuery(bridgeAddr, 501, 1000)).
		Return([]types.Log{}, nil).Once()
	ethClient.EXPECT().FilterLogs(ctx, expectedFilterQuery(bridgeAddr, 1, 500)).
		Return([]types.Log{}, nil).Once()
	ethClient.EXPECT().FilterLogs(ctx, expectedFilterQuery(bridgeAddr, 0, 0)).
		Return([]types.Log{}, nil).Once()

	blockNum, found, err := c.GetLatestBlockNumByGlobalIndexFromRPC(ctx, big.NewInt(99), nil)
	require.NoError(t, err)
	require.False(t, found)
	require.Equal(t, uint64(0), blockNum)
}

func TestGetLatestBlockNumByGlobalIndexFromRPC_ChunkedScan_ChunkError(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ethClient := mocks.NewEthClienter(t)
	c := newTestClaimSyncForRPC(t, ethClient)
	bridgeAddr := c.cfg.BridgeAddr

	maxRangeErr := errors.New("block range too large, max range: 500")
	chunkErr := errors.New("network timeout")
	ethClient.EXPECT().FilterLogs(ctx, expectedFilterQuery(bridgeAddr, 0, 1000)).
		Return(nil, maxRangeErr).Once()
	ethClient.EXPECT().FilterLogs(ctx, expectedFilterQuery(bridgeAddr, 501, 1000)).
		Return(nil, chunkErr).Once()

	_, found, err := c.GetLatestBlockNumByGlobalIndexFromRPC(ctx, big.NewInt(1), nil)
	require.False(t, found)
	require.ErrorContains(t, err, "network timeout")
}

func TestGetLatestBlockNumByGlobalIndexFromRPC_ExplicitToBlock(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ethClient := mocks.NewEthClienter(t)
	c := newTestClaimSyncForRPC(t, ethClient)

	globalIndex := big.NewInt(1)
	toBlock := aggkittypes.NewBlockNumber(200)
	log := buildClaimEventLog(t, globalIndex, common.HexToHash("0x9"), 150)
	ethClient.EXPECT().FilterLogs(ctx, mock.Anything).Return([]types.Log{log}, nil)

	blockNum, found, err := c.GetLatestBlockNumByGlobalIndexFromRPC(ctx, globalIndex, toBlock)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(150), blockNum)
}

func TestGetLatestBlockNumByGlobalIndexFromRPC_LogWithNoTopics_Skipped(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ethClient := mocks.NewEthClienter(t)
	c := newTestClaimSyncForRPC(t, ethClient)

	globalIndex := big.NewInt(1)
	noTopicLog := types.Log{BlockNumber: 50} // no topics
	matchingLog := buildClaimEventLog(t, globalIndex, common.HexToHash("0x1"), 100)
	ethClient.EXPECT().FilterLogs(ctx, mock.Anything).Return([]types.Log{noTopicLog, matchingLog}, nil)

	blockNum, found, err := c.GetLatestBlockNumByGlobalIndexFromRPC(ctx, globalIndex, nil)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(100), blockNum)
}
