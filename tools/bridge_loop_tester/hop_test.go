package bridgelooptester_test

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"
	"time"

	bridgeserviceclient "github.com/agglayer/aggkit/bridgeservice/client"
	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	trackerapi "github.com/agglayer/aggkit/bridgetracker/api"
	"github.com/agglayer/aggkit/log"
	bridgelooptester "github.com/agglayer/aggkit/tools/bridge_loop_tester"
	"github.com/agglayer/aggkit/tools/bridge_loop_tester/mocks"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Fixed identities the hop tests drive value between: L2A (network 1) -> L2B (network 2) for the
// L2->L2 case, and L2A -> L1 (network 0) for the case that skips the injected-leaf gate.
const (
	hopSourceNetwork      uint32 = 1
	hopDestinationNetwork uint32 = 2
	hopL1Network          uint32 = 0
	hopDepositCount       uint32 = 42
	hopLeafIndex          uint32 = 7
	hopInjectedLeafIndex  uint32 = 9
)

var (
	hopSourceAccount      = common.HexToAddress("0xaaaa000000000000000000000000000000000001")
	hopDestinationAccount = common.HexToAddress("0xbbbb000000000000000000000000000000000002")
	hopSourceBridgeAddr   = common.HexToAddress("0xcccc000000000000000000000000000000000003")
	hopDestBridgeAddr     = common.HexToAddress("0xdddd000000000000000000000000000000000004")
	hopBridgeTxHash       = common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111")
	hopClaimTxHash        = common.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222")
	hopApproveTxHash      = common.HexToHash("0x3333333333333333333333333333333333333333333333333333333333333333")
	hopForeignClaimTx     = common.HexToHash("0x4444444444444444444444444444444444444444444444444444444444444444")
	hopAutoClaimer        = common.HexToAddress("0xeeee000000000000000000000000000000000005")
	hopOriginTokenAddr    = common.HexToAddress("0xffff000000000000000000000000000000000006")
	hopWrappedTokenAddr   = common.HexToAddress("0xabcd000000000000000000000000000000000007")

	hopAmount = big.NewInt(1_000)
)

// hopHarness wires a HopEngine to mocked network and proxy layers, so a whole hop runs in
// microseconds with no chain and no HTTP server.
type hopHarness struct {
	t *testing.T

	srcClient *mocks.NetworkClient
	dstClient *mocks.NetworkClient
	srcBridge *mocks.Bridge
	dstBridge *mocks.Bridge
	backend   *mocks.EthBackend
	proxy     *mocks.Proxy

	tokens map[common.Address]*mocks.Token

	mu          sync.Mutex
	claimed     bool
	credited    bool
	dstNative   *big.Int
	dstToken    *big.Int
	checkpoints []bridgelooptester.HopCheckpoint

	destination uint32
	deps        bridgelooptester.HopDeps
}

// newHopHarness builds a harness whose destination network is destination (use hopL1Network to
// exercise the L1-destination path that skips the injected-leaf gate).
func newHopHarness(t *testing.T, destination uint32) *hopHarness {
	t.Helper()

	h := &hopHarness{
		t:           t,
		srcClient:   mocks.NewNetworkClient(t),
		dstClient:   mocks.NewNetworkClient(t),
		srcBridge:   mocks.NewBridge(t),
		dstBridge:   mocks.NewBridge(t),
		backend:     mocks.NewEthBackend(t),
		proxy:       mocks.NewProxy(t),
		tokens:      map[common.Address]*mocks.Token{},
		dstNative:   big.NewInt(500_000),
		dstToken:    big.NewInt(0),
		destination: destination,
	}

	h.srcClient.EXPECT().NetworkID().Return(hopSourceNetwork).Maybe()
	h.srcClient.EXPECT().From().Return(hopSourceAccount).Maybe()
	h.srcClient.EXPECT().Backend().Return(h.backend).Maybe()
	h.dstClient.EXPECT().NetworkID().Return(destination).Maybe()
	h.dstClient.EXPECT().From().Return(hopDestinationAccount).Maybe()
	h.srcBridge.EXPECT().Address().Return(hopSourceBridgeAddr).Maybe()
	h.dstBridge.EXPECT().Address().Return(hopDestBridgeAddr).Maybe()

	h.srcClient.EXPECT().NativeBalance(mock.Anything, hopSourceAccount).
		Return(big.NewInt(1_000_000), nil).Maybe()
	h.dstClient.EXPECT().NativeBalance(mock.Anything, hopDestinationAccount).
		RunAndReturn(func(context.Context, common.Address) (*big.Int, error) {
			h.mu.Lock()
			defer h.mu.Unlock()

			return new(big.Int).Set(h.dstNative), nil
		}).Maybe()

	h.proxy.EXPECT().TrackBridge(mock.Anything, hopSourceNetwork, mock.Anything).
		Return(&trackerapi.TrackingData{TrackingStatus: "Registered"}, nil).Maybe()

	h.deps = bridgelooptester.HopDeps{
		Networks: map[uint32]bridgelooptester.HopNetwork{
			hopSourceNetwork: {
				Config: bridgelooptester.Network{
					NetworkID: hopSourceNetwork, Name: "l2a", BridgeAddr: hopSourceBridgeAddr,
					MinNativeReserve: bridgelooptester.NewWeiAmount(1_000),
				},
				Client: h.srcClient,
				Bridge: h.srcBridge,
			},
			destination: {
				Config: bridgelooptester.Network{
					NetworkID: destination, Name: "destination", BridgeAddr: hopDestBridgeAddr,
				},
				Client: h.dstClient,
				Bridge: h.dstBridge,
			},
		},
		Proxy:   h.proxy,
		Logger:  log.NewLoggerNil(),
		Timings: hopTestTimings(),
		PersistCheckpoint: func(_ context.Context, checkpoint bridgelooptester.HopCheckpoint) error {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.checkpoints = append(h.checkpoints, checkpoint)

			return nil
		},
		NewTokenFn: func(
			client bridgelooptester.NetworkClient, address common.Address,
		) (bridgelooptester.Token, error) {
			token, ok := h.tokens[address]
			if !ok {
				return nil, fmt.Errorf("test harness has no token mock for %s on network %d",
					address, client.NetworkID())
			}

			return token, nil
		},
	}

	return h
}

// hopTestTimings are deliberately tiny so a manual hop's grace period costs milliseconds.
func hopTestTimings() bridgelooptester.HopTimings {
	return bridgelooptester.HopTimings{
		PollInterval:      time.Millisecond,
		HopTimeout:        20 * time.Second,
		ManualGracePeriod: 10 * time.Millisecond,
	}
}

// engine builds the HopEngine under test.
func (h *hopHarness) engine() *bridgelooptester.HopEngine {
	h.t.Helper()

	engine, err := bridgelooptester.NewHopEngine(h.deps)
	require.NoError(h.t, err)

	return engine
}

// states returns the states that were persisted, in order.
func (h *hopHarness) states() []bridgelooptester.HopState {
	h.mu.Lock()
	defer h.mu.Unlock()

	states := make([]bridgelooptester.HopState, 0, len(h.checkpoints))
	for _, checkpoint := range h.checkpoints {
		states = append(states, checkpoint.State)
	}

	return states
}

// expectBridge makes the source bridge accept a native bridgeAsset and return a deposit.
func (h *hopHarness) expectBridge() {
	h.srcBridge.EXPECT().BridgeAssetNative(mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context, req bridgelooptester.BridgeAssetRequest,
		) (*bridgelooptester.BridgeResult, error) {
			require.Equal(h.t, h.destination, req.DestinationNetwork)
			require.Equal(h.t, hopDestinationAccount, req.DestinationAddress)
			require.Equal(h.t, hopAmount, req.Amount)
			require.True(h.t, req.ForceUpdateGlobalExitRoot)

			return &bridgelooptester.BridgeResult{
				TxHash:  hopBridgeTxHash,
				Receipt: hopBridgeReceipt(),
				Event:   hopBridgeEvent(h.destination, common.Address{}),
			}, nil
		}).Once()
}

// expectGates makes the three readiness gates succeed, and asserts the claim proof is fetched for
// the *injected* leaf index (I'), not the one the origin-index gate returned (I).
func (h *hopHarness) expectGates() {
	h.proxy.EXPECT().
		WaitL1InfoTreeIndex(mock.Anything, hopSourceNetwork, uint64(hopDepositCount), mock.Anything, mock.Anything).
		Return(hopLeafIndex, nil).Once()

	if h.destination != hopL1Network {
		h.proxy.EXPECT().
			WaitInjectedLeaf(mock.Anything, h.destination, hopLeafIndex, mock.Anything, mock.Anything).
			Return(hopInjectedLeafIndex, nil).Once()
	}

	expectedLeafIndex := hopInjectedLeafIndex
	if h.destination == hopL1Network {
		expectedLeafIndex = hopLeafIndex
	}
	h.proxy.EXPECT().
		WaitClaimProof(mock.Anything, hopSourceNetwork, expectedLeafIndex, hopDepositCount,
			mock.Anything, mock.Anything).
		Return(hopClaimProof(), nil).Once()
}

// expectIsClaimed answers isClaimed from the harness's own claim flag, crediting the destination
// balance the first time the deposit is seen claimed (which is what the real chain does).
func (h *hopHarness) expectIsClaimed() {
	h.dstBridge.EXPECT().IsClaimed(mock.Anything, hopDepositCount, hopSourceNetwork).
		RunAndReturn(func(context.Context, uint32, uint32) (bool, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			if h.claimed && !h.credited {
				h.credited = true
				h.dstNative.Add(h.dstNative, hopAmount)
				h.dstToken.Add(h.dstToken, hopAmount)
			}

			return h.claimed, nil
		})
}

// setClaimed marks the deposit claimed for every later isClaimed answer.
func (h *hopHarness) setClaimed() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.claimed = true
}

// claimAfter marks the deposit claimed once isClaimed has been answered "not claimed" `after`
// times, which is how the tests inject an autoclaim service acting mid-wait.
func (h *hopHarness) claimAfter(after int) {
	calls := 0
	h.dstBridge.EXPECT().IsClaimed(mock.Anything, hopDepositCount, hopSourceNetwork).
		RunAndReturn(func(context.Context, uint32, uint32) (bool, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			calls++
			if calls > after {
				if !h.credited {
					h.credited = true
					h.dstNative.Add(h.dstNative, hopAmount)
					h.dstToken.Add(h.dstToken, hopAmount)
				}

				return true, nil
			}

			return false, nil
		})
}

// expectClaimRecord makes the proxy's /bridge/v1/claims cross-check name from as the claimer.
func (h *hopHarness) expectClaimRecord(from common.Address, txHash common.Hash) {
	h.proxy.EXPECT().WaitClaimed(mock.Anything, h.destination, mock.Anything, mock.Anything, mock.Anything).
		Return(&bridgeservicetypes.ClaimResponse{
			TxHash:      bridgeservicetypes.Hash(txHash.Hex()),
			FromAddress: bridgeservicetypes.Address(from.Hex()),
		}, nil).Maybe()
}

// expectNoClaimRecord makes the proxy unable to name the claimer, the common real-world case in
// which the claim syncer trails the on-chain isClaimed read.
func (h *hopHarness) expectNoClaimRecord() {
	h.proxy.EXPECT().WaitClaimed(mock.Anything, h.destination, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, bridgeserviceclient.ErrNotFound).Maybe()
}

// expectToolClaim makes the destination bridge accept the tool's own claimAsset.
func (h *hopHarness) expectToolClaim() {
	h.dstBridge.EXPECT().ClaimAsset(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, req bridgelooptester.ClaimRequest) (*ethtypes.Receipt, error) {
			require.Equal(h.t, bridgelooptester.GlobalIndex(hopSourceNetwork, hopDepositCount), req.GlobalIndex)
			require.Equal(h.t, hopDestinationAccount, req.DestinationAddress)
			require.Equal(h.t, hopAmount, req.Amount)
			h.setClaimed()

			return hopClaimReceipt(), nil
		}).Once()
}

// request builds a native hop request for the harness's route.
func (h *hopHarness) request(claim bridgelooptester.ClaimMode) bridgelooptester.HopRequest {
	return bridgelooptester.HopRequest{
		LoopName:  "eth-ring",
		Iteration: 1,
		HopIndex:  0,
		Hop: bridgelooptester.Hop{
			Source: hopSourceNetwork, Destination: h.destination, Claim: claim,
		},
		Asset:  bridgelooptester.AssetETH,
		Amount: hopAmount,
	}
}

// hopBridgeReceipt is the receipt of a successful bridge transaction.
func hopBridgeReceipt() *ethtypes.Receipt {
	return &ethtypes.Receipt{
		TxHash:      hopBridgeTxHash,
		Status:      ethtypes.ReceiptStatusSuccessful,
		BlockNumber: big.NewInt(1_234),
		GasUsed:     100_000,
	}
}

// hopClaimReceipt is the receipt of a successful claim transaction.
func hopClaimReceipt() *ethtypes.Receipt {
	return &ethtypes.Receipt{
		TxHash:            hopClaimTxHash,
		Status:            ethtypes.ReceiptStatusSuccessful,
		BlockNumber:       big.NewInt(2_345),
		GasUsed:           80_000,
		EffectiveGasPrice: big.NewInt(1),
	}
}

// hopBridgeEvent is the BridgeEvent a bridge receipt decodes to.
func hopBridgeEvent(destination uint32, token common.Address) bridgelooptester.BridgeEvent {
	return bridgelooptester.BridgeEvent{
		LeafType:           0,
		OriginNetwork:      hopSourceNetwork,
		OriginAddress:      token,
		DestinationNetwork: destination,
		DestinationAddress: hopDestinationAccount,
		Amount:             hopAmount,
		Metadata:           []byte{},
		DepositCount:       hopDepositCount,
		TxHash:             hopBridgeTxHash,
		BlockNumber:        1_234,
	}
}

// hopClaimProof is the proof GET /bridge/v1/claim-proof answers with.
func hopClaimProof() *bridgeservicetypes.ClaimProof {
	return &bridgeservicetypes.ClaimProof{
		L1InfoTreeLeaf: bridgeservicetypes.L1InfoTreeLeafResponse{
			L1InfoTreeIndex: hopInjectedLeafIndex,
			MainnetExitRoot: bridgeservicetypes.Hash(
				"0x5555555555555555555555555555555555555555555555555555555555555555"),
			RollupExitRoot: bridgeservicetypes.Hash(
				"0x6666666666666666666666666666666666666666666666666666666666666666"),
			GlobalExitRoot: bridgeservicetypes.Hash(
				"0x7777777777777777777777777777777777777777777777777777777777777777"),
		},
	}
}

func TestNewHopEngineValidation(t *testing.T) {
	t.Parallel()

	base := newHopHarness(t, hopDestinationNetwork).deps

	t.Run("rejects a HopTimeout that cannot contain the grace period", func(t *testing.T) {
		t.Parallel()

		deps := base
		deps.Timings = bridgelooptester.HopTimings{
			PollInterval: time.Second, HopTimeout: time.Minute, ManualGracePeriod: time.Minute,
		}
		_, err := bridgelooptester.NewHopEngine(deps)
		require.ErrorContains(t, err, "must exceed ManualGracePeriod")
	})

	t.Run("rejects a network keyed by the wrong id", func(t *testing.T) {
		t.Parallel()

		deps := base
		deps.Networks = map[uint32]bridgelooptester.HopNetwork{
			99: base.Networks[hopSourceNetwork],
		}
		_, err := bridgelooptester.NewHopEngine(deps)
		require.ErrorContains(t, err, "is keyed by 99 but its NetworkClient reports 1")
	})

	t.Run("rejects missing dependencies", func(t *testing.T) {
		t.Parallel()

		deps := base
		deps.Proxy = nil
		_, err := bridgelooptester.NewHopEngine(deps)
		require.ErrorContains(t, err, "proxy is required")
	})
}

// TestRunHopAutoClaimHappyPath is the auto-claim happy path: the tool bridges, waits, and an
// external claimer (an autoclaim service) claims the deposit within the grace period.
func TestRunHopAutoClaimHappyPath(t *testing.T) {
	t.Parallel()

	h := newHopHarness(t, hopDestinationNetwork)
	h.expectBridge()
	h.expectGates()
	h.claimAfter(2)
	h.expectClaimRecord(hopAutoClaimer, hopForeignClaimTx)

	result, err := h.engine().RunHop(context.Background(), h.request(bridgelooptester.ClaimAuto))
	require.NoError(t, err)
	require.NotNil(t, result)

	require.Equal(t, bridgelooptester.HopOutcomeSuccess, result.Outcome)
	require.Equal(t, bridgelooptester.HopStateVerified, result.FinalState)
	require.Equal(t, bridgelooptester.ClaimActorExternal, result.ClaimedBy)
	require.Equal(t, hopForeignClaimTx, result.ExternalClaimTxHash)
	require.Equal(t, hopAutoClaimer, result.ExternalClaimFromAddress)
	require.Equal(t, common.Hash{}, result.ClaimTxHash, "an auto hop must never submit a claim itself")
	require.Equal(t, hopBridgeTxHash, result.BridgeTxHash)
	require.Equal(t, hopDepositCount, result.DepositCount)
	require.Equal(t, bridgelooptester.GlobalIndex(hopSourceNetwork, hopDepositCount), result.GlobalIndex)
	require.True(t, result.BalanceVerified)
	require.False(t, result.BalanceExact, "a native hop's delta can never be asserted exactly")
	require.Equal(t, hopAmount, result.DestinationDelta)
	require.False(t, result.ClaimModeViolated)
	require.Empty(t, result.StalledGate)
	require.Positive(t, len(result.Phases))

	require.Equal(t, []bridgelooptester.HopState{
		bridgelooptester.HopStateBridging,
		bridgelooptester.HopStateBridged,
		bridgelooptester.HopStateWaitingOriginIndex,
		bridgelooptester.HopStateWaitingGERInjection,
		bridgelooptester.HopStateFetchingClaimProof,
		bridgelooptester.HopStateAwaitingClaim,
		bridgelooptester.HopStateClaimed,
		bridgelooptester.HopStateVerified,
	}, h.states())
}

// TestRunHopManualClaimHappyPath is the manual-claim happy path: nothing claims the deposit during
// the grace period (the negative assertion), and only then does the tool claim it itself.
func TestRunHopManualClaimHappyPath(t *testing.T) {
	t.Parallel()

	h := newHopHarness(t, hopDestinationNetwork)
	h.expectBridge()
	h.expectGates()
	h.expectIsClaimed()
	h.expectToolClaim()
	h.expectNoClaimRecord()

	result, err := h.engine().RunHop(context.Background(), h.request(bridgelooptester.ClaimManual))
	require.NoError(t, err)

	require.Equal(t, bridgelooptester.HopOutcomeSuccess, result.Outcome)
	require.Equal(t, bridgelooptester.ClaimActorTool, result.ClaimedBy)
	require.Equal(t, hopClaimTxHash, result.ClaimTxHash)
	require.Equal(t, uint64(2_345), result.ClaimBlockNumber)
	require.Equal(t, uint64(80_000), result.ClaimGasUsed)
	require.Equal(t, big.NewInt(80_000), result.ClaimGasCost)
	require.Equal(t, hopTestTimings().ManualGracePeriod, result.GracePeriod)
	require.True(t, result.BalanceVerified)
	require.Contains(t, h.states(), bridgelooptester.HopStateSubmittingClaim)
	require.Equal(t, bridgelooptester.HopStateVerified, result.FinalState)
}

// TestRunHopManualClaimStolenDuringGracePeriod is the negative test the manual claim mode exists
// for: something claims the deposit during the window in which nothing may, so the hop is a
// reportable failure with a message naming the offending claimer - never a retry.
func TestRunHopManualClaimStolenDuringGracePeriod(t *testing.T) {
	t.Parallel()

	h := newHopHarness(t, hopDestinationNetwork)
	h.expectBridge()
	h.expectGates()
	h.claimAfter(1) // unclaimed at the decision point, claimed on the first grace-period poll
	h.expectClaimRecord(hopAutoClaimer, hopForeignClaimTx)

	result, err := h.engine().RunHop(context.Background(), h.request(bridgelooptester.ClaimManual))
	require.Error(t, err)
	require.ErrorIs(t, err, bridgelooptester.ErrClaimModeViolation)

	var violation *bridgelooptester.ClaimModeViolationError
	require.ErrorAs(t, err, &violation)
	require.Equal(t, bridgelooptester.ClaimManual, violation.Expected)
	require.Equal(t, bridgelooptester.ClaimActorExternal, violation.Observed)
	require.Equal(t, hopDepositCount, violation.DepositCount)
	require.Equal(t, hopForeignClaimTx, violation.ClaimTxHash)
	require.Equal(t, hopAutoClaimer, violation.ClaimFromAddress)
	require.True(t, violation.ProofAvailable)

	require.Contains(t, err.Error(), "claim-mode violation")
	require.Contains(t, err.Error(), "asserts that nothing claims it")
	require.Contains(t, err.Error(), "an autoclaim service appears to be active for this route")
	require.Contains(t, err.Error(), hopAutoClaimer.String())

	require.Equal(t, bridgelooptester.HopOutcomeFailed, result.Outcome)
	require.True(t, result.ClaimModeViolated)
	require.Equal(t, bridgelooptester.HopStateFailed, result.FinalState)
	require.Equal(t, bridgelooptester.ClaimActorExternal, result.ClaimedBy)
	require.False(t, result.BalanceVerified)
	require.NotContains(t, h.states(), bridgelooptester.HopStateSubmittingClaim,
		"a violated manual hop must not go on to submit a claim")
}

// TestRunHopAutoClaimNeverClaimed is the mirror-image violation: an auto hop nobody claimed, which
// is how the tool detects that a network's autoclaim policy is not running for a route.
func TestRunHopAutoClaimNeverClaimed(t *testing.T) {
	t.Parallel()

	h := newHopHarness(t, hopDestinationNetwork)
	h.expectBridge()
	h.expectGates()
	h.expectIsClaimed() // never claimed
	h.expectNoClaimRecord()

	result, err := h.engine().RunHop(context.Background(), h.request(bridgelooptester.ClaimAuto))
	require.ErrorIs(t, err, bridgelooptester.ErrClaimModeViolation)

	var violation *bridgelooptester.ClaimModeViolationError
	require.ErrorAs(t, err, &violation)
	require.Equal(t, bridgelooptester.ClaimAuto, violation.Expected)
	require.Equal(t, bridgelooptester.ClaimActorNone, violation.Observed)
	require.True(t, violation.ProofAvailable, "the proof was available, so nothing but the policy is at fault")
	require.Contains(t, err.Error(), "no autoclaim service is claiming this route")

	require.True(t, result.ClaimModeViolated)
	require.Equal(t, bridgelooptester.ClaimActorNone, result.ClaimedBy)
}

// TestRunHopGateTimeoutNamesTheGate covers the "diagnosis, not a bare timeout" promise for every
// readiness gate the hop waits on.
func TestRunHopGateTimeoutNamesTheGate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		gate       string
		setupProxy func(h *hopHarness)
	}{
		{
			name: "origin index",
			gate: "l1-info-tree-index",
			setupProxy: func(h *hopHarness) {
				h.proxy.EXPECT().WaitL1InfoTreeIndex(mock.Anything, hopSourceNetwork, uint64(hopDepositCount),
					mock.Anything, mock.Anything).
					Return(0, &bridgelooptester.DeadlineExceededError{
						Gate:     "l1-info-tree-index",
						Detail:   "network_id=1 deposit_count=42",
						Deadline: time.Second,
						LastErr:  bridgeserviceclient.ErrNotFound,
					}).Once()
			},
		},
		{
			name: "ger injection",
			gate: "injected-l1-info-leaf",
			setupProxy: func(h *hopHarness) {
				h.proxy.EXPECT().WaitL1InfoTreeIndex(mock.Anything, hopSourceNetwork, uint64(hopDepositCount),
					mock.Anything, mock.Anything).Return(hopLeafIndex, nil).Once()
				h.proxy.EXPECT().WaitInjectedLeaf(mock.Anything, hopDestinationNetwork, hopLeafIndex,
					mock.Anything, mock.Anything).
					Return(0, &bridgelooptester.DeadlineExceededError{
						Gate:     "injected-l1-info-leaf",
						Detail:   "network_id=2 leaf_index=7",
						Deadline: time.Second,
						LastErr:  bridgeserviceclient.ErrNotFound,
					}).Once()
			},
		},
		{
			name: "claim proof",
			gate: "claim-proof",
			setupProxy: func(h *hopHarness) {
				h.proxy.EXPECT().WaitL1InfoTreeIndex(mock.Anything, hopSourceNetwork, uint64(hopDepositCount),
					mock.Anything, mock.Anything).Return(hopLeafIndex, nil).Once()
				h.proxy.EXPECT().WaitInjectedLeaf(mock.Anything, hopDestinationNetwork, hopLeafIndex,
					mock.Anything, mock.Anything).Return(hopInjectedLeafIndex, nil).Once()
				h.proxy.EXPECT().WaitClaimProof(mock.Anything, hopSourceNetwork, hopInjectedLeafIndex,
					hopDepositCount, mock.Anything, mock.Anything).
					Return(nil, &bridgelooptester.DeadlineExceededError{
						Gate:     "claim-proof",
						Detail:   "network_id=1 leaf_index=9 deposit_count=42",
						Deadline: time.Second,
						LastErr:  bridgeserviceclient.ErrServiceUnavailable,
					}).Once()
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHopHarness(t, hopDestinationNetwork)
			h.expectBridge()
			tc.setupProxy(h)

			result, err := h.engine().RunHop(context.Background(), h.request(bridgelooptester.ClaimManual))
			require.ErrorIs(t, err, bridgelooptester.ErrDeadlineExceeded)

			var gateErr *bridgelooptester.DeadlineExceededError
			require.ErrorAs(t, err, &gateErr)
			require.Equal(t, tc.gate, gateErr.Gate)
			require.NotEmpty(t, gateErr.Detail, "the diagnosis must carry the concrete inputs")

			require.Equal(t, bridgelooptester.HopOutcomeFailed, result.Outcome)
			require.Equal(t, tc.gate, result.StalledGate)
			require.False(t, result.ClaimModeViolated)
		})
	}
}

// TestRunHopThreadsTheInjectedLeafIndex is the regression test for the one contract the proxy layer
// documents but cannot enforce: the claim proof must be fetched for the *actually injected* leaf
// index I', not for the index I the origin-index gate returned.
func TestRunHopThreadsTheInjectedLeafIndex(t *testing.T) {
	t.Parallel()

	h := newHopHarness(t, hopDestinationNetwork)
	h.expectBridge()
	h.expectIsClaimed()
	h.expectToolClaim()
	h.expectNoClaimRecord()

	h.proxy.EXPECT().WaitL1InfoTreeIndex(mock.Anything, hopSourceNetwork, uint64(hopDepositCount),
		mock.Anything, mock.Anything).Return(hopLeafIndex, nil).Once()
	h.proxy.EXPECT().WaitInjectedLeaf(mock.Anything, hopDestinationNetwork, hopLeafIndex,
		mock.Anything, mock.Anything).Return(hopInjectedLeafIndex, nil).Once()
	// The assertion: leafIndex here is I' (9), never I (7). Any other value fails the expectation.
	h.proxy.EXPECT().WaitClaimProof(mock.Anything, hopSourceNetwork, hopInjectedLeafIndex,
		hopDepositCount, mock.Anything, mock.Anything).Return(hopClaimProof(), nil).Once()

	result, err := h.engine().RunHop(context.Background(), h.request(bridgelooptester.ClaimManual))
	require.NoError(t, err)
	require.Equal(t, hopLeafIndex, result.L1InfoTreeIndex)
	require.Equal(t, hopInjectedLeafIndex, result.InjectedLeafIndex)
	require.True(t, result.InjectedLeafAdvanced)
	require.False(t, result.InjectedLeafSkipped)
}

// TestRunHopSkipsInjectedLeafForL1Destination covers DESIGN.md §2's rule that an L1 destination has
// no injected-leaf gate, so I' == I by definition.
func TestRunHopSkipsInjectedLeafForL1Destination(t *testing.T) {
	t.Parallel()

	h := newHopHarness(t, hopL1Network)
	h.expectBridge()
	h.expectGates()
	h.expectIsClaimed()
	h.expectToolClaim()
	h.expectNoClaimRecord()

	result, err := h.engine().RunHop(context.Background(), h.request(bridgelooptester.ClaimManual))
	require.NoError(t, err)
	require.True(t, result.InjectedLeafSkipped)
	require.Equal(t, hopLeafIndex, result.L1InfoTreeIndex)
	require.Equal(t, hopLeafIndex, result.InjectedLeafIndex)
	require.False(t, result.InjectedLeafAdvanced)
	require.NotContains(t, h.states(), bridgelooptester.HopStateWaitingGERInjection)
}

// TestRunHopERC20ExactBalanceDelta covers the ERC20 path end to end: the wrapped address is
// resolved on both endpoints, the approve is submitted once, and the destination delta is asserted
// exactly (a token balance is untouched by gas, unlike a native one).
func TestRunHopERC20ExactBalanceDelta(t *testing.T) {
	t.Parallel()

	h := newHopHarness(t, hopDestinationNetwork)
	originNetwork := hopSourceNetwork

	sourceToken := mocks.NewToken(t)
	destinationToken := mocks.NewToken(t)
	h.tokens[hopOriginTokenAddr] = sourceToken
	h.tokens[hopWrappedTokenAddr] = destinationToken

	h.dstBridge.EXPECT().GetTokenWrappedAddress(mock.Anything, originNetwork, hopOriginTokenAddr).
		Return(hopWrappedTokenAddr, nil).Once()

	sourceToken.EXPECT().BalanceOf(mock.Anything, hopSourceAccount).Return(big.NewInt(10_000), nil).Once()
	sourceToken.EXPECT().Allowance(mock.Anything, hopSourceAccount, hopSourceBridgeAddr).
		Return(big.NewInt(0), nil).Once()
	sourceToken.EXPECT().Approve(mock.Anything, hopSourceBridgeAddr, mock.Anything).
		Return(&ethtypes.Receipt{TxHash: hopApproveTxHash, Status: ethtypes.ReceiptStatusSuccessful}, nil).Once()

	destinationToken.EXPECT().BalanceOf(mock.Anything, hopDestinationAccount).
		RunAndReturn(func(context.Context, common.Address) (*big.Int, error) {
			h.mu.Lock()
			defer h.mu.Unlock()

			return new(big.Int).Set(h.dstToken), nil
		})

	h.srcBridge.EXPECT().BridgeAssetERC20(mock.Anything, hopOriginTokenAddr, mock.Anything).
		Return(&bridgelooptester.BridgeResult{
			TxHash:  hopBridgeTxHash,
			Receipt: hopBridgeReceipt(),
			Event:   hopBridgeEvent(hopDestinationNetwork, hopOriginTokenAddr),
		}, nil).Once()

	h.expectGates()
	h.expectIsClaimed()
	h.expectToolClaim()
	h.expectNoClaimRecord()

	req := h.request(bridgelooptester.ClaimManual)
	req.Asset = bridgelooptester.AssetERC20
	req.TokenOriginNetwork = &originNetwork
	req.TokenOriginAddress = hopOriginTokenAddr

	result, err := h.engine().RunHop(context.Background(), req)
	require.NoError(t, err)

	require.Equal(t, hopOriginTokenAddr, result.SourceTokenAddress)
	require.Equal(t, hopWrappedTokenAddr, result.DestinationTokenAddress)
	require.True(t, result.Approved)
	require.Equal(t, hopApproveTxHash, result.ApproveTxHash)
	require.True(t, result.BalanceVerified)
	require.True(t, result.BalanceExact, "an ERC20 delta must be asserted exactly")
	require.Equal(t, hopAmount, result.DestinationDelta)
	require.Contains(t, h.states(), bridgelooptester.HopStateApproving)
}

// TestRunHopInsufficientSourceBalance covers the balance guard, including that MinNativeReserve is
// part of what a native hop must leave behind.
func TestRunHopInsufficientSourceBalance(t *testing.T) {
	t.Parallel()

	h := newHopHarness(t, hopDestinationNetwork)
	h.deps.Networks[hopSourceNetwork] = bridgelooptester.HopNetwork{
		Config: bridgelooptester.Network{
			NetworkID: hopSourceNetwork, Name: "l2a", BridgeAddr: hopSourceBridgeAddr,
			MinNativeReserve: bridgelooptester.NewWeiAmount(1_000_000),
		},
		Client: h.srcClient,
		Bridge: h.srcBridge,
	}

	result, err := h.engine().RunHop(context.Background(), h.request(bridgelooptester.ClaimManual))
	require.ErrorIs(t, err, bridgelooptester.ErrInsufficientBalance)

	var shortErr *bridgelooptester.InsufficientBalanceError
	require.ErrorAs(t, err, &shortErr)
	require.Equal(t, big.NewInt(1_001_000), shortErr.Required)
	require.Equal(t, big.NewInt(1_000_000), shortErr.Available)
	require.Equal(t, bridgelooptester.HopOutcomeFailed, result.Outcome)
	require.Empty(t, h.states(), "nothing may be checkpointed before the hop is known to be fundable")
}

// TestRunHopNativeBalanceTolerance covers the tolerant native check from both sides: a shortfall
// within the gas allowance is tolerated and recorded, and one beyond it fails the hop. See
// HopEngine.verifyDestinationBalance for why the native case can never be exact.
func TestRunHopNativeBalanceTolerance(t *testing.T) {
	t.Parallel()

	// The claim gas cost is deliberately excluded from the tolerance here (a receipt with no
	// effective gas price yields no cost), so the slack alone decides.
	claimReceiptWithoutGasPrice := func() *ethtypes.Receipt {
		receipt := hopClaimReceipt()
		receipt.EffectiveGasPrice = nil

		return receipt
	}

	tests := []struct {
		name     string
		slack    *big.Int
		credit   *big.Int
		wantErr  bool
		wantNote string
	}{
		{
			name:     "shortfall within the gas allowance is tolerated",
			slack:    new(big.Int).Set(hopAmount),
			credit:   big.NewInt(1),
			wantNote: "tolerated a native shortfall of 999",
		},
		{
			name:    "shortfall beyond the gas allowance fails the hop",
			slack:   big.NewInt(1),
			credit:  big.NewInt(1),
			wantErr: true,
		},
		{
			name:     "a credit larger than the amount is tolerated and recorded",
			slack:    big.NewInt(1),
			credit:   new(big.Int).Add(hopAmount, big.NewInt(5)),
			wantNote: "5 more than the hop amount",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHopHarness(t, hopDestinationNetwork)
			h.deps.NativeGasSlack = tc.slack
			h.expectBridge()
			h.expectGates()
			h.expectNoClaimRecord()

			h.dstBridge.EXPECT().IsClaimed(mock.Anything, hopDepositCount, hopSourceNetwork).
				RunAndReturn(func(context.Context, uint32, uint32) (bool, error) {
					h.mu.Lock()
					defer h.mu.Unlock()

					return h.claimed, nil
				})
			h.dstBridge.EXPECT().ClaimAsset(mock.Anything, mock.Anything).
				RunAndReturn(func(context.Context, bridgelooptester.ClaimRequest) (*ethtypes.Receipt, error) {
					h.mu.Lock()
					h.claimed = true
					h.dstNative.Add(h.dstNative, tc.credit)
					h.mu.Unlock()

					return claimReceiptWithoutGasPrice(), nil
				}).Once()

			result, err := h.engine().RunHop(context.Background(), h.request(bridgelooptester.ClaimManual))
			if tc.wantErr {
				require.ErrorIs(t, err, bridgelooptester.ErrBalanceMismatch)

				var mismatch *bridgelooptester.BalanceMismatchError
				require.ErrorAs(t, err, &mismatch)
				require.Contains(t, mismatch.Detail, "fell short of the hop amount by more than the gas allowance")
				require.False(t, result.BalanceVerified)
				require.Equal(t, bridgelooptester.ClaimActorTool, result.ClaimedBy)

				return
			}

			require.NoError(t, err)
			require.True(t, result.BalanceVerified)
			require.False(t, result.BalanceExact)
			require.Contains(t, result.BalanceNote, tc.wantNote)
		})
	}
}

// TestRunHopRequestValidation covers the malformed-request guards, each of which still yields a
// result so a caller's report never has a hole in it.
func TestRunHopRequestValidation(t *testing.T) {
	t.Parallel()

	originNetwork := hopSourceNetwork
	unknownNetwork := uint32(77)

	tests := []struct {
		name    string
		mutate  func(req *bridgelooptester.HopRequest)
		wantErr string
	}{
		{
			name:    "same source and destination",
			mutate:  func(r *bridgelooptester.HopRequest) { r.Hop.Destination = r.Hop.Source },
			wantErr: "source and destination network must differ",
		},
		{
			name:    "zero amount",
			mutate:  func(r *bridgelooptester.HopRequest) { r.Amount = big.NewInt(0) },
			wantErr: "amount must be > 0",
		},
		{
			name:    "unknown claim mode",
			mutate:  func(r *bridgelooptester.HopRequest) { r.Hop.Claim = "whenever" },
			wantErr: `unknown claim mode "whenever"`,
		},
		{
			name:    "unknown source network",
			mutate:  func(r *bridgelooptester.HopRequest) { r.Hop.Source = unknownNetwork },
			wantErr: "no network client configured for source network 77",
		},
		{
			name:    "unknown destination network",
			mutate:  func(r *bridgelooptester.HopRequest) { r.Hop.Destination = unknownNetwork },
			wantErr: "no network client configured for destination network 77",
		},
		{
			name:    "eth hop with a token origin network",
			mutate:  func(r *bridgelooptester.HopRequest) { r.TokenOriginNetwork = &originNetwork },
			wantErr: `must not carry a TokenOriginNetwork`,
		},
		{
			name: "erc20 hop without a token origin network",
			mutate: func(r *bridgelooptester.HopRequest) {
				r.Asset = bridgelooptester.AssetERC20
			},
			wantErr: "requires a TokenOriginNetwork",
		},
		{
			name: "erc20 hop without a token address",
			mutate: func(r *bridgelooptester.HopRequest) {
				r.Asset = bridgelooptester.AssetERC20
				r.TokenOriginNetwork = &originNetwork
			},
			wantErr: "requires a non-zero TokenOriginAddress",
		},
		{
			name:    "unknown asset kind",
			mutate:  func(r *bridgelooptester.HopRequest) { r.Asset = "gold" },
			wantErr: `unknown asset kind "gold"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHopHarness(t, hopDestinationNetwork)
			req := h.request(bridgelooptester.ClaimManual)
			tc.mutate(&req)

			result, err := h.engine().RunHop(context.Background(), req)
			require.ErrorContains(t, err, tc.wantErr)
			require.NotNil(t, result)
			require.Equal(t, bridgelooptester.HopOutcomeFailed, result.Outcome)
		})
	}
}

// TestRunHopCheckpointPersistenceFailureFailsTheHop covers the deliberate choice that a hop whose
// state cannot be persisted fails rather than silently continuing without resumability.
func TestRunHopCheckpointPersistenceFailureFailsTheHop(t *testing.T) {
	t.Parallel()

	h := newHopHarness(t, hopDestinationNetwork)
	h.deps.PersistCheckpoint = func(context.Context, bridgelooptester.HopCheckpoint) error {
		return errors.New("disk full")
	}

	_, err := h.engine().RunHop(context.Background(), h.request(bridgelooptester.ClaimManual))
	require.ErrorContains(t, err, "persist checkpoint for state")
	require.ErrorContains(t, err, "disk full")
}

// expectBridgeRecovery makes a resumed hop able to re-derive its deposit from the checkpointed
// bridge transaction hash: re-read the receipt, re-decode the BridgeEvent (DESIGN.md §4/§5).
func (h *hopHarness) expectBridgeRecovery() {
	receipt := hopBridgeReceipt()
	event := hopBridgeEvent(h.destination, common.Address{})

	h.backend.EXPECT().TransactionReceipt(mock.Anything, hopBridgeTxHash).Return(receipt, nil).Once()
	h.srcBridge.EXPECT().BridgeEventFromReceipt(mock.Anything).Return(&event, nil).Once()
}

// resumeCheckpoint builds a checkpoint for state, with a destination baseline that matches the
// harness's starting balance so the delta check has something to compare against.
func (h *hopHarness) resumeCheckpoint(state bridgelooptester.HopState) *bridgelooptester.HopCheckpoint {
	h.mu.Lock()
	defer h.mu.Unlock()

	return &bridgelooptester.HopCheckpoint{
		State:                    state,
		BridgeTxHash:             hopBridgeTxHash,
		DepositCount:             hopDepositCount,
		L1InfoTreeIndex:          hopLeafIndex,
		InjectedLeafIndex:        hopInjectedLeafIndex,
		DestinationBalanceBefore: new(big.Int).Set(h.dstNative).String(),
		StartedAt:                time.Now().Add(-time.Minute),
	}
}

// TestRunHopResumesEveryMidHopState covers the resume contract for every state a hop can be
// interrupted in whose position is re-derivable: the deposit is recovered from the checkpointed
// bridge transaction and the hop continues from wherever its idempotent reads land it, without
// re-bridging.
func TestRunHopResumesEveryMidHopState(t *testing.T) {
	t.Parallel()

	// Every one of these states resumes identically: the gates are idempotent reads, so they are
	// simply re-run, and the manual claim path then completes the hop.
	states := []bridgelooptester.HopState{
		bridgelooptester.HopStateBridged,
		bridgelooptester.HopStateWaitingOriginIndex,
		bridgelooptester.HopStateWaitingGERInjection,
		bridgelooptester.HopStateFetchingClaimProof,
		bridgelooptester.HopStateAwaitingClaim,
	}

	for _, state := range states {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()

			h := newHopHarness(t, hopDestinationNetwork)
			h.expectBridgeRecovery()
			h.expectGates()
			h.expectIsClaimed()
			h.expectToolClaim()
			h.expectNoClaimRecord()

			req := h.request(bridgelooptester.ClaimManual)
			req.Resume = h.resumeCheckpoint(state)

			result, err := h.engine().RunHop(context.Background(), req)
			require.NoError(t, err)

			require.True(t, result.Resumed)
			require.Equal(t, state, result.ResumedFrom)
			require.Equal(t, hopBridgeTxHash, result.BridgeTxHash)
			require.Equal(t, hopDepositCount, result.DepositCount)
			require.Equal(t, bridgelooptester.ClaimActorTool, result.ClaimedBy)
			require.Equal(t, hopClaimTxHash, result.ClaimTxHash)
			require.True(t, result.BalanceVerified)
			require.Equal(t, bridgelooptester.HopStateVerified, result.FinalState)
			// The source bridge was never asked to bridge again: mocks.Bridge asserts that, since
			// no BridgeAssetNative expectation was registered on it.
		})
	}
}

// TestRunHopResumeFromAwaitingClaimDetectsAStolenManualClaim covers the resume case that must stay
// a failure: HopStateAwaitingClaim is only ever checkpointed before the tool submits anything, so a
// deposit found claimed there was claimed by someone else, and a manual hop forbids that.
func TestRunHopResumeFromAwaitingClaimDetectsAStolenManualClaim(t *testing.T) {
	t.Parallel()

	h := newHopHarness(t, hopDestinationNetwork)
	h.expectBridgeRecovery()
	h.expectGates()
	h.setClaimed()
	h.expectIsClaimed()
	h.expectClaimRecord(hopAutoClaimer, hopForeignClaimTx)

	req := h.request(bridgelooptester.ClaimManual)
	req.Resume = h.resumeCheckpoint(bridgelooptester.HopStateAwaitingClaim)

	result, err := h.engine().RunHop(context.Background(), req)
	require.ErrorIs(t, err, bridgelooptester.ErrClaimModeViolation)
	require.Contains(t, err.Error(), hopAutoClaimer.String())
	require.True(t, result.ClaimModeViolated)
	require.Equal(t, bridgelooptester.ClaimActorExternal, result.ClaimedBy)
}

// TestRunHopResumeFromSubmittingClaim covers the two outcomes of the one state that is checkpointed
// before the tool's own claim: a claim that already landed is attributed to the tool (not treated
// as someone else's), and a claim that never landed is simply re-submitted.
func TestRunHopResumeFromSubmittingClaim(t *testing.T) {
	t.Parallel()

	t.Run("claim already landed is attributed to the tool", func(t *testing.T) {
		t.Parallel()

		h := newHopHarness(t, hopDestinationNetwork)
		h.expectBridgeRecovery()
		h.expectGates()
		h.setClaimed()
		h.expectIsClaimed()
		h.expectClaimRecord(hopDestinationAccount, hopClaimTxHash)

		req := h.request(bridgelooptester.ClaimManual)
		req.Resume = h.resumeCheckpoint(bridgelooptester.HopStateSubmittingClaim)

		result, err := h.engine().RunHop(context.Background(), req)
		require.NoError(t, err)
		require.Equal(t, bridgelooptester.ClaimActorTool, result.ClaimedBy)
		require.Equal(t, hopClaimTxHash, result.ClaimTxHash)
		require.NotContains(t, h.states(), bridgelooptester.HopStateSubmittingClaim,
			"a claim that already landed must not be submitted again")
	})

	t.Run("claim that never landed is re-submitted", func(t *testing.T) {
		t.Parallel()

		h := newHopHarness(t, hopDestinationNetwork)
		h.expectBridgeRecovery()
		h.expectGates()
		h.expectIsClaimed()
		h.expectToolClaim()
		h.expectNoClaimRecord()

		req := h.request(bridgelooptester.ClaimManual)
		req.Resume = h.resumeCheckpoint(bridgelooptester.HopStateSubmittingClaim)

		result, err := h.engine().RunHop(context.Background(), req)
		require.NoError(t, err)
		require.Equal(t, bridgelooptester.ClaimActorTool, result.ClaimedBy)
		require.Contains(t, h.states(), bridgelooptester.HopStateSubmittingClaim)
	})

	t.Run("claim landed but from another account is still a manual violation", func(t *testing.T) {
		t.Parallel()

		h := newHopHarness(t, hopDestinationNetwork)
		h.expectBridgeRecovery()
		h.expectGates()
		h.setClaimed()
		h.expectIsClaimed()
		h.expectClaimRecord(hopAutoClaimer, hopForeignClaimTx)

		req := h.request(bridgelooptester.ClaimManual)
		req.Resume = h.resumeCheckpoint(bridgelooptester.HopStateSubmittingClaim)

		_, err := h.engine().RunHop(context.Background(), req)
		require.ErrorIs(t, err, bridgelooptester.ErrClaimModeViolation)
	})
}

// TestRunHopResumeFromClaimed covers the resume that skips every gate: the claim was already
// observed, so all that is left is confirming it on-chain and checking the balance.
func TestRunHopResumeFromClaimed(t *testing.T) {
	t.Parallel()

	t.Run("confirms and verifies", func(t *testing.T) {
		t.Parallel()

		h := newHopHarness(t, hopDestinationNetwork)
		checkpoint := h.resumeCheckpoint(bridgelooptester.HopStateClaimed)
		checkpoint.ClaimTxHash = hopClaimTxHash

		h.expectBridgeRecovery()
		h.setClaimed()
		h.expectIsClaimed()
		h.expectClaimRecord(hopDestinationAccount, hopClaimTxHash)

		req := h.request(bridgelooptester.ClaimManual)
		req.Resume = checkpoint

		result, err := h.engine().RunHop(context.Background(), req)
		require.NoError(t, err)
		require.Equal(t, bridgelooptester.ClaimActorTool, result.ClaimedBy)
		require.True(t, result.BalanceVerified)
		require.Equal(t, []bridgelooptester.HopState{
			bridgelooptester.HopStateClaimed,
			bridgelooptester.HopStateVerified,
		}, h.states(), "no readiness gate is re-polled once the claim was already observed")
	})

	t.Run("refuses a checkpoint the chain disagrees with", func(t *testing.T) {
		t.Parallel()

		h := newHopHarness(t, hopDestinationNetwork)
		h.expectBridgeRecovery()
		h.expectIsClaimed() // never claimed

		req := h.request(bridgelooptester.ClaimManual)
		req.Resume = h.resumeCheckpoint(bridgelooptester.HopStateClaimed)

		_, err := h.engine().RunHop(context.Background(), req)
		require.ErrorContains(t, err, "reports false")
		require.ErrorContains(t, err, "reset the checkpoint")
	})
}

// TestRunHopResumeFromBridgingIsRefused covers the one state whose outcome a restart cannot
// observe: continuing would risk bridging the amount twice, so the engine refuses loudly and
// explains how to recover by hand rather than guessing.
func TestRunHopResumeFromBridgingIsRefused(t *testing.T) {
	t.Parallel()

	h := newHopHarness(t, hopDestinationNetwork)

	req := h.request(bridgelooptester.ClaimManual)
	req.Resume = &bridgelooptester.HopCheckpoint{State: bridgelooptester.HopStateBridging}

	result, err := h.engine().RunHop(context.Background(), req)
	require.ErrorIs(t, err, bridgelooptester.ErrAmbiguousResume)

	var ambiguous *bridgelooptester.AmbiguousResumeError
	require.ErrorAs(t, err, &ambiguous)
	require.Equal(t, bridgelooptester.HopStateBridging, ambiguous.State)
	require.Equal(t, hopSourceAccount, ambiguous.Account)
	require.Contains(t, err.Error(), "risk bridging twice")
	require.Contains(t, err.Error(), "/bridge/v1/bridges?network_id=1")
	require.Equal(t, bridgelooptester.HopOutcomeFailed, result.Outcome)
	require.Empty(t, h.states())
}

// TestRunHopResumeFromPendingAndApprovingRestartCleanly covers the two states whose side effects
// are safe to repeat: nothing was submitted at all, or only an idempotent approve was.
func TestRunHopResumeFromPendingAndApprovingRestartCleanly(t *testing.T) {
	t.Parallel()

	for _, state := range []bridgelooptester.HopState{
		bridgelooptester.HopStatePending,
		bridgelooptester.HopStateApproving,
	} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()

			h := newHopHarness(t, hopDestinationNetwork)
			h.expectBridge()
			h.expectGates()
			h.expectIsClaimed()
			h.expectToolClaim()
			h.expectNoClaimRecord()

			req := h.request(bridgelooptester.ClaimManual)
			req.Resume = &bridgelooptester.HopCheckpoint{State: state}

			result, err := h.engine().RunHop(context.Background(), req)
			require.NoError(t, err)
			require.True(t, result.Resumed)
			require.Equal(t, state, result.ResumedFrom)
			require.Equal(t, bridgelooptester.HopStateVerified, result.FinalState)
			require.Contains(t, h.states(), bridgelooptester.HopStateBridging,
				"a hop with nothing irreversible behind it restarts from the bridge")
		})
	}
}

// TestRunHopResumeFromTerminalStates covers the two verdict states: a verified hop is a no-op, and
// a failed hop is a test result the engine refuses to re-drive.
func TestRunHopResumeFromTerminalStates(t *testing.T) {
	t.Parallel()

	t.Run("verified is a no-op", func(t *testing.T) {
		t.Parallel()

		h := newHopHarness(t, hopDestinationNetwork)
		req := h.request(bridgelooptester.ClaimManual)
		req.Resume = h.resumeCheckpoint(bridgelooptester.HopStateVerified)

		result, err := h.engine().RunHop(context.Background(), req)
		require.NoError(t, err)
		require.Equal(t, bridgelooptester.HopOutcomeSuccess, result.Outcome)
		require.Equal(t, bridgelooptester.HopStateVerified, result.FinalState)
		require.Empty(t, h.states())
	})

	t.Run("failed is not re-driven", func(t *testing.T) {
		t.Parallel()

		h := newHopHarness(t, hopDestinationNetwork)
		req := h.request(bridgelooptester.ClaimManual)
		req.Resume = h.resumeCheckpoint(bridgelooptester.HopStateFailed)

		_, err := h.engine().RunHop(context.Background(), req)
		require.ErrorContains(t, err, "a failed hop is a test result")
	})
}

// TestRunHopResumeRejectsAnUnusableCheckpoint covers the two checkpoints that name a state the
// engine cannot act on.
func TestRunHopResumeRejectsAnUnusableCheckpoint(t *testing.T) {
	t.Parallel()

	t.Run("mid-hop state without a bridge transaction", func(t *testing.T) {
		t.Parallel()

		h := newHopHarness(t, hopDestinationNetwork)
		req := h.request(bridgelooptester.ClaimManual)
		req.Resume = &bridgelooptester.HopCheckpoint{State: bridgelooptester.HopStateAwaitingClaim}

		_, err := h.engine().RunHop(context.Background(), req)
		require.ErrorContains(t, err, "carries no bridge transaction hash")
	})

	t.Run("unknown state", func(t *testing.T) {
		t.Parallel()

		h := newHopHarness(t, hopDestinationNetwork)
		req := h.request(bridgelooptester.ClaimManual)
		req.Resume = &bridgelooptester.HopCheckpoint{State: "somewhere-else"}

		_, err := h.engine().RunHop(context.Background(), req)
		require.ErrorContains(t, err, `unknown checkpoint state "somewhere-else"`)
	})

	t.Run("bridge transaction that reverted", func(t *testing.T) {
		t.Parallel()

		h := newHopHarness(t, hopDestinationNetwork)
		h.backend.EXPECT().TransactionReceipt(mock.Anything, hopBridgeTxHash).
			Return(&ethtypes.Receipt{
				TxHash: hopBridgeTxHash, Status: ethtypes.ReceiptStatusFailed, BlockNumber: big.NewInt(1),
			}, nil).Once()

		req := h.request(bridgelooptester.ClaimManual)
		req.Resume = h.resumeCheckpoint(bridgelooptester.HopStateBridged)

		_, err := h.engine().RunHop(context.Background(), req)
		require.ErrorContains(t, err, "was mined with a failed status")
	})
}

// TestRunHopManualClaimLosesRaceAfterGracePeriod covers the AlreadyClaimed reconciliation: the
// claim submission lost a race, isClaimed confirms the deposit is claimed, and a manual hop still
// reports the claim-mode violation rather than passing on someone else's claim.
func TestRunHopManualClaimLosesRaceAfterGracePeriod(t *testing.T) {
	t.Parallel()

	h := newHopHarness(t, hopDestinationNetwork)
	h.expectBridge()
	h.expectGates()
	h.expectClaimRecord(hopAutoClaimer, hopForeignClaimTx)

	claimSubmitted := false
	h.dstBridge.EXPECT().IsClaimed(mock.Anything, hopDepositCount, hopSourceNetwork).
		RunAndReturn(func(context.Context, uint32, uint32) (bool, error) {
			h.mu.Lock()
			defer h.mu.Unlock()

			return claimSubmitted, nil
		})
	h.dstBridge.EXPECT().ClaimAsset(mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, bridgelooptester.ClaimRequest) (*ethtypes.Receipt, error) {
			h.mu.Lock()
			claimSubmitted = true
			h.mu.Unlock()

			return nil, fmt.Errorf("execution reverted: custom error %s: AlreadyClaimed",
				bridgelooptester.AlreadyClaimedSelector)
		}).Once()

	result, err := h.engine().RunHop(context.Background(), h.request(bridgelooptester.ClaimManual))
	require.ErrorIs(t, err, bridgelooptester.ErrClaimModeViolation)
	require.True(t, result.ClaimModeViolated)
	require.Equal(t, bridgelooptester.ClaimActorExternal, result.ClaimedBy)
}

// TestRunHopAutoClaimLosesRaceIsSuccess is the same reconciliation for an auto hop, where losing
// the race to the autoclaim service is precisely the expected outcome.
func TestRunHopAutoClaimLosesRaceIsSuccess(t *testing.T) {
	t.Parallel()

	h := newHopHarness(t, hopDestinationNetwork)
	h.expectBridge()
	h.expectGates()
	h.claimAfter(3)
	h.expectClaimRecord(hopAutoClaimer, hopForeignClaimTx)

	result, err := h.engine().RunHop(context.Background(), h.request(bridgelooptester.ClaimAuto))
	require.NoError(t, err)
	require.Equal(t, bridgelooptester.ClaimActorExternal, result.ClaimedBy)
	require.False(t, result.ClaimModeViolated)
}

// TestRunHopContextCancellation covers a caller asking the hop to stop mid-wait, which must be
// reported as the caller's cancellation and not as a claim-mode violation.
func TestRunHopContextCancellation(t *testing.T) {
	t.Parallel()

	h := newHopHarness(t, hopDestinationNetwork)
	h.deps.Timings = bridgelooptester.HopTimings{
		PollInterval:      time.Millisecond,
		HopTimeout:        time.Minute,
		ManualGracePeriod: 30 * time.Second,
	}
	h.expectBridge()
	h.expectGates()
	h.expectIsClaimed()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	result, err := h.engine().RunHop(ctx, h.request(bridgelooptester.ClaimManual))
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, result.ClaimModeViolated)
	require.Equal(t, bridgelooptester.HopOutcomeFailed, result.Outcome)
}
