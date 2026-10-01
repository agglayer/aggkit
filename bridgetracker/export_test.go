package bridgetracker

import (
	"github.com/agglayer/aggkit/bridgetracker/domain"
	aggkitcommon "github.com/agglayer/aggkit/common"
)

// SampleMetricsOnce runs a single metrics sampling pass, for tests in package bridgetracker_test
// (which can import the mocks without an import cycle)
func SampleMetricsOnce(
	logger aggkitcommon.Logger, store domain.SupervisedStore, activity domain.ActivityRegistry,
) {
	(&metricsSampler{logger: logger, supervised: store, activity: activity}).sample()
}
