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

// TestLiveDiscovery_NewRollupResolvedImmediately covers dynamic rollup discovery: a rollup attached
// to the manager AFTER Start (announced via a CreateNewRollup event) already exposes a bridge service
// source, so it is resolved and served live without a restart.
func TestLiveDiscovery_NewRollupResolvedImmediately(t *testing.T) {
	backend, auth := newTestBackend(t)
	mgrAddr, _ := deployRollupManagerWithRollups(t, backend, auth, 1)

	cfg := baseTestConfig(mgrAddr)

	f := startFinder(t, cfg, Options{
		EthClient:     newTestEthClient(backend),
		HealthChecker: newMapHealthChecker(nil),
	})

	const newNetworkID = uint32(2)
	_, err := f.GetURL(newNetworkID)
	require.ErrorIs(t, err, ErrURLNotFound, "network must be unknown before it is announced")

	sleepPastSeedTick(testPollInterval)

	// Deploy a brand-new rollup, give it a resolvable source, then announce it on the manager. The
	// finder resolves the source via a direct on-chain read during discovery (not via the metadata
	// event), so only the CreateNewRollup event needs to be observed.
	newRollup := deployStandaloneRollup(t, backend, auth, newNetworkID)
	const metadataURL = "https://new-rollup.example.com:5577"
	_, err = newRollup.contract.SetAggchainMetadata(auth, MetadataBridgeServiceURLKey, metadataURL)
	require.NoError(t, err)
	backend.Commit()

	mgr := newRollupManagerContract(t, backend, mgrAddr)
	_, err = mgr.EmitCreateNewRollup(auth, newNetworkID, newRollup.addr)
	require.NoError(t, err)
	backend.Commit()

	require.Eventually(t, func() bool {
		got, err := f.GetURL(newNetworkID)
		return err == nil && got.BridgeURL == metadataURL
	}, testEventuallyWait, testEventuallyTick, "expected newly attached rollup to be discovered live")

	entry, ok := f.cache.get(newNetworkID)
	require.True(t, ok)
	require.Equal(t, SourceMetadata, entry.source)
}

// TestLiveDiscovery_NewRollupNoSourceThenHealedByEvent covers the second discovery path: a rollup
// announced via AddExistingRollup that exposes no bridge service source yet is registered (watched)
// but left without a cache entry, and a subsequent SetTrustedSequencerURL event on it then populates
// the entry. This proves discovery adds the new rollup's address to the watched set.
func TestLiveDiscovery_NewRollupNoSourceThenHealedByEvent(t *testing.T) {
	backend, auth := newTestBackend(t)
	mgrAddr, _ := deployRollupManagerWithRollups(t, backend, auth, 1)

	cfg := baseTestConfig(mgrAddr)

	f := startFinder(t, cfg, Options{
		EthClient:     newTestEthClient(backend),
		HealthChecker: newMapHealthChecker(nil),
	})

	const newNetworkID = uint32(2)

	sleepPastSeedTick(testPollInterval)

	// Announce a new rollup that has no bridge service source yet.
	newRollup := deployStandaloneRollup(t, backend, auth, newNetworkID)
	mgr := newRollupManagerContract(t, backend, mgrAddr)
	_, err := mgr.EmitAddExistingRollup(auth, newNetworkID, newRollup.addr)
	require.NoError(t, err)
	backend.Commit()

	// Give discovery several ticks to register the address before the URL event is emitted, so the
	// event falls within the (now-extended) watched-address filter.
	sleepPastSeedTick(testPollInterval)
	sleepPastSeedTick(testPollInterval)

	_, err = f.GetURL(newNetworkID)
	require.ErrorIs(t, err, ErrURLNotFound, "no-source discovered rollup must not have a cache entry yet")

	// Now the rollup publishes a trusted sequencer URL; the finder must pick it up because discovery
	// added the rollup contract to the watched set.
	_, err = newRollup.contract.SetTrustedSequencerURL(auth, "https://seq.example.com:8545")
	require.NoError(t, err)
	backend.Commit()

	require.Eventually(t, func() bool {
		got, err := f.GetURL(newNetworkID)
		return err == nil && got.BridgeURL != ""
	}, testEventuallyWait, testEventuallyTick, "expected discovered rollup to be healed by a later URL event")

	got, err := f.GetURL(newNetworkID)
	require.NoError(t, err)
	require.Contains(t, got.BridgeURL, fmt.Sprintf(":%d", DefaultBridgeServicePort))
	require.Equal(t, "https://seq.example.com:8545", got.JSONRPCURL,
		"the healing SetTrustedSequencerURL event must also install the json-rpc endpoint")
}

// TestLiveDiscovery_IgnoredNetworkIsNeverRegistered verifies that a CreateNewRollup event announcing
// a networkID listed in Config.IgnoreNetworkIDs is a no-op: no cache entry is installed and the
// rollup's contract address is never added to the routing table or the watched-address set, even
// though it exposes a perfectly resolvable on-chain source.
func TestLiveDiscovery_IgnoredNetworkIsNeverRegistered(t *testing.T) {
	backend, auth := newTestBackend(t)
	mgrAddr, _ := deployRollupManagerWithRollups(t, backend, auth, 1)

	const ignoredNetworkID = uint32(2)

	cfg := baseTestConfig(mgrAddr)
	cfg.IgnoreNetworkIDs = []uint32{ignoredNetworkID}

	f := startFinder(t, cfg, Options{
		EthClient:     newTestEthClient(backend),
		HealthChecker: newMapHealthChecker(nil),
	})

	sleepPastSeedTick(testPollInterval)

	newRollup := deployStandaloneRollup(t, backend, auth, ignoredNetworkID)
	const metadataURL = "https://ignored-new-rollup.example.com:5577"
	_, err := newRollup.contract.SetAggchainMetadata(auth, MetadataBridgeServiceURLKey, metadataURL)
	require.NoError(t, err)
	backend.Commit()

	mgr := newRollupManagerContract(t, backend, mgrAddr)
	_, err = mgr.EmitCreateNewRollup(auth, ignoredNetworkID, newRollup.addr)
	require.NoError(t, err)
	backend.Commit()

	// Give the listener several ticks to (not) act on the event.
	sleepPastSeedTick(testPollInterval)
	sleepPastSeedTick(testPollInterval)

	_, err = f.GetURL(ignoredNetworkID)
	require.ErrorIs(t, err, ErrURLNotFound, "an ignored network must never be discovered live")
	require.NotContains(t, f.addrToNetworkID, newRollup.addr,
		"an ignored network's contract must never be registered in the routing table")
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
	require.Contains(t, initial.BridgeURL, fmt.Sprintf(":%d", DefaultBridgeServicePort))

	sleepPastSeedTick(testPollInterval)

	const metadataURL = "https://metadata.example.com:5577"
	_, err = rollups[0].contract.SetAggchainMetadata(auth, MetadataBridgeServiceURLKey, metadataURL)
	require.NoError(t, err)
	backend.Commit()

	require.Eventually(t, func() bool {
		got, err := f.GetURL(networkID)
		return err == nil && got.BridgeURL == metadataURL
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
	require.Equal(t, metadataURL, got.BridgeURL)

	sleepPastSeedTick(testPollInterval)

	const newSeqURL = "https://seq.example.com:8545"
	_, err = rollups[0].contract.SetTrustedSequencerURL(auth, newSeqURL)
	require.NoError(t, err)
	backend.Commit()

	// The json-rpc endpoint IS refreshed by the (bridge-wise rejected) sequencer event; waiting on it
	// also guarantees the event was scanned before the bridge-URL immutability assertion below.
	require.Eventually(t, func() bool {
		got, err := f.GetURL(networkID)
		return err == nil && got.JSONRPCURL == newSeqURL
	}, testEventuallyWait, testEventuallyTick,
		"SetTrustedSequencerURL must refresh the json-rpc endpoint of a metadata-sourced entry")

	got, err = f.GetURL(networkID)
	require.NoError(t, err)
	require.Equal(t, metadataURL, got.BridgeURL,
		"lower-priority sequencer event must not downgrade a metadata-sourced entry")
}

// TestLiveUpdate_MetadataClearedFallsBackToSequencer covers the fix for the finder getting stuck
// once an operator clears the on-chain BRIDGE_SERVICE_URL metadata: since metadata outranks a
// sequencer-derived URL, the empty AggchainMetadataSet event alone carries no usable candidate, but
// it must not be silently dropped either — the entry has to be re-resolved from scratch so a
// still-configured trustedSequencerURL can now take over.
func TestLiveUpdate_MetadataClearedFallsBackToSequencer(t *testing.T) {
	backend, auth := newTestBackend(t)
	mgrAddr, rollups := deployRollupManagerWithRollups(t, backend, auth, 1)
	const networkID = uint32(1)

	const seqURL = "https://seq.example.com:8545"
	_, err := rollups[0].contract.SetTrustedSequencerURL(auth, seqURL)
	require.NoError(t, err)
	backend.Commit()

	const metadataURL = "https://metadata.example.com:5577"
	_, err = rollups[0].contract.SetAggchainMetadata(auth, MetadataBridgeServiceURLKey, metadataURL)
	require.NoError(t, err)
	backend.Commit()

	cfg := baseTestConfig(mgrAddr)

	f := startFinder(t, cfg, Options{
		EthClient:     newTestEthClient(backend),
		HealthChecker: newMapHealthChecker(nil),
	})

	got, err := f.GetURL(networkID)
	require.NoError(t, err)
	require.Equal(t, metadataURL, got.BridgeURL)

	sleepPastSeedTick(testPollInterval)

	// Clear the metadata: emit AggchainMetadataSet again with an empty value.
	_, err = rollups[0].contract.SetAggchainMetadata(auth, MetadataBridgeServiceURLKey, "")
	require.NoError(t, err)
	backend.Commit()

	require.Eventually(t, func() bool {
		got, err := f.GetURL(networkID)
		return err == nil && got.BridgeURL != metadataURL
	}, testEventuallyWait, testEventuallyTick,
		"expected the entry to fall back to the sequencer-derived url once metadata was cleared")

	got, err = f.GetURL(networkID)
	require.NoError(t, err)
	require.Contains(t, got.BridgeURL, "seq.example.com")
	require.Contains(t, got.BridgeURL, fmt.Sprintf(":%d", DefaultBridgeServicePort))

	entry, ok := f.cache.get(networkID)
	require.True(t, ok)
	require.Equal(t, SourceSequencerURL, entry.source)
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
			return err == nil && got.BridgeURL != ""
		}, testEventuallyWait, testEventuallyTick, "expected first-ever install via SetTrustedSequencerURL")

		got, err := f.GetURL(networkID)
		require.NoError(t, err)
		require.Contains(t, got.BridgeURL, fmt.Sprintf(":%d", DefaultBridgeServicePort))
		require.Equal(t, "https://seq.example.com:8545", got.JSONRPCURL)
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
			return err == nil && got.BridgeURL == metadataURL
		}, testEventuallyWait, testEventuallyTick, "expected first-ever install via AggchainMetadataSet")
	})
}

// TestLiveUpdate_EventEmittedRightAfterStartIsNotMissed guards against the bug where the listener
// seeded lastScannedBlock to its own first-tick upper bound without scanning anything, permanently
// skipping any event emitted between Start's initial on-chain reads and that first tick (up to a
// whole pollInterval later). Seeding now happens inside Start (see newListener), anchored to the
// upper bound resolved right then, so an event committed immediately after Start returns — well
// before sleepPastSeedTick's window, unlike every other live-update test in this file — must still
// be picked up by the very first tick.
func TestLiveUpdate_EventEmittedRightAfterStartIsNotMissed(t *testing.T) {
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

	// Deliberately no sleepPastSeedTick here: the event is committed right away, before the first
	// tick would naturally fire pollInterval later.
	const seqURL = "https://seq.example.com:8545"
	_, err = rollups[0].contract.SetTrustedSequencerURL(auth, seqURL)
	require.NoError(t, err)
	backend.Commit()

	require.Eventually(t, func() bool {
		got, err := f.GetURL(networkID)
		return err == nil && got.BridgeURL != ""
	}, testEventuallyWait, testEventuallyTick,
		"event emitted right after Start must be picked up by the first tick, not silently skipped")
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
	require.Equal(t, deadMetadataURL, got.BridgeURL)

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
	require.Equal(t, deadMetadataURL, got.BridgeURL,
		"lower-priority sequencer event must be rejected outright even though it is healthy and current is unhealthy")
}
