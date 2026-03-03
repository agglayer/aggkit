package e2e

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/0xPolygon/cdk-rpc/rpc"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/validator"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/log"
	bfl "github.com/agglayer/aggkit/tools/backward_forward_let"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	gethkeystore "github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

const (
	bflCertSettleTimeout = 10 * time.Minute
	bflRestartTimeout    = 5 * time.Minute
	bflBridgeIndexWait   = 2 * time.Minute
)

// bflRunNonce is a unique value computed once per test binary run.
// It is injected as Metadata into fake bridge exits so that cert IDs (which
// include a hash of bridge-exit leaves) are unique across test runs. This
// prevents a stale agglayer "InError" cert from a previous run blocking the
// current run, since the agglayer deduplicates certs by their CertificateID.
var bflRunNonce = big.NewInt(time.Now().UnixNano()).Bytes()

// summaryForBFLToolConfig is a minimal struct for reading summary.json to build the bfl tool config.
type summaryForBFLToolConfig struct {
	Networks struct {
		L1 struct {
			Services struct {
				Geth struct {
					HTTPRpc struct {
						External string `json:"external"`
					} `json:"http_rpc"`
				} `json:"geth"`
			} `json:"services"`
		} `json:"l1"`
		Agglayer struct {
			Services struct {
				GrpcRPC struct {
					External string `json:"external"`
				} `json:"grpc_rpc"`
				AdminAPI struct {
					External string `json:"external"`
				} `json:"admin_api"`
			} `json:"services"`
		} `json:"agglayer"`
		L2Networks map[string]struct {
			Services struct {
				Aggkit struct {
					BridgeService struct {
						External string `json:"external"`
					} `json:"rest_api"`
				} `json:"aggkit"`
				OpGeth struct {
					HTTPRpc struct {
						External string `json:"external"`
					} `json:"http_rpc"`
				} `json:"op-geth"`
			} `json:"services"`
		} `json:"l2_networks"`
	} `json:"networks"`
}

// bflOriginalConfig stores the backed-up aggkit config content for restoration.
// Only valid between enableDebugSendCertEndpoint and disableDebugSendCertEndpoint calls.
var bflOriginalConfig []byte

// =============================================================================
// Test functions
// =============================================================================

// TestBackwardForwardLET_NoDivergence verifies that Diagnose returns NoDivergence on a healthy system.
func TestBackwardForwardLET_NoDivergence(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	cfg := prepareBFLToolConfig(t, testEnv.AggsenderRPCURL)
	toolEnv, err := bfl.SetupEnv(ctx, cfg)
	require.NoError(t, err)
	defer toolEnv.Close()

	diagnosis, err := bfl.Diagnose(ctx, toolEnv)
	require.NoError(t, err)
	require.Equal(t, bfl.NoDivergence, diagnosis.Case, "expected NoDivergence on healthy system")
}

// TestBackwardForwardLET_Case1 verifies Case1 (ForwardLET only: 1 divergent leaf, no extra L2 bridges).
func TestBackwardForwardLET_Case1(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Minute)
	defer cancel()

	authKey := testEnv.Keys.SovereignAdmin

	// Enable debug endpoint so we can send certs manually.
	enableDebugSendCertEndpoint(ctx, t, authKey)

	// Build bfl tool environment.
	cfg := prepareBFLToolConfig(t, testEnv.AggsenderRPCURL)
	toolEnv, err := bfl.SetupEnv(ctx, cfg)
	require.NoError(t, err)
	defer toolEnv.Close()

	// Record initial L2 deposit count.
	callOpts := &bind.CallOpts{Context: ctx}
	initialDCBig, err := toolEnv.L2Bridge.DepositCount(callOpts)
	require.NoError(t, err)
	initialDC := uint32(initialDCBig.Uint64())

	certSignerKey := loadCertSignerKey(t)

	// Build and send 1 malicious cert with 1 fake bridge exit.
	// On a fresh environment there is no settled cert, so buildMaliciousCert
	// will start at height=0 from the empty L2 bridge state.
	fakeBridgeExits := []*agglayertypes.BridgeExit{makeFakeBridgeExit(0)}
	cert := buildMaliciousCert(ctx, t, toolEnv, fakeBridgeExits, certSignerKey)
	sendMaliciousCertificate(ctx, t, toolEnv, cert, authKey)
	log.Infof("[Case1] sent malicious cert height=%d", cert.Height)

	waitForCertificateToSettle(ctx, t, toolEnv, cert.Height)

	// Restore normal aggkit mode before diagnosis.
	disableDebugSendCertEndpoint(ctx, t)

	// Re-build toolEnv with fresh state.
	toolEnv.Close()
	cfg2 := prepareBFLToolConfig(t, testEnv.AggsenderRPCURL)
	toolEnv, err = bfl.SetupEnv(ctx, cfg2)
	require.NoError(t, err)

	// Diagnose.
	diagnosis, err := bfl.Diagnose(ctx, toolEnv)
	require.NoError(t, err)
	require.Equal(t, bfl.Case1, diagnosis.Case)
	require.Len(t, diagnosis.DivergentLeaves, 1)
	require.NotEmpty(t, diagnosis.Undercollateralization)

	// Execute recovery.
	recoveryCtx, recoveryCancel := context.WithTimeout(ctx, 10*time.Minute)
	defer recoveryCancel()
	err = bfl.ExecuteRecovery(recoveryCtx, toolEnv, diagnosis)
	require.NoError(t, err)

	// Verify post-recovery state.
	callOpts2 := &bind.CallOpts{Context: ctx}
	postDCBig, err := toolEnv.L2Bridge.DepositCount(callOpts2)
	require.NoError(t, err)
	require.Equal(t, initialDC+1, uint32(postDCBig.Uint64()),
		"deposit count should be initial+1 after recovery")

	root, err := toolEnv.L2Bridge.GetRoot(callOpts2)
	require.NoError(t, err)
	require.Equal(t, diagnosis.L1SettledLER, common.Hash(root),
		"L2 LER should match L1 settled LER after recovery")

	inEmergency, err := toolEnv.L2Bridge.IsEmergencyState(callOpts2)
	require.NoError(t, err)
	require.False(t, inEmergency, "L2 bridge should not be in emergency state after recovery")
}

// TestBackwardForwardLET_Case2 verifies Case2 (BackwardLET + ForwardLET: 1 divergent leaf + extra L2 bridges).
func TestBackwardForwardLET_Case2(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Minute)
	defer cancel()

	authKey := testEnv.Keys.SovereignAdmin

	// Enable debug endpoint first so we can send certs manually.
	enableDebugSendCertEndpoint(ctx, t, authKey)

	// Build bfl tool environment.
	cfg := prepareBFLToolConfig(t, testEnv.AggsenderRPCURL)
	toolEnv, err := bfl.SetupEnv(ctx, cfg)
	require.NoError(t, err)
	defer toolEnv.Close()

	certSignerKey := loadCertSignerKey(t)

	// Build and send 1 malicious cert with 1 fake bridge exit.
	fakeBridgeExits := []*agglayertypes.BridgeExit{makeFakeBridgeExit(0)}
	cert := buildMaliciousCert(ctx, t, toolEnv, fakeBridgeExits, certSignerKey)
	sendMaliciousCertificate(ctx, t, toolEnv, cert, authKey)
	log.Infof("[Case2] sent malicious cert height=%d", cert.Height)

	waitForCertificateToSettle(ctx, t, toolEnv, cert.Height)

	// Create 2 real L2 bridge deposits BEFORE disabling debug mode so that the bridge service
	// (already running and synced) can index them quickly within createL2BridgeNoClaim's own
	// poll. After the subsequent aggkit restart we wait for re-sync separately.
	// With DivergencePoint=0 and l2CurrentDC=2, collectExtraL2Bridges(1,2)=[bridge at DC=1].
	createL2BridgeNoClaim(ctx, t)
	createL2BridgeNoClaim(ctx, t)

	// Restore normal aggkit mode.
	disableDebugSendCertEndpoint(ctx, t)

	// After aggkit restart the bridge service re-syncs from genesis. Wait until it has
	// re-indexed all L2 bridges so that Diagnose can see the extra bridges.
	waitForBridgeServiceSynced(ctx, t)

	// Re-build toolEnv.
	toolEnv.Close()
	cfg2 := prepareBFLToolConfig(t, testEnv.AggsenderRPCURL)
	toolEnv, err = bfl.SetupEnv(ctx, cfg2)
	require.NoError(t, err)

	// Diagnose.
	diagnosis, err := bfl.Diagnose(ctx, toolEnv)
	require.NoError(t, err)
	require.Equal(t, bfl.Case2, diagnosis.Case)
	require.Len(t, diagnosis.DivergentLeaves, 1)
	require.NotEmpty(t, diagnosis.ExtraL2Bridges)

	// Execute recovery.
	recoveryCtx, recoveryCancel := context.WithTimeout(ctx, 10*time.Minute)
	defer recoveryCancel()
	err = bfl.ExecuteRecovery(recoveryCtx, toolEnv, diagnosis)
	require.NoError(t, err)

	// Verify: DC should equal DivergencePoint + divergent leaves + extra real bridges.
	// For Case2, L2 LER will NOT match L1 settled LER because extra real L2 bridges were
	// appended after the fake leaf; the next aggsender cert will advance L1 to match.
	callOpts := &bind.CallOpts{Context: ctx}
	expectedDC := diagnosis.DivergencePoint + uint32(len(diagnosis.DivergentLeaves)) +
		uint32(len(diagnosis.ExtraL2Bridges))
	postDCBig, err := toolEnv.L2Bridge.DepositCount(callOpts)
	require.NoError(t, err)
	require.Equal(t, expectedDC, uint32(postDCBig.Uint64()),
		"deposit count should equal DivergencePoint+divergent+extraL2 after recovery")

	inEmergency, err := toolEnv.L2Bridge.IsEmergencyState(callOpts)
	require.NoError(t, err)
	require.False(t, inEmergency, "L2 bridge should not be in emergency state after recovery")
}

// TestBackwardForwardLET_Case3 verifies Case3 (ForwardLET only: 2 divergent leaves, no extra L2 bridges).
func TestBackwardForwardLET_Case3(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Minute)
	defer cancel()

	authKey := testEnv.Keys.SovereignAdmin

	// Enable debug endpoint first.
	enableDebugSendCertEndpoint(ctx, t, authKey)

	// Build first bfl tool environment.
	cfg := prepareBFLToolConfig(t, testEnv.AggsenderRPCURL)
	toolEnv, err := bfl.SetupEnv(ctx, cfg)
	require.NoError(t, err)
	defer toolEnv.Close()

	certSignerKey := loadCertSignerKey(t)

	// Send first malicious cert.
	fake1 := []*agglayertypes.BridgeExit{makeFakeBridgeExit(0)}
	cert1 := buildMaliciousCert(ctx, t, toolEnv, fake1, certSignerKey)
	sendMaliciousCertificate(ctx, t, toolEnv, cert1, authKey)
	log.Infof("[Case3] sent malicious cert1 height=%d", cert1.Height)
	waitForCertificateToSettle(ctx, t, toolEnv, cert1.Height)

	// Re-query state to get the new settled LER for cert2.
	toolEnv.Close()
	cfg = prepareBFLToolConfig(t, testEnv.AggsenderRPCURL)
	toolEnv, err = bfl.SetupEnv(ctx, cfg)
	require.NoError(t, err)

	// Send second malicious cert.
	fake2 := []*agglayertypes.BridgeExit{makeFakeBridgeExit(1)}
	cert2 := buildMaliciousCert(ctx, t, toolEnv, fake2, certSignerKey)
	sendMaliciousCertificate(ctx, t, toolEnv, cert2, authKey)
	log.Infof("[Case3] sent malicious cert2 height=%d", cert2.Height)
	waitForCertificateToSettle(ctx, t, toolEnv, cert2.Height)

	// Restore normal aggkit mode.
	disableDebugSendCertEndpoint(ctx, t)

	// Re-build toolEnv for diagnosis.
	toolEnv.Close()
	cfg2 := prepareBFLToolConfig(t, testEnv.AggsenderRPCURL)
	toolEnv, err = bfl.SetupEnv(ctx, cfg2)
	require.NoError(t, err)

	// Diagnose.
	diagnosis, err := bfl.Diagnose(ctx, toolEnv)
	require.NoError(t, err)
	require.Equal(t, bfl.Case3, diagnosis.Case)
	require.Len(t, diagnosis.DivergentLeaves, 2)
	require.Empty(t, diagnosis.ExtraL2Bridges)

	// Execute recovery.
	recoveryCtx, recoveryCancel := context.WithTimeout(ctx, 10*time.Minute)
	defer recoveryCancel()
	err = bfl.ExecuteRecovery(recoveryCtx, toolEnv, diagnosis)
	require.NoError(t, err)

	// Verify.
	callOpts := &bind.CallOpts{Context: ctx}
	root, err := toolEnv.L2Bridge.GetRoot(callOpts)
	require.NoError(t, err)
	require.Equal(t, diagnosis.L1SettledLER, common.Hash(root),
		"L2 LER should match L1 settled LER after recovery")

	inEmergency, err := toolEnv.L2Bridge.IsEmergencyState(callOpts)
	require.NoError(t, err)
	require.False(t, inEmergency, "L2 bridge should not be in emergency state after recovery")
}

// TestBackwardForwardLET_Case4 verifies Case4 (BackwardLET + ForwardLET: 2 divergent leaves + extra L2 bridges).
func TestBackwardForwardLET_Case4(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Minute)
	defer cancel()

	authKey := testEnv.Keys.SovereignAdmin

	// Enable debug endpoint first.
	enableDebugSendCertEndpoint(ctx, t, authKey)

	// Build bfl tool environment.
	cfg := prepareBFLToolConfig(t, testEnv.AggsenderRPCURL)
	toolEnv, err := bfl.SetupEnv(ctx, cfg)
	require.NoError(t, err)
	defer toolEnv.Close()

	certSignerKey := loadCertSignerKey(t)

	// Send first malicious cert.
	fake1 := []*agglayertypes.BridgeExit{makeFakeBridgeExit(0)}
	cert1 := buildMaliciousCert(ctx, t, toolEnv, fake1, certSignerKey)
	sendMaliciousCertificate(ctx, t, toolEnv, cert1, authKey)
	log.Infof("[Case4] sent malicious cert1 height=%d", cert1.Height)
	waitForCertificateToSettle(ctx, t, toolEnv, cert1.Height)

	// Re-query state and send cert2.
	toolEnv.Close()
	cfg = prepareBFLToolConfig(t, testEnv.AggsenderRPCURL)
	toolEnv, err = bfl.SetupEnv(ctx, cfg)
	require.NoError(t, err)

	fake2 := []*agglayertypes.BridgeExit{makeFakeBridgeExit(1)}
	cert2 := buildMaliciousCert(ctx, t, toolEnv, fake2, certSignerKey)
	sendMaliciousCertificate(ctx, t, toolEnv, cert2, authKey)
	log.Infof("[Case4] sent malicious cert2 height=%d", cert2.Height)
	waitForCertificateToSettle(ctx, t, toolEnv, cert2.Height)

	// Create 2 real L2 bridge deposits BEFORE disabling debug mode so the bridge service
	// (already running and synced) can index them quickly within createL2BridgeNoClaim's own
	// poll. After the subsequent aggkit restart we wait for re-sync separately.
	// With DivergencePoint=0 and l2CurrentDC=2, collectExtraL2Bridges(1,2)=[bridge at DC=1].
	createL2BridgeNoClaim(ctx, t)
	createL2BridgeNoClaim(ctx, t)

	// Restore normal aggkit mode.
	disableDebugSendCertEndpoint(ctx, t)

	// After aggkit restart the bridge service re-syncs from genesis. Wait until it has
	// re-indexed all L2 bridges so that Diagnose can see the extra bridges.
	waitForBridgeServiceSynced(ctx, t)

	// Re-build toolEnv for diagnosis.
	toolEnv.Close()
	cfg2 := prepareBFLToolConfig(t, testEnv.AggsenderRPCURL)
	toolEnv, err = bfl.SetupEnv(ctx, cfg2)
	require.NoError(t, err)

	// Diagnose.
	diagnosis, err := bfl.Diagnose(ctx, toolEnv)
	require.NoError(t, err)
	require.Equal(t, bfl.Case4, diagnosis.Case)
	require.Len(t, diagnosis.DivergentLeaves, 2)
	require.NotEmpty(t, diagnosis.ExtraL2Bridges)

	// Execute recovery.
	recoveryCtx, recoveryCancel := context.WithTimeout(ctx, 10*time.Minute)
	defer recoveryCancel()
	err = bfl.ExecuteRecovery(recoveryCtx, toolEnv, diagnosis)
	require.NoError(t, err)

	// Verify: DC should equal DivergencePoint + divergent leaves + extra real bridges.
	// For Case4, L2 LER will NOT match L1 settled LER because extra real L2 bridges were
	// appended after the fake leaves; the next aggsender cert will advance L1 to match.
	callOpts := &bind.CallOpts{Context: ctx}
	expectedDC := diagnosis.DivergencePoint + uint32(len(diagnosis.DivergentLeaves)) +
		uint32(len(diagnosis.ExtraL2Bridges))
	postDCBig, err := toolEnv.L2Bridge.DepositCount(callOpts)
	require.NoError(t, err)
	require.Equal(t, expectedDC, uint32(postDCBig.Uint64()),
		"deposit count should equal DivergencePoint+divergent+extraL2 after recovery")

	inEmergency, err := toolEnv.L2Bridge.IsEmergencyState(callOpts)
	require.NoError(t, err)
	require.False(t, inEmergency, "L2 bridge should not be in emergency state after recovery")
}

// TestBackwardForwardLET_AggsenderAPIFallback verifies the full recovery path when the aggsender
// DB is wiped. The test:
//  1. Creates a Case2 diverged state (1 fake bridge exit + 2 real L2 bridges).
//  2. Wipes the aggsender DB by restarting with a fresh StoragePath.
//  3. Runs diagnosis — expects AggsenderAPIFailed=true with cert IDs in MissingCerts.
//  4. Uses the reported cert IDs to call admin_getCertificate on the agglayer admin API.
//  5. Builds a JSON override file from the fetched bridge exits.
//  6. Runs diagnosis again with the override file — expects Case2 classification.
//  7. Executes recovery and verifies the post-recovery L2 state.
func TestBackwardForwardLET_AggsenderAPIFallback(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Minute)
	defer cancel()

	authKey := testEnv.Keys.SovereignAdmin

	// Phase 1: Setup — same structure as TestBackwardForwardLET_Case2.
	enableDebugSendCertEndpoint(ctx, t, authKey)

	cfg := prepareBFLToolConfig(t, testEnv.AggsenderRPCURL)
	toolEnv, err := bfl.SetupEnv(ctx, cfg)
	require.NoError(t, err)
	defer toolEnv.Close()

	certSignerKey := loadCertSignerKey(t)

	fakeBridgeExits := []*agglayertypes.BridgeExit{makeFakeBridgeExit(0)}
	cert := buildMaliciousCert(ctx, t, toolEnv, fakeBridgeExits, certSignerKey)
	sendMaliciousCertificate(ctx, t, toolEnv, cert, authKey)
	log.Infof("[AggsenderFallback] sent malicious cert height=%d", cert.Height)
	waitForCertificateToSettle(ctx, t, toolEnv, cert.Height)

	createL2BridgeNoClaim(ctx, t)
	createL2BridgeNoClaim(ctx, t)

	disableDebugSendCertEndpoint(ctx, t)
	waitForBridgeServiceSynced(ctx, t)

	// Phase 2: Wipe aggsender DB by restarting with a fresh StoragePath.
	// Save the current config so Phase 8 cleanup can restore it.
	configPath := testEnv.GetAggkitConfigPath()
	preWipeConfig, err := os.ReadFile(configPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		// Phase 8: Restore the pre-wipe aggkit config.
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), bflRestartTimeout)
		defer cleanupCancel()
		if restoreErr := testEnv.RestartAggkitWithConfig(cleanupCtx, func(cfgPath string) error {
			return os.WriteFile(cfgPath, preWipeConfig, 0o600)
		}); restoreErr != nil {
			t.Logf("WARNING: failed to restore aggkit config after DB wipe: %v", restoreErr)
		}
	})

	freshPath := fmt.Sprintf("/tmp/aggsender-empty-%d", time.Now().UnixNano())
	restartCtx, restartCancel := context.WithTimeout(ctx, bflRestartTimeout)
	defer restartCancel()
	err = testEnv.RestartAggkitWithConfig(restartCtx, func(cfgPath string) error {
		content, readErr := os.ReadFile(cfgPath)
		if readErr != nil {
			return readErr
		}
		// Inject StoragePath right after the [AggSender] header so it takes precedence
		// over any existing StoragePath further down in the section.
		patched := strings.Replace(
			string(content), "[AggSender]",
			"[AggSender]\nStoragePath = \""+freshPath+"\"", 1,
		)
		return os.WriteFile(cfgPath, []byte(patched), 0o600)
	})
	require.NoError(t, err, "restart aggkit with fresh aggsender storage")

	// Wait for bridge service to re-sync after the restart.
	waitForBridgeServiceSynced(ctx, t)

	// Phase 3: First diagnosis — should report AggsenderAPIFailed because the aggsender DB
	// is empty and cannot supply bridge exits for any settled height.
	toolEnv.Close()
	cfg3 := prepareBFLToolConfig(t, testEnv.AggsenderRPCURL)
	toolEnv, err = bfl.SetupEnv(ctx, cfg3)
	require.NoError(t, err)

	diagnosis, err := bfl.Diagnose(ctx, toolEnv)
	require.NoError(t, err)
	require.True(t, diagnosis.AggsenderAPIFailed,
		"expected AggsenderAPIFailed=true after aggsender DB wipe")
	require.NotEmpty(t, diagnosis.MissingCerts,
		"expected MissingCerts to be non-empty after aggsender DB wipe")
	// The malicious cert IS the latest settled cert, so its ID is auto-resolved.
	require.True(t, diagnosis.MissingCerts[0].CertIDResolved,
		"expected latest settled cert ID to be auto-resolved via agglayer gRPC")

	// Phase 4: Extract bridge exits from the agglayer admin API using cert IDs from tool output.
	summaryPath := filepath.Join(testEnv.EnvDir, "summary.json")
	summaryData, err := os.ReadFile(summaryPath)
	require.NoError(t, err)
	var summary summaryForBFLToolConfig
	require.NoError(t, json.Unmarshal(summaryData, &summary))
	adminURL := summary.Networks.Agglayer.Services.AdminAPI.External
	require.NotEmpty(t, adminURL, "agglayer admin API URL not found in summary.json")

	type overrideFileFormat struct {
		NetworkID   uint32                                 `json:"network_id"`
		Description string                                 `json:"description"`
		Heights     map[string][]*agglayertypes.BridgeExit `json:"heights"`
	}
	of := overrideFileFormat{
		NetworkID:   testEnv.L2.NetworkID,
		Description: "extracted by E2E test from agglayer admin API",
		Heights:     make(map[string][]*agglayertypes.BridgeExit),
	}
	for _, mc := range diagnosis.MissingCerts {
		require.True(t, mc.CertIDResolved,
			"cert ID at height %d could not be resolved; cannot call admin API", mc.Height)
		adminCert := callAgglayerAdminGetCertificate(t, adminURL, mc.CertID)
		require.NotNil(t, adminCert, "admin_getCertificate returned nil cert for height %d", mc.Height)
		of.Heights[strconv.FormatUint(mc.Height, 10)] = adminCert.BridgeExits
	}

	// Phase 5: Build JSON override file.
	overrideBytes, err := json.Marshal(of)
	require.NoError(t, err)
	overridePath := filepath.Join(t.TempDir(), "override.json")
	require.NoError(t, os.WriteFile(overridePath, overrideBytes, 0o600))

	// Phase 6: Second diagnosis with override file — should classify as Case2.
	toolEnv.Close()
	cfg6 := prepareBFLToolConfigWithOverride(t, testEnv.AggsenderRPCURL, overridePath)
	toolEnv, err = bfl.SetupEnv(ctx, cfg6)
	require.NoError(t, err)

	diagnosis2, err := bfl.Diagnose(ctx, toolEnv)
	require.NoError(t, err)
	require.False(t, diagnosis2.AggsenderAPIFailed,
		"expected AggsenderAPIFailed=false with override file")
	require.Equal(t, bfl.Case2, diagnosis2.Case,
		"expected Case2 diagnosis with override file")
	require.Len(t, diagnosis2.DivergentLeaves, 1,
		"expected 1 divergent leaf (the fake bridge exit)")
	require.NotEmpty(t, diagnosis2.ExtraL2Bridges,
		"expected extra L2 bridges for Case2")

	// Phase 7: Recovery.
	recoveryCtx, recoveryCancel := context.WithTimeout(ctx, 10*time.Minute)
	defer recoveryCancel()
	err = bfl.ExecuteRecovery(recoveryCtx, toolEnv, diagnosis2)
	require.NoError(t, err)

	// Verify post-recovery L2 state.
	callOpts := &bind.CallOpts{Context: ctx}
	expectedDC := diagnosis2.DivergencePoint + uint32(len(diagnosis2.DivergentLeaves)) +
		uint32(len(diagnosis2.ExtraL2Bridges))
	postDCBig, err := toolEnv.L2Bridge.DepositCount(callOpts)
	require.NoError(t, err)
	require.Equal(t, expectedDC, uint32(postDCBig.Uint64()),
		"deposit count should equal DivergencePoint+divergent+extraL2 after recovery")

	inEmergency, err := toolEnv.L2Bridge.IsEmergencyState(callOpts)
	require.NoError(t, err)
	require.False(t, inEmergency, "L2 bridge should not be in emergency state after recovery")
}

// =============================================================================
// Helper functions
// =============================================================================

// enableDebugSendCertEndpoint restarts aggkit with the DebugSendCertificate endpoint enabled.
// Saves the original config for later restoration by disableDebugSendCertEndpoint.
// A t.Cleanup is registered as a safety net.
func enableDebugSendCertEndpoint(ctx context.Context, t *testing.T, authKey *ecdsa.PrivateKey) {
	t.Helper()
	authAddress := crypto.PubkeyToAddress(authKey.PublicKey)

	// Save original config.
	configPath := testEnv.GetAggkitConfigPath()
	originalContent, err := os.ReadFile(configPath)
	require.NoError(t, err, "read aggkit config for backup")
	bflOriginalConfig = originalContent

	restartCtx, restartCancel := context.WithTimeout(ctx, bflRestartTimeout)
	defer restartCancel()

	err = testEnv.RestartAggkitWithConfig(restartCtx, func(cfgPath string) error {
		content, readErr := os.ReadFile(cfgPath)
		if readErr != nil {
			return readErr
		}
		// Inject debug settings right after the [AggSender] table header.
		patched := strings.Replace(
			string(content),
			"[AggSender]",
			fmt.Sprintf("[AggSender]\nEnableDebugSendCertificate = true\nDebugSendCertificateAuthAddress = %q",
				authAddress.Hex()),
			1,
		)
		return os.WriteFile(cfgPath, []byte(patched), 0o600)
	})
	require.NoError(t, err, "restart aggkit with debug cert endpoint")

	// Safety-net cleanup.
	t.Cleanup(func() {
		if bflOriginalConfig != nil {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), bflRestartTimeout)
			defer cleanupCancel()
			restoreErr := testEnv.RestartAggkitWithConfig(cleanupCtx, func(cfgPath string) error {
				return os.WriteFile(cfgPath, bflOriginalConfig, 0o600)
			})
			if restoreErr != nil {
				t.Logf("WARNING: cleanup failed to restore aggkit config: %v", restoreErr)
			} else {
				bflOriginalConfig = nil
			}
		}
	})
}

// disableDebugSendCertEndpoint restores the original aggkit config and restarts aggkit.
func disableDebugSendCertEndpoint(ctx context.Context, t *testing.T) {
	t.Helper()
	if bflOriginalConfig == nil {
		// Already restored (e.g. by cleanup or a prior explicit call).
		return
	}

	savedContent := bflOriginalConfig
	bflOriginalConfig = nil // clear so cleanup is a no-op

	restartCtx, restartCancel := context.WithTimeout(ctx, bflRestartTimeout)
	defer restartCancel()

	err := testEnv.RestartAggkitWithConfig(restartCtx, func(cfgPath string) error {
		return os.WriteFile(cfgPath, savedContent, 0o600)
	})
	require.NoError(t, err, "restart aggkit with original config")
}

// sendMaliciousCertificate signs and sends a malicious certificate via the aggsender debug endpoint.
func sendMaliciousCertificate(
	ctx context.Context, t *testing.T,
	toolEnv *bfl.Env,
	cert *agglayertypes.Certificate,
	authKey *ecdsa.PrivateKey,
) {
	t.Helper()
	_ = ctx
	certHash, err := toolEnv.AggsenderRPC.DebugSendCertificate(cert, authKey)
	require.NoError(t, err, "DebugSendCertificate height=%d", cert.Height)
	log.Infof("[sendMaliciousCertificate] sent cert height=%d hash=%s", cert.Height, certHash.Hex())
}

// waitForCertificateToSettle polls the AggLayer until the certificate at expectedHeight is settled.
func waitForCertificateToSettle(
	ctx context.Context, t *testing.T,
	toolEnv *bfl.Env,
	expectedHeight uint64,
) {
	t.Helper()
	log.Infof("[waitForCertificateToSettle] waiting for height=%d to settle", expectedHeight)
	err := pollWithBackoff(ctx, bflCertSettleTimeout, backoffInitial, backoffMax,
		fmt.Sprintf("cert-settle-h%d", expectedHeight),
		func() (bool, error) {
			hdr, pollErr := toolEnv.AgglayerClient.GetLatestSettledCertificateHeader(ctx, toolEnv.L2NetworkID)
			if pollErr != nil {
				// Non-fatal: may not have settled certs yet.
				return false, nil
			}
			if hdr == nil {
				return false, nil
			}
			return hdr.Height >= expectedHeight && hdr.Status == agglayertypes.Settled, nil
		},
	)
	require.NoError(t, err, "timeout waiting for certificate at height=%d to settle", expectedHeight)
}

// loadCertSignerKey loads the sequencer keystore (the agglayer proof signer for PP networks).
func loadCertSignerKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	keystorePath := filepath.Join(testEnv.EnvDir, "config", "001", "sequencer.keystore")
	contents, err := os.ReadFile(filepath.Clean(keystorePath))
	require.NoError(t, err, "read sequencer keystore")
	key, err := gethkeystore.DecryptKey(contents, keystorePassword)
	require.NoError(t, err, "decrypt sequencer keystore")
	return key.PrivateKey
}

// buildMaliciousCert builds a Certificate with the given fake bridge exits, rooted at the current
// settled LET state (or height=0 if no settled cert exists yet), and signs it with certSignerKey.
// The certificate is not sent; call sendMaliciousCertificate to submit it.
func buildMaliciousCert(
	ctx context.Context, t *testing.T,
	toolEnv *bfl.Env,
	fakeBridgeExits []*agglayertypes.BridgeExit,
	certSignerKey *ecdsa.PrivateKey,
) *agglayertypes.Certificate {
	t.Helper()
	require.NotEmpty(t, fakeBridgeExits, "need at least one fake bridge exit")

	// Step 1 — Read current settled state from AggLayer.
	// If no cert has settled yet (fresh environment), start from height=0 with empty state.
	var certHeight uint64
	var prevLER common.Hash
	var existingLeafCount uint32
	l1InfoTreeLeafCount := uint32(1) // default for height=0; L1 chain has at least 1 leaf

	info, infoErr := toolEnv.AgglayerClient.GetNetworkInfo(ctx, toolEnv.L2NetworkID)
	if infoErr == nil && info.SettledHeight != nil {
		// A previous cert has settled: build on top of it.
		certHeight = *info.SettledHeight + 1
		prevLER = *info.SettledLER
		existingLeafCount = uint32(*info.SettledLETLeafCount)

		// Get the L1InfoTreeLeafCount from the settled cert in the aggsender DB.
		// We query the SETTLED height to avoid picking up a stale malicious cert
		// stored by a previous (failed) test run.
		if settledCert, certErr := toolEnv.AggsenderRPC.GetCertificateHeaderPerHeight(info.SettledHeight); certErr == nil &&
			settledCert != nil && settledCert.Header != nil &&
			settledCert.Header.L1InfoTreeLeafCount > 0 {
			l1InfoTreeLeafCount = settledCert.Header.L1InfoTreeLeafCount
		}
	} else {
		// No settled cert yet — send the very first cert at height=0.
		// Read the actual L2 bridge root (the empty-tree root is non-zero) and
		// deposit count so prevLER and existingLeafCount are accurate.
		callOpts := &bind.CallOpts{Context: ctx}
		root, rootErr := toolEnv.L2Bridge.GetRoot(callOpts)
		require.NoError(t, rootErr, "GetRoot for initial prevLER")
		prevLER = common.Hash(root)

		dcBig, dcErr := toolEnv.L2Bridge.DepositCount(callOpts)
		require.NoError(t, dcErr, "DepositCount for initial leaf count")
		existingLeafCount = uint32(dcBig.Uint64())

		log.Infof("[buildMaliciousCert] no settled cert, height=0, prevLER=%s, dc=%d", prevLER, existingLeafCount)
	}

	// Step 2 — Build existing L2 bridge leaf hashes.
	// When settled certs exist, use aggsender's stored bridge exits for each settled height.
	// This ensures existingHashes matches the agglayer's LET state exactly (including any
	// fake exits from previously sent malicious certs).
	// For a fresh environment (no settled certs), use bridge service data.
	var existingHashes []common.Hash
	if infoErr == nil && info.SettledHeight != nil {
		existingHashes = make([]common.Hash, 0, existingLeafCount)
		for h := uint64(0); h <= *info.SettledHeight; h++ {
			hh := h
			exits, exitsErr := toolEnv.AggsenderRPC.GetCertificateBridgeExits(&hh)
			require.NoError(t, exitsErr, "GetCertificateBridgeExits height=%d", h)
			for _, be := range exits {
				existingHashes = append(existingHashes, bfl.BridgeExitLeafHash(be))
			}
		}
	} else {
		existingHashes = make([]common.Hash, 0, existingLeafCount)
		for dc := range existingLeafCount {
			br, bridgeErr := toolEnv.BridgeService.GetBridgeByDepositCount(ctx, toolEnv.L2NetworkID, dc)
			require.NoError(t, bridgeErr, "GetBridgeByDepositCount dc=%d", dc)
			existingHashes = append(existingHashes, bfl.BridgeResponseLeafHash(br))
		}
	}

	// Step 3 — Compute new leaf hashes for fake exits.
	newHashes := make([]common.Hash, 0, len(fakeBridgeExits))
	for _, be := range fakeBridgeExits {
		newHashes = append(newHashes, bfl.BridgeExitLeafHash(be))
	}

	// Step 4 — Compute the new local exit root.
	newLER, err := bfl.ComputeLERForNewLeaves(existingHashes, newHashes)
	require.NoError(t, err, "ComputeLERForNewLeaves")

	cert := &agglayertypes.Certificate{
		NetworkID:           toolEnv.L2NetworkID,
		Height:              certHeight,
		PrevLocalExitRoot:   prevLER,
		NewLocalExitRoot:    newLER,
		BridgeExits:         fakeBridgeExits,
		L1InfoTreeLeafCount: l1InfoTreeLeafCount,
	}

	// Step 5 — Sign the certificate for PP (PessimisticProof) networks.
	// AggchainDataMultisig.ExtractAggchainParams() returns ZeroHash, so the
	// hash is the same whether computed before or after setting AggchainData.
	hashToSign, err := validator.HashCertificateToSign(cert)
	require.NoError(t, err, "HashCertificateToSign")
	sig, err := crypto.Sign(hashToSign.Bytes(), certSignerKey)
	require.NoError(t, err, "sign certificate hash")

	cert.AggchainData = &agglayertypes.AggchainDataMultisig{
		Multisig: &agglayertypes.Multisig{
			Signatures: []agglayertypes.ECDSAMultisigEntry{
				{Index: 0, Signature: sig},
			},
		},
	}

	return cert
}

// makeFakeBridgeExit builds a fake BridgeExit using Amount=0 so the agglayer's PP
// balance check cannot underflow (zero tokens exported = zero balance needed).
//
// exitIndex differentiates exits within the same test binary run so that Case3/4
// (which send two malicious certs) produce unique leaf hashes for each cert.
//
// DestinationAddress is derived from bflRunNonce+exitIndex to ensure uniqueness across
// runs (so agglayer does not deduplicate certs from previous runs) and within a run
// (so Case3/4 certs produce distinct leaf hashes).
//
// Metadata is nil: BridgeExit.Hash() uses EmptyBytesHash (= keccak256([])) and the
// forwardLET contract also computes keccak256([]) for empty metadata — they agree.
func makeFakeBridgeExit(exitIndex int) *agglayertypes.BridgeExit {
	// Derive a unique DestinationAddress from the run nonce and per-exit index.
	addrBytes := crypto.Keccak256(append(append([]byte(nil), bflRunNonce...), byte(exitIndex)))
	destAddr := common.BytesToAddress(addrBytes)
	return &agglayertypes.BridgeExit{
		LeafType: bridgesynctypes.LeafTypeAsset,
		TokenInfo: &agglayertypes.TokenInfo{
			OriginNetwork:      0,                // mainnet native token
			OriginTokenAddress: common.Address{}, // native ETH address
		},
		DestinationNetwork: 0,             // L1 (mainnet); cannot exit to the same network as origin (L2=1)
		DestinationAddress: destAddr,      // unique per run+exitIndex
		Amount:             big.NewInt(0), // zero amount avoids PP balance-underflow rejection
		Metadata:           nil,           // nil: consistent with forwardLET contract's keccak256([])
	}
}

// createL2BridgeNoClaim performs an L2→L1 BridgeAsset call using the MintableERC20 token
// (L2-native tokens bypass the Local Balance Tree underflow check) and waits for the bridge
// service to index it. Does NOT claim on L1. Used to create "extra L2 bridges" for Case2/4 tests.
func createL2BridgeNoClaim(ctx context.Context, t *testing.T) {
	t.Helper()
	l2Opts, l2Key, err := testEnv.Keys.L2Keys.Checkout()
	require.NoError(t, err, "checkout L2 key")
	defer testEnv.Keys.L2Keys.Return(l2Key)

	amount := big.NewInt(1e18) // 1 TEST token

	// Mint tokens to the sender so they have a balance to bridge out.
	log.Infof("[createL2BridgeNoClaim] minting TEST tokens to %s", l2Opts.From.Hex())
	mintTx, err := testEnv.L2.Contracts.MintableERC20.Mint(l2Opts, l2Opts.From, amount)
	require.NoError(t, err, "mint MintableERC20")
	_, err = bind.WaitMined(ctx, testEnv.Clients.L2, mintTx)
	require.NoError(t, err, "wait for mint tx")

	// Approve the L2 bridge to pull tokens on behalf of the sender.
	log.Infof("[createL2BridgeNoClaim] approving L2 bridge to spend TEST tokens")
	approveTx, err := testEnv.L2.Contracts.MintableERC20.Approve(
		l2Opts, testEnv.L2.Contracts.L2BridgeAddress, amount,
	)
	require.NoError(t, err, "approve L2 bridge for MintableERC20")
	_, err = bind.WaitMined(ctx, testEnv.Clients.L2, approveTx)
	require.NoError(t, err, "wait for approve tx")

	// Bridge ERC20 tokens L2→L1. No ETH value needed.
	log.Infof("[createL2BridgeNoClaim] sending L2 BridgeAsset (ERC20)")
	tx, err := testEnv.L2.Contracts.L2Bridge.BridgeAsset(
		l2Opts, 0, l2Opts.From, amount, testEnv.L2.Contracts.MintableERC20Address, true, nil,
	)
	require.NoError(t, err, "L2 BridgeAsset")

	receipt, err := bind.WaitMined(ctx, testEnv.Clients.L2, tx)
	require.NoError(t, err, "wait for L2 BridgeAsset receipt")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, receipt.Status, "L2 BridgeAsset tx failed")

	// Find the BridgeEvent log (ERC20 bridging also emits a Transfer log, so scan all logs).
	var depositCount uint32
	var foundBridgeEvent bool
	for _, lg := range receipt.Logs {
		bridgeEvent, parseErr := testEnv.L2.Contracts.L2Bridge.ParseBridgeEvent(*lg)
		if parseErr == nil {
			depositCount = bridgeEvent.DepositCount
			foundBridgeEvent = true
			break
		}
	}
	require.True(t, foundBridgeEvent, "BridgeEvent not found in BridgeAsset receipt logs")
	log.Infof("[createL2BridgeNoClaim] bridge tx mined dc=%d block=%d",
		depositCount, receipt.BlockNumber.Uint64())

	// Wait for bridge service to index it.
	err = pollWithBackoff(ctx, bflBridgeIndexWait, 2*time.Second, 10*time.Second,
		fmt.Sprintf("bridge-service-l2-dc%d", depositCount),
		func() (bool, error) {
			_, indexErr := testEnv.Clients.BridgeService.GetBridgeByDepositCount(
				ctx, testEnv.L2.NetworkID, depositCount,
			)
			return indexErr == nil, nil
		},
	)
	require.NoError(t, err, "bridge service did not index L2 bridge dc=%d", depositCount)
	log.Infof("[createL2BridgeNoClaim] bridge service indexed dc=%d", depositCount)
}

// waitForBridgeServiceSynced waits until the bridge service has re-indexed all L2 bridges up to
// the current L2 deposit count. This is needed after an aggkit restart, because the bridge
// service re-syncs from genesis and may take several minutes to process historical blocks.
func waitForBridgeServiceSynced(ctx context.Context, t *testing.T) {
	t.Helper()
	callOpts := &bind.CallOpts{Context: ctx}
	dcBig, err := testEnv.L2.Contracts.L2Bridge.DepositCount(callOpts)
	require.NoError(t, err, "get L2 deposit count for bridge-service sync check")
	if dcBig.Uint64() == 0 {
		return
	}
	lastDC := uint32(dcBig.Uint64()) - 1
	log.Infof("[waitForBridgeServiceSynced] waiting for bridge service to index dc=%d", lastDC)
	err = pollWithBackoff(ctx, 10*time.Minute, 2*time.Second, 15*time.Second,
		fmt.Sprintf("bridge-service-sync-to-dc%d", lastDC),
		func() (bool, error) {
			_, indexErr := testEnv.Clients.BridgeService.GetBridgeByDepositCount(
				ctx, testEnv.L2.NetworkID, lastDC,
			)
			return indexErr == nil, nil
		},
	)
	require.NoError(t, err, "bridge service did not sync to dc=%d", lastDC)
	log.Infof("[waitForBridgeServiceSynced] bridge service synced to dc=%d", lastDC)
}

// prepareBFLToolConfig creates a temp config file for the backward/forward LET tool by:
//  1. Patching the host-mounted aggkit config with external (host-accessible) URLs.
//  2. Appending the [BackwardForwardLET] section.
//
// aggsenderRPCURL overrides the default one from summary.json (used for testing API fallback).
func prepareBFLToolConfig(t *testing.T, aggsenderRPCURL string) *bfl.Config {
	t.Helper()
	return buildBFLToolConfig(t, aggsenderRPCURL, "")
}

// prepareBFLToolConfigWithOverride is like prepareBFLToolConfig but also sets
// CertificateExitsFile so the tool uses the JSON override file for bridge exit data.
func prepareBFLToolConfigWithOverride(t *testing.T, aggsenderRPCURL, certExitsFile string) *bfl.Config {
	t.Helper()
	return buildBFLToolConfig(t, aggsenderRPCURL, certExitsFile)
}

// buildBFLToolConfig is the shared implementation for prepareBFLToolConfig and
// prepareBFLToolConfigWithOverride. certExitsFile may be empty.
func buildBFLToolConfig(t *testing.T, aggsenderRPCURL, certExitsFile string) *bfl.Config {
	t.Helper()

	summaryPath := filepath.Join(testEnv.EnvDir, "summary.json")
	summaryData, err := os.ReadFile(summaryPath)
	require.NoError(t, err)

	var summary summaryForBFLToolConfig
	require.NoError(t, json.Unmarshal(summaryData, &summary))

	l2Network, ok := summary.Networks.L2Networks["001"]
	require.True(t, ok, "L2 network 001 not found in summary.json")

	l1URL := summary.Networks.L1.Services.Geth.HTTPRpc.External
	l2URL := l2Network.Services.OpGeth.HTTPRpc.External
	agglayerGRPCURL := summary.Networks.Agglayer.Services.GrpcRPC.External
	bridgeServiceURL := l2Network.Services.Aggkit.BridgeService.External

	sovereignAdminKeyPath := filepath.Join(testEnv.EnvDir, "config", "001", "sovereignadmin.keystore")

	// Read original config.
	originalCfgPath := testEnv.GetAggkitConfigPath()
	content, err := os.ReadFile(originalCfgPath)
	require.NoError(t, err)

	// Patch internal docker container URLs with external host-accessible URLs.
	patched := string(content)
	patched = strings.ReplaceAll(patched, "http://geth:8545", l1URL)
	patched = strings.ReplaceAll(patched, "http://op-geth-001:8545", l2URL)
	patched = strings.ReplaceAll(patched, "http://agglayer:4443", agglayerGRPCURL)

	// Optional override file line.
	certExitsFileLine := ""
	if certExitsFile != "" {
		certExitsFileLine = fmt.Sprintf("\nCertificateExitsFile = %q", certExitsFile)
	}

	// Append [AgglayerClient] and [BackwardForwardLET] sections.
	appendSection := fmt.Sprintf(`

[AgglayerClient.GRPC]
URL                = %q
MinConnectTimeout  = "5s"
RequestTimeout     = "300s"
UseTLS             = false

[BackwardForwardLET]
BridgeServiceURL = %q
AggsenderRPCURL  = %q
L2NetworkID      = %d%s

[BackwardForwardLET.GERRemoverKey]
Method   = "local"
Path     = %q
Password = %q

[BackwardForwardLET.EmergencyPauserKey]
Method   = "local"
Path     = %q
Password = %q

[BackwardForwardLET.EmergencyUnpauserKey]
Method   = "local"
Path     = %q
Password = %q
`,
		agglayerGRPCURL,
		bridgeServiceURL,
		aggsenderRPCURL,
		testEnv.L2.NetworkID,
		certExitsFileLine,
		sovereignAdminKeyPath, keystorePassword,
		sovereignAdminKeyPath, keystorePassword,
		sovereignAdminKeyPath, keystorePassword,
	)

	tmpFile := filepath.Join(t.TempDir(), "aggkit-config-bfl-test.toml")
	err = os.WriteFile(tmpFile, append([]byte(patched), []byte(appendSection)...), 0o600)
	require.NoError(t, err)

	cliCtx := buildBFLToolCLIContext(t, tmpFile)
	cfg, err := bfl.LoadConfig(cliCtx)
	require.NoError(t, err)
	return cfg
}

// callAgglayerAdminGetCertificate calls admin_getCertificate on the agglayer admin JSON-RPC
// and returns the Certificate. The cert ID is the agglayer CertificateId resolved from
// diagnosis.MissingCerts. Requires debug-mode = true in the agglayer config.
func callAgglayerAdminGetCertificate(
	t *testing.T,
	adminURL string,
	certID common.Hash,
) *agglayertypes.Certificate {
	t.Helper()
	response, err := rpc.JSONRPCCall(adminURL, "admin_getCertificate", certID)
	require.NoError(t, err, "admin_getCertificate RPC call failed for certID=%s", certID.Hex())
	require.Nil(t, response.Error, "admin_getCertificate returned error: %v", response.Error)
	// The result is [Certificate, CertificateHeader|null].
	var pair [2]json.RawMessage
	require.NoError(t, json.Unmarshal(response.Result, &pair),
		"failed to unmarshal admin_getCertificate result as [Certificate, CertificateHeader|null]")
	var cert agglayertypes.Certificate
	require.NoError(t, json.Unmarshal(pair[0], &cert),
		"failed to unmarshal Certificate from admin_getCertificate pair[0]")
	return &cert
}

// buildBFLToolCLIContext creates a *cli.Context with --cfg pointing to configPath
// so that bfl.LoadConfig can be used from tests.
func buildBFLToolCLIContext(t *testing.T, configPath string) *cli.Context {
	t.Helper()
	app := cli.NewApp()
	app.Flags = []cli.Flag{
		&cli.StringSliceFlag{Name: "cfg", Aliases: []string{"c"}},
	}
	set := flag.NewFlagSet("", flag.ContinueOnError)
	for _, f := range app.Flags {
		require.NoError(t, f.Apply(set))
	}
	require.NoError(t, set.Parse([]string{"--cfg", configPath}))
	return cli.NewContext(app, set, nil)
}
