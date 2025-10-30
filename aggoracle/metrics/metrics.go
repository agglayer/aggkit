package metrics

import (
	"time"

	"github.com/agglayer/aggkit/prometheus"
	prometheusclient "github.com/prometheus/client_golang/prometheus"
)

const (
	prefix                = "aggoracle_"
	gerProcessDuration    = prefix + "ger_processing_duration_seconds"
	gerProcessCount       = prefix + "ger_processing_trigger_total"
	gerProcessErrorsCount = prefix + "ger_processing_errors_total"
)

// Register the metrics for the aggoracle package
func Register() {
	gerProcessingDuration := prometheusclient.HistogramOpts{
		Name: gerProcessDuration,
		Help: "[AGGORACLE] Duration in seconds for processing a Global Exit Root.",
	}

	gerProcessTriggerCount := prometheusclient.CounterOpts{
		Name: gerProcessCount,
		Help: "[AGGORACLE] Total number of GER processing triggers.",
	}

	gerProcessErrorCount := prometheusclient.CounterOpts{
		Name: gerProcessErrorsCount,
		Help: "[AGGORACLE] Total number of GER processing errors, labeled by error type.",
	}

	prometheus.RegisterHistograms(gerProcessingDuration)
	prometheus.RegisterCounters(gerProcessTriggerCount, gerProcessErrorCount)
}

// IncGERProcessCount increments the GER processing trigger counter
func IncGERProcessCount() {
	prometheus.CounterInc(gerProcessCount)
}

// IncGERProcessCount increments the GER processing error counter
func IncGERProcessErrCount() {
	prometheus.CounterInc(gerProcessErrorsCount)
}

// ObserveGERProcessDuration records the duration taken to process a GER
func ObserveGERProcessDuration(d time.Duration) {
	prometheus.HistogramObserve(gerProcessDuration, d.Seconds())
}
