package exit_certificate

import (
	"math/big"
	"strings"

	"github.com/agglayer/aggkit/log"
)

// RunStepC computes the value locked in smart contracts for each token.
//
// Formula: SC_locked = LBT_totalSupply − accumulated_EOA_balances
//
// The LBT gives total supply per token. The accumulated EOA balances (Step B)
// tell us how much is held by EOAs. The difference is held by smart contracts.
func RunStepC(lbtEntries []LBTEntry, stepB *StepBResult) (*StepCResult, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP C — SC-locked value extraction")
	log.Info("═══════════════════════════════════════════")
	log.Infof("LBT has %d entries", len(lbtEntries))

	lbtByToken := indexByAddress(lbtEntries)

	eoaByToken := make(map[string]*big.Int, len(stepB.Accumulated))
	for _, entry := range stepB.Accumulated {
		key := strings.ToLower(entry.WrappedTokenAddress.Hex())
		eoaByToken[key] = parseDecimalBigInt(entry.TotalBalance)
	}

	scLockedValues, nonZeroCount := computeSCLocked(lbtByToken, eoaByToken)

	for tokenKey, eoaTotal := range eoaByToken {
		if _, exists := lbtByToken[tokenKey]; !exists && eoaTotal.Sign() > 0 {
			log.Warnf("Token %s has EOA balance (%s) but is not in LBT — skipping", tokenKey, eoaTotal)
		}
	}

	log.Infof("STEP C complete: %d tokens analyzed, %d have SC-locked value",
		len(scLockedValues), nonZeroCount)

	return &StepCResult{SCLockedValues: scLockedValues}, nil
}

func computeSCLocked(lbtByToken map[string]LBTEntry, eoaByToken map[string]*big.Int) ([]SCLockedValue, int) {
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

		if locked.Sign() > 0 {
			nonZeroCount++
		}

		scLockedValues = append(scLockedValues, SCLockedValue{
			WrappedTokenAddress: lbt.WrappedTokenAddress,
			OriginNetwork:       lbt.OriginNetwork,
			OriginTokenAddress:  lbt.OriginTokenAddress,
			LBTBalance:          lbtBalance.String(),
			EOAAccumulated:      eoaTotal.String(),
			SCLockedBalance:     locked.String(),
		})
	}

	return scLockedValues, nonZeroCount
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
