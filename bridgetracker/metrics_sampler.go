package bridgetracker

import (
	"context"
	"time"

	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/metrics"
	aggkitcommon "github.com/agglayer/aggkit/common"
)

// DefaultMetricsSampleInterval is how often the Prometheus gauges are refreshed
const DefaultMetricsSampleInterval = 15 * time.Second

// metricsSampler periodically publishes the supervised registry/activity subsystem's size
// (cache footprint, alive counts) as Prometheus gauges. The repo's prometheus helpers are
// push-style (GaugeSet), hence the polling loop. It only reads from the stores
type metricsSampler struct {
	logger     aggkitcommon.Logger
	supervised domain.SupervisedStore
	// activity is nil when the activity endpoint is not configured
	activity domain.ActivityRegistry
	interval time.Duration
}

// StartMetricsSampler registers the bridgetracker Prometheus metrics and starts refreshing them
// every interval (DefaultMetricsSampleInterval if <= 0) until ctx is done. The gauges are
// refreshed once immediately. Call it only when Prometheus is enabled
func (b *BridgeTracker) StartMetricsSampler(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultMetricsSampleInterval
	}
	s := &metricsSampler{
		logger:     b.logger,
		supervised: b.supervised,
		activity:   b.activity,
		interval:   interval,
	}
	metrics.Register(s.activity != nil)
	go s.run(ctx)
}

func (s *metricsSampler) run(ctx context.Context) {
	s.sample()
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sample()
		}
	}
}

// sample refreshes every gauge. A failed read keeps the gauge's previous value (and logs),
// rather than reporting a misleading zero
func (s *metricsSampler) sample() {
	if provider, ok := s.supervised.(domain.CacheStatsProvider); ok {
		if stats, err := provider.CacheStats(); err != nil {
			s.logger.Warnf("bridgetracker: metrics reading cache size: %v", err)
		} else {
			metrics.SetCacheSizeBytes(stats.SizeBytes)
		}
	} else {
		metrics.SetCacheSizeBytes(0)
	}

	if active, err := s.supervised.GetTrackerActives(nil); err != nil {
		s.logger.Warnf("bridgetracker: metrics counting active trackers: %v", err)
	} else {
		metrics.SetAliveTrackers(len(active))
	}

	if s.activity != nil {
		if addrs, err := s.activity.GetActiveAddresses(); err != nil {
			s.logger.Warnf("bridgetracker: metrics counting active activity addresses: %v", err)
		} else {
			metrics.SetAliveActivities(len(addrs))
		}
	}
}
