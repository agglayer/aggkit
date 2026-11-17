package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgeservice/client"
	"github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

// This file holds the helpers to provoke and observe reorgs in the anvil-backed envs (anvil-2chains):
//
//   - AnvilChain drives an anvil node: reads heads (latest/finalized), controls block production (pause
//     interval mining, mine N blocks), and rewrites history (reorg of N blocks, rollback, snapshot/revert).
//   - WaitForReorgEvent / WaitForBridge* observe what aggkit's bridge-service reports about it.
//
// How finality works in the env: anvil computes the "finalized" tag as latest - 2*slots_in_an_epoch and
// the snapshot images run it with `--block-time 1 --slots-in-an-epoch 1`, so finalized trails latest by
// only 2 blocks. The lag is a pure function of the head number (never of time), so a block stays non-final
// for as long as the chain is not mined further: a test holds data in a non-final state by pausing interval
// mining (PauseMining) and mining blocks by hand (Mine), and makes it final by mining the lag
// (MineUntilFinalized). No env change is needed.
//
// anvil_reorg on a chain that keeps producing blocks is NOT a reliable way to trigger aggkit's reorg
// detection: a syncer only tracks the blocks that contain events plus the head it saw at each poll, so a
// reorg of untracked head blocks goes unnoticed. Pause mining, mine the block with the event plus one more
// block, wait for the syncer to serve the event, and only then reorg (see TestReorgDetectionSmoke).

const (
	// anvilReorgPollInterval is how often the Wait* helpers poll the bridge-service.
	anvilReorgPollInterval = 500 * time.Millisecond

	// finalityLagReadAttempts bounds the re-reads of FinalityLag when blocks keep being mined meanwhile.
	finalityLagReadAttempts = 5

	// waitStatusLogInterval controls how often the Wait* helpers log that they are still waiting.
	waitStatusLogInterval = 10 * time.Second

	// jsonRPCMethodNotFound is the JSON-RPC error code of a method the node does not implement.
	jsonRPCMethodNotFound = -32601

	// listServedBridgesPageSize is the page size ListServedBridges asks for (the bridge-service maximum).
	listServedBridgesPageSize = uint32(200)
)

// ChainHead identifies a block of a chain by number and hash.
type ChainHead struct {
	Number uint64
	Hash   common.Hash
}

// String returns "<number>/<short hash>" for logs.
func (h ChainHead) String() string {
	hash := h.Hash.Hex()
	return fmt.Sprintf("%d/%s", h.Number, hash[:10])
}

// ErrReorgNoop is returned by AnvilChain.Reorg when the replacement blocks are identical to the replaced
// ones, so the chain did not change: see Reorg.
var ErrReorgNoop = errors.New("reorg built the same blocks")

// MiningMode is the block production mode of an anvil node. The anvil images of the e2e envs start with
// interval mining (`--block-time 1`): Automine=false, IntervalSeconds=1. The zero value is manual mining:
// no block is produced until Mine is called.
type MiningMode struct {
	// Automine mines a block as soon as a transaction arrives.
	Automine bool
	// IntervalSeconds is the interval-mining period, 0 when interval mining is off.
	IntervalSeconds uint64
}

// IsPaused reports whether the node produces no blocks on its own.
func (m MiningMode) IsPaused() bool {
	return !m.Automine && m.IntervalSeconds == 0
}

// ReorgTx is a signed transaction to include in a block of the replacement chain of AnvilChain.Reorg.
type ReorgTx struct {
	// Hash is the transaction hash, informational (anvil recomputes it from Raw).
	Hash common.Hash
	// Raw is the signed, RLP encoded transaction (eth_getRawTransactionByHash format).
	Raw hexutil.Bytes
	// BlockOffset is the 0-based index of the replacement block that includes the transaction: 0 is the
	// first block after the common ancestor, depth-1 is the new head.
	BlockOffset uint64
}

// AnvilChain drives the anvil node behind an ethclient. Create one with NewAnvilChain from any client of
// an anvil env (Env.Clients.L1, Env.L2.Client, Env.L2B.Client). The methods that rewrite history or control
// mining are only meaningful on an anvil node: use Supported to skip a test on any other env.
type AnvilChain struct {
	name string
	eth  *ethclient.Client
	rpc  *rpc.Client
}

// NewAnvilChain returns the helper for the anvil node behind eth. name only labels logs and errors.
func NewAnvilChain(name string, eth *ethclient.Client) *AnvilChain {
	return &AnvilChain{name: name, eth: eth, rpc: eth.Client()}
}

// Name returns the label given to NewAnvilChain.
func (a *AnvilChain) Name() string {
	return a.name
}

// Supported reports whether the node answers the anvil_* RPCs the helpers rely on. It returns false only
// when the node answers that the method does not exist (JSON-RPC -32601); any other failure (connection
// refused, timeout...) is returned as an error, so an unhealthy env fails the test instead of skipping it.
func (a *AnvilChain) Supported(ctx context.Context) (bool, error) {
	_, err := a.MiningMode(ctx)
	if err == nil {
		return true, nil
	}
	var rpcErr rpc.Error
	if errors.As(err, &rpcErr) && rpcErr.ErrorCode() == jsonRPCMethodNotFound {
		return false, nil
	}
	return false, err
}

// Head returns the latest block.
func (a *AnvilChain) Head(ctx context.Context) (ChainHead, error) {
	return a.headAt(ctx, nil)
}

// FinalizedHead returns the block of the "finalized" tag.
func (a *AnvilChain) FinalizedHead(ctx context.Context) (ChainHead, error) {
	return a.headAt(ctx, big.NewInt(int64(rpc.FinalizedBlockNumber)))
}

func (a *AnvilChain) headAt(ctx context.Context, number *big.Int) (ChainHead, error) {
	header, err := a.eth.HeaderByNumber(ctx, number)
	if err != nil {
		return ChainHead{}, fmt.Errorf("%s: header at %v: %w", a.name, number, err)
	}
	return ChainHead{Number: header.Number.Uint64(), Hash: header.Hash()}, nil
}

// FinalityLag returns how many blocks the finalized tag trails the latest block (2 in the anvil-2chains
// env). It re-reads when a block is mined between the reads, so it only fails when the chain mines faster
// than the reads: pause mining first.
func (a *AnvilChain) FinalityLag(ctx context.Context) (uint64, error) {
	for attempt := 0; attempt < finalityLagReadAttempts; attempt++ {
		before, err := a.Head(ctx)
		if err != nil {
			return 0, err
		}
		finalized, err := a.FinalizedHead(ctx)
		if err != nil {
			return 0, err
		}
		after, err := a.Head(ctx)
		if err != nil {
			return 0, err
		}
		if before.Number == after.Number {
			return before.Number - finalized.Number, nil
		}
	}
	return 0, fmt.Errorf("%s: the chain kept mining while measuring the finality lag, pause mining first", a.name)
}

// Mine mines blocks blocks on top of the head (anvil_mine). Pending transactions go into the first one.
func (a *AnvilChain) Mine(ctx context.Context, blocks uint64) error {
	if blocks == 0 {
		return nil
	}
	if err := a.rpc.CallContext(ctx, nil, "anvil_mine", hexutil.Uint64(blocks)); err != nil {
		return fmt.Errorf("%s: anvil_mine %d: %w", a.name, blocks, err)
	}
	return nil
}

// MineSpaced mines blocks blocks like Mine, moving the chain clock forward by spacing seconds before each
// one (anvil_mine's interval argument), so every block is timestamped at least spacing seconds after the
// previous one. Mine the block that a later reorg replaces with spacing >= 2 to be sure the replacement
// differs from it (see Reorg). The clock jump stays: block timestamps run ahead of the wall clock until the
// chain is reorged or rolled back below the spaced block, and nothing resets it afterwards (see the e2e
// README: tests that depend on block time vs wall clock should not share a stack run with this).
func (a *AnvilChain) MineSpaced(ctx context.Context, blocks, spacing uint64) error {
	if blocks == 0 {
		return nil
	}
	if err := a.rpc.CallContext(ctx, nil, "anvil_mine", hexutil.Uint64(blocks), hexutil.Uint64(spacing)); err != nil {
		return fmt.Errorf("%s: anvil_mine %d (spacing %ds): %w", a.name, blocks, spacing, err)
	}
	return nil
}

// MineUntilFinalized mines the blocks needed for block number to become finalized (head >= number + lag)
// and does nothing if it already is. It returns the blocks it mined.
func (a *AnvilChain) MineUntilFinalized(ctx context.Context, number uint64) (uint64, error) {
	lag, err := a.FinalityLag(ctx)
	if err != nil {
		return 0, err
	}
	head, err := a.Head(ctx)
	if err != nil {
		return 0, err
	}
	target := number + lag
	if head.Number >= target {
		return 0, nil
	}
	if err := a.Mine(ctx, target-head.Number); err != nil {
		return 0, err
	}
	return target - head.Number, nil
}

// MiningMode returns the current block production mode (anvil_getAutomine, anvil_getIntervalMining).
func (a *AnvilChain) MiningMode(ctx context.Context) (MiningMode, error) {
	var automine bool
	if err := a.rpc.CallContext(ctx, &automine, "anvil_getAutomine"); err != nil {
		return MiningMode{}, fmt.Errorf("%s: anvil_getAutomine: %w", a.name, err)
	}
	var rawInterval json.RawMessage
	if err := a.rpc.CallContext(ctx, &rawInterval, "anvil_getIntervalMining"); err != nil {
		return MiningMode{}, fmt.Errorf("%s: anvil_getIntervalMining: %w", a.name, err)
	}
	interval, err := parseIntervalMining(rawInterval)
	if err != nil {
		return MiningMode{}, fmt.Errorf("%s: anvil_getIntervalMining: %w", a.name, err)
	}
	return MiningMode{Automine: automine, IntervalSeconds: interval}, nil
}

// parseIntervalMining decodes the anvil_getIntervalMining result: null when interval mining is off, a
// number of seconds otherwise (a hex string is accepted too).
func parseIntervalMining(raw json.RawMessage) (uint64, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return 0, nil
	}
	var seconds uint64
	if err := json.Unmarshal(raw, &seconds); err == nil {
		return seconds, nil
	}
	var hexSeconds hexutil.Uint64
	if err := json.Unmarshal(raw, &hexSeconds); err != nil {
		return 0, fmt.Errorf("unexpected interval %s: %w", trimmed, err)
	}
	return uint64(hexSeconds), nil
}

// SetMiningMode switches the node to mode: evm_setAutomine(true) for Automine, evm_setIntervalMining
// otherwise (0 disables interval mining, leaving the node mining only on Mine).
func (a *AnvilChain) SetMiningMode(ctx context.Context, mode MiningMode) error {
	if mode.Automine {
		if err := a.rpc.CallContext(ctx, nil, "evm_setAutomine", true); err != nil {
			return fmt.Errorf("%s: evm_setAutomine: %w", a.name, err)
		}
		return nil
	}
	if err := a.rpc.CallContext(ctx, nil, "evm_setIntervalMining", mode.IntervalSeconds); err != nil {
		return fmt.Errorf("%s: evm_setIntervalMining %d: %w", a.name, mode.IntervalSeconds, err)
	}
	return nil
}

// PauseMining stops the node from producing blocks on its own and returns the function that restores the
// mode it had. While paused, transactions stay pending until Mine is called, and every other actor of the
// env (agglayer on L1, the aggoracle on L2...) is paused with the chain: always restore, see
// PauseMiningForTest for tests.
func (a *AnvilChain) PauseMining(ctx context.Context) (restore func(context.Context) error, err error) {
	previous, err := a.MiningMode(ctx)
	if err != nil {
		return nil, err
	}
	if err := a.SetMiningMode(ctx, MiningMode{}); err != nil {
		return nil, err
	}
	log.Infof("[%s] mining paused (was automine=%t interval=%ds)", a.name, previous.Automine, previous.IntervalSeconds)
	return func(ctx context.Context) error {
		if err := a.SetMiningMode(ctx, previous); err != nil {
			return err
		}
		log.Infof("[%s] mining restored (automine=%t interval=%ds)", a.name, previous.Automine, previous.IntervalSeconds)
		return nil
	}, nil
}

// PauseMiningForTest is PauseMining for a test: it fails the test if the node cannot be paused and
// registers the restore with Cleanup, so the env keeps working for the next test even if this one fails.
func (a *AnvilChain) PauseMiningForTest(ctx context.Context, tb testing.TB) {
	tb.Helper()
	restore, err := a.PauseMining(ctx)
	if err != nil {
		tb.Fatalf("pause mining of %s: %v", a.name, err)
	}
	tb.Cleanup(func() {
		restoreCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := restore(restoreCtx); err != nil {
			tb.Errorf("restore mining of %s: %v", a.name, err)
		}
	})
}

// Reorg replaces the last depth blocks with depth new blocks (anvil_reorg): the head number does not
// change, the block hashes from the common ancestor on do. The replacement blocks are empty except for
// txs, and the transactions of the replaced blocks that are not in txs are dropped (not returned to the
// pool). Pause mining first, otherwise the head moves while the reorg is built.
//
// A reorg is only a reorg if the replacement blocks differ from the replaced ones, and anvil builds them
// deterministically: the first one is timestamped with the timestamp of the common ancestor (the clock is
// reset to it), so replacing a block with one that has the same transactions gives back the very same block
// (same hash) unless the replaced block was mined more than a second after its ancestor. Reorg fails in that
// case instead of silently doing nothing. To get a reorg that always changes the hashes, drop a transaction
// or move it to another block offset, or mine the replaced blocks with MineSpaced.
func (a *AnvilChain) Reorg(ctx context.Context, depth uint64, txs ...ReorgTx) error {
	if depth == 0 {
		return fmt.Errorf("%s: reorg depth must be > 0", a.name)
	}
	before, err := a.Head(ctx)
	if err != nil {
		return err
	}
	// anvil takes the raw transaction as a plain hex string (an object is read as a call request).
	pairs := make([][2]any, 0, len(txs))
	for _, tx := range txs {
		if tx.BlockOffset >= depth {
			return fmt.Errorf("%s: tx %s offset %d is outside the reorged range (depth %d)",
				a.name, tx.Hash.Hex(), tx.BlockOffset, depth)
		}
		pairs = append(pairs, [2]any{tx.Raw, tx.BlockOffset})
	}
	if err := a.rpc.CallContext(ctx, nil, "anvil_reorg", depth, pairs); err != nil {
		return fmt.Errorf("%s: anvil_reorg depth=%d txs=%d: %w", a.name, depth, len(txs), err)
	}
	after, err := a.Head(ctx)
	if err != nil {
		return err
	}
	if after.Hash == before.Hash {
		return fmt.Errorf("%w: %s head %s is unchanged after anvil_reorg depth=%d",
			ErrReorgNoop, a.name, after, depth)
	}
	return nil
}

// TxsInLastBlocks returns the transactions of the last depth blocks as ReorgTx (hash, raw bytes and the
// block offset they have in a reorg of that depth), in block and in-block order.
func (a *AnvilChain) TxsInLastBlocks(ctx context.Context, depth uint64) ([]ReorgTx, error) {
	head, err := a.Head(ctx)
	if err != nil {
		return nil, err
	}
	if depth == 0 || depth > head.Number {
		return nil, fmt.Errorf("%s: invalid depth %d for head %d", a.name, depth, head.Number)
	}
	first := head.Number - depth + 1
	var txs []ReorgTx
	for number := first; number <= head.Number; number++ {
		var block struct {
			Transactions []common.Hash `json:"transactions"`
		}
		if err := a.rpc.CallContext(ctx, &block, "eth_getBlockByNumber", hexutil.EncodeUint64(number), false); err != nil {
			return nil, fmt.Errorf("%s: block %d: %w", a.name, number, err)
		}
		for _, hash := range block.Transactions {
			raw, err := a.RawTransaction(ctx, hash)
			if err != nil {
				return nil, err
			}
			txs = append(txs, ReorgTx{Hash: hash, Raw: raw, BlockOffset: number - first})
		}
	}
	return txs, nil
}

// RawTransaction returns the signed raw encoding of a mined transaction (eth_getRawTransactionByHash).
func (a *AnvilChain) RawTransaction(ctx context.Context, hash common.Hash) (hexutil.Bytes, error) {
	var raw hexutil.Bytes
	if err := a.rpc.CallContext(ctx, &raw, "eth_getRawTransactionByHash", hash); err != nil {
		return nil, fmt.Errorf("%s: raw transaction %s: %w", a.name, hash.Hex(), err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("%s: raw transaction %s: not found", a.name, hash.Hex())
	}
	return raw, nil
}

// ReorgKeepingTxs is a reorg of the last depth blocks that re-includes, in the block offset they had,
// every transaction of the replaced blocks except the ones in drop: the head number stays and the block
// hashes from the common ancestor on change, while only the dropped transactions vanish. With no drop it is
// a pure "same transactions, other blocks" reorg, which Reorg only accepts if the replaced blocks were mined
// with MineSpaced (or more than a second after their ancestor). It is the surgical way to reorg a chain
// that other actors (agglayer, aggoracle) also send transactions to, since an empty Reorg would drop theirs
// as well. It fails if a dropped transaction is not in the replaced blocks.
func (a *AnvilChain) ReorgKeepingTxs(ctx context.Context, depth uint64, drop ...common.Hash) error {
	txs, err := a.TxsInLastBlocks(ctx, depth)
	if err != nil {
		return err
	}
	dropped := make(map[common.Hash]bool, len(drop))
	for _, hash := range drop {
		dropped[hash] = true
	}
	kept := make([]ReorgTx, 0, len(txs))
	for _, tx := range txs {
		if dropped[tx.Hash] {
			delete(dropped, tx.Hash)
			continue
		}
		kept = append(kept, tx)
	}
	for hash := range dropped {
		return fmt.Errorf("%s: tx %s is not in the last %d blocks, cannot drop it", a.name, hash.Hex(), depth)
	}
	return a.Reorg(ctx, depth, kept...)
}

// Rollback drops the last depth blocks without replacing them (anvil_rollback): the head number goes back
// by depth and the dropped transactions vanish. Mine afterwards to build a new tip.
func (a *AnvilChain) Rollback(ctx context.Context, depth uint64) error {
	if err := a.rpc.CallContext(ctx, nil, "anvil_rollback", depth); err != nil {
		return fmt.Errorf("%s: anvil_rollback %d: %w", a.name, depth, err)
	}
	return nil
}

// Snapshot saves the chain state (evm_snapshot) and returns the snapshot id for Revert.
func (a *AnvilChain) Snapshot(ctx context.Context) (string, error) {
	var id string
	if err := a.rpc.CallContext(ctx, &id, "evm_snapshot"); err != nil {
		return "", fmt.Errorf("%s: evm_snapshot: %w", a.name, err)
	}
	return id, nil
}

// Revert goes back to a snapshot taken with Snapshot (evm_revert): the head moves back to the snapshot's
// block, so it behaves as a reorg whose replacement is empty. A snapshot is consumed by its revert, and the
// snapshots taken after it are discarded.
func (a *AnvilChain) Revert(ctx context.Context, id string) error {
	var ok bool
	if err := a.rpc.CallContext(ctx, &ok, "evm_revert", id); err != nil {
		return fmt.Errorf("%s: evm_revert %s: %w", a.name, id, err)
	}
	if !ok {
		return fmt.Errorf("%s: evm_revert %s: unknown or already used snapshot", a.name, id)
	}
	return nil
}

// waitUntil polls check every anvilReorgPollInterval until it returns done, an error ends the wait
// immediately, or timeout/ctx expire. what names the awaited condition in the timeout error, which also
// carries lastState() (what the last poll observed) when given.
func waitUntil(
	ctx context.Context, timeout time.Duration, what string, check func() (done bool, err error),
	lastState func() string,
) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(anvilReorgPollInterval)
	defer ticker.Stop()
	started := time.Now()
	lastLog := started
	for {
		done, err := check()
		if err != nil {
			return fmt.Errorf("waiting for %s: %w", what, err)
		}
		if done {
			return nil
		}
		if time.Since(lastLog) >= waitStatusLogInterval {
			lastLog = time.Now()
			log.Infof("still waiting for %s (%s elapsed)", what, time.Since(started).Round(time.Second))
		}
		select {
		case <-waitCtx.Done():
			state := ""
			if lastState != nil {
				state = ": last state: " + lastState()
			}
			return fmt.Errorf("timed out after %s waiting for %s%s", timeout, what, state)
		case <-ticker.C:
		}
	}
}

// LastReorgEvent reads GET /bridge/v1/last-reorg-event for networkID (0 = L1, or the L2 network id) from a
// bridge-service. A service that never saw a reorg answers the zero value.
func LastReorgEvent(
	ctx context.Context, bs *client.Client, networkID uint32,
) (bridgesync.LastReorg, error) {
	event, err := bs.GetLastReorgEvent(ctx, int(networkID))
	if err != nil {
		return bridgesync.LastReorg{}, fmt.Errorf("last-reorg-event of network %d: %w", networkID, err)
	}
	return *event, nil
}

// WaitForReorgEvent waits until the bridge-service reports a reorg event for networkID detected after
// baseline (read it with LastReorgEvent BEFORE provoking the reorg) and returns it. A reorg detected by
// aggkit takes about one reorg-detector tick (2s) to show up once the tracked blocks changed.
//
// Events are ordered by detected_at, which has a 1s resolution: the endpoint returns one row of the reorg
// detector's table, which holds one row per syncer for the same detection and has no tie-break, so comparing
// whole events would take the other row of the baseline's detection for a new one. Two reorgs less than
// a second apart cannot be told apart, which a reorg-detector tick of 2s rules out for sequential reorgs.
//
// Pass the direct bridge-service client (Env.L2.BridgeService) to check the syncers, or
// Env.Clients.BridgeServiceProxy to check the same through the aggkit-proxy: calling the helper with both
// tells a syncer problem (neither reports it) from a proxy problem (only the direct one does).
func WaitForReorgEvent(
	ctx context.Context, bs *client.Client, networkID uint32, baseline bridgesync.LastReorg, timeout time.Duration,
) (bridgesync.LastReorg, error) {
	var (
		current bridgesync.LastReorg
		lastErr error
	)
	err := waitUntil(ctx, timeout, fmt.Sprintf("a reorg event of network %d detected after %+v", networkID, baseline),
		func() (bool, error) {
			event, err := LastReorgEvent(ctx, bs, networkID)
			if err != nil {
				lastErr = err // transient: the bridge-service may be busy resolving the reorg
				return false, nil
			}
			lastErr = nil
			current = event
			return event.DetectedAt > baseline.DetectedAt, nil
		},
		func() string {
			if lastErr != nil {
				return lastErr.Error()
			}
			return fmt.Sprintf("last-reorg-event=%+v", current)
		})
	return current, err
}

// WaitForBridgeBlockFinalized waits until the bridge-service serves the bridge (networkID, depositCount)
// with block_finalized == want and returns it. A bridge served without block_finalized (the syncer cannot
// tell) never matches.
func WaitForBridgeBlockFinalized(
	ctx context.Context, bs *client.Client, networkID, depositCount uint32, want bool, timeout time.Duration,
) (*types.BridgeResponse, error) {
	var (
		found *types.BridgeResponse
		state = "not polled yet"
	)
	err := waitUntil(ctx, timeout,
		fmt.Sprintf("bridge (network %d, deposit_count %d) with block_finalized=%t", networkID, depositCount, want),
		func() (bool, error) {
			bridge, err := bs.GetBridgeByDepositCount(ctx, networkID, depositCount)
			switch {
			case errors.Is(err, client.ErrNotFound):
				state = "bridge not served"
				return false, nil
			case err != nil:
				state = err.Error() // transient: keep polling
				return false, nil
			}
			state = fmt.Sprintf("bridge served at block %d with block_finalized=%s", bridge.BlockNum,
				formatBoolPtr(bridge.BlockFinalized))
			if bridge.BlockFinalized == nil || *bridge.BlockFinalized != want {
				return false, nil
			}
			found = bridge
			return true, nil
		},
		func() string { return state })
	return found, err
}

// WaitForBridgeAbsent waits until the bridge-service stops serving the bridge (networkID, depositCount),
// e.g. after a reorg dropped the transaction that created it.
func WaitForBridgeAbsent(
	ctx context.Context, bs *client.Client, networkID, depositCount uint32, timeout time.Duration,
) error {
	state := "not polled yet"
	return waitUntil(ctx, timeout, fmt.Sprintf("bridge (network %d, deposit_count %d) to disappear", networkID, depositCount),
		func() (bool, error) {
			bridge, err := bs.GetBridgeByDepositCount(ctx, networkID, depositCount)
			switch {
			case errors.Is(err, client.ErrNotFound):
				return true, nil
			case err != nil:
				state = err.Error()
				return false, nil
			}
			state = fmt.Sprintf("bridge still served at block %d", bridge.BlockNum)
			return false, nil
		},
		func() string { return state })
}

// WaitForBridgeSynced waits until the bridge syncer of networkID (0 = L1, anything else = the L2 of this
// bridge-service) is active, not halted and in sync with its bridge contract (GET /bridge/v1/sync-status).
func WaitForBridgeSynced(
	ctx context.Context, bs *client.Client, networkID uint32, timeout time.Duration,
) error {
	state := "not polled yet"
	return waitUntil(ctx, timeout, fmt.Sprintf("the bridge syncer of network %d to be synced", networkID),
		func() (bool, error) {
			status, err := bs.GetSyncStatus(ctx)
			if err != nil {
				state = err.Error() // transient: keep polling
				return false, nil
			}
			info := status.L2Info
			if networkID == 0 {
				info = status.L1Info
			}
			if info == nil {
				return false, errors.New("sync-status carries no info for the network")
			}
			state = fmt.Sprintf("%+v", *info)
			return info.IsActive && !info.IsHalted && info.IsSynced && info.Error == "", nil
		},
		func() string { return state })
}

// formatBoolPtr renders an optional boolean for logs: "true", "false" or "<nil>" (omitted by the server).
func formatBoolPtr(b *bool) string {
	if b == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%t", *b)
}

// WaitForBridgeStable waits until the bridge-service has served the bridge (networkID, depositCount) with
// match returning true for stableFor in a row. A reorg makes a syncer delete the rows of the reorged blocks
// and download them again (up to its poll period later), so a bridge that must survive a reorg can vanish
// for a moment: this tolerates that blip (the clock restarts when the bridge stops matching) but fails if
// the bridge does not come back, or keeps flapping, within timeout.
func WaitForBridgeStable(
	ctx context.Context, bs *client.Client, networkID, depositCount uint32,
	match func(*types.BridgeResponse) bool, stableFor, timeout time.Duration,
) error {
	var (
		matchingSince time.Time
		state         = "not polled yet"
	)
	return waitUntil(ctx, timeout,
		fmt.Sprintf("bridge (network %d, deposit_count %d) to be served unchanged for %s", networkID, depositCount, stableFor),
		func() (bool, error) {
			bridge, err := bs.GetBridgeByDepositCount(ctx, networkID, depositCount)
			if err != nil {
				state = err.Error() // not served (yet) or transient: the clock restarts
				matchingSince = time.Time{}
				return false, nil
			}
			state = fmt.Sprintf("bridge served at block %d with block_finalized=%s", bridge.BlockNum,
				formatBoolPtr(bridge.BlockFinalized))
			if !match(bridge) {
				matchingSince = time.Time{}
				return false, nil
			}
			if matchingSince.IsZero() {
				matchingSince = time.Now()
			}
			return time.Since(matchingSince) >= stableFor, nil
		},
		func() string { return state })
}

// RequireBridgeStaysAbsent checks, for the given duration, that the bridge-service does not serve the
// bridge (networkID, depositCount), and fails on the first poll that does.
func RequireBridgeStaysAbsent(
	ctx context.Context, bs *client.Client, networkID, depositCount uint32, duration time.Duration,
) error {
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		bridge, err := bs.GetBridgeByDepositCount(ctx, networkID, depositCount)
		switch {
		case err == nil:
			return fmt.Errorf("bridge (network %d, deposit_count %d) is served again at block %d",
				networkID, depositCount, bridge.BlockNum)
		case !errors.Is(err, client.ErrNotFound):
			return fmt.Errorf("get bridge (network %d, deposit_count %d): %w", networkID, depositCount, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(anvilReorgPollInterval):
		}
	}
	return nil
}

// ServedBridge is what a bridge-service serves for a bridge that tests compare across reorgs.
type ServedBridge struct {
	BlockNum uint64
	TxHash   common.Hash
}

// ListServedBridges returns every bridge a bridge-service serves for networkID, keyed by deposit count. It
// walks all the pages of GET /bridge/v1/bridges (served newest first), so it also works on a long-lived
// stack where earlier tests left many bridges.
func ListServedBridges(
	ctx context.Context, bs *client.Client, networkID uint32,
) (map[uint32]ServedBridge, error) {
	pageSize := listServedBridgesPageSize
	served := make(map[uint32]ServedBridge)
	for page := uint32(1); ; page++ {
		pageNumber := page
		result, err := bs.GetBridges(ctx, client.GetBridgesParams{
			NetworkID: networkID, PageNumber: &pageNumber, PageSize: &pageSize,
		})
		if err != nil {
			return nil, fmt.Errorf("list bridges of network %d (page %d): %w", networkID, page, err)
		}
		for _, bridge := range result.Bridges {
			served[bridge.DepositCount] = ServedBridge{
				BlockNum: bridge.BlockNum,
				TxHash:   common.HexToHash(string(bridge.TxHash)),
			}
		}
		if len(result.Bridges) == 0 || uint64(page)*uint64(pageSize) >= uint64(result.Count) {
			return served, nil
		}
	}
}
