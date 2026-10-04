// Package metrics exposes the Prometheus series of the legacy EVM downloader (package sync).
package metrics

import (
	"github.com/agglayer/aggkit/prometheus"
	prometheusclient "github.com/prometheus/client_golang/prometheus"
)

const (
	namespace                    = "sync"
	logsOmissionConfirmedName    = "logs_omission_confirmed_total"
	logsOmissionRangeRetriesName = "logs_omission_range_retries_total"
	syncerLabel                  = "syncer"
	logsOmissionConfirmedHelp    = "eth_getLogs omissions confirmed by block-hash arbitration " +
		"(per check; a block re-confirmed after a range retry counts again)"
	logsOmissionRangeRetriesHelp = "Whole-range retries triggered by the eth_getLogs completeness check"
)

// Register registers the sync downloader metrics. It is idempotent and does nothing until
// prometheus.Init() has run. It deliberately does not use sync.Once: a call made before
// prometheus.Init() must not prevent a later call from registering the series.
func Register() {
	if _, ok := prometheus.CounterVec(logsOmissionConfirmedName); ok {
		return
	}
	prometheus.RegisterCounterVecs(
		prometheus.CounterVecOpts{
			CounterOpts: prometheusclient.CounterOpts{
				Namespace: namespace,
				Name:      logsOmissionConfirmedName,
				Help:      logsOmissionConfirmedHelp,
			},
			Labels: []string{syncerLabel},
		},
		prometheus.CounterVecOpts{
			CounterOpts: prometheusclient.CounterOpts{
				Namespace: namespace,
				Name:      logsOmissionRangeRetriesName,
				Help:      logsOmissionRangeRetriesHelp,
			},
			Labels: []string{syncerLabel},
		},
	)
}

// AddLogsOmissionConfirmed adds n to the confirmed eth_getLogs omissions counter of the given syncer.
// It does nothing if n is not positive.
func AddLogsOmissionConfirmed(syncerID string, n int) {
	if n <= 0 {
		return
	}
	prometheus.CounterVecAdd(logsOmissionConfirmedName, syncerID, float64(n))
}

// IncLogsOmissionRangeRetry increments the omission-triggered range retry counter of the given syncer.
func IncLogsOmissionRangeRetry(syncerID string) {
	prometheus.CounterVecInc(logsOmissionRangeRetriesName, syncerID)
}
