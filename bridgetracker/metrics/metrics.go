package metrics

import (
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/prometheus"
	prometheusClient "github.com/prometheus/client_golang/prometheus"
)

const (
	namespace       = "bridgetracker"
	cacheSizeBytes  = "cache_size_bytes"
	aliveTrackers   = "alive_trackers"
	aliveActivities = "alive_activities"

	activityReorgsDetected   = "activity_reorgs_detected_total"
	activityEntriesCorrected = "activity_entries_corrected_total"
	activityEntriesRemoved   = "activity_entries_removed_total"
)

// Register the metrics for the bridgetracker package. The activity metrics (aliveActivities and
// the activity reorg counters) are only registered when the activity endpoint is configured
// (withActivity), so they are absent from /metrics otherwise instead of being reported as a
// misleading zero
func Register(withActivity bool) {
	gauges := []prometheusClient.GaugeOpts{
		{
			Namespace: namespace,
			Name:      cacheSizeBytes,
			Help:      "On-disk size in bytes of the SQLite cache (0 when the in-memory backend is used)",
		},
		{
			Namespace: namespace,
			Name:      aliveTrackers,
			Help:      "Number of supervised bridges not yet in a terminal state",
		},
	}
	if withActivity {
		gauges = append(gauges, prometheusClient.GaugeOpts{
			Namespace: namespace,
			Name:      aliveActivities,
			Help:      "Number of from_addresses currently supervised by the activity subsystem",
		})
	}
	prometheus.RegisterGauges(gauges...)
	if withActivity {
		prometheus.RegisterCounters(
			prometheusClient.CounterOpts{
				Namespace: namespace,
				Name:      activityReorgsDetected,
				Help: "Number of activity scans of one network's bridge service that found a reorg " +
					"(a known non-final bridge moved or gone)",
			},
			prometheusClient.CounterOpts{
				Namespace: namespace,
				Name:      activityEntriesCorrected,
				Help:      "Number of activity entries re-read at a different block or transaction and corrected",
			},
			prometheusClient.CounterOpts{
				Namespace: namespace,
				Name:      activityEntriesRemoved,
				Help:      "Number of activity entries removed because a reorg dropped their bridge",
			},
		)
	}

	log.Info("Registered prometheus bridgetracker metrics")
}

// SetCacheSizeBytes sets the on-disk size of the SQLite cache
func SetCacheSizeBytes(value int64) {
	prometheus.GaugeSet(cacheSizeBytes, float64(value))
}

// SetAliveTrackers sets the number of supervised bridges not yet in a terminal state
func SetAliveTrackers(value int) {
	prometheus.GaugeSet(aliveTrackers, float64(value))
}

// SetAliveActivities sets the number of supervised from_addresses
func SetAliveActivities(value int) {
	prometheus.GaugeSet(aliveActivities, float64(value))
}

// IncActivityReorgsDetected counts one network scan of the activity endpoint that found a reorg
func IncActivityReorgsDetected() {
	prometheus.CounterInc(activityReorgsDetected)
}

// AddActivityEntriesCorrected counts activity entries corrected after a reorg moved their bridge
func AddActivityEntriesCorrected(n int) {
	prometheus.CounterAdd(activityEntriesCorrected, float64(n))
}

// AddActivityEntriesRemoved counts activity entries removed after a reorg dropped their bridge
func AddActivityEntriesRemoved(n int) {
	prometheus.CounterAdd(activityEntriesRemoved, float64(n))
}
