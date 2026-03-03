package backward_forward_let

import (
	"context"
	"fmt"
	"math/big"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

// ExecuteRecovery performs the on-chain recovery steps for the given diagnosis.
// It activates emergency state (if not already active), runs BackwardLET and/or ForwardLET
// as required by the case, and deactivates emergency state when done.
func ExecuteRecovery(ctx context.Context, env *Env, diagnosis *DiagnosisResult) error {
	l2ChainID, err := env.L2Client.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("get L2 chain ID: %w", err)
	}

	adminAuth, err := buildTransactOpts(
		ctx, env.Config.BackwardForwardLET.GERRemoverKey, l2ChainID, "ger-remover",
	)
	if err != nil {
		return fmt.Errorf("build admin transact opts: %w", err)
	}

	pauserAuth, err := buildTransactOpts(
		ctx, env.Config.BackwardForwardLET.EmergencyPauserKey, l2ChainID, "emergency-pauser",
	)
	if err != nil {
		return fmt.Errorf("build pauser transact opts: %w", err)
	}

	unpauserAuth, err := buildTransactOpts(
		ctx, env.Config.BackwardForwardLET.EmergencyUnpauserKey, l2ChainID, "emergency-unpauser",
	)
	if err != nil {
		return fmt.Errorf("build unpauser transact opts: %w", err)
	}

	callOpts := &bind.CallOpts{Context: ctx}

	if !diagnosis.IsEmergencyState {
		if err := stepActivateEmergency(ctx, env, pauserAuth, callOpts); err != nil {
			return fmt.Errorf("activate emergency state: %w", err)
		}
	} else {
		fmt.Println("[step] Emergency state already active, skipping activation.")
	}

	defer func() {
		if deactivateErr := stepDeactivateEmergency(ctx, env, unpauserAuth, callOpts); deactivateErr != nil {
			fmt.Printf("WARNING: failed to deactivate emergency state: %v\n", deactivateErr)
		}
	}()

	if diagnosis.Case == Case2 || diagnosis.Case == Case4 {
		if err := stepBackwardLET(ctx, env, adminAuth, callOpts, diagnosis); err != nil {
			return fmt.Errorf("backward LET: %w", err)
		}
	}

	// First ForwardLET: insert divergent leaves (bridges included on agglayer but not L2).
	if err := stepForwardLETDivergentLeaves(ctx, env, adminAuth, callOpts, diagnosis); err != nil {
		return fmt.Errorf("forward LET divergent leaves: %w", err)
	}

	// Second ForwardLET: insert extra L2 bridges (bridges on L2 but not agglayer).
	if len(diagnosis.ExtraL2Bridges) > 0 {
		if err := stepForwardLETExtraL2Bridges(ctx, env, adminAuth, callOpts, diagnosis); err != nil {
			return fmt.Errorf("forward LET extra L2 bridges: %w", err)
		}
	}

	return nil
}

// stepActivateEmergency sends an ActivateEmergencyState transaction and verifies the result.
func stepActivateEmergency(
	ctx context.Context,
	env *Env,
	auth *bind.TransactOpts,
	callOpts *bind.CallOpts,
) error {
	fmt.Println("[step] Activating emergency state...")

	tx, err := env.L2Bridge.ActivateEmergencyState(auth)
	if err != nil {
		return fmt.Errorf("send ActivateEmergencyState tx: %w", err)
	}

	receipt, err := waitForReceipt(ctx, env.L2Client, tx)
	if err != nil {
		return fmt.Errorf("wait for ActivateEmergencyState receipt: %w", err)
	}
	if receipt.Status != 1 {
		return fmt.Errorf("ActivateEmergencyState tx failed (status=%d)", receipt.Status)
	}

	active, err := env.L2Bridge.IsEmergencyState(callOpts)
	if err != nil {
		return fmt.Errorf("verify emergency state after activation: %w", err)
	}
	if !active {
		return fmt.Errorf("emergency state not active after ActivateEmergencyState")
	}

	fmt.Println("[step] Emergency state activated.")
	return nil
}

// stepDeactivateEmergency sends a DeactivateEmergencyState transaction and verifies the result.
func stepDeactivateEmergency(
	ctx context.Context,
	env *Env,
	auth *bind.TransactOpts,
	callOpts *bind.CallOpts,
) error {
	fmt.Println("[step] Deactivating emergency state...")

	tx, err := env.L2Bridge.DeactivateEmergencyState(auth)
	if err != nil {
		return fmt.Errorf("send DeactivateEmergencyState tx: %w", err)
	}

	receipt, err := waitForReceipt(ctx, env.L2Client, tx)
	if err != nil {
		return fmt.Errorf("wait for DeactivateEmergencyState receipt: %w", err)
	}
	if receipt.Status != 1 {
		return fmt.Errorf("DeactivateEmergencyState tx failed (status=%d)", receipt.Status)
	}

	active, err := env.L2Bridge.IsEmergencyState(callOpts)
	if err != nil {
		return fmt.Errorf("verify emergency state after deactivation: %w", err)
	}
	if active {
		return fmt.Errorf("emergency state still active after DeactivateEmergencyState")
	}

	fmt.Println("[step] Emergency state deactivated.")
	return nil
}

// stepBackwardLET rolls the L2 bridge back to diagnosis.DivergencePoint.
func stepBackwardLET(
	ctx context.Context,
	env *Env,
	auth *bind.TransactOpts,
	callOpts *bind.CallOpts,
	diagnosis *DiagnosisResult,
) error {
	fmt.Printf("[step] BackwardLET: rolling back to DC=%d...\n", diagnosis.DivergencePoint)

	allLeafHashes, err := fetchL2LeafHashesUpTo(ctx, env, diagnosis.L2CurrentDepositCount)
	if err != nil {
		return fmt.Errorf("fetch L2 leaf hashes: %w", err)
	}

	frontier, nextLeaf, proof, err := ComputeBackwardLETParams(allLeafHashes, diagnosis.DivergencePoint)
	if err != nil {
		return fmt.Errorf("compute BackwardLET params: %w", err)
	}

	var frontierBytes [32][32]byte
	for i, h := range frontier {
		frontierBytes[i] = [32]byte(h) //nolint:gosec // G602: i is bounded to [32] by range over [32]common.Hash
	}
	var proofBytes [32][32]byte
	for i, h := range proof {
		proofBytes[i] = [32]byte(h) //nolint:gosec // G602: i is bounded to [32] by range over [32]common.Hash
	}

	tx, err := env.L2Bridge.BackwardLET(
		auth,
		new(big.Int).SetUint64(uint64(diagnosis.DivergencePoint)),
		frontierBytes,
		[32]byte(nextLeaf),
		proofBytes,
	)
	if err != nil {
		return fmt.Errorf("send BackwardLET tx: %w", err)
	}

	receipt, err := waitForReceipt(ctx, env.L2Client, tx)
	if err != nil {
		return fmt.Errorf("wait for BackwardLET receipt: %w", err)
	}
	if receipt.Status != 1 {
		return fmt.Errorf("BackwardLET tx failed (status=%d)", receipt.Status)
	}

	dcBig, err := env.L2Bridge.DepositCount(callOpts)
	if err != nil {
		return fmt.Errorf("get deposit count after BackwardLET: %w", err)
	}
	if uint32(dcBig.Uint64()) != diagnosis.DivergencePoint {
		return fmt.Errorf("deposit count mismatch after BackwardLET: expected %d, got %d",
			diagnosis.DivergencePoint, dcBig.Uint64())
	}

	fmt.Printf("[step] BackwardLET complete. DC=%d\n", diagnosis.DivergencePoint)
	return nil
}

// stepForwardLETDivergentLeaves inserts divergent L1 leaves into the L2 bridge.
// These are bridges included on agglayer but not on L2.
func stepForwardLETDivergentLeaves(
	ctx context.Context,
	env *Env,
	auth *bind.TransactOpts,
	callOpts *bind.CallOpts,
	diagnosis *DiagnosisResult,
) error {
	fmt.Printf("[step] ForwardLET (divergent leaves): inserting %d leaf(ves)...\n", len(diagnosis.DivergentLeaves))

	newLeaves := make([]agglayerbridgel2.AgglayerBridgeL2LeafData, 0, len(diagnosis.DivergentLeaves))
	for _, be := range diagnosis.DivergentLeaves {
		newLeaves = append(newLeaves, bridgeExitToContractLeaf(be))
	}

	// Compute the frontier at DivergencePoint from the L2 bridge service data.
	var leafHashesUpToDivergence []common.Hash
	var err error
	if diagnosis.DivergencePoint > 0 {
		leafHashesUpToDivergence, err = fetchL2LeafHashesUpTo(ctx, env, diagnosis.DivergencePoint)
		if err != nil {
			return fmt.Errorf("fetch L2 leaf hashes up to divergence point: %w", err)
		}
	}

	frontier, err := computeFrontier(leafHashesUpToDivergence, diagnosis.DivergencePoint)
	if err != nil {
		return fmt.Errorf("compute frontier at divergence point: %w", err)
	}

	divergentLeafHashes := make([]common.Hash, 0, len(diagnosis.DivergentLeaves))
	for _, be := range diagnosis.DivergentLeaves {
		divergentLeafHashes = append(divergentLeafHashes, BridgeExitLeafHash(be))
	}

	expectedLER, err := computeRootFromFrontier(frontier, diagnosis.DivergencePoint, divergentLeafHashes)
	if err != nil {
		return fmt.Errorf("compute expected LER for divergent leaves: %w", err)
	}

	tx, err := env.L2Bridge.ForwardLET(auth, newLeaves, [32]byte(expectedLER))
	if err != nil {
		return fmt.Errorf("send ForwardLET (divergent leaves) tx: %w", err)
	}

	receipt, err := waitForReceipt(ctx, env.L2Client, tx)
	if err != nil {
		return fmt.Errorf("wait for ForwardLET (divergent leaves) receipt: %w", err)
	}
	if receipt.Status != 1 {
		return fmt.Errorf("ForwardLET (divergent leaves) tx failed (status=%d)", receipt.Status)
	}

	expectedDC := diagnosis.DivergencePoint + uint32(len(diagnosis.DivergentLeaves))

	dcBig, err := env.L2Bridge.DepositCount(callOpts)
	if err != nil {
		return fmt.Errorf("get deposit count after ForwardLET (divergent leaves): %w", err)
	}
	if uint32(dcBig.Uint64()) != expectedDC {
		return fmt.Errorf("deposit count mismatch after ForwardLET (divergent leaves): expected %d, got %d",
			expectedDC, dcBig.Uint64())
	}

	root32, err := env.L2Bridge.GetRoot(callOpts)
	if err != nil {
		return fmt.Errorf("get root after ForwardLET (divergent leaves): %w", err)
	}
	if common.Hash(root32) != expectedLER {
		return fmt.Errorf("LER mismatch after ForwardLET (divergent leaves): expected %s, got %s",
			expectedLER.Hex(), common.Hash(root32).Hex())
	}

	fmt.Printf("[step] ForwardLET (divergent leaves) complete. DC=%d, LER=%s\n", expectedDC, expectedLER.Hex())
	return nil
}

// stepForwardLETExtraL2Bridges inserts extra real L2 bridges into the L2 bridge.
// These are bridges on L2 but not yet on agglayer, appended after the divergent leaves.
// The bridge service doesn't know about divergent leaves (inserted via ForwardLET), so
// the frontier at DivergencePoint+len(DivergentLeaves) is built from L2 service data
// plus the divergent leaf hashes.
func stepForwardLETExtraL2Bridges(
	ctx context.Context,
	env *Env,
	auth *bind.TransactOpts,
	callOpts *bind.CallOpts,
	diagnosis *DiagnosisResult,
) error {
	fmt.Printf("[step] ForwardLET (extra L2 bridges): inserting %d leaf(ves)...\n", len(diagnosis.ExtraL2Bridges))

	newLeaves := make([]agglayerbridgel2.AgglayerBridgeL2LeafData, 0, len(diagnosis.ExtraL2Bridges))
	for _, ld := range diagnosis.ExtraL2Bridges {
		newLeaves = append(newLeaves, leafDataToContractLeaf(ld))
	}

	// Compute the frontier at DivergencePoint + len(DivergentLeaves).
	// The bridge service only holds real L2 bridges; divergent leaves were injected via
	// ForwardLET and are not visible there, so we build the full leaf hash sequence manually.
	afterDivergentCount := diagnosis.DivergencePoint + uint32(len(diagnosis.DivergentLeaves))

	allHashesBeforeExtra := make([]common.Hash, 0, int(afterDivergentCount))
	if diagnosis.DivergencePoint > 0 {
		l2Hashes, err := fetchL2LeafHashesUpTo(ctx, env, diagnosis.DivergencePoint)
		if err != nil {
			return fmt.Errorf("fetch L2 leaf hashes up to divergence point: %w", err)
		}
		allHashesBeforeExtra = append(allHashesBeforeExtra, l2Hashes...)
	}
	for _, be := range diagnosis.DivergentLeaves {
		allHashesBeforeExtra = append(allHashesBeforeExtra, BridgeExitLeafHash(be))
	}

	frontier, err := computeFrontier(allHashesBeforeExtra, afterDivergentCount)
	if err != nil {
		return fmt.Errorf("compute frontier after divergent leaves: %w", err)
	}

	extraLeafHashes := make([]common.Hash, 0, len(diagnosis.ExtraL2Bridges))
	for _, ld := range diagnosis.ExtraL2Bridges {
		extraLeafHashes = append(extraLeafHashes, leafDataLeafHash(ld))
	}

	expectedLER, err := computeRootFromFrontier(frontier, afterDivergentCount, extraLeafHashes)
	if err != nil {
		return fmt.Errorf("compute expected LER for extra L2 bridges: %w", err)
	}

	tx, err := env.L2Bridge.ForwardLET(auth, newLeaves, [32]byte(expectedLER))
	if err != nil {
		return fmt.Errorf("send ForwardLET (extra L2 bridges) tx: %w", err)
	}

	receipt, err := waitForReceipt(ctx, env.L2Client, tx)
	if err != nil {
		return fmt.Errorf("wait for ForwardLET (extra L2 bridges) receipt: %w", err)
	}
	if receipt.Status != 1 {
		return fmt.Errorf("ForwardLET (extra L2 bridges) tx failed (status=%d)", receipt.Status)
	}

	expectedDC := afterDivergentCount + uint32(len(diagnosis.ExtraL2Bridges))

	dcBig, err := env.L2Bridge.DepositCount(callOpts)
	if err != nil {
		return fmt.Errorf("get deposit count after ForwardLET (extra L2 bridges): %w", err)
	}
	if uint32(dcBig.Uint64()) != expectedDC {
		return fmt.Errorf("deposit count mismatch after ForwardLET (extra L2 bridges): expected %d, got %d",
			expectedDC, dcBig.Uint64())
	}

	root32, err := env.L2Bridge.GetRoot(callOpts)
	if err != nil {
		return fmt.Errorf("get root after ForwardLET (extra L2 bridges): %w", err)
	}
	if common.Hash(root32) != expectedLER {
		return fmt.Errorf("LER mismatch after ForwardLET (extra L2 bridges): expected %s, got %s",
			expectedLER.Hex(), common.Hash(root32).Hex())
	}

	fmt.Printf("[step] ForwardLET (extra L2 bridges) complete. DC=%d, LER=%s\n", expectedDC, expectedLER.Hex())
	return nil
}
