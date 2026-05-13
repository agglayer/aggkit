package exit_certificate

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
)

// agglayerTokenEntry is a single entry from admin_getTokenBalance response.
type agglayerTokenEntry struct {
	OriginNetwork      uint32         `json:"originNetwork"`
	OriginTokenAddress common.Address `json:"originTokenAddress"`
	Amount             string         `json:"amount"` // decimal U256
}

// agglayerBalanceResponse is the full admin_getTokenBalance JSON response.
type agglayerBalanceResponse struct {
	Balances []agglayerTokenEntry `json:"balances"`
}

// tokenKey identifies a token uniquely.
type tokenKey struct {
	OriginNetwork      uint32
	OriginTokenAddress common.Address
}

// RunStepF queries the agglayer admin API for token balances and performs a three-way comparison:
// LBT (Step 0 total supplies) == agglayer balance == sum of certificate bridge exits.
// lbtEntries may be nil when LBT data is unavailable; the check then falls back to two-way comparison.
// Skipped when agglayerAdminURL is not set in options.
func RunStepF(
	ctx context.Context, cfg *Config,
	certificate *agglayertypes.Certificate,
	lbtEntries []LBTEntry,
) (*StepFResult, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP F — Agglayer token balance check")
	log.Info("═══════════════════════════════════════════")

	if cfg.Options.AgglayerAdminURL == "" {
		log.Warn("STEP F skipped: agglayerAdminURL not set in options")
		return &StepFResult{Skipped: true}, nil
	}

	log.Infof("Querying %s (network %d)", cfg.Options.AgglayerAdminURL, cfg.L2NetworkID)

	raw, err := singleRPC(
		ctx, cfg.Options.AgglayerAdminURL,
		"admin_getTokenBalance",
		[]any{cfg.L2NetworkID, nil},
		defaultRetries,
	)
	if err != nil {
		return nil, fmt.Errorf("admin_getTokenBalance (network %d): %w", cfg.L2NetworkID, err)
	}

	var agglayerResp agglayerBalanceResponse
	if err := json.Unmarshal(raw, &agglayerResp); err != nil {
		return nil, fmt.Errorf("parse admin_getTokenBalance response: %w", err)
	}

	groups := groupBridgeExitsByToken(certificate)
	checks := compareTokenBalances(groups, agglayerResp.Balances, lbtEntries)

	allMatch := true
	for _, c := range checks {
		if !c.Match {
			allMatch = false
			if c.LBTAmount != "" {
				log.Warnf("❌ MISMATCH (network=%d addr=%s): lbt=%s  certificate=%s  agglayer=%s",
					c.OriginNetwork, c.OriginTokenAddress, c.LBTAmount, c.CertificateAmount, c.AgglayerAmount)
			} else {
				log.Warnf("❌ MISMATCH (network=%d addr=%s): certificate=%s  agglayer=%s",
					c.OriginNetwork, c.OriginTokenAddress, c.CertificateAmount, c.AgglayerAmount)
			}
			for i, e := range c.CertificateEntries {
				log.Infof("    ⚠️ [%d] dest_network=%d dest=%s amount=%s",
					i, e.DestinationNetwork, e.DestinationAddress, e.Amount)
			}
		} else {
			if c.LBTAmount != "" {
				log.Infof("✅ (network=%d addr=%s): lbt=%s  certificate=%s  agglayer=%s",
					c.OriginNetwork, c.OriginTokenAddress, c.LBTAmount, c.CertificateAmount, c.AgglayerAmount)
			} else {
				log.Infof("✅ (network=%d addr=%s): certificate=%s  agglayer=%s",
					c.OriginNetwork, c.OriginTokenAddress, c.CertificateAmount, c.AgglayerAmount)
			}
		}
	}
	if allMatch {
		if lbtEntries != nil {
			log.Infof("All %d token balances match ✅ LBT = agglayer = certificate", len(checks))
		} else {
			log.Infof("All %d token balances match agglayer state ✅", len(checks))
		}
	}

	log.Info("STEP F complete")

	result := &StepFResult{
		AllMatch:      allMatch,
		TokenBalances: raw,
		Checks:        checks,
	}
	if !allMatch {
		if cfg.Options.ContinueIfBalanceMismatch {
			log.Warn("Balance mismatches detected — continuing anyway (continueIfBalanceMismatch=true)")
			capMap := buildCapMap(checks)
			if len(capMap) > 0 {
				capped := *certificate
				capped.BridgeExits = capBridgeExits(certificate.BridgeExits, capMap)
				result.CappedCertificate = &capped
				log.Infof("🔧 Capped certificate: %d → %d bridge exits",
					len(certificate.BridgeExits), len(capped.BridgeExits))
			}
		} else {
			return result, fmt.Errorf("token balance mismatches detected (set options.continueIfBalanceMismatch=true to ignore)")
		}
	}
	return result, nil
}

// groupBridgeExitsByToken groups bridge exits from the certificate by TokenInfo.
func groupBridgeExitsByToken(cert *agglayertypes.Certificate) map[tokenKey][]*agglayertypes.BridgeExit {
	groups := make(map[tokenKey][]*agglayertypes.BridgeExit)
	if cert == nil {
		return groups
	}
	for _, exit := range cert.BridgeExits {
		if exit == nil || exit.TokenInfo == nil || exit.Amount == nil {
			continue
		}
		k := tokenKey{exit.TokenInfo.OriginNetwork, exit.TokenInfo.OriginTokenAddress}
		groups[k] = append(groups[k], exit)
	}
	return groups
}

// compareTokenBalances builds the per-token three-way comparison list.
// When lbtEntries is non-nil, match requires LBT == agglayer == certificate sum.
// When lbtEntries is nil, match requires agglayer == certificate sum (two-way fallback).
// CertificateEntries is populated only on mismatch.
func compareTokenBalances(
	groups map[tokenKey][]*agglayertypes.BridgeExit,
	agglayerEntries []agglayerTokenEntry,
	lbtEntries []LBTEntry,
) []TokenBalanceCheck {
	agglayerMap := make(map[tokenKey]*big.Int, len(agglayerEntries))
	for _, e := range agglayerEntries {
		k := tokenKey{e.OriginNetwork, e.OriginTokenAddress}
		amount, ok := new(big.Int).SetString(e.Amount, 10)
		if !ok {
			log.Warnf("Could not parse agglayer amount %q for token (network=%d addr=%s)",
				e.Amount, e.OriginNetwork, e.OriginTokenAddress.Hex())
			continue
		}
		agglayerMap[k] = amount
	}

	lbtMap := make(map[tokenKey]*big.Int, len(lbtEntries))
	for _, e := range lbtEntries {
		k := tokenKey{e.OriginNetwork, e.OriginTokenAddress}
		amount, ok := new(big.Int).SetString(e.Balance, 10)
		if !ok {
			log.Warnf("Could not parse LBT balance %q for token (network=%d addr=%s)",
				e.Balance, e.OriginNetwork, e.OriginTokenAddress.Hex())
			continue
		}
		lbtMap[k] = amount
	}

	seen := make(map[tokenKey]struct{}, len(groups)+len(agglayerMap)+len(lbtMap))
	for k := range groups {
		seen[k] = struct{}{}
	}
	for k := range agglayerMap {
		seen[k] = struct{}{}
	}
	for k := range lbtMap {
		seen[k] = struct{}{}
	}

	hasLBT := lbtEntries != nil

	checks := make([]TokenBalanceCheck, 0, len(seen))
	for k := range seen {
		exits := groups[k]
		certAmt := new(big.Int)
		for _, e := range exits {
			certAmt.Add(certAmt, e.Amount)
		}

		agglAmt := agglayerMap[k]
		if agglAmt == nil {
			agglAmt = new(big.Int)
		}

		check := TokenBalanceCheck{
			OriginNetwork:      k.OriginNetwork,
			OriginTokenAddress: k.OriginTokenAddress.Hex(),
			CertificateAmount:  certAmt.String(),
			AgglayerAmount:     agglAmt.String(),
		}

		if hasLBT {
			lbtAmt := lbtMap[k]
			if lbtAmt == nil {
				lbtAmt = new(big.Int)
			}
			check.LBTAmount = lbtAmt.String()
			check.Match = certAmt.Cmp(agglAmt) == 0 && agglAmt.Cmp(lbtAmt) == 0
		} else {
			check.Match = certAmt.Cmp(agglAmt) == 0
		}

		if !check.Match {
			check.CertificateEntries = make([]CertificateEntry, len(exits))
			for i, e := range exits {
				check.CertificateEntries[i] = CertificateEntry{
					DestinationNetwork: e.DestinationNetwork,
					DestinationAddress: e.DestinationAddress.Hex(),
					Amount:             e.Amount.String(),
				}
			}
		}
		checks = append(checks, check)
	}

	sort.Slice(checks, func(i, j int) bool {
		if checks[i].OriginNetwork != checks[j].OriginNetwork {
			return checks[i].OriginNetwork < checks[j].OriginNetwork
		}
		return checks[i].OriginTokenAddress < checks[j].OriginTokenAddress
	})
	return checks
}

// buildCapMap derives the per-token capped amount from the balance checks.
// cappedAmt = min(agglayer, lbt) when LBT is available, agglayer otherwise.
// Only tokens where certAmt > cappedAmt are included.
func buildCapMap(checks []TokenBalanceCheck) map[tokenKey]*big.Int {
	caps := make(map[tokenKey]*big.Int)
	for _, c := range checks {
		if c.Match {
			continue
		}
		certAmt, ok := new(big.Int).SetString(c.CertificateAmount, 10)
		if !ok || certAmt.Sign() == 0 {
			continue
		}
		agglAmt, ok := new(big.Int).SetString(c.AgglayerAmount, 10)
		if !ok {
			agglAmt = new(big.Int)
		}

		var cappedAmt *big.Int
		if c.LBTAmount != "" {
			lbtAmt, ok := new(big.Int).SetString(c.LBTAmount, 10)
			if !ok {
				lbtAmt = new(big.Int)
			}
			if agglAmt.Cmp(lbtAmt) <= 0 {
				cappedAmt = new(big.Int).Set(agglAmt)
			} else {
				cappedAmt = new(big.Int).Set(lbtAmt)
			}
		} else {
			cappedAmt = new(big.Int).Set(agglAmt)
		}

		if certAmt.Cmp(cappedAmt) > 0 {
			k := tokenKey{
				OriginNetwork:      c.OriginNetwork,
				OriginTokenAddress: common.HexToAddress(c.OriginTokenAddress),
			}
			caps[k] = cappedAmt
			log.Infof("🔧 Cap token (network=%d addr=%s): %s → %s (agglayer=%s lbt=%s)",
				c.OriginNetwork, c.OriginTokenAddress,
				certAmt.String(), cappedAmt.String(),
				c.AgglayerAmount, c.LBTAmount)
		}
	}
	return caps
}

// capBridgeExits returns a new deep-copied slice of bridge exits with amounts proportionally
// scaled down for any token present in capMap. Exits that scale to zero are removed.
func capBridgeExits(exits []*agglayertypes.BridgeExit, capMap map[tokenKey]*big.Int) []*agglayertypes.BridgeExit {
	// Group by token to compute per-token totals.
	type group struct {
		indices []int
		total   *big.Int
	}
	groups := make(map[tokenKey]*group)
	for i, e := range exits {
		if e == nil || e.TokenInfo == nil || e.Amount == nil {
			continue
		}
		k := tokenKey{e.TokenInfo.OriginNetwork, e.TokenInfo.OriginTokenAddress}
		g, ok := groups[k]
		if !ok {
			g = &group{total: new(big.Int)}
			groups[k] = g
		}
		g.indices = append(g.indices, i)
		g.total.Add(g.total, e.Amount)
	}

	// Pre-compute scaled amounts (default: keep original).
	newAmounts := make([]*big.Int, len(exits))
	for i, e := range exits {
		if e != nil && e.Amount != nil {
			newAmounts[i] = new(big.Int).Set(e.Amount)
		} else {
			newAmounts[i] = new(big.Int)
		}
	}

	for k, cappedAmt := range capMap {
		g, ok := groups[k]
		if !ok || g.total.Sign() == 0 || cappedAmt.Cmp(g.total) >= 0 {
			continue
		}
		sumScaled := new(big.Int)
		for _, idx := range g.indices {
			// scaled = original * cappedAmt / total
			scaled := new(big.Int).Mul(exits[idx].Amount, cappedAmt)
			scaled.Div(scaled, g.total)
			newAmounts[idx] = scaled
			sumScaled.Add(sumScaled, scaled)
		}
		// Add rounding remainder to the last exit to keep the exact capped total.
		remainder := new(big.Int).Sub(cappedAmt, sumScaled)
		if remainder.Sign() > 0 {
			newAmounts[g.indices[len(g.indices)-1]].Add(newAmounts[g.indices[len(g.indices)-1]], remainder)
		}
	}

	// Build result with deep-copied exits; drop zero-amount entries.
	result := make([]*agglayertypes.BridgeExit, 0, len(exits))
	for i, e := range exits {
		if e == nil || newAmounts[i] == nil || newAmounts[i].Sign() == 0 {
			continue
		}
		exitCopy := *e
		if e.TokenInfo != nil {
			tc := *e.TokenInfo
			exitCopy.TokenInfo = &tc
		}
		if e.Metadata != nil {
			md := make([]byte, len(e.Metadata))
			copy(md, e.Metadata)
			exitCopy.Metadata = md
		}
		exitCopy.Amount = new(big.Int).Set(newAmounts[i])
		result = append(result, &exitCopy)
	}
	return result
}
