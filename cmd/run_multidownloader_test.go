package main

import (
	"errors"
	"testing"

	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/claimsync"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config"
	configtypes "github.com/agglayer/aggkit/config/types"
	"github.com/stretchr/testify/require"
)

// TestL1MultidownloaderIsNeeded_DemandMatrix covers the four bridge/claim selector combinations for
// an L1 syncer run where the info tree syncer does NOT run (only L1BRIDGESYNC is active). The shared
// multidownloader is needed as soon as one active consumer explicitly selects it.
func TestL1MultidownloaderIsNeeded_DemandMatrix(t *testing.T) {
	l1Components := []string{aggkitcommon.L1BRIDGESYNC}

	cases := []struct {
		name        string
		bridgeDl    configtypes.Downloader
		claimDl     configtypes.Downloader
		wantNeeded  bool
		wantByActiv bool
	}{
		{"both legacy", configtypes.LegacyDownloader, configtypes.LegacyDownloader, false, false},
		{"bridge multidownloader", configtypes.Multidownloader, configtypes.LegacyDownloader, true, true},
		{"claim multidownloader", configtypes.LegacyDownloader, configtypes.Multidownloader, true, true},
		{"both multidownloader", configtypes.Multidownloader, configtypes.Multidownloader, true, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bridgeCfg := bridgesync.Config{Downloader: tc.bridgeDl}
			claimCfg := claimsync.ConfigStandalone{Downloader: tc.claimDl}

			require.Equal(t, tc.wantNeeded, l1MultidownloaderIsNeeded(l1Components, bridgeCfg, claimCfg))
			require.Equal(t, tc.wantByActiv,
				l1MultidownloaderRequiredByActiveConsumer(l1Components, bridgeCfg, claimCfg))
		})
	}
}

// TestL1MultidownloaderIsNeeded_BridgeAndClaimShareActivation documents that no dedicated
// L1CLAIMSYNC component exists: the claim syncer is activated by the same components as the bridge
// syncer.
func TestL1MultidownloaderIsNeeded_BridgeAndClaimShareActivation(t *testing.T) {
	for _, component := range []string{aggkitcommon.BRIDGE, aggkitcommon.L1BRIDGESYNC} {
		t.Run(component, func(t *testing.T) {
			require.True(t, l1BridgeSyncMustRun([]string{component}))
			require.True(t, l1ClaimSyncMustRun([]string{component}))
		})
	}
	require.False(t, l1ClaimSyncMustRun([]string{aggkitcommon.L2CLAIMSYNC}))
}

// TestL1MultidownloaderIsNeeded_InfoTreeKeepsExistingDemand verifies the historical behaviour: the
// multidownloader is wanted whenever the info tree syncer runs, even though the info tree does not
// "select" it and falls back to the legacy downloader when it is disabled.
func TestL1MultidownloaderIsNeeded_InfoTreeKeepsExistingDemand(t *testing.T) {
	legacyBridge := bridgesync.Config{Downloader: configtypes.LegacyDownloader}
	legacyClaim := claimsync.ConfigStandalone{Downloader: configtypes.LegacyDownloader}

	for _, component := range []string{
		aggkitcommon.AGGORACLE,
		aggkitcommon.AGGSENDER,
		aggkitcommon.AGGSENDERVALIDATOR,
		aggkitcommon.AGGCHAINPROOFGEN,
		aggkitcommon.BRIDGE,
		aggkitcommon.L1INFOTREESYNC,
		aggkitcommon.L2GERSYNC,
	} {
		t.Run(component, func(t *testing.T) {
			require.True(t, l1InfoTreeMustRun([]string{component}))
			require.True(t, l1MultidownloaderIsNeeded([]string{component}, legacyBridge, legacyClaim))
			// The info tree demand is not an explicit consumer selection.
			require.False(t, l1MultidownloaderRequiredByActiveConsumer(
				[]string{component}, legacyBridge, legacyClaim))
		})
	}
}

// TestL1MultidownloaderIsNeeded_NoInfoTreeLegacyStandalone covers the case where the info tree does
// not run (only L1BRIDGESYNC) and both consumers stay legacy: no multidownloader is built.
func TestL1MultidownloaderIsNeeded_NoInfoTreeLegacyStandalone(t *testing.T) {
	components := []string{aggkitcommon.L1BRIDGESYNC}

	require.False(t, l1InfoTreeMustRun(components))
	require.True(t, l1BridgeSyncMustRun(components))
	require.True(t, l1ClaimSyncMustRun(components))

	legacyBridge := bridgesync.Config{Downloader: configtypes.LegacyDownloader}
	legacyClaim := claimsync.ConfigStandalone{Downloader: configtypes.LegacyDownloader}
	require.False(t, l1MultidownloaderIsNeeded(components, legacyBridge, legacyClaim))
}

// TestValidateDownloaderSelections_MultidownloaderDisabled checks that an ACTIVE L1 consumer that
// selected the multidownloader is rejected when the service is disabled (no silent fallback), while
// an unused selection and the info tree fallback are accepted.
func TestValidateDownloaderSelections_MultidownloaderDisabled(t *testing.T) {
	t.Run("active L1 bridge selecting multidownloader is rejected", func(t *testing.T) {
		cfg := config.Config{}
		cfg.BridgeL1Sync.Downloader = configtypes.Multidownloader
		cfg.L1Multidownloader.Enabled = false

		require.Error(t, validateDownloaderSelections(cfg, []string{aggkitcommon.L1BRIDGESYNC}))
	})

	t.Run("active L1 claim selecting multidownloader is rejected", func(t *testing.T) {
		cfg := config.Config{}
		cfg.ClaimL1Sync.Downloader = configtypes.Multidownloader
		cfg.L1Multidownloader.Enabled = false

		require.Error(t, validateDownloaderSelections(cfg, []string{aggkitcommon.BRIDGE}))
	})

	t.Run("active L1 bridge with legacy and disabled multidownloader is accepted", func(t *testing.T) {
		cfg := config.Config{}
		cfg.BridgeL1Sync.Downloader = configtypes.LegacyDownloader
		cfg.L1Multidownloader.Enabled = false

		require.NoError(t, validateDownloaderSelections(cfg, []string{aggkitcommon.L1BRIDGESYNC}))
	})

	t.Run("unused L1 bridge multidownloader selection is accepted when disabled", func(t *testing.T) {
		// Only the info tree is active, so the bridge selection is unused and must not block startup.
		cfg := config.Config{}
		cfg.BridgeL1Sync.Downloader = configtypes.Multidownloader
		cfg.L1Multidownloader.Enabled = false

		require.NoError(t, validateDownloaderSelections(cfg, []string{aggkitcommon.L1INFOTREESYNC}))
	})

	t.Run("info tree fallback with disabled multidownloader is accepted", func(t *testing.T) {
		cfg := config.Config{}
		cfg.L1Multidownloader.Enabled = false

		require.NoError(t, validateDownloaderSelections(cfg, []string{aggkitcommon.AGGORACLE}))
	})

	t.Run("active L1 bridge selecting multidownloader is accepted when enabled", func(t *testing.T) {
		cfg := config.Config{}
		cfg.BridgeL1Sync.Downloader = configtypes.Multidownloader
		cfg.L1Multidownloader.Enabled = true

		require.NoError(t, validateDownloaderSelections(cfg, []string{aggkitcommon.L1BRIDGESYNC}))
	})
}

// TestValidateDownloaderSelections_L2Unsupported checks that the shared multidownloader is rejected
// for an active L2 section and ignored for an inactive one.
func TestValidateDownloaderSelections_L2Unsupported(t *testing.T) {
	t.Run("active L2 bridge selecting multidownloader is rejected", func(t *testing.T) {
		cfg := config.Config{}
		cfg.BridgeL2Sync.Downloader = configtypes.Multidownloader
		cfg.L1Multidownloader.Enabled = true

		require.Error(t, validateDownloaderSelections(cfg, []string{aggkitcommon.L2BRIDGESYNC}))
	})

	t.Run("active L2 claim selecting multidownloader is rejected", func(t *testing.T) {
		cfg := config.Config{}
		cfg.ClaimL2Sync.Downloader = configtypes.Multidownloader
		cfg.L1Multidownloader.Enabled = true

		require.Error(t, validateDownloaderSelections(cfg, []string{aggkitcommon.L2CLAIMSYNC}))
	})

	t.Run("inactive L2 selecting multidownloader is accepted", func(t *testing.T) {
		cfg := config.Config{}
		cfg.BridgeL2Sync.Downloader = configtypes.Multidownloader
		cfg.ClaimL2Sync.Downloader = configtypes.Multidownloader
		cfg.L1Multidownloader.Enabled = true

		require.NoError(t, validateDownloaderSelections(cfg, []string{aggkitcommon.L1INFOTREESYNC}))
	})

	t.Run("active L2 bridge with legacy is accepted", func(t *testing.T) {
		cfg := config.Config{}
		cfg.BridgeL2Sync.Downloader = configtypes.LegacyDownloader

		require.NoError(t, validateDownloaderSelections(cfg, []string{aggkitcommon.L2BRIDGESYNC}))
	})
}

// TestValidateDownloaderSelections_InvalidSelectorAlwaysRejected checks that selector types are
// validated regardless of whether the section is active.
func TestValidateDownloaderSelections_InvalidSelectorAlwaysRejected(t *testing.T) {
	t.Run("invalid selector on an active section is rejected", func(t *testing.T) {
		cfg := config.Config{}
		cfg.BridgeL1Sync.Downloader = configtypes.Downloader("bogus")

		require.Error(t, validateDownloaderSelections(cfg, []string{aggkitcommon.L1BRIDGESYNC}))
	})

	t.Run("invalid selector on an inactive section is rejected", func(t *testing.T) {
		cfg := config.Config{}
		cfg.ClaimL2Sync.Downloader = configtypes.Downloader("bogus")

		require.Error(t, validateDownloaderSelections(cfg, []string{aggkitcommon.L1INFOTREESYNC}))
	})
}

// TestBootL1Syncers_Ordering pins the startup ordering that prevents the reorg-detector subscription
// race and the "cannot add new syncer config after initialization" error: every L1 syncer is built
// before the multidownloader is initialized, and no L1 consumer starts before it does.
func TestBootL1Syncers_Ordering(t *testing.T) {
	type hookErrors struct {
		buildL1 error
		initMD  error
		startL1 error
	}

	newHooks := func(order *[]string, errs hookErrors, withL2 bool) l1BootHooks {
		hooks := l1BootHooks{
			buildL1Syncers: func() error {
				*order = append(*order, "buildL1")
				return errs.buildL1
			},
			initMD: func() error {
				*order = append(*order, "initMD")
				return errs.initMD
			},
			startMD: func() error {
				*order = append(*order, "startMD")
				return nil
			},
			startL1Consumers: func() error {
				*order = append(*order, "startL1")
				return errs.startL1
			},
		}
		if withL2 {
			hooks.buildL2Syncers = func() error {
				*order = append(*order, "buildL2")
				return nil
			}
		}
		return hooks
	}

	t.Run("with multidownloader the L1 consumers start last", func(t *testing.T) {
		var order []string
		require.NoError(t, bootL1Syncers(true, newHooks(&order, hookErrors{}, true)))
		require.Equal(t, []string{"buildL1", "buildL2", "initMD", "startMD", "startL1"}, order)
	})

	t.Run("without multidownloader consumers still start", func(t *testing.T) {
		var order []string
		require.NoError(t, bootL1Syncers(false, newHooks(&order, hookErrors{}, true)))
		require.Equal(t, []string{"buildL1", "buildL2", "startL1"}, order)
	})

	t.Run("L2 hook is optional", func(t *testing.T) {
		var order []string
		require.NoError(t, bootL1Syncers(false, newHooks(&order, hookErrors{}, false)))
		require.Equal(t, []string{"buildL1", "startL1"}, order)
	})

	t.Run("initialize failure aborts before starting the multidownloader or consumers", func(t *testing.T) {
		var order []string
		initErr := errors.New("initialize failed")
		err := bootL1Syncers(true, newHooks(&order, hookErrors{initMD: initErr}, true))
		require.ErrorIs(t, err, initErr)
		require.Equal(t, []string{"buildL1", "buildL2", "initMD"}, order)
	})

	t.Run("build failure aborts before anything else", func(t *testing.T) {
		var order []string
		buildErr := errors.New("build failed")
		err := bootL1Syncers(true, newHooks(&order, hookErrors{buildL1: buildErr}, true))
		require.ErrorIs(t, err, buildErr)
		require.Equal(t, []string{"buildL1"}, order)
	})

	t.Run("start consumer failure is propagated", func(t *testing.T) {
		var order []string
		startErr := errors.New("start failed")
		err := bootL1Syncers(true, newHooks(&order, hookErrors{startL1: startErr}, true))
		require.ErrorIs(t, err, startErr)
		require.Equal(t, []string{"buildL1", "buildL2", "initMD", "startMD", "startL1"}, order)
	})
}
