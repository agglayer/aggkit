package exit_certificate

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/aggchainbase"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
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
//  3. L2 network ID matches the bridge contract.
//  4. sovereignRollupAddr is set.
//  5. Network type is PP (FEP is not supported).
//  6. Multisig threshold is 1.
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

	// --- 3. L2 network ID matches bridge contract ---
	checkL2NetworkID(ctx, cfg, result, &failures)

	// --- 4. sovereignRollupAddr is set ---
	zeroAddr := [20]byte{}
	if cfg.SovereignRollupAddr == zeroAddr {
		log.Info("❌ sovereignRollupAddr is not set — required to verify network type and threshold")
		failures = append(failures, "sovereignRollupAddr is required (set it in the config to verify the network is PP with threshold=1)")
		result.NetworkType = "unchecked"
	} else if l1Client != nil {
		// --- 5 & 6. Network type + threshold ---
		checkContractPrereqs(ctx, cfg, l1Client, result, &failures)
	} else {
		// L1 client failed — contract checks cannot run
		result.NetworkType = "unchecked"
		log.Info("❌ network type and threshold checks skipped — l1RpcUrl is not available")
		failures = append(failures, "network type and threshold could not be verified (l1RpcUrl unavailable)")
	}
	checkNativeGasToken(ctx, cfg, result, &failures)

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

func checkNativeGasToken(ctx context.Context, cfg *Config, result *StepCheckResult, failures *[]string) {
	gasTokenNetwork, gasTokenAddr, err := fetchGasTokenInfo(ctx, cfg.L2RPCURL, cfg.L2BridgeAddress, "latest")
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

// checkContractPrereqs queries the aggchainbase contract for network type and threshold.
// l1Client is already dialed and verified reachable by the caller.
func checkContractPrereqs(ctx context.Context, cfg *Config, l1Client *ethclient.Client, result *StepCheckResult, failures *[]string) {
	caller, err := aggchainbase.NewAggchainbaseCaller(cfg.SovereignRollupAddr, l1Client)
	if err != nil {
		msg := fmt.Sprintf("create aggchainbase caller (addr=%s): %v", cfg.SovereignRollupAddr.Hex(), err)
		log.Infof("❌ %s", msg)
		*failures = append(*failures, msg)
		result.NetworkType = "unchecked"
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
		if bridgeAddr != cfg.L2BridgeAddress {
			msg := fmt.Sprintf("bridge address mismatch: bridge contract=%s, config=%s", bridgeAddr.Hex(), cfg.L2BridgeAddress.Hex())
			log.Infof("❌ %s", msg)
			*failures = append(*failures, msg)
		} else {
			log.Infof("✅ bridge address from aggchainbase matches config (%s)", bridgeAddr.Hex())
		}
	}

	rollupManager, err := caller.RollupManager(callOpts)
	if err != nil {
		msg := fmt.Sprintf("query RollupManager: %v", err)
		log.Infof("❌ %s", msg)
		*failures = append(*failures, msg)
	} else {
		log.Infof("   RollupManager address: %s", rollupManager.Hex())
	}
}
