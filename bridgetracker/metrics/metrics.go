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
)

// Register the metrics for the bridgetracker package. aliveActivities is only registered when
// the activity endpoint is configured (withActivity), so it is absent from /metrics otherwise
// instead of being reported as a misleading zero
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
