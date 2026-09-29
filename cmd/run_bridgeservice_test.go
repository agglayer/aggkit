package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	bridgetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/claimsync"
	"github.com/agglayer/aggkit/config"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/l2gersync"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const testNetworkID = uint32(10)

func TestBuildPublicConfig(t *testing.T) {
	finalizedBlock, err := aggkittypes.NewBlockNumberFinality("FinalizedBlock")
	require.NoError(t, err)
	latestBlock, err := aggkittypes.NewBlockNumberFinality("LatestBlock")
	require.NoError(t, err)

	globalExitRootManagerAddr := common.HexToAddress("0x1111111111111111111111111111111111111111")
	rollupManagerAddr := common.HexToAddress("0x2222222222222222222222222222222222222222")
	bridgeL1Addr := common.HexToAddress("0x3333333333333333333333333333333333333333")
	globalExitRootL2Addr := common.HexToAddress("0x4444444444444444444444444444444444444444")
	bridgeL2Addr := common.HexToAddress("0x5555555555555555555555555555555555555555")

	cfg := &config.Config{
		L1NetworkConfig: ethermanconfig.L1NetworkConfig{
			GlobalExitRootManagerAddr: globalExitRootManagerAddr,
			RollupManagerAddr:         rollupManagerAddr,
		},
		L1InfoTreeSync: l1infotreesync.Config{
			BlockFinality:      *finalizedBlock,
			InitialBlock:       100,
			SyncBlockChunkSize: 50,
		},
		BridgeL1Sync: bridgesync.Config{
			BlockFinality:      *latestBlock,
			InitialBlockNum:    0,
			SyncBlockChunkSize: 100,
			BridgeAddr:         bridgeL1Addr,
		},
		BridgeL2Sync: bridgesync.Config{
			BlockFinality:      *latestBlock,
			InitialBlockNum:    0,
			SyncBlockChunkSize: 100,
			BridgeAddr:         bridgeL2Addr,
		},
		L2GERSync: l2gersync.Config{
			BlockFinality:        *latestBlock,
			InitialBlockNum:      0,
			SyncBlockChunkSize:   100,
			GlobalExitRootL2Addr: globalExitRootL2Addr,
		},
	}

	allRunning := runningBridgeComponents{
		L1InfoTreeSync: true,
		BridgeL1Sync:   true,
		BridgeL2Sync:   true,
		L2GERSync:      true,
	}
	got, err := buildPublicConfig(cfg, testNetworkID, string(l2gersync.SovereignChain), allRunning)
	require.NoError(t, err)

	expectedInternalChecksum, err := cfg.Checksum()
	require.NoError(t, err)
	require.NotEmpty(t, expectedInternalChecksum)

	expected := bridgetypes.PublicConfigResponse{
		NetworkID:              testNetworkID,
		InternalConfigChecksum: expectedInternalChecksum,
		Components: bridgetypes.PublicComponentsConfig{
			L1InfoTreeSync: &bridgetypes.SyncComponentConfig{
				BlockFinality: "FinalizedBlock", InitialBlock: 100, SyncBlockChunkSize: 50,
			},
			BridgeL1Sync: &bridgetypes.SyncComponentConfig{
				BlockFinality: "LatestBlock", InitialBlock: 0, SyncBlockChunkSize: 100,
			},
			BridgeL2Sync: &bridgetypes.SyncComponentConfig{
				BlockFinality: "LatestBlock", InitialBlock: 0, SyncBlockChunkSize: 100,
			},
			L2GERSync: &bridgetypes.L2GERSyncComponentConfig{
				SyncComponentConfig: bridgetypes.SyncComponentConfig{
					BlockFinality: "LatestBlock", InitialBlock: 0, SyncBlockChunkSize: 100,
				},
				SyncMode: "SovereignChain",
			},
		},
		Contracts: bridgetypes.PublicContractsConfig{
			L1: bridgetypes.L1ContractsConfig{
				GlobalExitRootAddr: bridgetypes.Address(globalExitRootManagerAddr.Hex()),
				RollupManagerAddr:  bridgetypes.Address(rollupManagerAddr.Hex()),
				BridgeAddr:         bridgetypes.Address(bridgeL1Addr.Hex()),
			},
			L2: bridgetypes.L2ContractsConfig{
				GlobalExitRootAddr: bridgetypes.Address(globalExitRootL2Addr.Hex()),
				BridgeAddr:         bridgetypes.Address(bridgeL2Addr.Hex()),
			},
		},
	}

	expectedPublicChecksum, err := expected.PublicChecksum()
	require.NoError(t, err)
	require.NotEmpty(t, expectedPublicChecksum)
	expected.PublicConfigChecksum = expectedPublicChecksum

	require.Equal(t, expected, got)
}

func TestBuildPublicConfig_L2GERSyncModeEmptyWhenRunningWithoutMode(t *testing.T) {
	cfg := &config.Config{}

	got, err := buildPublicConfig(cfg, testNetworkID, "", runningBridgeComponents{L2GERSync: true})
	require.NoError(t, err)

	require.NotNil(t, got.Components.L2GERSync)
	require.Empty(t, got.Components.L2GERSync.SyncMode)
}

func TestBuildPublicConfig_ComponentsOmittedWhenNotRunning(t *testing.T) {
	cfg := &config.Config{}

	// None of the components are running: all of them must be omitted from the response,
	// including from its JSON encoding (relies on the omitempty tags in PublicComponentsConfig).
	got, err := buildPublicConfig(cfg, testNetworkID, "", runningBridgeComponents{})
	require.NoError(t, err)

	require.Nil(t, got.Components.L1InfoTreeSync)
	require.Nil(t, got.Components.BridgeL1Sync)
	require.Nil(t, got.Components.BridgeL2Sync)
	require.Nil(t, got.Components.L2GERSync)

	marshaled, err := json.Marshal(got.Components)
	require.NoError(t, err)
	require.JSONEq(t, "{}", string(marshaled))
}

func TestBuildPublicConfig_OnlyRunningComponentsPopulated(t *testing.T) {
	cfg := &config.Config{}

	got, err := buildPublicConfig(cfg, testNetworkID, "", runningBridgeComponents{
		BridgeL1Sync: true,
		L2GERSync:    true,
	})
	require.NoError(t, err)

	require.Nil(t, got.Components.L1InfoTreeSync)
	require.NotNil(t, got.Components.BridgeL1Sync)
	require.Nil(t, got.Components.BridgeL2Sync)
	require.NotNil(t, got.Components.L2GERSync)
}

func TestBuildPublicConfig_InternalChecksumChangesWithConfig(t *testing.T) {
	cfgA := &config.Config{}
	cfgB := &config.Config{}
	cfgB.BridgeL1Sync.SyncBlockChunkSize = 42

	gotA, err := buildPublicConfig(cfgA, testNetworkID, "", runningBridgeComponents{})
	require.NoError(t, err)
	gotB, err := buildPublicConfig(cfgB, testNetworkID, "", runningBridgeComponents{})
	require.NoError(t, err)

	// Same config -> same checksum, deterministically
	gotAAgain, err := buildPublicConfig(cfgA, testNetworkID, "", runningBridgeComponents{})
	require.NoError(t, err)
	require.Equal(t, gotA.InternalConfigChecksum, gotAAgain.InternalConfigChecksum)

	// Different config -> different checksum, even though BridgeL1Sync isn't running (and thus
	// isn't part of the public config): the internal checksum covers the entire configuration.
	require.NotEqual(t, gotA.InternalConfigChecksum, gotB.InternalConfigChecksum)
}

func TestBuildPublicConfig_PublicChecksumChangesWithPublicConfig(t *testing.T) {
	cfgA := &config.Config{}
	cfgB := &config.Config{}
	cfgB.BridgeL1Sync.SyncBlockChunkSize = 42
	running := runningBridgeComponents{BridgeL1Sync: true}

	gotA, err := buildPublicConfig(cfgA, testNetworkID, "", running)
	require.NoError(t, err)
	gotB, err := buildPublicConfig(cfgB, testNetworkID, "", running)
	require.NoError(t, err)

	require.NotEqual(t, gotA.PublicConfigChecksum, gotB.PublicConfigChecksum)
	require.NotEqual(t, gotA.InternalConfigChecksum, gotB.InternalConfigChecksum)
}

func TestBuildPublicConfig_PublicChecksumStableWhenOnlyInternalConfigChanges(t *testing.T) {
	cfgA := &config.Config{}
	cfgB := &config.Config{}
	// DBPath isn't exposed in the public config, so it must not affect PublicConfigChecksum.
	cfgB.BridgeL1Sync.DBPath = "/some/other/path"
	running := runningBridgeComponents{BridgeL1Sync: true}

	gotA, err := buildPublicConfig(cfgA, testNetworkID, "", running)
	require.NoError(t, err)
	gotB, err := buildPublicConfig(cfgB, testNetworkID, "", running)
	require.NoError(t, err)

	require.Equal(t, gotA.PublicConfigChecksum, gotB.PublicConfigChecksum)
	require.NotEqual(t, gotA.InternalConfigChecksum, gotB.InternalConfigChecksum)
}

// TestBridgerOrNil_NilPointerYieldsUntypedNilInterface proves bridgerOrNil returns a genuine nil
// interface for a nil *bridgesync.BridgeSync. Assigning that nil pointer directly to a
// bridgeservice.Bridger-typed variable instead (skipping the helper) would produce a non-nil
// interface wrapping a nil pointer -- require.True(t, iface == nil) is deliberately used instead
// of require.Nil, because testify's require.Nil looks through that wrapping via reflection and
// would report a typed nil as nil too, hiding exactly the bug this guards against.
func TestBridgerOrNil_NilPointerYieldsUntypedNilInterface(t *testing.T) {
	var nilPtr *bridgesync.BridgeSync
	iface := bridgerOrNil(nilPtr)
	require.True(t, iface == nil, "a nil *bridgesync.BridgeSync must become an untyped nil interface")

	iface = bridgerOrNil(&bridgesync.BridgeSync{})
	require.False(t, iface == nil, "a non-nil *bridgesync.BridgeSync must still convert normally")
}

// TestClaimerOrNil_NilPointerYieldsUntypedNilInterface is claimerOrNil's counterpart to
// TestBridgerOrNil_NilPointerYieldsUntypedNilInterface.
func TestClaimerOrNil_NilPointerYieldsUntypedNilInterface(t *testing.T) {
	var nilPtr *claimsync.ClaimSync
	iface := claimerOrNil(nilPtr)
	require.True(t, iface == nil, "a nil *claimsync.ClaimSync must become an untyped nil interface")

	iface = claimerOrNil(&claimsync.ClaimSync{})
	require.False(t, iface == nil, "a non-nil *claimsync.ClaimSync must still convert normally")
}

// TestL1InfoTreeSyncerOrNil_NilPointerYieldsUntypedNilInterface is
// l1InfoTreeSyncerOrNil's counterpart to TestBridgerOrNil_NilPointerYieldsUntypedNilInterface.
func TestL1InfoTreeSyncerOrNil_NilPointerYieldsUntypedNilInterface(t *testing.T) {
	var nilPtr *l1infotreesync.L1InfoTreeSync
	iface := l1InfoTreeSyncerOrNil(nilPtr)
	require.True(t, iface == nil, "a nil *l1infotreesync.L1InfoTreeSync must become an untyped nil interface")

	iface = l1InfoTreeSyncerOrNil(&l1infotreesync.L1InfoTreeSync{})
	require.False(t, iface == nil, "a non-nil *l1infotreesync.L1InfoTreeSync must still convert normally")
}

// TestL2GERSyncerOrNil_NilPointerYieldsUntypedNilInterface is l2GERSyncerOrNil's counterpart to
// TestBridgerOrNil_NilPointerYieldsUntypedNilInterface.
func TestL2GERSyncerOrNil_NilPointerYieldsUntypedNilInterface(t *testing.T) {
	var nilPtr *l2gersync.L2GERSync
	iface := l2GERSyncerOrNil(nilPtr)
	require.True(t, iface == nil, "a nil *l2gersync.L2GERSync must become an untyped nil interface")

	iface = l2GERSyncerOrNil(&l2gersync.L2GERSync{})
	require.False(t, iface == nil, "a non-nil *l2gersync.L2GERSync must still convert normally")
}

// TestCreateBridgeService_NilSyncersDoNotPanic is a regression test for the typed-nil hazard the
// *OrNil helpers fix: a bridge-service instance that doesn't run every syncer (e.g.
// --components=l1bridgesync,l2bridgesync,l2gersync, which has no l1infotreesync or claim syncers)
// must still answer 200 on both public endpoints, with the unconfigured syncers' entries either
// omitted (the three new ones) or in their "not configured" shape (the three legacy ones), never
// a panic turned into a 500 by gin's Recovery middleware. The router below is wired with
// gin.Recovery() exactly like the production HTTP server (common.NewHTTPServer), so a regression
// here reproduces the exact symptom this fixes rather than crashing the test binary outright.
func TestCreateBridgeService_NilSyncersDoNotPanic(t *testing.T) {
	b := createBridgeService(
		&config.Config{}, "", testNetworkID, runningBridgeComponents{},
		nil, // upgradeQuery: unused by the endpoints this test exercises
		nil, // l1InfoTree
		nil, // injectedGERs
		nil, // bridgeL1
		nil, // bridgeL2
		nil, // claimL1
		nil, // claimL2
	)

	router := gin.New()
	router.Use(gin.Recovery())
	b.RegisterRoutes(router)

	for _, path := range []string{"/bridge/v1/sync-status", "/health"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, w.Code, "%s must answer 200 even when no syncer is configured", path)
	}

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/bridge/v1/sync-status", nil))
	require.Equal(t, http.StatusOK, w.Code)

	var status bridgetypes.SyncStatus
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &status))
	require.False(t, status.L1Info.IsActive, "l1_info must be in its not-configured shape")
	require.False(t, status.L1Info.IsHalted)
	require.False(t, status.L2Info.IsActive, "l2_info must be in its not-configured shape")
	require.False(t, status.L2GERInfo.IsActive, "l2_ger_info must be in its not-configured shape")
	require.Nil(t, status.L1InfoTreeInfo, "l1_info_tree_info must be omitted when l1infotreesync is not configured")
	require.Nil(t, status.ClaimL1Info, "claim_l1_info must be omitted when claimsync L1 is not configured")
	require.Nil(t, status.ClaimL2Info, "claim_l2_info must be omitted when claimsync L2 is not configured")
}
