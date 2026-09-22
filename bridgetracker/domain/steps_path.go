package domain

import (
	"time"

	"github.com/agglayer/aggkit/bridgetracker/types"
)

// PendingPath materializes the expected path of a freshly resolved bridge: every step of the
// route pending, except the first one which is already in progress. The resolved bridge type
// reveals the whole route, so this is the snapshot published the moment the tx resolves —
// clients see the full way the bridge will walk before any milestone has been checked. The
// per-step protocol duration estimations (ExpectedDuration) will be stamped here when they land.
//
// The first step's StartDate defaults to now, but resolvers[path[0]] (nil-safe: a nil/incomplete
// map, or a resolver whose StartDate returns nil, both simply keep now) is asked first — the
// origin deposit's own block timestamp is always a deterministic candidate here (see
// WaitingGERUpdateResolver/WaitingLERUpdateResolver), the moment the bridge was actually created
// rather than the moment the tracker happened to notice it
func PendingPath(
	bridgeType types.BridgeType, info *BridgeInfo, resolvers map[types.BridgeStep]StepResolver, now time.Time,
) []BridgeStepPath {
	path := ExpectedPath(bridgeType)
	steps := make([]BridgeStepPath, len(path))
	for i, stepID := range path {
		steps[i] = BridgeStepPath{Step: stepID, Status: types.StepStatusPending}
	}
	steps[0].Status = types.StepStatusInProgress
	startDate := now
	if r, ok := resolvers[path[0]]; ok {
		if sd := r.StartDate(info, nil); sd != nil {
			startDate = *sd
		}
	}
	steps[0].StartDate = &startDate
	return steps
}
