package sources

import (
	"context"
	"errors"
	"testing"

	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// activityRPCTestFromBlock/activityRPCTestToBlock are the fixed [from, to] finality pair used
// across these tests, distinguished by Offset so CustomHeaderByNumber's mock expectations
// (expectRange) can tell which bound is being resolved.
var (
	activityRPCTestFromBlock = aggkittypes.BlockNumberFinality{Block: aggkittypes.Latest, Offset: -10}
	activityRPCTestToBlock   = aggkittypes.LatestBlock
)

const activityRPCTestNetworkID = uint32(7)

var activityRPCTestBridgeAddr = common.HexToAddress("0xb41d9e00000000000000000000000000000000")

// activityRPCTestLister resolves activityRPCTestNetworkID to activityRPCTestBridgeAddr; every
// other network errors, mirroring fakeNetworkLister's own "not configured" behaviour.
type activityRPCTestLister struct{}

func (activityRPCTestLister) BridgeAddress(_ context.Context, networkID uint32) (common.Address, error) {
	if networkID != activityRPCTestNetworkID {
		return common.Address{}, errors.New("no bridge contract address configured")
	}
	return activityRPCTestBridgeAddr, nil
}

// expectRange stubs client's CustomHeaderByNumber so activityRPCTestFromBlock resolves to from
// and activityRPCTestToBlock resolves to to.
func expectRange(client *mocks.EthClienter, from, to uint64) {
	client.EXPECT().
		CustomHeaderByNumber(mock.Anything, mock.MatchedBy(func(b *aggkittypes.BlockNumberFinality) bool {
			return b.Offset == activityRPCTestFromBlock.Offset
		})).
		Return(&aggkittypes.BlockHeader{Number: from}, nil)
	client.EXPECT().
		CustomHeaderByNumber(mock.Anything, mock.MatchedBy(func(b *aggkittypes.BlockNumberFinality) bool {
			return b.Offset == activityRPCTestToBlock.Offset
		})).
		Return(&aggkittypes.BlockHeader{Number: to}, nil)
}

// expectTxSender stubs client's eth_getTransactionByHash Call so ExtractTxnAddresses' fast path
// resolves sender straight off the transaction (one sent directly to the bridge contract), with
// no debug_traceTransaction needed.
func expectTxSender(t *testing.T, client *mocks.EthClienter, sender common.Address) {
	t.Helper()
	client.EXPECT().
		Call(mock.Anything, bridgesync.GetTransactionByHashEndpoint, mock.Anything).
		Run(func(result any, _ string, _ ...any) {
			tx, ok := result.(*bridgesync.Transaction)
			require.True(t, ok)
			tx.FromRaw = sender.Hex()
			tx.To = activityRPCTestBridgeAddr.Hex()
		}).
		Return(nil)
}

func newActivityRPCScannerForTest(t *testing.T, client aggkittypes.BaseEthereumClienter) *activityRPCScanner {
	t.Helper()
	scanner, err := newActivityRPCScanner(
		StaticClients{activityRPCTestNetworkID: client}, activityRPCTestLister{},
		activityRPCTestFromBlock, activityRPCTestToBlock, log.WithFields("module", "activity_rpc_test"))
	require.NoError(t, err)
	return scanner
}

// TestActivityRPCScanner_BridgesFrom_EmptyWindow verifies an empty (fromBlock > toBlock) window
// is reported as "nothing found", not an error, and never even calls FilterLogs.
func TestActivityRPCScanner_BridgesFrom_EmptyWindow(t *testing.T) {
	client := mocks.NewEthClienter(t)
	expectRange(client, 200, 100) // from (200) > to (100)
	scanner := newActivityRPCScannerForTest(t, client)

	items, err := scanner.BridgesFrom(t.Context(), activityRPCTestNetworkID, common.HexToAddress(testFromAddress))
	require.NoError(t, err)
	require.Empty(t, items)
}

// TestActivityRPCScanner_BridgesFrom_MatchingBridge verifies a BridgeEvent log sent directly to
// the bridge contract by the requested address is decoded into a domain.ScannedBridge whose
// Bridge is built through bridgeservice.NewBridgeResponse (see BridgesFrom).
func TestActivityRPCScanner_BridgesFrom_MatchingBridge(t *testing.T) {
	client := mocks.NewEthClienter(t)
	expectRange(client, 100, 200)

	rawLog := bridgeEventLog(t, 1, 7)
	rawLog.TxHash = testTxHash
	client.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return([]gethtypes.Log{*rawLog}, nil)
	expectTxSender(t, client, common.HexToAddress(testFromAddress))
	client.EXPECT().HeaderByHash(mock.Anything, testBlockHash).
		Return(&gethtypes.Header{Time: testBlockTimestamp}, nil)

	scanner := newActivityRPCScannerForTest(t, client)
	items, err := scanner.BridgesFrom(t.Context(), activityRPCTestNetworkID, common.HexToAddress(testFromAddress))
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, activityRPCTestNetworkID, items[0].NetworkID)
	require.Equal(t, uint32(1), items[0].Bridge.DestinationNetwork)
	require.Equal(t, uint32(7), items[0].Bridge.DepositCount)
	require.Equal(t, testFromAddress, string(*items[0].Bridge.FromAddress))
}

// TestActivityRPCScanner_BridgesFrom_FiltersOtherSenders verifies a BridgeEvent log sent by a
// different address is decoded but then dropped, never reaching the returned slice.
func TestActivityRPCScanner_BridgesFrom_FiltersOtherSenders(t *testing.T) {
	client := mocks.NewEthClienter(t)
	expectRange(client, 100, 200)

	rawLog := bridgeEventLog(t, 1, 7)
	rawLog.TxHash = testTxHash
	client.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return([]gethtypes.Log{*rawLog}, nil)
	expectTxSender(t, client, common.HexToAddress("0x2222222222222222222222222222222222222222"))

	scanner := newActivityRPCScannerForTest(t, client)
	items, err := scanner.BridgesFrom(t.Context(), activityRPCTestNetworkID, common.HexToAddress(testFromAddress))
	require.NoError(t, err)
	require.Empty(t, items)
}

// TestActivityRPCScanner_BridgesFrom_FilterLogsError verifies an eth_getLogs failure is bubbled
// up as an error, not swallowed.
func TestActivityRPCScanner_BridgesFrom_FilterLogsError(t *testing.T) {
	client := mocks.NewEthClienter(t)
	expectRange(client, 100, 200)
	client.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return(nil, errors.New("rpc unavailable"))

	scanner := newActivityRPCScannerForTest(t, client)
	_, err := scanner.BridgesFrom(t.Context(), activityRPCTestNetworkID, common.HexToAddress(testFromAddress))
	require.ErrorContains(t, err, "rpc unavailable")
}
