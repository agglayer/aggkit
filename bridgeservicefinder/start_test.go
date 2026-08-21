package bridgeservicefinder

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStart_SingleSourceInIsolation covers matrix item #1: each source resolved in isolation for a
// dedicated network (config-only, metadata-only, sequencer-only), verified via GetURL.
func TestStart_SingleSourceInIsolation(t *testing.T) {
	backend, auth := newTestBackend(t)
	mgrAddr, rollups := deployRollupManagerWithRollups(t, backend, auth, 3)

	const (
		configNetwork    = uint32(1)
		metadataNetwork  = uint32(2)
		sequencerNetwork = uint32(3)
		configURL        = "https://config.example.com:5577"
		metadataURL      = "https://metadata.example.com:5577"
		rawSequencerURL  = "https://sequencer.example.com:8545"
	)

	// Metadata-only network: set aggchainMetadata, never call SetTrustedSequencerURL.
	_, err := rollups[metadataNetwork-1].contract.SetAggchainMetadata(auth, MetadataBridgeServiceURLKey, metadataURL)
	require.NoError(t, err)
	backend.Commit()

	// Sequencer-only network: only call SetTrustedSequencerURL, never SetAggchainMetadata.
	_, err = rollups[sequencerNetwork-1].contract.SetTrustedSequencerURL(auth, rawSequencerURL)
	require.NoError(t, err)
	backend.Commit()

	cfg := baseTestConfig(mgrAddr)
	cfg.BridgeURLs = map[uint32]string{configNetwork: configURL}

	f, err := New(cfg, Options{
		EthClient:     newTestEthClient(backend),
		HealthChecker: newMapHealthChecker(nil), // everything reported unhealthy is fine, GetURL unaffected
		Logger:        testLogger(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, f.Start(ctx))

	gotConfig, err := f.GetURL(configNetwork)
	require.NoError(t, err)
	require.Equal(t, configURL, gotConfig.BridgeURL)
	require.Empty(t, gotConfig.JSONRPCURL, "config network has no on-chain sequencer url set")

	gotMetadata, err := f.GetURL(metadataNetwork)
	require.NoError(t, err)
	require.Equal(t, metadataURL, gotMetadata.BridgeURL)
	require.Empty(t, gotMetadata.JSONRPCURL, "metadata network has no on-chain sequencer url set")

	gotSequencer, err := f.GetURL(sequencerNetwork)
	require.NoError(t, err)
	require.Contains(t, gotSequencer.BridgeURL, "sequencer.example.com")
	require.Contains(t, gotSequencer.BridgeURL, fmt.Sprintf(":%d", DefaultBridgeServicePort))
	require.Equal(t, rawSequencerURL, gotSequencer.JSONRPCURL,
		"json-rpc url must be the raw sequencer url, without port substitution")
}

// TestStart_PriorityCombosOnSingleNetwork covers matrix item #2: all priority combinations at
// Start on a single network.
func TestStart_PriorityCombosOnSingleNetwork(t *testing.T) {
	const networkID = uint32(1)

	t.Run("config+metadata+seq all present: config wins", func(t *testing.T) {
		backend, auth := newTestBackend(t)
		mgrAddr, rollups := deployRollupManagerWithRollups(t, backend, auth, 1)

		_, err := rollups[0].contract.SetAggchainMetadata(auth, MetadataBridgeServiceURLKey, "https://metadata.example.com:5577")
		require.NoError(t, err)
		backend.Commit()

		_, err = rollups[0].contract.SetTrustedSequencerURL(auth, "https://sequencer.example.com:8545")
		require.NoError(t, err)
		backend.Commit()

		const configURL = "https://config.example.com:5577"
		cfg := baseTestConfig(mgrAddr)
		cfg.BridgeURLs = map[uint32]string{networkID: configURL}

		f, err := New(cfg, Options{
			EthClient:     newTestEthClient(backend),
			HealthChecker: newMapHealthChecker(nil),
			Logger:        testLogger(),
		})
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)

		require.NoError(t, f.Start(ctx))

		got, err := f.GetURL(networkID)
		require.NoError(t, err)
		require.Equal(t, configURL, got.BridgeURL)
		require.Equal(t, "https://sequencer.example.com:8545", got.JSONRPCURL,
			"config-sourced bridge url must still be enriched with the on-chain json-rpc endpoint")
	})

	t.Run("metadata+seq present, no config: metadata wins", func(t *testing.T) {
		backend, auth := newTestBackend(t)
		mgrAddr, rollups := deployRollupManagerWithRollups(t, backend, auth, 1)

		const metadataURL = "https://metadata.example.com:5577"
		_, err := rollups[0].contract.SetAggchainMetadata(auth, MetadataBridgeServiceURLKey, metadataURL)
		require.NoError(t, err)
		backend.Commit()

		_, err = rollups[0].contract.SetTrustedSequencerURL(auth, "https://sequencer.example.com:8545")
		require.NoError(t, err)
		backend.Commit()

		cfg := baseTestConfig(mgrAddr)

		f, err := New(cfg, Options{
			EthClient:     newTestEthClient(backend),
			HealthChecker: newMapHealthChecker(nil),
			Logger:        testLogger(),
		})
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)

		require.NoError(t, f.Start(ctx))

		got, err := f.GetURL(networkID)
		require.NoError(t, err)
		require.Equal(t, metadataURL, got.BridgeURL)
		require.Equal(t, "https://sequencer.example.com:8545", got.JSONRPCURL)
	})

	t.Run("sequencer only: seq wins with port substituted", func(t *testing.T) {
		backend, auth := newTestBackend(t)
		mgrAddr, rollups := deployRollupManagerWithRollups(t, backend, auth, 1)

		_, err := rollups[0].contract.SetTrustedSequencerURL(auth, "https://sequencer.example.com:8545")
		require.NoError(t, err)
		backend.Commit()

		cfg := baseTestConfig(mgrAddr)

		f, err := New(cfg, Options{
			EthClient:     newTestEthClient(backend),
			HealthChecker: newMapHealthChecker(nil),
			Logger:        testLogger(),
		})
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)

		require.NoError(t, f.Start(ctx))

		got, err := f.GetURL(networkID)
		require.NoError(t, err)
		require.Contains(t, got.BridgeURL, "sequencer.example.com")
		require.Contains(t, got.BridgeURL, fmt.Sprintf(":%d", DefaultBridgeServicePort))
		require.NotContains(t, got.BridgeURL, ":8545")
		require.Equal(t, "https://sequencer.example.com:8545", got.JSONRPCURL)
	})

	t.Run("none present: GetURL errors, Start still succeeds", func(t *testing.T) {
		backend, auth := newTestBackend(t)
		mgrAddr, _ := deployRollupManagerWithRollups(t, backend, auth, 1)

		cfg := baseTestConfig(mgrAddr)

		f, err := New(cfg, Options{
			EthClient:     newTestEthClient(backend),
			HealthChecker: newMapHealthChecker(nil),
			Logger:        testLogger(),
		})
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)

		require.NoError(t, f.Start(ctx))

		_, err = f.GetURL(networkID)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrURLNotFound)
	})
}

// TestStart_ConfigImmunity covers matrix item #3: a config-sourced network's bridge URL must never
// change even after emitting on-chain events targeting the underlying contract (the listener routes
// them, but applyUpdate treats SourceConfig entries as terminal). The JSON-RPC endpoint, by
// contrast, IS refreshed by the SetTrustedSequencerURL event, since it is independent of the
// bridge-URL priority rules.
func TestStart_ConfigImmunity(t *testing.T) {
	backend, auth := newTestBackend(t)
	// Deploy a second, on-chain-resolved rollup too, so the listener has a rollup contract to watch
	// for URL events (beyond the rollup manager address it always watches for discovery).
	mgrAddr, rollups := deployRollupManagerWithRollups(t, backend, auth, 2)

	const (
		configNetwork = uint32(1)
		liveNetwork   = uint32(2)
		configURL     = "https://config.example.com:5577"
	)

	_, err := rollups[liveNetwork-1].contract.SetTrustedSequencerURL(auth, "https://seq.example.com:8545")
	require.NoError(t, err)
	backend.Commit()

	cfg := baseTestConfig(mgrAddr)
	cfg.BridgeURLs = map[uint32]string{configNetwork: configURL}
	cfg.PollInterval.Duration = testPollInterval

	f, err := New(cfg, Options{
		EthClient:     newTestEthClient(backend),
		HealthChecker: newMapHealthChecker(nil),
		Logger:        testLogger(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	require.NoError(t, f.Start(ctx))

	sleepPastSeedTick(testPollInterval)

	// Now emit events at the config network's underlying address (rollup #1, which is enumerated
	// on-chain too; its entry is config-sourced, so applyUpdate rejects both bridge-URL candidates).
	const newSeqURL = "https://new-seq.example.com:9999"
	_, err = rollups[configNetwork-1].contract.SetTrustedSequencerURL(auth, newSeqURL)
	require.NoError(t, err)
	backend.Commit()

	_, err = rollups[configNetwork-1].contract.SetAggchainMetadata(auth, MetadataBridgeServiceURLKey, "https://evil2.example.com:9999")
	require.NoError(t, err)
	backend.Commit()

	// The json-rpc endpoint must pick up the new sequencer url even though the bridge url is immune.
	require.Eventually(t, func() bool {
		got, err := f.GetURL(configNetwork)
		return err == nil && got.JSONRPCURL == newSeqURL
	}, testEventuallyWait, testEventuallyTick,
		"SetTrustedSequencerURL must refresh the json-rpc endpoint of a config-sourced entry")

	got, err := f.GetURL(configNetwork)
	require.NoError(t, err)
	require.Equal(t, configURL, got.BridgeURL, "config-sourced bridge url must remain immune to on-chain events")
}

// TestStart_ConfigRPCImmunity verifies Config.RPCURLs semantics end-to-end: the override is served
// instead of the on-chain sequencer url, and a later SetTrustedSequencerURL event - which DOES
// refresh the bridge URL of this sequencer-sourced entry - must not touch the overridden JSON-RPC
// endpoint.
func TestStart_ConfigRPCImmunity(t *testing.T) {
	backend, auth := newTestBackend(t)
	mgrAddr, rollups := deployRollupManagerWithRollups(t, backend, auth, 1)

	const (
		networkID   = uint32(1)
		rpcOverride = "https://rpc-override.example.com:8545"
	)

	_, err := rollups[0].contract.SetTrustedSequencerURL(auth, "https://seq.example.com:8545")
	require.NoError(t, err)
	backend.Commit()

	cfg := baseTestConfig(mgrAddr)
	cfg.RPCURLs = map[uint32]string{networkID: rpcOverride}

	f, err := New(cfg, Options{
		EthClient:     newTestEthClient(backend),
		HealthChecker: newMapHealthChecker(nil),
		Logger:        testLogger(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	require.NoError(t, f.Start(ctx))

	got, err := f.GetURL(networkID)
	require.NoError(t, err)
	require.Contains(t, got.BridgeURL, "seq.example.com")
	require.Equal(t, rpcOverride, got.JSONRPCURL, "config rpc override must beat the on-chain sequencer url")

	sleepPastSeedTick(testPollInterval)

	// The event refreshes the (sequencer-sourced, unhealthy) bridge URL, proving it was processed...
	_, err = rollups[0].contract.SetTrustedSequencerURL(auth, "https://new-seq.example.com:8545")
	require.NoError(t, err)
	backend.Commit()

	require.Eventually(t, func() bool {
		got, err := f.GetURL(networkID)
		return err == nil && got.BridgeURL != "" && strings.Contains(got.BridgeURL, "new-seq.example.com")
	}, testEventuallyWait, testEventuallyTick, "expected the bridge url to be refreshed by the event")

	// ...but the overridden json-rpc endpoint must remain untouched.
	got, err = f.GetURL(networkID)
	require.NoError(t, err)
	require.Equal(t, rpcOverride, got.JSONRPCURL,
		"SetTrustedSequencerURL must not overwrite a config-overridden json-rpc endpoint")
}

// TestStart_InitialCacheAcrossMultipleNetworks covers matrix item #5: initial cache build across
// several enumerated networks with different source combinations in one Start() call.
func TestStart_InitialCacheAcrossMultipleNetworks(t *testing.T) {
	backend, auth := newTestBackend(t)
	mgrAddr, rollups := deployRollupManagerWithRollups(t, backend, auth, 4)

	const (
		networkConfigOnly   = uint32(1)
		networkMetadataOnly = uint32(2)
		networkSeqOnly      = uint32(3)
		networkNoSource     = uint32(4)
		configURL           = "https://config.example.com:5577"
		metadataURL         = "https://metadata.example.com:5577"
	)

	_, err := rollups[networkMetadataOnly-1].contract.SetAggchainMetadata(auth, MetadataBridgeServiceURLKey, metadataURL)
	require.NoError(t, err)
	backend.Commit()

	_, err = rollups[networkSeqOnly-1].contract.SetTrustedSequencerURL(auth, "https://seq.example.com:8545")
	require.NoError(t, err)
	backend.Commit()
	// networkNoSource: neither setter is ever called.

	cfg := baseTestConfig(mgrAddr)
	cfg.BridgeURLs = map[uint32]string{networkConfigOnly: configURL}

	f, err := New(cfg, Options{
		EthClient:     newTestEthClient(backend),
		HealthChecker: newMapHealthChecker(nil),
		Logger:        testLogger(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	require.NoError(t, f.Start(ctx))

	got, err := f.GetURL(networkConfigOnly)
	require.NoError(t, err)
	require.Equal(t, configURL, got.BridgeURL)

	got, err = f.GetURL(networkMetadataOnly)
	require.NoError(t, err)
	require.Equal(t, metadataURL, got.BridgeURL)

	got, err = f.GetURL(networkSeqOnly)
	require.NoError(t, err)
	require.Contains(t, got.BridgeURL, fmt.Sprintf(":%d", DefaultBridgeServicePort))
	require.Equal(t, "https://seq.example.com:8545", got.JSONRPCURL)

	_, err = f.GetURL(networkNoSource)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrURLNotFound)
}

// TestStart_UnreachableService_RequireAllHealthyOnStartFalse covers matrix item #6a: Start returns
// nil even when a resolved service is unreachable (default RequireAllHealthyOnStart=false), and the
// URL is still served, with the internal healthy flag recorded as false.
func TestStart_UnreachableService_RequireAllHealthyOnStartFalse(t *testing.T) {
	backend, auth := newTestBackend(t)
	mgrAddr, rollups := deployRollupManagerWithRollups(t, backend, auth, 1)

	deadURL := closedServerURL(t)

	_, err := rollups[0].contract.SetTrustedSequencerURL(auth, deadURL)
	require.NoError(t, err)
	backend.Commit()

	cfg := baseTestConfig(mgrAddr)
	cfg.RequireAllHealthyOnStart = false

	f, err := New(cfg, Options{
		EthClient: newTestEthClient(backend),
		Logger:    testLogger(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	require.NoError(t, f.Start(ctx))

	got, err := f.GetURL(1)
	require.NoError(t, err)
	require.Contains(t, got.BridgeURL, fmt.Sprintf(":%d", DefaultBridgeServicePort))

	concrete, ok := f.(*finder)
	require.True(t, ok)

	entry, ok := concrete.cache.get(1)
	require.True(t, ok)
	require.False(t, entry.healthy, "unreachable service must be recorded as unhealthy")
}

// TestStart_UnreachableService_RequireAllHealthyOnStartTrue covers matrix item #6b: Start returns a
// wrapped ErrServicesUnhealthyOnStart when RequireAllHealthyOnStart=true and at least one resolved
// service is unreachable.
func TestStart_UnreachableService_RequireAllHealthyOnStartTrue(t *testing.T) {
	backend, auth := newTestBackend(t)
	mgrAddr, rollups := deployRollupManagerWithRollups(t, backend, auth, 1)

	deadURL := closedServerURL(t)

	_, err := rollups[0].contract.SetTrustedSequencerURL(auth, deadURL)
	require.NoError(t, err)
	backend.Commit()

	cfg := baseTestConfig(mgrAddr)
	cfg.RequireAllHealthyOnStart = true

	f, err := New(cfg, Options{
		EthClient: newTestEthClient(backend),
		Logger:    testLogger(),
	})
	require.NoError(t, err)

	err = f.Start(context.Background())
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrServicesUnhealthyOnStart))
}

// TestStart_AllHealthy_RequireAllHealthyOnStartTrue covers matrix item #6c: Start returns nil when
// RequireAllHealthyOnStart=true and every resolved service is healthy.
func TestStart_AllHealthy_RequireAllHealthyOnStartTrue(t *testing.T) {
	backend, auth := newTestBackend(t)
	mgrAddr, rollups := deployRollupManagerWithRollups(t, backend, auth, 1)

	healthSrv := newHealthServer(t, true)

	// AggchainMetadataSet is used rather than SetTrustedSequencerURL because the latter substitutes
	// the port to DefaultBridgeServicePort (see withPort), which would rewrite this httptest
	// server's actual URL/port.
	_, err := rollups[0].contract.SetAggchainMetadata(auth, MetadataBridgeServiceURLKey, healthSrv.Server.URL)
	require.NoError(t, err)
	backend.Commit()

	cfg := baseTestConfig(mgrAddr)
	cfg.RequireAllHealthyOnStart = true
	cfg.HealthCheckPath = "" // default path is "/"; the handler ignores the exact path anyway

	f, err := New(cfg, Options{
		EthClient: newTestEthClient(backend),
		Logger:    testLogger(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	require.NoError(t, f.Start(ctx))
}

// TestStart_IgnoreNetworkIDs_SkipsEnumeration verifies that a networkID listed in
// Config.IgnoreNetworkIDs is skipped entirely during buildInitialCache's enumeration, even though it
// exposes a perfectly resolvable on-chain source: no cache entry is installed for it, while sibling
// networks are still resolved normally. It also verifies a config override for the SAME networkID is
// still served, since the ignore only skips on-chain inspection, never a static override.
func TestStart_IgnoreNetworkIDs_SkipsEnumeration(t *testing.T) {
	backend, auth := newTestBackend(t)
	mgrAddr, rollups := deployRollupManagerWithRollups(t, backend, auth, 3)

	const (
		ignoredNetwork     = uint32(1)
		ignoredWithConfig  = uint32(2)
		normalNetwork      = uint32(3)
		ignoredConfigURL   = "https://ignored-config.example.com:5577"
		ignoredMetadataURL = "https://ignored-metadata.example.com:5577" // must never surface
		normalMetadataURL  = "https://normal-metadata.example.com:5577"
	)

	// Give the purely-ignored network a perfectly good on-chain source: it must still be skipped.
	_, err := rollups[ignoredNetwork-1].contract.SetAggchainMetadata(auth, MetadataBridgeServiceURLKey, ignoredMetadataURL)
	require.NoError(t, err)
	backend.Commit()

	// The ignored-but-config-overridden network also has an on-chain source; it must never surface,
	// only the config URL should.
	_, err = rollups[ignoredWithConfig-1].contract.SetAggchainMetadata(auth, MetadataBridgeServiceURLKey, ignoredMetadataURL)
	require.NoError(t, err)
	backend.Commit()

	_, err = rollups[normalNetwork-1].contract.SetAggchainMetadata(auth, MetadataBridgeServiceURLKey, normalMetadataURL)
	require.NoError(t, err)
	backend.Commit()

	cfg := baseTestConfig(mgrAddr)
	cfg.BridgeURLs = map[uint32]string{ignoredWithConfig: ignoredConfigURL}
	cfg.IgnoreNetworkIDs = []uint32{ignoredNetwork, ignoredWithConfig}

	f, err := New(cfg, Options{
		EthClient:     newTestEthClient(backend),
		HealthChecker: newMapHealthChecker(nil),
		Logger:        testLogger(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	require.NoError(t, f.Start(ctx))

	_, err = f.GetURL(ignoredNetwork)
	require.ErrorIs(t, err, ErrURLNotFound, "ignored network must not be resolved despite exposing a valid on-chain source")

	gotConfig, err := f.GetURL(ignoredWithConfig)
	require.NoError(t, err)
	require.Equal(t, ignoredConfigURL, gotConfig.BridgeURL, "config override must still be served for an ignored network")
	require.Empty(t, gotConfig.JSONRPCURL, "on-chain enrichment must not happen for an ignored network")

	gotNormal, err := f.GetURL(normalNetwork)
	require.NoError(t, err)
	require.Equal(t, normalMetadataURL, gotNormal.BridgeURL, "sibling network must resolve normally")

	concrete, ok := f.(*finder)
	require.True(t, ok)
	require.NotContains(t, concrete.addrToNetworkID, rollups[ignoredNetwork-1].addr,
		"an ignored network's contract must never be registered in the routing table")
}

// trackingHealthChecker wraps a mapHealthChecker and records every baseURL probed, so a test can
// assert a specific URL was (or was not) probed at all, not just what the probe returned.
type trackingHealthChecker struct {
	*mapHealthChecker
	probed map[string]bool
}

func newTrackingHealthChecker(healthy map[string]bool) *trackingHealthChecker {
	return &trackingHealthChecker{mapHealthChecker: newMapHealthChecker(healthy), probed: map[string]bool{}}
}

func (t *trackingHealthChecker) IsHealthy(ctx context.Context, baseURL string) bool {
	t.probed[baseURL] = true
	return t.mapHealthChecker.IsHealthy(ctx, baseURL)
}

// TestStart_IgnoreNetworkIDs_SkipsHealthProbe verifies that a networkID listed in
// Config.IgnoreNetworkIDs is exempt from probeAll's /health probe even when it has a cache entry
// installed by a Config.BridgeURLs override (config-seeding runs independently of the enumeration
// loop the ignore list otherwise affects). It also proves this holds under
// RequireAllHealthyOnStart=true: Start must succeed even though the ignored network's config URL is
// unreachable, since it is never probed and therefore never counted as unhealthy.
func TestStart_IgnoreNetworkIDs_SkipsHealthProbe(t *testing.T) {
	backend, auth := newTestBackend(t)
	mgrAddr, _ := deployRollupManagerWithRollups(t, backend, auth, 1)

	const (
		ignoredNetwork = uint32(1)
		normalNetwork  = uint32(2) // never enumerated (RollupCount=1); served from config only
	)

	deadURL := closedServerURL(t) // guaranteed unreachable, would fail RequireAllHealthyOnStart if probed
	const normalURL = "https://normal.example.com:5577"

	cfg := baseTestConfig(mgrAddr)
	cfg.BridgeURLs = map[uint32]string{ignoredNetwork: deadURL, normalNetwork: normalURL}
	cfg.IgnoreNetworkIDs = []uint32{ignoredNetwork}
	cfg.RequireAllHealthyOnStart = true

	hc := newTrackingHealthChecker(map[string]bool{normalURL: true})

	f, err := New(cfg, Options{
		EthClient:     newTestEthClient(backend),
		HealthChecker: hc,
		Logger:        testLogger(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// Start must succeed: the only unreachable entry is the ignored one, which is never probed.
	require.NoError(t, f.Start(ctx))

	require.False(t, hc.probed[deadURL], "an ignored network's config-sourced url must never be health-probed")
	require.True(t, hc.probed[normalURL], "sanity check: a non-ignored config-sourced url is still probed")

	got, err := f.GetURL(ignoredNetwork)
	require.NoError(t, err)
	require.Equal(t, deadURL, got.BridgeURL, "the ignored network's config override must still be served")

	concrete, ok := f.(*finder)
	require.True(t, ok)
	entry, ok := concrete.cache.get(ignoredNetwork)
	require.True(t, ok)
	require.False(t, entry.healthy, "an unprobed entry must default to healthy=false")
}

// TestStart_NetworkZero covers matrix item #11: network 0 / L1 is never enumerated on-chain; it is
// only served if provided via Config.URLs.
func TestStart_NetworkZero(t *testing.T) {
	t.Run("with config URL", func(t *testing.T) {
		backend, auth := newTestBackend(t)
		mgrAddr, _ := deployRollupManagerWithRollups(t, backend, auth, 0)

		const l1URL = "https://l1.example.com:5577"
		cfg := baseTestConfig(mgrAddr)
		cfg.BridgeURLs = map[uint32]string{0: l1URL}

		f, err := New(cfg, Options{
			EthClient:     newTestEthClient(backend),
			HealthChecker: newMapHealthChecker(nil),
			Logger:        testLogger(),
		})
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)

		require.NoError(t, f.Start(ctx))

		got, err := f.GetURL(0)
		require.NoError(t, err)
		require.Equal(t, l1URL, got.BridgeURL)
		require.Empty(t, got.JSONRPCURL, "network 0 / L1 is config-only, so no json-rpc endpoint")
	})

	t.Run("without config URL", func(t *testing.T) {
		backend, auth := newTestBackend(t)
		mgrAddr, _ := deployRollupManagerWithRollups(t, backend, auth, 0)

		cfg := baseTestConfig(mgrAddr)

		f, err := New(cfg, Options{
			EthClient:     newTestEthClient(backend),
			HealthChecker: newMapHealthChecker(nil),
			Logger:        testLogger(),
		})
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)

		require.NoError(t, f.Start(ctx))

		_, err = f.GetURL(0)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrURLNotFound)
	})
}
