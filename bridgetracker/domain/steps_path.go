package domain

import (
	"time"

	"github.com/agglayer/aggkit/bridgetracker/types"
)

// PendingPath materializes the expected path of a freshly resolved bridge: every step of the
// route pending, except the first one which is already in progress (started at now). The
// resolved bridge type reveals the whole route, so this is the snapshot published the moment
// the tx resolves — clients see the full way the bridge will walk before any milestone has
// been checked. The per-step protocol duration estimations (ExpectedDuration) will be
// stamped here when they land
func PendingPath(bridgeType types.BridgeType, now time.Time) []BridgeStepPath {
	path := ExpectedPath(bridgeType)
	steps := make([]BridgeStepPath, len(path))
	for i, stepID := range path {
		steps[i] = BridgeStepPath{Step: stepID, Status: types.StepStatusPending}
	}
	steps[0].Status = types.StepStatusInProgress
	steps[0].StartDate = &now
	return steps
}
