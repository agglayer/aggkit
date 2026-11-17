package types

import "fmt"

// TrackingStatus is the full lifecycle of a supervised bridge, from registration to
// settlement: Registered while BridgeStatus is still nil, then Running/Error/Finished from
// the same signal TrackingData.StepIndex points at once it resolves (see the TrackingData
// envelope in bridgetracker/api, the only package that constructs it)
type TrackingStatus int

const (
	// TrackingStatusRegistered the bridge has been added to the supervised list but the
	// tracker has no information about it yet (BridgeStatus is nil)
	TrackingStatusRegistered TrackingStatus = iota
	// TrackingStatusRunning the bridge is resolved and alive: still being polled/updated
	TrackingStatusRunning
	// TrackingStatusError the bridge is resolved and stopped: one of its steps reached an error
	TrackingStatusError
	// TrackingStatusFinished the bridge is resolved and reached its terminal step (Claimed)
	TrackingStatusFinished
)

var trackingStatusNames = map[TrackingStatus]string{
	TrackingStatusRegistered: "registered",
	TrackingStatusRunning:    "running",
	TrackingStatusError:      "error",
	TrackingStatusFinished:   "finished",
}

// String representation of the enum
func (t TrackingStatus) String() string {
	if name, ok := trackingStatusNames[t]; ok {
		return name
	}
	return fmt.Sprintf("Unknown(%d)", int(t))
}

// TrackerClaimStatus is a simplified claim-readiness summary of a supervised bridge, derived
// from TrackingStatus and the current step (see domain.TrackingData.ClaimStatus) for clients
// that only care about "is this claimable" without walking AllSteps themselves. Unrelated to
// ClaimStatus (bridgetracker/types/claim_status.go), the tri-state result of the Activity
// endpoint's own on-chain isClaimed() check.
//
// Every value except TrackerClaimStatusClaimedFinalized is tentative: while the claim's (or the
// bridge's) block is not final, a reorg can still send "claimed" back to "readyToClaim" or
// "pending" (see TrackerClaimStatusClaimed)
type TrackerClaimStatus int

const (
	// TrackerClaimStatusPending the bridge has not reached StepWaitingClaim yet (including
	// while still unresolved, TrackingStatusRegistered)
	TrackerClaimStatusPending TrackerClaimStatus = iota
	// TrackerClaimStatusReadyToClaim the current step is StepWaitingClaim: the bridge is
	// ready to be claimed
	TrackerClaimStatusReadyToClaim
	// TrackerClaimStatusClaimed the current step is StepClaimed: the bridge has been claimed.
	// Not final: the claim's block can still be reorged out, in which case the status goes back
	// to TrackerClaimStatusReadyToClaim (the claim tx is gone) or TrackerClaimStatusPending (the
	// GER or the L1 info tree leaf is gone as well). Becomes TrackerClaimStatusClaimedFinalized
	// once the claim's block is final
	TrackerClaimStatusClaimed
	// TrackerClaimStatusError TrackingStatus is TrackingStatusError: the tracker gave up
	// resolving the bridge, or one of its steps reached an error
	TrackerClaimStatusError
	// TrackerClaimStatusClaimedFinalized the bridge has been claimed and the claim's block is
	// final: the only final state, it never changes again. Wire value "claimed_finalized".
	// Appended after TrackerClaimStatusError (not next to TrackerClaimStatusClaimed) so the
	// numeric value of every pre-existing status stays unchanged
	TrackerClaimStatusClaimedFinalized
)

var trackerClaimStatusNames = map[TrackerClaimStatus]string{
	TrackerClaimStatusPending:          "pending",
	TrackerClaimStatusReadyToClaim:     "readyToClaim",
	TrackerClaimStatusClaimed:          "claimed",
	TrackerClaimStatusError:            "error",
	TrackerClaimStatusClaimedFinalized: "claimed_finalized",
}

// String representation of the enum
func (c TrackerClaimStatus) String() string {
	if name, ok := trackerClaimStatusNames[c]; ok {
		return name
	}
	return fmt.Sprintf("Unknown(%d)", int(c))
}

// ParseTrackerClaimStatus reverses TrackerClaimStatus.String(): it returns the status whose wire
// name is s, or an error for any other value (including the "Unknown(N)" String() form)
func ParseTrackerClaimStatus(s string) (TrackerClaimStatus, error) {
	for status, name := range trackerClaimStatusNames {
		if name == s {
			return status, nil
		}
	}
	return TrackerClaimStatusError, fmt.Errorf("invalid tracker claim status %q", s)
}

// IsClaimed reports whether the bridge has been claimed, final or not: true for
// TrackerClaimStatusClaimed and TrackerClaimStatusClaimedFinalized. This is what
// ActivityFilterClaimed matches, so that filterBridges=claimed keeps returning every claimed
// bridge after "claimed_finalized" was introduced
func (c TrackerClaimStatus) IsClaimed() bool {
	return c == TrackerClaimStatusClaimed || c == TrackerClaimStatusClaimedFinalized
}
