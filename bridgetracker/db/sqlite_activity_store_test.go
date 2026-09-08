package db

import (
	"context"
	"errors"
	"math/big"
	"path"
	"testing"
	"time"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

var testFromAddress = common.HexToAddress("0x1111111111111111111111111111111111111111")

// testScannedNetworkID is the network these tests' bridges were scanned from, matching
// testBridge's hardcoded OriginNetwork so most tests need not care about the distinction
const testScannedNetworkID = uint32(1)

func testBridge(globalIndex int64) *bridgeservicetypes.BridgeResponse {
	return &bridgeservicetypes.BridgeResponse{
		OriginNetwork:      1,
		DestinationNetwork: 2,
		DepositCount:       uint32(globalIndex),
		GlobalIndex:        big.NewInt(globalIndex),
		TxHash:             bridgeservicetypes.Hash("0xtx"),
	}
}

func testScannedBridge(globalIndex int64) *domain.ScannedBridge {
	return activityScannedBridge(testBridge(globalIndex), testScannedNetworkID)
}

func activityScannedBridge(bridge *bridgeservicetypes.BridgeResponse, networkID uint32) *domain.ScannedBridge {
	return &domain.ScannedBridge{Bridge: bridge, NetworkID: networkID}
}

// fakeActivityScanner is a hand-rolled ActivityBridgeScanner for tests: it returns whichever of
// bridges is not in known, mirroring ActivitySource.BridgesFrom's real contract
type fakeActivityScanner struct {
	bridges   []*domain.ScannedBridge
	err       error
	warnings  []domain.ActivityWarning
	calls     int
	lastKnown map[string]struct{}
}

func (f *fakeActivityScanner) BridgesFrom(
	_ context.Context, _ common.Address, known map[string]struct{},
) ([]*domain.ScannedBridge, []domain.ActivityWarning, error) {
	f.calls++
	f.lastKnown = known
	if f.err != nil {
		return nil, nil, f.err
	}
	out := make([]*domain.ScannedBridge, 0, len(f.bridges))
	for _, b := range f.bridges {
		if _, ok := known[b.Bridge.GlobalIndex.String()]; ok {
			continue
		}
		out = append(out, b)
	}
	return out, f.warnings, nil
}

// fakeActivityClaims is a hand-rolled ActivityClaimChecker for tests: isClaimed/claimInfo are
// consulted in FIFO order per call, one entry per expected IsClaimed/ClaimInfo invocation
type fakeActivityClaims struct {
	isClaimed              []bool
	isClaimedErrs          []error
	isClaimedCalls         int
	lastIsClaimedNetworkID uint32
	claimInfo              []*bridgeservicetypes.ClaimResponse
	claimInfoCalls         int
	readyToClaim           bool
	readyToClaimErr        error
	readyToClaims          []bool
	readyToClaimErrs       []error
	readyToClaimCalls      int
}

func (f *fakeActivityClaims) IsClaimed(_ context.Context, bridge *domain.ScannedBridge) (bool, error) {
	f.lastIsClaimedNetworkID = bridge.NetworkID
	i := f.isClaimedCalls
	f.isClaimedCalls++
	if i < len(f.isClaimedErrs) && f.isClaimedErrs[i] != nil {
		return false, f.isClaimedErrs[i]
	}
	return f.isClaimed[i], nil
}

func (f *fakeActivityClaims) ClaimInfo(
	context.Context, *domain.ScannedBridge,
) (*bridgeservicetypes.ClaimResponse, error) {
	claim := f.claimInfo[f.claimInfoCalls]
	f.claimInfoCalls++
	return claim, nil
}

func (f *fakeActivityClaims) IsReadyToClaim(context.Context, *domain.ScannedBridge) (bool, error) {
	i := f.readyToClaimCalls
	f.readyToClaimCalls++
	if i < len(f.readyToClaimErrs) && f.readyToClaimErrs[i] != nil {
		return false, f.readyToClaimErrs[i]
	}
	if i < len(f.readyToClaims) {
		return f.readyToClaims[i], nil
	}
	if f.readyToClaimErr != nil {
		return false, f.readyToClaimErr
	}
	return f.readyToClaim, nil
}

// newTestSQLiteActivityStore builds a sqliteActivityStore with a one-hour idle timeout, long
// enough that no test below evicts anything by accident, backed by a fresh temp-dir DB file
// (shared with a real sqliteRegistry as the supervised store, exactly as they'd share one file
// in production — see NewSQLiteActivityStore's doc)
func newTestSQLiteActivityStore(t *testing.T, scanner domain.ActivityBridgeScanner, claims domain.ActivityClaimChecker,
) *sqliteActivityStore {
	t.Helper()

	dbPath := path.Join(t.TempDir(), "bridgetracker_test.sqlite")
	logger := log.WithFields("module", "activity_test")
	supervised, err := NewSQLiteRegistry(dbPath, 10, logger)
	require.NoError(t, err)

	store, err := NewSQLiteActivityStore(dbPath, scanner, claims, supervised, logger, time.Hour)
	require.NoError(t, err)
	s, ok := store.(*sqliteActivityStore)
	require.True(t, ok)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	return s
}

// TestSQLiteActivityStoreUnclaimedBridgeIsRecheckedEveryCall verifies an unclaimed bridge's
// claim state is re-verified on every GetActivity call, and that includeTracking=false never
// registers it with the tracker
func TestSQLiteActivityStoreUnclaimedBridgeIsRecheckedEveryCall(t *testing.T) {
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	claims := &fakeActivityClaims{isClaimed: []bool{false, false}}
	store := newTestSQLiteActivityStore(t, scanner, claims)

	for range 2 {
		entries, _, err := store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		require.Equal(t, types.ClaimStatusUnclaimed, entries[0].ClaimStatus)
		require.Nil(t, entries[0].Claim)
		require.Nil(t, entries[0].Tracking)
	}
	require.Equal(t, 2, claims.isClaimedCalls)
	require.Equal(t, 0, claims.claimInfoCalls)
}

// TestSQLiteActivityStoreIncludeTrackingRegistersUnclaimedBridge verifies includeTracking=true
// registers a still-unclaimed bridge with the supervised store and reports its snapshot, keyed
// by the scanned network — not Bridge.OriginNetwork
func TestSQLiteActivityStoreIncludeTrackingRegistersUnclaimedBridge(t *testing.T) {
	bridge := testBridge(1)
	scanner := &fakeActivityScanner{
		bridges: []*domain.ScannedBridge{activityScannedBridge(bridge, testScannedNetworkID)},
	}
	claims := &fakeActivityClaims{isClaimed: []bool{false}}
	store := newTestSQLiteActivityStore(t, scanner, claims)

	entries, _, err := store.GetActivity(t.Context(), testFromAddress, true, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, types.ClaimStatusUnclaimed, entries[0].ClaimStatus)
	require.NotNil(t, entries[0].Tracking)

	wantID := domain.TrackingID{NetworkID: testScannedNetworkID, TxHash: common.HexToHash(string(bridge.TxHash))}
	require.Equal(t, wantID, entries[0].Tracking.ID())
}

// TestSQLiteActivityStoreClaimedAndIndexedBridgeIsNeverRechecked verifies a bridge that is
// claimed with its claim record already fetched is never rechecked on a later call — settled,
// and persisted as such across calls
func TestSQLiteActivityStoreClaimedAndIndexedBridgeIsNeverRechecked(t *testing.T) {
	claim := &bridgeservicetypes.ClaimResponse{TxHash: "0xclaimtx"}
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	// only one IsClaimed/ClaimInfo entry: a second consultation would panic on out-of-range
	claims := &fakeActivityClaims{isClaimed: []bool{true}, claimInfo: []*bridgeservicetypes.ClaimResponse{claim}}
	store := newTestSQLiteActivityStore(t, scanner, claims)

	entries, _, err := store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, types.ClaimStatusClaimed, entries[0].ClaimStatus)
	require.Equal(t, claim, entries[0].Claim)

	entries, _, err = store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, claim, entries[0].Claim)
	require.Equal(t, 1, claims.isClaimedCalls)
	require.Equal(t, 1, claims.claimInfoCalls)
	require.Equal(t, 2, scanner.calls) // BridgesFrom is still called every time to find new bridges
}

// TestSQLiteActivityStoreIsClaimedFailureReportsErrorStatus verifies a failed isClaimed() check
// is reported as ClaimStatusError — never silently as ClaimStatusUnclaimed — and is retried on
// the next call
func TestSQLiteActivityStoreIsClaimedFailureReportsErrorStatus(t *testing.T) {
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	claims := &fakeActivityClaims{
		isClaimed:     []bool{false, false},
		isClaimedErrs: []error{errors.New("no bridge contract address configured for network 2"), nil},
	}
	store := newTestSQLiteActivityStore(t, scanner, claims)

	entries, _, err := store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, types.ClaimStatusError, entries[0].ClaimStatus)
	require.Equal(t, "no bridge contract address configured for network 2", entries[0].Errors["claim"])

	entries, _, err = store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, types.ClaimStatusUnclaimed, entries[0].ClaimStatus)
	require.Equal(t, 2, claims.isClaimedCalls)
	require.Nil(t, entries[0].Errors, "a successful recheck must not carry over the previous failure")
}

// TestSQLiteActivityStoreFilterPendingExcludesClaimedAndErrored verifies ActivityFilterPending
// returns only bridges still unclaimed and not yet ready to claim, and never fetches a claimed
// bridge's claim record
func TestSQLiteActivityStoreFilterPendingExcludesClaimedAndErrored(t *testing.T) {
	pendingBridge := testScannedBridge(2)
	scanner := &fakeActivityScanner{
		bridges: []*domain.ScannedBridge{
			testScannedBridge(1), pendingBridge, testScannedBridge(3), testScannedBridge(4),
		},
	}
	claims := &fakeActivityClaims{
		isClaimed:     []bool{true, false, false, false},
		isClaimedErrs: []error{nil, nil, errors.New("boom"), nil},
		readyToClaims: []bool{false, true},
	}
	store := newTestSQLiteActivityStore(t, scanner, claims)

	entries, _, err := store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterPending)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, pendingBridge.Bridge, entries[0].Bridge)
	require.Equal(t, types.TrackerClaimStatusPending, entries[0].TrackerClaimStatus)
	require.Equal(t, 0, claims.claimInfoCalls)
}

// TestSQLiteActivityStoreFilterClaimedExcludesPending verifies ActivityFilterClaimed returns
// only confirmed-claimed bridges
func TestSQLiteActivityStoreFilterClaimedExcludesPending(t *testing.T) {
	claimedBridge := testScannedBridge(1)
	claim := &bridgeservicetypes.ClaimResponse{TxHash: "0xclaimtx"}
	scanner := &fakeActivityScanner{
		bridges: []*domain.ScannedBridge{claimedBridge, testScannedBridge(2), testScannedBridge(3)},
	}
	claims := &fakeActivityClaims{
		isClaimed:     []bool{true, false, false},
		isClaimedErrs: []error{nil, nil, errors.New("boom")},
		claimInfo:     []*bridgeservicetypes.ClaimResponse{claim},
	}
	store := newTestSQLiteActivityStore(t, scanner, claims)

	entries, _, err := store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterClaimed)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, claimedBridge.Bridge, entries[0].Bridge)
	require.Equal(t, claim, entries[0].Claim)
}

// TestSQLiteActivityStoreScannerReceivesGrowingKnownSet verifies the scanner is called with an
// empty known set the first time, and with the previously found bridge's key once it has been
// cached — persisted, not just held in memory for the call
func TestSQLiteActivityStoreScannerReceivesGrowingKnownSet(t *testing.T) {
	bridge := testScannedBridge(1)
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{bridge}}
	claims := &fakeActivityClaims{isClaimed: []bool{false, false}}
	store := newTestSQLiteActivityStore(t, scanner, claims)

	_, _, err := store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Empty(t, scanner.lastKnown, "nothing cached yet on the first call")

	_, _, err = store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Contains(t, scanner.lastKnown, bridge.Bridge.GlobalIndex.String())
}

// TestSQLiteActivityStoreScannerErrorFailsTheCall verifies a scanner failure fails GetActivity
// entirely
func TestSQLiteActivityStoreScannerErrorFailsTheCall(t *testing.T) {
	wantErr := errors.New("bridge service unreachable")
	scanner := &fakeActivityScanner{err: wantErr}
	store := newTestSQLiteActivityStore(t, scanner, &fakeActivityClaims{})

	_, _, err := store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.ErrorIs(t, err, wantErr)
}

// TestSQLiteActivityStoreTimestampsTrackCreationAndLastUpdate verifies CreatedAt is stamped
// once and never changes, while UpdatedAt advances on every recheck of a still-unsettled entry
func TestSQLiteActivityStoreTimestampsTrackCreationAndLastUpdate(t *testing.T) {
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	claims := &fakeActivityClaims{isClaimed: []bool{false, false}}
	store := newTestSQLiteActivityStore(t, scanner, claims)

	t1 := time.Now()
	store.now = func() time.Time { return t1 }
	entries, _, err := store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.True(t, entries[0].CreatedAt.Equal(t1))
	require.True(t, entries[0].UpdatedAt.Equal(t1))

	t2 := t1.Add(time.Minute)
	store.now = func() time.Time { return t2 }
	entries, _, err = store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.True(t, entries[0].CreatedAt.Equal(t1), "creation time must not change")
	require.True(t, entries[0].UpdatedAt.Equal(t2), "update time must advance on every recheck")
}

// TestSQLiteActivityStoreIdleAddressIsNotForgotten verifies an address untouched for longer than
// idleTimeout is NOT forgotten: unlike the in-memory adapter, this store never deletes a
// persisted row on its own for now (see sqliteActivityStore.sweepIdle) — proven by a settled
// bridge staying settled (isClaimed is not asked again) well past idleTimeout
func TestSQLiteActivityStoreIdleAddressIsNotForgotten(t *testing.T) {
	claim := &bridgeservicetypes.ClaimResponse{TxHash: "0xclaimtx"}
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	// a single isClaimed/claimInfo entry: a second consultation would panic on out-of-range,
	// proving the settled entry survived past idleTimeout instead of being forgotten and re-scanned
	claims := &fakeActivityClaims{isClaimed: []bool{true}, claimInfo: []*bridgeservicetypes.ClaimResponse{claim}}

	dbPath := path.Join(t.TempDir(), "bridgetracker_test.sqlite")
	logger := log.WithFields("module", "activity_test")
	supervised, err := NewSQLiteRegistry(dbPath, 10, logger)
	require.NoError(t, err)
	storeIface, err := NewSQLiteActivityStore(dbPath, scanner, claims, supervised, logger, time.Minute)
	require.NoError(t, err)
	store, ok := storeIface.(*sqliteActivityStore)
	require.True(t, ok)
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	now := time.Now()
	store.now = func() time.Time { return now }

	entries, _, err := store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, claim, entries[0].Claim)
	require.Equal(t, 1, claims.isClaimedCalls, "settled after the first call")

	now = now.Add(2 * time.Minute) // well past idleTimeout

	entries, _, err = store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, claim, entries[0].Claim)
	require.Equal(t, 1, claims.isClaimedCalls, "the row must still be there, so the settled entry is never rechecked")

	var count int
	require.NoError(t, store.db.QueryRow("SELECT COUNT(*) FROM activity_bridge").Scan(&count))
	require.Equal(t, 1, count, "no row is deleted for now (pruning stays in-memory-only)")
}

// TestSQLiteActivityStorePersistsAcrossInstances pins the actual point of this adapter: a
// second store opened over the same DB file sees exactly what the first one wrote
func TestSQLiteActivityStorePersistsAcrossInstances(t *testing.T) {
	claim := &bridgeservicetypes.ClaimResponse{TxHash: "0xclaimtx"}
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	claims := &fakeActivityClaims{isClaimed: []bool{true}, claimInfo: []*bridgeservicetypes.ClaimResponse{claim}}

	dbPath := path.Join(t.TempDir(), "bridgetracker_test.sqlite")
	logger := log.WithFields("module", "activity_test")
	supervised, err := NewSQLiteRegistry(dbPath, 10, logger)
	require.NoError(t, err)

	first, err := NewSQLiteActivityStore(dbPath, scanner, claims, supervised, logger, time.Hour)
	require.NoError(t, err)
	entries, _, err := first.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, claim, entries[0].Claim)
	firstSQLite, ok := first.(*sqliteActivityStore)
	require.True(t, ok)
	require.NoError(t, firstSQLite.Close())

	// second store, second scanner/claims that would panic if consulted again: proves the
	// second instance served the result straight from the DB, without rescanning/rechecking
	second, err := NewSQLiteActivityStore(
		dbPath, &fakeActivityScanner{}, &fakeActivityClaims{}, supervised, logger, time.Hour)
	require.NoError(t, err)
	secondSQLite, ok := second.(*sqliteActivityStore)
	require.True(t, ok)
	t.Cleanup(func() { require.NoError(t, secondSQLite.Close()) })

	entries, _, err = second.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, claim, entries[0].Claim)
}

// TestSQLiteActivityStoreRecordsScanWarnings verifies a network's scan failure is persisted into
// activity_address.scan_state, and that a fully successful scan leaves it untouched
func TestSQLiteActivityStoreRecordsScanWarnings(t *testing.T) {
	warning := domain.ActivityWarning{NetworkID: 7, Message: "bridge service unreachable"}
	scanner := &fakeActivityScanner{warnings: []domain.ActivityWarning{warning}}
	store := newTestSQLiteActivityStore(t, scanner, &fakeActivityClaims{})

	_, warnings, err := store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, []domain.ActivityWarning{warning}, warnings)

	row, err := store.selectAddressRow(testFromAddress.Hex())
	require.NoError(t, err)
	scanState, err := decodeScanState(row.ScanState)
	require.NoError(t, err)
	require.Equal(t, warning.Message, scanState[7].LastError)
	require.NotZero(t, scanState[7].LastErrorAt)
}

// TestSQLiteActivityStoreBridgeNetworkIDUsesScannedNetworkNotBridgeOriginNetwork verifies
// ActivityEntry.BridgeNetworkID reflects the network the bridge was scanned from, not
// Bridge.OriginNetwork
func TestSQLiteActivityStoreBridgeNetworkIDUsesScannedNetworkNotBridgeOriginNetwork(t *testing.T) {
	const scannedNetworkID = uint32(7)
	bridge := testBridge(1)
	bridge.OriginNetwork = 99
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{activityScannedBridge(bridge, scannedNetworkID)}}
	claims := &fakeActivityClaims{isClaimed: []bool{false}}
	store := newTestSQLiteActivityStore(t, scanner, claims)

	entries, _, err := store.GetActivity(t.Context(), testFromAddress, true, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, scannedNetworkID, entries[0].BridgeNetworkID)
	require.Equal(t, scannedNetworkID, claims.lastIsClaimedNetworkID)
	require.Equal(t, scannedNetworkID, entries[0].Tracking.ID().NetworkID)
}

// TestSQLiteActivityStoreSchemaVersionMismatchIsAMiss pins that a bridge row written under a
// different schema_version is treated exactly like a missing one
func TestSQLiteActivityStoreSchemaVersionMismatchIsAMiss(t *testing.T) {
	claim := &bridgeservicetypes.ClaimResponse{TxHash: "0xclaimtx"}
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	claims := &fakeActivityClaims{isClaimed: []bool{true, false}, claimInfo: []*bridgeservicetypes.ClaimResponse{claim}}
	store := newTestSQLiteActivityStore(t, scanner, claims)

	entries, _, err := store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, types.ClaimStatusClaimed, entries[0].ClaimStatus)

	_, err = store.db.Exec("UPDATE activity_bridge SET schema_version = ?", activitySchemaVersion+1)
	require.NoError(t, err)

	// a settled (claimed) bridge whose row is now stale must be re-scanned/re-refreshed from
	// scratch, not read back as claimed straight from the stale row
	entries, _, err = store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, types.ClaimStatusUnclaimed, entries[0].ClaimStatus)
	require.Equal(t, 2, claims.isClaimedCalls)
}
