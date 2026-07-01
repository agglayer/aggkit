package bridgeservicefinder

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// startFinder is a small helper that constructs and starts a finder with the given config and
// options, failing the test on error, and returns the concrete *finder for white-box assertions.
// The Start context is cancelled automatically on test cleanup so the listener goroutine it spawns
// does not leak past the test.
func startFinder(t *testing.T, cfg Config, opts Options) *finder {
	t.Helper()

	if opts.Logger == nil {
		opts.Logger = testLogger()
	}

	f, err := New(cfg, opts)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	require.NoError(t, f.Start(ctx))

	concrete, ok := f.(*finder)
	require.True(t, ok)

	return concrete
}

// TestLiveUpdate_SequencerThenMetadataUpgrades covers matrix item #4a: a sequencer-sourced network
// is upgraded to metadata (higher priority) via a live AggchainMetadataSet event.
func TestLiveUpdate_SequencerThenMetadataUpgrades(t *testing.T) {
	backend, auth := newTestBackend(t)
	mgrAddr, rollups := deployRollupManagerWithRollups(t, backend, auth, 1)
	const networkID = uint32(1)

	_, err := rollups[0].contract.SetTrustedSequencerURL(auth, "https://seq.example.com:8545")
	require.NoError(t, err)
	backend.Commit()

	cfg := baseTestConfig(mgrAddr)

	f := startFinder(t, cfg, Options{
		EthClient:     newTestEthClient(backend),
		HealthChecker: newMapHealthChecker(nil),
	})

	initial, err := f.GetURL(networkID)
	require.NoError(t, err)
	require.Contains(t, initial, fmt.Sprintf(":%d", DefaultBridgeServicePort))

	sleepPastSeedTick(testPollInterval)

	const metadataURL = "https://metadata.example.com:5577"
	_, err = rollups[0].contract.SetAggchainMetadata(auth, MetadataBridgeServiceURLKey, metadataURL)
	require.NoError(t, err)
	backend.Commit()

	require.Eventually(t, func() bool {
		got, err := f.GetURL(networkID)
		return err == nil && got == metadataURL
	}, testEventuallyWait, testEventuallyTick, "expected cache to upgrade to metadata-sourced URL")

	entry, ok := f.cache.get(networkID)
	require.True(t, ok)
	require.Equal(t, SourceMetadata, entry.source)
}

// TestLiveUpdate_MetadataThenSequencerRejected covers matrix item #4b: a metadata-sourced network
// must NOT be downgraded by a subsequent SetTrustedSequencerURL event.
func TestLiveUpdate_MetadataThenSequencerRejected(t *testing.T) {
	backend, auth := newTestBackend(t)
	mgrAddr, rollups := deployRollupManagerWithRollups(t, backend, auth, 1)
	const networkID = uint32(1)

	const metadataURL = "https://metadata.example.com:5577"
	_, err := rollups[0].contract.SetAggchainMetadata(auth, MetadataBridgeServiceURLKey, metadataURL)
	require.NoError(t, err)
	backend.Commit()

	cfg := baseTestConfig(mgrAddr)

	f := startFinder(t, cfg, Options{
		EthClient:     newTestEthClient(backend),
		HealthChecker: newMapHealthChecker(nil),
	})

	got, err := f.GetURL(networkID)
	require.NoError(t, err)
	require.Equal(t, metadataURL, got)

	sleepPastSeedTick(testPollInterval)

	_, err = rollups[0].contract.SetTrustedSequencerURL(auth, "https://seq.example.com:8545")
	require.NoError(t, err)
	backend.Commit()

	// Generous wait past when the event should have been scanned; the URL must remain unchanged.
	sleepPastSeedTick(testPollInterval)
	sleepPastSeedTick(testPollInterval)

	got, err = f.GetURL(networkID)
	require.NoError(t, err)
	require.Equal(t, metadataURL, got, "lower-priority sequencer event must not downgrade a metadata-sourced entry")
}

// TestLiveUpdate_FirstInstallViaEventOnNoSourceNetwork covers matrix item #7: a network that had
// ErrNoSourceAvailable at Start (no cache entry at all) gets its first-ever cache entry installed
// purely via a later live event, one test per event type.
func TestLiveUpdate_FirstInstallViaEventOnNoSourceNetwork(t *testing.T) {
	t.Run("via SetTrustedSequencerURL", func(t *testing.T) {
		backend, auth := newTestBackend(t)
		mgrAddr, rollups := deployRollupManagerWithRollups(t, backend, auth, 1)
		const networkID = uint32(1)

		cfg := baseTestConfig(mgrAddr)

		f := startFinder(t, cfg, Options{
			EthClient:     newTestEthClient(backend),
			HealthChecker: newMapHealthChecker(nil),
		})

		_, err := f.GetURL(networkID)
		require.ErrorIs(t, err, ErrURLNotFound)

		sleepPastSeedTick(testPollInterval)

		_, err = rollups[0].contract.SetTrustedSequencerURL(auth, "https://seq.example.com:8545")
		require.NoError(t, err)
		backend.Commit()

		require.Eventually(t, func() bool {
			got, err := f.GetURL(networkID)
			return err == nil && got != ""
		}, testEventuallyWait, testEventuallyTick, "expected first-ever install via SetTrustedSequencerURL")

		got, err := f.GetURL(networkID)
		require.NoError(t, err)
		require.Contains(t, got, fmt.Sprintf(":%d", DefaultBridgeServicePort))
	})

	t.Run("via AggchainMetadataSet", func(t *testing.T) {
		backend, auth := newTestBackend(t)
		mgrAddr, rollups := deployRollupManagerWithRollups(t, backend, auth, 1)
		const networkID = uint32(1)

		cfg := baseTestConfig(mgrAddr)

		f := startFinder(t, cfg, Options{
			EthClient:     newTestEthClient(backend),
			HealthChecker: newMapHealthChecker(nil),
		})

		_, err := f.GetURL(networkID)
		require.ErrorIs(t, err, ErrURLNotFound)

		sleepPastSeedTick(testPollInterval)

		const metadataURL = "https://metadata.example.com:5577"
		_, err = rollups[0].contract.SetAggchainMetadata(auth, MetadataBridgeServiceURLKey, metadataURL)
		require.NoError(t, err)
		backend.Commit()

		require.Eventually(t, func() bool {
			got, err := f.GetURL(networkID)
			return err == nil && got == metadataURL
		}, testEventuallyWait, testEventuallyTick, "expected first-ever install via AggchainMetadataSet")
	})
}

// TestLiveUpdate_SameURLSameSourceIsNoop covers matrix item #9: emitting the identical
// SetTrustedSequencerURL event twice must not change the cached URL.
func TestLiveUpdate_SameURLSameSourceIsNoop(t *testing.T) {
	backend, auth := newTestBackend(t)
	mgrAddr, rollups := deployRollupManagerWithRollups(t, backend, auth, 1)
	const networkID = uint32(1)

	const seqURL = "https://seq.example.com:8545"
	_, err := rollups[0].contract.SetTrustedSequencerURL(auth, seqURL)
	require.NoError(t, err)
	backend.Commit()

	cfg := baseTestConfig(mgrAddr)

	f := startFinder(t, cfg, Options{
		EthClient:     newTestEthClient(backend),
		HealthChecker: newMapHealthChecker(nil),
	})

	initial, err := f.GetURL(networkID)
	require.NoError(t, err)

	sleepPastSeedTick(testPollInterval)

	// Emit the identical event twice.
	_, err = rollups[0].contract.SetTrustedSequencerURL(auth, seqURL)
	require.NoError(t, err)
	backend.Commit()

	_, err = rollups[0].contract.SetTrustedSequencerURL(auth, seqURL)
	require.NoError(t, err)
	backend.Commit()

	sleepPastSeedTick(testPollInterval)
	sleepPastSeedTick(testPollInterval)

	got, err := f.GetURL(networkID)
	require.NoError(t, err)
	require.Equal(t, initial, got, "identical source+url event must be a no-op")
}

// TestLiveUpdate_CrossTierRejectionIsUnconditional covers matrix item #10: a lower-priority event
// (SetTrustedSequencerURL) targeting a metadata-sourced, UNHEALTHY entry is rejected outright,
// regardless of the candidate's own health outcome, because the priority check happens before the
// health gate.
func TestLiveUpdate_CrossTierRejectionIsUnconditional(t *testing.T) {
	backend, auth := newTestBackend(t)
	mgrAddr, rollups := deployRollupManagerWithRollups(t, backend, auth, 1)
	const networkID = uint32(1)

	deadMetadataURL := closedServerURL(t)
	_, err := rollups[0].contract.SetAggchainMetadata(auth, MetadataBridgeServiceURLKey, deadMetadataURL)
	require.NoError(t, err)
	backend.Commit()

	cfg := baseTestConfig(mgrAddr)

	f := startFinder(t, cfg, Options{
		EthClient: newTestEthClient(backend),
		// Real HTTP health checker (default) so the dead metadata URL is unhealthy at Start, and a
		// candidate sequencer URL pointing at a live server would be genuinely healthy.
	})

	got, err := f.GetURL(networkID)
	require.NoError(t, err)
	require.Equal(t, deadMetadataURL, got)

	entry, ok := f.cache.get(networkID)
	require.True(t, ok)
	require.False(t, entry.healthy, "metadata entry must be recorded unhealthy at start")

	sleepPastSeedTick(testPollInterval)

	healthySrv := newHealthServer(t, true)
	_, err = rollups[0].contract.SetTrustedSequencerURL(auth, healthySrv.Server.URL)
	require.NoError(t, err)
	backend.Commit()

	sleepPastSeedTick(testPollInterval)
	sleepPastSeedTick(testPollInterval)

	got, err = f.GetURL(networkID)
	require.NoError(t, err)
	require.Equal(t, deadMetadataURL, got,
		"lower-priority sequencer event must be rejected outright even though it is healthy and current is unhealthy")
}
