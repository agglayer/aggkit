// Package finality answers one question for the bridge tracker and the proxy: is this bridge /
// claim final, i.e. can it no longer be removed by a reorg of its network?
//
// The aggkit bridge service reports it per entry as block_finalized (a *bool: true, false or
// omitted, see bridgeservice/types.Bridge / Claim). Omitted means the bridge service could not
// tell (older than agglayer/aggkit#1921, block stored without hash, syncer unavailable, reorg in
// progress...). Resolver turns that tri-state into a plain bool following these rules:
//
//  1. An explicit true / false reported by the bridge service always wins: the receipt fallback is
//     never consulted when the value is set.
//  2. Omitted means NOT final, unless the fallback below proves otherwise.
//  3. Monotonic: once an entry has been seen final it stays final, even if a later answer (another
//     bridge-service instance, a lagging one) says false or omits the field.
//  4. Fallback, only when the field is omitted: fetch the receipt of the entry's tx from the RPC of
//     its network, check that it is mined in the block the entry reports, and that this block is
//     at or below the block the proxy considers finalized on that network (see Config.FinalizedBlock).
//     Deciding by block number alone is NOT done: a stored block can have been replaced by another
//     one with the same number, so the tx itself must be found in that block.
//
// Entries that did not come from the bridge service (e.g. found through the RPC log scan fallback
// of the activity endpoint) never carry the field: the caller is the one deciding whether to use
// Resolver for them. Resolver itself just takes plain values.
package finality

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"golang.org/x/sync/singleflight"
)

const (
	// DefaultHeadTTL is the default Config.HeadTTL: how long the finalized head of a network is
	// reused before it is fetched again
	DefaultHeadTTL = 5 * time.Second

	// DefaultCacheSize is the default Config.CacheSize: how many entries seen final are
	// remembered. Same order of magnitude as bridgetracker.DefaultMaxTrackedBridges
	DefaultCacheSize = 100_000

	// headFetchTimeout bounds the fetch of a finalized head. The fetch is shared by every caller
	// waiting for it (see RPCResolver.finalizedHead), so it does not depend on any single caller's
	// context
	headFetchTimeout = 20 * time.Second
)

// Resolver tells whether a bridge-service entry (a bridge or a claim) is final.
//
// Implementations must be safe for concurrent use.
type Resolver interface {
	// IsFinal reports whether the entry created by txHash, mined in blockNum of networkID, is final.
	// reported is the block_finalized value of the bridge-service response: nil when it was
	// omitted. See the package documentation for the rules.
	//
	// The returned bool is the answer to use, always: false whenever err != nil. A non-nil error
	// only says why the entry could not be proven final this time (RPC failure, context
	// cancelled); callers must treat it as "not final yet", may log it, and must never fail the
	// surrounding operation nor discard / remove entries because of it. Asking again later is the
	// retry.
	IsFinal(
		ctx context.Context, networkID uint32, txHash common.Hash, blockNum uint64, reported *bool,
	) (bool, error)
}

// ClientResolver resolves the JSON-RPC client of a network. It is satisfied by
// sources.EthClientResolver implementations (sources.FinderClients, sources.StaticClients)
type ClientResolver interface {
	RPCClientFor(ctx context.Context, networkID uint32) (aggkittypes.BaseEthereumClienter, error)
}

// Config is the configuration of an RPCResolver
type Config struct {
	// FinalizedBlock returns the block the proxy considers finalized on a network (a tag like
	// FinalizedBlock or SafeBlock, or LatestBlock/-N): an entry mined in a block at or below it is
	// final. Required. An empty result falls back to aggkittypes.FinalizedBlock. See
	// bridgetracker.Config.FinalizedBlockFor
	FinalizedBlock func(networkID uint32) aggkittypes.BlockNumberFinality

	// HeadTTL is how long the head returned by FinalizedBlock is reused before it is fetched again
	// from the network (once per network and period, never once per call). A value <= 0 falls back
	// to DefaultHeadTTL
	HeadTTL time.Duration

	// CacheSize bounds how many entries seen final are remembered (least recently used first out).
	// Only final answers are stored, so an eviction can only cost one more receipt lookup. A value
	// <= 0 falls back to DefaultCacheSize
	CacheSize int

	// Logger is optional; nil disables logging
	Logger aggkitcommon.Logger
}

// entryKey identifies an entry in the monotonic cache: the block number is part of it so a tx
// that a reorg moved to another block is not considered final because of its previous position
type entryKey struct {
	networkID uint32
	txHash    common.Hash
	blockNum  uint64
}

// headEntry is a cached finalized head
type headEntry struct {
	number    uint64
	fetchedAt time.Time
}

// RPCResolver is the Resolver backed by the per-network JSON-RPC clients
type RPCResolver struct {
	clients        ClientResolver
	finalizedBlock func(networkID uint32) aggkittypes.BlockNumberFinality
	headTTL        time.Duration
	logger         aggkitcommon.Logger

	// final is the monotonic cache: entries seen final
	final *boundedSet[entryKey]

	mu    sync.Mutex
	heads map[uint32]headEntry
	// flights collapses the concurrent refreshes of the head of one network into one RPC call
	flights singleflight.Group
	// now is the clock, injectable for tests
	now func() time.Time
}

var _ Resolver = (*RPCResolver)(nil)

// NewRPCResolver returns an RPCResolver fetching receipts and heads through clients
func NewRPCResolver(clients ClientResolver, cfg Config) (*RPCResolver, error) {
	if clients == nil {
		return nil, errors.New("finality: a JSON-RPC client resolver is required")
	}
	if cfg.FinalizedBlock == nil {
		return nil, errors.New("finality: Config.FinalizedBlock is required")
	}
	headTTL := cfg.HeadTTL
	if headTTL <= 0 {
		headTTL = DefaultHeadTTL
	}
	cacheSize := cfg.CacheSize
	if cacheSize <= 0 {
		cacheSize = DefaultCacheSize
	}
	return &RPCResolver{
		clients:        clients,
		finalizedBlock: cfg.FinalizedBlock,
		headTTL:        headTTL,
		logger:         cfg.Logger,
		final:          newBoundedSet[entryKey](cacheSize),
		heads:          make(map[uint32]headEntry),
		now:            time.Now,
	}, nil
}

// IsFinal implements Resolver
func (r *RPCResolver) IsFinal(
	ctx context.Context, networkID uint32, txHash common.Hash, blockNum uint64, reported *bool,
) (bool, error) {
	key := entryKey{networkID: networkID, txHash: txHash, blockNum: blockNum}

	// Monotonic: once seen final, whatever is reported later (false, omitted) cannot undo it
	if r.final.contains(key) {
		return true, nil
	}

	if reported != nil {
		// An explicit value always wins and the fallback is never used. Only true is remembered:
		// a false can turn into true later as the network finalizes
		if *reported {
			r.final.add(key)
		}
		return *reported, nil
	}

	return r.resolveFromReceipt(ctx, key)
}

// resolveFromReceipt is the fallback for an omitted block_finalized: the entry is final when its
// tx is found mined in key.blockNum and that block is at or below the network's finalized head.
// The head is checked first: it is cached, so an entry that cannot be final yet costs no RPC call,
// and the one receipt call is only made when it can change the answer — once per entry, since a
// final answer is cached
func (r *RPCResolver) resolveFromReceipt(ctx context.Context, key entryKey) (bool, error) {
	head, err := r.finalizedHead(ctx, key.networkID)
	if err != nil {
		return false, err
	}
	if key.blockNum > head {
		return false, nil
	}

	client, err := r.clients.RPCClientFor(ctx, key.networkID)
	if err != nil {
		return false, fmt.Errorf("finality: resolving JSON-RPC client of network %d: %w", key.networkID, err)
	}
	receipt, err := client.TransactionReceipt(ctx, key.txHash)
	if errors.Is(err, ethereum.NotFound) {
		// Not mined (anymore) as far as this node knows: not final, and not an error either
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("finality: fetching receipt of %s on network %d: %w", key.txHash, key.networkID, err)
	}
	// The tx must be in the block the entry reports: a stored block can have been replaced by
	// another one with the same number, and the tx re-mined elsewhere
	if receipt == nil || receipt.BlockNumber == nil || !receipt.BlockNumber.IsUint64() ||
		receipt.BlockNumber.Uint64() != key.blockNum {
		return false, nil
	}

	r.final.add(key)
	r.debugf("tx %s of network %d is final in block %d (finalized head %d), resolved from its receipt",
		key.txHash, key.networkID, key.blockNum, head)
	return true, nil
}

// finalizedHead returns the number of the block the proxy considers finalized on networkID,
// reusing a value younger than the HeadTTL; concurrent refreshes of one network share one fetch.
// A failed refresh is an error, an expired value is never served (a head built from LatestBlock/-N
// can go backwards on a reorg, so an old value is not always the safe one)
func (r *RPCResolver) finalizedHead(ctx context.Context, networkID uint32) (uint64, error) {
	if head, ok := r.cachedHead(networkID); ok {
		return head, nil
	}

	// The fetch must outlive the context of the caller that happens to start it: others wait for it
	detached := context.WithoutCancel(ctx)
	resultCh := r.flights.DoChan(strconv.FormatUint(uint64(networkID), 10), func() (any, error) {
		fetchCtx, cancel := context.WithTimeout(detached, headFetchTimeout)
		defer cancel()
		return r.fetchHead(fetchCtx, networkID)
	})
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case result := <-resultCh:
		if result.Err != nil {
			return 0, result.Err
		}
		head, _ := result.Val.(uint64)
		return head, nil
	}
}

// cachedHead returns the cached finalized head of networkID if it is still fresh
func (r *RPCResolver) cachedHead(networkID uint32) (uint64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.heads[networkID]
	if !ok || r.now().Sub(entry.fetchedAt) >= r.headTTL {
		return 0, false
	}
	return entry.number, true
}

// fetchHead fetches the finalized head of networkID from its RPC and caches it
func (r *RPCResolver) fetchHead(ctx context.Context, networkID uint32) (uint64, error) {
	blockSpec := r.finalizedBlock(networkID)
	if blockSpec.IsEmpty() {
		blockSpec = aggkittypes.FinalizedBlock
	}

	client, err := r.clients.RPCClientFor(ctx, networkID)
	if err != nil {
		return 0, fmt.Errorf("finality: resolving JSON-RPC client of network %d: %w", networkID, err)
	}
	header, err := client.CustomHeaderByNumber(ctx, &blockSpec)
	if err != nil {
		return 0, fmt.Errorf("finality: fetching %s header of network %d: %w", blockSpec.String(), networkID, err)
	}

	r.mu.Lock()
	r.heads[networkID] = headEntry{number: header.Number, fetchedAt: r.now()}
	r.mu.Unlock()
	return header.Number, nil
}

func (r *RPCResolver) debugf(format string, args ...any) {
	if r.logger != nil {
		r.logger.Debugf(format, args...)
	}
}
