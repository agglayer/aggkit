package bridgeservicefinder

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// Note: these tests use AggchainMetadataSet (source #2) rather than SetTrustedSequencerURL (source
// #3) as the vehicle for candidate URLs, because the sequencer-URL source substitutes the port to
// DefaultBridgeServicePort (see withPort), which would silently rewrite the exact httptest.Server
// URL/port these tests need to preserve verbatim to talk to their controllable health servers.

// TestHealthGating_HealthyCurrentRejectsUnhealthyCandidate covers matrix item #8a: a currently
// healthy cache entry must not be displaced by a candidate that probes unhealthy.
func TestHealthGating_HealthyCurrentRejectsUnhealthyCandidate(t *testing.T) {
	backend, auth := newTestBackend(t)
	mgrAddr, rollups := deployRollupManagerWithRollups(t, backend, auth, 1)
	const networkID = uint32(1)

	healthySrv := newHealthServer(t, true)
	_, err := rollups[0].contract.SetAggchainMetadata(auth, MetadataBridgeServiceURLKey, healthySrv.Server.URL)
	require.NoError(t, err)
	backend.Commit()

	cfg := baseTestConfig(mgrAddr)

	f := startFinder(t, cfg, Options{
		EthClient: newTestEthClient(backend),
	})

	got, err := f.GetURL(networkID)
	require.NoError(t, err)
	require.Equal(t, healthySrv.Server.URL, got)

	entry, ok := f.cache.get(networkID)
	require.True(t, ok)
	require.True(t, entry.healthy)

	sleepPastSeedTick(testPollInterval)

	deadURL := closedServerURL(t)
	_, err = rollups[0].contract.SetAggchainMetadata(auth, MetadataBridgeServiceURLKey, deadURL)
	require.NoError(t, err)
	backend.Commit()

	sleepPastSeedTick(testPollInterval)
	sleepPastSeedTick(testPollInterval)

	got, err = f.GetURL(networkID)
	require.NoError(t, err)
	require.Equal(t, healthySrv.Server.URL, got, "healthy current URL must not be displaced by an unreachable candidate")
}

// TestHealthGating_UnhealthyCurrentAlwaysReplaced covers matrix item #8b: when the current entry is
// unhealthy, a new candidate always replaces it, regardless of whether the candidate itself is
// healthy (b1) or unhealthy (b2).
func TestHealthGating_UnhealthyCurrentAlwaysReplaced(t *testing.T) {
	t.Run("candidate healthy", func(t *testing.T) {
		backend, auth := newTestBackend(t)
		mgrAddr, rollups := deployRollupManagerWithRollups(t, backend, auth, 1)
		const networkID = uint32(1)

		deadURL := closedServerURL(t)
		_, err := rollups[0].contract.SetAggchainMetadata(auth, MetadataBridgeServiceURLKey, deadURL)
		require.NoError(t, err)
		backend.Commit()

		cfg := baseTestConfig(mgrAddr)

		f := startFinder(t, cfg, Options{
			EthClient: newTestEthClient(backend),
		})

		entry, ok := f.cache.get(networkID)
		require.True(t, ok)
		require.False(t, entry.healthy)

		sleepPastSeedTick(testPollInterval)

		healthySrv := newHealthServer(t, true)
		_, err = rollups[0].contract.SetAggchainMetadata(auth, MetadataBridgeServiceURLKey, healthySrv.Server.URL)
		require.NoError(t, err)
		backend.Commit()

		require.Eventually(t, func() bool {
			got, err := f.GetURL(networkID)
			return err == nil && got == healthySrv.Server.URL
		}, testEventuallyWait, testEventuallyTick, "unhealthy current must be replaced by a healthy candidate")

		entry, ok = f.cache.get(networkID)
		require.True(t, ok)
		require.True(t, entry.healthy)
	})

	t.Run("candidate also unhealthy", func(t *testing.T) {
		backend, auth := newTestBackend(t)
		mgrAddr, rollups := deployRollupManagerWithRollups(t, backend, auth, 1)
		const networkID = uint32(1)

		deadURL1 := closedServerURL(t)
		_, err := rollups[0].contract.SetAggchainMetadata(auth, MetadataBridgeServiceURLKey, deadURL1)
		require.NoError(t, err)
		backend.Commit()

		cfg := baseTestConfig(mgrAddr)

		f := startFinder(t, cfg, Options{
			EthClient: newTestEthClient(backend),
		})

		entry, ok := f.cache.get(networkID)
		require.True(t, ok)
		require.False(t, entry.healthy)

		sleepPastSeedTick(testPollInterval)

		deadURL2 := closedServerURL(t)
		_, err = rollups[0].contract.SetAggchainMetadata(auth, MetadataBridgeServiceURLKey, deadURL2)
		require.NoError(t, err)
		backend.Commit()

		require.Eventually(t, func() bool {
			got, err := f.GetURL(networkID)
			return err == nil && got == deadURL2
		}, testEventuallyWait, testEventuallyTick,
			"unhealthy current must be replaced by a new candidate even if the candidate is also unhealthy")

		entry, ok = f.cache.get(networkID)
		require.True(t, ok)
		require.False(t, entry.healthy)
	})
}

// TestHealthGating_NoPriorEntryInstallsRegardlessOfHealth is a focused isolation test (using the
// mapHealthChecker double) confirming applyUpdate's "no prior entry: install" branch installs the
// candidate and records its probe result verbatim, for both a healthy and an unhealthy candidate.
func TestHealthGating_NoPriorEntryInstallsRegardlessOfHealth(t *testing.T) {
	c := newCache()
	res := newResolver(nil, DefaultBridgeServicePort)

	t.Run("healthy candidate", func(t *testing.T) {
		hc := newMapHealthChecker(map[string]bool{"http://healthy.example.com": true})
		lst := &listener{
			logger:        testLogger(),
			healthChecker: hc,
			resolver:      res,
			cache:         c,
		}

		lst.applyUpdate(context.Background(), 100, "http://healthy.example.com", SourceSequencerURL, dummyLog())

		entry, ok := c.get(100)
		require.True(t, ok)
		require.Equal(t, "http://healthy.example.com", entry.url)
		require.True(t, entry.healthy)
	})

	t.Run("unhealthy candidate", func(t *testing.T) {
		hc := newMapHealthChecker(map[string]bool{})
		lst := &listener{
			logger:        testLogger(),
			healthChecker: hc,
			resolver:      res,
			cache:         c,
		}

		lst.applyUpdate(context.Background(), 101, "http://unhealthy.example.com", SourceSequencerURL, dummyLog())

		entry, ok := c.get(101)
		require.True(t, ok)
		require.Equal(t, "http://unhealthy.example.com", entry.url)
		require.False(t, entry.healthy)
	})
}
