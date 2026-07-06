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

// RunStepF verifies the certificate's per-token bridge-exit sums.
//
// When useAgglayerAdminToStepFCheck is true (the default) it queries the agglayer admin API for token
// balances and performs a three-way comparison: LBT (Step 0 total supplies) == agglayer balance ==
// sum of certificate bridge exits. agglayerAdminURL is required. lbtEntries may be nil, in which case
// it falls back to a two-way agglayer-vs-certificate comparison.
//
// When useAgglayerAdminToStepFCheck is false it skips the agglayer admin query and instead runs an
// offline two-way comparison of the LBT (Step 0) totals against the certificate bridge-exit sums (see
// runStepFOfflineLBT). When no LBT data is available there is nothing to compare and the step is skipped.
func RunStepF(
	ctx context.Context, cfg *Config,
	certificate *agglayertypes.Certificate,
	lbtEntries []LBTEntry,
) (*StepFResult, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP F — Agglayer token balance check")
	log.Info("═══════════════════════════════════════════")

	// Whenever the agglayer admin endpoint is configured, dump its full local balance tree (LBT) to
	// disk up front — regardless of the comparison mode below — so the agglayer-side balances are always
	// captured. The response is reused by the agglayer comparison to avoid a second RPC round-trip.
	var agglayerRaw json.RawMessage
	if cfg.Options.AgglayerAdminURL != "" {
		raw, err := queryAgglayerTokenBalance(ctx, cfg)
		if err != nil {
			return nil, err
		}
		agglayerRaw = raw
		// LoadConfig always sets OutputDir, so it is empty only for programmatically built configs
		// (e.g. unit tests) — skip the dump there rather than dropping the file in the process's
		// working directory.
		if cfg.Options.OutputDir != "" {
			saveJSON(cfg.Options.OutputDir, fileStepFAgglayerLBT, agglayerRaw)
		}
	}

	// The agglayer admin query is opt-out. When disabled we still run an offline LBT vs certificate
	// comparison instead of skipping the step outright.
	if !cfg.Options.UseAgglayerAdminToStepFCheck {
		return runStepFOfflineLBT(cfg, certificate, lbtEntries)
	}

	if cfg.Options.AgglayerAdminURL == "" {
		return nil, fmt.Errorf("step F requires agglayerAdminURL to be set in options")
	}

	raw := agglayerRaw
	var agglayerResp agglayerBalanceResponse
	if err := json.Unmarshal(raw, &agglayerResp); err != nil {
		return nil, fmt.Errorf("parse admin_getTokenBalance response: %w", err)
	}

	groups := groupBridgeExitsByToken(certificate)
	checks := compareTokenBalances(groups, agglayerResp.Balances, lbtEntries, genesisPrefundWei(cfg))

	allMatch := true
	for _, c := range checks {
		if !c.Match {
			allMatch = false
			if c.LBTAmount != "" {
				log.Warnf("❌ MISMATCH (network=%d addr=%s): lbt=%s  certificate=%s  agglayer=%s  "+
					"(certificate−agglayer=%s, certificate−lbt=%s)",
					c.OriginNetwork, c.OriginTokenAddress, c.LBTAmount, c.CertificateAmount, c.AgglayerAmount,
					amountDiff(c.CertificateAmount, c.AgglayerAmount), amountDiff(c.CertificateAmount, c.LBTAmount))
			} else {
				log.Warnf("❌ MISMATCH (network=%d addr=%s): certificate=%s  agglayer=%s  (certificate−agglayer=%s)",
					c.OriginNetwork, c.OriginTokenAddress, c.CertificateAmount, c.AgglayerAmount,
					amountDiff(c.CertificateAmount, c.AgglayerAmount))
			}
			for i, e := range c.CertificateEntries {
				log.Debugf("    ⚠️ [%d] dest_network=%d dest=%s amount=%s",
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

	return finalizeStepFResult(cfg, certificate, checks, raw, allMatch)
}

// nativeTokenKey identifies the native token (the gas token: origin network 0, zero origin address)
// in the comparison maps.
var nativeTokenKey = tokenKey{}

// amountDiff returns the signed decimal difference a − b between two internally generated decimal
// amount strings, or "?" if either is not parseable.
func amountDiff(a, b string) string {
	av, okA := new(big.Int).SetString(a, decimalBase)
	bv, okB := new(big.Int).SetString(b, decimalBase)
	if !okA || !okB {
		return "?"
	}
	return new(big.Int).Sub(av, bv).String()
}

// genesisPrefundWei parses options.genesisPrefundETHWei into a *big.Int, nil when unset. The format
// is validated by LoadConfig, so a parse failure only happens on hand-built configs and is treated
// as unset (with a warning).
func genesisPrefundWei(cfg *Config) *big.Int {
	if cfg.Options.GenesisPrefundETHWei == "" {
		return nil
	}
	v, ok := new(big.Int).SetString(cfg.Options.GenesisPrefundETHWei, decimalBase)
	if !ok {
		log.Warnf("invalid options.genesisPrefundETHWei %q ignored", cfg.Options.GenesisPrefundETHWei)
		return nil
	}
	return v
}

// discountGenesisPrefund subtracts the declared genesis pre-fund from the native-token certificate
// sum before it is compared, floored at zero. Genesis-minted native funds sit in accounts — and
// therefore in the certificate's bridge exits — without a matching agglayer deposit, so the
// comparison must run against the genuinely bridged amount. It logs the certificate total, the
// declared pre-fund and the resulting difference. Non-native tokens and an unset/zero pre-fund are
// returned unchanged.
func discountGenesisPrefund(certAmt *big.Int, k tokenKey, prefund *big.Int) *big.Int {
	if prefund == nil || prefund.Sign() <= 0 || k != nativeTokenKey {
		return certAmt
	}
	adjusted := new(big.Int).Sub(certAmt, prefund)
	if adjusted.Sign() < 0 {
		log.Warnf("🔧 Genesis pre-fund (native token): genesisPrefundETHWei (%s) exceeds the certificate sum (%s); "+
			"flooring the compared certificate amount at 0", prefund, certAmt)
		return new(big.Int)
	}
	log.Infof("🔧 Genesis pre-fund (native token): certificate=%s − genesisPrefundETHWei=%s = %s "+
		"(compared certificate amount)", certAmt, prefund, adjusted)
	return adjusted
}

// queryAgglayerTokenBalance calls admin_getTokenBalance on the agglayer admin RPC for the configured
// L2 network and returns the raw JSON response (the agglayer's full local balance tree for the network).
func queryAgglayerTokenBalance(ctx context.Context, cfg *Config) (json.RawMessage, error) {
	log.Infof("Querying %s (network %d)", cfg.Options.AgglayerAdminURL, cfg.L2NetworkID)
	if cfg.Options.AgglayerAdminToken != "" {
		log.Info("Using bearer token for agglayer admin authentication")
	}

	raw, err := singleRPCAuth(
		ctx, cfg.Options.AgglayerAdminURL,
		"admin_getTokenBalance",
		[]any{cfg.L2NetworkID, nil},
		defaultRetries,
		cfg.Options.AgglayerAdminToken,
	)
	if err != nil {
		return nil, fmt.Errorf("admin_getTokenBalance (network %d): %w", cfg.L2NetworkID, err)
	}
	return raw, nil
}

// runStepFOfflineLBT runs Step F without contacting the agglayer admin API
// (useAgglayerAdminToStepFCheck=false): it compares the LBT (Step 0) totals against the certificate
// bridge-exit sums per token. When no LBT data is available there is nothing to compare and the step
// is skipped with a benign all-match result.
func runStepFOfflineLBT(
	cfg *Config, certificate *agglayertypes.Certificate, lbtEntries []LBTEntry,
) (*StepFResult, error) {
	if len(lbtEntries) == 0 {
		log.Warn("STEP F skipped: useAgglayerAdminToStepFCheck=false and no LBT data available for the offline check")
		return &StepFResult{AllMatch: true}, nil
	}

	log.Info("useAgglayerAdminToStepFCheck=false — comparing LBT (step 0) vs certificate bridge exits (no agglayer query)")
	groups := groupBridgeExitsByToken(certificate)
	checks := compareCertificateToLBT(groups, lbtEntries, genesisPrefundWei(cfg))

	allMatch := true
	for _, c := range checks {
		if !c.Match {
			allMatch = false
			log.Warnf("❌ MISMATCH (network=%d addr=%s): lbt=%s  certificate=%s  (certificate−lbt=%s)",
				c.OriginNetwork, c.OriginTokenAddress, c.LBTAmount, c.CertificateAmount,
				amountDiff(c.CertificateAmount, c.LBTAmount))
			for i, e := range c.CertificateEntries {
				log.Infof("    ⚠️ [%d] dest_network=%d dest=%s amount=%s",
					i, e.DestinationNetwork, e.DestinationAddress, e.Amount)
			}
		} else {
			log.Infof("✅ (network=%d addr=%s): lbt=%s  certificate=%s",
				c.OriginNetwork, c.OriginTokenAddress, c.LBTAmount, c.CertificateAmount)
		}
	}
	if allMatch {
		log.Infof("All %d token balances match ✅ LBT = certificate", len(checks))
	}
	log.Info("STEP F complete (offline LBT check)")

	return finalizeStepFResult(cfg, certificate, checks, nil, allMatch)
}

// finalizeStepFResult assembles the StepFResult from the comparison checks, applying the
// ignoreBalanceMismatch policy: on a mismatch it either caps the certificate's bridge exits to each
// token's RemainingBalance (ignoreBalanceMismatch=true) or returns an error. raw is the agglayer admin
// response when available (nil for the offline LBT-only check).
func finalizeStepFResult(
	cfg *Config, certificate *agglayertypes.Certificate,
	checks []TokenBalanceCheck, raw json.RawMessage, allMatch bool,
) (*StepFResult, error) {
	result := &StepFResult{
		AllMatch:      allMatch,
		TokenBalances: raw,
		Checks:        checks,
	}
	if allMatch {
		return result, nil
	}
	if !cfg.Options.IgnoreBalanceMismatch {
		return result, fmt.Errorf("token balance mismatches detected (set options.ignoreBalanceMismatch=true to ignore)")
	}

	log.Warn("Balance mismatches detected — continuing anyway (ignoreBalanceMismatch=true)")
	for _, c := range checks {
		if !c.Match {
			log.Debugf("  ⚠️ check: network=%d addr=%s lbt=%s certificate=%s agglayer=%s match=%v",
				c.OriginNetwork, c.OriginTokenAddress, c.LBTAmount, c.CertificateAmount, c.AgglayerAmount, c.Match)
		}
	}
	capped := *certificate
	capped.BridgeExits = capCertificateExits(certificate.BridgeExits, checks, cfg.Options.CapMode)
	result.CappedCertificate = &capped
	log.Infof("🔧 Capped certificate: %d → %d bridge exits",
		len(certificate.BridgeExits), len(capped.BridgeExits))
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
// nativePrefund (nil when unset) is discounted from the native-token certificate sum before
// comparing (see discountGenesisPrefund). CertificateEntries is populated only on mismatch.
func compareTokenBalances(
	groups map[tokenKey][]*agglayertypes.BridgeExit,
	agglayerEntries []agglayerTokenEntry,
	lbtEntries []LBTEntry,
	nativePrefund *big.Int,
) []TokenBalanceCheck {
	agglayerMap := make(map[tokenKey]*big.Int, len(agglayerEntries))
	for _, e := range agglayerEntries {
		k := tokenKey{e.OriginNetwork, e.OriginTokenAddress}
		amount, ok := new(big.Int).SetString(e.Amount, decimalBase)
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
		amount, ok := new(big.Int).SetString(e.Balance, decimalBase)
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
		certAmt = discountGenesisPrefund(certAmt, k, nativePrefund)

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
			if agglAmt.Cmp(lbtAmt) <= 0 {
				check.RemainingBalance = new(big.Int).Set(agglAmt)
			} else {
				check.RemainingBalance = new(big.Int).Set(lbtAmt)
			}
		} else {
			check.Match = certAmt.Cmp(agglAmt) == 0
			check.RemainingBalance = new(big.Int).Set(agglAmt)
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

// compareCertificateToLBT builds a per-token comparison of the certificate bridge-exit sums against
// the LBT (Step 0) totals, without any agglayer data (used when useAgglayerAdminToStepFCheck=false).
// Match requires certificate sum == LBT total per token; AgglayerAmount is left empty. RemainingBalance
// is the LBT total, used as the cap budget when ignoreBalanceMismatch is set. nativePrefund (nil when
// unset) is discounted from the native-token certificate sum before comparing (see
// discountGenesisPrefund). CertificateEntries is populated only on mismatch.
func compareCertificateToLBT(
	groups map[tokenKey][]*agglayertypes.BridgeExit, lbtEntries []LBTEntry, nativePrefund *big.Int,
) []TokenBalanceCheck {
	lbtMap := make(map[tokenKey]*big.Int, len(lbtEntries))
	for _, e := range lbtEntries {
		k := tokenKey{e.OriginNetwork, e.OriginTokenAddress}
		amount, ok := new(big.Int).SetString(e.Balance, decimalBase)
		if !ok {
			log.Warnf("Could not parse LBT balance %q for token (network=%d addr=%s)",
				e.Balance, e.OriginNetwork, e.OriginTokenAddress.Hex())
			continue
		}
		lbtMap[k] = amount
	}

	seen := make(map[tokenKey]struct{}, len(groups)+len(lbtMap))
	for k := range groups {
		seen[k] = struct{}{}
	}
	for k := range lbtMap {
		seen[k] = struct{}{}
	}

	checks := make([]TokenBalanceCheck, 0, len(seen))
	for k := range seen {
		exits := groups[k]
		certAmt := new(big.Int)
		for _, e := range exits {
			certAmt.Add(certAmt, e.Amount)
		}
		certAmt = discountGenesisPrefund(certAmt, k, nativePrefund)

		lbtAmt := lbtMap[k]
		if lbtAmt == nil {
			lbtAmt = new(big.Int)
		}

		check := TokenBalanceCheck{
			OriginNetwork:      k.OriginNetwork,
			OriginTokenAddress: k.OriginTokenAddress.Hex(),
			LBTAmount:          lbtAmt.String(),
			CertificateAmount:  certAmt.String(),
			Match:              certAmt.Cmp(lbtAmt) == 0,
			RemainingBalance:   new(big.Int).Set(lbtAmt),
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

// capCertificateExits returns a new slice of bridge exits trimmed to stay within each
// token's RemainingBalance (= min(LBT, agglayer) from its TokenBalanceCheck).
//
// The mode selects the order in which each token's budget is allocated to its exits:
//   - CapModeByAppearance: exits are served in the order they appear.
//   - CapModeByAmount: exits are served smallest-amount first, so the small holders keep their full
//     amount and the largest ones are the first to be capped/dropped once the budget runs out.
//
// An exit that would exceed the remaining budget is capped to it; an exit with no budget left is
// dropped. Regardless of mode, the surviving exits are emitted in their original order.
func capCertificateExits(
	exits []*agglayertypes.BridgeExit, checks []TokenBalanceCheck, mode string,
) []*agglayertypes.BridgeExit {
	remaining := make(map[tokenKey]*big.Int, len(checks))
	for _, c := range checks {
		if c.RemainingBalance == nil {
			continue
		}
		k := tokenKey{c.OriginNetwork, common.HexToAddress(c.OriginTokenAddress)}
		remaining[k] = new(big.Int).Set(c.RemainingBalance)
	}

	// capExit carries the per-exit outcome computed during budget allocation; nil capTo with
	// drop=false means "keep the exit unchanged".
	type capExit struct {
		drop  bool
		capTo *big.Int
	}
	outcomes := make([]capExit, len(exits))

	for _, idx := range capAllocationOrder(exits, mode) {
		e := exits[idx]
		if e == nil || e.TokenInfo == nil || e.Amount == nil {
			continue
		}
		k := tokenKey{e.TokenInfo.OriginNetwork, e.TokenInfo.OriginTokenAddress}
		rem, hasCap := remaining[k]
		if !hasCap {
			continue
		}
		if rem.Sign() == 0 {
			outcomes[idx].drop = true
			continue
		}
		if e.Amount.Cmp(rem) <= 0 {
			rem.Sub(rem, e.Amount)
		} else {
			outcomes[idx].capTo = new(big.Int).Set(rem)
			rem.SetInt64(0)
		}
	}

	result := make([]*agglayertypes.BridgeExit, 0, len(exits))
	for idx, e := range exits {
		switch {
		case outcomes[idx].drop:
			k := tokenKey{e.TokenInfo.OriginNetwork, e.TokenInfo.OriginTokenAddress}
			log.Debugf("🔧 Drop bridge exit (network=%d addr=%s amount=%s): no budget left",
				k.OriginNetwork, k.OriginTokenAddress, e.Amount)
		case outcomes[idx].capTo != nil:
			k := tokenKey{e.TokenInfo.OriginNetwork, e.TokenInfo.OriginTokenAddress}
			log.Infof("🔧 Cap bridge exit (network=%d addr=%s): %s → %s",
				k.OriginNetwork, k.OriginTokenAddress, e.Amount, outcomes[idx].capTo)
			result = append(result, capExitCopy(e, outcomes[idx].capTo))
		default:
			result = append(result, e)
		}
	}
	return result
}

// capAllocationOrder returns the exit indices in the order their token budget should be allocated.
// For CapModeByAmount it is a stable sort by ascending amount, so the smallest exits are served
// first and the largest ones are the first to run out of budget (capped/dropped). Exits without a
// comparable amount are pushed to the end (they never consume budget); every other mode uses
// appearance order.
func capAllocationOrder(exits []*agglayertypes.BridgeExit, mode string) []int {
	order := make([]int, len(exits))
	for i := range order {
		order[i] = i
	}
	if mode != CapModeByAmount {
		return order
	}
	sort.SliceStable(order, func(a, b int) bool {
		ea, eb := exits[order[a]], exits[order[b]]
		if ea == nil || ea.Amount == nil {
			return false
		}
		if eb == nil || eb.Amount == nil {
			return true
		}
		return ea.Amount.Cmp(eb.Amount) < 0
	})
	return order
}

// capExitCopy returns a deep copy of e with its amount replaced by capTo, so the original exit
// (still referenced by the uncapped certificate) is left untouched.
func capExitCopy(e *agglayertypes.BridgeExit, capTo *big.Int) *agglayertypes.BridgeExit {
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
	exitCopy.Amount = new(big.Int).Set(capTo)
	return &exitCopy
}
