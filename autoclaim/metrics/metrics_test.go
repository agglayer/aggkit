package metrics

import (
	"testing"

	"github.com/agglayer/aggkit/prometheus"
	prometheusclient "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
)

func TestRegister_ExposesSeries(t *testing.T) {
	prometheus.Init()
	Register()
	require.NotPanics(t, Register, "duplicate registration must not panic")

	for _, name := range []string{l1ToL2StalledDestinationsName, l2ToLxStalledDestinationsName} {
		_, ok := prometheus.Gauge(name)
		require.True(t, ok, name)
	}
	for _, name := range []string{destinationErrorsName, skippedAlreadyClaimedName} {
		_, ok := prometheus.CounterVec(name)
		require.True(t, ok, name)
	}

	// Series appear in the default registry once they have a sample.
	IncDestinationError(DetectorL2ToLx, 4242)
	IncSkippedAlreadyClaimed(DetectorL2ToLx, 4242)
	SetStalledDestinations(DetectorL2ToLx, 0)
	families, err := prometheusclientGather()
	require.NoError(t, err)
	for _, want := range []string{
		"autoclaim_l1_to_l2_stalled_destinations",
		"autoclaim_l2_to_lx_stalled_destinations",
		"autoclaim_detector_destination_errors_total",
		"autoclaim_detector_skipped_already_claimed_total",
	} {
		require.Contains(t, families, want)
	}
}

func TestHelpers_UpdateSeries(t *testing.T) {
	prometheus.Init()
	Register()

	SetStalledDestinations(DetectorL2ToLx, 3)
	gauge, ok := prometheus.Gauge(l2ToLxStalledDestinationsName)
	require.True(t, ok)
	require.Equal(t, 3.0, testutil.ToFloat64(gauge))

	vec, ok := prometheus.CounterVec(destinationErrorsName)
	require.True(t, ok)
	counter := vec.WithLabelValues(string(DetectorL2ToLx), "7777")
	before := testutil.ToFloat64(counter)
	IncDestinationError(DetectorL2ToLx, 7777)
	require.Equal(t, before+1, testutil.ToFloat64(counter))
}

// prometheusclientGather returns the names of the metric families in the default registry.
func prometheusclientGather() ([]string, error) {
	mfs, err := prometheusclient.DefaultGatherer.Gather()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(mfs))
	for _, mf := range mfs {
		names = append(names, mf.GetName())
	}
	return names, nil
}
