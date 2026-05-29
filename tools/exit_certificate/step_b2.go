package exit_certificate

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"

	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
)

// RunStepB2 probes the contract addresses from Step B1 for the ERC-20 interface.
// For each contract that responds to totalSupply() with a non-zero value it checks
// whether it holds any of the tracked wrapped tokens:
//   - holds at least one → DetectedERC20 (relevant to the certificate)
//   - holds none         → DiscardedERC20 (no tracked value locked inside)
//
// RPC execution errors on totalSupply() calls are silently treated as "not ERC-20".
func RunStepB2(
	ctx context.Context, cfg *Config, targetBlock uint64,
	contractAddrs, eoaAddrs []common.Address,
	wrappedTokens []WrappedToken,
) (*StepB2Result, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP B2 — ERC-20 detection in contracts")
	log.Info("═══════════════════════════════════════════")

	if len(contractAddrs) == 0 {
		log.Info("No contract addresses to probe")
		log.Info("STEP B2 complete: 0 ERC-20 contracts detected")
		return &StepB2Result{}, nil
	}

	blockTag := toBlockTag(targetBlock)
	batchSize := cfg.Options.RPCBatchSize
	concurrency := cfg.Options.ConcurrencyLimit

	log.Infof("Probing %d contracts for ERC-20 totalSupply()...", len(contractAddrs))
	erc20Supplies := detectERC20Contracts(ctx, cfg.L2RPCURL, contractAddrs, blockTag, concurrency)
	log.Infof("%d/%d contracts responded to ERC20 totalSupply()", len(erc20Supplies), len(contractAddrs))

	if len(erc20Supplies) == 0 {
		log.Info("STEP B2 complete: 0 ERC-20 contracts detected")
		return &StepB2Result{}, nil
	}

	jobs := make([]erc20ProbeJob, 0, len(erc20Supplies))
	for addr, info := range erc20Supplies {
		jobs = append(jobs, erc20ProbeJob{addr: addr, info: info})
	}

	detected := make([]DetectedERC20, 0, len(erc20Supplies))
	var discarded []DiscardedERC20

	err := runWorkerPool(
		ctx, jobs, concurrency,
		func(j erc20ProbeJob) (erc20ProbeResult, error) {
			tokenLabel := j.info.name
			if tokenLabel == "" {
				tokenLabel = j.info.symbol
			}
			wrappedBalances, err := checkWrappedTokenBalances(
				ctx, cfg.L2RPCURL, j.addr, wrappedTokens, blockTag, batchSize, concurrency, tokenLabel, true,
			)
			if err != nil {
				return erc20ProbeResult{}, fmt.Errorf("check wrapped balances for ERC-20 %s: %w", j.addr.Hex(), err)
			}

			if len(wrappedBalances) == 0 {
				log.Debugf("  discarded %s %q (%s) (no tracked wrapped tokens held)", j.addr.Hex(), j.info.name, j.info.symbol)
				return erc20ProbeResult{discarded: &DiscardedERC20{
					Address:     j.addr,
					Name:        j.info.name,
					Symbol:      j.info.symbol,
					TotalSupply: j.info.supply.String(),
				}}, nil
			}

			log.Infof("⚠  ERC-20 %s %q (%s) locks tracked wrapped tokens:", j.addr.Hex(), j.info.name, j.info.symbol)
			for _, wb := range wrappedBalances {
				log.Infof("     → %s : %s", wb.Token.WrappedTokenAddress.Hex(), wb.Balance)
			}

			return erc20ProbeResult{detected: &DetectedERC20{
				Address:              j.addr,
				Name:                 j.info.name,
				Symbol:               j.info.symbol,
				TotalSupply:          j.info.supply.String(),
				WrappedTokenBalances: wrappedBalances,
			}}, nil
		},
		func(r erc20ProbeResult) {
			if r.detected != nil {
				detected = append(detected, *r.detected)
			} else {
				discarded = append(discarded, *r.discarded)
			}
		},
		"step_b2: ERC-20 probe",
	)
	if err != nil {
		return nil, err
	}

	log.Infof("STEP B2 complete: %d relevant ERC-20(s), %d discarded", len(detected), len(discarded))

	return &StepB2Result{
		DetectedERC20s:  detected,
		DiscardedERC20s: discarded,
	}, nil
}

// checkWrappedTokenBalances calls balanceOf(contractAddr) on each wrapped token contract
// and eth_getBalance for native ETH. Returns only entries where the balance is > 0.
// ETH is represented as the zero-address token (OriginNetwork=0, OriginTokenAddress=0x0,
// WrappedTokenAddress=0x0). Pass silent=true to suppress progress logs (recommended when
// called concurrently).
func checkWrappedTokenBalances(
	ctx context.Context, rpcURL string,
	contractAddr common.Address, wrappedTokens []WrappedToken,
	blockTag string, batchSize, concurrency int, tokenLabel string, silent bool,
) ([]WrappedTokenBalance, error) {
	// calls = [balanceOf(t0), ..., balanceOf(tN), eth_getBalance]
	calls := make([]RPCCall, len(wrappedTokens)+1)
	for i, t := range wrappedTokens {
		calls[i] = RPCCall{
			Method: "eth_call",
			Params: []any{
				map[string]string{
					"to":   t.WrappedTokenAddress.Hex(),
					"data": encodeBalanceOf(contractAddr),
				},
				blockTag,
			},
		}
	}
	calls[len(wrappedTokens)] = RPCCall{
		Method: "eth_getBalance",
		Params: []any{contractAddr.Hex(), blockTag},
	}

	label := fmt.Sprintf("step_b2: %-20.20s wrappedBalances", tokenLabel)
	if silent {
		label = ""
	}
	results, err := concurrentBatchRPC(ctx, rpcURL, calls, batchSize, concurrency, label)
	if err != nil {
		return nil, err
	}

	var balances []WrappedTokenBalance
	for i, result := range results[:len(wrappedTokens)] {
		bal := unmarshalHexBigInt(result)
		if bal != nil && bal.Sign() > 0 {
			balances = append(balances, WrappedTokenBalance{
				Token:   wrappedTokens[i],
				Balance: bal.String(),
			})
		}
	}
	if ethBal := unmarshalHexBigInt(results[len(wrappedTokens)]); ethBal != nil && ethBal.Sign() > 0 {
		balances = append(balances, WrappedTokenBalance{
			Token:   WrappedToken{}, // zero address = native ETH
			Balance: ethBal.String(),
		})
	}
	return balances, nil
}

// probeProgressPct is the granularity for progress logging in detectERC20Contracts.
const probeProgressPct = 10

// nameSelector is the function selector for ERC-20 name().
const nameSelector = "0x06fdde03"

// symbolSelector is the function selector for ERC-20 symbol().
const symbolSelector = "0x95d89b41"

// erc20Info holds the data fetched per contract during the ERC-20 probe.
type erc20Info struct {
	supply *big.Int
	name   string
	symbol string
}

type erc20ProbeJob struct {
	addr common.Address
	info erc20Info
}

type erc20ProbeResult struct {
	detected  *DetectedERC20
	discarded *DiscardedERC20
}

// detectERC20Contracts calls totalSupply() on each contract in parallel.
// For contracts with supply > 0 it also fetches name().
// RPC execution errors (e.g. reverts on non-ERC-20 contracts) are silently ignored.
func detectERC20Contracts(
	ctx context.Context, rpcURL string, contracts []common.Address,
	blockTag string, concurrency int,
) map[common.Address]erc20Info {
	type result struct {
		addr common.Address
		info erc20Info
	}

	total := len(contracts)
	resultCh := make(chan result, total)
	sem := make(chan struct{}, concurrency)

	var done, detected atomic.Int32
	var wg sync.WaitGroup
	for _, addr := range contracts {
		wg.Add(1)
		sem <- struct{}{}
		go func(a common.Address) {
			defer wg.Done()
			defer func() { <-sem }()

			raw, err := singleRPC(ctx, rpcURL, "eth_call", []any{
				map[string]string{"to": a.Hex(), "data": totalSupplySelector},
				blockTag,
			}, defaultRetries)

			var info erc20Info
			if err == nil {
				info.supply = unmarshalHexBigInt(raw)
			}

			if info.supply != nil && info.supply.Sign() > 0 {
				// Verify balanceOf(address(0)) succeeds to confirm the ERC-20 interface.
				// Contracts that happen to match the totalSupply() selector but are not
				// real ERC-20s will revert here.
				_, balErr := singleRPC(ctx, rpcURL, "eth_call", []any{
					map[string]string{"to": a.Hex(), "data": encodeBalanceOf(common.Address{})},
					blockTag,
				}, defaultRetries)
				if balErr != nil {
					info.supply = nil
				}
			}

			if info.supply != nil && info.supply.Sign() > 0 {
				detected.Add(1)
				nameRaw, nameErr := singleRPC(ctx, rpcURL, "eth_call", []any{
					map[string]string{"to": a.Hex(), "data": nameSelector},
					blockTag,
				}, defaultRetries)
				if nameErr == nil {
					var nameHex string
					if json.Unmarshal(nameRaw, &nameHex) == nil {
						info.name = decodeABIString(common.FromHex(nameHex))
					}
				}

				symbolRaw, symbolErr := singleRPC(ctx, rpcURL, "eth_call", []any{
					map[string]string{"to": a.Hex(), "data": symbolSelector},
					blockTag,
				}, defaultRetries)
				if symbolErr == nil {
					var symbolHex string
					if json.Unmarshal(symbolRaw, &symbolHex) == nil {
						info.symbol = decodeABIString(common.FromHex(symbolHex))
					}
				}
			}

			n := int(done.Add(1))
			prevPct := (n - 1) * probeProgressPct / total
			currPct := n * probeProgressPct / total
			if currPct > prevPct || n == total {
				log.Infof("  B2 ERC-20 probe: %d/%d (%d%%) — %d ERC-20(s) detected",
					n, total, currPct*probeProgressPct, detected.Load())
			}

			resultCh <- result{addr: a, info: info}
		}(addr)
	}

	wg.Wait()
	close(resultCh)

	erc20s := make(map[common.Address]erc20Info)
	for r := range resultCh {
		if r.info.supply != nil && r.info.supply.Sign() > 0 {
			erc20s[r.addr] = r.info
		}
	}
	return erc20s
}
