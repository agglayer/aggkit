package sources

import (
	"errors"
	"math/big"
	"testing"

	"github.com/agglayer/aggkit/bridgetracker"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	trackertypes "github.com/agglayer/aggkit/bridgetracker/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// testGERAddress is the canned GlobalExitRoot contract address findEventUpdateL1InfoTreeBackwards
// filters by in these tests
var testGERAddress = common.HexToAddress("0xge2")

func newSettlementSource(client *mocks.BaseEthereumClienter) *SettlementSource {
	return NewSettlementSource(StaticClients{0: client}, aggkittypes.FinalizedBlock, testGERAddress)
}

// expectBackwardsUpdateL1InfoTreeLog stubs client's FilterLogs to answer
// findEventUpdateL1InfoTreeBackwards' very first chunk (fromBlock down to fromBlock minus
// l1InfoTreeBackwardsSearchChunkSize, or 0) with a single matching log
func expectBackwardsUpdateL1InfoTreeLog(client *mocks.BaseEthereumClienter, fromBlock uint64, log gethtypes.Log) {
	fromChunk := uint64(0)
	if fromBlock > l1InfoTreeBackwardsSearchChunkSize {
		fromChunk = fromBlock - l1InfoTreeBackwardsSearchChunkSize
	}
	client.EXPECT().FilterLogs(mock.Anything, ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(fromChunk),
		ToBlock:   new(big.Int).SetUint64(fromBlock),
		Addresses: []common.Address{testGERAddress},
		Topics:    [][]common.Hash{{updateL1InfoTreeSignature}},
	}).Return([]gethtypes.Log{log}, nil)
}

// mainnetExitRoot/rollupExitRoot are the canned exit roots the tests use to build an
// UpdateL1InfoTree log; both are indexed bytes32 params, so they sit directly in Topics[1]/[2]
var (
	mainnetExitRoot  = common.HexToHash("0x0a")
	rollupExitRoot   = common.HexToHash("0x0b")
	wantGER          = crypto.Keccak256Hash(mainnetExitRoot[:], rollupExitRoot[:])
	updateL1InfoTree = &gethtypes.Log{
		Topics: []common.Hash{updateL1InfoTreeSignature, mainnetExitRoot, rollupExitRoot},
	}
)

// testBackwardsBlockHash/testBackwardsBlockTimestamp stub the block hash/timestamp of the
// earlier UpdateL1InfoTree event findEventUpdateL1InfoTreeBackwards finds, in tests where it
// lands in a different block than the settlement tx itself (testBlockHash/testBlockTimestamp,
// from sources_test.go, stand for the settlement tx's own block)
var (
	testBackwardsBlockHash      = common.HexToHash("0xba0000000000000000000000000000000000000000000000000000000000ba")
	testBackwardsBlockTimestamp = uint64(1690000000)
)

// expectBackwardsBlockTimestamp stubs client's HeaderByHash for testBackwardsBlockHash to
// report testBackwardsBlockTimestamp
func expectBackwardsBlockTimestamp(client *mocks.BaseEthereumClienter) {
	client.EXPECT().HeaderByHash(mock.Anything, testBackwardsBlockHash).
		Return(&gethtypes.Header{Time: testBackwardsBlockTimestamp}, nil)
}

func TestSettlementSourceBothMandatoryEventsPresent(t *testing.T) {
	client := mocks.NewBaseEthereumClienter(t)
	client.EXPECT().TransactionReceipt(mock.Anything, testTxHash).Return(&gethtypes.Receipt{
		BlockNumber: big.NewInt(12345), BlockHash: testBlockHash,
		Logs: []*gethtypes.Log{
			{Topics: []common.Hash{verifyBatchesTrustedAggregatorSignature}},
			updateL1InfoTree,
		},
	}, nil)
	expectFinalized(client, 12345)
	expectBlockTimestamp(client)

	result, err := newSettlementSource(client).SettlementGERUpdate(
		t.Context(), &bridgetracker.BridgeInfo{}, testTxHash)
	require.NoError(t, err)
	require.Equal(t, &trackertypes.L1SettledGERResult{
		TxHash: testTxHash, SettlementBlockNumber: 12345, SettlementBlockTimestamp: testBlockTimestamp,
		GER: wantGER, GERBlockNumber: 12345, GERBlockTimestamp: testBlockTimestamp,
		HasVerifyBatchesTrustedAggregator: true, HasUpdateL1InfoTree: true,
	}, result)
}

// TestSettlementSourceOptionalV2Captured pins that UpdateL1InfoTreeV2 is reported when present,
// alongside the two mandatory events, without being required on its own — and that its
// LeafCount resolves the leaf index directly, one less than the event's LeafCount
func TestSettlementSourceOptionalV2Captured(t *testing.T) {
	leafCount := uint64(8)
	client := mocks.NewBaseEthereumClienter(t)
	client.EXPECT().TransactionReceipt(mock.Anything, testTxHash).Return(&gethtypes.Receipt{
		BlockNumber: big.NewInt(12345), BlockHash: testBlockHash,
		Logs: []*gethtypes.Log{
			{Topics: []common.Hash{verifyBatchesTrustedAggregatorSignature}},
			updateL1InfoTree,
			{Topics: []common.Hash{
				updateL1InfoTreeV2Signature, common.BigToHash(new(big.Int).SetUint64(leafCount)),
			}},
		},
	}, nil)
	expectFinalized(client, 12345)
	expectBlockTimestamp(client)

	result, err := newSettlementSource(client).SettlementGERUpdate(
		t.Context(), &bridgetracker.BridgeInfo{}, testTxHash)
	require.NoError(t, err)
	require.True(t, result.HasUpdateL1InfoTreeV2)
	require.NotNil(t, result.L1InfoTreeIndex)
	require.Equal(t, uint32(leafCount-1), *result.L1InfoTreeIndex)
}

// TestSettlementSourceMalformedUpdateL1InfoTreeV2Log pins that an UpdateL1InfoTreeV2 log
// missing its indexed leafCount topic still counts as "V2 present" (informational), but leaves
// L1InfoTreeIndex nil — WaitL1SettledGERResolver then falls back to the extra GER -> leaf lookup
func TestSettlementSourceMalformedUpdateL1InfoTreeV2Log(t *testing.T) {
	client := mocks.NewBaseEthereumClienter(t)
	client.EXPECT().TransactionReceipt(mock.Anything, testTxHash).Return(&gethtypes.Receipt{
		BlockNumber: big.NewInt(12345), BlockHash: testBlockHash,
		Logs: []*gethtypes.Log{
			{Topics: []common.Hash{verifyBatchesTrustedAggregatorSignature}},
			updateL1InfoTree,
			{Topics: []common.Hash{updateL1InfoTreeV2Signature}},
		},
	}, nil)
	expectFinalized(client, 12345)
	expectBlockTimestamp(client)

	result, err := newSettlementSource(client).SettlementGERUpdate(
		t.Context(), &bridgetracker.BridgeInfo{}, testTxHash)
	require.NoError(t, err)
	require.True(t, result.HasUpdateL1InfoTreeV2)
	require.Nil(t, result.L1InfoTreeIndex)
}

// TestSettlementSourceMalformedUpdateL1InfoTreeLog pins that an UpdateL1InfoTree log missing its
// indexed topics (fewer than 3 total) is not mistaken for a match: without both exit roots there
// is no GER to compute from it, so it is treated the same as the event being altogether absent
// from this receipt — falling back to findEventUpdateL1InfoTreeBackwards for an earlier one
func TestSettlementSourceMalformedUpdateL1InfoTreeLog(t *testing.T) {
	client := mocks.NewBaseEthereumClienter(t)
	client.EXPECT().TransactionReceipt(mock.Anything, testTxHash).Return(&gethtypes.Receipt{
		BlockNumber: big.NewInt(12345), BlockHash: testBlockHash,
		Logs: []*gethtypes.Log{
			{Topics: []common.Hash{verifyBatchesTrustedAggregatorSignature}},
			{Topics: []common.Hash{updateL1InfoTreeSignature}},
		},
	}, nil)
	expectFinalized(client, 12345)
	expectBlockTimestamp(client)
	expectBackwardsUpdateL1InfoTreeLog(client, 12345, gethtypes.Log{
		BlockNumber: 12000, Index: 3, BlockHash: testBackwardsBlockHash,
		Topics: []common.Hash{updateL1InfoTreeSignature, mainnetExitRoot, rollupExitRoot},
	})
	expectBackwardsBlockTimestamp(client)

	result, err := newSettlementSource(client).SettlementGERUpdate(
		t.Context(), &bridgetracker.BridgeInfo{}, testTxHash)
	require.NoError(t, err)
	require.Equal(t, &trackertypes.L1SettledGERResult{
		TxHash: testTxHash, SettlementBlockNumber: 12345, SettlementBlockTimestamp: testBlockTimestamp,
		GER: wantGER, GERBlockNumber: 12000, GERBlockTimestamp: testBackwardsBlockTimestamp, GERLogIndex: 3,
		HasVerifyBatchesTrustedAggregator: true,
	}, result)
}

// TestSettlementSourceMissingVerifyBatchesTrustedAggregator pins that a finalized settlement tx
// receipt missing VerifyBatchesTrustedAggregator is a permanent failure (domain.ErrBadSettlementTx):
// unlike a not-yet-finalized receipt, this one is already final and simply does not carry an
// event a genuine settlement always emits, so retrying it can never change the outcome
func TestSettlementSourceMissingVerifyBatchesTrustedAggregator(t *testing.T) {
	testCases := []struct {
		name string
		logs []*gethtypes.Log
	}{
		{name: "no logs at all"},
		{name: "only UpdateL1InfoTree", logs: []*gethtypes.Log{updateL1InfoTree}},
		{
			name: "only the optional UpdateL1InfoTreeV2",
			logs: []*gethtypes.Log{{Topics: []common.Hash{updateL1InfoTreeV2Signature}}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			client := mocks.NewBaseEthereumClienter(t)
			client.EXPECT().TransactionReceipt(mock.Anything, testTxHash).Return(&gethtypes.Receipt{
				BlockNumber: big.NewInt(12345), BlockHash: testBlockHash, Logs: tc.logs,
			}, nil)
			expectFinalized(client, 12345)
			expectBlockTimestamp(client)

			result, err := newSettlementSource(client).SettlementGERUpdate(
				t.Context(), &bridgetracker.BridgeInfo{}, testTxHash)
			require.ErrorIs(t, err, domain.ErrBadSettlementTx)
			require.Nil(t, result)
		})
	}
}

// TestSettlementSourceMissingUpdateL1InfoTreeFallsBackToEarlierEvent pins that a settlement tx
// whose own receipt carries VerifyBatchesTrustedAggregator but not UpdateL1InfoTree is not
// rejected outright: the settlement simply did not move the GER itself, so
// findEventUpdateL1InfoTreeBackwards supplies the GER an earlier settlement already established
func TestSettlementSourceMissingUpdateL1InfoTreeFallsBackToEarlierEvent(t *testing.T) {
	client := mocks.NewBaseEthereumClienter(t)
	client.EXPECT().TransactionReceipt(mock.Anything, testTxHash).Return(&gethtypes.Receipt{
		BlockNumber: big.NewInt(12345), BlockHash: testBlockHash,
		Logs: []*gethtypes.Log{
			{Topics: []common.Hash{verifyBatchesTrustedAggregatorSignature}},
		},
	}, nil)
	expectFinalized(client, 12345)
	expectBlockTimestamp(client)
	expectBackwardsUpdateL1InfoTreeLog(client, 12345, gethtypes.Log{
		BlockNumber: 12000, Index: 3, BlockHash: testBackwardsBlockHash,
		Topics: []common.Hash{updateL1InfoTreeSignature, mainnetExitRoot, rollupExitRoot},
	})
	expectBackwardsBlockTimestamp(client)

	result, err := newSettlementSource(client).SettlementGERUpdate(
		t.Context(), &bridgetracker.BridgeInfo{}, testTxHash)
	require.NoError(t, err)
	require.Equal(t, &trackertypes.L1SettledGERResult{
		TxHash: testTxHash, SettlementBlockNumber: 12345, SettlementBlockTimestamp: testBlockTimestamp,
		GER: wantGER, GERBlockNumber: 12000, GERBlockTimestamp: testBackwardsBlockTimestamp, GERLogIndex: 3,
		HasVerifyBatchesTrustedAggregator: true,
	}, result)
}

// TestSettlementSourceMissingUpdateL1InfoTreeAndNoEarlierEvent pins that when
// findEventUpdateL1InfoTreeBackwards finds nothing either, SettlementGERUpdate surfaces its
// domain.ErrBadSettlementTx rather than swallowing it
func TestSettlementSourceMissingUpdateL1InfoTreeAndNoEarlierEvent(t *testing.T) {
	client := mocks.NewBaseEthereumClienter(t)
	client.EXPECT().TransactionReceipt(mock.Anything, testTxHash).Return(&gethtypes.Receipt{
		BlockNumber: big.NewInt(5000), BlockHash: testBlockHash,
		Logs: []*gethtypes.Log{
			{Topics: []common.Hash{verifyBatchesTrustedAggregatorSignature}},
		},
	}, nil)
	expectFinalized(client, 5000)
	expectBlockTimestamp(client)
	client.EXPECT().FilterLogs(mock.Anything, ethereum.FilterQuery{
		FromBlock: big.NewInt(0), ToBlock: big.NewInt(5000),
		Addresses: []common.Address{testGERAddress},
		Topics:    [][]common.Hash{{updateL1InfoTreeSignature}},
	}).Return(nil, nil)

	result, err := newSettlementSource(client).SettlementGERUpdate(
		t.Context(), &bridgetracker.BridgeInfo{}, testTxHash)
	require.ErrorIs(t, err, domain.ErrBadSettlementTx)
	require.Nil(t, result)
}

// TestFindEventUpdateL1InfoTreeBackwardsPaginates pins that an empty first chunk does not stop
// the search: it keeps walking further back in l1InfoTreeBackwardsSearchChunkSize chunks until
// one of them carries a match
func TestFindEventUpdateL1InfoTreeBackwardsPaginates(t *testing.T) {
	client := mocks.NewBaseEthereumClienter(t)
	client.EXPECT().FilterLogs(mock.Anything, ethereum.FilterQuery{
		FromBlock: big.NewInt(15000), ToBlock: big.NewInt(25000),
		Addresses: []common.Address{testGERAddress},
		Topics:    [][]common.Hash{{updateL1InfoTreeSignature}},
	}).Return(nil, nil)
	client.EXPECT().FilterLogs(mock.Anything, ethereum.FilterQuery{
		FromBlock: big.NewInt(4999), ToBlock: big.NewInt(14999),
		Addresses: []common.Address{testGERAddress},
		Topics:    [][]common.Hash{{updateL1InfoTreeSignature}},
	}).Return([]gethtypes.Log{
		{
			BlockNumber: 10000, Index: 2, BlockHash: testBackwardsBlockHash,
			Topics: []common.Hash{updateL1InfoTreeSignature, mainnetExitRoot, rollupExitRoot},
		},
	}, nil)
	expectBackwardsBlockTimestamp(client)

	event, err := newSettlementSource(client).findEventUpdateL1InfoTreeBackwards(t.Context(), client, 25000)
	require.NoError(t, err)
	require.Equal(t, &updateL1InfoTreeEvent{
		GER: wantGER, BlockNumber: 10000, BlockTimestamp: testBackwardsBlockTimestamp, LogIndex: 2,
	}, event)
}

// TestFindEventUpdateL1InfoTreeBackwardsNotFound pins that reaching block 0 without a match is
// domain.ErrBadSettlementTx: the L1 Global Exit Root is never unset, so its absence anywhere
// before fromBlock means the settlement tx is not what it claims to be
func TestFindEventUpdateL1InfoTreeBackwardsNotFound(t *testing.T) {
	client := mocks.NewBaseEthereumClienter(t)
	client.EXPECT().FilterLogs(mock.Anything, ethereum.FilterQuery{
		FromBlock: big.NewInt(0), ToBlock: big.NewInt(5000),
		Addresses: []common.Address{testGERAddress},
		Topics:    [][]common.Hash{{updateL1InfoTreeSignature}},
	}).Return(nil, nil)

	_, err := newSettlementSource(client).findEventUpdateL1InfoTreeBackwards(t.Context(), client, 5000)
	require.ErrorIs(t, err, domain.ErrBadSettlementTx)
}

// TestFindEventUpdateL1InfoTreeBackwardsMalformedLog pins that a matched log missing its
// indexed exit root topics is a domain.ErrBadSettlementTx too, rather than being silently
// skipped: a genuine UpdateL1InfoTree event always carries both, so this signals corrupt data,
// not something an earlier chunk would fix
func TestFindEventUpdateL1InfoTreeBackwardsMalformedLog(t *testing.T) {
	client := mocks.NewBaseEthereumClienter(t)
	client.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return([]gethtypes.Log{
		{BlockNumber: 4000, Topics: []common.Hash{updateL1InfoTreeSignature}},
	}, nil)

	_, err := newSettlementSource(client).findEventUpdateL1InfoTreeBackwards(t.Context(), client, 5000)
	require.ErrorIs(t, err, domain.ErrBadSettlementTx)
}

// TestFindEventUpdateL1InfoTreeBackwardsFetchError pins that a FilterLogs failure propagates
// as-is (transient, retried by the engine), not wrapped as domain.ErrBadSettlementTx
func TestFindEventUpdateL1InfoTreeBackwardsFetchError(t *testing.T) {
	client := mocks.NewBaseEthereumClienter(t)
	client.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return(nil, errors.New("rpc down"))

	_, err := newSettlementSource(client).findEventUpdateL1InfoTreeBackwards(t.Context(), client, 5000)
	require.ErrorContains(t, err, "rpc down")
}

func TestSettlementSourceTxNotFound(t *testing.T) {
	client := mocks.NewBaseEthereumClienter(t)
	client.EXPECT().TransactionReceipt(mock.Anything, testTxHash).Return(nil, ethereum.NotFound)

	result, err := newSettlementSource(client).SettlementGERUpdate(
		t.Context(), &bridgetracker.BridgeInfo{}, testTxHash)
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestSettlementSourceNotYetFinalized(t *testing.T) {
	client := mocks.NewBaseEthereumClienter(t)
	client.EXPECT().TransactionReceipt(mock.Anything, testTxHash).Return(&gethtypes.Receipt{
		BlockNumber: big.NewInt(12346),
		Logs: []*gethtypes.Log{
			{Topics: []common.Hash{verifyBatchesTrustedAggregatorSignature}},
			{Topics: []common.Hash{updateL1InfoTreeSignature}},
		},
	}, nil)
	expectFinalized(client, 12345)

	result, err := newSettlementSource(client).SettlementGERUpdate(
		t.Context(), &bridgetracker.BridgeInfo{}, testTxHash)
	require.NoError(t, err)
	require.Nil(t, result, "mined, but not final yet")
}

func TestSettlementSourceReceiptFetchError(t *testing.T) {
	client := mocks.NewBaseEthereumClienter(t)
	client.EXPECT().TransactionReceipt(mock.Anything, testTxHash).Return(nil, errors.New("rpc down"))

	_, err := newSettlementSource(client).SettlementGERUpdate(
		t.Context(), &bridgetracker.BridgeInfo{}, testTxHash)
	require.ErrorContains(t, err, "rpc down")
}

func TestSettlementSourceUnknownNetwork(t *testing.T) {
	source := NewSettlementSource(StaticClients{}, aggkittypes.FinalizedBlock, testGERAddress)

	_, err := source.SettlementGERUpdate(t.Context(), &bridgetracker.BridgeInfo{}, testTxHash)
	require.ErrorContains(t, err, "network 0")
}
