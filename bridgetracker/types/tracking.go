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
