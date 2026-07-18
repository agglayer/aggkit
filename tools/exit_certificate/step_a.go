package exit_certificate

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
)

const (
	// accountRangePageSize is the number of accounts requested per debug_accountRange page.
	// Both geth and erigon/cdk-erigon cap a single response at 256 (paginating via the next cursor),
	// so requesting 256 yields the same throughput as a larger value while keeping each call cheap.
	// Requesting more than the cap risks exceeding the HTTP timeout on a slow/loaded node (observed
	// on cdk-erigon: a 5000-account request timed out, whereas a 256 request returned), so we ask
	// for exactly the cap.
	accountRangePageSize = 256

	// accountRangeProgressInterval controls how often progress is logged during the state dump.
	accountRangeProgressInterval = 50

	// transferEventSignature is keccak256("Transfer(address,address,uint256)").
	transferEventSignature = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"

	// transferTopicFrom and transferTopicTo are the indexed-topic positions of a Transfer event.
	transferTopicFrom = 1
	transferTopicTo   = 2
)

// transferTopic is the topic[0] of an ERC-20 Transfer event.
var transferTopic = common.HexToHash(transferEventSignature)

// maxAccountRangePages bounds the pagination loop as a safety valve against a node that never
// returns an empty "next" cursor. At accountRangePageSize=256 this allows ~6.4B accounts — far
// beyond any realistic chain state — before the dump aborts with an error. It is a var (not a
// const) so tests can shrink it to exercise the truncation guard.
var maxAccountRangePages = 25_000_000

// accountRangeDialect distinguishes the two incompatible debug_accountRange ABIs in the wild.
//
//   - geth/op-geth: AccountRange(block, start hexutil.Bytes, maxResults, nocode, nostorage,
//     incompletes) — `start` is 0x-hex and there are 6 args.
//   - erigon/cdk-erigon: AccountRange(block, start []byte, maxResults, excludeCode, excludeStorage)
//     — `start` is base64 (Go []byte) and there are 5 args.
//
// Both return accounts keyed by address and a base64 `next` cursor.
type accountRangeDialect int

const (
	dialectUnknown accountRangeDialect = iota
	dialectGeth
	dialectErigon
)

func (d accountRangeDialect) String() string {
	switch d {
	case dialectGeth:
		return "geth"
	case dialectErigon:
		return "erigon"
	default:
		return "undetected"
	}
}

// RunStepA collects every value-holding address at targetBlock without replaying the full
// transaction history via debug_traceTransaction.
//
// It always combines two cheap sources and merges them, each covering the other's blind spot:
//  1. a state-trie dump at targetBlock (debug_accountRange) — every account with non-zero
//     balance/nonce/code (all native-ETH holders and every contract), and
//  2. Transfer event logs (eth_getLogs) per wrapped token and per extra ERC-20 contract
//     (cfg.Options.ExtraERC20Contracts) — every token holder, including token-only EOAs that never
//     appear in a state dump or a trace (an ERC-20 transfer only mutates the token contract's
//     storage, so the recipient account itself is never "touched"). Extra ERC-20 holders must be
//     discovered here so Step B3 can probe their balances.
func RunStepA(
	ctx context.Context, cfg *Config, targetBlock uint64, wrappedTokens []WrappedToken,
) (*StepAResult, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP A — Collect addresses (state dump + Transfer logs)")
	log.Info("═══════════════════════════════════════════")

	if targetBlock < cfg.Options.L2StartBlock {
		return nil, fmt.Errorf("targetBlock %d is before l2StartBlock %d", targetBlock, cfg.Options.L2StartBlock)
	}

	finalAddrs := make(map[common.Address]struct{})
	add := func(addrs []common.Address) {
		for _, a := range addrs {
			finalAddrs[a] = struct{}{}
		}
	}

	accounts, err := collectAccountsViaStateDump(ctx, cfg, targetBlock)
	if err != nil {
		return nil, fmt.Errorf("state dump: %w", err)
	}
	add(accounts)
	holders, err := collectTokenHoldersViaLogs(ctx, cfg, targetBlock, wrappedTokens)
	if err != nil {
		return nil, fmt.Errorf("token holders via logs: %w", err)
	}
	add(holders)

	// The zero address is always included: it can hold value like any other account (a plain
	// transfer(0x0, amount) is not a burn — the tokens stay in totalSupply — and native ETH can be
	// sent there too, including genesis allocs). Dropping it would leave that value uncovered by
	// the certificate and the per-token totals would no longer reconcile with the LBT. It is added
	// unconditionally rather than trusting discovery: the state dump can miss it (no preimage for
	// the zero key) and the Transfer-log scan only surfaces it when a mint/burn happened, which
	// would make the Step B genesis-preload detection depend on unrelated token activity.
	finalAddrs[common.Address{}] = struct{}{}

	addresses := make([]common.Address, 0, len(finalAddrs))
	for addr := range finalAddrs {
		addresses = append(addresses, addr)
	}
	sort.Slice(addresses, func(i, j int) bool {
		return strings.ToLower(addresses[i].Hex()) < strings.ToLower(addresses[j].Hex())
	})

	log.Infof("STEP A complete: %d unique addresses", len(addresses))
	return &StepAResult{Addresses: addresses, WrappedTokens: wrappedTokens}, nil
}

// collectAccountsViaStateDump walks the entire account trie at targetBlock via paginated
// debug_accountRange calls and returns every account address. This captures all native-ETH
// holders and every contract in O(#accounts), without replaying transaction history.
// The node's debug_accountRange dialect (geth vs erigon) is auto-detected on the first page.
func collectAccountsViaStateDump(ctx context.Context, cfg *Config, targetBlock uint64) ([]common.Address, error) {
	blockTag := toBlockTag(targetBlock)
	log.Infof("Dumping account trie at block %d via debug_accountRange (page size %d)...",
		targetBlock, accountRangePageSize)

	dialect, res, err := firstAccountRangePage(ctx, cfg.L2RPCURL, blockTag)
	if err != nil {
		return nil, err
	}
	log.Infof("debug_accountRange dialect: %s", dialect)

	addrSet := make(map[common.Address]struct{})
	var start []byte
	var frontier []byte // highest raw account key seen so far (monotonic), for re-seeking
	stepStart := time.Now()

	completed := false
	for page := 0; page < maxAccountRangePages; page++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		for key, acc := range res.Accounts {
			if addr, ok := accountAddress(key, acc.Address); ok {
				addrSet[addr] = struct{}{}
			}
			if kb, ok := accountKeyBytes(key); ok && (frontier == nil || bytes.Compare(kb, frontier) > 0) {
				frontier = kb
			}
		}

		next, err := decodeNextKey(res.Next)
		if err != nil {
			return nil, fmt.Errorf("decode accountRange next cursor: %w", err)
		}

		// Decide the next page's start. This cdk-erigon endpoint (behind a load balancer)
		// intermittently returns a valid HTTP 200 page with an empty "next" cursor BEFORE the trie
		// end, which naive cursor-following mistakes for completion — silently dropping accounts
		// (observed: 735k of 899k). For the erigon dialect we therefore never trust an empty cursor
		// as "done": we re-seek strictly past the highest key seen and only stop once a re-seek
		// genuinely returns no new account (retried a few times to ride out transient empty pages).
		// The geth dialect (a real archive node) keeps the plain cursor semantics.
		if len(next) > 0 {
			start = next
		} else if dialect == dialectErigon && frontier != nil {
			reseek, done, rerr := reseekPastFrontier(ctx, cfg.L2RPCURL, blockTag, frontier, dialect)
			if rerr != nil {
				return nil, rerr
			}
			if done {
				completed = true
				break
			}
			res = reseek
			continue
		} else {
			completed = true
			break
		}

		if (page+1)%accountRangeProgressInterval == 0 {
			log.Infof("  debug_accountRange: %d accounts so far (%.0fs)",
				len(addrSet), time.Since(stepStart).Seconds())
		}

		res, err = debugAccountRange(ctx, cfg.L2RPCURL, blockTag, start, accountRangePageSize, dialect)
		if err != nil {
			return nil, err
		}
	}

	// Fail loudly instead of returning a silently-truncated set: if the page cap was hit while the
	// node was still returning accounts, later steps would run on incomplete data and under-report
	// balances.
	if !completed {
		return nil, fmt.Errorf("debug_accountRange did not complete after %d pages (node kept "+
			"returning accounts); aborting to avoid a truncated address set", maxAccountRangePages)
	}

	// Guard against a node that returns an empty dump without an RPC error — e.g. a stock geth
	// archive node without address preimages, where incompletes=false skips every account. A real
	// chain at any block always has accounts (at minimum the bridge), so 0 here means the dump is
	// unusable and Step A fails loudly instead of silently omitting native holders and contracts.
	if len(addrSet) == 0 {
		return nil, fmt.Errorf("debug_accountRange returned 0 accounts at %s (node may lack address "+
			"preimages); cannot use the state dump", blockTag)
	}

	log.Infof("State dump complete: %d accounts", len(addrSet))
	addresses := make([]common.Address, 0, len(addrSet))
	for addr := range addrSet {
		addresses = append(addresses, addr)
	}
	return addresses, nil
}

// accountKeyBytes decodes a debug_accountRange map key (a 0x-hex account key — the address for the
// erigon dialect) into its raw bytes, used to track the pagination frontier when re-seeking past an
// intermittently-truncated page.
func accountKeyBytes(key string) ([]byte, bool) {
	s := strings.TrimPrefix(key, "0x")
	s = strings.TrimPrefix(s, "0X")
	if len(s) == 0 || len(s)%2 != 0 {
		return nil, false
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, false
	}
	return b, true
}

// incrementKey returns key+1 (big-endian). On overflow (an all-0xff key) it returns an all-0xff key
// of the same length, so the follow-up seek lands at the very end of the address space.
func incrementKey(key []byte) []byte {
	out := make([]byte, len(key))
	copy(out, key)
	for i := len(out) - 1; i >= 0; i-- {
		out[i]++
		if out[i] != 0 {
			return out
		}
	}
	for i := range out {
		out[i] = 0xff
	}
	return out
}

// reseekPastFrontier fetches the accounts strictly after frontier. It returns (page, done=false) when
// new accounts remain, or (nil, done=true) once a re-seek yields no new account. Empty results are
// retried a few times to ride out the endpoint's intermittent truncated pages (a valid HTTP 200
// response with no accounts / an empty cursor), which would otherwise be mistaken for the trie end.
func reseekPastFrontier(
	ctx context.Context, rpcURL, blockTag string, frontier []byte, dialect accountRangeDialect,
) (*accountRangeResult, bool, error) {
	const emptyRetries = 3
	start := incrementKey(frontier)
	for attempt := 0; attempt < emptyRetries; attempt++ {
		res, err := debugAccountRange(ctx, rpcURL, blockTag, start, accountRangePageSize, dialect)
		if err != nil {
			return nil, false, err
		}
		for key := range res.Accounts {
			if kb, ok := accountKeyBytes(key); ok && bytes.Compare(kb, frontier) > 0 {
				return res, false, nil
			}
		}
		time.Sleep(500 * time.Millisecond) //nolint:mnd // brief pause before retrying an empty page
	}
	return nil, true, nil
}

// firstAccountRangePage fetches the first page of the state dump, auto-detecting the node's
// debug_accountRange dialect by trying erigon (the cdk-erigon form) first, then geth. It returns
// the detected dialect so the caller can paginate with the same encoding.
func firstAccountRangePage(
	ctx context.Context, rpcURL, blockTag string,
) (accountRangeDialect, *accountRangeResult, error) {
	start := make([]byte, common.HashLength) // 32 zero bytes → start at the beginning of the trie
	errs := make([]string, 0, 2)             //nolint:mnd // geth + erigon
	for _, d := range []accountRangeDialect{dialectErigon, dialectGeth} {
		res, err := debugAccountRange(ctx, rpcURL, blockTag, start, accountRangePageSize, d)
		if err == nil {
			return d, res, nil
		}
		errs = append(errs, fmt.Sprintf("%s: %v", d, err))
	}
	return dialectUnknown, nil, fmt.Errorf("debug_accountRange not supported (%s)", strings.Join(errs, "; "))
}

// accountRangeResult is the subset of debug_accountRange's response we consume.
type accountRangeResult struct {
	Accounts map[string]accountRangeEntry `json:"accounts"`
	// Next is the cursor for the next page. Both geth and erigon marshal it from a Go []byte, so it
	// arrives base64-encoded; some clients return a 0x-hex string. decodeNextKey handles both.
	Next string `json:"next"`
}

type accountRangeEntry struct {
	Address *string `json:"address"`
}

// debugAccountRange fetches one page of accounts from the state trie at blockTag, starting at the
// given trie key, encoding the request for the given client dialect. Code and storage are excluded
// to keep responses small; the geth form additionally passes incompletes=false to skip accounts
// whose address preimage is unknown.
func debugAccountRange(
	ctx context.Context, rpcURL, blockTag string, start []byte, maxResults int, dialect accountRangeDialect,
) (*accountRangeResult, error) {
	result, err := singleRPC(ctx, rpcURL, "debug_accountRange",
		accountRangeParams(blockTag, start, maxResults, dialect), defaultRetries)
	if err != nil {
		return nil, fmt.Errorf("debug_accountRange at %s: %w", blockTag, err)
	}
	var res accountRangeResult
	if err := json.Unmarshal(result, &res); err != nil {
		return nil, fmt.Errorf("unmarshal debug_accountRange response: %w", err)
	}
	return &res, nil
}

// accountRangeParams builds the debug_accountRange parameter list for the given dialect.
func accountRangeParams(blockTag string, start []byte, maxResults int, dialect accountRangeDialect) []any {
	if dialect == dialectErigon {
		// erigon: [block, start(base64), maxResults, excludeCode, excludeStorage]
		return []any{blockTag, base64.StdEncoding.EncodeToString(start), maxResults, true, true}
	}
	// geth: [block, start(0x-hex), maxResults, nocode, nostorage, incompletes]
	return []any{blockTag, "0x" + hex.EncodeToString(start), maxResults, true, true, false}
}

// accountAddress resolves an address from a debug_accountRange entry. The map key is the account
// address (common.Address) when the node has the preimage; the inner "address" field is used as a
// fallback. Returns ok=false when neither yields a valid address.
func accountAddress(key string, innerAddr *string) (common.Address, bool) {
	if innerAddr != nil && common.IsHexAddress(*innerAddr) {
		return common.HexToAddress(*innerAddr), true
	}
	if common.IsHexAddress(key) {
		return common.HexToAddress(key), true
	}
	return common.Address{}, false
}

// decodeNextKey decodes the debug_accountRange "next" cursor. Geth encodes the Go []byte as
// base64; other clients may return a 0x-hex string or an empty value. An empty or all-zero result
// means the dump is complete.
func decodeNextKey(next string) ([]byte, error) {
	if next == "" {
		return nil, nil
	}
	var raw []byte
	if strings.HasPrefix(next, "0x") || strings.HasPrefix(next, "0X") {
		raw = common.FromHex(next)
	} else {
		decoded, err := base64.StdEncoding.DecodeString(next)
		if err != nil {
			return nil, fmt.Errorf("base64 decode %q: %w", next, err)
		}
		raw = decoded
	}
	if allZero(raw) {
		return nil, nil
	}
	return raw, nil
}

// allZero reports whether b is empty or contains only zero bytes.
func allZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

// collectTokenHoldersViaLogs discovers every token holder by scanning Transfer event logs across
// [0, targetBlock] for each wrapped token and each extra ERC-20 contract from
// cfg.Options.ExtraERC20Contracts. Both the indexed `from` and `to` fields are collected,
// capturing token-only EOAs that never appear in a state dump or trace. The extra ERC-20s are
// scanned here — not in Step B3 — because B3 only probes balanceOf against Step A's address set:
// a passive holder of an extra token would otherwise never be discovered and their share of the
// collateral would flow to exitAddress instead.
//
// The scan deliberately starts at block 0 rather than l2StartBlock: a passive holder may have
// received a token before l2StartBlock and still hold it at targetBlock. Such token-only
// EOAs have no nonce/balance/code, so the state dump cannot include them either — the Transfer-log
// scan is the only source that surfaces them, and skipping early blocks would silently drop them.
func collectTokenHoldersViaLogs(
	ctx context.Context, cfg *Config, targetBlock uint64, wrappedTokens []WrappedToken,
) ([]common.Address, error) {
	tokens := make([]common.Address, 0, len(wrappedTokens)+len(cfg.Options.ExtraERC20Contracts))
	seen := make(map[common.Address]struct{}, cap(tokens))
	addToken := func(addr common.Address) {
		if _, ok := seen[addr]; ok {
			return
		}
		seen[addr] = struct{}{}
		tokens = append(tokens, addr)
	}
	for _, tok := range wrappedTokens {
		addToken(tok.WrappedTokenAddress)
	}
	for _, addr := range cfg.Options.ExtraERC20Contracts {
		addToken(addr)
	}

	if len(tokens) == 0 {
		log.Info("No wrapped tokens or extra ERC-20 contracts provided; skipping Transfer-log holder discovery")
		return nil, nil
	}

	blockRange := uint64(cfg.Options.BlockRange)
	if blockRange == 0 {
		blockRange = defaultBlockRange
	}
	const start = uint64(0)

	type logJob struct {
		token    common.Address
		from, to uint64
	}
	var jobs []logJob
	for _, token := range tokens {
		for from := start; from <= targetBlock; from += blockRange {
			to := min(from+blockRange-1, targetBlock)
			jobs = append(jobs, logJob{token: token, from: from, to: to})
		}
	}

	log.Infof("Scanning Transfer logs for %d tokens (%d wrapped + %d extra ERC-20, deduplicated) "+
		"over blocks %d→%d (%d ranges, concurrency=%d)...",
		len(tokens), len(wrappedTokens), len(cfg.Options.ExtraERC20Contracts),
		start, targetBlock, len(jobs), cfg.Options.ConcurrencyLimit)

	addrSet := make(map[common.Address]struct{})
	err := runWorkerPool(
		ctx, jobs, cfg.Options.ConcurrencyLimit,
		func(j logJob) ([]common.Address, error) {
			return fetchTransferHoldersInRange(ctx, cfg.L2RPCURL, j.token, j.from, j.to)
		},
		func(addrs []common.Address) {
			for _, a := range addrs {
				addrSet[a] = struct{}{}
			}
		},
		"TransferLogs",
	)
	if err != nil {
		return nil, fmt.Errorf("scan Transfer logs: %w", err)
	}

	log.Infof("Transfer-log scan complete: %d unique holder addresses", len(addrSet))
	addresses := make([]common.Address, 0, len(addrSet))
	for addr := range addrSet {
		addresses = append(addresses, addr)
	}
	return addresses, nil
}

// fetchTransferHoldersInRange returns the `from` and `to` addresses of every Transfer event
// emitted by token within [fromBlock, toBlock].
func fetchTransferHoldersInRange(
	ctx context.Context, rpcURL string, token common.Address, fromBlock, toBlock uint64,
) ([]common.Address, error) {
	result, err := singleRPC(ctx, rpcURL, "eth_getLogs", []any{
		map[string]any{
			"address":   token.Hex(),
			"topics":    []string{transferTopic.Hex()},
			"fromBlock": toBlockTag(fromBlock),
			"toBlock":   toBlockTag(toBlock),
		},
	}, defaultRetries)
	if err != nil {
		return nil, err
	}
	var logs []struct {
		Topics []string `json:"topics"`
	}
	if err := json.Unmarshal(result, &logs); err != nil {
		return nil, fmt.Errorf("unmarshal Transfer logs: %w", err)
	}

	addrs := make([]common.Address, 0, len(logs)*2) //nolint:mnd // from + to per log
	for _, lg := range logs {
		// topics[0] is the event signature; topics[1]=from, topics[2]=to (both indexed).
		// The zero address is kept like any other holder: tokens transferred to 0x0 remain in
		// totalSupply, so the certificate must cover them for the LBT to reconcile.
		for _, pos := range []int{transferTopicFrom, transferTopicTo} {
			if pos >= len(lg.Topics) {
				continue
			}
			addrs = append(addrs, common.HexToAddress(lg.Topics[pos]))
		}
	}
	return addrs, nil
}
