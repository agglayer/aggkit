package exit_certificate

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/agglayer/aggkit/log"
)

// RunStepC computes the value locked in smart contracts for each token.
//
// Formula: SC_locked = LBT_totalSupply − accumulated_EOA_balances
//
// When ERC20HolderBreakdowns are present (from Step B3), the portion of each token
// held by a vault/staking contract is distributed proportionally to its holders as
// individual HolderBridge exits instead of a single exit to exitAddress. The
// corresponding SC_locked value is reduced by the amount distributed.
func RunStepC(lbtEntries []LBTEntry, stepB *StepBResult) (*StepCResult, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP C — SC-locked value extraction")
	log.Info("═══════════════════════════════════════════")
	log.Infof("LBT has %d entries", len(lbtEntries))
	log.Infof("ERC-20 contracts to distribute as individual bridges: %d", len(stepB.ERC20HolderBreakdowns))

	lbtByToken := indexByAddress(lbtEntries)

	eoaByToken := make(map[string]*big.Int, len(stepB.Accumulated))
	for _, entry := range stepB.Accumulated {
		key := strings.ToLower(entry.WrappedTokenAddress.Hex())
		eoaByToken[key] = parseDecimalBigInt(entry.TotalBalance)
	}

	holderBridges, covered, err := processBreakdowns(stepB.ERC20HolderBreakdowns)
	if err != nil {
		return nil, err
	}

	scLockedValues, nonZeroCount, err := computeSCLocked(lbtByToken, eoaByToken, covered)
	if err != nil {
		return nil, err
	}

	for tokenKey, eoaTotal := range eoaByToken {
		if _, exists := lbtByToken[tokenKey]; !exists && eoaTotal.Sign() > 0 {
			log.Warnf("Token %s has EOA balance (%s) but is not in LBT — skipping", tokenKey, eoaTotal)
		}
	}

	log.Infof("STEP C complete: %d tokens analyzed, %d have SC-locked value, %d holder bridge exits",
		len(scLockedValues), nonZeroCount, len(holderBridges))

	return &StepCResult{SCLockedValues: scLockedValues, HolderBridges: holderBridges}, nil
}

// processBreakdowns computes HolderBridge entries from ERC-20 holder breakdowns (Step B3).
// The collateral token and each holder's balance are treated as 1:1: each holder receives
// exactly their vault-token balance as the collateral token amount — no proportional
// scaling against totalSupply.
//
// Returns an error if the sum of holder amounts exceeds the vault's actual holdings
// (over-distribution), which would indicate corrupt balance data.
func processBreakdowns(breakdowns []ERC20HolderBreakdown) ([]HolderBridge, map[string]*big.Int, error) {
	var holderBridges []HolderBridge
	covered := make(map[string]*big.Int) // wrappedToken lowercaseHex → total covered

	for _, bd := range breakdowns {
		if bd.Detected == nil || len(bd.Detected.WrappedTokenBalances) == 0 {
			continue
		}

		for _, wtb := range bd.Detected.WrappedTokenBalances {
			contractHolds := parseDecimalBigInt(wtb.Balance)
			if contractHolds.Sign() == 0 {
				continue
			}

			tokenKey := strings.ToLower(wtb.Token.WrappedTokenAddress.Hex())

			distributed := new(big.Int)
			for _, h := range bd.Holders {
				amount := parseDecimalBigInt(h.Balance)
				if amount.Sign() <= 0 {
					continue
				}
				holderBridges = append(holderBridges, HolderBridge{
					VaultAddress:        bd.Address,
					WrappedTokenAddress: wtb.Token.WrappedTokenAddress,
					OriginNetwork:       wtb.Token.OriginNetwork,
					OriginTokenAddress:  wtb.Token.OriginTokenAddress,
					HolderAddress:       h.Address,
					Amount:              amount.String(),
				})
				distributed.Add(distributed, amount)
			}

			if distributed.Cmp(contractHolds) > 0 {
				return nil, nil, fmt.Errorf(
					"vault %s: holder balances sum (%s) exceeds vault holdings (%s) for token %s — corrupt balance data",
					bd.Address.Hex(), distributed, contractHolds, wtb.Token.WrappedTokenAddress.Hex(),
				)
			}

			remainder := new(big.Int).Sub(contractHolds, distributed)
			log.Infof("  vault %s | token %s | total=%s | individual_bridges=%s (%d holder(s)) | to_exit_addr=%s",
				bd.Address.Hex(), wtb.Token.WrappedTokenAddress.Hex(),
				contractHolds, distributed, len(bd.Holders), remainder)
			if remainder.Sign() > 0 {
				log.Infof("    ↳ %s unattributed (contract holders not in EOA list) → will flow to exitAddress as SC-locked",
					remainder)
			}

			if covered[tokenKey] == nil {
				covered[tokenKey] = new(big.Int)
			}
			covered[tokenKey].Add(covered[tokenKey], distributed)
		}
	}

	return holderBridges, covered, nil
}

func computeSCLocked(
	lbtByToken map[string]LBTEntry,
	eoaByToken map[string]*big.Int,
	covered map[string]*big.Int,
) ([]SCLockedValue, int, error) {
	scLockedValues := make([]SCLockedValue, 0, len(lbtByToken))
	nonZeroCount := 0

	for tokenKey, lbt := range lbtByToken {
		lbtBalance := parseDecimalBigInt(lbt.Balance)
		eoaTotal := new(big.Int)
		if val, exists := eoaByToken[tokenKey]; exists {
			eoaTotal.Set(val)
		}

		locked := new(big.Int).Sub(lbtBalance, eoaTotal)
		if locked.Sign() < 0 {
			log.Warnf("Token %s: EOA total (%s) exceeds LBT (%s) by %s. Clamping to 0.",
				lbt.WrappedTokenAddress.Hex(), eoaTotal, lbtBalance, new(big.Int).Neg(locked))
			locked = new(big.Int)
		}

		holdersCovered := new(big.Int)
		if coveredAmt, ok := covered[tokenKey]; ok {
			beforeCoverage := new(big.Int).Set(locked)
			locked.Sub(locked, coveredAmt)
			if locked.Sign() < 0 {
				return nil, 0, fmt.Errorf(
					"token %s: holder bridge coverage (%s) exceeds SC-locked balance (%s); possible LBT or EOA data inconsistency",
					lbt.WrappedTokenAddress.Hex(), coveredAmt,
					new(big.Int).Add(locked, coveredAmt),
				)
			}
			holdersCovered.Set(coveredAmt)
			log.Infof("  SC_locked[%s]: %s → %s (-%s to holder bridges; %s vault remainder → SCLockedValues → exitAddress)",
				lbt.WrappedTokenAddress.Hex(), beforeCoverage, locked, coveredAmt, locked)
		}

		if locked.Sign() > 0 {
			nonZeroCount++
		}

		holdersCoveredStr := ""
		if holdersCovered.Sign() > 0 {
			holdersCoveredStr = holdersCovered.String()
		}

		totalLocked := new(big.Int).Add(locked, holdersCovered)

		scLockedValues = append(scLockedValues, SCLockedValue{
			WrappedTokenAddress:    lbt.WrappedTokenAddress,
			OriginNetwork:          lbt.OriginNetwork,
			OriginTokenAddress:     lbt.OriginTokenAddress,
			LBTBalance:             lbtBalance.String(),
			EOAAccumulated:         eoaTotal.String(),
			ERC20HoldersCovered:    holdersCoveredStr,
			TotalSCLockedBalance:   totalLocked.String(),
			PendingSCLockedBalance: locked.String(),
		})
	}

	return scLockedValues, nonZeroCount, nil
}

// indexByAddress indexes LBT entries by lowercased hex address.
// The native token entry (WrappedTokenAddress == zero address) is intentionally
// included: it maps to "0x0000...0000" and is treated the same as wrapped tokens
// for SC-locked value computation. Step D handles the native token distinction
// when building BridgeExit entries.
func indexByAddress(entries []LBTEntry) map[string]LBTEntry {
	m := make(map[string]LBTEntry, len(entries))
	for _, e := range entries {
		m[strings.ToLower(e.WrappedTokenAddress.Hex())] = e
	}
	return m
}
