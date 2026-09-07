package bridgelooptester_test

import (
	"context"
	"math/big"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	"github.com/agglayer/aggkit/bridgesync"
	bridgelooptester "github.com/agglayer/aggkit/tools/bridge_loop_tester"
	"github.com/agglayer/aggkit/tools/bridge_loop_tester/mocks"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// abiSelectorLen is the length in bytes of an ABI function/error selector.
const abiSelectorLen = 4

var (
	testBridgeAddr = common.HexToAddress("0x1111111111111111111111111111111111111111")
	testTokenAddr  = common.HexToAddress("0x4444444444444444444444444444444444444444")
	testDestAddr   = common.HexToAddress("0x5555555555555555555555555555555555555555")
)

// bridgeABI returns the agglayerbridgel2 ABI, used by the tests both to build synthetic logs and to
// unpack the calldata the Bridge wrapper produced.
func bridgeABI(t *testing.T) *abi.ABI {
	t.Helper()

	parsed, err := agglayerbridgel2.Agglayerbridgel2MetaData.GetAbi()
	require.NoError(t, err)

	return parsed
}

// newMockedBridge builds a Bridge over a mocked NetworkClient, and returns both so a test can set
// expectations on the client and on its backend.
func newMockedBridge(t *testing.T) (bridgelooptester.Bridge, *mocks.NetworkClient, *mocks.EthBackend) {
	t.Helper()

	backend := mocks.NewEthBackend(t)
	client := mocks.NewNetworkClient(t)
	client.EXPECT().Backend().Return(backend).Maybe()
	client.EXPECT().Name().Return("L2A").Maybe()

	bridge, err := bridgelooptester.NewBridge(client, testBridgeAddr)
	require.NoError(t, err)

	return bridge, client, backend
}

// bridgeEventLog builds a synthetic BridgeEvent log. Every BridgeEvent argument is non-indexed, so
// the log carries only the event id as a topic and everything else in its data.
func bridgeEventLog(t *testing.T, emitter common.Address, depositCount uint32) *ethtypes.Log {
	t.Helper()

	parsed := bridgeABI(t)
	event, ok := parsed.Events["BridgeEvent"]
	require.True(t, ok)

	data, err := event.Inputs.Pack(
		uint8(0),
		uint32(0),
		common.Address{},
		uint32(2),
		testDestAddr,
		big.NewInt(1_000_000),
		[]byte{0xaa, 0xbb},
		depositCount,
	)
	require.NoError(t, err)

	return &ethtypes.Log{
		Address:     emitter,
		Topics:      []common.Hash{event.ID},
		Data:        data,
		TxHash:      common.HexToHash("0xfeed"),
		BlockNumber: 99,
	}
}

// TestBridgeEventFromReceipt covers DESIGN.md §5: the deposit count comes from the BridgeEvent log
// in the bridge tx's own receipt, and an ERC20 deposit's extra Transfer log must not confuse the
// scan.
func TestBridgeEventFromReceipt(t *testing.T) {
	t.Parallel()

	bridge, _, _ := newMockedBridge(t)

	transferLog := &ethtypes.Log{
		Address: testTokenAddr,
		Topics:  []common.Hash{common.HexToHash("0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef")},
		Data:    common.BigToHash(big.NewInt(1_000_000)).Bytes(),
	}
	receipt := &ethtypes.Receipt{
		TxHash: common.HexToHash("0xfeed"),
		Logs:   []*ethtypes.Log{nil, transferLog, bridgeEventLog(t, testBridgeAddr, 17)},
	}

	event, err := bridge.BridgeEventFromReceipt(receipt)
	require.NoError(t, err)
	require.Equal(t, uint32(17), event.DepositCount)
	require.Equal(t, uint8(0), event.LeafType)
	require.Equal(t, uint32(0), event.OriginNetwork)
	require.Equal(t, common.Address{}, event.OriginAddress)
	require.Equal(t, uint32(2), event.DestinationNetwork)
	require.Equal(t, testDestAddr, event.DestinationAddress)
	require.Equal(t, big.NewInt(1_000_000), event.Amount)
	require.Equal(t, []byte{0xaa, 0xbb}, event.Metadata)
	require.Equal(t, common.HexToHash("0xfeed"), event.TxHash)
	require.Equal(t, uint64(99), event.BlockNumber)
}

// TestBridgeEventFromReceiptOtherEmitter checks the fallback scan: a BridgeEvent emitted by an
// address other than the configured bridge is still decoded, matching the proven pattern in
// test/e2e/bridge_utils.go, which scans every log without filtering by address.
func TestBridgeEventFromReceiptOtherEmitter(t *testing.T) {
	t.Parallel()

	bridge, _, _ := newMockedBridge(t)

	receipt := &ethtypes.Receipt{
		TxHash: common.HexToHash("0xfeed"),
		Logs:   []*ethtypes.Log{bridgeEventLog(t, testTokenAddr, 3)},
	}

	event, err := bridge.BridgeEventFromReceipt(receipt)
	require.NoError(t, err)
	require.Equal(t, uint32(3), event.DepositCount)
}

func TestBridgeEventFromReceiptNotFound(t *testing.T) {
	t.Parallel()

	bridge, _, _ := newMockedBridge(t)

	_, err := bridge.BridgeEventFromReceipt(nil)
	require.ErrorIs(t, err, bridgelooptester.ErrBridgeEventNotFound)

	_, err = bridge.BridgeEventFromReceipt(&ethtypes.Receipt{
		TxHash: common.HexToHash("0xdead"),
		Logs: []*ethtypes.Log{{
			Address: testBridgeAddr,
			Topics:  []common.Hash{common.HexToHash("0x1234")},
		}},
	})
	require.ErrorIs(t, err, bridgelooptester.ErrBridgeEventNotFound)
	require.ErrorContains(t, err, "1 logs scanned")
}

// TestNewBridgeValidation covers the constructor guards.
func TestNewBridgeValidation(t *testing.T) {
	t.Parallel()

	_, err := bridgelooptester.NewBridge(nil, testBridgeAddr)
	require.ErrorContains(t, err, "new bridge: network client is required")

	client := mocks.NewNetworkClient(t)
	client.EXPECT().Name().Return("L2A").Maybe()
	_, err = bridgelooptester.NewBridge(client, common.Address{})
	require.ErrorContains(t, err, "bridge address must not be the zero address")
}

// expectViewCall wires the mocked backend to answer one bridge view call, keyed by method selector,
// with the ABI-encoded outputs.
func expectViewCall(t *testing.T, backend *mocks.EthBackend, method string, outputs ...any) {
	t.Helper()

	parsed := bridgeABI(t)
	abiMethod, ok := parsed.Methods[method]
	require.True(t, ok)

	encoded, err := abiMethod.Outputs.Pack(outputs...)
	require.NoError(t, err)

	backend.EXPECT().
		CallContract(mock.Anything, mock.MatchedBy(func(call ethereum.CallMsg) bool {
			return len(call.Data) >= abiSelectorLen && string(call.Data[:abiSelectorLen]) == string(abiMethod.ID)
		}), mock.Anything).
		Return(encoded, nil)
}

// TestBridgeViewCalls covers the read-only surface, including that GasTokenAddress is a real
// runtime call (per DESIGN.md gap G3) whose result is cached after the first read.
func TestBridgeViewCalls(t *testing.T) {
	t.Parallel()

	bridge, _, backend := newMockedBridge(t)

	expectViewCall(t, backend, "networkID", uint32(1))
	expectViewCall(t, backend, "WETHToken", testTokenAddr)
	expectViewCall(t, backend, "computeTokenProxyAddress", testDestAddr)
	expectViewCall(t, backend, "isClaimed", true)
	// .Once() proves the immutable gas token is read from the chain exactly once and then cached.
	parsed := bridgeABI(t)
	gasTokenOutputs, err := parsed.Methods["gasTokenAddress"].Outputs.Pack(common.Address{})
	require.NoError(t, err)
	backend.EXPECT().
		CallContract(mock.Anything, mock.MatchedBy(func(call ethereum.CallMsg) bool {
			return len(call.Data) >= abiSelectorLen &&
				string(call.Data[:abiSelectorLen]) == string(parsed.Methods["gasTokenAddress"].ID)
		}), mock.Anything).
		Return(gasTokenOutputs, nil).Once()

	ctx := context.Background()

	require.Equal(t, testBridgeAddr, bridge.Address())

	networkID, err := bridge.NetworkID(ctx)
	require.NoError(t, err)
	require.Equal(t, uint32(1), networkID)

	weth, err := bridge.WETHToken(ctx)
	require.NoError(t, err)
	require.Equal(t, testTokenAddr, weth)

	wrapped, err := bridge.GetTokenWrappedAddress(ctx, 1, testTokenAddr)
	require.NoError(t, err)
	require.Equal(t, testDestAddr, wrapped)

	claimed, err := bridge.IsClaimed(ctx, 17, 1)
	require.NoError(t, err)
	require.True(t, claimed)

	for range 3 {
		gasToken, err := bridge.GasTokenAddress(ctx)
		require.NoError(t, err)
		require.Equal(t, common.Address{}, gasToken)
	}
}

// TestBridgeAssetNativeSubmitsWithMsgValue pins the native path of DESIGN.md §6: token=0x0 plus
// msg.value, submitted through the serialized sender.
func TestBridgeAssetNativeSubmitsWithMsgValue(t *testing.T) {
	t.Parallel()

	bridge, client, backend := newMockedBridge(t)
	expectViewCall(t, backend, "gasTokenAddress", common.Address{})

	amount := big.NewInt(1_000_000)
	receipt := &ethtypes.Receipt{
		Status: ethtypes.ReceiptStatusSuccessful,
		TxHash: common.HexToHash("0xfeed"),
		Logs:   []*ethtypes.Log{bridgeEventLog(t, testBridgeAddr, 42)},
	}

	var sent bridgelooptester.TxRequest
	client.EXPECT().
		SendTx(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, req bridgelooptester.TxRequest) (*ethtypes.Receipt, error) {
			sent = req

			return receipt, nil
		}).Once()

	result, err := bridge.BridgeAssetNative(context.Background(), bridgelooptester.BridgeAssetRequest{
		DestinationNetwork:        2,
		DestinationAddress:        testDestAddr,
		Amount:                    amount,
		ForceUpdateGlobalExitRoot: true,
	})
	require.NoError(t, err)
	require.Equal(t, common.HexToHash("0xfeed"), result.TxHash)
	require.Equal(t, uint32(42), result.Event.DepositCount)

	require.Equal(t, "bridgeAsset(native)", sent.Label)
	require.Equal(t, &testBridgeAddr, sent.To)
	require.Equal(t, amount, sent.Value, "the native path must attach msg.value")

	args := unpackCalldata(t, bridgeABI(t), "bridgeAsset", sent.Data)
	require.Equal(t, uint32(2), args[0])
	require.Equal(t, testDestAddr, args[1])
	require.Equal(t, amount, args[2])
	require.Equal(t, common.Address{}, args[3], "the native path must pass the zero token address")
	require.Equal(t, true, args[4])
}

// TestBridgeAssetNativeRefusesNonEtherGasToken covers the DESIGN.md §6 refusal: an ETH hop on a
// network whose gas token is not ether must fail before anything is submitted.
func TestBridgeAssetNativeRefusesNonEtherGasToken(t *testing.T) {
	t.Parallel()

	// No SendTx expectation is registered: the mocked client panics if the code submits anything.
	bridge, _, backend := newMockedBridge(t)
	expectViewCall(t, backend, "gasTokenAddress", testTokenAddr)
	expectViewCall(t, backend, "WETHToken", testDestAddr)

	_, err := bridge.BridgeAssetNative(context.Background(), bridgelooptester.BridgeAssetRequest{
		DestinationNetwork: 2,
		DestinationAddress: testDestAddr,
		Amount:             big.NewInt(1),
	})
	require.ErrorIs(t, err, bridgelooptester.ErrNativeAssetUnsupported)
	require.ErrorContains(t, err, "L2A reports gasTokenAddress()="+testTokenAddr.String())
	require.ErrorContains(t, err, "WETHToken()="+testDestAddr.String())
}

// TestBridgeAssetERC20 pins the ERC20 path: the token address goes in the calldata, never in
// msg.value, and the zero address is refused.
func TestBridgeAssetERC20(t *testing.T) {
	t.Parallel()

	bridge, client, _ := newMockedBridge(t)

	receipt := &ethtypes.Receipt{
		Status: ethtypes.ReceiptStatusSuccessful,
		TxHash: common.HexToHash("0xfeed"),
		Logs:   []*ethtypes.Log{bridgeEventLog(t, testBridgeAddr, 7)},
	}

	var sent bridgelooptester.TxRequest
	client.EXPECT().
		SendTx(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, req bridgelooptester.TxRequest) (*ethtypes.Receipt, error) {
			sent = req

			return receipt, nil
		}).Once()

	result, err := bridge.BridgeAssetERC20(context.Background(), testTokenAddr, bridgelooptester.BridgeAssetRequest{
		DestinationNetwork: 0,
		DestinationAddress: testDestAddr,
		Amount:             big.NewInt(500),
		GasLimit:           400_000,
	})
	require.NoError(t, err)
	require.Equal(t, uint32(7), result.Event.DepositCount)

	require.Equal(t, "bridgeAsset(erc20)", sent.Label)
	require.Equal(t, uint64(400_000), sent.GasLimit)
	require.Zero(t, sent.Value.Sign(), "the ERC20 path must not attach msg.value")

	args := unpackCalldata(t, bridgeABI(t), "bridgeAsset", sent.Data)
	require.Equal(t, testTokenAddr, args[3])

	_, err = bridge.BridgeAssetERC20(context.Background(), common.Address{}, bridgelooptester.BridgeAssetRequest{})
	require.ErrorContains(t, err, "token must not be the zero address")
}

// TestBridgeAssetMissingBridgeEvent checks a submitted deposit whose receipt has no BridgeEvent is
// reported as such, instead of yielding a zero deposit count.
func TestBridgeAssetMissingBridgeEvent(t *testing.T) {
	t.Parallel()

	bridge, client, _ := newMockedBridge(t)

	client.EXPECT().
		SendTx(mock.Anything, mock.Anything).
		Return(&ethtypes.Receipt{
			Status: ethtypes.ReceiptStatusSuccessful,
			TxHash: common.HexToHash("0xfeed"),
		}, nil).Once()

	_, err := bridge.BridgeAssetERC20(context.Background(), testTokenAddr, bridgelooptester.BridgeAssetRequest{
		DestinationNetwork: 0,
		DestinationAddress: testDestAddr,
		Amount:             big.NewInt(1),
	})
	require.ErrorIs(t, err, bridgelooptester.ErrBridgeEventNotFound)
	require.ErrorContains(t, err, "bridgeAsset(erc20) on L2A: tx 0x")
}

// TestClaimPacksTheSameArgumentsAsAutoclaim pins the claim calldata: the argument list must be the
// one autoclaim/claimtx.PackClaim builds, off the same agglayerbridgel2 ABI.
func TestClaimPacksTheSameArgumentsAsAutoclaim(t *testing.T) {
	t.Parallel()

	for _, method := range []string{"claimAsset", "claimMessage"} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			bridge, client, _ := newMockedBridge(t)

			var sent bridgelooptester.TxRequest
			client.EXPECT().
				SendTx(mock.Anything, mock.Anything).
				RunAndReturn(func(_ context.Context, req bridgelooptester.TxRequest) (*ethtypes.Receipt, error) {
					sent = req

					return &ethtypes.Receipt{Status: ethtypes.ReceiptStatusSuccessful}, nil
				}).Once()

			req := bridgelooptester.ClaimRequest{
				GlobalIndex:        bridgelooptester.GlobalIndex(1, 17),
				MainnetExitRoot:    common.HexToHash("0xaa"),
				RollupExitRoot:     common.HexToHash("0xbb"),
				OriginNetwork:      1,
				OriginAddress:      testTokenAddr,
				DestinationNetwork: 2,
				DestinationAddress: testDestAddr,
				Amount:             big.NewInt(1_000),
				Metadata:           []byte{0x01},
			}
			req.ProofLocalExitRoot[0] = common.HexToHash("0xcc")
			req.ProofRollupExitRoot[31] = common.HexToHash("0xdd")

			claim := bridge.ClaimAsset
			if method == "claimMessage" {
				claim = bridge.ClaimMessage
			}
			_, err := claim(context.Background(), req)
			require.NoError(t, err)

			require.Equal(t, method, sent.Label)
			require.Equal(t, &testBridgeAddr, sent.To)
			require.Nil(t, sent.Value)

			args := unpackCalldata(t, bridgeABI(t), method, sent.Data)
			require.Len(t, args, 11)
			require.Equal(t, req.ProofLocalExitRoot, args[0])
			require.Equal(t, req.ProofRollupExitRoot, args[1])
			require.Equal(t, req.GlobalIndex, args[2])
			require.Equal(t, [32]byte(req.MainnetExitRoot), args[3])
			require.Equal(t, [32]byte(req.RollupExitRoot), args[4])
			require.Equal(t, req.OriginNetwork, args[5])
			require.Equal(t, req.OriginAddress, args[6])
			require.Equal(t, req.DestinationNetwork, args[7])
			require.Equal(t, req.DestinationAddress, args[8])
			require.Equal(t, req.Amount, args[9])
			require.Equal(t, req.Metadata, args[10])
		})
	}
}

// TestClaimRequiresGlobalIndex checks the guard against submitting a claim with no global index.
func TestClaimRequiresGlobalIndex(t *testing.T) {
	t.Parallel()

	bridge, _, _ := newMockedBridge(t)

	_, err := bridge.ClaimAsset(context.Background(), bridgelooptester.ClaimRequest{})
	require.ErrorContains(t, err, "claimAsset on L2A: GlobalIndex is required")
}

// TestGlobalIndexDelegatesToBridgesync checks the encoding is not re-implemented here (DESIGN.md §7).
func TestGlobalIndexDelegatesToBridgesync(t *testing.T) {
	t.Parallel()

	for _, networkID := range []uint32{0, 1, 2, 7} {
		require.Equal(t,
			bridgesync.GenerateGlobalIndexForNetworkID(networkID, 123),
			bridgelooptester.GlobalIndex(networkID, 123))
	}
}

// unpackCalldata splits a selector-prefixed calldata blob back into its arguments.
func unpackCalldata(t *testing.T, parsed *abi.ABI, method string, data []byte) []any {
	t.Helper()

	abiMethod, ok := parsed.Methods[method]
	require.True(t, ok)
	require.GreaterOrEqual(t, len(data), abiSelectorLen)
	require.Equal(t, abiMethod.ID, data[:abiSelectorLen])

	args, err := abiMethod.Inputs.Unpack(data[abiSelectorLen:])
	require.NoError(t, err)

	return args
}
