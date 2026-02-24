package remove_ger

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	"github.com/agglayer/aggkit/bridgeservice/client"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

const pollBridgeTimeout = 2 * time.Minute

// ExecuteRecovery runs the recovery flow for the given diagnosis. All steps execute on L2.
// On any error, returns immediately; the bridge may remain in emergency state for manual intervention.
func ExecuteRecovery(ctx context.Context, cfg *Config, env *Env, diagnosis *DiagnosisResult) error {
	l2ChainID, err := env.L2.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("get L2 chain ID: %w", err)
	}
	auth, err := buildSovereignAdminTransactor(cfg, l2ChainID)
	if err != nil {
		return fmt.Errorf("sovereign admin transactor: %w", err)
	}

	callOpts := &bind.CallOpts{Context: ctx}

	switch diagnosis.Scenario {
	case ScenarioNoClaims:
		if err := stepFreezeBridge(ctx, env, auth, callOpts); err != nil {
			return err
		}
		if err := stepRemoveGERs(ctx, env, auth, callOpts, diagnosis.InvalidGER); err != nil {
			return err
		}
		if err := stepRestoreBridge(ctx, env, auth, callOpts); err != nil {
			return err
		}
		return nil

	case ScenarioCategoryA:
		if err := stepFreezeBridge(ctx, env, auth, callOpts); err != nil {
			return err
		}
		if err := stepRemoveGERs(ctx, env, auth, callOpts, diagnosis.InvalidGER); err != nil {
			return err
		}
		if err := stepUnsetClaims(ctx, env, auth, callOpts, diagnosis.Claims); err != nil {
			return err
		}
		if err := stepRestoreBridge(ctx, env, auth, callOpts); err != nil {
			return err
		}
		return nil

	case ScenarioCategoryB1:
		if err := stepFreezeBridge(ctx, env, auth, callOpts); err != nil {
			return err
		}
		if err := stepRemoveGERs(ctx, env, auth, callOpts, diagnosis.InvalidGER); err != nil {
			return err
		}
		if err := stepForceEmitDetailedClaimEvents(ctx, cfg, env, auth, diagnosis.Claims); err != nil {
			return err
		}
		if err := stepRestoreBridge(ctx, env, auth, callOpts); err != nil {
			return err
		}
		return nil

	case ScenarioCategoryB2:
		if err := stepFreezeBridge(ctx, env, auth, callOpts); err != nil {
			return err
		}
		if err := stepRemoveGERs(ctx, env, auth, callOpts, diagnosis.InvalidGER); err != nil {
			return err
		}
		if err := stepUnsetClaims(ctx, env, auth, callOpts, diagnosis.Claims); err != nil {
			return err
		}
		correctIndexes := make([]*big.Int, 0, len(diagnosis.Claims))
		for _, cd := range diagnosis.Claims {
			if cd.CorrectBridge == nil {
				return fmt.Errorf("B.2 claim missing CorrectBridge")
			}
			correctIndexes = append(correctIndexes,
				bridgesync.GenerateGlobalIndexForNetworkID(cd.CorrectBridge.OriginNetwork, cd.CorrectBridge.DepositCount))
		}
		if err := stepSetClaims(ctx, env, auth, callOpts, diagnosis.Claims, correctIndexes); err != nil {
			return err
		}
		if err := stepForceEmitDetailedClaimEvents(ctx, cfg, env, auth, diagnosis.Claims); err != nil {
			return err
		}
		if err := stepRestoreBridge(ctx, env, auth, callOpts); err != nil {
			return err
		}
		return nil

	default:
		return fmt.Errorf("unsupported scenario: %s", diagnosis.Scenario)
	}
}

func stepFreezeBridge(ctx context.Context, env *Env, auth *bind.TransactOpts, callOpts *bind.CallOpts) error {
	fmt.Println("Step: Freeze bridge (activateEmergencyState)")
	tx, err := env.L2Bridge.ActivateEmergencyState(auth)
	if err != nil {
		return fmt.Errorf("activateEmergencyState: %w (bridge may remain in previous state)", err)
	}
	fmt.Printf("  Tx hash: %s\n", tx.Hash().Hex())
	receipt, err := waitForReceipt(ctx, env.L2, tx)
	if err != nil {
		return fmt.Errorf("wait for activateEmergencyState receipt: %w", err)
	}
	if receipt.Status != 1 {
		return fmt.Errorf("activateEmergencyState tx failed (status %d)", receipt.Status)
	}
	ok, err := env.L2Bridge.IsEmergencyState(callOpts)
	if err != nil {
		return fmt.Errorf("verify IsEmergencyState: %w", err)
	}
	if !ok {
		return fmt.Errorf("bridge is not in emergency state after activateEmergencyState")
	}
	fmt.Println("  Verified: bridge is in emergency state")
	return nil
}

func stepRemoveGERs(
	ctx context.Context, env *Env, auth *bind.TransactOpts, callOpts *bind.CallOpts, ger common.Hash,
) error {
	fmt.Printf("Step: Remove GER %s (removeGlobalExitRoots)\n", ger.Hex())
	gersToRemove := [][32]byte{ger}
	tx, err := env.L2GERManager.RemoveGlobalExitRoots(auth, gersToRemove)
	if err != nil {
		return fmt.Errorf("removeGlobalExitRoots: %w (bridge may remain in emergency state)", err)
	}
	fmt.Printf("  Tx hash: %s\n", tx.Hash().Hex())
	receipt, err := waitForReceipt(ctx, env.L2, tx)
	if err != nil {
		return fmt.Errorf("wait for removeGlobalExitRoots receipt: %w", err)
	}
	if receipt.Status != 1 {
		return fmt.Errorf("removeGlobalExitRoots tx failed (status %d)", receipt.Status)
	}
	ts, err := env.L2GERManager.GlobalExitRootMap(callOpts, ger)
	if err != nil {
		return fmt.Errorf("verify GlobalExitRootMap: %w", err)
	}
	if ts != nil && ts.Sign() > 0 {
		return fmt.Errorf("GER still present on L2 after removal (timestamp %s)", ts.String())
	}
	fmt.Println("  Verified: GER removed from L2")
	if env.BridgeService != nil {
		gerHex := ger.Hex()
		err = pollBridgeService(ctx, env.BridgeService, func() (bool, error) {
			res, err := env.BridgeService.GetRemoveGEREvents(ctx, client.GetRemoveGEREventsParams{
				GlobalExitRoot: &gerHex,
				Limit:          ptrInt(10),
			})
			if err != nil {
				return false, err
			}
			return res != nil && len(res.RemoveGEREvents) > 0, nil
		}, pollBridgeTimeout)
		if err != nil {
			fmt.Printf("  Warning: bridge service poll for remove GER event: %v\n", err)
		} else {
			fmt.Println("  Bridge service: remove GER event indexed")
		}
	}
	return nil
}

func stepRestoreBridge(ctx context.Context, env *Env, auth *bind.TransactOpts, callOpts *bind.CallOpts) error {
	fmt.Println("Step: Restore bridge (deactivateEmergencyState)")
	tx, err := env.L2Bridge.DeactivateEmergencyState(auth)
	if err != nil {
		return fmt.Errorf("deactivateEmergencyState: %w"+
			" (bridge remains in emergency state — manual intervention required)", err)
	}
	fmt.Printf("  Tx hash: %s\n", tx.Hash().Hex())
	receipt, err := waitForReceipt(ctx, env.L2, tx)
	if err != nil {
		return fmt.Errorf("wait for deactivateEmergencyState receipt: %w", err)
	}
	if receipt.Status != 1 {
		return fmt.Errorf("deactivateEmergencyState tx failed (status %d)", receipt.Status)
	}
	ok, err := env.L2Bridge.IsEmergencyState(callOpts)
	if err != nil {
		return fmt.Errorf("verify !IsEmergencyState: %w", err)
	}
	if ok {
		return fmt.Errorf("bridge is still in emergency state after deactivateEmergencyState")
	}
	fmt.Println("  Verified: bridge is not in emergency state")
	return nil
}

func stepUnsetClaims(
	ctx context.Context, env *Env, auth *bind.TransactOpts, callOpts *bind.CallOpts, claims []ClaimDiagnosis,
) error {
	if len(claims) == 0 {
		return nil
	}
	fmt.Println("Step: Unset claims (unsetMultipleClaims)")
	globalIndexes := make([]*big.Int, 0, len(claims))
	for _, cd := range claims {
		globalIndexes = append(globalIndexes, cd.GlobalIndex)
		fmt.Printf("  Unset global index %s\n", formatGlobalIndex(cd.GlobalIndex))
	}
	tx, err := env.L2Bridge.UnsetMultipleClaims(auth, globalIndexes)
	if err != nil {
		return fmt.Errorf("unsetMultipleClaims: %w (bridge may remain in emergency state)", err)
	}
	fmt.Printf("  Tx hash: %s\n", tx.Hash().Hex())
	receipt, err := waitForReceipt(ctx, env.L2, tx)
	if err != nil {
		return fmt.Errorf("wait for unsetMultipleClaims receipt: %w", err)
	}
	if receipt.Status != 1 {
		return fmt.Errorf("unsetMultipleClaims tx failed (status %d)", receipt.Status)
	}
	for _, cd := range claims {
		claimed, err := env.L2Bridge.IsClaimed(callOpts, cd.DepositCount, cd.OriginNetwork)
		if err != nil {
			return fmt.Errorf("verify IsClaimed(false) for deposit_count=%d origin_network=%d: %w",
				cd.DepositCount, cd.OriginNetwork, err)
		}
		if claimed {
			return fmt.Errorf("claim still marked claimed after unset (deposit_count=%d origin_network=%d)",
				cd.DepositCount, cd.OriginNetwork)
		}
	}
	fmt.Println("  Verified: all claims unset")
	return nil
}

func stepSetClaims(
	ctx context.Context, env *Env, auth *bind.TransactOpts, callOpts *bind.CallOpts,
	claims []ClaimDiagnosis, correctGlobalIndexes []*big.Int,
) error {
	if len(correctGlobalIndexes) == 0 {
		return nil
	}
	fmt.Println("Step: Set claims (setMultipleClaims)")
	tx, err := env.L2Bridge.SetMultipleClaims(auth, correctGlobalIndexes)
	if err != nil {
		return fmt.Errorf("setMultipleClaims: %w (bridge may remain in emergency state)", err)
	}
	fmt.Printf("  Tx hash: %s\n", tx.Hash().Hex())
	receipt, err := waitForReceipt(ctx, env.L2, tx)
	if err != nil {
		return fmt.Errorf("wait for setMultipleClaims receipt: %w", err)
	}
	if receipt.Status != 1 {
		return fmt.Errorf("setMultipleClaims tx failed (status %d)", receipt.Status)
	}
	for i, cd := range claims {
		if cd.CorrectBridge == nil {
			continue
		}
		claimed, err := env.L2Bridge.IsClaimed(callOpts, cd.CorrectBridge.DepositCount, cd.CorrectBridge.OriginNetwork)
		if err != nil {
			return fmt.Errorf("verify IsClaimed(true) for correct claim: %w", err)
		}
		if !claimed {
			return fmt.Errorf("correct claim not marked claimed after set (global index %s)",
				formatGlobalIndex(correctGlobalIndexes[i]))
		}
	}
	fmt.Println("  Verified: all correct claims set")
	return nil
}

func stepForceEmitDetailedClaimEvents(
	ctx context.Context, _ *Config, env *Env, auth *bind.TransactOpts, claims []ClaimDiagnosis,
) error {
	if len(claims) == 0 {
		return nil
	}
	claimDataList, err := buildForceEmitClaimData(ctx, env, claims)
	if err != nil {
		return fmt.Errorf("build forceEmit claim data: %w", err)
	}
	fmt.Println("Step: Force emit detailed claim events (forceEmitDetailedClaimEvent)")
	for i := range claimDataList {
		fmt.Printf("  Emit for global index %s\n", formatGlobalIndex(claimDataList[i].GlobalIndex))
	}
	tx, err := env.L2Bridge.ForceEmitDetailedClaimEvent(auth, claimDataList)
	if err != nil {
		return fmt.Errorf("forceEmitDetailedClaimEvent: %w (bridge may remain in emergency state)", err)
	}
	fmt.Printf("  Tx hash: %s\n", tx.Hash().Hex())
	receipt, err := waitForReceipt(ctx, env.L2, tx)
	if err != nil {
		return fmt.Errorf("wait for forceEmitDetailedClaimEvent receipt: %w", err)
	}
	if receipt.Status != 1 {
		return fmt.Errorf("forceEmitDetailedClaimEvent tx failed (status %d)", receipt.Status)
	}
	if env.BridgeService != nil {
		for _, cd := range claims {
			globalIndex := cd.GlobalIndex
			if cd.CorrectBridge != nil {
				globalIndex = bridgesync.GenerateGlobalIndexForNetworkID(
					cd.CorrectBridge.OriginNetwork, cd.CorrectBridge.DepositCount)
			}
			idx := globalIndex
			err = pollBridgeService(ctx, env.BridgeService, func() (bool, error) {
				res, err := env.BridgeService.GetClaims(ctx, client.GetClaimsParams{
					NetworkID:   1,
					GlobalIndex: idx,
				})
				if err != nil {
					return false, err
				}
				return res != nil && len(res.Claims) > 0, nil
			}, pollBridgeTimeout)
			if err != nil {
				fmt.Printf("  Warning: bridge service poll for claim %s: %v\n", formatGlobalIndex(globalIndex), err)
			}
		}
		fmt.Println("  Bridge service: corrected claim(s) indexed")
	}
	return nil
}

func buildForceEmitClaimData(
	ctx context.Context, env *Env, claims []ClaimDiagnosis,
) ([]agglayerbridgel2.AgglayerBridgeL2ClaimData, error) {
	out := make([]agglayerbridgel2.AgglayerBridgeL2ClaimData, 0, len(claims))
	for _, cd := range claims {
		if cd.CorrectBridge == nil {
			return nil, fmt.Errorf("claim with global index %s has nil CorrectBridge", formatGlobalIndex(cd.GlobalIndex))
		}
		bridgeResp, err := env.BridgeService.GetBridgeByDepositCount(ctx, 0, cd.CorrectBridge.DepositCount)
		if err != nil {
			return nil, fmt.Errorf("get L1 bridge deposit_count=%d: %w", cd.CorrectBridge.DepositCount, err)
		}
		l1Leaf, err := getL1InfoLeafByDepositCount(ctx, env.BridgeService, cd.CorrectBridge.DepositCount)
		if err != nil {
			return nil, fmt.Errorf("get L1 info leaf for deposit_count=%d: %w", cd.CorrectBridge.DepositCount, err)
		}
		var proofLocal, proofRollup [32][32]byte
		proof, err := env.BridgeService.GetClaimProof(ctx, 0, l1Leaf.L1InfoTreeIndex, cd.CorrectBridge.DepositCount)
		if err != nil {
			return nil, fmt.Errorf("get claim proof leaf_index=%d deposit_count=%d: %w",
				l1Leaf.L1InfoTreeIndex, cd.CorrectBridge.DepositCount, err)
		}
		for i := 0; i < 32 && i < len(proof.ProofLocalExitRoot); i++ {
			proofLocal[i] = common.HexToHash(string(proof.ProofLocalExitRoot[i]))
		}
		for i := 0; i < 32 && i < len(proof.ProofRollupExitRoot); i++ {
			proofRollup[i] = common.HexToHash(string(proof.ProofRollupExitRoot[i]))
		}
		globalIndex := cd.GlobalIndex
		if cd.Category == ScenarioCategoryB2 {
			globalIndex = bridgesync.GenerateGlobalIndexForNetworkID(
				cd.CorrectBridge.OriginNetwork, cd.CorrectBridge.DepositCount)
		}
		var mainnetExitRoot, rollupExitRoot [32]byte
		copy(mainnetExitRoot[:], l1Leaf.MainnetExitRoot[:])
		copy(rollupExitRoot[:], l1Leaf.RollupExitRoot[:])
		amount := cd.CorrectBridge.Amount
		if amount == nil {
			amount = big.NewInt(0)
		}
		metadata := cd.CorrectBridge.Metadata
		if metadata == nil {
			metadata = []byte{}
		}
		_ = bridgeResp // bridge data used for correctBridge content; claim data already in cd.CorrectBridge
		out = append(out, agglayerbridgel2.AgglayerBridgeL2ClaimData{
			SmtProofLocalExitRoot:  proofLocal,
			SmtProofRollupExitRoot: proofRollup,
			GlobalIndex:            globalIndex,
			MainnetExitRoot:        mainnetExitRoot,
			RollupExitRoot:         rollupExitRoot,
			LeafType:               cd.CorrectBridge.LeafType,
			OriginNetwork:          cd.CorrectBridge.OriginNetwork,
			OriginAddress:          cd.CorrectBridge.OriginAddress,
			DestinationNetwork:     cd.CorrectBridge.DestinationNetwork,
			DestinationAddress:     cd.CorrectBridge.DestinationAddress,
			Amount:                 amount,
			Metadata:               metadata,
		})
	}
	return out, nil
}

func ptrInt(n int) *int { return &n }
