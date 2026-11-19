package bridgetracker_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/agglayer/aggkit/bridgetracker"
	trackerdb "github.com/agglayer/aggkit/bridgetracker/db"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	domainmocks "github.com/agglayer/aggkit/bridgetracker/domain/mocks"
	finalitymocks "github.com/agglayer/aggkit/bridgetracker/finality/mocks"
	"github.com/agglayer/aggkit/bridgetracker/mocks"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestEngineOriginReorgedOutSQLite pins that the engine re-checks the origin of a supervised bridge
// on its own tick with the SQLite registry too (before agglayer/aggkit#1970 only the registry's Get
// did, on reload): an L2->L1 bridge whose origin block is replaced goes back to registered — path
// dropped, subscriber notified — and is tracked at its new block once re-included
func TestEngineOriginReorgedOutSQLite(t *testing.T) {
	logger := log.WithFields("module", "engine_reorg_sqlite_test")
	registry, err := trackerdb.NewSQLiteRegistry(filepath.Join(t.TempDir(), "tracker.sqlite"), 0, logger, nil)
	require.NoError(t, err)
	id := domain.TrackingID{NetworkID: 1, TxHash: common.HexToHash("0xb1")}
	originHash := common.HexToHash("0xa1")
	bridge := &domain.BridgeInfo{
		NetworkID: 1, LeafType: types.BridgeLeafTypeAsset, DestinationNetwork: 0, DepositCount: 7,
		BlockNumber: 1000, BlockHash: originHash,
	}

	var mu sync.Mutex
	found, canonical := bridge, originHash
	bridges := mocks.NewBridgeEventSource(t)
	bridges.EXPECT().FindBridge(mock.Anything, id).RunAndReturn(
		func(context.Context, domain.TrackingID) (*domain.BridgeInfo, error) {
			mu.Lock()
			defer mu.Unlock()
			if found == nil {
				return nil, bridgetracker.ErrBridgeTxNotFound
			}
			return found, nil
		})
	origin := domainmocks.NewOriginBlockSource(t)
	origin.EXPECT().CanonicalBlockHash(mock.Anything, uint32(1), mock.Anything).RunAndReturn(
		func(context.Context, uint32, uint64) (common.Hash, error) {
			mu.Lock()
			defer mu.Unlock()
			return canonical, nil
		})
	lers := mocks.NewLERSource(t)
	lers.EXPECT().OriginLER(mock.Anything, mock.Anything).Return(nil, nil) // the LER update is still pending
	finality := finalitymocks.NewResolver(t)
	finality.EXPECT().IsFinal(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(false, nil)

	engine, err := bridgetracker.NewEngine(bridgetracker.EngineConfig{}, logger, registry, bridgetracker.EngineSources{
		Bridges:           bridges,
		Certificates:      mocks.NewCertificateSource(t),
		GERs:              mocks.NewGERSource(t),
		LERs:              lers,
		ClaimChecker:      mocks.NewClaimChecker(t),
		Claims:            mocks.NewClaimSource(t),
		Settlement:        mocks.NewSettlementSource(t),
		SettlementHistory: domainmocks.NewSettlementHistorySource(t),
		OriginBlocks:      origin,
		Finality:          finality,
		InjectedGERs:      domainmocks.NewInjectedGERChecker(t),
	})
	require.NoError(t, err)

	updates, unsubscribe, err := registry.Subscribe(id)
	require.NoError(t, err)
	defer unsubscribe()

	bridgetracker.TickOnce(t.Context(), engine)
	tracking := <-updates
	require.Equal(t, uint64(1000), tracking.Info().BlockNumber)
	require.Equal(t, types.StepWaitingLERUpdate, tracking.AllSteps()[*tracking.StepIndex()].Step)

	// the origin block is replaced, the tx is not mined again yet
	mu.Lock()
	found, canonical = nil, common.HexToHash("0xa2")
	mu.Unlock()
	bridgetracker.TickOnce(t.Context(), engine)
	tracking = <-updates
	require.Equal(t, types.TrackingStatusRegistered, tracking.TrackingStatus())
	require.Nil(t, tracking.Info())
	require.Nil(t, tracking.AllSteps())
	stored, err := registry.Get(id, false)
	require.NoError(t, err)
	require.Nil(t, stored.Info(), "persisted as unresolved")
	require.Nil(t, stored.AllSteps(), "the persisted path of the reorged-out bridge is dropped")
	actives, err := registry.GetTrackerActives(nil)
	require.NoError(t, err)
	require.Len(t, actives, 1, "still supervised: FindBridge keeps looking for it")

	// re-included in another block
	moved := *bridge
	moved.BlockNumber, moved.BlockHash = 1003, common.HexToHash("0xa3")
	mu.Lock()
	found, canonical = &moved, moved.BlockHash
	mu.Unlock()
	bridgetracker.TickOnce(t.Context(), engine)
	tracking = <-updates
	require.Equal(t, uint64(1003), tracking.Info().BlockNumber)
	require.Equal(t, moved.BlockHash, tracking.Info().BlockHash)
	stored, err = registry.Get(id, false)
	require.NoError(t, err)
	require.Equal(t, uint64(1003), stored.Info().BlockNumber)
	require.Equal(t, types.StepWaitingLERUpdate, stored.AllSteps()[*stored.StepIndex()].Step)
}
