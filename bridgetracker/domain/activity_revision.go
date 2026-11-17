package domain

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"strings"

	"github.com/agglayer/aggkit/bridgetracker/types"
)

// ActivityRevisionPrefix is the scheme version every revision string starts with. The scheme is
// the exact set of fields HashActivityEntry hashes plus how they are encoded and combined:
// changing any of it MUST bump the prefix (v2:...), so two instances hashing different things
// never agree by accident (see ActivityRevision).
const ActivityRevisionPrefix = "v1:"

// EmptyActivityRevision is the revision of an address with no bridges under the v1 scheme: the
// scheme prefix followed by 256 zero bits (ActivityRevision's zero value). It is also what an
// implementation serves until the follow-up that computes the revision lands (agglayer/aggkit#1968,
// #1969) — so, for now, every address reads as empty
const EmptyActivityRevision = ActivityRevisionPrefix +
	"0000000000000000000000000000000000000000000000000000000000000000"

// activityRevisionDomainTag separates the v1 per-entry hash from any other SHA-256 use
const activityRevisionDomainTag = "aggkit/bridgetracker/activity-revision/v1"

const activityHashSize = sha256.Size

// ActivityEntryHash is the hash of one ActivityEntry under the v1 revision scheme
// (see HashActivityEntry)
type ActivityEntryHash [activityHashSize]byte

// HashActivityEntry is the contribution of entry to its address's revision: SHA-256 over exactly
//   - the bridge's global_index,
//   - the bridge's tx_hash and block_num,
//   - the claim_status (ActivityEntry.TrackerClaimStatus, by wire name) with
//     types.TrackerClaimStatusClaimedFinalized counted as types.TrackerClaimStatusClaimed, so an
//     entry only becoming final does not change it,
//   - the claim's tx_hash (empty while there is no Claim).
//
// Nothing else is hashed, deliberately: not Finalized, CreatedAt/UpdatedAt (the
// last_updated_timestamp advances on every refresh), Errors (warnings and transient errors),
// Tracking (the tracker's current step has its own WebSocket channel), Source, BridgeNetworkID
// nor any other field of Bridge/Claim. Hashes are lower-cased, so only their value counts, not
// how a source spelled them. Every field is length-prefixed, so no two different tuples share an
// encoding. A pure function: the in-memory cache and the SQLite store must produce identical
// values from identical entries.
func HashActivityEntry(entry *ActivityEntry) ActivityEntryHash {
	var globalIndex, bridgeTxHash, claimTxHash string
	var blockNum uint64
	if entry.Bridge != nil {
		globalIndex = string(entry.Bridge.GlobalIndex)
		bridgeTxHash = strings.ToLower(string(entry.Bridge.TxHash))
		blockNum = entry.Bridge.BlockNum
	}
	if entry.Claim != nil {
		claimTxHash = strings.ToLower(string(entry.Claim.TxHash))
	}
	status := entry.TrackerClaimStatus
	if status == types.TrackerClaimStatusClaimedFinalized {
		status = types.TrackerClaimStatusClaimed
	}

	h := sha256.New()
	writeRevisionField(h, activityRevisionDomainTag)
	writeRevisionField(h, globalIndex)
	writeRevisionField(h, bridgeTxHash)
	var block [8]byte
	binary.BigEndian.PutUint64(block[:], blockNum)
	_, _ = h.Write(block[:])
	writeRevisionField(h, status.String())
	writeRevisionField(h, claimTxHash)

	var out ActivityEntryHash
	h.Sum(out[:0])
	return out
}

// writeRevisionField writes s to h preceded by its length (uint32, big-endian)
func writeRevisionField(h hash.Hash, s string) {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(s)))
	_, _ = h.Write(length[:])
	_, _ = h.Write([]byte(s))
}

// ActivityRevision accumulates the ActivityEntryHash of every entry of one address into the
// address's revision: the sum of the hashes modulo 2^256. Being a sum it is order independent
// (the same entries give the same revision in any order, in memory or in SQLite) and
// incremental — adding an entry is Add, removing it is Remove, changing it is
// Remove(old hash).Add(new hash) — without recomputing over every entry, and Add then Remove of
// the same entry gives back the exact previous value. Entries are unique per address (one per
// global_index), so a duplicated hash never cancels out the way it would with XOR.
//
// The zero value is the empty activity (EmptyActivityRevision). It is a value type: Add/Remove
// return the new accumulator and leave the receiver untouched. There is no persistence by design:
// the revision is a pure function of the entries, so it is the same after a restart, after the
// address's cache is rebuilt and on every instance holding the same data.
type ActivityRevision struct {
	sum [activityHashSize]byte // big-endian
}

// Add returns the revision with entry's hash included
func (r ActivityRevision) Add(entry ActivityEntryHash) ActivityRevision {
	var out ActivityRevision
	carry := 0
	for i := activityHashSize - 1; i >= 0; i-- {
		s := int(r.sum[i]) + int(entry[i]) + carry
		out.sum[i] = byte(s)
		carry = s >> 8 //nolint:mnd // one byte
	}
	return out
}

// Remove returns the revision with entry's hash taken out; entry must have been added before,
// or the result is meaningless (as it would be removing a bridge the address never had)
func (r ActivityRevision) Remove(entry ActivityEntryHash) ActivityRevision {
	var out ActivityRevision
	borrow := 0
	for i := activityHashSize - 1; i >= 0; i-- {
		d := int(r.sum[i]) - int(entry[i]) - borrow
		borrow = 0
		if d < 0 {
			d += 1 << 8 //nolint:mnd // one byte
			borrow = 1
		}
		out.sum[i] = byte(d)
	}
	return out
}

// String is the wire value of ActivityResponse.revision: ActivityRevisionPrefix followed by the
// 256-bit sum as 64 lowercase hex characters
func (r ActivityRevision) String() string {
	return ActivityRevisionPrefix + hex.EncodeToString(r.sum[:])
}
