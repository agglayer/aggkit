package db

import (
	"path"
	"testing"

	"github.com/agglayer/aggkit/bridgetracker/domain"
	domainmocks "github.com/agglayer/aggkit/bridgetracker/domain/mocks"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// claimedNotFinalSteps is a path whose claim is done but not final (agglayer/aggkit#1970)
func claimedNotFinalSteps() []domain.BridgeStepPath {
	return []domain.BridgeStepPath{{Step: types.StepClaimed, Status: types.StepStatusDone}}
}

func terminalSinceOf(t *testing.T, r *sqliteRegistry, id domain.TrackingID) int64 {
	t.Helper()
	row, err := r.selectRow(id)
	require.NoError(t, err)
	return row.TerminalSince
}

// TestSQLiteRegistryClaimNotFinalStaysActive pins that a claim that is not final is not terminal:
// the row stays in the active list (the engine keeps re-evaluating it) until the claim is final
func TestSQLiteRegistryClaimNotFinalStaysActive(t *testing.T) {
	r := newTestSQLiteRegistry(t)
	id := domain.TrackingID{NetworkID: 1, TxHash: testHash}
	_, err := r.Get(id, true)
	require.NoError(t, err)

	require.NoError(t, r.UpdateTracking(id, domain.TrackingBridgeTx{Info: testBridgeInfo()}, claimedNotFinalSteps()))
	tracking, err := r.Get(id, false)
	require.NoError(t, err)
	require.Equal(t, types.TrackerClaimStatusClaimed, tracking.ClaimStatus())
	require.Zero(t, terminalSinceOf(t, r, id))
	actives, err := r.GetTrackerActives(nil)
	require.NoError(t, err)
	require.Len(t, actives, 1)

	require.NoError(t, r.UpdateTracking(id, domain.TrackingBridgeTx{Info: testBridgeInfo()}, testAllSteps(true)))
	tracking, err = r.Get(id, false)
	require.NoError(t, err)
	require.Equal(t, types.TrackerClaimStatusClaimedFinalized, tracking.ClaimStatus())
	require.NotZero(t, terminalSinceOf(t, r, id))
	actives, err = r.GetTrackerActives(nil)
	require.NoError(t, err)
	require.Empty(t, actives)
}

// TestSQLiteRegistryLegacyTerminalClaimIsSupervisedAgain pins the upgrade path: a row written
// before #1970 stamped a claim that is not final as terminal; it is supervised again, and its
// terminal_since is cleared on its next write
func TestSQLiteRegistryLegacyTerminalClaimIsSupervisedAgain(t *testing.T) {
	r := newTestSQLiteRegistry(t)
	id := domain.TrackingID{NetworkID: 1, TxHash: testHash}
	_, err := r.Get(id, true)
	require.NoError(t, err)
	require.NoError(t, r.UpdateTracking(id, domain.TrackingBridgeTx{Info: testBridgeInfo()}, claimedNotFinalSteps()))
	_, err = r.db.Exec("UPDATE tracked_bridge SET terminal_since = 1234")
	require.NoError(t, err)

	actives, err := r.GetTrackerActives(nil)
	require.NoError(t, err)
	require.Len(t, actives, 1)

	// the claim is gone: back to readyToClaim, active and no longer stamped terminal
	readyToClaim := []domain.BridgeStepPath{
		{Step: types.StepWaitingClaim, Status: types.StepStatusInProgress},
		{Step: types.StepClaimed, Status: types.StepStatusPending},
	}
	require.NoError(t, r.UpdateTracking(id, domain.TrackingBridgeTx{Info: testBridgeInfo()}, readyToClaim))
	require.Zero(t, terminalSinceOf(t, r, id))
	actives, err = r.GetTrackerActives(nil)
	require.NoError(t, err)
	require.Len(t, actives, 1)
}

// TestSQLiteRegistryUnresolvedTxDropsSteps pins what domain.ResetBridgeTx relies on: a tx written
// without Info (the origin reorged out) carries no path, whichever update writes it
func TestSQLiteRegistryUnresolvedTxDropsSteps(t *testing.T) {
	r := newTestSQLiteRegistry(t)
	id := domain.TrackingID{NetworkID: 1, TxHash: testHash}
	_, err := r.Get(id, true)
	require.NoError(t, err)
	updates, unsubscribe, err := r.Subscribe(id)
	require.NoError(t, err)
	defer unsubscribe()

	require.NoError(t, publishStatus(r, id, testBridgeInfo(), testAllSteps(false)))
	<-updates

	require.NoError(t, r.UpdateTrackingBridgeTx(id, domain.TrackingBridgeTx{}))
	update := <-updates
	require.Nil(t, update.AllSteps())
	tracking, err := r.Get(id, false)
	require.NoError(t, err)
	require.Nil(t, tracking.AllSteps())
	require.Equal(t, types.TrackingStatusRegistered, tracking.TrackingStatus())

	require.NoError(t, publishStatus(r, id, testBridgeInfo(), testAllSteps(false)))
	<-updates
	require.NoError(t, r.UpdateTracking(id, domain.TrackingBridgeTx{}, nil))
	tracking, err = r.Get(id, false)
	require.NoError(t, err)
	require.Nil(t, tracking.AllSteps())
}

// TestSQLiteRegistryFinalOriginIsNotVerifiedOnReload pins that a row whose origin block is known
// final is trusted on reload without asking the chain (no reorg can remove it)
func TestSQLiteRegistryFinalOriginIsNotVerifiedOnReload(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "bridgetracker_test.sqlite")
	logger := log.WithFields("module", "bridgetracker_test")
	id := domain.TrackingID{NetworkID: 1, TxHash: testHash}

	first, err := NewSQLiteRegistry(dbPath, 0, logger, nil)
	require.NoError(t, err)
	published := testBridgeInfo()
	published.BlockNumber, published.BlockHash, published.BlockFinalized = 100, common.HexToHash("0xaaaa"), true
	_, err = first.Get(id, true)
	require.NoError(t, err)
	require.NoError(t, publishStatus(first, id, published, claimedNotFinalSteps()))
	firstSQLite, ok := first.(*sqliteRegistry)
	require.True(t, ok)
	require.NoError(t, firstSQLite.Close())

	// strict mock without expectations: any CanonicalBlockHash call fails the test
	second, err := NewSQLiteRegistry(dbPath, 0, logger, domainmocks.NewOriginBlockSource(t))
	require.NoError(t, err)
	secondSQLite, ok := second.(*sqliteRegistry)
	require.True(t, ok)
	t.Cleanup(func() { require.NoError(t, secondSQLite.Close()) })

	tracking, err := second.Get(id, false)
	require.NoError(t, err)
	require.Equal(t, published, tracking.Info())
}
