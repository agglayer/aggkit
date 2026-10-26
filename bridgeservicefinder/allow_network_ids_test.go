package bridgeservicefinder

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNew_IgnoreAndAllowNetworkIDsAreMutuallyExclusive verifies that setting both
// Config.IgnoreNetworkIDs and Config.AllowNetworkIDs is rejected at construction.
func TestNew_IgnoreAndAllowNetworkIDsAreMutuallyExclusive(t *testing.T) {
	backend, auth := newTestBackend(t)
	mgrAddr, _ := deployRollupManagerWithRollups(t, backend, auth, 1)

	cfg := baseTestConfig(mgrAddr)
	cfg.IgnoreNetworkIDs = []uint32{1}
	cfg.AllowNetworkIDs = []uint32{2}

	_, err := New(cfg, Options{
		EthClient:     newTestEthClient(backend),
		HealthChecker: newMapHealthChecker(nil),
		Logger:        testLogger(),
	})
	require.ErrorIs(t, err, ErrConflictingNetworkFilters)
}

func TestNetworkFilter(t *testing.T) {
	t.Run("empty filter excludes nothing", func(t *testing.T) {
		f, err := newNetworkFilter(Config{})
		require.NoError(t, err)
		require.False(t, f.active())
		require.False(t, f.excluded(0))
		require.False(t, f.excluded(7))
	})

	t.Run("ignore list excludes only listed networks", func(t *testing.T) {
		f, err := newNetworkFilter(Config{IgnoreNetworkIDs: []uint32{3}})
		require.NoError(t, err)
		require.True(t, f.active())
		require.True(t, f.excluded(3))
		require.False(t, f.excluded(4))
	})

	t.Run("allow list excludes everything not listed, including L1", func(t *testing.T) {
		f, err := newNetworkFilter(Config{AllowNetworkIDs: []uint32{2}})
		require.NoError(t, err)
		require.True(t, f.active())
		require.False(t, f.excluded(2))
		require.True(t, f.excluded(1))
		require.True(t, f.excluded(0), "network 0 must be listed explicitly to be served")
	})

	t.Run("allow list containing 0 keeps L1", func(t *testing.T) {
		f, err := newNetworkFilter(Config{AllowNetworkIDs: []uint32{0, 2}})
		require.NoError(t, err)
		require.False(t, f.excluded(0))
	})
}

// TestStart_AllowNetworkIDs_OnlyAllowedAreResolved verifies that with a non-empty
// Config.AllowNetworkIDs only the listed networks are resolved: the rest are skipped during
// enumeration (even with a config override) and GetURL reports ErrNetworkDisabled for them.
func TestStart_AllowNetworkIDs_OnlyAllowedAreResolved(t *testing.T) {
	backend, auth := newTestBackend(t)
	mgrAddr, rollups := deployRollupManagerWithRollups(t, backend, auth, 3)

	const (
		allowedNetwork     = uint32(2)
		notAllowed         = uint32(1)
		notAllowedWithConf = uint32(3)
		allowedURL         = "https://allowed.example.com:5577"
		otherURL           = "https://other.example.com:5577"
	)

	for _, id := range []uint32{allowedNetwork, notAllowed} {
		url := otherURL
		if id == allowedNetwork {
			url = allowedURL
		}
		_, err := rollups[id-1].contract.SetAggchainMetadata(auth, MetadataBridgeServiceURLKey, url)
		require.NoError(t, err)
		backend.Commit()
	}

	cfg := baseTestConfig(mgrAddr)
	cfg.BridgeURLs = map[uint32]string{notAllowedWithConf: otherURL}
	cfg.AllowNetworkIDs = []uint32{allowedNetwork}

	f, err := New(cfg, Options{
		EthClient:     newTestEthClient(backend),
		HealthChecker: newMapHealthChecker(nil),
		Logger:        testLogger(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, f.Start(ctx))

	got, err := f.GetURL(allowedNetwork)
	require.NoError(t, err)
	require.Equal(t, allowedURL, got.BridgeURL)

	for _, id := range []uint32{notAllowed, notAllowedWithConf} {
		_, err = f.GetURL(id)
		require.ErrorIs(t, err, ErrNetworkDisabled, "network %d is not in AllowNetworkIDs", id)
		require.ErrorIs(t, err, ErrURLNotFound)
	}

	require.Equal(t, []uint32{allowedNetwork}, f.NetworkIDs())

	concrete, ok := f.(*finder)
	require.True(t, ok)
	require.NotContains(t, concrete.addrToNetworkID, rollups[notAllowed-1].addr)
}

// TestLiveDiscovery_AllowNetworkIDs verifies live discovery against an allowlist: an allowed
// network that does not exist yet is registered once it is attached (AutoRegisterNewNetworks=true),
// while a non-listed one is neither registered nor recorded pending, even with
// AutoRegisterNewNetworks=false.
func TestLiveDiscovery_AllowNetworkIDs(t *testing.T) {
	const (
		allowedNetworkID = uint32(2)
		blockedNetworkID = uint32(3)
		metadataURL      = "https://new-rollup.example.com:5577"
	)

	for _, autoRegister := range []bool{true, false} {
		t.Run(map[bool]string{true: "auto-register on", false: "auto-register off"}[autoRegister], func(t *testing.T) {
			backend, auth := newTestBackend(t)
			mgrAddr, _ := deployRollupManagerWithRollups(t, backend, auth, 1)

			cfg := baseTestConfig(mgrAddr)
			cfg.AllowNetworkIDs = []uint32{0, 1, allowedNetworkID}
			cfg.AutoRegisterNewNetworks = autoRegister

			f := startFinder(t, cfg, Options{
				EthClient:     newTestEthClient(backend),
				HealthChecker: newMapHealthChecker(nil),
			})

			sleepPastSeedTick(testPollInterval)

			mgr := newRollupManagerContract(t, backend, mgrAddr)
			blocked := deployStandaloneRollup(t, backend, auth, blockedNetworkID)
			allowed := deployStandaloneRollup(t, backend, auth, allowedNetworkID)
			for id, r := range map[uint32]testRollup{blockedNetworkID: blocked, allowedNetworkID: allowed} {
				_, err := r.contract.SetAggchainMetadata(auth, MetadataBridgeServiceURLKey, metadataURL)
				require.NoError(t, err)
				backend.Commit()
				_, err = mgr.EmitCreateNewRollup(auth, id, r.addr)
				require.NoError(t, err)
				backend.Commit()
			}

			sleepPastSeedTick(testPollInterval)
			sleepPastSeedTick(testPollInterval)

			_, err := f.GetURL(blockedNetworkID)
			require.ErrorIs(t, err, ErrNetworkDisabled)
			require.NotContains(t, f.addrToNetworkID, blocked.addr)
			for _, p := range f.PendingNetworks() {
				require.NotEqual(t, blockedNetworkID, p.NetworkID, "a non-allowed network must never be pending")
			}

			if autoRegister {
				got, err := f.GetURL(allowedNetworkID)
				require.NoError(t, err)
				require.Equal(t, metadataURL, got.BridgeURL)
			}
		})
	}
}
