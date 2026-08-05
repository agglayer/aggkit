package sources

import (
	"errors"
	"math/big"
	"testing"

	"github.com/agglayer/aggkit/bridgetracker"
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

func newSettlementSource(client *mocks.BaseEthereumClienter) *SettlementSource {
	return NewSettlementSource(StaticClients{0: client}, aggkittypes.FinalizedBlock)
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

func TestSettlementSourceBothMandatoryEventsPresent(t *testing.T) {
	client := mocks.NewBaseEthereumClienter(t)
	client.EXPECT().TransactionReceipt(mock.Anything, testTxHash).Return(&gethtypes.Receipt{
		BlockNumber: big.NewInt(12345),
		Logs: []*gethtypes.Log{
			{Topics: []common.Hash{verifyBatchesTrustedAggregatorSignature}},
			updateL1InfoTree,
		},
	}, nil)
	expectFinalized(client, 12345)

	result, err := newSettlementSource(client).SettlementGERUpdate(
		t.Context(), &bridgetracker.BridgeInfo{}, testTxHash)
	require.NoError(t, err)
	require.Equal(t, &trackertypes.L1SettledGERResult{
		TxHash: testTxHash, BlockNumber: 12345, GER: wantGER,
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
		BlockNumber: big.NewInt(12345),
		Logs: []*gethtypes.Log{
			{Topics: []common.Hash{verifyBatchesTrustedAggregatorSignature}},
			updateL1InfoTree,
			{Topics: []common.Hash{
				updateL1InfoTreeV2Signature, common.BigToHash(new(big.Int).SetUint64(leafCount)),
			}},
		},
	}, nil)
	expectFinalized(client, 12345)

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
		BlockNumber: big.NewInt(12345),
		Logs: []*gethtypes.Log{
			{Topics: []common.Hash{verifyBatchesTrustedAggregatorSignature}},
			updateL1InfoTree,
			{Topics: []common.Hash{updateL1InfoTreeV2Signature}},
		},
	}, nil)
	expectFinalized(client, 12345)

	result, err := newSettlementSource(client).SettlementGERUpdate(
		t.Context(), &bridgetracker.BridgeInfo{}, testTxHash)
	require.NoError(t, err)
	require.True(t, result.HasUpdateL1InfoTreeV2)
	require.Nil(t, result.L1InfoTreeIndex)
}

// TestSettlementSourceMalformedUpdateL1InfoTreeLog pins that an UpdateL1InfoTree log missing its
// indexed topics (fewer than 3 total) is not mistaken for a match: without both exit roots there
// is no GER to compute, so the mandatory event is treated as not there yet
func TestSettlementSourceMalformedUpdateL1InfoTreeLog(t *testing.T) {
	client := mocks.NewBaseEthereumClienter(t)
	client.EXPECT().TransactionReceipt(mock.Anything, testTxHash).Return(&gethtypes.Receipt{
		BlockNumber: big.NewInt(12345),
		Logs: []*gethtypes.Log{
			{Topics: []common.Hash{verifyBatchesTrustedAggregatorSignature}},
			{Topics: []common.Hash{updateL1InfoTreeSignature}},
		},
	}, nil)
	expectFinalized(client, 12345)

	result, err := newSettlementSource(client).SettlementGERUpdate(
		t.Context(), &bridgetracker.BridgeInfo{}, testTxHash)
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestSettlementSourceMissingMandatoryEvent(t *testing.T) {
	testCases := []struct {
		name string
		logs []*gethtypes.Log
	}{
		{name: "no logs at all"},
		{
			name: "only VerifyBatchesTrustedAggregator",
			logs: []*gethtypes.Log{{Topics: []common.Hash{verifyBatchesTrustedAggregatorSignature}}},
		},
		{
			name: "only UpdateL1InfoTree",
			logs: []*gethtypes.Log{updateL1InfoTree},
		},
		{
			name: "only the optional UpdateL1InfoTreeV2",
			logs: []*gethtypes.Log{{Topics: []common.Hash{updateL1InfoTreeV2Signature}}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			client := mocks.NewBaseEthereumClienter(t)
			client.EXPECT().TransactionReceipt(mock.Anything, testTxHash).Return(&gethtypes.Receipt{
				BlockNumber: big.NewInt(12345), Logs: tc.logs,
			}, nil)
			expectFinalized(client, 12345)

			result, err := newSettlementSource(client).SettlementGERUpdate(
				t.Context(), &bridgetracker.BridgeInfo{}, testTxHash)
			require.NoError(t, err)
			require.Nil(t, result, "mandatory evidence missing, still pending")
		})
	}
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
	source := NewSettlementSource(StaticClients{}, aggkittypes.FinalizedBlock)

	_, err := source.SettlementGERUpdate(t.Context(), &bridgetracker.BridgeInfo{}, testTxHash)
	require.ErrorContains(t, err, "network 0")
}
