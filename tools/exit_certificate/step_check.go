package exit_certificate

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/aggchainbase"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/polygonrollupmanagerpessimistic"
	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

// aggchainTypePP is the [2]byte identifier for Pessimistic Proof mode in the aggchainbase contract.
var aggchainTypePP = [2]byte{0, 0}

// RunStepCheck verifies prerequisites before running the pipeline:
//  1. Anvil is installed ($PATH).
//  2. L1 RPC is set and reachable.
//  3. l1BridgeAddress is the L1 bridge: networkID() on it must return 0 (the L1/mainnet network).
//  4. L2 network ID matches the bridge contract.
//  5. sovereignRollupAddr is set.
//  6. Network type is PP (FEP is not supported).
//  7. Multisig threshold is 1, and l1BridgeAddress matches both aggchainbase.bridgeAddress() and
//     the canonical rollupManager.bridgeAddress().
//  8. No custom gas token is configured on the L2 bridge.
//  9. No unsettled L2 bridge exits at the target block (AET-11): the L2 bridge's local exit root
//     at the resolved target block must equal the agglayer's last settled LER, otherwise the
//     certificate cannot be generated from that snapshot and Step H would abort after the
//     expensive scan/replay phases.
//
// All checks run regardless of individual failures. Returns a combined error listing every
// failed check.
func RunStepCheck(ctx context.Context, cfg *Config) (*StepCheckResult, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP CHECK — Verify prerequisites")
	log.Info("═══════════════════════════════════════════")

	result := &StepCheckResult{}
	var failures []string

	// --- 1. Anvil ---
	if _, err := exec.LookPath("anvil"); err != nil {
		result.AnvilInstalled = false
		log.Info("❌ anvil not found in $PATH — install the Foundry toolchain from https://getfoundry.sh")
		failures = append(failures, "anvil not found in $PATH (install from https://getfoundry.sh)")
	} else {
		result.AnvilInstalled = true
		log.Info("✅ anvil is installed")
	}

	// --- 2. L1 RPC reachable ---
	var l1Client *ethclient.Client
	if cfg.L1RPCURL == "" {
		log.Info("❌ l1RpcUrl is not set")
		failures = append(failures, "l1RpcUrl is required")
	} else {
		var err error
		l1Client, err = ethclient.DialContext(ctx, cfg.L1RPCURL)
		if err != nil {
			msg := fmt.Sprintf("l1RpcUrl unreachable (%s): %v", cfg.L1RPCURL, err)
			log.Infof("❌ %s", msg)
			failures = append(failures, msg)
		} else {
			if _, err := l1Client.BlockNumber(ctx); err != nil {
				l1Client.Close()
				l1Client = nil
				msg := fmt.Sprintf("l1RpcUrl not responding (%s): %v", cfg.L1RPCURL, err)
				log.Infof("❌ %s", msg)
				failures = append(failures, msg)
			} else {
				log.Infof("✅ l1RpcUrl is reachable (%s)", cfg.L1RPCURL)
			}
		}
	}
	if l1Client != nil {
		defer l1Client.Close()
	}

	// --- 3. l1BridgeAddress is the L1 bridge ---
	if l1Client != nil {
		checkL1BridgeNetworkID(ctx, cfg, l1Client, result, &failures)
	} else {
		result.L1BridgeAddressStatus = uncheckedStatus
		msg := "l1BridgeAddress could not be verified (l1RpcUrl unavailable)"
		log.Infof("❌ %s", msg)
		failures = append(failures, msg)
	}

	// --- 4. L2 network ID matches bridge contract ---
	checkL2NetworkID(ctx, cfg, result, &failures)

	// --- 5. sovereignRollupAddr is set ---
	zeroAddr := [20]byte{}
	if cfg.SovereignRollupAddr == zeroAddr {
		log.Info("❌ sovereignRollupAddr is not set — required to verify network type and threshold")
		msg := "sovereignRollupAddr is required (set it in the config to verify the network is PP with threshold=1)"
		failures = append(failures, msg)
		result.NetworkType = uncheckedStatus
	} else if l1Client != nil {
		// --- 6 & 7. Network type + threshold + bridge addresses ---
		checkContractPrereqs(ctx, cfg, l1Client, result, &failures)
	} else {
		// L1 client failed — contract checks cannot run
		result.NetworkType = uncheckedStatus
		log.Info("❌ network type and threshold checks skipped — l1RpcUrl is not available")
		failures = append(failures, "network type and threshold could not be verified (l1RpcUrl unavailable)")
	}
	checkNativeGasToken(ctx, cfg, &failures)

	// --- 9. No unsettled L2 bridge exits at the target block (AET-11) ---
	checkUnsettledBridgeExits(ctx, cfg, result, &failures)

	log.Info("───────────────────────────────────────────")
	if len(failures) == 0 {
		log.Info("✅ All checks passed")
		log.Info("STEP CHECK complete")
		return result, nil
	}

	log.Infof("❌ %d check(s) failed", len(failures))
	log.Info("STEP CHECK failed")
	return result, fmt.Errorf("prerequisite checks failed:\n  - %s", strings.Join(failures, "\n  - "))
}

// checkL1BridgeNetworkID verifies cfg.L1BridgeAddress really is the L1 bridge by calling
// networkID() on it over the L1 RPC and requiring 0 (the L1/mainnet network in agglayer).
// Step E trusts this address to detect unclaimed L1→L2 deposits via eth_getLogs, which silently
// returns no logs on a wrong address, so a typo, a non-bridge contract, or an L2-only bridge
// address (the default when l1BridgeAddress is unset) must be caught here.
// The outcome is recorded in result.L1BridgeAddressStatus.
func checkL1BridgeNetworkID(
	ctx context.Context, cfg *Config, l1Client *ethclient.Client,
	result *StepCheckResult, failures *[]string,
) {
	caller, err := agglayerbridgel2.NewAgglayerbridgel2Caller(cfg.L1BridgeAddress, l1Client)
	if err != nil {
		result.L1BridgeAddressStatus = errorStatus
		msg := fmt.Sprintf("create L1 bridge caller (addr=%s): %v", cfg.L1BridgeAddress.Hex(), err)
		log.Infof("❌ %s", msg)
		*failures = append(*failures, msg)
		return
	}

	onChainID, err := caller.NetworkID(&bind.CallOpts{Context: ctx})
	if err != nil {
		result.L1BridgeAddressStatus = errorStatus
		msg := fmt.Sprintf("query networkID() on l1BridgeAddress %s: %v — the address is probably not "+
			"the L1 bridge contract (Step E would silently miss every unclaimed L1→L2 deposit)",
			cfg.L1BridgeAddress.Hex(), err)
		log.Infof("❌ %s", msg)
		*failures = append(*failures, msg)
		return
	}
	if onChainID != 0 {
		result.L1BridgeAddressStatus = fmt.Sprintf("invalid (networkID()=%d)", onChainID)
		msg := fmt.Sprintf("l1BridgeAddress %s is not the L1 bridge: networkID()=%d, expected 0 "+
			"(the L1/mainnet network)", cfg.L1BridgeAddress.Hex(), onChainID)
		log.Infof("❌ %s", msg)
		*failures = append(*failures, msg)
		return
	}
	result.L1BridgeAddressStatus = okStatus
	log.Infof("✅ l1BridgeAddress is the L1 bridge (networkID()=0, %s)", cfg.L1BridgeAddress.Hex())
}

// checkL2NetworkID dials the L2 RPC, calls NetworkID() on the bridge contract, and verifies
// it matches cfg.L2NetworkID.
func checkL2NetworkID(ctx context.Context, cfg *Config, result *StepCheckResult, failures *[]string) {
	l2Client, err := ethclient.DialContext(ctx, cfg.L2RPCURL)
	if err != nil {
		msg := fmt.Sprintf("dial L2 RPC (%s): %v", cfg.L2RPCURL, err)
		log.Infof("❌ %s", msg)
		*failures = append(*failures, msg)
		return
	}
	defer l2Client.Close()

	caller, err := agglayerbridgel2.NewAgglayerbridgel2Caller(cfg.L2BridgeAddress, l2Client)
	if err != nil {
		msg := fmt.Sprintf("create bridge caller (addr=%s): %v", cfg.L2BridgeAddress.Hex(), err)
		log.Infof("❌ %s", msg)
		*failures = append(*failures, msg)
		return
	}

	onChainID, err := caller.NetworkID(&bind.CallOpts{Context: ctx})
	if err != nil {
		msg := fmt.Sprintf("query bridge NetworkID(): %v", err)
		log.Infof("❌ %s", msg)
		*failures = append(*failures, msg)
		return
	}

	result.BridgeNetworkID = onChainID
	if onChainID == cfg.L2NetworkID {
		log.Infof("✅ l2NetworkId matches bridge contract (%d)", cfg.L2NetworkID)
	} else {
		msg := fmt.Sprintf("l2NetworkId mismatch: config=%d, bridge contract=%d", cfg.L2NetworkID, onChainID)
		log.Infof("❌ %s", msg)
		*failures = append(*failures, msg)
	}
}

func checkNativeGasToken(ctx context.Context, cfg *Config, failures *[]string) {
	gasTokenNetwork, gasTokenAddr, err := fetchGasTokenInfo(ctx, cfg.L2RPCURL, cfg.L2BridgeAddress)
	if err != nil {
		msg := fmt.Sprintf("fetch bridge gas token info: %v", err)
		log.Infof("❌ %s", msg)
		*failures = append(*failures, msg)
		return
	}
	if gasTokenAddr != (common.Address{}) {
		msg := fmt.Sprintf("Bridge gas token not supported: network=%d, address=%s", gasTokenNetwork, gasTokenAddr.Hex())
		log.Infof("❌ %s", msg)
		*failures = append(*failures, msg)
	} else {
		log.Infof("✅ No bridge gas token: network=%d, address=%s", gasTokenNetwork, gasTokenAddr.Hex())
	}
}

// lerReaderFn reads the bridge contract's local exit root (getRoot()) at blockTag. It matches
// readLocalExitRoot's signature so tests can inject a stub in place of the real RPC call.
type lerReaderFn func(
	ctx context.Context, rpcURL string, bridgeAddr common.Address, blockTag string,
) (common.Hash, error)

// errUnsettledBridgeExits marks the AET-11 mismatch (as opposed to a query failure) so callers
// can distinguish "the target block really has unsettled exits" from "the check could not run".
var errUnsettledBridgeExits = errors.New("unsettled L2 bridge exits")

// verifyNoUnsettledBridgeExits is the AET-11 core shared by Step CHECK and Step 0: it applies the
// same pending-certificate guard and settled-LER derivation as Step H (fetchSettledNetworkState),
// reads the L2 bridge's LER at targetBlock, and compares the two — whatever Step H would reject at
// the end of the pipeline, this rejects up front. A mismatch wraps errUnsettledBridgeExits; both
// roots are returned whenever they were fetched so callers can record them.
func verifyNoUnsettledBridgeExits(
	ctx context.Context, cfg *Config, client agglayer.AgglayerClientInterface,
	readLER lerReaderFn, targetBlock uint64,
) (settledLER, l2LER common.Hash, err error) {
	settledLER, _, err = fetchSettledNetworkState(ctx, cfg, client)
	if err != nil {
		return common.Hash{}, common.Hash{}, err
	}

	l2LER, err = readLER(ctx, cfg.L2RPCURL, cfg.L2BridgeAddress, toBlockTag(targetBlock))
	if err != nil {
		return common.Hash{}, common.Hash{},
			fmt.Errorf("read L2 bridge local exit root at target block %d: %w", targetBlock, err)
	}

	if l2LER != settledLER {
		return settledLER, l2LER, fmt.Errorf(
			"target block %d has %w: L2 bridge LER %s != agglayer settled LER %s — "+
				"every L2→L1 bridge exit up to the target block must be settled by the agglayer before the "+
				"certificate can be generated; wait until the agglayer settles them (keep the aggsender running "+
				"after the sequencer halt until the last certificate settles) and re-run, or choose a target "+
				"block at or below the settled state",
			targetBlock, errUnsettledBridgeExits, l2LER.Hex(), settledLER.Hex(),
		)
	}
	return settledLER, l2LER, nil
}

// assertNoUnsettledBridgeExits is the hard-failing variant Step 0 runs right after resolving the
// target block: unlike Step CHECK (which aggregates failures) it aborts the step, and it always
// validates the exact block the rest of the pipeline uses. When agglayerClient.grpc.url is not
// configured the check is skipped with a warning — Step CHECK already reports the missing URL as
// a failure, and Step H will require it anyway. With options.ignoreLERMismatch the abort
// is downgraded to a warning.
func assertNoUnsettledBridgeExits(ctx context.Context, cfg *Config, targetBlock uint64) error {
	agglayerClientCfg := cfg.Options.AgglayerClient
	if agglayerClientCfg.GRPC == nil || agglayerClientCfg.GRPC.URL == "" {
		log.Warn("unsettled-bridge-exits check skipped: agglayerClient.grpc.url is not configured " +
			"(step CHECK reports this as a failure; step H requires it)")
		return nil
	}

	client, err := agglayer.NewAgglayerClient(agglayerClientCfg, log.GetDefaultLogger())
	if err != nil {
		return suppressUnsettledExitsError(cfg, fmt.Errorf("create agglayer client: %w", err))
	}

	return assertNoUnsettledBridgeExitsWith(ctx, cfg, client, readLocalExitRoot, targetBlock)
}

// assertNoUnsettledBridgeExitsWith is the injectable core of assertNoUnsettledBridgeExits (tests
// pass an agglayer client mock and a stub LER reader).
func assertNoUnsettledBridgeExitsWith(
	ctx context.Context, cfg *Config, client agglayer.AgglayerClientInterface,
	readLER lerReaderFn, targetBlock uint64,
) error {
	settledLER, _, err := verifyNoUnsettledBridgeExits(ctx, cfg, client, readLER, targetBlock)
	if err != nil {
		return suppressUnsettledExitsError(cfg, err)
	}
	log.Infof("✅ L2 bridge LER at target block %d matches the agglayer settled LER (%s)",
		targetBlock, settledLER.Hex())
	return nil
}

// suppressUnsettledExitsError downgrades an AET-11 check error to a warning when
// options.ignoreLERMismatch is set; otherwise it returns the error unchanged.
func suppressUnsettledExitsError(cfg *Config, err error) error {
	if cfg.Options.IgnoreLERMismatch {
		log.Warnf("⚠️  unsettled-bridge-exits check failed but ignoreLERMismatch=true — "+
			"continuing (the agglayer will most likely reject the certificate): %v", err)
		return nil
	}
	return err
}

// reportUnsettledExitsFailure appends msg to the Step CHECK failure list, or only warns when
// options.ignoreLERMismatch is set (the check outcome is still recorded in the result).
func reportUnsettledExitsFailure(cfg *Config, failures *[]string, msg string) {
	if cfg.Options.IgnoreLERMismatch {
		log.Warnf("⚠️  %s (ignored: ignoreLERMismatch=true)", msg)
		return
	}
	log.Infof("❌ %s", msg)
	*failures = append(*failures, msg)
}

// checkTargetBlock returns the block the unsettled-exits check runs at: the block Step 0 already
// resolved (step-0-l2_target_block.json) when available — so the check validates the exact block
// the rest of the pipeline uses — otherwise cfg.TargetBlock resolved on the spot (in the full
// pipeline Step CHECK runs before Step 0; with the sequencer halted — an operational
// precondition — the resolution is stable, and Step 0 re-validates its own resolved block anyway).
func checkTargetBlock(ctx context.Context, cfg *Config) (uint64, string, error) {
	if n, err := loadTargetBlock(cfg.Options.OutputDir); err == nil {
		return n, "step 0 output (" + fileStep0TargetBlock + ")", nil
	}
	n, err := resolveTargetBlockNumber(ctx, cfg.L2RPCURL, cfg.TargetBlock)
	return n, "config targetBlock", err
}

// checkUnsettledBridgeExits is the AET-11 preflight: a permissionless L2→L1 bridge exit made
// before the sequencer halt but never settled by the agglayer advances the L2 local exit root
// past the settled state; the certificate cannot include that exit, so Step H would abort — but
// only after the expensive scan (Steps A/B) and replay (Step G) phases already ran. Checking it
// here gives the operator the actionable error before any expensive work. Step 0 re-runs the
// same verification on its own resolved block (assertNoUnsettledBridgeExits).
//
// It requires options.agglayerClient.grpc.url — the same requirement Step H enforces later, so
// this only moves the failure earlier. The outcome is recorded in result.UnsettledExitsStatus.
func checkUnsettledBridgeExits(ctx context.Context, cfg *Config, result *StepCheckResult, failures *[]string) {
	agglayerClientCfg := cfg.Options.AgglayerClient
	if agglayerClientCfg.GRPC == nil || agglayerClientCfg.GRPC.URL == "" {
		result.UnsettledExitsStatus = uncheckedStatus
		reportUnsettledExitsFailure(cfg, failures,
			"agglayerClient.grpc.url is required (needed to verify no unsettled L2 bridge exits, "+
				"and later by step H)")
		return
	}

	client, err := agglayer.NewAgglayerClient(agglayerClientCfg, log.GetDefaultLogger())
	if err != nil {
		result.UnsettledExitsStatus = errorStatus
		reportUnsettledExitsFailure(cfg, failures, fmt.Sprintf("create agglayer client: %v", err))
		return
	}

	checkUnsettledBridgeExitsWith(ctx, cfg, client, readLocalExitRoot, result, failures)
}

// checkUnsettledBridgeExitsWith is the injectable core of checkUnsettledBridgeExits (tests pass
// an agglayer client mock and a stub LER reader). It picks the target block (checkTargetBlock),
// runs verifyNoUnsettledBridgeExits, and maps the outcome onto the Step CHECK result/failures.
func checkUnsettledBridgeExitsWith(
	ctx context.Context, cfg *Config, client agglayer.AgglayerClientInterface,
	readLER lerReaderFn, result *StepCheckResult, failures *[]string,
) {
	targetBlock, source, err := checkTargetBlock(ctx, cfg)
	if err != nil {
		result.UnsettledExitsStatus = errorStatus
		reportUnsettledExitsFailure(cfg, failures,
			fmt.Sprintf("resolve target block for the unsettled-exits check: %v", err))
		return
	}
	log.Infof("   unsettled-exits check target block: %d (from %s)", targetBlock, source)

	settledLER, l2LER, err := verifyNoUnsettledBridgeExits(ctx, cfg, client, readLER, targetBlock)
	if err != nil {
		if errors.Is(err, errUnsettledBridgeExits) {
			result.UnsettledExitsStatus = fmt.Sprintf("unsettled exits at block %d", targetBlock)
			result.SettledLER = settledLER.Hex()
			result.L2BridgeLER = l2LER.Hex()
		} else {
			result.UnsettledExitsStatus = errorStatus
		}
		reportUnsettledExitsFailure(cfg, failures, err.Error())
		return
	}

	result.SettledLER = settledLER.Hex()
	result.L2BridgeLER = l2LER.Hex()
	result.UnsettledExitsStatus = okStatus
	log.Infof("✅ L2 bridge LER at target block %d matches the agglayer settled LER (%s)",
		targetBlock, settledLER.Hex())
}

// checkContractPrereqs queries the aggchainbase contract for network type and threshold.
// l1Client is already dialed and verified reachable by the caller.
func checkContractPrereqs(
	ctx context.Context, cfg *Config, l1Client *ethclient.Client, result *StepCheckResult, failures *[]string,
) {
	caller, err := aggchainbase.NewAggchainbaseCaller(cfg.SovereignRollupAddr, l1Client)
	if err != nil {
		msg := fmt.Sprintf("create aggchainbase caller (addr=%s): %v", cfg.SovereignRollupAddr.Hex(), err)
		log.Infof("❌ %s", msg)
		*failures = append(*failures, msg)
		result.NetworkType = uncheckedStatus
		return
	}

	callOpts := &bind.CallOpts{Context: ctx}

	// --- 5. Network type ---
	aggchainType, err := caller.AGGCHAINTYPE(callOpts)
	if err != nil {
		msg := fmt.Sprintf("query AGGCHAINTYPE: %v", err)
		log.Infof("❌ %s", msg)
		*failures = append(*failures, msg)
		result.NetworkType = "unknown"
		log.Info("   (AGGCHAINTYPE unavailable — contract may be pre-aggchainbase;" +
			" attempting legacy rollup manager diagnostics)")
		logLegacyRollupInfo(ctx, caller, cfg.SovereignRollupAddr, l1Client)
	} else if aggchainType == aggchainTypePP {
		result.NetworkType = "PP"
		log.Info("✅ network type is Pessimistic Proof (PP) — supported")
	} else {
		result.NetworkType = "FEP"
		msg := fmt.Sprintf("network type is FEP (AGGCHAINTYPE=%v) — only Pessimistic Proof (PP) is supported", aggchainType)
		log.Infof("❌ %s", msg)
		*failures = append(*failures, msg)
	}

	// --- 6. Threshold ---
	threshold, err := caller.Threshold(callOpts)
	if err != nil {
		msg := fmt.Sprintf("query Threshold: %v", err)
		log.Infof("❌ %s", msg)
		*failures = append(*failures, msg)
		return
	}

	signers, err := caller.GetAggchainSignerInfos(callOpts)
	if err != nil {
		msg := fmt.Sprintf("query GetAggchainSignerInfos: %v", err)
		log.Infof("❌ %s", msg)
		*failures = append(*failures, msg)
		return
	}

	result.Threshold = threshold.Uint64()
	result.SignerCount = len(signers)
	for _, s := range signers {
		result.Signers = append(result.Signers, s.Addr.Hex())
	}

	log.Infof("   Multisig committee: threshold=%d of %d", result.Threshold, result.SignerCount)
	for i, s := range signers {
		log.Infof("   Signer[%d]: addr=%s url=%s", i, s.Addr.Hex(), s.Url)
	}

	if result.Threshold == 1 {
		log.Info("✅ threshold is 1 — supported")
	} else {
		msg := fmt.Sprintf(
			"multisig threshold is %d — this tool produces only 1 signature, agglayer will reject the certificate",
			result.Threshold,
		)
		log.Infof("❌ %s", msg)
		*failures = append(*failures, msg)
	}
	bridgeAddr, err := caller.BridgeAddress(callOpts)
	if err != nil {
		msg := fmt.Sprintf("query BridgeAddress: %v", err)
		log.Infof("❌ %s", msg)
		*failures = append(*failures, msg)
	} else {
		// aggchainbase lives on L1 and its bridgeAddress is the L1 bridge, so it must match
		// l1BridgeAddress — the address Step E scans for unclaimed L1→L2 deposits.
		if bridgeAddr != cfg.L1BridgeAddress {
			msg := fmt.Sprintf("L1 bridge address mismatch: aggchainbase bridgeAddress()=%s, config l1BridgeAddress=%s",
				bridgeAddr.Hex(), cfg.L1BridgeAddress.Hex())
			log.Infof("❌ %s", msg)
			*failures = append(*failures, msg)
		} else {
			log.Infof("✅ l1BridgeAddress matches aggchainbase bridgeAddress() (%s)", bridgeAddr.Hex())
		}
	}

	rollupManager, err := caller.RollupManager(callOpts)
	if err != nil {
		msg := fmt.Sprintf("query RollupManager: %v", err)
		log.Infof("❌ %s", msg)
		*failures = append(*failures, msg)
	} else {
		log.Infof("   RollupManager address: %s", rollupManager.Hex())
		checkRollupManagerBridgeAddress(ctx, cfg, rollupManager, l1Client, result, failures)
	}
}

// checkRollupManagerBridgeAddress cross-checks cfg.L1BridgeAddress against the canonical L1 bridge
// the RollupManager publishes (bridgeAddress()). This is the authoritative source, so the mismatch
// error carries the correct address to put in the config.
func checkRollupManagerBridgeAddress(
	ctx context.Context, cfg *Config, rollupManagerAddr common.Address,
	l1Client *ethclient.Client, result *StepCheckResult, failures *[]string,
) {
	rmCaller, err := polygonrollupmanagerpessimistic.NewPolygonrollupmanagerpessimisticCaller(rollupManagerAddr, l1Client)
	if err != nil {
		msg := fmt.Sprintf("create rollup manager caller (addr=%s): %v", rollupManagerAddr.Hex(), err)
		log.Infof("❌ %s", msg)
		*failures = append(*failures, msg)
		return
	}

	canonicalBridge, err := rmCaller.BridgeAddress(&bind.CallOpts{Context: ctx})
	if err != nil {
		msg := fmt.Sprintf("query rollupManager bridgeAddress() (addr=%s): %v", rollupManagerAddr.Hex(), err)
		log.Infof("❌ %s", msg)
		*failures = append(*failures, msg)
		return
	}

	result.RollupManagerBridgeAddress = canonicalBridge.Hex()
	if canonicalBridge != cfg.L1BridgeAddress {
		msg := fmt.Sprintf("l1BridgeAddress mismatch: rollupManager %s reports the L1 bridge is %s, config has %s — "+
			"set l1BridgeAddress=%s", rollupManagerAddr.Hex(), canonicalBridge.Hex(),
			cfg.L1BridgeAddress.Hex(), canonicalBridge.Hex())
		log.Infof("❌ %s", msg)
		*failures = append(*failures, msg)
		return
	}
	log.Infof("✅ l1BridgeAddress matches rollupManager bridgeAddress() (%s)", canonicalBridge.Hex())
}

// logLegacyRollupInfo gathers rollup manager diagnostics when AGGCHAINTYPE is unavailable
// (pre-aggchainbase contracts). It does not modify check results or failures — it only logs.
func logLegacyRollupInfo(
	ctx context.Context,
	caller *aggchainbase.AggchainbaseCaller,
	sovereignRollupAddr common.Address,
	l1Client *ethclient.Client,
) {
	callOpts := &bind.CallOpts{Context: ctx}

	rollupManagerAddr, err := caller.RollupManager(callOpts)
	if err != nil {
		log.Infof("   (legacy diagnostics) RollupManager() failed: %v", err)
		return
	}
	log.Infof("   (legacy diagnostics) RollupManager: %s", rollupManagerAddr.Hex())

	rmCaller, err := polygonrollupmanagerpessimistic.NewPolygonrollupmanagerpessimisticCaller(rollupManagerAddr, l1Client)
	if err != nil {
		log.Infof("   (legacy diagnostics) create rollup manager caller: %v", err)
		return
	}

	rollupID, err := rmCaller.RollupAddressToID(callOpts, sovereignRollupAddr)
	if err != nil {
		log.Infof("   (legacy diagnostics) RollupAddressToID(%s): %v", sovereignRollupAddr.Hex(), err)
		return
	}
	log.Infof("   (legacy diagnostics) rollupID: %d", rollupID)

	rollupData, err := rmCaller.RollupIDToRollupData(callOpts, rollupID)
	if err != nil {
		log.Infof("   (legacy diagnostics) RollupIDToRollupData(%d): %v", rollupID, err)
		return
	}
	log.Infof("   (legacy diagnostics) rollupTypeID: %d  chainID: %d  forkID: %d  rollupVerifierType: %d",
		rollupData.RollupTypeID, rollupData.ChainID, rollupData.ForkID, rollupData.RollupVerifierType)

	typeInfo, err := rmCaller.RollupTypeMap(callOpts, uint32(rollupData.RollupTypeID))
	if err != nil {
		log.Infof("   (legacy diagnostics) RollupTypeMap(%d): %v", rollupData.RollupTypeID, err)
		return
	}
	log.Infof("   (legacy diagnostics) rollupType: consensusImpl=%s  verifier=%s  forkID=%d  verifierType=%d  obsolete=%v",
		typeInfo.ConsensusImplementation.Hex(), typeInfo.Verifier.Hex(),
		typeInfo.ForkID, typeInfo.RollupVerifierType, typeInfo.Obsolete)
	log.Infof("   (legacy diagnostics) rollupType: genesis=%x  programVKey=%x",
		typeInfo.Genesis, typeInfo.ProgramVKey)
}
