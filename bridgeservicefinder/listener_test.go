package bridgeservicefinder

import (
	"context"
	"fmt"
	"testing"

	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
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

// TestLiveDiscovery_IgnoredNetworkNeverPendingEvenWithAutoRegisterDisabled verifies that the
// Config.IgnoreNetworkIDs check in discoverRollup precedes the Config.AutoRegisterNewNetworks gate:
// a CreateNewRollup event announcing a networkID that is both ignored AND would otherwise be
// blocked by AutoRegisterNewNetworks=false must never be recorded as pending. An ignored network is
// a deliberate operator decision, not a network "waiting to be activated", so it must not show up in
// Finder.PendingNetworks() regardless of the flag's value.
func TestLiveDiscovery_IgnoredNetworkNeverPendingEvenWithAutoRegisterDisabled(t *testing.T) {
	backend, auth := newTestBackend(t)
	mgrAddr, _ := deployRollupManagerWithRollups(t, backend, auth, 1)

	const ignoredNetworkID = uint32(2)

	cfg := baseTestConfig(mgrAddr)
	cfg.IgnoreNetworkIDs = []uint32{ignoredNetworkID}
	cfg.AutoRegisterNewNetworks = false

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
	require.Empty(t, f.PendingNetworks(),
		"an ignored network must never be recorded pending, even with AutoRegisterNewNetworks disabled")
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

// --- AutoRegisterNewNetworks=false gating tests ------------------------------------------------

// TestLiveDiscovery_NewRollupPendingWhenAutoRegisterDisabled covers the discoverRollup gate: with
// Config.AutoRegisterNewNetworks disabled, a rollup attached to the manager after Start (announced
// via CreateNewRollup) is never resolved or served, even though it exposes a perfectly resolvable
// source. It is instead recorded pending exactly once (repeated lifecycle events for the same
// address must not duplicate the record), addrToNetworkID/watchedAddresses are left untouched, and
// a later SetTrustedSequencerURL from that (never-watched) address is silently ignored.
func TestLiveDiscovery_NewRollupPendingWhenAutoRegisterDisabled(t *testing.T) {
	backend, auth := newTestBackend(t)
	mgrAddr, _ := deployRollupManagerWithRollups(t, backend, auth, 1)

	cfg := baseTestConfig(mgrAddr)
	cfg.AutoRegisterNewNetworks = false

	f := startFinder(t, cfg, Options{
		EthClient:     newTestEthClient(backend),
		HealthChecker: newMapHealthChecker(nil),
	})

	const newNetworkID = uint32(2)

	sleepPastSeedTick(testPollInterval)

	newRollup := deployStandaloneRollup(t, backend, auth, newNetworkID)
	const metadataURL = "https://pending-rollup.example.com:5577"
	_, err := newRollup.contract.SetAggchainMetadata(auth, MetadataBridgeServiceURLKey, metadataURL)
	require.NoError(t, err)
	backend.Commit()

	mgr := newRollupManagerContract(t, backend, mgrAddr)
	_, err = mgr.EmitCreateNewRollup(auth, newNetworkID, newRollup.addr)
	require.NoError(t, err)
	backend.Commit()

	require.Eventually(t, func() bool {
		return len(f.PendingNetworks()) == 1
	}, testEventuallyWait, testEventuallyTick, "expected the attached rollup to be recorded pending")

	_, err = f.GetURL(newNetworkID)
	require.ErrorIs(t, err, ErrURLNotFound,
		"a network pending because AutoRegisterNewNetworks is disabled must never be served")

	pending := f.PendingNetworks()
	require.Len(t, pending, 1)
	require.Equal(t, newNetworkID, pending[0].NetworkID)
	require.Equal(t, newRollup.addr, pending[0].RollupAddress)
	require.Equal(t, PendingReasonRollupAttached, pending[0].Reason)

	require.NotContains(t, f.addrToNetworkID, newRollup.addr,
		"a pending network's contract must never be registered in the routing table")

	// Re-announce the SAME rollup a second time: PendingNetworks must still list it exactly once.
	sleepPastSeedTick(testPollInterval)

	_, err = mgr.EmitCreateNewRollup(auth, newNetworkID, newRollup.addr)
	require.NoError(t, err)
	backend.Commit()

	sleepPastSeedTick(testPollInterval)
	sleepPastSeedTick(testPollInterval)

	require.Len(t, f.PendingNetworks(), 1,
		"repeated lifecycle events for the same pending network must not duplicate the pending record")

	// Because the address was never watched, a later SetTrustedSequencerURL from it must be ignored
	// outright: the log never even reaches the listener's address filter.
	_, err = newRollup.contract.SetTrustedSequencerURL(auth, "https://seq.example.com:8545")
	require.NoError(t, err)
	backend.Commit()

	sleepPastSeedTick(testPollInterval)
	sleepPastSeedTick(testPollInterval)

	_, err = f.GetURL(newNetworkID)
	require.ErrorIs(t, err, ErrURLNotFound,
		"a SetTrustedSequencerURL from a pending (unwatched) network must be ignored")
	require.Len(t, f.PendingNetworks(), 1)
}

// TestLiveDiscovery_AlreadyServedFromBridgeURLsNeverGoesPending covers a network pre-provisioned in
// Config.BridgeURLs (the documented way to make a network servable without AutoRegisterNewNetworks):
// when it is later attached to the rollup manager it must NOT be recorded as pending -
// discoverRollup's gate must consult the cache, not just addrToNetworkID, before deciding a network
// is new - and skipping the pending record must be ALL it skips: the rollup contract still has to be
// registered and watched so its JSON-RPC endpoint is resolved on-chain at discovery time and later
// SetTrustedSequencerURL events keep it fresh, which is the flag-independent refresh guarantee
// doc.go states.
func TestLiveDiscovery_AlreadyServedFromBridgeURLsNeverGoesPending(t *testing.T) {
	backend, auth := newTestBackend(t)
	mgrAddr, _ := deployRollupManagerWithRollups(t, backend, auth, 0)

	const preConfiguredNetworkID = uint32(5)
	const preConfiguredURL = "http://cfg.example.com:5577"

	cfg := baseTestConfig(mgrAddr)
	cfg.AutoRegisterNewNetworks = false
	cfg.BridgeURLs = map[uint32]string{preConfiguredNetworkID: preConfiguredURL}

	f := startFinder(t, cfg, Options{
		EthClient:     newTestEthClient(backend),
		HealthChecker: newMapHealthChecker(nil),
	})

	// The static override is installed at Start regardless of the flag, before the rollup is ever
	// attached on-chain.
	urls, err := f.GetURL(preConfiguredNetworkID)
	require.NoError(t, err)
	require.Equal(t, preConfiguredURL, urls.BridgeURL)

	sleepPastSeedTick(testPollInterval)

	// The rollup already announces a trusted sequencer URL when it is attached, so discovery has a
	// JSON-RPC endpoint to resolve on-chain for it (the bridge URL stays the config override).
	const firstSequencerURL = "https://seq-1.example.com:8545"

	newRollup := deployStandaloneRollup(t, backend, auth, preConfiguredNetworkID)
	_, err = newRollup.contract.SetTrustedSequencerURL(auth, firstSequencerURL)
	require.NoError(t, err)
	backend.Commit()

	mgr := newRollupManagerContract(t, backend, mgrAddr)
	_, err = mgr.EmitCreateNewRollup(auth, preConfiguredNetworkID, newRollup.addr)
	require.NoError(t, err)
	backend.Commit()

	require.Eventually(t, func() bool {
		got, err := f.GetURL(preConfiguredNetworkID)
		return err == nil && got.JSONRPCURL == firstSequencerURL
	}, testEventuallyWait, testEventuallyTick,
		"discovery must resolve the json-rpc endpoint of a network served from a static override")

	require.Empty(t, f.PendingNetworks(),
		"a network already served from a static BridgeURLs override must never be recorded pending")

	urls, err = f.GetURL(preConfiguredNetworkID)
	require.NoError(t, err)
	require.Equal(t, preConfiguredURL, urls.BridgeURL,
		"the static override must keep serving the network unaffected by the later on-chain attach")

	entry, ok := f.cache.get(preConfiguredNetworkID)
	require.True(t, ok)
	require.Equal(t, SourceConfig, entry.source,
		"discovery must not downgrade a config-sourced entry to an on-chain source")

	require.Contains(t, f.addrToNetworkID, newRollup.addr,
		"the rollup contract of a network served from a static override must still be registered")

	// The address being watched is what makes a later URL event observable: refresh the trusted
	// sequencer URL and the json-rpc endpoint must follow, while the config bridge URL does not.
	const refreshedSequencerURL = "https://seq-2.example.com:9545"

	_, err = newRollup.contract.SetTrustedSequencerURL(auth, refreshedSequencerURL)
	require.NoError(t, err)
	backend.Commit()

	require.Eventually(t, func() bool {
		got, err := f.GetURL(preConfiguredNetworkID)
		return err == nil && got.JSONRPCURL == refreshedSequencerURL
	}, testEventuallyWait, testEventuallyTick,
		"a later SetTrustedSequencerURL must refresh the json-rpc endpoint of an already-served network")

	urls, err = f.GetURL(preConfiguredNetworkID)
	require.NoError(t, err)
	require.Equal(t, preConfiguredURL, urls.BridgeURL,
		"the static bridge url override stays terminal across json-rpc refreshes")
}

// TestLiveDiscovery_ConfigSourcedEntryIsNeverOverwrittenByDiscovery pins the terminal-source rule on
// the discovery path, the same rule applyUpdate enforces for events: a cache entry tagged
// SourceConfig is never replaced by a URL the discovery path resolved from the chain. The entry is
// seeded directly (the shape buildInitialCache installs for a Config.BridgeURLs network that is not
// on-chain yet) while the resolver's override map does not cover the network, which is the only
// state in which the two can disagree - resolve consults config first, so an override the resolver
// does know about is returned as SourceConfig and merely refreshed. The rollup address must still be
// registered and watched.
func TestLiveDiscovery_ConfigSourcedEntryIsNeverOverwrittenByDiscovery(t *testing.T) {
	backend, auth := newTestBackend(t)
	mgrAddr, _ := deployRollupManagerWithRollups(t, backend, auth, 0)

	const networkID = uint32(5)
	const operatorURL = "http://operator-override.example.com:5577"

	f := startFinder(t, baseTestConfig(mgrAddr), Options{
		EthClient:     newTestEthClient(backend),
		HealthChecker: newMapHealthChecker(nil),
	})

	f.cache.set(networkID, cacheEntry{url: operatorURL, source: SourceConfig, healthy: true})

	sleepPastSeedTick(testPollInterval)

	newRollup := deployStandaloneRollup(t, backend, auth, networkID)
	const metadataURL = "https://on-chain.example.com:5577"
	_, err := newRollup.contract.SetAggchainMetadata(auth, MetadataBridgeServiceURLKey, metadataURL)
	require.NoError(t, err)
	backend.Commit()

	mgr := newRollupManagerContract(t, backend, mgrAddr)
	_, err = mgr.EmitCreateNewRollup(auth, networkID, newRollup.addr)
	require.NoError(t, err)
	backend.Commit()

	require.Eventually(t, func() bool {
		// discoverRollup's guard returns right after l.cache.get(rollupID) without ever reaching
		// cache.set on this path (the entry stays config-sourced forever), so there is no cache
		// value change to poll for; the write to addrToNetworkID is guarded by cache.mu for exactly
		// this reason - take the read lock here instead of reading the map bare, or -race reports a
		// read/write conflict against discoverRollup's write.
		f.cache.mu.RLock()
		_, known := f.addrToNetworkID[newRollup.addr]
		f.cache.mu.RUnlock()
		return known
	}, testEventuallyWait, testEventuallyTick, "discovery must register the attached rollup's contract")

	sleepPastSeedTick(testPollInterval)

	urls, err := f.GetURL(networkID)
	require.NoError(t, err)
	require.Equal(t, operatorURL, urls.BridgeURL,
		"a config-sourced entry must not be overwritten by the url discovery resolved on-chain")

	entry, ok := f.cache.get(networkID)
	require.True(t, ok)
	require.Equal(t, SourceConfig, entry.source, "the entry's source must stay SourceConfig")
	require.True(t, entry.healthy, "the config-sourced entry's health state must be left untouched")
}

// TestLiveUpdate_FirstInstallPendingWhenAutoRegisterDisabled covers the applyUpdate gate: a network
// enumerated at Start with no source (ErrNoSourceAvailable, no cache entry) whose first-ever bridge
// service URL arrives via a live event after Start is NOT installed when
// Config.AutoRegisterNewNetworks is disabled - it is recorded pending instead, one test per event
// type, mirroring TestLiveUpdate_FirstInstallViaEventOnNoSourceNetwork's structure.
func TestLiveUpdate_FirstInstallPendingWhenAutoRegisterDisabled(t *testing.T) {
	t.Run("via SetTrustedSequencerURL", func(t *testing.T) {
		backend, auth := newTestBackend(t)
		mgrAddr, rollups := deployRollupManagerWithRollups(t, backend, auth, 1)
		const networkID = uint32(1)

		cfg := baseTestConfig(mgrAddr)
		cfg.AutoRegisterNewNetworks = false

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
			return len(f.PendingNetworks()) == 1
		}, testEventuallyWait, testEventuallyTick, "expected the first install to be recorded pending")

		_, err = f.GetURL(networkID)
		require.ErrorIs(t, err, ErrURLNotFound,
			"a first install via SetTrustedSequencerURL must not be applied when AutoRegisterNewNetworks is disabled")

		pending := f.PendingNetworks()
		require.Len(t, pending, 1)
		require.Equal(t, networkID, pending[0].NetworkID)
		require.Equal(t, PendingReasonFirstURLEvent, pending[0].Reason)
	})

	t.Run("via AggchainMetadataSet", func(t *testing.T) {
		backend, auth := newTestBackend(t)
		mgrAddr, rollups := deployRollupManagerWithRollups(t, backend, auth, 1)
		const networkID = uint32(1)

		cfg := baseTestConfig(mgrAddr)
		cfg.AutoRegisterNewNetworks = false

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
			return len(f.PendingNetworks()) == 1
		}, testEventuallyWait, testEventuallyTick, "expected the first install to be recorded pending")

		_, err = f.GetURL(networkID)
		require.ErrorIs(t, err, ErrURLNotFound,
			"a first install via AggchainMetadataSet must not be applied when AutoRegisterNewNetworks is disabled")

		pending := f.PendingNetworks()
		require.Len(t, pending, 1)
		require.Equal(t, networkID, pending[0].NetworkID)
		require.Equal(t, PendingReasonFirstURLEvent, pending[0].Reason)
	})
}

// TestLiveUpdate_RefreshUnaffectedByAutoRegisterDisabled covers the refresh guarantee: once a network has
// been served (here, from Start's own enumeration, which the flag never gates), refreshes of that
// already-served entry keep applying exactly as they do today, health gating included -
// AutoRegisterNewNetworks only ever gates a FIRST install, never a refresh of an existing entry.
func TestLiveUpdate_RefreshUnaffectedByAutoRegisterDisabled(t *testing.T) {
	backend, auth := newTestBackend(t)
	mgrAddr, rollups := deployRollupManagerWithRollups(t, backend, auth, 1)
	const networkID = uint32(1)

	healthySrv := newHealthServer(t, true)
	_, err := rollups[0].contract.SetAggchainMetadata(auth, MetadataBridgeServiceURLKey, healthySrv.Server.URL)
	require.NoError(t, err)
	backend.Commit()

	cfg := baseTestConfig(mgrAddr)
	cfg.AutoRegisterNewNetworks = false

	f := startFinder(t, cfg, Options{
		EthClient: newTestEthClient(backend),
	})

	got, err := f.GetURL(networkID)
	require.NoError(t, err)
	require.Equal(t, healthySrv.Server.URL, got.BridgeURL)

	sleepPastSeedTick(testPollInterval)

	// A healthy-to-healthy metadata refresh must still be applied.
	healthySrv2 := newHealthServer(t, true)
	_, err = rollups[0].contract.SetAggchainMetadata(auth, MetadataBridgeServiceURLKey, healthySrv2.Server.URL)
	require.NoError(t, err)
	backend.Commit()

	require.Eventually(t, func() bool {
		got, err := f.GetURL(networkID)
		return err == nil && got.BridgeURL == healthySrv2.Server.URL
	}, testEventuallyWait, testEventuallyTick,
		"refresh of an already-served network must apply even with AutoRegisterNewNetworks disabled")

	sleepPastSeedTick(testPollInterval)

	// Health gating for that same refresh path must also stay intact: a healthy current entry must
	// still reject an unreachable candidate.
	deadURL := closedServerURL(t)
	_, err = rollups[0].contract.SetAggchainMetadata(auth, MetadataBridgeServiceURLKey, deadURL)
	require.NoError(t, err)
	backend.Commit()

	sleepPastSeedTick(testPollInterval)
	sleepPastSeedTick(testPollInterval)

	got, err = f.GetURL(networkID)
	require.NoError(t, err)
	require.Equal(t, healthySrv2.Server.URL, got.BridgeURL,
		"healthy current must still reject an unreachable candidate even with AutoRegisterNewNetworks disabled")

	require.Empty(t, f.PendingNetworks(),
		"a refresh of an already-served network must never be recorded pending")
}

// TestChainRefreshGating_NoPriorEntryPendingWhenAutoRegisterDisabled is a focused isolation test
// (mirroring health_gating_test.go's TestHealthGating_NoPriorEntryInstallsRegardlessOfHealth
// pattern) confirming refreshFromChain's own "!exists && !autoRegisterNewNetworks" gate directly:
// a first-ever install produced by a metadata-clear re-resolve is recorded pending
// (PendingReasonChainRefresh, BlockNumber 0 since refreshFromChain has no types.Log in scope)
// instead of being installed, when AutoRegisterNewNetworks is disabled.
func TestChainRefreshGating_NoPriorEntryPendingWhenAutoRegisterDisabled(t *testing.T) {
	const networkID = uint32(200)
	addr := common.HexToAddress("0x00000000000000000000000000000000000c0de")
	const candidateURL = "https://config-resolved.example.com:5577"

	c := newCache()
	res := newResolver(map[uint32]string{networkID: candidateURL}, nil, DefaultBridgeServicePort)

	lst := &listener{
		logger:        testLogger(),
		healthChecker: newMapHealthChecker(map[string]bool{candidateURL: true}),
		resolver:      res,
		cache:         c,
		readerFactory: func(common.Address, aggkittypes.BaseEthereumClienter) (RollupContractReader, error) {
			return nil, nil
		},
		autoRegisterNewNetworks: false,
	}

	lst.refreshFromChain(context.Background(), networkID, addr)

	_, ok := c.get(networkID)
	require.False(t, ok,
		"a chain-refresh first install must not be applied when AutoRegisterNewNetworks is disabled")

	pending := c.pendingList()
	require.Len(t, pending, 1)
	require.Equal(t, networkID, pending[0].NetworkID)
	require.Equal(t, addr, pending[0].RollupAddress)
	require.Equal(t, uint64(0), pending[0].BlockNumber,
		"refreshFromChain has no types.Log in scope, so BlockNumber must be 0")
	require.Equal(t, PendingReasonChainRefresh, pending[0].Reason)
}
