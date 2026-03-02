package backward_forward_let

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/big"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	bridgeservice "github.com/agglayer/aggkit/bridgeservice/client"
	"github.com/agglayer/aggkit/bridgesync"
	aggkitgrpc "github.com/agglayer/aggkit/grpc"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"google.golang.org/grpc/codes"
)

// Diagnose compares the AggLayer's settled L1 state against L2's on-chain bridge state,
// classifies the divergence into one of 4 runbook cases, and returns a DiagnosisResult.
func Diagnose(ctx context.Context, env *Env) (*DiagnosisResult, error) {
	result := &DiagnosisResult{Case: NoDivergence}

	// Step 1 — Query AggLayer settled state.
	info, err := env.AgglayerClient.GetNetworkInfo(ctx, env.L2NetworkID)
	if err != nil {
		// A NotFound response means the network is not yet known to the agglayer
		// (no certificates have been settled), so there is no divergence.
		var grpcErr aggkitgrpc.GRPCError
		if errors.As(err, &grpcErr) && grpcErr.Code == codes.NotFound {
			return result, nil
		}
		return nil, fmt.Errorf("get network info from agglayer: %w", err)
	}
	if info.SettledHeight == nil {
		// Agglayer has no settled certificates for this network.
		return result, nil
	}

	result.L1SettledHeight = *info.SettledHeight
	if info.SettledCertificateID != nil {
		result.L1SettledCertificateID = *info.SettledCertificateID
	}
	if info.SettledLER != nil {
		result.L1SettledLER = *info.SettledLER
	}
	if info.SettledLETLeafCount != nil {
		result.L1SettledDepositCount = uint32(*info.SettledLETLeafCount)
	}

	// Step 2 — Query L2 bridge contract.
	callOpts := &bind.CallOpts{Context: ctx}

	depositCountBig, err := env.L2Bridge.DepositCount(callOpts)
	if err != nil {
		return nil, fmt.Errorf("get L2 deposit count: %w", err)
	}
	result.L2CurrentDepositCount = uint32(depositCountBig.Uint64())

	root32, err := env.L2Bridge.GetRoot(callOpts)
	if err != nil {
		return nil, fmt.Errorf("get L2 bridge root: %w", err)
	}
	result.L2CurrentLER = common.Hash(root32)

	inEmergency, err := env.L2Bridge.IsEmergencyState(callOpts)
	if err != nil {
		return nil, fmt.Errorf("check L2 emergency state: %w", err)
	}
	result.IsEmergencyState = inEmergency

	// Step 3 — Detect NoDivergence.
	if result.L2CurrentLER == result.L1SettledLER &&
		result.L2CurrentDepositCount == result.L1SettledDepositCount {
		return result, nil
	}

	// Step 4 — Find DivergencePoint by walking settled certificates from newest to oldest.
	divergentLeaves, divPoint, divFound, apiErr := findDivergencePoint(ctx, env, result.L1SettledHeight,
		result.L1SettledDepositCount)
	if apiErr != nil {
		// Aggsender API was unreachable. Return partial result.
		result.AggsenderAPIFailed = true
		result.FailedCertHeight = apiErr.height
		result.FailedCertID = apiErr.certID
		return result, nil
	}

	result.DivergentLeaves = divergentLeaves
	if divFound {
		result.DivergencePoint = divPoint
	} else {
		// All settled leaves diverge; DivergencePoint = 0 (nothing matched).
		result.DivergencePoint = 0
	}

	// Step 5 — Classify the case.
	result.Case = classifyCase(result.L1SettledDepositCount, result.L2CurrentDepositCount, result.DivergencePoint)

	// Step 6 — Collect ExtraL2Bridges for Cases 2 and 4.
	if result.Case == Case2 || result.Case == Case4 {
		extra, err := collectExtraL2Bridges(ctx, env, result.DivergencePoint+1, result.L2CurrentDepositCount)
		if err != nil {
			return nil, fmt.Errorf("collect extra L2 bridges: %w", err)
		}
		result.ExtraL2Bridges = extra
	}

	// Step 7 — Compute undercollateralization.
	result.Undercollateralization = computeUndercollateralization(result.DivergentLeaves)

	return result, nil
}

// aggsenderAPIError carries context about a failed aggsender RPC call.
type aggsenderAPIError struct {
	height uint64
	certID common.Hash
}

// findDivergencePoint walks settled certificate heights from newest to oldest.
// It returns (divergentLeaves, divergencePoint, found, apiError).
// If apiError is non-nil, the aggsender RPC failed and the result is partial.
func findDivergencePoint(
	ctx context.Context,
	env *Env,
	settledHeight uint64,
	totalSettledLeaves uint32,
) ([]*agglayertypes.BridgeExit, uint32, bool, *aggsenderAPIError) {
	dcEnd := totalSettledLeaves
	var divergentLeaves []*agglayertypes.BridgeExit

	for h := settledHeight; ; h-- {
		exits, err := env.AggsenderRPC.GetCertificateBridgeExits(&h)
		if err != nil {
			// Determine cert ID for the error report from the agglayer client if possible.
			var certID common.Hash
			hdr, hdrErr := env.AgglayerClient.GetLatestSettledCertificateHeader(ctx, env.L2NetworkID)
			if hdrErr == nil && hdr != nil {
				certID = hdr.CertificateID
			}
			return nil, 0, false, &aggsenderAPIError{height: h, certID: certID}
		}

		n := uint32(len(exits))
		if n == 0 {
			// Empty certificate; skip.
			if h == 0 {
				break
			}
			continue
		}

		dcStart := dcEnd - n

		// Compare each exit in this certificate against the L2 bridge service.
		allMatch := checkCertExitsMatchL2(ctx, env, exits, dcStart)

		if allMatch {
			// This certificate fully matches L2; divergence starts after it.
			return divergentLeaves, dcEnd - 1, true, nil
		}

		// Prepend exits (maintain ascending deposit-count order).
		divergentLeaves = append(exits, divergentLeaves...)
		dcEnd = dcStart

		if h == 0 {
			break
		}
	}

	// No fully-matching certificate found.
	return divergentLeaves, 0, false, nil
}

// checkCertExitsMatchL2 returns true if all bridge exits in the certificate match
// the L2 bridge service data at their corresponding deposit counts.
func checkCertExitsMatchL2(
	ctx context.Context,
	env *Env,
	exits []*agglayertypes.BridgeExit,
	dcStart uint32,
) bool {
	for i, exit := range exits {
		dc := dcStart + uint32(i)
		br, err := env.BridgeService.GetBridgeByDepositCount(ctx, env.L2NetworkID, dc)
		if err != nil {
			// Not found or error — treat as mismatch.
			return false
		}
		if BridgeExitLeafHash(exit) != BridgeResponseLeafHash(br) {
			return false
		}
	}
	return true
}

// classifyCase returns the RecoveryCase based on settled and current deposit counts.
func classifyCase(l1SettledDC, l2CurrentDC, divergencePoint uint32) RecoveryCase {
	extraL2 := l2CurrentDC > divergencePoint
	extraL1 := l1SettledDC > divergencePoint+1 // more than 1 divergent L1 leaf

	switch {
	case !extraL2 && !extraL1:
		return Case1
	case extraL2 && !extraL1:
		return Case2
	case !extraL2 && extraL1:
		return Case3
	default: // extraL2 && extraL1
		return Case4
	}
}

// collectExtraL2Bridges gathers real L2 bridges for deposit counts [startDC, endDC).
func collectExtraL2Bridges(
	ctx context.Context,
	env *Env,
	startDC, endDC uint32,
) ([]bridgesync.LeafData, error) {
	extra := make([]bridgesync.LeafData, 0, endDC-startDC)
	for dc := startDC; dc < endDC; dc++ {
		br, err := env.BridgeService.GetBridgeByDepositCount(ctx, env.L2NetworkID, dc)
		if err != nil {
			if isNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("get L2 bridge at DC=%d: %w", dc, err)
		}
		extra = append(extra, BridgeResponseToLeafData(br))
	}
	return extra, nil
}

// computeUndercollateralization groups divergent leaves by token and sums their amounts.
func computeUndercollateralization(leaves []*agglayertypes.BridgeExit) []UndercollateralizedToken {
	type tokenKey struct {
		OriginNetwork uint32
		OriginAddress common.Address
	}
	totals := make(map[tokenKey]*big.Int)
	order := make([]tokenKey, 0)

	for _, leaf := range leaves {
		if leaf.TokenInfo == nil {
			continue
		}
		key := tokenKey{
			OriginNetwork: leaf.TokenInfo.OriginNetwork,
			OriginAddress: leaf.TokenInfo.OriginTokenAddress,
		}
		amount := leaf.Amount
		if amount == nil {
			amount = big.NewInt(0)
		}
		if _, exists := totals[key]; !exists {
			totals[key] = new(big.Int)
			order = append(order, key)
		}
		totals[key].Add(totals[key], amount)
	}

	result := make([]UndercollateralizedToken, 0, len(order))
	for _, key := range order {
		result = append(result, UndercollateralizedToken{
			TokenOriginNetwork: key.OriginNetwork,
			TokenOriginAddress: key.OriginAddress,
			Amount:             totals[key],
		})
	}
	return result
}

// isNotFound returns true if the error is a bridgeservice ErrNotFound sentinel.
func isNotFound(err error) bool {
	return errors.Is(err, bridgeservice.ErrNotFound)
}

// PrintDiagnosis prints a human-readable diagnosis summary to w.
func PrintDiagnosis(w io.Writer, result *DiagnosisResult) {
	fmt.Fprintln(w, "=== Backward/Forward LET Diagnosis ===")
	fmt.Fprintln(w)

	// L1 vs L2 state table.
	fmt.Fprintf(w, "%-30s %-66s %s\n", "State", "LER", "Deposit Count")
	fmt.Fprintf(w, "%-30s %-66s %d\n", "L1 Settled (AggLayer)",
		result.L1SettledLER.Hex(), result.L1SettledDepositCount)
	fmt.Fprintf(w, "%-30s %-66s %d\n", "L2 On-Chain (Bridge)",
		result.L2CurrentLER.Hex(), result.L2CurrentDepositCount)
	fmt.Fprintf(w, "L1 Settled Height:          %d\n", result.L1SettledHeight)
	fmt.Fprintf(w, "L1 Settled Certificate ID:  %s\n", result.L1SettledCertificateID.Hex())
	fmt.Fprintln(w)

	if result.IsEmergencyState {
		fmt.Fprintln(w, "WARNING: L2 bridge is currently in emergency state (paused).")
		fmt.Fprintln(w)
	}

	if result.Case == NoDivergence {
		fmt.Fprintln(w, "Case: NoDivergence — L1 settled state and L2 on-chain state are in sync.")
		return
	}

	if result.AggsenderAPIFailed {
		fmt.Fprintln(w, "WARNING: Aggsender RPC was unreachable during diagnosis.")
		fmt.Fprintf(w, "  Failed certificate height: %d\n", result.FailedCertHeight)
		fmt.Fprintf(w, "  Failed certificate ID:     %s\n", result.FailedCertID.Hex())
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Action required: contact your AggLayer admin with the above certificate details.")
		fmt.Fprintln(w, "Recovery cannot proceed until the aggsender RPC is accessible.")
		return
	}

	fmt.Fprintf(w, "Case: %s\n", caseDescription(result.Case))
	fmt.Fprintf(w, "Divergence Point (last matching DC): %d\n", result.DivergencePoint)
	fmt.Fprintln(w)

	// Divergent leaves table.
	if len(result.DivergentLeaves) > 0 {
		fmt.Fprintf(w, "Divergent L1-Settled Leaves (%d):\n", len(result.DivergentLeaves))
		fmt.Fprintf(w, "  %-8s %-10s %-42s %-10s %-42s %s\n",
			"LeafType", "OriginNet", "OriginAddr", "DestNet", "DestAddr", "Amount")
		for i, be := range result.DivergentLeaves {
			originNet := uint32(0)
			originAddr := common.Address{}
			if be.TokenInfo != nil {
				originNet = be.TokenInfo.OriginNetwork
				originAddr = be.TokenInfo.OriginTokenAddress
			}
			amount := big.NewInt(0)
			if be.Amount != nil {
				amount = be.Amount
			}
			fmt.Fprintf(w, "  [%d] %-8d %-10d %-42s %-10d %-42s %s\n",
				i, be.LeafType.Uint8(), originNet, originAddr.Hex(),
				be.DestinationNetwork, be.DestinationAddress.Hex(),
				amount.String())
		}
		fmt.Fprintln(w)
	}

	// Extra L2 bridges table.
	if len(result.ExtraL2Bridges) > 0 {
		fmt.Fprintf(w, "Extra Real L2 Bridges (%d):\n", len(result.ExtraL2Bridges))
		fmt.Fprintf(w, "  %-8s %-10s %-42s %-10s %-42s %s\n",
			"LeafType", "OriginNet", "OriginAddr", "DestNet", "DestAddr", "Amount")
		for i, ld := range result.ExtraL2Bridges {
			amount := big.NewInt(0)
			if ld.Amount != nil {
				amount = ld.Amount
			}
			fmt.Fprintf(w, "  [%d] %-8d %-10d %-42s %-10d %-42s %s\n",
				i, ld.LeafType, ld.OriginNetwork, ld.OriginAddress.Hex(),
				ld.DestinationNetwork, ld.DestinationAddress.Hex(),
				amount.String())
		}
		fmt.Fprintln(w)
	}

	// Undercollateralization table.
	if len(result.Undercollateralization) > 0 {
		fmt.Fprintf(w, "Undercollateralized Tokens (%d):\n", len(result.Undercollateralization))
		fmt.Fprintf(w, "  %-10s %-42s %s\n", "OriginNet", "OriginAddr", "Amount")
		for _, uc := range result.Undercollateralization {
			fmt.Fprintf(w, "  %-10d %-42s %s\n",
				uc.TokenOriginNetwork, uc.TokenOriginAddress.Hex(), uc.Amount.String())
		}
		fmt.Fprintln(w)
	}

	// Recovery summary.
	fmt.Fprintln(w, "=== Recovery Plan ===")
	printRecoveryPlanSummary(w, result)
}

func caseDescription(c RecoveryCase) string {
	switch c {
	case Case1:
		return "Case1 — ForwardLET only: single divergent leaf batch, no extra L2 bridges"
	case Case2:
		return "Case2 — BackwardLET + ForwardLET: single divergent leaf + extra real L2 bridges"
	case Case3:
		return "Case3 — ForwardLET only: multiple divergent leaf batches, no extra L2 bridges"
	case Case4:
		return "Case4 — BackwardLET + ForwardLET: multiple divergent leaves + extra real L2 bridges"
	default:
		return string(c)
	}
}

func printRecoveryPlanSummary(w io.Writer, result *DiagnosisResult) {
	fmt.Fprintln(w, "The following steps will be executed:")
	step := 1

	switch result.Case {
	case Case2, Case4:
		fmt.Fprintf(w, "  %d. BackwardLET: roll back L2 bridge to DivergencePoint DC=%d\n",
			step, result.DivergencePoint)
		step++
		fmt.Fprintf(w, "  %d. ForwardLET:  replay %d real L2 bridge(s) on-chain\n",
			step, len(result.ExtraL2Bridges))
		step++
	case Case1, Case3:
		fmt.Fprintf(w, "  %d. ForwardLET:  inject %d correct leaf(ves) to fix the settled LET\n",
			step, len(result.DivergentLeaves))
		step++
	}

	fmt.Fprintf(w, "  %d. Verify: confirm L2 LER matches L1 settled LER\n", step)
}
