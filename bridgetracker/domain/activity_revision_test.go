package domain

import (
	"strings"
	"testing"
	"time"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/stretchr/testify/require"
)

const (
	revisionTestBridgeTx = "0xaaaa000000000000000000000000000000000000000000000000000000000001"
	revisionTestClaimTx  = "0xbbbb000000000000000000000000000000000000000000000000000000000002"
	revisionTestHash     = "0xCCCC000000000000000000000000000000000000000000000000000000000003"
	revisionTestDest     = "0x00000000000000000000000000000000000000D0"
)

// revisionTestEntry is a claimed bridge with its claim record: every hashed field has a value
func revisionTestEntry() *ActivityEntry {
	return &ActivityEntry{
		Bridge: &bridgeservicetypes.BridgeResponse{
			GlobalIndex:        "4294967296",
			TxHash:             revisionTestBridgeTx,
			BlockNum:           1000,
			BlockPos:           2,
			DepositCount:       3,
			Amount:             "100",
			DestinationNetwork: 2,
			DestinationAddress: revisionTestDest,
			BridgeHash:         revisionTestHash,
		},
		BridgeNetworkID:    1,
		TrackerClaimStatus: types.TrackerClaimStatusClaimed,
		Claim:              &bridgeservicetypes.ClaimResponse{TxHash: revisionTestClaimTx},
	}
}

// revisionOf folds entries into a revision, in the order given
func revisionOf(entries ...*ActivityEntry) ActivityRevision {
	var revision ActivityRevision
	for _, entry := range entries {
		revision = revision.Add(HashActivityEntry(entry))
	}
	return revision
}

// revisionTestEntries is a few distinct bridges of one address
func revisionTestEntries() []*ActivityEntry {
	entries := make([]*ActivityEntry, 0, 4)
	for i, globalIndex := range []bridgeservicetypes.BigIntString{"1", "2", "3", "18446744073709551616"} {
		entries = append(entries, &ActivityEntry{
			Bridge: &bridgeservicetypes.BridgeResponse{
				GlobalIndex: globalIndex,
				TxHash:      bridgeservicetypes.Hash(revisionTestBridgeTx),
				BlockNum:    uint64(100 + i),
			},
			TrackerClaimStatus: types.TrackerClaimStatusPending,
		})
	}
	return entries
}

// TestEmptyActivityRevision pins the empty-activity value of the v2 scheme: the prefix followed
// by 256 zero bits, which is the zero value of ActivityRevision too
func TestEmptyActivityRevision(t *testing.T) {
	require.Equal(t, "v2:"+strings.Repeat("0", 64), EmptyActivityRevision)
	require.Equal(t, EmptyActivityRevision, ActivityRevision{}.String())
	require.True(t, strings.HasPrefix(EmptyActivityRevision, ActivityRevisionPrefix))
}

// TestActivityRevisionStringFormat verifies the wire format: the scheme prefix and 64 lowercase
// hex characters
func TestActivityRevisionStringFormat(t *testing.T) {
	revision := revisionOf(revisionTestEntry()).String()

	require.True(t, strings.HasPrefix(revision, ActivityRevisionPrefix))
	require.Len(t, revision, len(ActivityRevisionPrefix)+64)
	require.Equal(t, strings.ToLower(revision), revision)
	require.NotEqual(t, EmptyActivityRevision, revision)
}

// TestHashActivityEntryGolden pins the v2 hash of a fixed entry: every instance, and the
// in-memory and SQLite stores, must produce this exact value, so any change to the hashed fields
// or their encoding fails here and has to bump ActivityRevisionPrefix instead
func TestHashActivityEntryGolden(t *testing.T) {
	// sha256 of: len4+"aggkit/bridgetracker/activity-revision/v2", bridge network id and deposit_count
	// (4 bytes each), len4+global_index, len4+bridge tx_hash, block_num and block_pos (8 bytes each),
	// len4+amount, destination_network (4 bytes), len4+lower-cased destination_address,
	// len4+lower-cased bridge_hash, len4+"claimed", len4+claim tx_hash (every number big-endian,
	// len4 = uint32 big-endian length), checked independently of the Go code (Python hashlib)
	require.Equal(t, "v2:b47e19d3b428b7cf144fc3bcc4b57513e438d9bb5327b7bb6a1aacfab08995a7",
		revisionOf(revisionTestEntry()).String())
}

// TestHashActivityEntryDeterministic verifies the same entry always gives the same hash, and an
// equal copy of it too
func TestHashActivityEntryDeterministic(t *testing.T) {
	require.Equal(t, HashActivityEntry(revisionTestEntry()), HashActivityEntry(revisionTestEntry()))
}

// TestHashActivityEntryClaimedFinalizedCountsAsClaimed verifies claimed and claimed_finalized
// give the same hash (an entry only becoming final must not change the revision), while every
// other claim_status is told apart
func TestHashActivityEntryClaimedFinalizedCountsAsClaimed(t *testing.T) {
	hashWith := func(status types.TrackerClaimStatus) ActivityEntryHash {
		entry := revisionTestEntry()
		entry.TrackerClaimStatus = status
		return HashActivityEntry(entry)
	}

	require.Equal(t, hashWith(types.TrackerClaimStatusClaimed), hashWith(types.TrackerClaimStatusClaimedFinalized))

	distinct := map[ActivityEntryHash]types.TrackerClaimStatus{}
	for _, status := range []types.TrackerClaimStatus{
		types.TrackerClaimStatusPending, types.TrackerClaimStatusReadyToClaim,
		types.TrackerClaimStatusClaimed, types.TrackerClaimStatusError,
	} {
		other, seen := distinct[hashWith(status)]
		require.False(t, seen, "%s and %s must hash differently", status, other)
		distinct[hashWith(status)] = status
	}
}

// TestHashActivityEntryHashedFieldsChangeIt verifies a change in each hashed field changes the
// hash
func TestHashActivityEntryHashedFieldsChangeIt(t *testing.T) {
	base := HashActivityEntry(revisionTestEntry())

	for name, mutate := range map[string]func(*ActivityEntry){
		"bridge global_index": func(e *ActivityEntry) { e.Bridge.GlobalIndex = "4294967297" },
		"bridge tx_hash": func(e *ActivityEntry) {
			e.Bridge.TxHash = "0xaaaa000000000000000000000000000000000000000000000000000000000009"
		},
		"bridge block_num":           func(e *ActivityEntry) { e.Bridge.BlockNum++ },
		"bridge block_pos":           func(e *ActivityEntry) { e.Bridge.BlockPos++ },
		"bridge network id":          func(e *ActivityEntry) { e.BridgeNetworkID = 7 },
		"bridge deposit_count":       func(e *ActivityEntry) { e.Bridge.DepositCount++ },
		"bridge amount":              func(e *ActivityEntry) { e.Bridge.Amount = "42" },
		"bridge destination_network": func(e *ActivityEntry) { e.Bridge.DestinationNetwork++ },
		"bridge destination_address": func(e *ActivityEntry) { e.Bridge.DestinationAddress = "0xd1" },
		"bridge bridge_hash":         func(e *ActivityEntry) { e.Bridge.BridgeHash = "0xdd" },
		"claim_status":               func(e *ActivityEntry) { e.TrackerClaimStatus = types.TrackerClaimStatusReadyToClaim },
		"claim tx_hash": func(e *ActivityEntry) {
			e.Claim.TxHash = "0xbbbb0000000000000000000000000000000000000000000000000000000000ff"
		},
		"claim record added": func(e *ActivityEntry) { e.Claim = nil },
	} {
		entry := revisionTestEntry()
		mutate(entry)
		require.NotEqual(t, base, HashActivityEntry(entry), name)
	}
}

// TestHashActivityEntryNonHashedFieldsDoNotChangeIt verifies everything the revision deliberately
// ignores leaves the hash unchanged: the finality flag, the timestamps (last_updated_timestamp
// advances on every refresh), errors and warnings, the tracker's snapshot, the source and the
// bridge/claim fields outside the hashed ones
func TestHashActivityEntryNonHashedFieldsDoNotChangeIt(t *testing.T) {
	base := HashActivityEntry(revisionTestEntry())

	for name, mutate := range map[string]func(*ActivityEntry){
		"finalized":  func(e *ActivityEntry) { e.Finalized = true },
		"updated_at": func(e *ActivityEntry) { e.UpdatedAt = time.Unix(1_700_000_000, 0) },
		"created_at": func(e *ActivityEntry) { e.CreatedAt = time.Unix(1_700_000_000, 0) },
		"errors":     func(e *ActivityEntry) { e.Errors = map[string]string{"readiness": "boom"} },
		"tracking": func(e *ActivityEntry) {
			e.Tracking = NewTrackingData(TrackingID{NetworkID: 1}, TrackingBridgeTx{}, nil)
		},
		"source":            func(e *ActivityEntry) { e.Source = ActivitySourceRPC },
		"claim_status raw":  func(e *ActivityEntry) { e.ClaimStatus = types.ClaimStatusClaimed },
		"block_finalized":   func(e *ActivityEntry) { yes := true; e.Bridge.BlockFinalized = &yes },
		"bridge timestamp":  func(e *ActivityEntry) { e.Bridge.BlockTimestamp = 1_700_000_000 },
		"claim block_num":   func(e *ActivityEntry) { e.Claim.BlockNum = 1234 },
		"claimed_finalized": func(e *ActivityEntry) { e.TrackerClaimStatus = types.TrackerClaimStatusClaimedFinalized },
		"hash letter case": func(e *ActivityEntry) {
			e.Bridge.TxHash = bridgeservicetypes.Hash(strings.ToUpper(revisionTestBridgeTx))
		},
		"address letter case": func(e *ActivityEntry) {
			e.Bridge.DestinationAddress = bridgeservicetypes.Address(strings.ToLower(revisionTestDest))
			e.Bridge.BridgeHash = bridgeservicetypes.Hash(strings.ToLower(revisionTestHash))
		},
		"claim hash letter case": func(e *ActivityEntry) { e.Claim.TxHash = bridgeservicetypes.Hash(strings.ToUpper(revisionTestClaimTx)) },
	} {
		entry := revisionTestEntry()
		mutate(entry)
		require.Equal(t, base, HashActivityEntry(entry), name)
	}
}

// TestHashActivityEntryFieldsAreNotConcatenatedAmbiguously verifies the encoding separates
// fields: moving characters between neighbouring fields changes the hash
func TestHashActivityEntryFieldsAreNotConcatenatedAmbiguously(t *testing.T) {
	a := &ActivityEntry{Bridge: &bridgeservicetypes.BridgeResponse{GlobalIndex: "12", TxHash: "3"}}
	b := &ActivityEntry{Bridge: &bridgeservicetypes.BridgeResponse{GlobalIndex: "1", TxHash: "23"}}

	require.NotEqual(t, HashActivityEntry(a), HashActivityEntry(b))
}

// TestHashActivityEntryNilBridge verifies a malformed entry does not panic
func TestHashActivityEntryNilBridge(t *testing.T) {
	require.NotPanics(t, func() { HashActivityEntry(&ActivityEntry{}) })
}

// TestActivityRevisionOrderIndependent verifies the same entries give the same revision in any
// order
func TestActivityRevisionOrderIndependent(t *testing.T) {
	entries := revisionTestEntries()
	want := revisionOf(entries...)

	for _, order := range [][]int{{3, 2, 1, 0}, {1, 3, 0, 2}, {2, 0, 3, 1}} {
		shuffled := make([]*ActivityEntry, 0, len(order))
		for _, i := range order {
			shuffled = append(shuffled, entries[i])
		}
		require.Equal(t, want, revisionOf(shuffled...))
		require.Equal(t, want.String(), revisionOf(shuffled...).String())
	}
}

// TestActivityRevisionAddThenRemoveRestoresPrevious verifies Remove undoes Add exactly, in any
// order, down to the empty activity
func TestActivityRevisionAddThenRemoveRestoresPrevious(t *testing.T) {
	entries := revisionTestEntries()
	before := revisionOf(entries[:2]...)

	extra := HashActivityEntry(entries[2])
	after := before.Add(extra)
	require.NotEqual(t, before, after)
	require.Equal(t, before, after.Remove(extra))

	// removing an entry that is not the last one added
	require.Equal(t, revisionOf(entries[1]), revisionOf(entries[:2]...).Remove(HashActivityEntry(entries[0])))

	// every entry removed gives back the empty activity
	full := revisionOf(entries...)
	for _, entry := range entries {
		full = full.Remove(HashActivityEntry(entry))
	}
	require.Equal(t, ActivityRevision{}, full)
	require.Equal(t, EmptyActivityRevision, full.String())
}

// TestActivityRevisionChangeIsRemoveThenAdd verifies the incremental update of a changed entry
// (Remove the old hash, Add the new one) equals recomputing the revision from scratch
func TestActivityRevisionChangeIsRemoveThenAdd(t *testing.T) {
	entries := revisionTestEntries()
	revision := revisionOf(entries...)

	changed := *entries[1]
	changed.TrackerClaimStatus = types.TrackerClaimStatusReadyToClaim
	incremental := revision.Remove(HashActivityEntry(entries[1])).Add(HashActivityEntry(&changed))

	require.Equal(t, revisionOf(entries[0], &changed, entries[2], entries[3]), incremental)
	require.NotEqual(t, revision, incremental)

	// the change going back restores the previous revision
	require.Equal(t, revision, incremental.Remove(HashActivityEntry(&changed)).Add(HashActivityEntry(entries[1])))
}

// TestActivityRevisionIgnoresFinality verifies an entry only becoming final leaves the revision
// of the whole address unchanged
func TestActivityRevisionIgnoresFinality(t *testing.T) {
	entries := revisionTestEntries()
	before := revisionOf(entries...).String()

	entries[0].TrackerClaimStatus = types.TrackerClaimStatusClaimed
	claimed := revisionOf(entries...).String()
	require.NotEqual(t, before, claimed, "claiming is a state change")

	entries[0].TrackerClaimStatus = types.TrackerClaimStatusClaimedFinalized
	entries[0].Finalized = true
	require.Equal(t, claimed, revisionOf(entries...).String())
}

// TestActivityRevisionAddRemoveAreValueOperations verifies Add/Remove leave the receiver
// untouched
func TestActivityRevisionAddRemoveAreValueOperations(t *testing.T) {
	var empty ActivityRevision
	hash := HashActivityEntry(revisionTestEntry())

	added := empty.Add(hash)
	require.Equal(t, ActivityRevision{}, empty)
	_ = added.Remove(hash)
	require.NotEqual(t, ActivityRevision{}, added)
}

// TestActivityRevisionCarryAndBorrow verifies the 256-bit modular arithmetic across byte
// boundaries and wrap-around
func TestActivityRevisionCarryAndBorrow(t *testing.T) {
	var one, allOnes ActivityEntryHash
	one[len(one)-1] = 1
	for i := range allOnes {
		allOnes[i] = 0xff
	}

	// carry through every byte: 0xff..ff + 1 wraps to zero
	require.Equal(t, ActivityRevision{}, ActivityRevision{}.Add(allOnes).Add(one))
	// borrow through every byte: 0 - 1 wraps to 0xff..ff
	require.Equal(t, "v2:"+strings.Repeat("f", 64), ActivityRevision{}.Remove(one).String())
	// 0x00ff + 0x0001 carries one byte up
	var low ActivityEntryHash
	low[len(low)-1] = 0xff
	require.Equal(t, "v2:"+strings.Repeat("0", 60)+"0100", ActivityRevision{}.Add(low).Add(one).String())
	require.Equal(t, ActivityRevision{}.Add(low), ActivityRevision{}.Add(low).Add(one).Remove(one))
}
