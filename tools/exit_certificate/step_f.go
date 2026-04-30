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

// RunStepF queries the agglayer admin API for token balances and compares them
// against the sums derived from the step-D certificate bridge exits.
// Skipped when agglayerAdminURL is not set in options.
func RunStepF(
	ctx context.Context, cfg *Config,
	certificate *agglayertypes.Certificate,
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
	checks := compareTokenBalances(groups, agglayerResp.Balances)

	allMatch := true
	for _, c := range checks {
		if !c.Match {
			allMatch = false
			log.Warnf("MISMATCH (network=%d addr=%s): certificate=%s agglayer=%s",
				c.OriginNetwork, c.OriginTokenAddress, c.CertificateAmount, c.AgglayerAmount)
			for i, e := range c.CertificateEntries {
				log.Infof("  [%d] dest_network=%d dest=%s amount=%s",
					i, e.DestinationNetwork, e.DestinationAddress, e.Amount)
			}
		}
	}
	if allMatch {
		log.Infof("All %d token balances match agglayer state", len(checks))
	}

	log.Info("STEP F complete")

	return &StepFResult{
		AllMatch:      allMatch,
		TokenBalances: raw,
		Checks:        checks,
	}, nil
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

// compareTokenBalances builds the per-token comparison list from both sources.
// CertificateEntries is populated only on mismatch.
func compareTokenBalances(
	groups map[tokenKey][]*agglayertypes.BridgeExit,
	agglayerEntries []agglayerTokenEntry,
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

	seen := make(map[tokenKey]struct{}, len(groups)+len(agglayerMap))
	for k := range groups {
		seen[k] = struct{}{}
	}
	for k := range agglayerMap {
		seen[k] = struct{}{}
	}

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

		match := certAmt.Cmp(agglAmt) == 0
		check := TokenBalanceCheck{
			OriginNetwork:      k.OriginNetwork,
			OriginTokenAddress: k.OriginTokenAddress.Hex(),
			CertificateAmount:  certAmt.String(),
			AgglayerAmount:     agglAmt.String(),
			Match:              match,
		}
		if !match {
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
