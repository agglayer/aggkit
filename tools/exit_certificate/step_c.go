package exit_certificate

import (
	"math/big"
	"strings"

	"github.com/agglayer/aggkit/log"
)

// RunStepC loads LBT entries from the configured file and computes SC-locked values.
func RunStepC(cfg *Config, stepB *StepBResult) (*StepCResult, error) {
	lbtEntries, err := LoadLBTEntries(cfg.LBTFile)
	if err != nil {
		return nil, err
	}
	log.Infof("Loading LBT data from: %s", cfg.LBTFile)
	return RunStepCWithEntries(lbtEntries, stepB)
}

// RunStepCWithEntries computes the value locked in smart contracts for each token.
//
// Formula: SC_locked = LBT_totalSupply − accumulated_EOA_balances
//
// The LBT gives total supply per token. The accumulated EOA balances (Step B)
// tell us how much is held by EOAs. The difference is held by smart contracts.
func RunStepCWithEntries(lbtEntries []LBTEntry, stepB *StepBResult) (*StepCResult, error) {
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

	var scLockedValues []SCLockedValue
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

	for tokenKey, eoaTotal := range eoaByToken {
		if _, exists := lbtByToken[tokenKey]; !exists && eoaTotal.Sign() > 0 {
			log.Warnf("Token %s has EOA balance (%s) but is not in LBT — skipping", tokenKey, eoaTotal)
		}
	}

	log.Infof("STEP C complete: %d tokens analyzed, %d have SC-locked value",
		len(scLockedValues), nonZeroCount)

	return &StepCResult{SCLockedValues: scLockedValues}, nil
}

func indexByAddress(entries []LBTEntry) map[string]LBTEntry {
	m := make(map[string]LBTEntry, len(entries))
	for _, e := range entries {
		m[strings.ToLower(e.WrappedTokenAddress.Hex())] = e
	}
	return m
}
