// Package metrics exposes the Prometheus series of the autoclaim bridge detectors.
package metrics

import (
	"strconv"

	"github.com/agglayer/aggkit/prometheus"
	prometheusclient "github.com/prometheus/client_golang/prometheus"
)

// Detector identifies the bridge detector a metric sample belongs to.
type Detector string

const (
	// DetectorL1ToL2 is the L1-to-L2 bridge detector.
	DetectorL1ToL2 Detector = "l1_to_l2"
	// DetectorL2ToLx is the L2-to-Lx bridge detector.
	DetectorL2ToLx Detector = "l2_to_lx"

	namespace                     = "autoclaim"
	l1ToL2StalledDestinationsName = "l1_to_l2_stalled_destinations"
	l2ToLxStalledDestinationsName = "l2_to_lx_stalled_destinations"
	destinationErrorsName         = "detector_destination_errors_total"
	skippedAlreadyClaimedName     = "detector_skipped_already_claimed_total"
	detectorLabel                 = "detector"
	destinationLabel              = "destination"
)

// Register registers the autoclaim detector metrics. It is a no-op until prometheus.Init() has run,
// and a duplicate registration only logs a warning.
func Register() {
	prometheus.RegisterGauges(
		prometheusclient.GaugeOpts{
			Namespace: namespace,
			Name:      l1ToL2StalledDestinationsName,
			Help:      "Number of destinations currently failing or backed off in the L1-to-L2 bridge detector",
		},
		prometheusclient.GaugeOpts{
			Namespace: namespace,
			Name:      l2ToLxStalledDestinationsName,
			Help:      "Number of destinations currently failing or backed off in the L2-to-Lx bridge detector",
		},
	)
	prometheus.RegisterCounterVecs(
		prometheus.CounterVecOpts{
			CounterOpts: prometheusclient.CounterOpts{
				Namespace: namespace,
				Name:      destinationErrorsName,
				Help:      "Number of failing detector polls per detector and destination (one per back-off window)",
			},
			Labels: []string{detectorLabel, destinationLabel},
		},
		prometheus.CounterVecOpts{
			CounterOpts: prometheusclient.CounterOpts{
				Namespace: namespace,
				Name:      skippedAlreadyClaimedName,
				Help:      "Number of bridges skipped by a detector because they were already claimed",
			},
			Labels: []string{detectorLabel, destinationLabel},
		},
	)
}

// SetStalledDestinations sets the number of stalled destinations of the given detector.
func SetStalledDestinations(detector Detector, n int) {
	switch detector {
	case DetectorL1ToL2:
		prometheus.GaugeSet(l1ToL2StalledDestinationsName, float64(n))
	case DetectorL2ToLx:
		prometheus.GaugeSet(l2ToLxStalledDestinationsName, float64(n))
	}
}

// IncDestinationError increments the error counter of the given detector and destination network.
func IncDestinationError(detector Detector, destination uint32) {
	prometheus.CounterVecInc(destinationErrorsName, string(detector), strconv.FormatUint(uint64(destination), 10))
}

// IncSkippedAlreadyClaimed increments the already-claimed skip counter of the given detector and destination.
func IncSkippedAlreadyClaimed(detector Detector, destination uint32) {
	prometheus.CounterVecInc(skippedAlreadyClaimedName, string(detector), strconv.FormatUint(uint64(destination), 10))
}
