package metrics

import (
	"testing"

	"github.com/agglayer/aggkit/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
)

func TestRegisterIsIdempotent(t *testing.T) {
	prometheus.Init()
	Register()
	require.NotPanics(t, Register, "duplicate registration must not panic")

	for _, name := range []string{logsOmissionConfirmedName, logsOmissionRangeRetriesName} {
		_, ok := prometheus.CounterVec(name)
		require.True(t, ok, name)
	}
}

func TestHelpers_UpdateSeries(t *testing.T) {
	prometheus.Init()
	Register()

	const id = "metrics-test-helpers"
	confirmed, ok := prometheus.CounterVec(logsOmissionConfirmedName)
	require.True(t, ok)
	retries, ok := prometheus.CounterVec(logsOmissionRangeRetriesName)
	require.True(t, ok)

	confirmedBefore := testutil.ToFloat64(confirmed.WithLabelValues(id))
	retriesBefore := testutil.ToFloat64(retries.WithLabelValues(id))

	AddLogsOmissionConfirmed(id, 3)
	IncLogsOmissionRangeRetry(id)

	require.Equal(t, confirmedBefore+3, testutil.ToFloat64(confirmed.WithLabelValues(id)))
	require.Equal(t, retriesBefore+1, testutil.ToFloat64(retries.WithLabelValues(id)))
}

func TestAddLogsOmissionConfirmedIgnoresNonPositive(t *testing.T) {
	prometheus.Init()
	Register()

	const id = "metrics-test-nonpositive"
	confirmed, ok := prometheus.CounterVec(logsOmissionConfirmedName)
	require.True(t, ok)
	before := testutil.ToFloat64(confirmed.WithLabelValues(id))

	AddLogsOmissionConfirmed(id, 0)
	AddLogsOmissionConfirmed(id, -2)

	require.Equal(t, before, testutil.ToFloat64(confirmed.WithLabelValues(id)))
}
