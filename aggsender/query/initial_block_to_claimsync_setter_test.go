package query

import (
	"context"
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"

	agglayermocks "github.com/agglayer/aggkit/agglayer/mocks"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/metrics"
	"github.com/agglayer/aggkit/aggsender/mocks"
	aggsendertypes "github.com/agglayer/aggkit/aggsender/types"
	claimsynctypesmocks "github.com/agglayer/aggkit/claimsync/types/mocks"
	aggkitcommon "github.com/agglayer/aggkit/common"
	configtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/log"
	aggkitprometheus "github.com/agglayer/aggkit/prometheus"
	aggkittypes "github.com/agglayer/aggkit/types"
	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
)

func newTestSetter(t *testing.T) (
	*SetInitialBlockToClaimSyncer,
	*mocks.CertificateQuerier,
	*agglayermocks.AgglayerClientMock,
) {
	t.Helper()
	certQuerier := mocks.NewCertificateQuerier(t)
	agglayerClient := agglayermocks.NewAgglayerClientMock(t)
	logger := log.WithFields("module", "test")
	setter := NewSetInitialBlockToClaimSyncer(certQuerier, agglayerClient, uint32(1), logger)
	return setter, certQuerier, agglayerClient
}

// noRetryHandler executes exactly once with no sleep.
func noRetryHandler() *aggkitcommon.RetryHandlerDelays {
	return aggkitcommon.NewRetryHandler(nil, 0)
}

func TestSetClaimSyncerNextRequiredBlock_NilClaimSyncer(t *testing.T) {
	t.Parallel()
	setter, _, _ := newTestSetter(t)

	err := setter.SetClaimSyncerNextRequiredBlock(t.Context(), nil, noRetryHandler())
	require.NoError(t, err)
}

func TestSetClaimSyncerNextRequiredBlock_AlreadyHasProcessedBlocks(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	setter, _, _ := newTestSetter(t)

	claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
	claimSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(100), true, nil)

	err := setter.SetClaimSyncerNextRequiredBlock(ctx, claimSyncer, noRetryHandler())
	require.NoError(t, err)
}

func TestSetClaimSyncerNextRequiredBlock_GetLastProcessedBlockError(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	setter, _, _ := newTestSetter(t)

	claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
	claimSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), false, errors.New("storage error"))

	err := setter.SetClaimSyncerNextRequiredBlock(ctx, claimSyncer, noRetryHandler())
	require.ErrorContains(t, err, "storage error")
}

func TestSetClaimSyncerNextRequiredBlock_Success(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	setter, certQuerier, agglayerClient := newTestSetter(t)

	certHeader := &agglayertypes.CertificateHeader{}
	claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
	claimSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), false, nil)
	agglayerClient.EXPECT().GetLatestSettledCertificateHeader(ctx, uint32(1)).Return(certHeader, nil)
	certQuerier.EXPECT().GetSettledBlocksFromCertHeader(ctx, certHeader).
		Return(aggsendertypes.SettledBlocks{LastBridgeExitBlock: 42, LastImportedBridgeExitBlock: 42})
	claimSyncer.EXPECT().SetNextRequiredBlock(ctx, uint64(42)).Return(nil)

	err := setter.SetClaimSyncerNextRequiredBlock(ctx, claimSyncer, noRetryHandler())
	require.NoError(t, err)
}

func TestSetClaimSyncerNextRequiredBlock_GetLatestSettledCertHeaderError(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	setter, _, agglayerClient := newTestSetter(t)

	claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
	claimSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), false, nil)
	agglayerClient.EXPECT().GetLatestSettledCertificateHeader(ctx, uint32(1)).
		Return(nil, errors.New("agglayer unavailable"))

	err := setter.SetClaimSyncerNextRequiredBlock(ctx, claimSyncer, noRetryHandler())
	require.ErrorIs(t, err, aggkitcommon.ErrExecutionFails)
	require.ErrorContains(t, err, "agglayer unavailable")
}

func TestSetClaimSyncerNextRequiredBlock_GetLastSettledCertToBlockError(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	setter, certQuerier, agglayerClient := newTestSetter(t)

	certHeader := &agglayertypes.CertificateHeader{}
	claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
	claimSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), false, nil)
	agglayerClient.EXPECT().GetLatestSettledCertificateHeader(ctx, uint32(1)).Return(certHeader, nil)
	certQuerier.EXPECT().GetSettledBlocksFromCertHeader(ctx, certHeader).
		Return(aggsendertypes.SettledBlocks{LastBridgeExitBlockErr: errors.New("db error")})

	err := setter.SetClaimSyncerNextRequiredBlock(ctx, claimSyncer, noRetryHandler())
	require.ErrorIs(t, err, aggkitcommon.ErrExecutionFails)
	require.ErrorContains(t, err, "db error")
}

func TestSetClaimSyncerNextRequiredBlock_SetNextRequiredBlockError(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	setter, certQuerier, agglayerClient := newTestSetter(t)

	certHeader := &agglayertypes.CertificateHeader{}
	claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
	claimSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), false, nil)
	agglayerClient.EXPECT().GetLatestSettledCertificateHeader(ctx, uint32(1)).Return(certHeader, nil)
	certQuerier.EXPECT().GetSettledBlocksFromCertHeader(ctx, certHeader).
		Return(aggsendertypes.SettledBlocks{LastBridgeExitBlock: 10, LastImportedBridgeExitBlock: 10})
	claimSyncer.EXPECT().SetNextRequiredBlock(ctx, uint64(10)).Return(errors.New("syncer error"))

	err := setter.SetClaimSyncerNextRequiredBlock(ctx, claimSyncer, noRetryHandler())
	require.ErrorIs(t, err, aggkitcommon.ErrExecutionFails)
	require.ErrorContains(t, err, "syncer error")
}

func TestSetClaimSyncerNextRequiredBlock_NilCertFromAgglayer(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	setter, certQuerier, agglayerClient := newTestSetter(t)

	claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
	claimSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), false, nil)
	agglayerClient.EXPECT().GetLatestSettledCertificateHeader(ctx, uint32(1)).Return(nil, nil)
	// nil cert means no settled certificate yet; GetSettledBlocksFromCertHeader is still called
	// with nil and returns only the FEP start block (bridge/import queries are skipped).
	certQuerier.EXPECT().GetSettledBlocksFromCertHeader(ctx, (*agglayertypes.CertificateHeader)(nil)).
		Return(aggsendertypes.SettledBlocks{LastBridgeExitBlock: 10, LastImportedBridgeExitBlock: 10})
	claimSyncer.EXPECT().SetNextRequiredBlock(ctx, uint64(10)).Return(nil)

	err := setter.SetClaimSyncerNextRequiredBlock(ctx, claimSyncer, noRetryHandler())
	require.NoError(t, err)
}

// TestSetClaimSyncerNextRequiredBlock_RPCFallback covers the two possible outcomes of the RPC
// log-scan fallback for a settled imported bridge exit (IBE) that could not be resolved from the
// local claim DB: the scan finds the claim (Success), or the scan completes over its whole range
// without finding a match (NotFound), in which case the setter falls back immediately to block 0
// (no SettledIBELowerBounder configured in this test) rather than retrying or erroring out.
func TestSetClaimSyncerNextRequiredBlock_RPCFallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		rpcBlock          uint64
		rpcFound          bool
		expectedNextBlock uint64
	}{
		{
			name:              "Success",
			rpcBlock:          80,
			rpcFound:          true,
			expectedNextBlock: 80, // earliest = min(LastBridgeExitBlock=100, LastImportedBridgeExitBlock=80)
		},
		{
			name:              "NotFound",
			rpcBlock:          0,
			rpcFound:          false,
			expectedNextBlock: 0, // no SettledIBELowerBounder configured, fallback resolves to 0
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			setter, certQuerier, agglayerClient := newTestSetter(t)

			globalIndex := big.NewInt(7)
			settledIBE := &agglayertypes.SettledImportedBridgeExit{GlobalIndex: globalIndex}
			certHeader := &agglayertypes.CertificateHeader{}
			claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
			claimSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), false, nil)
			agglayerClient.EXPECT().GetLatestSettledCertificateHeader(ctx, uint32(1)).Return(certHeader, nil)
			certQuerier.EXPECT().GetSettledBlocksFromCertHeader(ctx, certHeader).Return(aggsendertypes.SettledBlocks{
				LastBridgeExitBlock:            100,
				LastImportedBridgeExitBlockErr: errors.New("claim not in db"),
				SettledImportedBridgeExit:      settledIBE,
			})
			claimSyncer.EXPECT().
				GetLatestBlockNumByGlobalIndexFromRPC(ctx, globalIndex, (*aggkittypes.BlockNumberFinality)(nil)).
				Return(tt.rpcBlock, tt.rpcFound, nil)
			claimSyncer.EXPECT().SetNextRequiredBlock(ctx, tt.expectedNextBlock).Return(nil)

			err := setter.SetClaimSyncerNextRequiredBlock(ctx, claimSyncer, noRetryHandler())
			require.NoError(t, err)
		})
	}
}

// TestSetClaimSyncerNextRequiredBlock_RPCFallback_Error verifies that when the RPC log scan
// itself fails to complete (found=false, err!=nil), the setter returns the error -- so the
// caller's retry loop retries -- for as long as the consecutive-failure counter stays below
// maxIBERPCLookupFailures, and only falls back to a lower bound once that limit is reached.
func TestSetClaimSyncerNextRequiredBlock_RPCFallback_Error(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	setter, certQuerier, agglayerClient := newTestSetter(t)

	globalIndex := big.NewInt(7)
	settledIBE := &agglayertypes.SettledImportedBridgeExit{GlobalIndex: globalIndex}
	certHeader := &agglayertypes.CertificateHeader{}
	claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
	claimSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), false, nil)
	agglayerClient.EXPECT().GetLatestSettledCertificateHeader(ctx, uint32(1)).Return(certHeader, nil)
	certQuerier.EXPECT().GetSettledBlocksFromCertHeader(ctx, certHeader).Return(aggsendertypes.SettledBlocks{
		LastBridgeExitBlock:            100,
		LastImportedBridgeExitBlockErr: errors.New("claim not in db"),
		SettledImportedBridgeExit:      settledIBE,
	})
	// Every RPC attempt fails to complete. maxIBERPCLookupFailures is 5, so attempts 1-4 must
	// return the error (letting the retry loop retry) and attempt 5 must fall back.
	claimSyncer.EXPECT().GetLatestBlockNumByGlobalIndexFromRPC(ctx, globalIndex, (*aggkittypes.BlockNumberFinality)(nil)).
		Return(uint64(0), false, errors.New("rpc error"))
	// No SettledIBELowerBounder is configured, so the fallback resolves to block 0.
	claimSyncer.EXPECT().SetNextRequiredBlock(ctx, uint64(0)).Return(nil)

	// 5 attempts total: attempts 0-3 fail with the RPC error, attempt 4 (the 5th) falls back.
	retryHandler := aggkitcommon.NewRetryHandler([]configtypes.Duration{{Duration: time.Millisecond}}, 4)
	err := setter.SetClaimSyncerNextRequiredBlock(ctx, claimSyncer, retryHandler)
	require.NoError(t, err)
}

// fakeSettledIBELowerBounder is a hand-written test double for SettledIBELowerBounder.
// .mockery.yaml does not cover aggsender/query, so there is no generated mock for this interface.
type fakeSettledIBELowerBounder struct {
	bound uint64
	found bool
	err   error
}

func (f *fakeSettledIBELowerBounder) LowerBoundForSettledIBE(
	_ context.Context,
	_ *agglayertypes.SettledImportedBridgeExit,
) (uint64, bool, error) {
	return f.bound, f.found, f.err
}

// TestSetClaimSyncerNextRequiredBlock_RPCFallback_UsesStorageLowerBound verifies that once both
// the local claim DB and the RPC log-scan fallback fail to resolve the settled IBE's block, an
// installed SettledIBELowerBounder that reports a bound is used as the claim syncer's starting
// block instead of the safe-but-wide default of 0.
func TestSetClaimSyncerNextRequiredBlock_RPCFallback_UsesStorageLowerBound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	certQuerier := mocks.NewCertificateQuerier(t)
	agglayerClient := agglayermocks.NewAgglayerClientMock(t)
	logger := log.WithFields("module", "test")
	bounder := &fakeSettledIBELowerBounder{bound: 55, found: true}
	setter := NewSetInitialBlockToClaimSyncer(certQuerier, agglayerClient, uint32(1), logger,
		WithSettledIBELowerBounder(bounder))

	globalIndex := big.NewInt(7)
	settledIBE := &agglayertypes.SettledImportedBridgeExit{GlobalIndex: globalIndex}
	certHeader := &agglayertypes.CertificateHeader{}
	claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
	claimSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), false, nil)
	agglayerClient.EXPECT().GetLatestSettledCertificateHeader(ctx, uint32(1)).Return(certHeader, nil)
	certQuerier.EXPECT().GetSettledBlocksFromCertHeader(ctx, certHeader).Return(aggsendertypes.SettledBlocks{
		LastBridgeExitBlock:            100,
		LastImportedBridgeExitBlockErr: errors.New("claim not in db"),
		SettledImportedBridgeExit:      settledIBE,
	})
	// RPC scan completes but finds no match, so the setter falls back to settledIBELowerBounder.
	claimSyncer.EXPECT().
		GetLatestBlockNumByGlobalIndexFromRPC(ctx, globalIndex, (*aggkittypes.BlockNumberFinality)(nil)).
		Return(uint64(0), false, nil)
	// earliest = min(LastBridgeExitBlock=100, LastImportedBridgeExitBlock=55 from the bounder)
	claimSyncer.EXPECT().SetNextRequiredBlock(ctx, uint64(55)).Return(nil)

	err := setter.SetClaimSyncerNextRequiredBlock(ctx, claimSyncer, noRetryHandler())
	require.NoError(t, err)
}

// TestSetClaimSyncerNextRequiredBlock_RPCFallback_UsesStorageLowerBound_ThreeWayMin extends
// TestSetClaimSyncerNextRequiredBlock_RPCFallback_UsesStorageLowerBound (which only exercises two
// of the three SettledBlocks sources) to prove EarliestBlock genuinely takes the minimum across
// all three: LastBridgeExitBlock, the bounder's value (standing in for LastImportedBridgeExitBlock),
// and LastSettledL2BlockNum, with the true minimum coming from neither of the other two tests'
// covered sources.
func TestSetClaimSyncerNextRequiredBlock_RPCFallback_UsesStorageLowerBound_ThreeWayMin(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	certQuerier := mocks.NewCertificateQuerier(t)
	agglayerClient := agglayermocks.NewAgglayerClientMock(t)
	logger := log.WithFields("module", "test")
	bounder := &fakeSettledIBELowerBounder{bound: 70, found: true}
	setter := NewSetInitialBlockToClaimSyncer(certQuerier, agglayerClient, uint32(1), logger,
		WithSettledIBELowerBounder(bounder))

	globalIndex := big.NewInt(17)
	settledIBE := &agglayertypes.SettledImportedBridgeExit{GlobalIndex: globalIndex}
	certHeader := &agglayertypes.CertificateHeader{}
	claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
	claimSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), false, nil)
	agglayerClient.EXPECT().GetLatestSettledCertificateHeader(ctx, uint32(1)).Return(certHeader, nil)
	certQuerier.EXPECT().GetSettledBlocksFromCertHeader(ctx, certHeader).Return(aggsendertypes.SettledBlocks{
		LastBridgeExitBlock:            100,
		LastSettledL2BlockNum:          50,
		LastImportedBridgeExitBlockErr: errors.New("claim not in db"),
		SettledImportedBridgeExit:      settledIBE,
	})
	claimSyncer.EXPECT().
		GetLatestBlockNumByGlobalIndexFromRPC(ctx, globalIndex, (*aggkittypes.BlockNumberFinality)(nil)).
		Return(uint64(0), false, nil)
	// earliest = min(LastBridgeExitBlock=100, LastImportedBridgeExitBlock=70 from the bounder,
	// LastSettledL2BlockNum=50) = 50: the true minimum comes from the source neither of the other
	// two RPCFallback tests set to a non-default value.
	claimSyncer.EXPECT().SetNextRequiredBlock(ctx, uint64(50)).Return(nil)

	err := setter.SetClaimSyncerNextRequiredBlock(ctx, claimSyncer, noRetryHandler())
	require.NoError(t, err)
}

// TestSetClaimSyncerNextRequiredBlock_BridgeExitBlockErr_NoFallback documents that an error on
// LastBridgeExitBlock (bridgesync lag, as seen in production incident bali-84-op, where it failed
// 33 times then succeeded on try 34) is deliberately NOT given a fallback: the setter must keep
// returning the error so the caller's retry loop retries forever, and must not touch the IBE
// fallback machinery at all. claimSyncer here has no RPC-lookup or SetNextRequiredBlock
// expectations configured, so if the setter incorrectly invoked either of them (e.g. by treating
// this like an IBE failure), the mock would panic on the unexpected call rather than silently
// permit it -- which is what proves "no fallback" without needing a metrics assertion (the
// fallback is the only caller of the metric).
func TestSetClaimSyncerNextRequiredBlock_BridgeExitBlockErr_NoFallback(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	setter, certQuerier, agglayerClient := newTestSetter(t)

	certHeader := &agglayertypes.CertificateHeader{}
	claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
	claimSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), false, nil)
	agglayerClient.EXPECT().GetLatestSettledCertificateHeader(ctx, uint32(1)).Return(certHeader, nil)
	certQuerier.EXPECT().GetSettledBlocksFromCertHeader(ctx, certHeader).Return(aggsendertypes.SettledBlocks{
		LastBridgeExitBlockErr: errors.New("bridgesync lag"),
	})

	err := setter.SetClaimSyncerNextRequiredBlock(ctx, claimSyncer, noRetryHandler())
	require.ErrorIs(t, err, aggkitcommon.ErrExecutionFails)
	require.ErrorContains(t, err, "bridgesync lag")
}

// TestSetClaimSyncerNextRequiredBlock_NotStuckWithInfiniteRetries is the core regression test for
// issue #1842: with a real, infinite-retry retry handler (as aggsender configures in production)
// and an RPC log-scan that completes with no match, SetClaimSyncerNextRequiredBlock must return
// promptly via the immediate fallback rather than looping "try N/INFINITE" forever, which is
// exactly what was observed in production (namespace bali-82-op) before this fix. The context
// timeout bounds the test's worst case instead of letting a regression hang the suite: if the
// fallback were removed, every attempt would keep failing until ctx expires, and the elapsed-time
// assertion below would fail well before that (see the RED-phase evidence in the review notes).
func TestSetClaimSyncerNextRequiredBlock_NotStuckWithInfiniteRetries(t *testing.T) {
	t.Parallel()

	const testTimeout = 300 * time.Millisecond
	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	setter, certQuerier, agglayerClient := newTestSetter(t)

	globalIndex := big.NewInt(19)
	settledIBE := &agglayertypes.SettledImportedBridgeExit{GlobalIndex: globalIndex}
	certHeader := &agglayertypes.CertificateHeader{}
	claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
	claimSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), false, nil)
	agglayerClient.EXPECT().GetLatestSettledCertificateHeader(ctx, uint32(1)).Return(certHeader, nil)
	certQuerier.EXPECT().GetSettledBlocksFromCertHeader(ctx, certHeader).Return(aggsendertypes.SettledBlocks{
		LastBridgeExitBlock:            100,
		LastImportedBridgeExitBlockErr: errors.New("claim not in db"),
		SettledImportedBridgeExit:      settledIBE,
	})
	claimSyncer.EXPECT().
		GetLatestBlockNumByGlobalIndexFromRPC(ctx, globalIndex, (*aggkittypes.BlockNumberFinality)(nil)).
		Return(uint64(0), false, nil)
	claimSyncer.EXPECT().SetNextRequiredBlock(ctx, uint64(0)).Return(nil)

	retryHandler := aggkitcommon.NewRetryHandler(
		[]configtypes.Duration{{Duration: 5 * time.Millisecond}}, aggkitcommon.MaxAttemptsInfinite)

	start := time.Now()
	err := setter.SetClaimSyncerNextRequiredBlock(ctx, claimSyncer, retryHandler)
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.Less(t, elapsed, testTimeout/2,
		"setter must resolve on the first attempt via the immediate fallback, not retry until ctx times out")
}

// TestClaimSyncerStartingBlockBasedOnLatestSettledCert_CounterResetsAfterSuccess is a white-box
// test (same package) of the ibeRPCLookupFailures consecutive-failure counter: it must reset to 0
// after a successful RPC lookup, so a transient run of RPC errors doesn't eat into a later,
// unrelated run's retry budget. It calls the unexported per-attempt method directly, once per
// scripted RPC outcome, to observe the counter deterministically across attempts without needing
// to drive a whole retry loop.
func TestClaimSyncerStartingBlockBasedOnLatestSettledCert_CounterResetsAfterSuccess(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	setter, certQuerier, _ := newTestSetter(t)

	globalIndex := big.NewInt(23)
	settledIBE := &agglayertypes.SettledImportedBridgeExit{GlobalIndex: globalIndex}
	certHeader := &agglayertypes.CertificateHeader{}
	blocksTemplate := aggsendertypes.SettledBlocks{
		LastBridgeExitBlock:            1000,
		LastImportedBridgeExitBlockErr: errors.New("claim not in db"),
		SettledImportedBridgeExit:      settledIBE,
	}
	certQuerier.EXPECT().GetSettledBlocksFromCertHeader(ctx, certHeader).Return(blocksTemplate)

	rpcErr := errors.New("rpc error")
	type scriptedResult struct {
		block uint64
		found bool
		err   error
	}
	// Calls 1-2: two consecutive RPC errors (counter -> 1, 2). Call 3: a success, which must reset
	// the counter to 0. Calls 4-7: four more consecutive RPC errors, which must count 1, 2, 3, 4 --
	// a fresh full budget -- rather than resuming from the pre-reset count of 2.
	script := []scriptedResult{
		{0, false, rpcErr},
		{0, false, rpcErr},
		{80, true, nil},
		{0, false, rpcErr},
		{0, false, rpcErr},
		{0, false, rpcErr},
		{0, false, rpcErr},
	}
	call := 0
	claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
	claimSyncer.EXPECT().
		GetLatestBlockNumByGlobalIndexFromRPC(ctx, globalIndex, (*aggkittypes.BlockNumberFinality)(nil)).
		RunAndReturn(func(context.Context, *big.Int, *aggkittypes.BlockNumberFinality) (uint64, bool, error) {
			r := script[call]
			call++
			return r.block, r.found, r.err
		})

	for i, r := range script {
		block, _, err := setter.claimSyncerStartingBlockBasedOnLatestSettledCert(ctx, claimSyncer, certHeader)
		if r.err != nil {
			require.Errorf(t, err, "call %d expected an error", i+1)
		} else {
			require.NoErrorf(t, err, "call %d expected success", i+1)
			require.Equalf(t, uint64(80), block, "call %d: earliest = min(LastBridgeExitBlock=1000, "+
				"LastImportedBridgeExitBlock=80 from RPC)", i+1)
		}
	}

	require.Equal(t, 4, setter.ibeRPCLookupFailures,
		"after the reset at call 3, calls 4-7 must count as a fresh run of consecutive failures (4), "+
			"not continue from the pre-reset count of 2")
}

// claimSyncerStartBlockFallbackMetric is the internal registration name (before the "aggsender"
// namespace prefix) of the aggsender_claim_syncer_start_block_fallback_total counter vec. It must
// match the private claimSyncerStartBlockFallback constant in aggsender/metrics; there is no
// exported seam to read it back other than through the real prometheus registry (see
// bridgeservice/bridge_test.go's requestMetricCount for the same pattern).
const claimSyncerStartBlockFallbackMetric = "claim_syncer_start_block_fallback_total"

var registerClaimSyncerMetricsOnce sync.Once

// claimSyncerFallbackCount returns the current value of the
// aggsender_claim_syncer_start_block_fallback_total counter for the given reason label,
// registering the real prometheus collectors on first use.
func claimSyncerFallbackCount(t *testing.T, reason string) float64 {
	t.Helper()
	registerClaimSyncerMetricsOnce.Do(func() {
		aggkitprometheus.Init()
		metrics.Register()
	})
	counter, exists := aggkitprometheus.CounterVec(claimSyncerStartBlockFallbackMetric)
	require.True(t, exists, "claim syncer start block fallback counter is not registered")
	return promtestutil.ToFloat64(counter.WithLabelValues(reason))
}

// TestClaimSyncerStartBlockFallback_MetricByReason asserts that the fallback increments the
// aggsender_claim_syncer_start_block_fallback_total counter under the correct reason label for
// each of the two fallback triggers: RPC-not-found and RPC-lookup-exhausted.
//
// Deliberately NOT t.Parallel(): it reads back the real, process-global prometheus registry. Every
// other test in this file that triggers a fallback is a t.Parallel() test whose body (the part
// that actually calls the production fallback code) only runs during the package's later
// concurrent phase, once every top-level test has either completed or called t.Parallel(). Because
// this test stays serial, it is guaranteed to run to completion before that concurrent phase
// begins, so no other test's fallback call can land between this test's before/after reads.
func TestClaimSyncerStartBlockFallback_MetricByReason(t *testing.T) {
	ctx := t.Context()

	t.Run("NotFound", func(t *testing.T) {
		before := claimSyncerFallbackCount(t, metrics.ReasonIBENotFoundOnRPC)

		setter, certQuerier, agglayerClient := newTestSetter(t)
		globalIndex := big.NewInt(29)
		settledIBE := &agglayertypes.SettledImportedBridgeExit{GlobalIndex: globalIndex}
		certHeader := &agglayertypes.CertificateHeader{}
		claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
		claimSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), false, nil)
		agglayerClient.EXPECT().GetLatestSettledCertificateHeader(ctx, uint32(1)).Return(certHeader, nil)
		certQuerier.EXPECT().GetSettledBlocksFromCertHeader(ctx, certHeader).Return(aggsendertypes.SettledBlocks{
			LastBridgeExitBlock:            100,
			LastImportedBridgeExitBlockErr: errors.New("claim not in db"),
			SettledImportedBridgeExit:      settledIBE,
		})
		claimSyncer.EXPECT().
			GetLatestBlockNumByGlobalIndexFromRPC(ctx, globalIndex, (*aggkittypes.BlockNumberFinality)(nil)).
			Return(uint64(0), false, nil)
		claimSyncer.EXPECT().SetNextRequiredBlock(ctx, uint64(0)).Return(nil)

		err := setter.SetClaimSyncerNextRequiredBlock(ctx, claimSyncer, noRetryHandler())
		require.NoError(t, err)

		require.Equal(t, before+1, claimSyncerFallbackCount(t, metrics.ReasonIBENotFoundOnRPC))
	})

	t.Run("RPCLookupExhausted", func(t *testing.T) {
		before := claimSyncerFallbackCount(t, metrics.ReasonIBERPCLookupExhausted)

		setter, certQuerier, agglayerClient := newTestSetter(t)
		globalIndex := big.NewInt(31)
		settledIBE := &agglayertypes.SettledImportedBridgeExit{GlobalIndex: globalIndex}
		certHeader := &agglayertypes.CertificateHeader{}
		claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
		claimSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), false, nil)
		agglayerClient.EXPECT().GetLatestSettledCertificateHeader(ctx, uint32(1)).Return(certHeader, nil)
		certQuerier.EXPECT().GetSettledBlocksFromCertHeader(ctx, certHeader).Return(aggsendertypes.SettledBlocks{
			LastBridgeExitBlock:            100,
			LastImportedBridgeExitBlockErr: errors.New("claim not in db"),
			SettledImportedBridgeExit:      settledIBE,
		})
		claimSyncer.EXPECT().
			GetLatestBlockNumByGlobalIndexFromRPC(ctx, globalIndex, (*aggkittypes.BlockNumberFinality)(nil)).
			Return(uint64(0), false, errors.New("rpc error"))
		claimSyncer.EXPECT().SetNextRequiredBlock(ctx, uint64(0)).Return(nil)

		retryHandler := aggkitcommon.NewRetryHandler([]configtypes.Duration{{Duration: time.Millisecond}}, 4)
		err := setter.SetClaimSyncerNextRequiredBlock(ctx, claimSyncer, retryHandler)
		require.NoError(t, err)

		require.Equal(t, before+1, claimSyncerFallbackCount(t, metrics.ReasonIBERPCLookupExhausted))
	})
}

// TestSetClaimSyncerNextRequiredBlock_IBEFallbackWithBridgeExitErr pins down the gated behaviour
// of the bali-84-op incident shape: LastBridgeExitBlock erroring (bridgesync lag) *and* the
// settled IBE requiring the RPC fallback at the same time. Because the IBE fallback is now gated
// on LastBridgeExitBlockErr == nil, it must NOT run at all while the bridgesync lag persists: no
// RPC lookup is issued, no WARN/metric fires, and the overall call still returns an error and
// keeps retrying (LastBridgeExitBlockErr is never given a fallback of its own -- see
// fallbackSettledIBEBlock's doc comment). This also means the noise that used to fire once per
// retry attempt (a WARN, a metric increment, and a full chunked RPC scan) is eliminated entirely
// for the duration of the lag, rather than merely reduced.
//
// Deliberately NOT t.Parallel(); see TestClaimSyncerStartBlockFallback_MetricByReason's doc
// comment for why that is required when reading the real prometheus registry.
func TestSetClaimSyncerNextRequiredBlock_IBEFallbackWithBridgeExitErr(t *testing.T) {
	ctx := t.Context()
	setter, certQuerier, agglayerClient := newTestSetter(t)

	before := claimSyncerFallbackCount(t, metrics.ReasonIBENotFoundOnRPC)

	globalIndex := big.NewInt(37)
	settledIBE := &agglayertypes.SettledImportedBridgeExit{GlobalIndex: globalIndex}
	certHeader := &agglayertypes.CertificateHeader{}
	claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
	claimSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), false, nil)
	agglayerClient.EXPECT().GetLatestSettledCertificateHeader(ctx, uint32(1)).Return(certHeader, nil)
	certQuerier.EXPECT().GetSettledBlocksFromCertHeader(ctx, certHeader).Return(aggsendertypes.SettledBlocks{
		LastBridgeExitBlockErr:         errors.New("bridgesync lag"),
		LastImportedBridgeExitBlockErr: errors.New("claim not in db"),
		SettledImportedBridgeExit:      settledIBE,
	})
	// No GetLatestBlockNumByGlobalIndexFromRPC expectation: the gate on LastBridgeExitBlockErr
	// must prevent the RPC fallback from ever being invoked while the bridge-exit source is
	// erroring, on every attempt. If the setter incorrectly invoked it anyway, the mock would
	// panic on the unexpected call.
	// No SetNextRequiredBlock expectation: LastBridgeExitBlockErr means EarliestBlock never
	// succeeds, so the syncer's starting block is never set, on any attempt.

	const attempts = 3 // maxRetries=2 -> 3 total tries
	retryHandler := aggkitcommon.NewRetryHandler([]configtypes.Duration{{Duration: time.Millisecond}}, attempts-1)
	err := setter.SetClaimSyncerNextRequiredBlock(ctx, claimSyncer, retryHandler)

	require.ErrorIs(t, err, aggkitcommon.ErrExecutionFails)
	require.ErrorContains(t, err, "bridgesync lag")
	claimSyncer.AssertNumberOfCalls(t, "GetLatestBlockNumByGlobalIndexFromRPC", 0)

	after := claimSyncerFallbackCount(t, metrics.ReasonIBENotFoundOnRPC)
	require.Equal(t, before, after,
		"the IBE fallback (and its metric) must not fire at all while LastBridgeExitBlockErr is set, "+
			"on any attempt")
}

// TestSetClaimSyncerNextRequiredBlock_SmallRetryBudgetReachesRPCExhaustionFallback is the
// regression test for F6: a caller whose retryHandler only allows a small, fixed number of
// attempts before giving up -- as the AggSender validator's initial-check retry handler does,
// NewRetryHandler(delays, 1), which allows exactly 2 total attempts -- must still be able to reach
// the RPC-lookup-exhausted fallback within that budget, instead of exhausting its own retries (and,
// for the validator, panicking) before the fallback is ever considered. maxIBERPCLookupFailures is
// 5, far more than 2, so this only works because the consecutive-failure budget actually enforced
// is derived from the caller's own retryHandler (min(maxIBERPCLookupFailures, attempts allowed)),
// not the fixed constant alone.
func TestSetClaimSyncerNextRequiredBlock_SmallRetryBudgetReachesRPCExhaustionFallback(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	setter, certQuerier, agglayerClient := newTestSetter(t)

	globalIndex := big.NewInt(41)
	settledIBE := &agglayertypes.SettledImportedBridgeExit{GlobalIndex: globalIndex}
	certHeader := &agglayertypes.CertificateHeader{}
	claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
	claimSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), false, nil)
	agglayerClient.EXPECT().GetLatestSettledCertificateHeader(ctx, uint32(1)).Return(certHeader, nil)
	certQuerier.EXPECT().GetSettledBlocksFromCertHeader(ctx, certHeader).Return(aggsendertypes.SettledBlocks{
		LastBridgeExitBlock:            100,
		LastImportedBridgeExitBlockErr: errors.New("claim not in db"),
		SettledImportedBridgeExit:      settledIBE,
	})
	claimSyncer.EXPECT().
		GetLatestBlockNumByGlobalIndexFromRPC(ctx, globalIndex, (*aggkittypes.BlockNumberFinality)(nil)).
		Return(uint64(0), false, errors.New("rpc error"))
	claimSyncer.EXPECT().SetNextRequiredBlock(ctx, uint64(0)).Return(nil)

	const validatorShapedMaxRetries = 1 // NewRetryHandler(delays, 1) -> 2 total attempts
	retryHandler := aggkitcommon.NewRetryHandler(
		[]configtypes.Duration{{Duration: time.Millisecond}}, validatorShapedMaxRetries)

	err := setter.SetClaimSyncerNextRequiredBlock(ctx, claimSyncer, retryHandler)

	require.NoError(t, err,
		"a caller with only 2 attempts must reach the exhaustion fallback within its own budget, "+
			"not exhaust its retries with an error first")
	claimSyncer.AssertNumberOfCalls(t, "GetLatestBlockNumByGlobalIndexFromRPC", 2)
}
