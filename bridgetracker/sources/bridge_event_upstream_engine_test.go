package sources

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgetracker"
	trackerdb "github.com/agglayer/aggkit/bridgetracker/db"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	domainmocks "github.com/agglayer/aggkit/bridgetracker/domain/mocks"
	finalitymocks "github.com/agglayer/aggkit/bridgetracker/finality/mocks"
	trackermocks "github.com/agglayer/aggkit/bridgetracker/mocks"
	trackertypes "github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/agglayer/aggkit/log"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestEngineDoesNotTrackBridgeFromInconsistentUpstreamJSON covers inconsistent upstream data through the
// whole tracker: the engine over the SQLite registry and the real BridgeEventSource, whose L2
// endpoint answers a receipt whose log belongs to another block (the reported
// receipt.blockHash=0xAAAA / log.blockHash=0xBBBB, with the log's block served by the endpoint).
// While the endpoint contradicts itself the bridge is neither resolved, nor given a path, nor
// published to a subscriber, and it stays supervised, retried on every poll; once the endpoint
// answers coherently the bridge is tracked from the receipt's own block
func TestEngineDoesNotTrackBridgeFromInconsistentUpstreamJSON(t *testing.T) {
	const originNetwork = uint32(1)
	logger := log.WithFields("module", "bridge_event_upstream_engine_test")
	id := domain.TrackingID{NetworkID: originNetwork, TxHash: testTxHash}
	blockA, blockB := upstreamFlavours[0].blocks()

	registry, err := trackerdb.NewSQLiteRegistry(filepath.Join(t.TempDir(), "tracker.sqlite"), 0, logger, nil)
	require.NoError(t, err)

	// an L2 -> L1 bridge whose log, as the endpoint serves it, was mined in block B instead of A
	chain := coherentUpstreamChain(t, blockA)
	chain.receipt.Logs[0] = upstreamBridgeLog(t, 0, blockA)
	chain.receipt.Logs[0].BlockHash = blockB.hash
	chain.setByHash(blockB)
	upstream := newFakeUpstream(t, chain)
	bridges := newUpstreamNetworkSource(t, upstream, originNetwork, true)

	// everything after the bridge's resolution is out of the scope of this test: the LER update is
	// pending, nothing is final, and the origin block stays the receipt's one
	origin := domainmocks.NewOriginBlockSource(t)
	origin.EXPECT().CanonicalBlockHash(mock.Anything, mock.Anything, mock.Anything).Return(blockA.hash, nil).Maybe()
	lers := trackermocks.NewLERSource(t)
	lers.EXPECT().OriginLER(mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	finality := finalitymocks.NewResolver(t)
	finality.EXPECT().IsFinal(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(false, nil).Maybe()

	engine, err := bridgetracker.NewEngine(
		bridgetracker.EngineConfig{PollInterval: 10 * time.Millisecond, UnresolvedTimeout: time.Hour},
		logger, registry, bridgetracker.EngineSources{
			Bridges:           bridges,
			Certificates:      trackermocks.NewCertificateSource(t),
			GERs:              trackermocks.NewGERSource(t),
			LERs:              lers,
			ClaimChecker:      trackermocks.NewClaimChecker(t),
			Claims:            trackermocks.NewClaimSource(t),
			Settlement:        trackermocks.NewSettlementSource(t),
			SettlementHistory: domainmocks.NewSettlementHistorySource(t),
			OriginBlocks:      origin,
			Finality:          finality,
			InjectedGERs:      domainmocks.NewInjectedGERChecker(t),
		})
	require.NoError(t, err)

	updates, unsubscribe, err := registry.Subscribe(id)
	require.NoError(t, err)
	defer unsubscribe()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	engine.Start(ctx)

	// retried on every poll, never resolved
	var tracking *domain.TrackingData
	require.Eventually(t, func() bool {
		var getErr error
		tracking, getErr = registry.Get(id, false)
		return getErr == nil && tracking.BridgeTx().Error != nil && tracking.BridgeTx().Error.RetryCount >= 3
	}, 10*time.Second, 10*time.Millisecond, "the engine must keep retrying the contradictory upstream")
	require.Nil(t, tracking.Info(), "no bridge facts may be stored from inconsistent upstream data")
	require.Nil(t, tracking.AllSteps(), "no path may be seeded for it")
	require.Equal(t, trackertypes.TrackingStatusRegistered, tracking.TrackingStatus())
	require.False(t, tracking.Failed())
	require.Equal(t, trackertypes.StepErrorTransient, tracking.BridgeTx().Error.ErrorType)
	require.Contains(t, tracking.BridgeTx().Error.Description[0], errInvalidUpstreamData.Error())
	actives, err := registry.GetTrackerActives(nil)
	require.NoError(t, err)
	require.Len(t, actives, 1, "still supervised, so that it is retried")

	// the endpoint recovers: its log is the receipt's own
	upstream.mutate(func(chain *upstreamChain) {
		chain.receipt.Logs[0].BlockHash = blockA.hash
	})
	require.Eventually(t, func() bool {
		var getErr error
		tracking, getErr = registry.Get(id, false)
		return getErr == nil && tracking.Info() != nil
	}, 10*time.Second, 10*time.Millisecond, "the engine must resolve the bridge once the upstream is coherent")
	require.Equal(t, blockA.hash, tracking.Info().BlockHash)
	require.Equal(t, upstreamBlockNumber, tracking.Info().BlockNumber)
	require.Equal(t, blockA.header.Time, tracking.Info().BlockTimestamp)
	require.Nil(t, tracking.BridgeTx().Error, "a successful resolution clears the accumulated error")
	require.NotEmpty(t, tracking.AllSteps())
	require.Equal(t, trackertypes.StepWaitingLERUpdate, tracking.AllSteps()[0].Step)

	// the subscriber was only ever told about the bridge once it was coherent
	cancel()
	var published []*domain.TrackingData
	for drained := false; !drained; {
		select {
		case update := <-updates:
			published = append(published, update)
		default:
			drained = true
		}
	}
	for _, update := range published {
		if update.Info() != nil {
			require.Equal(t, blockA.hash, update.Info().BlockHash,
				"the subscriber must never see facts taken from the contradictory upstream")
		}
	}
}
