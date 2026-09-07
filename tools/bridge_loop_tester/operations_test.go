package bridgelooptester_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"path/filepath"
	"testing"

	bridgeserviceclient "github.com/agglayer/aggkit/bridgeservice/client"
	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	bridgelooptester "github.com/agglayer/aggkit/tools/bridge_loop_tester"
	"github.com/agglayer/aggkit/tools/bridge_loop_tester/mocks"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// erc20Ring builds a closed ERC20 ring 1 -> 2 -> 0 -> 1 with its token originating on network 1.
func erc20Ring(name string, originNetwork uint32) bridgelooptester.Loop {
	origin := originNetwork

	return bridgelooptester.Loop{
		Name:               name,
		Asset:              bridgelooptester.AssetERC20,
		Amount:             bridgelooptester.NewWeiAmount(500),
		Enabled:            true,
		TokenOriginNetwork: &origin,
		Hops: []bridgelooptester.Hop{
			{Source: 1, Destination: 2, Claim: bridgelooptester.ClaimManual},
			{Source: 2, Destination: 0, Claim: bridgelooptester.ClaimAuto},
			{Source: 0, Destination: 1, Claim: bridgelooptester.ClaimManual},
		},
	}
}

func TestPreflightRefusesNetworkZeroWhenTheProxyHasNoL1BridgeService(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	// Replace the network-0 expectation with the Gap G4 fail-fast condition.
	h.proxy = mocks.NewProxy(t)
	h.proxy.EXPECT().Health(mock.Anything).Return(nil, nil).Maybe()
	h.proxy.EXPECT().BridgeAddresses(mock.Anything, uint32(0)).
		Return(nil, fmt.Errorf("wrapped: %w", bridgelooptester.ErrL1BridgeServiceUnavailable)).Maybe()
	for _, networkID := range []uint32{1, 2} {
		contracts := bridgeservicetypes.PublicContractsConfig{}
		contracts.L2.BridgeAddr = bridgeservicetypes.Address(bridgeAddressOf(networkID).Hex())
		h.proxy.EXPECT().BridgeAddresses(mock.Anything, networkID).
			Return(&bridgeservicetypes.PublicConfigResponse{
				NetworkID: networkID,
				Contracts: contracts,
			}, nil).Maybe()
	}

	orchestrator, err := bridgelooptester.NewOrchestrator(
		context.Background(), h.cfg, h.deps(&fakeHopRunner{}, bridgelooptester.NoopStateStore{}))
	require.NoError(t, err)
	defer orchestrator.Close()

	// The ETH ring routes through network 0, so this is a permanent refusal, in run mode too.
	report, err := orchestrator.Preflight(context.Background(), false)
	require.NoError(t, err)
	require.False(t, report.OK())
	require.True(t, report.NetworkZeroParticipates)
	require.False(t, report.L1BridgeServiceAvailable)
	require.Contains(t, report.Err().Error(), "permanent refusal, not something to retry")
}

func TestPreflightWarnsWhenNetworkZeroIsUnusedAndUnavailable(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	// A 2-network ring that never touches network 0.
	h.cfg.Loops = []bridgelooptester.Loop{{
		Name:    "l2-ring",
		Asset:   bridgelooptester.AssetETH,
		Amount:  bridgelooptester.NewWeiAmount(1),
		Enabled: true,
		Hops: []bridgelooptester.Hop{
			{Source: 1, Destination: 2, Claim: bridgelooptester.ClaimManual},
			{Source: 2, Destination: 1, Claim: bridgelooptester.ClaimManual},
		},
	}}

	h.proxy = mocks.NewProxy(t)
	h.proxy.EXPECT().Health(mock.Anything).Return(nil, nil).Maybe()
	h.proxy.EXPECT().BridgeAddresses(mock.Anything, uint32(0)).
		Return(nil, bridgelooptester.ErrL1BridgeServiceUnavailable).Maybe()
	for _, networkID := range []uint32{1, 2} {
		contracts := bridgeservicetypes.PublicContractsConfig{}
		contracts.L2.BridgeAddr = bridgeservicetypes.Address(bridgeAddressOf(networkID).Hex())
		h.proxy.EXPECT().BridgeAddresses(mock.Anything, networkID).
			Return(&bridgeservicetypes.PublicConfigResponse{Contracts: contracts}, nil).Maybe()
	}

	orchestrator, err := bridgelooptester.NewOrchestrator(
		context.Background(), h.cfg, h.deps(&fakeHopRunner{}, bridgelooptester.NoopStateStore{}))
	require.NoError(t, err)
	defer orchestrator.Close()

	report, err := orchestrator.Preflight(context.Background(), false)
	require.NoError(t, err)
	require.False(t, report.NetworkZeroParticipates)
	require.NotEmpty(t, report.Warnings)
	// Network 0 is configured but unused, so the missing L1 bridge service is only a warning, and
	// the network-0 config 404 in strict mode is what makes `validate` complain, not `run`.
	require.True(t, report.OK())
}

// TestPreflightStrictIsStricterThanRunMode pins the documented difference: a signer below its
// MinNativeReserve and a bridge address the proxy disagrees with refuse `validate` but only warn a
// deliberately-started run.
func TestPreflightStrictIsStricterThanRunMode(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.cfg.Networks[1].MinNativeReserve = bridgelooptester.NewWeiAmount(2_000_000_000_000_000_000)

	orchestrator, err := bridgelooptester.NewOrchestrator(
		context.Background(), h.cfg, h.deps(&fakeHopRunner{}, bridgelooptester.NoopStateStore{}))
	require.NoError(t, err)
	defer orchestrator.Close()

	runMode, err := orchestrator.Preflight(context.Background(), false)
	require.NoError(t, err)
	require.True(t, runMode.OK())
	require.Contains(t, fmt.Sprint(runMode.Warnings), "below its configured MinNativeReserve")

	strict, err := orchestrator.Preflight(context.Background(), true)
	require.NoError(t, err)
	require.False(t, strict.OK())
	require.Contains(t, strict.Err().Error(), "below its configured MinNativeReserve")
}

func TestPreflightReportsBridgeAddressDisagreement(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.proxy = mocks.NewProxy(t)
	h.proxy.EXPECT().Health(mock.Anything).Return(nil, nil).Maybe()
	for _, networkID := range []uint32{0, 1, 2} {
		contracts := bridgeservicetypes.PublicContractsConfig{}
		reported := bridgeAddressOf(networkID)
		if networkID == 1 {
			reported = common.HexToAddress("0xdeadbeef") // disagrees with the configured address
		}
		if networkID == 0 {
			contracts.L1.BridgeAddr = bridgeservicetypes.Address(reported.Hex())
		} else {
			contracts.L2.BridgeAddr = bridgeservicetypes.Address(reported.Hex())
		}
		h.proxy.EXPECT().BridgeAddresses(mock.Anything, networkID).
			Return(&bridgeservicetypes.PublicConfigResponse{Contracts: contracts}, nil).Maybe()
	}

	orchestrator, err := bridgelooptester.NewOrchestrator(
		context.Background(), h.cfg, h.deps(&fakeHopRunner{}, bridgelooptester.NoopStateStore{}))
	require.NoError(t, err)
	defer orchestrator.Close()

	strict, err := orchestrator.Preflight(context.Background(), true)
	require.NoError(t, err)
	require.False(t, strict.OK())
	require.Contains(t, strict.Err().Error(), "watching different contracts")
	require.Equal(t, common.HexToAddress("0xdeadbeef"), strict.Network(1).BridgeAddrReported)
}

func TestPreflightRefusesABridgeReportingAnotherNetworkID(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.bridges[1] = mocks.NewBridge(t)
	h.bridges[1].EXPECT().Address().Return(bridgeAddressOf(1)).Maybe()
	h.bridges[1].EXPECT().NetworkID(mock.Anything).Return(uint32(7), nil).Maybe()
	h.bridges[1].EXPECT().GasTokenAddress(mock.Anything).Return(common.Address{}, nil).Maybe()
	h.bridges[1].EXPECT().WETHToken(mock.Anything).Return(common.Address{}, nil).Maybe()

	orchestrator, err := bridgelooptester.NewOrchestrator(
		context.Background(), h.cfg, h.deps(&fakeHopRunner{}, bridgelooptester.NoopStateStore{}))
	require.NoError(t, err)
	defer orchestrator.Close()

	report, err := orchestrator.Preflight(context.Background(), false)
	require.NoError(t, err)
	require.False(t, report.OK())
	require.Contains(t, report.Err().Error(), "reports networkID() = 7")
}

func TestEnsureTokenDeploysMintsAndReusesAcrossRestarts(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.cfg.Loops = []bridgelooptester.Loop{erc20Ring("erc20-ring", 1)}
	h.cfg.Global.Iterations = 1

	tokenAddress := common.HexToAddress("0x7071")
	token := mocks.NewToken(t)
	token.EXPECT().Address().Return(tokenAddress).Maybe()
	// First run: the account holds nothing, so the tool mints mintCycles * Amount.
	token.EXPECT().BalanceOf(mock.Anything, signerAddressOf(1)).Return(big.NewInt(0), nil).Once()
	token.EXPECT().Mint(mock.Anything, signerAddressOf(1), big.NewInt(500*100)).
		Return(&ethtypes.Receipt{}, nil).Once()

	deploys := 0
	store, err := bridgelooptester.NewFileStateStore(filepath.Join(t.TempDir(), "state.json"))
	require.NoError(t, err)

	deps := h.deps(&fakeHopRunner{}, store)
	deps.NewTokenFn = func(bridgelooptester.NetworkClient, common.Address) (bridgelooptester.Token, error) {
		return token, nil
	}
	deps.DeployTokenFn = func(
		context.Context, bridgelooptester.NetworkClient, string, string,
	) (bridgelooptester.Token, error) {
		deploys++

		return token, nil
	}

	orchestrator, err := bridgelooptester.NewOrchestrator(context.Background(), h.cfg, deps)
	require.NoError(t, err)

	report, err := orchestrator.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, deploys)
	require.Equal(t, tokenAddress, report.Loops[0].TokenAddress)
	require.True(t, report.Loops[0].TokenDeployed)
	orchestrator.Close()

	persisted, err := store.Load(context.Background())
	require.NoError(t, err)
	require.Equal(t, tokenAddress, persisted.Token("erc20-ring").Address)
	require.Equal(t, big.NewInt(50_000), persisted.Token("erc20-ring").MintedAmount())

	// Second run over the same state file: the recorded token has code, so it is reused and no
	// second contract is deployed. It already holds enough, so nothing is minted either.
	h.backends[1].EXPECT().CodeAt(mock.Anything, tokenAddress, mock.Anything).
		Return([]byte{0x60, 0x00}, nil).Once()
	token.EXPECT().BalanceOf(mock.Anything, signerAddressOf(1)).Return(big.NewInt(50_000), nil).Once()

	restarted, err := bridgelooptester.NewOrchestrator(context.Background(), h.cfg, deps)
	require.NoError(t, err)
	defer restarted.Close()

	_, err = restarted.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, deploys, "a recorded token with code at its address must be reused, not redeployed")
}

func TestEnsureTokenRedeploysWhenTheRecordedAddressHasNoCode(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.cfg.Loops = []bridgelooptester.Loop{erc20Ring("erc20-ring", 1)}
	h.cfg.Global.Iterations = 1

	tokenAddress := common.HexToAddress("0x7072")
	token := mocks.NewToken(t)
	token.EXPECT().Address().Return(tokenAddress).Maybe()
	token.EXPECT().BalanceOf(mock.Anything, mock.Anything).Return(big.NewInt(1_000_000), nil).Maybe()

	store, err := bridgelooptester.NewFileStateStore(filepath.Join(t.TempDir(), "state.json"))
	require.NoError(t, err)

	seeded := bridgelooptester.NewState()
	seeded.SetToken(&bridgelooptester.TokenState{
		LoopName:      "erc20-ring",
		OriginNetwork: 1,
		Address:       common.HexToAddress("0xc0ffee"),
	})
	require.NoError(t, store.Save(context.Background(), seeded))

	// The recorded address has no code (a state file carried to a reset chain).
	h.backends[1].EXPECT().CodeAt(mock.Anything, common.HexToAddress("0xc0ffee"), mock.Anything).
		Return(nil, nil).Once()

	deploys := 0
	deps := h.deps(&fakeHopRunner{}, store)
	deps.NewTokenFn = func(bridgelooptester.NetworkClient, common.Address) (bridgelooptester.Token, error) {
		return token, nil
	}
	deps.DeployTokenFn = func(
		context.Context, bridgelooptester.NetworkClient, string, string,
	) (bridgelooptester.Token, error) {
		deploys++

		return token, nil
	}

	orchestrator, err := bridgelooptester.NewOrchestrator(context.Background(), h.cfg, deps)
	require.NoError(t, err)
	defer orchestrator.Close()

	_, err = orchestrator.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, deploys)
}

func TestDeployTokenRecordsTheAddressForALoop(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	tokenAddress := common.HexToAddress("0xabcabc")

	token := mocks.NewToken(t)
	token.EXPECT().Address().Return(tokenAddress).Maybe()
	token.EXPECT().Mint(mock.Anything, signerAddressOf(1), big.NewInt(1_234)).
		Return(&ethtypes.Receipt{}, nil).Once()
	token.EXPECT().BalanceOf(mock.Anything, signerAddressOf(1)).Return(big.NewInt(1_234), nil).Once()

	store, err := bridgelooptester.NewFileStateStore(filepath.Join(t.TempDir(), "state.json"))
	require.NoError(t, err)

	deps := h.deps(&fakeHopRunner{}, store)
	deps.DeployTokenFn = func(
		_ context.Context, _ bridgelooptester.NetworkClient, name, symbol string,
	) (bridgelooptester.Token, error) {
		require.Equal(t, "MyToken", name)
		require.Equal(t, "MTK", symbol)

		return token, nil
	}

	orchestrator, err := bridgelooptester.NewOrchestrator(context.Background(), h.cfg, deps)
	require.NoError(t, err)
	defer orchestrator.Close()

	result, err := orchestrator.DeployToken(context.Background(), bridgelooptester.DeployTokenRequest{
		NetworkID: 1,
		Name:      "MyToken",
		Symbol:    "MTK",
		Mint:      big.NewInt(1_234),
		LoopName:  "erc20-ring",
	})
	require.NoError(t, err)
	require.Equal(t, tokenAddress, result.Address)
	require.Equal(t, uint32(1), result.NetworkID)
	require.Equal(t, signerAddressOf(1), result.Owner)
	require.Equal(t, big.NewInt(1_234), result.Minted)
	require.Equal(t, "erc20-ring", result.RecordedForLoop)

	persisted, err := store.Load(context.Background())
	require.NoError(t, err)
	require.Equal(t, tokenAddress, persisted.Token("erc20-ring").Address)
	require.Equal(t, big.NewInt(1_234), persisted.Token("erc20-ring").MintedAmount())
}

func TestDeployTokenRejectsAnUnconfiguredNetwork(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	orchestrator, err := bridgelooptester.NewOrchestrator(
		context.Background(), h.cfg, h.deps(&fakeHopRunner{}, bridgelooptester.NoopStateStore{}))
	require.NoError(t, err)
	defer orchestrator.Close()

	_, err = orchestrator.DeployToken(context.Background(), bridgelooptester.DeployTokenRequest{NetworkID: 9})
	require.Error(t, err)
	require.Contains(t, err.Error(), "network 9 is not configured")
}

func TestClaimReportsAnAlreadyClaimedDepositWithoutSubmitting(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.bridges[2].EXPECT().IsClaimed(mock.Anything, uint32(5), uint32(1)).Return(true, nil).Once()

	orchestrator, err := bridgelooptester.NewOrchestrator(
		context.Background(), h.cfg, h.deps(&fakeHopRunner{}, bridgelooptester.NoopStateStore{}))
	require.NoError(t, err)
	defer orchestrator.Close()

	result, err := orchestrator.Claim(context.Background(), bridgelooptester.RecoveryClaimRequest{
		Source:       1,
		Destination:  2,
		DepositCount: 5,
	})
	require.NoError(t, err)
	require.True(t, result.AlreadyClaimed)
	require.True(t, result.Claimed)
	require.Empty(t, result.ClaimTxHash)
}

func TestClaimSubmitsWithTheActuallyInjectedLeafIndex(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	h.bridges[2].EXPECT().IsClaimed(mock.Anything, uint32(5), uint32(1)).Return(false, nil).Once()
	h.proxy.EXPECT().BridgeByDepositCount(mock.Anything, uint32(1), uint32(5)).
		Return(&bridgeservicetypes.BridgeResponse{
			LeafType:           0,
			OriginNetwork:      1,
			OriginAddress:      bridgeservicetypes.Address(common.HexToAddress("0x11").Hex()),
			DestinationNetwork: 2,
			DestinationAddress: bridgeservicetypes.Address(signerAddressOf(2).Hex()),
			Amount:             bridgeservicetypes.BigIntString("777"),
			Metadata:           "0x",
		}, nil).Once()

	// I = 10, but the leaf actually injected on the destination is I' = 14; the claim proof must
	// be fetched for I', never for I.
	h.proxy.EXPECT().WaitL1InfoTreeIndex(mock.Anything, uint32(1), uint64(5), mock.Anything, mock.Anything).
		Return(uint32(10), nil).Once()
	h.proxy.EXPECT().WaitInjectedLeaf(mock.Anything, uint32(2), uint32(10), mock.Anything, mock.Anything).
		Return(uint32(14), nil).Once()
	h.proxy.EXPECT().
		WaitClaimProof(mock.Anything, uint32(1), uint32(14), uint32(5), mock.Anything, mock.Anything).
		Return(&bridgeservicetypes.ClaimProof{
			L1InfoTreeLeaf: bridgeservicetypes.L1InfoTreeLeafResponse{
				MainnetExitRoot: bridgeservicetypes.Hash(common.HexToHash("0xaa").Hex()),
				RollupExitRoot:  bridgeservicetypes.Hash(common.HexToHash("0xbb").Hex()),
			},
		}, nil).Once()

	h.bridges[2].EXPECT().ClaimAsset(mock.Anything, mock.Anything).
		Run(func(_ context.Context, req bridgelooptester.ClaimRequest) {
			require.Equal(t, big.NewInt(777), req.Amount)
			require.Equal(t, uint32(2), req.DestinationNetwork)
			require.Equal(t, common.HexToHash("0xaa"), req.MainnetExitRoot)
		}).
		Return(&ethtypes.Receipt{
			TxHash:      common.HexToHash("0xc1a1m"),
			GasUsed:     21_000,
			BlockNumber: big.NewInt(99),
		}, nil).Once()
	h.bridges[2].EXPECT().IsClaimed(mock.Anything, uint32(5), uint32(1)).Return(true, nil).Once()

	orchestrator, err := bridgelooptester.NewOrchestrator(
		context.Background(), h.cfg, h.deps(&fakeHopRunner{}, bridgelooptester.NoopStateStore{}))
	require.NoError(t, err)
	defer orchestrator.Close()

	result, err := orchestrator.Claim(context.Background(), bridgelooptester.RecoveryClaimRequest{
		Source:       1,
		Destination:  2,
		DepositCount: 5,
	})
	require.NoError(t, err)
	require.False(t, result.AlreadyClaimed)
	require.True(t, result.Claimed)
	require.Equal(t, uint32(10), result.L1InfoTreeIndex)
	require.Equal(t, uint32(14), result.InjectedLeafIndex)
	require.Equal(t, common.HexToHash("0xc1a1m"), result.ClaimTxHash)
	require.Equal(t, uint64(99), result.ClaimBlockNumber)
	require.Equal(t, big.NewInt(777), result.Amount)
}

func TestClaimRefusesADepositDestinedElsewhere(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.bridges[2].EXPECT().IsClaimed(mock.Anything, uint32(5), uint32(1)).Return(false, nil).Once()
	h.proxy.EXPECT().BridgeByDepositCount(mock.Anything, uint32(1), uint32(5)).
		Return(&bridgeservicetypes.BridgeResponse{DestinationNetwork: 0}, nil).Once()

	orchestrator, err := bridgelooptester.NewOrchestrator(
		context.Background(), h.cfg, h.deps(&fakeHopRunner{}, bridgelooptester.NoopStateStore{}))
	require.NoError(t, err)
	defer orchestrator.Close()

	_, err = orchestrator.Claim(context.Background(), bridgelooptester.RecoveryClaimRequest{
		Source:       1,
		Destination:  2,
		DepositCount: 5,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "destined for network 0")
}

func TestClaimSurfacesAnUnindexedDeposit(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.bridges[2].EXPECT().IsClaimed(mock.Anything, uint32(9), uint32(1)).Return(false, nil).Once()
	h.proxy.EXPECT().BridgeByDepositCount(mock.Anything, uint32(1), uint32(9)).
		Return(nil, bridgeserviceclient.ErrNotFound).Once()

	orchestrator, err := bridgelooptester.NewOrchestrator(
		context.Background(), h.cfg, h.deps(&fakeHopRunner{}, bridgelooptester.NoopStateStore{}))
	require.NoError(t, err)
	defer orchestrator.Close()

	_, err = orchestrator.Claim(context.Background(), bridgelooptester.RecoveryClaimRequest{
		Source:       1,
		Destination:  2,
		DepositCount: 9,
	})
	require.Error(t, err)
	require.ErrorIs(t, err, bridgeserviceclient.ErrNotFound)
}

func TestStatusReportsAStrandedRingOffline(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	statePath := filepath.Join(t.TempDir(), "state.json")
	h.cfg.Global.StatePath = statePath

	store, err := bridgelooptester.NewFileStateStore(statePath)
	require.NoError(t, err)

	state := bridgelooptester.NewState()
	record := state.Loop("eth-ring")
	record.HopIndex = 1
	record.ValueNetwork = 1
	record.CyclesCompleted = 6
	record.CyclesAttempted = 7
	record.Halted = true
	record.HaltClass = bridgelooptester.FailureClaimMode
	record.LastError = "the manual hop was claimed by someone else"
	record.InFlight = &bridgelooptester.HopCheckpoint{
		State:        bridgelooptester.HopStateAwaitingClaim,
		BridgeTxHash: common.HexToHash("0xb1"),
	}
	require.NoError(t, store.Save(context.Background(), state))

	// Status must not need a chain or a proxy: it is given no store and resolves the file itself.
	report, err := bridgelooptester.Status(context.Background(), h.cfg, nil)
	require.NoError(t, err)
	require.True(t, report.Exists)
	require.Equal(t, statePath, report.StatePath)
	require.Len(t, report.Loops, 1)

	loop := report.Loops[0]
	require.Equal(t, "eth-ring", loop.Name)
	require.Equal(t, uint64(6), loop.CyclesCompleted)
	require.Equal(t, uint64(7), loop.CyclesAttempted)
	require.True(t, loop.Halted)
	require.Equal(t, bridgelooptester.FailureClaimMode, loop.HaltClass)
	require.Equal(t, bridgelooptester.HopStateAwaitingClaim, loop.InFlightState)
	require.Equal(t, common.HexToHash("0xb1"), loop.InFlightBridgeTx)
	require.True(t, loop.ValueLocation.Stranded)
	require.True(t, loop.ValueLocation.InFlight)
	require.Equal(t, uint32(1), loop.ValueLocation.NetworkID)
	require.Contains(t, loop.ValueLocation.Detail, "STRANDED in flight")
	require.Contains(t, loop.ValueLocation.Detail, "L2B", "the detail names the network the deposit is "+
		"claimable on, so an operator knows where to look")
}

func TestStatusReportsAHealthyRingAtRest(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.cfg.Global.StatePath = filepath.Join(t.TempDir(), "state.json")

	store, err := bridgelooptester.NewFileStateStore(h.cfg.Global.StatePath)
	require.NoError(t, err)

	state := bridgelooptester.NewState()
	state.Loop("eth-ring").CyclesCompleted = 12
	require.NoError(t, store.Save(context.Background(), state))

	report, err := bridgelooptester.Status(context.Background(), h.cfg, nil)
	require.NoError(t, err)
	require.False(t, report.Loops[0].ValueLocation.Stranded)
	require.Contains(t, report.Loops[0].ValueLocation.Detail, "the ring is closed")
}

func TestStatusWithNoPersistedState(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	report, err := bridgelooptester.Status(context.Background(), h.cfg, nil)
	require.NoError(t, err)
	require.False(t, report.Exists)
	require.Empty(t, report.StatePath)
	require.Len(t, report.Loops, 1)
	require.False(t, report.Loops[0].ValueLocation.Stranded)

	_, err = bridgelooptester.Status(context.Background(), nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "config is required")
}

func TestReportSerialisesAndAggregates(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.cfg.Global.Iterations = 2

	runner := &fakeHopRunner{}
	orchestrator, err := bridgelooptester.NewOrchestrator(
		context.Background(), h.cfg, h.deps(runner, bridgelooptester.NoopStateStore{}))
	require.NoError(t, err)
	defer orchestrator.Close()

	report, err := orchestrator.Run(context.Background())
	require.NoError(t, err)

	// Accessors S10 builds on.
	require.NotNil(t, report.Loop("eth-ring"))
	require.Nil(t, report.Loop("nope"))
	require.NotNil(t, report.Loop("eth-ring").Cycle(2))
	require.Nil(t, report.Loop("eth-ring").Cycle(99))
	require.Len(t, report.Loop("eth-ring").AllHops(), 6)
	require.Contains(t, report.Summary(), "hops=6")
	require.Len(t, report.AllHops(), 6)
	require.Positive(t, report.Duration)

	// The whole report round-trips through JSON, so an e2e run can be archived verbatim.
	encoded, err := jsonRoundTrip(report)
	require.NoError(t, err)
	require.Equal(t, report.Totals.HopsSucceeded, encoded.Totals.HopsSucceeded)
	require.Equal(t, report.Totals.InjectedLeafAdvanced, encoded.Totals.InjectedLeafAdvanced)
	require.Len(t, encoded.Loops, 1)
	require.Len(t, encoded.Loops[0].Cycles, 2)
	require.True(t, encoded.Loops[0].Cycles[0].Hops[0].InjectedLeafAdvanced)
	require.Equal(t, bridgelooptester.ClaimActorExternal, encoded.Loops[0].Cycles[0].Hops[0].ClaimedBy)
	require.Len(t, encoded.Loops[0].Cycles[0].Hops[0].Phases, 2)
}

// jsonRoundTrip encodes and decodes a Report, so a test can assert the whole record survives being
// written to disk or handed to another process.
func jsonRoundTrip(report *bridgelooptester.Report) (*bridgelooptester.Report, error) {
	encoded, err := json.Marshal(report)
	if err != nil {
		return nil, err
	}

	var decoded bridgelooptester.Report
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, err
	}

	return &decoded, nil
}
