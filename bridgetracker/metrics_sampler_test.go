package bridgetracker_test

import (
	"errors"
	"testing"

	"github.com/agglayer/aggkit/bridgetracker"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/metrics"
	"github.com/agglayer/aggkit/bridgetracker/mocks"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/prometheus"
	"github.com/ethereum/go-ethereum/common"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

// cacheStatsStore is a SupervisedStore that also implements domain.CacheStatsProvider
type cacheStatsStore struct {
	*mocks.SupervisedStore
	stats domain.CacheStats
	err   error
}

func (c *cacheStatsStore) CacheStats() (domain.CacheStats, error) { return c.stats, c.err }

func gaugeValue(t *testing.T, name string) float64 {
	t.Helper()
	g, ok := prometheus.Gauge(name)
	require.True(t, ok, "gauge %s not registered", name)
	var m dto.Metric
	require.NoError(t, g.Write(&m))
	return m.GetGauge().GetValue()
}

func sampleOnce(store domain.SupervisedStore, activity domain.ActivityRegistry) {
	bridgetracker.SampleMetricsOnce(log.WithFields("module", "test"), store, activity)
}

func TestMetricsSamplerDiskBackedWithActivity(t *testing.T) {
	prometheus.Init()
	metrics.Register(true)

	store := &cacheStatsStore{SupervisedStore: mocks.NewSupervisedStore(t), stats: domain.CacheStats{SizeBytes: 4096}}
	store.EXPECT().GetTrackerActives((*uint32)(nil)).Return([]*domain.TrackingData{{}, {}}, nil)
	activity := mocks.NewActivityRegistry(t)
	activity.EXPECT().GetActiveAddresses().Return([]common.Address{{1}, {2}, {3}}, nil)

	sampleOnce(store, activity)

	require.EqualValues(t, 4096, gaugeValue(t, "cache_size_bytes"))
	require.EqualValues(t, 2, gaugeValue(t, "alive_trackers"))
	require.EqualValues(t, 3, gaugeValue(t, "alive_activities"))
}

func TestMetricsSamplerMemoryBackedReportsZeroSize(t *testing.T) {
	prometheus.Init()
	metrics.Register(false)

	store := mocks.NewSupervisedStore(t)
	store.EXPECT().GetTrackerActives((*uint32)(nil)).Return(nil, nil)
	metrics.SetCacheSizeBytes(999)

	sampleOnce(store, nil)

	require.EqualValues(t, 0, gaugeValue(t, "cache_size_bytes"))
	require.EqualValues(t, 0, gaugeValue(t, "alive_trackers"))
}

func TestMetricsSamplerKeepsPreviousValueOnError(t *testing.T) {
	prometheus.Init()
	metrics.Register(true)
	metrics.SetCacheSizeBytes(111)
	metrics.SetAliveTrackers(7)
	metrics.SetAliveActivities(5)

	store := &cacheStatsStore{SupervisedStore: mocks.NewSupervisedStore(t), err: errors.New("boom")}
	store.EXPECT().GetTrackerActives((*uint32)(nil)).Return(nil, errors.New("boom"))
	activity := mocks.NewActivityRegistry(t)
	activity.EXPECT().GetActiveAddresses().Return(nil, errors.New("boom"))

	sampleOnce(store, activity)

	require.EqualValues(t, 111, gaugeValue(t, "cache_size_bytes"))
	require.EqualValues(t, 7, gaugeValue(t, "alive_trackers"))
	require.EqualValues(t, 5, gaugeValue(t, "alive_activities"))
}
