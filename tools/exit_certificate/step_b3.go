package exit_certificate

import (
	"bytes"
	"context"
	"fmt"
	"sort"

	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
)

// RunStepB3 fetches the per-EOA token balance for each contract listed in
// cfg.Options.ExtraERC20Contracts. For each address, balanceOf is called for
// every EOA collected in Step A. Collateral info (tracked wrapped tokens held)
// is attached from the B2 detected list when available.
func RunStepB3(
	ctx context.Context, cfg *Config, targetBlock uint64,
	eoaAddrs []common.Address,
	b2Result *StepB2Result,
) (*StepB3Result, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP B3 — Extra ERC-20 holder decomposition")
	log.Info("═══════════════════════════════════════════")

	if len(cfg.Options.ExtraERC20Contracts) == 0 {
		log.Info("No extra ERC-20 contracts configured — STEP B3 skipped")
		return &StepB3Result{}, nil
	}

	blockTag := toBlockTag(targetBlock)
	batchSize := cfg.Options.RPCBatchSize
	concurrency := cfg.Options.ConcurrencyLimit

	// Index all B2 detected contracts by address to attach collateral info.
	b2Detected := make(map[common.Address]*DetectedERC20, len(b2Result.DetectedERC20s))
	for i := range b2Result.DetectedERC20s {
		d := &b2Result.DetectedERC20s[i]
		b2Detected[d.Address] = d
	}

	log.Infof("Processing %d extra ERC-20 contract(s) against %d EOA(s)",
		len(cfg.Options.ExtraERC20Contracts), len(eoaAddrs))

	breakdowns := make([]ERC20HolderBreakdown, 0, len(cfg.Options.ExtraERC20Contracts))
	for _, addr := range cfg.Options.ExtraERC20Contracts {
		log.Infof("  %s — fetching balances for %d EOA(s)...", addr.Hex(), len(eoaAddrs))
		holderBalances, err := fetchTokenBalances(
			ctx, cfg.L2RPCURL, addr, eoaAddrs, blockTag, batchSize, concurrency,
		)
		if err != nil {
			return nil, fmt.Errorf("fetchTokenBalances for ERC-20 %s: %w", addr.Hex(), err)
		}

		holders := make([]ERC20Holder, 0, len(holderBalances))
		for holderAddr, bal := range holderBalances {
			holders = append(holders, ERC20Holder{Address: holderAddr, Balance: bal.String()})
		}
		// holderBalances is a map, so the range order above is random per run; sort by address
		// (same pattern as Step A) so the holders file is reproducible across runs (AET-33).
		sort.Slice(holders, func(i, j int) bool {
			return bytes.Compare(holders[i].Address.Bytes(), holders[j].Address.Bytes()) < 0
		})
		log.Infof("  %s — %d holder(s) found", addr.Hex(), len(holders))

		breakdowns = append(breakdowns, ERC20HolderBreakdown{
			Address:  addr,
			Holders:  holders,
			Detected: b2Detected[addr], // nil when not in B2 detected list
		})
	}

	log.Infof("STEP B3 complete: %d contract(s) processed", len(breakdowns))
	return &StepB3Result{Breakdowns: breakdowns}, nil
}
