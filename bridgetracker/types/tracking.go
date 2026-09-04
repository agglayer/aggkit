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
// endpoint's own on-chain isClaimed() check
type TrackerClaimStatus int

const (
	// TrackerClaimStatusPending the bridge has not reached StepWaitingClaim yet (including
	// while still unresolved, TrackingStatusRegistered)
	TrackerClaimStatusPending TrackerClaimStatus = iota
	// TrackerClaimStatusReadyToClaim the current step is StepWaitingClaim: the bridge is
	// ready to be claimed
	TrackerClaimStatusReadyToClaim
	// TrackerClaimStatusClaimed the current step is StepClaimed: the bridge has been claimed
	TrackerClaimStatusClaimed
	// TrackerClaimStatusError TrackingStatus is TrackingStatusError: the tracker gave up
	// resolving the bridge, or one of its steps reached an error
	TrackerClaimStatusError
)

var trackerClaimStatusNames = map[TrackerClaimStatus]string{
	TrackerClaimStatusPending:      "pending",
	TrackerClaimStatusReadyToClaim: "readyToClaim",
	TrackerClaimStatusClaimed:      "claimed",
	TrackerClaimStatusError:        "error",
}

// String representation of the enum
func (c TrackerClaimStatus) String() string {
	if name, ok := trackerClaimStatusNames[c]; ok {
		return name
	}
	return fmt.Sprintf("Unknown(%d)", int(c))
}
