package metrics

import (
	"time"

	"github.com/agglayer/aggkit/prometheus"
	prometheusclient "github.com/prometheus/client_golang/prometheus"
)

const (
	namespace             = "aggoracle"
	gerProcessDuration    = "ger_processing_duration_seconds"
	gerProcessCount       = "ger_processing_trigger_total"
	gerProcessErrorsCount = "ger_processing_errors_total"
)

// Register the metrics for the aggoracle package
func Register() {
	gerProcessingDuration := prometheusclient.HistogramOpts{
		Namespace: namespace,
		Name:      gerProcessDuration,
		Help:      "Duration in seconds for processing a Global Exit Root.",
	}

	gerProcessTriggerCount := prometheusclient.CounterOpts{
		Namespace: namespace,
		Name:      gerProcessCount,
		Help:      "Total number of GER processing triggers.",
	}

	gerProcessErrorCount := prometheusclient.CounterOpts{
		Namespace: namespace,
		Name:      gerProcessErrorsCount,
		Help:      "Total number of GER processing errors.",
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
