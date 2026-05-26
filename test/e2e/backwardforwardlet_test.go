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
	aggsenderdb "github.com/agglayer/aggkit/aggsender/db"
	aggsendertypes "github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/aggsender/validator"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	aggkitdb "github.com/agglayer/aggkit/db"
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
	// bflCertSettleTimeout is used when waiting for a specific cert to settle.
	// The test environment uses a PoS beacon chain with 12s L1 blocks and 15-block epochs (~3 min/epoch).
	// A cert may take up to one full epoch to settle, so 5 minutes provides comfortable margin.
	bflCertSettleTimeout = 5 * time.Minute
	bflRestartTimeout    = 2 * time.Minute
	bflBridgeIndexWait   = 2 * time.Minute
	// bflNoPendingTimeout is used when waiting for the agglayer to have no in-flight certs.
	// Same epoch-based reasoning as bflCertSettleTimeout; using a larger margin in case
	// the cert is submitted early in an epoch and the epoch is longer than expected.
	bflNoPendingTimeout = 10 * time.Minute
	// nilStr is the display value used for nil pointer fields in debug log messages.
	nilStr = "nil"
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

// =============================================================================
// Test functions
// =============================================================================

// TestBackwardForwardLET_NoDivergence verifies that Diagnose returns NoDivergence on a healthy system.
func TestBackwardForwardLET_NoDivergence(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
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
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

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
	// sendMaliciousCertificateViaTool waits for no pending certs, builds, stops aggkit,
	// sends to agglayer, writes to DB, then restarts aggkit.
	fakeBridgeExits := []*agglayertypes.BridgeExit{makeFakeBridgeExit(0)}
	cert := sendMaliciousCertificateViaTool(ctx, t, toolEnv, fakeBridgeExits, certSignerKey)
	log.Infof("[Case1] sent malicious cert height=%d", cert.Height)

	waitForCertificateToSettle(ctx, t, toolEnv, cert.Height)

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
	recoveryCtx, recoveryCancel := context.WithTimeout(ctx, 2*time.Minute)
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
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()

	// Build bfl tool environment.
	cfg := prepareBFLToolConfig(t, testEnv.AggsenderRPCURL)
	toolEnv, err := bfl.SetupEnv(ctx, cfg)
	require.NoError(t, err)
	defer toolEnv.Close()

	certSignerKey := loadCertSignerKey(t)

	// Build and send 1 malicious cert with 1 fake bridge exit.
	fakeBridgeExits := []*agglayertypes.BridgeExit{makeFakeBridgeExit(0)}
	cert := sendMaliciousCertificateViaTool(ctx, t, toolEnv, fakeBridgeExits, certSignerKey)
	log.Infof("[Case2] sent malicious cert height=%d", cert.Height)

	waitForCertificateToSettle(ctx, t, toolEnv, cert.Height)

	// Create 2 real L2 bridge deposits. The bridge service is continuously running and
	// synced, so createL2BridgeNoClaim polls until each bridge is indexed.
	// With DivergencePoint=0 and l2CurrentDC=2, collectExtraL2Bridges(0,2)=[bridges at DC=0,1].
	createL2BridgeNoClaim(ctx, t)
	createL2BridgeNoClaim(ctx, t)

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
	recoveryCtx, recoveryCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer recoveryCancel()
	err = bfl.ExecuteRecovery(recoveryCtx, toolEnv, diagnosis)
	require.NoError(t, err)

	// Verify: DC should equal DivergencePoint + divergent leaves + extra real bridges.
	// For Case2, L2 LER will NOT match L1 settled LER because extra real L2 bridges were
	// appended after the fake leaf. The operator-documented procedure (wipe aggsender DB)
	// is what advances L1 in production; we skip that here since later tests are tolerant
	// of leaked Case2 state (Case3 accepts Case3 or Case4 classification).
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
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()

	// Build first bfl tool environment.
	cfg := prepareBFLToolConfig(t, testEnv.AggsenderRPCURL)
	toolEnv, err := bfl.SetupEnv(ctx, cfg)
	require.NoError(t, err)
	defer toolEnv.Close()

	certSignerKey := loadCertSignerKey(t)

	// Send first malicious cert.
	fake1 := []*agglayertypes.BridgeExit{makeFakeBridgeExit(0)}
	cert1 := sendMaliciousCertificateViaTool(ctx, t, toolEnv, fake1, certSignerKey)
	log.Infof("[Case3] sent malicious cert1 height=%d", cert1.Height)
	waitForCertificateToSettle(ctx, t, toolEnv, cert1.Height)

	// Re-query state to get the new settled LER for cert2.
	toolEnv.Close()
	cfg = prepareBFLToolConfig(t, testEnv.AggsenderRPCURL)
	toolEnv, err = bfl.SetupEnv(ctx, cfg)
	require.NoError(t, err)

	// Send second malicious cert.
	fake2 := []*agglayertypes.BridgeExit{makeFakeBridgeExit(1)}
	cert2 := sendMaliciousCertificateViaTool(ctx, t, toolEnv, fake2, certSignerKey)
	log.Infof("[Case3] sent malicious cert2 height=%d", cert2.Height)
	waitForCertificateToSettle(ctx, t, toolEnv, cert2.Height)

	// Re-build toolEnv for diagnosis.
	toolEnv.Close()
	cfg2 := prepareBFLToolConfig(t, testEnv.AggsenderRPCURL)
	toolEnv, err = bfl.SetupEnv(ctx, cfg2)
	require.NoError(t, err)

	// Diagnose.
	// When run after Case2 recovery, ForwardLET'd leaves from Case2 persist as extra L2 bridges,
	// so the tool may classify this as Case4 instead of Case3. Both are valid.
	diagnosis, err := bfl.Diagnose(ctx, toolEnv)
	require.NoError(t, err)
	require.True(t, diagnosis.Case == bfl.Case3 || diagnosis.Case == bfl.Case4,
		"expected Case3 or Case4, got %s", diagnosis.Case)
	require.GreaterOrEqual(t, len(diagnosis.DivergentLeaves), 2,
		"expected at least 2 divergent leaves, got %d", len(diagnosis.DivergentLeaves))

	// Execute recovery.
	recoveryCtx, recoveryCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer recoveryCancel()
	err = bfl.ExecuteRecovery(recoveryCtx, toolEnv, diagnosis)
	require.NoError(t, err)

	// Verify using computed deposit count (works for both Case3 and Case4).
	callOpts := &bind.CallOpts{Context: ctx}
	expectedDC := diagnosis.DivergencePoint + uint32(len(diagnosis.DivergentLeaves)) +
		uint32(len(diagnosis.ExtraL2Bridges))
	postDCBig, err := toolEnv.L2Bridge.DepositCount(callOpts)
	require.NoError(t, err)
	require.Equal(t, expectedDC, uint32(postDCBig.Uint64()),
		"deposit count should equal DivergencePoint+divergent+extraL2 after recovery")

	// When there are no extra L2 bridges (pure Case3), the LER should match L1 settled LER.
	if len(diagnosis.ExtraL2Bridges) == 0 {
		root, rootErr := toolEnv.L2Bridge.GetRoot(callOpts)
		require.NoError(t, rootErr)
		require.Equal(t, diagnosis.L1SettledLER, common.Hash(root),
			"L2 LER should match L1 settled LER after Case3 recovery")
	}

	inEmergency, err := toolEnv.L2Bridge.IsEmergencyState(callOpts)
	require.NoError(t, err)
	require.False(t, inEmergency, "L2 bridge should not be in emergency state after recovery")
}

// TestBackwardForwardLET_Case4 verifies Case4 (BackwardLET + ForwardLET: 2 divergent leaves + extra L2 bridges).
func TestBackwardForwardLET_Case4(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()

	// Build bfl tool environment.
	cfg := prepareBFLToolConfig(t, testEnv.AggsenderRPCURL)
	toolEnv, err := bfl.SetupEnv(ctx, cfg)
	require.NoError(t, err)
	defer toolEnv.Close()

	certSignerKey := loadCertSignerKey(t)

	// Send first malicious cert.
	fake1 := []*agglayertypes.BridgeExit{makeFakeBridgeExit(0)}
	cert1 := sendMaliciousCertificateViaTool(ctx, t, toolEnv, fake1, certSignerKey)
	log.Infof("[Case4] sent malicious cert1 height=%d", cert1.Height)
	waitForCertificateToSettle(ctx, t, toolEnv, cert1.Height)

	// Re-query state and send cert2.
	toolEnv.Close()
	cfg = prepareBFLToolConfig(t, testEnv.AggsenderRPCURL)
	toolEnv, err = bfl.SetupEnv(ctx, cfg)
	require.NoError(t, err)

	fake2 := []*agglayertypes.BridgeExit{makeFakeBridgeExit(1)}
	cert2 := sendMaliciousCertificateViaTool(ctx, t, toolEnv, fake2, certSignerKey)
	log.Infof("[Case4] sent malicious cert2 height=%d", cert2.Height)
	waitForCertificateToSettle(ctx, t, toolEnv, cert2.Height)

	// Create 2 real L2 bridge deposits. The bridge service is continuously running and
	// synced, so createL2BridgeNoClaim polls until each bridge is indexed.
	// With DivergencePoint=0 and l2CurrentDC=2, collectExtraL2Bridges(0,2)=[bridges at DC=0,1].
	createL2BridgeNoClaim(ctx, t)
	createL2BridgeNoClaim(ctx, t)

	// Re-build toolEnv for diagnosis.
	toolEnv.Close()
	cfg2 := prepareBFLToolConfig(t, testEnv.AggsenderRPCURL)
	toolEnv, err = bfl.SetupEnv(ctx, cfg2)
	require.NoError(t, err)

	// Diagnose.
	diagnosis, err := bfl.Diagnose(ctx, toolEnv)
	require.NoError(t, err)
	require.Equal(t, bfl.Case4, diagnosis.Case)
	require.GreaterOrEqual(t, len(diagnosis.DivergentLeaves), 2,
		"expected at least 2 divergent leaves, got %d", len(diagnosis.DivergentLeaves))
	require.NotEmpty(t, diagnosis.ExtraL2Bridges)

	// Execute recovery.
	recoveryCtx, recoveryCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer recoveryCancel()
	err = bfl.ExecuteRecovery(recoveryCtx, toolEnv, diagnosis)
	require.NoError(t, err)

	// Verify: DC should equal DivergencePoint + divergent leaves + extra real bridges.
	// For Case4, L2 LER will NOT match L1 settled LER because extra real L2 bridges were
	// appended after the fake leaves. The operator-documented procedure (wipe aggsender DB)
	// is what advances L1 in production; not exercised here.
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
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()

	// Phase 1: Setup — same structure as TestBackwardForwardLET_Case2.
	cfg := prepareBFLToolConfig(t, testEnv.AggsenderRPCURL)
	toolEnv, err := bfl.SetupEnv(ctx, cfg)
	require.NoError(t, err)
	defer toolEnv.Close()

	certSignerKey := loadCertSignerKey(t)

	fakeBridgeExits := []*agglayertypes.BridgeExit{makeFakeBridgeExit(0)}
	cert := sendMaliciousCertificateViaTool(ctx, t, toolEnv, fakeBridgeExits, certSignerKey)
	log.Infof("[AggsenderFallback] sent malicious cert height=%d", cert.Height)
	waitForCertificateToSettle(ctx, t, toolEnv, cert.Height)

	createL2BridgeNoClaim(ctx, t)
	createL2BridgeNoClaim(ctx, t)

	// Pre-collect cert IDs for all settled heights BEFORE wiping the aggsender DB.
	// When run after prior tests, there are multiple settled heights but only the latest
	// is auto-resolved by the diagnosis tool. Pre-collecting lets us build a complete
	// override file in Phase 4 even for unresolved heights.
	toolEnv.Close()
	cfgPre := prepareBFLToolConfig(t, testEnv.AggsenderRPCURL)
	toolEnv, err = bfl.SetupEnv(ctx, cfgPre)
	require.NoError(t, err)

	preInfo, preInfoErr := toolEnv.AgglayerClient.GetNetworkInfo(ctx, toolEnv.L2NetworkID)
	require.NoError(t, preInfoErr, "GetNetworkInfo before DB wipe")
	require.NotNil(t, preInfo.SettledHeight, "expected settled certs before DB wipe")

	// Pre-collect cert IDs by reading the aggsender DB directly (not via RPC).
	// A cert submitted just before this point may be Pending in the DB (not yet settled).
	// If it settles between now and Phase 3, it will appear in MissingCerts with
	// CertIDResolved=false — but its ID won't be in preInfo.SettledHeight yet.
	// Direct DB access captures cert IDs for ALL heights including Pending certs,
	// ensuring the override file is complete even for late-settling certs.
	preStore := openAggsenderDBForTest(t, testEnv.GetAggsenderDBPath())
	allHeaders, headersErr := preStore.GetCertificateHeadersByStatus(nil)
	require.NoError(t, headersErr, "read cert headers from aggsender DB before wipe")
	preCertIDs := make(map[uint64]common.Hash, len(allHeaders))
	for _, header := range allHeaders {
		if header != nil && header.CertificateID != (common.Hash{}) {
			preCertIDs[header.Height] = header.CertificateID
		}
	}
	t.Logf("[AggsenderFallback] pre-collected %d cert IDs from aggsender DB (settled heights 0..%d)",
		len(preCertIDs), *preInfo.SettledHeight)

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
		} else {
			// Wait for bridge service to re-sync after config restore. This ensures
			// l1infotreesync has processed any pending reorgs before the post-test
			// bridge health check runs.
			waitForBridgeServiceSynced(cleanupCtx, t)
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
	// Some cert IDs may not be auto-resolved (only the latest settled height is guaranteed).
	// For unresolved IDs, use the pre-collected cert IDs from before the DB wipe.
	for _, mc := range diagnosis.MissingCerts {
		certID := mc.CertID
		if !mc.CertIDResolved {
			preID, ok := preCertIDs[mc.Height]
			if !ok {
				require.Failf(t, "no pre-collected cert ID",
					"[AggsenderFallback] no cert ID for height %d: not auto-resolved and not in pre-collected DB snapshot",
					mc.Height)
			}
			certID = preID
		}
		adminCert := callAgglayerAdminGetCertificate(t, adminURL, certID)
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
	// When run after other tests, accumulated state may produce Case4 instead of Case2.
	require.True(t, diagnosis2.Case == bfl.Case2 || diagnosis2.Case == bfl.Case4,
		"expected Case2 or Case4 diagnosis with override file, got %s", diagnosis2.Case)
	require.GreaterOrEqual(t, len(diagnosis2.DivergentLeaves), 1,
		"expected at least 1 divergent leaf (the fake bridge exit)")
	require.NotEmpty(t, diagnosis2.ExtraL2Bridges,
		"expected extra L2 bridges")

	// Phase 7: Recovery.
	recoveryCtx, recoveryCancel := context.WithTimeout(ctx, 2*time.Minute)
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

// sendMaliciousCertificateViaTool builds and sends a malicious certificate to the agglayer,
// and records it in the aggsender DB. This replaces the old DebugSendCertificate RPC endpoint.
//
// The function:
//  1. Stops aggkit so the aggsender cannot submit new certs that would race with our injection.
//  2. Waits for the agglayer to have no in-flight certs (possible now that aggkit is stopped).
//  3. Opens the aggsender DB directly and builds the cert using agglayer settled state.
//  4. Sends the cert to the agglayer and writes it to the aggsender DB.
//  5. Restarts aggkit so it picks up the malicious cert from the DB.
//
// Returns the actual certificate that was sent.
func sendMaliciousCertificateViaTool(
	ctx context.Context, t *testing.T,
	toolEnv *bfl.Env,
	fakeBridgeExits []*agglayertypes.BridgeExit,
	certSignerKey *ecdsa.PrivateKey,
) *agglayertypes.Certificate {
	t.Helper()

	// Stop aggkit first so the aggsender cannot submit new certs while we prepare
	// the injection. With aggkit stopped, any in-flight cert will settle and then
	// LatestPendingHeight will become nil (no new submissions possible).
	require.NoError(t, testEnv.StopAggkit(ctx), "stop aggkit before build+inject")

	// Wait for the agglayer to have no in-flight certs. Now that aggkit is stopped,
	// LatestPendingHeight will become nil once the last in-flight cert settles.
	waitForAgglayerNoPendingCerts(ctx, t, toolEnv)

	// Open the aggsender DB directly (aggkit RPC is unavailable since it is stopped).
	dbPath := testEnv.GetAggsenderDBPath()
	certStore := openAggsenderDBForTest(t, dbPath)

	// The aggkit container stores cert files at /tmp/certificates/ (container path), which is
	// bind-mounted to aggkitDataDir on the host. Inline those file references so that
	// GetCertificateBridgeExits can read cert content without needing the container filesystem.
	inlineContainerCertPaths(t, dbPath)

	// Build the cert using the agglayer settled state and the aggsender DB.
	cert := buildMaliciousCert(ctx, t, toolEnv, certStore, fakeBridgeExits, certSignerKey)

	certJSON, err := json.Marshal(cert)
	require.NoError(t, err, "marshal cert to JSON")
	certFile := filepath.Join(t.TempDir(), "cert.json")
	require.NoError(t, os.WriteFile(certFile, certJSON, 0o600))

	cfgPath := prepareAgglayerOnlyConfigPath(t)
	cliCtx := buildSendCertCLIContext(ctx, t, cfgPath, certFile, dbPath)
	require.NoError(t, bfl.RunSendCert(cliCtx), "send-cert tool failed for cert height=%d", cert.Height)
	log.Infof("[sendMaliciousCertificateViaTool] sent cert height=%d", cert.Height)

	// RunSendCert stores the SignedCertificate as a file reference ("@<host-path>").
	// The container aggkit resolves file references using container-side paths, so it
	// cannot find the HOST-path file. Patch the DB to store the cert JSON inline so
	// the aggsender inside the container can read bridge exits via GetCertificateBridgeExits.
	patchCertToInlineJSON(t, dbPath, cert.Height, certJSON)

	// Restart aggkit so it picks up the malicious cert from the DB.
	require.NoError(t, testEnv.StartAggkit(ctx), "restart aggkit after DB write")

	// Wait for the bridge service to re-sync after the restart. This ensures
	// l1infotreesync has had time to process any zero-hash blocks that were
	// created when aggkit was stopped, preventing GER injection index skips.
	waitForBridgeServiceSynced(ctx, t)
	return cert
}

// waitForAgglayerNoPendingCerts polls until the agglayer has no actively-processing
// certificate for our L2 network. The condition is met when:
//   - LatestPendingHeight is nil (no certs ever submitted), OR
//   - LatestPendingStatus is Settled or InError (last cert is in a terminal state)
//
// Note: LatestPendingHeight never becomes nil once any cert has been submitted (it
// tracks the last submitted height). We use LatestPendingStatus instead to detect when
// the last cert has finished processing.
// Used after stopping aggkit to wait for any in-flight cert to finish before injecting.
func waitForAgglayerNoPendingCerts(ctx context.Context, t *testing.T, toolEnv *bfl.Env) {
	t.Helper()
	log.Infof("[waitForAgglayerNoPendingCerts] waiting for agglayer to finish processing in-flight certs")
	err := pollWithBackoff(ctx, bflNoPendingTimeout, backoffInitial, backoffMax, "no-pending-certs",
		func() (bool, error) {
			info, pollErr := toolEnv.AgglayerClient.GetNetworkInfo(ctx, toolEnv.L2NetworkID)
			if pollErr != nil {
				// UNKNOWN_NETWORK_TYPE means this L2 network has never submitted a cert to the
				// agglayer, so there are definitely no in-flight certs. Proceed immediately.
				if strings.Contains(pollErr.Error(), "UNKNOWN_NETWORK_TYPE") {
					log.Debugf("[waitForAgglayerNoPendingCerts] network unknown (no certs ever submitted), proceeding")
					return true, nil
				}
				log.Debugf("[waitForAgglayerNoPendingCerts] GetNetworkInfo error (retrying): %v", pollErr)
				return false, nil // non-fatal, keep polling
			}
			pendingH := nilStr
			settledH := nilStr
			pendingS := nilStr
			if info.LatestPendingHeight != nil {
				pendingH = fmt.Sprintf("%d", *info.LatestPendingHeight)
			}
			if info.SettledHeight != nil {
				settledH = fmt.Sprintf("%d", *info.SettledHeight)
			}
			if info.LatestPendingStatus != nil {
				pendingS = info.LatestPendingStatus.String()
			}
			log.Debugf("[waitForAgglayerNoPendingCerts] pendingH=%s settledH=%s pendingStatus=%s", pendingH, settledH, pendingS)
			// No cert has ever been submitted.
			if info.LatestPendingHeight == nil {
				return true, nil
			}
			// Last submitted cert has settled: SettledHeight >= LatestPendingHeight.
			if info.SettledHeight != nil && *info.SettledHeight >= *info.LatestPendingHeight {
				return true, nil
			}
			// Last cert went to InError (terminal failure state): no further processing.
			if info.LatestPendingStatus != nil && *info.LatestPendingStatus == agglayertypes.InError {
				return true, nil
			}
			return false, nil
		})
	require.NoError(t, err, "timeout waiting for agglayer to finish processing in-flight certs")
	log.Infof("[waitForAgglayerNoPendingCerts] agglayer has no in-flight certs")
}

// prepareAgglayerOnlyConfigPath writes a minimal TOML config containing only the
// [AgglayerClient] section — sufficient for RunSendCert to connect to the agglayer.
func prepareAgglayerOnlyConfigPath(t *testing.T) string {
	t.Helper()
	summaryPath := filepath.Join(testEnv.EnvDir, "summary.json")
	summaryData, err := os.ReadFile(summaryPath)
	require.NoError(t, err)
	var summary summaryForBFLToolConfig
	require.NoError(t, json.Unmarshal(summaryData, &summary))
	agglayerGRPCURL := summary.Networks.Agglayer.Services.GrpcRPC.External

	content := fmt.Sprintf(`
[AgglayerClient.GRPC]
URL                = %q
MinConnectTimeout  = "5s"
RequestTimeout     = "300s"
UseTLS             = false
`, agglayerGRPCURL)

	tmpFile := filepath.Join(t.TempDir(), "agglayer-client.toml")
	require.NoError(t, os.WriteFile(tmpFile, []byte(content), 0o600))
	return tmpFile
}

// buildSendCertCLIContext constructs a *cli.Context for the send-cert subcommand with
// --cfg, --cert-file, and --db-path flags set.
func buildSendCertCLIContext(ctx context.Context, t *testing.T, configPath, certFilePath, dbPath string) *cli.Context {
	t.Helper()
	app := cli.NewApp()
	flags := []cli.Flag{
		&cli.StringSliceFlag{Name: "cfg"},
		&cli.StringFlag{Name: "cert-file"},
		&cli.StringFlag{Name: "db-path"},
	}
	set := flag.NewFlagSet("", flag.ContinueOnError)
	for _, f := range flags {
		require.NoError(t, f.Apply(set))
	}
	require.NoError(t, set.Parse([]string{"--cfg", configPath, "--cert-file", certFilePath, "--db-path", dbPath}))
	cliCtx := cli.NewContext(app, set, nil)
	cliCtx.Context = ctx
	return cliCtx
}

// waitForCertificateToSettle polls the AggLayer until the certificate at expectedHeight is settled.
// Uses GetNetworkInfo.SettledHeight (same source as Diagnose) so the condition is consistent:
// once this returns, a subsequent Diagnose call will also see the settled state.
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
			info, pollErr := toolEnv.AgglayerClient.GetNetworkInfo(ctx, toolEnv.L2NetworkID)
			if pollErr != nil {
				// Non-fatal: UNKNOWN_NETWORK_TYPE or transient error — no settled certs yet.
				log.Debugf("[waitForCertificateToSettle] GetNetworkInfo error (retrying): %v", pollErr)
				return false, nil
			}
			settledH := nilStr
			if info.SettledHeight != nil {
				settledH = fmt.Sprintf("%d", *info.SettledHeight)
			}
			pendingH := nilStr
			if info.LatestPendingHeight != nil {
				pendingH = fmt.Sprintf("%d", *info.LatestPendingHeight)
			}
			pendingS := nilStr
			if info.LatestPendingStatus != nil {
				pendingS = info.LatestPendingStatus.String()
			}
			settledLER := nilStr
			if info.SettledLER != nil {
				settledLER = info.SettledLER.Hex()
			}
			settledDC := nilStr
			if info.SettledLETLeafCount != nil {
				settledDC = fmt.Sprintf("%d", *info.SettledLETLeafCount)
			}
			log.Debugf("[waitForCertificateToSettle] settledH=%s settledLER=%s settledDC=%s pendingH=%s pendingStatus=%s",
				settledH, settledLER, settledDC, pendingH, pendingS)
			if info.SettledHeight == nil {
				return false, nil
			}
			done := *info.SettledHeight >= expectedHeight
			if done {
				log.Debugf("[waitForCertificateToSettle] settled height=%d >= expected=%d",
					*info.SettledHeight, expectedHeight)
			}
			return done, nil
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

// patchCertToInlineJSON updates the signed_certificate column for height to store
// the raw cert JSON inline instead of a "@<host-path>" file reference. This is
// necessary because send_cert.go writes certs to HOST filesystem paths, which the
// container aggsender cannot resolve when trying to read bridge exits.
func patchCertToInlineJSON(t *testing.T, dbPath string, height uint64, certJSON []byte) {
	t.Helper()
	sqlDB, err := aggkitdb.NewSQLiteDB(dbPath)
	require.NoError(t, err, "open aggsender DB for inline patch at %s", dbPath)
	defer sqlDB.Close()
	_, err = sqlDB.Exec(
		"UPDATE certificate_info SET signed_certificate = ? WHERE height = ?",
		string(certJSON), height,
	)
	require.NoError(t, err, "patch signed_certificate to inline JSON at height=%d", height)
	log.Debugf("[patchCertToInlineJSON] patched cert height=%d to inline JSON (%d bytes)", height, len(certJSON))
}

// inlineContainerCertPaths replaces "@/tmp/..." file references in the certificate_info table
// with inline JSON content. The aggkit container writes cert files to /tmp/certificates (container
// path), which is bind-mounted to aggkitDataDir on the host (= filepath.Dir(dbPath)). Without this
// translation, host-side reads via GetCertificateBridgeExits fail because the container path
// /tmp/certificates/... does not exist on the host.
// Must be called while aggkit is stopped to avoid concurrent DB writes.
func inlineContainerCertPaths(t *testing.T, dbPath string) {
	t.Helper()
	aggkitDataDir := filepath.Dir(dbPath) // dbPath = aggkitDataDir/aggsender.sqlite
	sqlDB, err := aggkitdb.NewSQLiteDB(dbPath)
	require.NoError(t, err, "open aggsender DB for cert path inlining at %s", dbPath)
	defer sqlDB.Close()

	rows, err := sqlDB.Query(
		"SELECT height, signed_certificate FROM certificate_info WHERE signed_certificate LIKE '@/%'")
	require.NoError(t, err, "query certificate_info for file-ref certs")

	type entry struct {
		height        int64
		containerPath string
	}
	var entries []entry
	for rows.Next() {
		var h int64
		var sc string
		require.NoError(t, rows.Scan(&h, &sc))
		entries = append(entries, entry{h, strings.TrimPrefix(sc, "@")})
	}
	require.NoError(t, rows.Err())
	rows.Close()

	const containerTmpPrefix = "/tmp/"
	for _, e := range entries {
		if !strings.HasPrefix(e.containerPath, containerTmpPrefix) {
			continue
		}
		relPath := e.containerPath[len(containerTmpPrefix):]
		hostPath := filepath.Join(aggkitDataDir, relPath)
		content, readErr := os.ReadFile(hostPath)
		require.NoError(t, readErr, "read cert file for height=%d at %s", e.height, hostPath)
		_, execErr := sqlDB.Exec(
			"UPDATE certificate_info SET signed_certificate = ? WHERE height = ?",
			string(content), e.height,
		)
		require.NoError(t, execErr, "inline cert content at height=%d", e.height)
		log.Debugf("[inlineContainerCertPaths] inlined cert height=%d (%d bytes)", e.height, len(content))
	}
}

// openAggsenderDBForTest opens the aggsender SQLite DB at dbPath for direct queries.
// Used when aggkit is stopped and the RPC server is unavailable.
func openAggsenderDBForTest(t *testing.T, dbPath string) aggsenderdb.AggSenderStorage {
	t.Helper()
	storage, err := aggsenderdb.NewAggSenderSQLStorage(log.GetDefaultLogger(), aggsenderdb.AggSenderSQLStorageConfig{
		DBPath:          dbPath,
		CertificatesDir: filepath.Join(filepath.Dir(dbPath), "certificates"),
	})
	require.NoError(t, err, "open aggsender DB at %s", dbPath)
	return storage
}

// buildMaliciousCert builds a Certificate with the given fake bridge exits, rooted at the current
// settled LET state (or height=0 if no settled cert exists yet), and signs it with certSignerKey.
// certStore is used to query bridge exits and cert headers directly from the aggsender DB
// (without needing the aggsender RPC server to be running).
// The certificate is not sent; call sendMaliciousCertificateViaTool to submit it.
func buildMaliciousCert(
	ctx context.Context, t *testing.T,
	toolEnv *bfl.Env,
	certStore aggsenderdb.AggSenderStorage,
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
		if header, certErr := certStore.GetCertificateHeaderByHeight(*info.SettledHeight); certErr == nil &&
			header != nil && header.L1InfoTreeLeafCount > 0 {
			l1InfoTreeLeafCount = header.L1InfoTreeLeafCount
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
			cert, certErr := certStore.GetCertificateByHeight(h)
			require.NoError(t, certErr, "GetCertificateByHeight height=%d", h)
			if cert == nil || cert.SignedCertificate == nil {
				continue
			}
			if cert.Header != nil && cert.Header.CertSource == aggsendertypes.CertificateSourceAggLayer {
				continue
			}
			var agglayerCert agglayertypes.Certificate
			require.NoError(t, json.Unmarshal([]byte(*cert.SignedCertificate), &agglayerCert),
				"unmarshal cert at height=%d", h)
			for _, be := range agglayerCert.BridgeExits {
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
	err = pollWithBackoff(ctx, 1*time.Minute, 2*time.Second, 15*time.Second,
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
