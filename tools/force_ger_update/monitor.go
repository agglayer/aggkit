package force_ger_update

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerger"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/event"
)

// updateL1InfoTreeSignature is the topic0 hash of the V1 UpdateL1InfoTree(bytes32,bytes32) event.
// Watching/scanning V1 is sufficient: V1 and V2 are emitted in the same transaction.
var updateL1InfoTreeSignature = crypto.Keccak256Hash([]byte("UpdateL1InfoTree(bytes32,bytes32)"))

// watchResubscribeBackoff bounds how long the watch loop waits before retrying a failed/broken WS
// subscription. It is a var (rather than a const) so tests can shrink it.
var watchResubscribeBackoff = 5 * time.Second

// bootScanCallTimeout bounds each individual RPC call made during LastGERUpdate's boot scan. The
// GERMonitor interface gives LastGERUpdate no context (it runs once, before the caller has a
// long-lived ctx to propagate), so every call gets its own bounded timeout instead of running
// unbounded — mirroring sync/evmdownloader.go's DefaultFilterLogsTimeout convention.
const bootScanCallTimeout = 30 * time.Second

// Monitor implements GERMonitor without any syncer, reorg detector, or persistent storage: LastGERUpdate
// performs a boot-time backward chunked FilterLogs scan, and Start watches (WS subscription) or polls
// (FilterLogs) for new UpdateL1InfoTree events.
type Monitor struct {
	cfg ForceGERUpdateConfig

	// l1Client is the (mandatory) HTTP client used for the boot scan, for polling (when L1WSURL is
	// unset), and for resolving block timestamps in every mode.
	l1Client aggkittypes.BaseEthereumClienter

	// wsClient is the optional WS client used to subscribe to UpdateL1InfoTree via the agglayerger
	// binding when L1WSURL is set. It is nil otherwise.
	wsClient aggkittypes.BaseEthereumClienter

	gerAddr common.Address
}

var _ GERMonitor = (*Monitor)(nil)

// NewMonitor builds a GERMonitor watching cfg.GlobalExitRootManagerAddr. l1Client is mandatory and is
// used for the boot scan, block-timestamp resolution, and (absent L1WSURL) polling. wsClient must be
// non-nil iff cfg.L1WSURL is set; it is then used exclusively to open the WatchUpdateL1InfoTree
// subscription.
func NewMonitor(cfg ForceGERUpdateConfig, l1Client, wsClient aggkittypes.BaseEthereumClienter) (*Monitor, error) {
	if l1Client == nil {
		return nil, fmt.Errorf("monitor: l1Client is required")
	}
	if cfg.L1WSURL != "" && wsClient == nil {
		return nil, fmt.Errorf("monitor: L1WSURL is configured but wsClient is nil")
	}
	if cfg.FilterLogsChunkSize == 0 {
		return nil, fmt.Errorf("monitor: FilterLogsChunkSize must be greater than 0")
	}

	return &Monitor{
		cfg:      cfg,
		l1Client: l1Client,
		wsClient: wsClient,
		gerAddr:  cfg.GlobalExitRootManagerAddr,
	}, nil
}

// LastGERUpdate implements GERMonitor.
func (m *Monitor) LastGERUpdate() (time.Time, error) {
	latest, err := m.boundedBlockNumber()
	if err != nil {
		return time.Time{}, fmt.Errorf("get latest L1 block number: %w", err)
	}

	var lookbackFloor uint64
	if latest > m.cfg.InitialLookbackBlocks {
		lookbackFloor = latest - m.cfg.InitialLookbackBlocks
	}

	// Scan backwards in FilterLogsChunkSize chunks, starting at the latest block, down to
	// lookbackFloor. The first (i.e. newest) non-empty chunk wins.
	for hi := latest; ; {
		lo := lookbackFloor
		if hi-lookbackFloor+1 >= m.cfg.FilterLogsChunkSize {
			lo = hi - m.cfg.FilterLogsChunkSize + 1
		}

		logs, err := m.boundedFilterLogs(lo, hi)
		if err != nil {
			return time.Time{}, fmt.Errorf("filter UpdateL1InfoTree logs [%d,%d]: %w", lo, hi, err)
		}

		if newest := newestLog(logs); newest != nil {
			ts, err := m.boundedBlockTimestamp(newest.BlockNumber)
			if err != nil {
				return time.Time{}, fmt.Errorf("resolve timestamp of block %d: %w", newest.BlockNumber, err)
			}
			return ts, nil
		}

		if lo == lookbackFloor {
			break
		}
		hi = lo - 1
	}

	log.Warnf("force_ger_update: no UpdateL1InfoTree event found in the last %d blocks (up to block %d); "+
		"treating the GER as stale", m.cfg.InitialLookbackBlocks, latest)
	return time.Time{}, nil
}

// boundedBlockNumber, boundedFilterLogs and boundedBlockTimestamp wrap the corresponding l1Client
// calls with bootScanCallTimeout: LastGERUpdate has no caller-supplied context (see GERMonitor), so
// each of its RPC calls is individually bounded instead of running unbounded.
func (m *Monitor) boundedBlockNumber() (uint64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), bootScanCallTimeout)
	defer cancel()
	return m.l1Client.BlockNumber(ctx)
}

func (m *Monitor) boundedFilterLogs(fromBlock, toBlock uint64) ([]types.Log, error) {
	ctx, cancel := context.WithTimeout(context.Background(), bootScanCallTimeout)
	defer cancel()
	return m.l1Client.FilterLogs(ctx, m.filterQuery(fromBlock, toBlock))
}

func (m *Monitor) boundedBlockTimestamp(blockNumber uint64) (time.Time, error) {
	ctx, cancel := context.WithTimeout(context.Background(), bootScanCallTimeout)
	defer cancel()
	return m.blockTimestamp(ctx, blockNumber)
}

// Start implements GERMonitor.
func (m *Monitor) Start(ctx context.Context) (<-chan GERUpdateEvent, error) {
	out := make(chan GERUpdateEvent)

	if m.cfg.L1WSURL != "" {
		go m.watch(ctx, out)
		return out, nil
	}

	latest, err := m.l1Client.BlockNumber(ctx)
	if err != nil {
		return nil, fmt.Errorf("get latest L1 block number to start polling: %w", err)
	}

	go m.poll(ctx, out, latest)
	return out, nil
}

// poll implements the L1WSURL-unset watch mode: every EventPollInterval, FilterLogs is called from
// lastSeenBlock+1 to the current latest block. out is closed when ctx is cancelled.
func (m *Monitor) poll(ctx context.Context, out chan<- GERUpdateEvent, lastSeenBlock uint64) {
	defer close(out)

	ticker := time.NewTicker(m.cfg.EventPollInterval.Duration)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		latest, err := m.l1Client.BlockNumber(ctx)
		if err != nil {
			log.Warnf("force_ger_update: poll: get latest L1 block number: %v", err)
			continue
		}
		if latest <= lastSeenBlock {
			continue
		}

		logs, err := m.l1Client.FilterLogs(ctx, m.filterQuery(lastSeenBlock+1, latest))
		if err != nil {
			log.Warnf("force_ger_update: poll: filter UpdateL1InfoTree logs [%d,%d]: %v", lastSeenBlock+1, latest, err)
			continue
		}
		lastSeenBlock = latest

		if !m.emitAll(ctx, ascendingLogs(logs), out) {
			return
		}
	}
}

// watch implements the L1WSURL-set watch mode: it subscribes via the agglayerger binding's
// WatchUpdateL1InfoTree and automatically re-subscribes (with backoff) whenever the subscription
// breaks, until ctx is cancelled. out is closed when ctx is cancelled.
func (m *Monitor) watch(ctx context.Context, out chan<- GERUpdateEvent) {
	defer close(out)

	ger, err := agglayerger.NewAgglayerger(m.gerAddr, m.wsClient)
	if err != nil {
		log.Errorf("force_ger_update: watch: bind GER contract %s: %v", m.gerAddr, err)
		return
	}

	for ctx.Err() == nil {
		sink := make(chan *agglayerger.AgglayergerUpdateL1InfoTree)
		sub, err := ger.WatchUpdateL1InfoTree(&bind.WatchOpts{Context: ctx}, sink, nil, nil)
		if err != nil {
			log.Warnf("force_ger_update: watch: subscribe UpdateL1InfoTree: %v; retrying in %s", err, watchResubscribeBackoff)
			if !sleepOrDone(ctx, watchResubscribeBackoff) {
				return
			}
			continue
		}

		if !m.consumeSubscription(ctx, sub, sink, out) {
			return
		}

		// The subscription ended (error or remote close) while ctx is still alive: back off and
		// re-subscribe.
		if !sleepOrDone(ctx, watchResubscribeBackoff) {
			return
		}
	}
}

// consumeSubscription forwards every event delivered on sink to out (with its block timestamp
// resolved) until ctx is cancelled (returns false, caller must stop) or the subscription ends
// (returns true, caller should re-subscribe).
func (m *Monitor) consumeSubscription(
	ctx context.Context,
	sub event.Subscription,
	sink chan *agglayerger.AgglayergerUpdateL1InfoTree,
	out chan<- GERUpdateEvent,
) bool {
	defer sub.Unsubscribe()

	for {
		select {
		case <-ctx.Done():
			return false
		case err := <-sub.Err():
			if err != nil {
				log.Warnf("force_ger_update: watch: subscription error: %v", err)
			}
			return true
		case ev := <-sink:
			ts, err := m.blockTimestamp(ctx, ev.Raw.BlockNumber)
			if err != nil {
				log.Warnf("force_ger_update: watch: resolve timestamp of block %d: %v", ev.Raw.BlockNumber, err)
				continue
			}
			if !emit(ctx, out, GERUpdateEvent{BlockNumber: ev.Raw.BlockNumber, BlockTimestamp: ts}) {
				return false
			}
		}
	}
}

// filterQuery builds the UpdateL1InfoTree FilterQuery for the [fromBlock, toBlock] range.
func (m *Monitor) filterQuery(fromBlock, toBlock uint64) ethereum.FilterQuery {
	return ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(fromBlock),
		ToBlock:   new(big.Int).SetUint64(toBlock),
		Addresses: []common.Address{m.gerAddr},
		Topics:    [][]common.Hash{{updateL1InfoTreeSignature}},
	}
}

// blockTimestamp resolves the wall-clock timestamp of blockNumber's header.
func (m *Monitor) blockTimestamp(ctx context.Context, blockNumber uint64) (time.Time, error) {
	header, err := m.l1Client.CustomHeaderByNumber(ctx, aggkittypes.NewBlockNumber(blockNumber))
	if err != nil {
		return time.Time{}, fmt.Errorf("get header for block %d: %w", blockNumber, err)
	}
	return time.Unix(int64(header.Time), 0).UTC(), nil
}

// emitAll resolves and delivers every log in order, stopping (and returning false) if ctx is
// cancelled while doing so.
func (m *Monitor) emitAll(ctx context.Context, logs []types.Log, out chan<- GERUpdateEvent) bool {
	for _, l := range logs {
		ts, err := m.blockTimestamp(ctx, l.BlockNumber)
		if err != nil {
			log.Warnf("force_ger_update: resolve timestamp of block %d: %v", l.BlockNumber, err)
			continue
		}
		if !emit(ctx, out, GERUpdateEvent{BlockNumber: l.BlockNumber, BlockTimestamp: ts}) {
			return false
		}
	}
	return true
}

// emit delivers ev on out, returning false (without delivering) if ctx is cancelled first.
func emit(ctx context.Context, out chan<- GERUpdateEvent, ev GERUpdateEvent) bool {
	select {
	case out <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

// sleepOrDone waits for d, returning false early (without having slept the full duration) if ctx is
// cancelled first.
func sleepOrDone(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// newestLog returns a pointer to the log with the highest (BlockNumber, Index) in logs, or nil if
// logs is empty.
func newestLog(logs []types.Log) *types.Log {
	if len(logs) == 0 {
		return nil
	}
	newest := logs[0]
	for _, l := range logs[1:] {
		if logIsAfter(l, newest) {
			newest = l
		}
	}
	return &newest
}

// ascendingLogs returns a copy of logs sorted by (BlockNumber, Index) ascending.
func ascendingLogs(logs []types.Log) []types.Log {
	sorted := make([]types.Log, len(logs))
	copy(sorted, logs)
	sort.Slice(sorted, func(i, j int) bool {
		return logIsAfter(sorted[j], sorted[i])
	})
	return sorted
}

// logIsAfter reports whether a was emitted after b, ordered by (BlockNumber, Index).
func logIsAfter(a, b types.Log) bool {
	if a.BlockNumber != b.BlockNumber {
		return a.BlockNumber > b.BlockNumber
	}
	return a.Index > b.Index
}
