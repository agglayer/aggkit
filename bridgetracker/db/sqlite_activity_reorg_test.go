package db

import (
	"context"
	"errors"
	"path"
	"sort"
	"testing"
	"time"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	finalitymocks "github.com/agglayer/aggkit/bridgetracker/finality/mocks"
	"github.com/agglayer/aggkit/bridgetracker/mocks"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// reportedFinality is a finality resolver (the mockery mock of finality.Resolver) answering
// block_finalized exactly as the bridge service served it: final only when reported true. Test
// fixtures mark a bridge or claim final by setting its BlockFinalized
func reportedFinality() *finalitymocks.Resolver {
	resolver := &finalitymocks.Resolver{}
	resolver.EXPECT().IsFinal(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ uint32, _ common.Hash, _ uint64, reported *bool) (bool, error) {
			return reported != nil && *reported, nil
		}).Maybe()
	return resolver
}

// finalBridge marks b as reported final by the bridge service (block_finalized=true)
func finalBridge(b *bridgeservicetypes.BridgeResponse) *bridgeservicetypes.BridgeResponse {
	final := true
	b.BlockFinalized = &final
	return b
}

// finalClaim marks c as reported final by the bridge service (block_finalized=true)
func finalClaim(c *bridgeservicetypes.ClaimResponse) *bridgeservicetypes.ClaimResponse {
	final := true
	c.BlockFinalized = &final
	return c
}

// finalScannedBridge is testScannedBridge with its bridge reported final
func finalScannedBridge(globalIndex int64) *domain.ScannedBridge {
	return activityScannedBridge(finalBridge(testBridge(globalIndex)), testScannedNetworkID)
}

const reorgTestTimestamp = uint64(1_700_000_000)

// reorgBridge is the bridge of global index globalIndex, served by the bridge service of network
// 1 at blockNum/txHash, final or not
func reorgBridge(globalIndex int64, blockNum uint64, txHash string, final bool) *domain.ScannedBridge {
	b := testBridge(globalIndex)
	b.BlockNum = blockNum
	b.BlockTimestamp = reorgTestTimestamp + blockNum
	b.TxHash = bridgeservicetypes.Hash(txHash)
	if final {
		finalBridge(b)
	}
	return &domain.ScannedBridge{Bridge: b, NetworkID: testScannedNetworkID, Source: domain.ActivitySourceBridgeService}
}

// reorgClaim is the claim record of global index 1 on network 2 at blockNum/txHash
func reorgClaim(blockNum uint64, txHash string, final bool) *bridgeservicetypes.ClaimResponse {
	c := &bridgeservicetypes.ClaimResponse{
		BlockNum: blockNum, TxHash: bridgeservicetypes.Hash(txHash), GlobalIndex: "1", DestinationNetwork: 2,
	}
	if final {
		finalClaim(c)
	}
	return c
}

// testBridgeKey is the domain.BridgeKey of testScannedBridge(globalIndex)
func testBridgeKey(globalIndex int64) domain.BridgeKey {
	return domain.BridgeKey{NetworkID: testScannedNetworkID, DepositCount: uint32(globalIndex)}
}

// activityHarness drives one activity store (in-memory ActivityCache or SQLite) through scripted
// refreshes: every bridge-service scan and claim check is a mockery expectation, so an
// unexpected call (e.g. isClaimed() on a settled entry) fails the test
type activityHarness struct {
	t         *testing.T
	store     domain.ActivityRegistry
	scanner   *mocks.ActivityBridgeScanner
	claims    *mocks.ActivityClaimChecker
	revisions []string
}

const (
	memoryStore = "memory"
	sqliteStore = "sqlite"
)

func newActivityHarness(t *testing.T, kind string) *activityHarness {
	t.Helper()
	h := &activityHarness{t: t, scanner: mocks.NewActivityBridgeScanner(t), claims: mocks.NewActivityClaimChecker(t)}
	switch kind {
	case memoryStore:
		h.store = bridgetracker.NewActivityCache(h.scanner, h.claims, bridgetracker.NewMemoryRegistry(10),
			reportedFinality(), log.WithFields("module", "activity_test"))
	case sqliteStore:
		h.store = newTestSQLiteActivityStore(t, h.scanner, h.claims)
	default:
		t.Fatalf("unknown store %q", kind)
	}
	mustRegisterActivity(t, h.store, testFromAddress, 0)
	return h
}

// runOnBothStores runs scenario against the in-memory store and the SQLite one, then checks both
// went through exactly the same revisions
func runOnBothStores(t *testing.T, scenario func(h *activityHarness)) {
	t.Helper()
	revisions := map[string][]string{}
	for _, kind := range []string{memoryStore, sqliteStore} {
		t.Run(kind, func(t *testing.T) {
			h := newActivityHarness(t, kind)
			scenario(h)
			revisions[kind] = h.revisions
		})
	}
	require.Equal(t, revisions[memoryStore], revisions[sqliteStore],
		"both stores give the same revision for the same entries")
}

// scan scripts the next BridgesFrom call
func (h *activityHarness) scan(found []*domain.ScannedBridge, invalidated ...domain.BridgeKey) {
	h.scanner.EXPECT().BridgesFrom(mock.Anything, testFromAddress, mock.Anything).
		Return(found, invalidated, nil, nil).Once()
}

// unclaimed scripts one isClaimed()=false check followed by one readiness check
func (h *activityHarness) unclaimed(ready bool) {
	h.claims.EXPECT().IsClaimed(mock.Anything, mock.Anything).Return(false, nil).Once()
	h.claims.EXPECT().IsReadyToClaim(mock.Anything, mock.Anything).Return(ready, nil).Once()
}

// claimed scripts one isClaimed()=true check followed by one claim record fetch
func (h *activityHarness) claimed(claim *bridgeservicetypes.ClaimResponse) {
	h.claims.EXPECT().IsClaimed(mock.Anything, mock.Anything).Return(true, nil).Once()
	h.claims.EXPECT().ClaimInfo(mock.Anything, mock.Anything).Return(claim, nil).Once()
}

// refresh runs one refresh and returns the address's entries (sorted by global index), recording
// the revision the store serves afterwards
func (h *activityHarness) refresh() []*domain.ActivityEntry {
	h.t.Helper()
	require.NoError(h.t, h.store.RefreshAddress(h.t.Context(), testFromAddress))
	return h.entries()
}

func (h *activityHarness) entries() []*domain.ActivityEntry {
	h.t.Helper()
	entries, _, _, err := h.store.GetActivity(h.t.Context(), testFromAddress, false, types.ActivityFilterAll, allActivity)
	require.NoError(h.t, err)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Bridge.GlobalIndex.ToBigInt().Cmp(entries[j].Bridge.GlobalIndex.ToBigInt()) < 0
	})
	h.revisions = append(h.revisions, h.revision())
	return entries
}

func (h *activityHarness) revision() string {
	h.t.Helper()
	reader, ok := h.store.(domain.ActivityRevisionReader)
	require.True(h.t, ok, "every activity store serves the revision")
	revision, err := reader.GetActivityRevision(h.t.Context(), testFromAddress)
	require.NoError(h.t, err)
	return revision
}

// lastRevisions returns the last two revisions recorded
func (h *activityHarness) lastRevisions() (string, string) {
	n := len(h.revisions)
	return h.revisions[n-2], h.revisions[n-1]
}

// TestActivityReorg_BridgeRemoved: a non-final bridge the scan invalidates is removed, and the
// revision goes back to the empty activity
func TestActivityReorg_BridgeRemoved(t *testing.T) {
	runOnBothStores(t, func(h *activityHarness) {
		t := h.t
		h.scan([]*domain.ScannedBridge{reorgBridge(1, 100, "0xb1", false)})
		h.unclaimed(false)
		entries := h.refresh()
		require.Len(t, entries, 1)
		require.NotEqual(t, domain.EmptyActivityRevision, h.revision(), "an addition changes the revision")

		h.scan(nil, testBridgeKey(1))
		require.Empty(t, h.refresh())
		require.Equal(t, domain.EmptyActivityRevision, h.revision(), "a removal changes it back")
	})
}

// TestActivityReorg_BridgeReincludedInAnotherBlock: the scan returning a known non-final bridge
// at another block/tx corrects the entry (no duplicate) and changes the revision
func TestActivityReorg_BridgeReincludedInAnotherBlock(t *testing.T) {
	runOnBothStores(t, func(h *activityHarness) {
		t := h.t
		h.scan([]*domain.ScannedBridge{reorgBridge(1, 100, "0xb1", false)})
		h.unclaimed(false)
		h.refresh()

		h.scan([]*domain.ScannedBridge{reorgBridge(1, 200, "0xb2", false)})
		h.unclaimed(false)
		entries := h.refresh()
		require.Len(t, entries, 1)
		require.Equal(t, uint64(200), entries[0].Bridge.BlockNum)
		require.Equal(t, bridgeservicetypes.Hash("0xb2"), entries[0].Bridge.TxHash)
		require.True(t, entries[0].CreatedAt.Equal(time.Unix(int64(reorgTestTimestamp+200), 0)),
			"creation timestamp follows the block the bridge is in now")
		before, after := h.lastRevisions()
		require.NotEqual(t, before, after, "a correction changes the revision")
	})
}

// TestActivityReorg_BridgeReincludedBecomesFinal: the finality of a bridge re-included in another
// block is the one of its new block. The entry is not final while that block is not, becomes
// final with it (still a single entry, at the new location), and that last step alone does not
// change the revision
func TestActivityReorg_BridgeReincludedBecomesFinal(t *testing.T) {
	runOnBothStores(t, func(h *activityHarness) {
		t := h.t
		h.scan([]*domain.ScannedBridge{reorgBridge(1, 100, "0xb1", false)})
		h.unclaimed(false)
		entries := h.refresh()
		require.False(t, entries[0].Finalized)

		h.scan([]*domain.ScannedBridge{reorgBridge(1, 200, "0xb2", false)})
		h.unclaimed(false)
		entries = h.refresh()
		require.Len(t, entries, 1)
		require.Equal(t, uint64(200), entries[0].Bridge.BlockNum)
		require.False(t, entries[0].Finalized, "the new block is not final either")

		h.scan([]*domain.ScannedBridge{reorgBridge(1, 200, "0xb2", true)})
		h.unclaimed(false)
		entries = h.refresh()
		require.Len(t, entries, 1, "no duplicate left behind at the old block")
		require.Equal(t, uint64(200), entries[0].Bridge.BlockNum)
		require.Equal(t, bridgeservicetypes.Hash("0xb2"), entries[0].Bridge.TxHash)
		require.True(t, entries[0].Finalized, "final once the block it was re-included in is final")
		require.Equal(t, types.TrackerClaimStatusPending, entries[0].TrackerClaimStatus)

		require.Len(t, h.revisions, 3)
		require.NotEqual(t, h.revisions[0], h.revisions[1], "the correction changes the revision")
		require.Equal(t, h.revisions[1], h.revisions[2], "becoming final does not")
	})
}

// TestActivityReorg_BridgeReplacedBySameGlobalIndex: a different bridge taking the same deposit
// count (so global index) replaces the entry's data
func TestActivityReorg_BridgeReplacedBySameGlobalIndex(t *testing.T) {
	runOnBothStores(t, func(h *activityHarness) {
		t := h.t
		h.scan([]*domain.ScannedBridge{reorgBridge(1, 100, "0xb1", false)})
		h.unclaimed(false)
		h.refresh()

		replacement := reorgBridge(1, 100, "0xc1", false)
		replacement.Bridge.Amount = "42"
		h.scan([]*domain.ScannedBridge{replacement})
		h.unclaimed(false)
		entries := h.refresh()
		require.Len(t, entries, 1)
		require.Equal(t, bridgeservicetypes.Hash("0xc1"), entries[0].Bridge.TxHash)
		require.Equal(t, bridgeservicetypes.BigIntString("42"), entries[0].Bridge.Amount)
		before, after := h.lastRevisions()
		require.NotEqual(t, before, after)
	})
}

// TestActivityReorg_FinalBridgeIsHandedToTheScannerAsFinal: once its block is final, the bridge is
// reported as such in known (so the scan can stop there) and an unclaimed entry is finalized,
// without the revision changing
func TestActivityReorg_FinalBridgeIsHandedToTheScannerAsFinal(t *testing.T) {
	runOnBothStores(t, func(h *activityHarness) {
		t := h.t
		h.scan([]*domain.ScannedBridge{reorgBridge(1, 100, "0xb1", false)})
		h.unclaimed(false)
		entries := h.refresh()
		require.False(t, entries[0].Finalized)

		h.scan([]*domain.ScannedBridge{reorgBridge(1, 100, "0xb1", true)})
		h.unclaimed(false)
		entries = h.refresh()
		require.True(t, entries[0].BridgeFinal)
		require.True(t, entries[0].Finalized, "unclaimed, bridge final")
		require.Equal(t, types.TrackerClaimStatusPending, entries[0].TrackerClaimStatus)
		before, after := h.lastRevisions()
		require.Equal(t, before, after, "becoming final does not change the revision")

		h.scanner.EXPECT().BridgesFrom(mock.Anything, testFromAddress,
			mock.MatchedBy(func(known map[domain.BridgeKey]domain.KnownBridge) bool {
				k, ok := known[testBridgeKey(1)]
				return ok && k.Final && k.BlockNum == 100 && k.DepositCount == 1
			})).Return(nil, nil, nil, nil).Once()
		h.unclaimed(false) // still unclaimed: readiness keeps being checked
		h.refresh()
		before, after = h.lastRevisions()
		require.Equal(t, before, after, "a refresh that changes nothing keeps the revision")
	})
}

// TestActivityReorg_ScanFailureKeepsState: a failed scan (or one that only reports a warning, as
// a bridge service answering 503 does) never removes anything; entries are still rechecked
func TestActivityReorg_ScanFailureKeepsState(t *testing.T) {
	runOnBothStores(t, func(h *activityHarness) {
		t := h.t
		h.scan([]*domain.ScannedBridge{reorgBridge(1, 100, "0xb1", false)})
		h.unclaimed(true)
		h.refresh()

		h.scanner.EXPECT().BridgesFrom(mock.Anything, testFromAddress, mock.Anything).
			Return(nil, nil, nil, errors.New("boom")).Once()
		h.unclaimed(true)
		require.Error(t, h.store.RefreshAddress(t.Context(), testFromAddress))
		entries := h.entries()
		require.Len(t, entries, 1)
		require.Equal(t, types.TrackerClaimStatusReadyToClaim, entries[0].TrackerClaimStatus)

		warning := domain.ActivityWarning{NetworkID: 1, Message: "bridge service for network 1 is temporarily unavailable"}
		h.scanner.EXPECT().BridgesFrom(mock.Anything, testFromAddress, mock.Anything).
			Return(nil, nil, []domain.ActivityWarning{warning}, nil).Once()
		h.unclaimed(true)
		require.NoError(t, h.store.RefreshAddress(t.Context(), testFromAddress))
		entries, _, warnings, err := h.store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll, allActivity)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		require.Equal(t, []domain.ActivityWarning{warning}, warnings)
		h.revisions = append(h.revisions, h.revision())
		require.Len(t, h.revisions, 3)
		require.Equal(t, h.revisions[0], h.revisions[2], "nothing changed")
	})
}

// TestActivityReorg_ClaimReorgedOut: a claim that is not final yet goes back to readyToClaim when
// isClaimed() turns false and the GER injection is still there, or to pending when the GER
// injection was reorged out too
func TestActivityReorg_ClaimReorgedOut(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ready bool
		want  types.TrackerClaimStatus
	}{
		{name: "claim only: claimed -> readyToClaim", ready: true, want: types.TrackerClaimStatusReadyToClaim},
		{name: "claim and GER injection: claimed -> pending", ready: false, want: types.TrackerClaimStatusPending},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runOnBothStores(t, func(h *activityHarness) {
				t := h.t
				h.scan([]*domain.ScannedBridge{reorgBridge(1, 100, "0xb1", true)})
				h.claimed(reorgClaim(300, "0xc1", false))
				entries := h.refresh()
				require.Equal(t, types.TrackerClaimStatusClaimed, entries[0].TrackerClaimStatus)
				require.False(t, entries[0].Finalized)

				h.scan(nil)
				h.unclaimed(tc.ready)
				entries = h.refresh()
				require.Equal(t, types.ClaimStatusUnclaimed, entries[0].ClaimStatus)
				require.Equal(t, tc.want, entries[0].TrackerClaimStatus)
				require.Nil(t, entries[0].Claim)
				before, after := h.lastRevisions()
				require.NotEqual(t, before, after, "a claim_status change changes the revision")
			})
		})
	}
}

// TestActivityReorg_ClaimReincludedInAnotherBlock: the claim record is fetched again while the
// claim is not final, so a claim re-included elsewhere updates the entry
func TestActivityReorg_ClaimReincludedInAnotherBlock(t *testing.T) {
	runOnBothStores(t, func(h *activityHarness) {
		t := h.t
		h.scan([]*domain.ScannedBridge{reorgBridge(1, 100, "0xb1", true)})
		h.claimed(reorgClaim(300, "0xc1", false))
		h.refresh()

		h.scan(nil)
		h.claimed(reorgClaim(310, "0xc2", false))
		entries := h.refresh()
		require.Equal(t, types.TrackerClaimStatusClaimed, entries[0].TrackerClaimStatus)
		require.Equal(t, bridgeservicetypes.Hash("0xc2"), entries[0].Claim.TxHash)
		require.Equal(t, uint64(310), entries[0].Claim.BlockNum)
		before, after := h.lastRevisions()
		require.NotEqual(t, before, after, "the claim's tx_hash is hashed")
	})
}

// TestActivityReorg_FinalClaimSettles: once the bridge and the claim are both final the entry is
// claimed_finalized / finalized with the same revision, and nothing is asked about it any more
func TestActivityReorg_FinalClaimSettles(t *testing.T) {
	runOnBothStores(t, func(h *activityHarness) {
		t := h.t
		h.scan([]*domain.ScannedBridge{reorgBridge(1, 100, "0xb1", false)})
		h.claimed(reorgClaim(300, "0xc1", false))
		entries := h.refresh()
		require.Equal(t, types.TrackerClaimStatusClaimed, entries[0].TrackerClaimStatus)

		// claim final, bridge not yet: still claimed, still rechecked
		h.scan([]*domain.ScannedBridge{reorgBridge(1, 100, "0xb1", false)})
		h.claimed(reorgClaim(300, "0xc1", true))
		entries = h.refresh()
		require.Equal(t, types.TrackerClaimStatusClaimed, entries[0].TrackerClaimStatus)
		require.True(t, entries[0].ClaimFinal)
		require.False(t, entries[0].Finalized)

		// bridge final too: settled. The final claim is not asked about again
		h.scan([]*domain.ScannedBridge{reorgBridge(1, 100, "0xb1", true)})
		entries = h.refresh()
		require.Equal(t, types.TrackerClaimStatusClaimedFinalized, entries[0].TrackerClaimStatus)
		require.True(t, entries[0].Finalized)
		settledAt := entries[0].UpdatedAt

		claimedOnly, _, _, err := h.store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterClaimed, allActivity)
		require.NoError(t, err)
		require.Len(t, claimedOnly, 1, "filterBridges=claimed includes claimed_finalized")

		// settled: no claim call (the mock would fail on one), entry untouched
		h.scan(nil)
		entries = h.refresh()
		require.Equal(t, types.TrackerClaimStatusClaimedFinalized, entries[0].TrackerClaimStatus)
		require.True(t, entries[0].UpdatedAt.Equal(settledAt), "a settled entry is never refreshed again")

		require.Len(t, h.revisions, 4)
		for _, revision := range h.revisions {
			require.Equal(t, h.revisions[0], revision, "becoming final never changes the revision")
		}
	})
}

// TestActivityReorg_ClaimCheckFailureKeepsTheClaim: a failed isClaimed() or claim record fetch on
// a claimed entry keeps the last known claim instead of discarding it
func TestActivityReorg_ClaimCheckFailureKeepsTheClaim(t *testing.T) {
	runOnBothStores(t, func(h *activityHarness) {
		t := h.t
		h.scan([]*domain.ScannedBridge{reorgBridge(1, 100, "0xb1", true)})
		h.claimed(reorgClaim(300, "0xc1", false))
		h.refresh()

		h.scan(nil)
		h.claims.EXPECT().IsClaimed(mock.Anything, mock.Anything).Return(false, errors.New("rpc down")).Once()
		entries := h.refresh()
		require.Equal(t, types.TrackerClaimStatusClaimed, entries[0].TrackerClaimStatus)
		require.Equal(t, bridgeservicetypes.Hash("0xc1"), entries[0].Claim.TxHash)

		h.scan(nil)
		h.claims.EXPECT().IsClaimed(mock.Anything, mock.Anything).Return(true, nil).Once()
		h.claims.EXPECT().ClaimInfo(mock.Anything, mock.Anything).Return(nil, errors.New("503")).Once()
		entries = h.refresh()
		require.Equal(t, bridgeservicetypes.Hash("0xc1"), entries[0].Claim.TxHash)

		require.Len(t, h.revisions, 3)
		require.Equal(t, h.revisions[0], h.revisions[2], "a transient failure changes nothing")
	})
}

// TestActivityReorg_RevisionSameAfterRebuild: flushing the address and rebuilding its cache from
// the same data gives back the same revision, with nothing persisted for it
func TestActivityReorg_RevisionSameAfterRebuild(t *testing.T) {
	runOnBothStores(t, func(h *activityHarness) {
		t := h.t
		bridges := []*domain.ScannedBridge{reorgBridge(1, 100, "0xb1", true), reorgBridge(2, 110, "0xb2", false)}
		h.scan(bridges)
		h.claimed(reorgClaim(300, "0xc1", false))
		h.unclaimed(true)
		h.refresh()

		h.store.FlushActivity(testFromAddress)
		require.Equal(t, domain.EmptyActivityRevision, h.revision())
		mustRegisterActivity(t, h.store, testFromAddress, 0)
		h.scan(bridges)
		h.claimed(reorgClaim(300, "0xc1", false))
		h.unclaimed(true)
		entries := h.refresh()
		require.Len(t, entries, 2)
		before, after := h.lastRevisions()
		require.Equal(t, before, after)
	})
}

// TestSQLiteActivityStoreRestartInTheMiddleOfANonFinalState: a store reopened on the same file
// while an entry is claimed but not settled resumes it: the final bridge is still handed to the
// scanner as final, the claim keeps being checked until final, and the revision is unchanged
func TestSQLiteActivityStoreRestartInTheMiddleOfANonFinalState(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "bridgetracker_test.sqlite")
	logger := log.WithFields("module", "activity_test")
	supervised, err := NewSQLiteRegistry(dbPath, 10, logger, nil)
	require.NoError(t, err)

	scanner, claims := mocks.NewActivityBridgeScanner(t), mocks.NewActivityClaimChecker(t)
	first, err := NewSQLiteActivityStore(dbPath, scanner, claims, supervised, reportedFinality(), logger)
	require.NoError(t, err)
	h := &activityHarness{t: t, store: first, scanner: scanner, claims: claims}
	mustRegisterActivity(t, first, testFromAddress, 0)
	h.scan([]*domain.ScannedBridge{reorgBridge(1, 100, "0xb1", true)})
	h.claimed(reorgClaim(300, "0xc1", false))
	entries := h.refresh()
	require.Equal(t, types.TrackerClaimStatusClaimed, entries[0].TrackerClaimStatus)
	closeActivityStore(t, first)

	scanner, claims = mocks.NewActivityBridgeScanner(t), mocks.NewActivityClaimChecker(t)
	second, err := NewSQLiteActivityStore(dbPath, scanner, claims, supervised, reportedFinality(), logger)
	require.NoError(t, err)
	t.Cleanup(func() { closeActivityStore(t, second) })
	h.store, h.scanner, h.claims = second, scanner, claims

	entries = h.entries()
	require.Equal(t, types.TrackerClaimStatusClaimed, entries[0].TrackerClaimStatus)
	require.True(t, entries[0].BridgeFinal)
	before, after := h.lastRevisions()
	require.Equal(t, before, after, "same revision after a restart, without persisting it")

	h.scanner.EXPECT().BridgesFrom(mock.Anything, testFromAddress,
		mock.MatchedBy(func(known map[domain.BridgeKey]domain.KnownBridge) bool {
			return known[testBridgeKey(1)].Final
		})).
		Return(nil, nil, nil, nil).Once()
	h.claimed(reorgClaim(300, "0xc1", true))
	entries = h.refresh()
	require.Equal(t, types.TrackerClaimStatusClaimedFinalized, entries[0].TrackerClaimStatus)
	require.True(t, entries[0].Finalized)
	before, after = h.lastRevisions()
	require.Equal(t, before, after)
}

// TestSQLiteActivityStoreRevisionOfRowsWithoutStoredHash: a row written before revision_hash
// existed (NULL) contributes the hash recomputed from its data, so the revision is the same
func TestSQLiteActivityStoreRevisionOfRowsWithoutStoredHash(t *testing.T) {
	h := newActivityHarness(t, sqliteStore)
	h.scan([]*domain.ScannedBridge{reorgBridge(1, 100, "0xb1", true), reorgBridge(2, 110, "0xb2", false)})
	h.claimed(reorgClaim(300, "0xc1", false))
	h.unclaimed(false)
	h.refresh()
	want := h.revision()

	store, ok := h.store.(*sqliteActivityStore)
	require.True(t, ok)
	_, err := store.db.Exec("UPDATE activity_bridge SET revision_hash = NULL")
	require.NoError(t, err)
	require.Equal(t, want, h.revision())
}

// TestSQLiteActivityStoreParsesClaimedFinalized: claimed_finalized round-trips through the
// tracker_claim_status column, and an unknown value still falls back to error
func TestSQLiteActivityStoreParsesClaimedFinalized(t *testing.T) {
	require.Equal(t, types.TrackerClaimStatusClaimedFinalized, parseTrackerClaimStatus("claimed_finalized"))
	require.Equal(t, types.TrackerClaimStatusClaimed, parseTrackerClaimStatus("claimed"))
	require.Equal(t, types.TrackerClaimStatusError, parseTrackerClaimStatus("bogus"))
	require.True(t, bridgetracker.MatchesActivityFilter(
		&domain.ActivityEntry{TrackerClaimStatus: types.TrackerClaimStatusClaimedFinalized}, types.ActivityFilterClaimed))
}

// TestSQLiteActivityStoreForgetIsScopedToTheAddress: invalidating a global index for one address
// never deletes the row another address holds under it
func TestSQLiteActivityStoreForgetIsScopedToTheAddress(t *testing.T) {
	h := newActivityHarness(t, sqliteStore)
	store, ok := h.store.(*sqliteActivityStore)
	require.True(t, ok)
	h.scan([]*domain.ScannedBridge{reorgBridge(1, 100, "0xb1", false)})
	h.unclaimed(false)
	h.refresh()

	require.NoError(t, store.forgetBridges(common.HexToAddress("0x2222").Hex(), []domain.BridgeKey{testBridgeKey(1)}))
	require.Len(t, h.entries(), 1)
}

func closeActivityStore(t *testing.T, store domain.ActivityRegistry) {
	t.Helper()
	sqliteStore, ok := store.(*sqliteActivityStore)
	require.True(t, ok)
	require.NoError(t, sqliteStore.Close())
}
