package exit_certificate

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	agglayerbridgel2 "github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/tools/exit_certificate/bridgesyncerlite"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	anvilReadyTimeout = 30 * time.Second
	anvilPollInterval = 300 * time.Millisecond
	// receiptPollTimeout is how long a collector waits for one tx's receipt. With interval mining a
	// tx's receipt only appears once its whole block is mined, which — for a block batching many
	// cold-state txs against a remote fork — can take a while, so this is generous to avoid false
	// timeouts. A tx that exceeds this bound is not aborted immediately: its exit is deferred and
	// retried after the main send/collect phase (see retryDeferredExit), and only a retry failure is
	// terminal.
	receiptPollTimeout = 300 * time.Second
	// receiptPollInterval is how long a worker waits between receipt polls. With --no-mining the tx
	// is mined by the background miner (see backgroundMineInterval), not synchronously on send, so
	// the first poll always misses; keep this small so that miss costs ~tens of ms, not a fixed 200ms
	// floor per tx (which at mainnet scale dominated the whole replay).
	receiptPollInterval = 25 * time.Millisecond

	// largeETHBalance is MaxUint256 in hex, enough for any bridgeAsset call regardless of exit amounts.
	largeETHBalance = "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"

	abiFuncSelectorSize = 4 // bytes in an ABI function selector

	// uint256Bits is the bit width of an EVM uint256, used to build maxUint256 (2^256-1).
	uint256Bits = 256

	// replayProgressSteps is how many progress lines replayBridgeExits aims to emit over the full
	// replay (one roughly every 1% of exits), instead of one line per individual bridge.
	replayProgressSteps = 100

	// replayLogMaxGap caps how long the replay can run without emitting a progress line, so there is
	// periodic feedback even when 1% of exits (replayProgressSteps) takes a long time to complete.
	replayLogMaxGap = 15 * time.Second

	// forkRetryAttempts/forkRetryBackoff bound how often a replay tx send is retried when the remote
	// fork backend drops a request (see isTransientForkError). Forking a remote RPC under concurrency
	// causes intermittent transport failures; a few backed-off retries ride them out without killing
	// the whole replay.
	forkRetryAttempts = 5
	forkRetryBackoff  = 500 * time.Millisecond

	// replayInFlightWindow bounds how many sent-but-unconfirmed bridge txs sit in Anvil's mempool at
	// once (the send/collect pipeline's channel capacity). It decouples send throughput from the
	// per-tx receipt wait while keeping block size and memory bounded — sending all exits at once
	// would have Anvil mine one gigantic block. It also caps how many txs land in a single interval
	// block, bounding that block's mine time (and thus the receipt latency collectors wait on).
	replayInFlightWindow = 2000

	// anvilBlockTimeSeconds is Anvil's --block-time: it mines a block on this fixed interval, batching
	// all txs pending at each tick into one block. This bounds block count (runtime/interval) instead
	// of one-per-tx (~hundreds of thousands), which kept Anvil from degrading. A worker waits up to
	// one interval for its receipt, so this also caps replay throughput at ~concurrency/interval.
	anvilBlockTimeSeconds = 2

	// anvilTxGasLimit is the explicit gas limit set on every replay transaction. We do NOT rely on
	// Anvil's auto gas estimation: the parallel replay submits many bridgeAsset txs concurrently, so
	// estimateGas runs against a pending state whose global depositCount (and thus the exit-tree
	// Merkle path / SSTORE cost) differs from what the tx sees when actually mined. That under-estimate
	// caused intermittent out-of-gas reverts ("reverted: no revert reason available"). A fixed, generous
	// limit (well under Anvil's 30M block limit) removes the estimation race. A bridgeAsset costs ~300k.
	anvilTxGasLimit = "0x4c4b40" // 5,000,000
)

var (
	// bridgeABI is the parsed ABI for the AgglayerBridgeL2 contract, used to
	// encode/decode bridgeAsset, getRoot, and getTokenWrappedAddress calls.
	bridgeABI abi.ABI

	bridgeEventTopicHash common.Hash

	// maxUint256 is 2^256-1, used as the patched ERC-20 balance and approve amount so a sender can
	// bridge a token any number of times without underflowing its balance/allowance.
	maxUint256 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint256Bits), big.NewInt(1))

	// errReceiptTimeout marks a receipt poll that exhausted receiptPollTimeout without the tx mining,
	// as opposed to a revert or a hard RPC error. Collectors defer these exits for a retry pass rather
	// than aborting the replay (a revert is deterministic and still aborts immediately).
	errReceiptTimeout = errors.New("timeout waiting for receipt")
)

func init() {
	parsed, err := agglayerbridgel2.Agglayerbridgel2MetaData.GetAbi()
	if err != nil {
		panic(fmt.Sprintf("parse agglayerbridgel2 ABI: %v", err))
	}
	bridgeABI = *parsed
	bridgeEventTopicHash = crypto.Keccak256Hash([]byte(
		"BridgeEvent(uint8,uint32,address,uint32,address,uint256,bytes,uint32)",
	))
}

// tokenOriginKey identifies an L1/L2 token by its origin chain and address.
type tokenOriginKey struct {
	network uint32
	addr    common.Address
}

// rpcLog is the JSON representation of a log entry in an eth_getTransactionReceipt response.
type rpcLog struct {
	Address     string   `json:"address"`
	Topics      []string `json:"topics"`
	Data        string   `json:"data"`
	BlockNumber string   `json:"blockNumber"`
	LogIndex    string   `json:"logIndex"`
}

type bridgeEventLog struct {
	LeafType           uint8
	OriginNetwork      uint32
	OriginAddress      common.Address
	DestinationNetwork uint32
	DestinationAddress common.Address
	Amount             *big.Int
	Metadata           []byte
	DepositCount       uint32
}

// FailedBridgeExit records the bridge exit whose replay aborted Step G, persisted to
// step-g-failed-exit.json so the offending exit can be inspected after the run fails.
type FailedBridgeExit struct {
	Index              int    `json:"index"`
	Error              string `json:"error"`
	OriginNetwork      uint32 `json:"originNetwork"`
	OriginTokenAddress string `json:"originTokenAddress"`
	DestinationNetwork uint32 `json:"destinationNetwork"`
	DestinationAddress string `json:"destinationAddress"`
	Amount             string `json:"amount"`
	IsNative           bool   `json:"isNative"`
	L2TokenAddress     string `json:"l2TokenAddress"`
}

// isContextCanceled reports whether err is (or wraps) context.Canceled. Used to suppress noisy
// error logs from the in-flight replay workers that abort once failFast cancels the shared context
// after the first real failure — those cancellations are expected, not the root cause.
func isContextCanceled(err error) bool {
	return errors.Is(err, context.Canceled)
}

// isTransientForkError reports whether err looks like a transient failure of Anvil's fork backend
// (the upstream L2 RPC) — a dropped connection, transport error, or timeout while Anvil lazily
// fetches forked state — rather than a real EVM revert. Forking a remote/public RPC under high
// concurrency triggers these intermittently; they are worth retrying, whereas a contract revert is
// deterministic and must not be retried.
func isTransientForkError(err error) bool {
	if err == nil || isContextCanceled(err) {
		return false
	}
	msg := strings.ToLower(err.Error())
	// A genuine revert is reported as "...reverted..."; never treat those as transient.
	if strings.Contains(msg, "revert") {
		return false
	}
	for _, marker := range []string{"fork error", "transport", "dispatch", "timeout", "connection", "eof"} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// RunStepG2 computes Certificate.NewLocalExitRoot and the per-exit metadata.
//
// By default (options.verifyNewLocalExitRootUsingShadowFork is true — see defaultOptions) it spins
// up the Anvil shadow-fork, replays every exit against the real bridge contract, recovers the
// on-chain deposit order and metadata, and verifies the lite tree root against the contract's
// getRoot(). When the option is set to false it instead computes the root purely off-chain: it
// builds the lite exit tree from Step G1's genesis→fork bridges plus the certificate's bridge exits
// (in their given order, with each exit's own metadata) and takes the tree root as the
// NewLocalExitRoot — no Anvil.
//
// forkBlock is the block resolved by Step G1. lbtEntries (Step 0 output) is used only by the
// shadow-fork path as a wrapped-token lookup so getTokenWrappedAddress RPC calls are avoided.
func RunStepG2(
	ctx context.Context, cfg *Config, forkBlock uint64, certificate *agglayertypes.Certificate, lbtEntries []LBTEntry,
) (*StepGResult, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP G2 - Calculate NewLocalExitRoot")
	log.Info("═══════════════════════════════════════════")

	if certificate == nil {
		return nil, fmt.Errorf("certificate is nil")
	}

	if len(certificate.BridgeExits) == 0 {
		log.Info("No bridge exits — using EmptyLER")
		initialLER, err := readLocalExitRoot(ctx, cfg.L2RPCURL, cfg.L2BridgeAddress, toBlockTag(forkBlock))
		if err != nil {
			log.Warnf("Could not read initial LocalExitRoot: %v", err)
		}
		log.Infof("InitialLocalExitRoot: %s", initialLER.Hex())
		return &StepGResult{
			InitialLocalExitRoot: initialLER,
			NewLocalExitRoot:     bridgesynctypes.EmptyLER,
			BridgeExitCount:      0,
		}, nil
	}

	if !cfg.Options.VerifyNewLocalExitRootUsingShadowFork {
		return runStepG2LiteOnly(ctx, cfg, forkBlock, certificate)
	}
	return runStepG2ShadowFork(ctx, cfg, forkBlock, certificate, lbtEntries)
}

// runStepG2LiteOnly computes the NewLocalExitRoot off-chain (no Anvil): it appends the certificate's
// bridge exits — in their given order, each with its own metadata — onto Step G1's genesis→fork
// lite tree and takes the resulting root. It trusts the off-chain leaf encoding rather than
// verifying it against the contract; use the shadow-fork path to verify.
func runStepG2LiteOnly(
	ctx context.Context, cfg *Config, forkBlock uint64, certificate *agglayertypes.Certificate,
) (*StepGResult, error) {
	log.Info("Computing NewLocalExitRoot off-chain from the lite exit tree (shadow-fork verification disabled)")

	gasTokenNetwork, gasTokenAddress := fetchGasTokenInfoOrDefault(ctx, cfg)

	// InitialLocalExitRoot (the LER at the fork block) is informational here; read it from the real L2.
	initialLER, err := readLocalExitRoot(ctx, cfg.L2RPCURL, cfg.L2BridgeAddress, toBlockTag(forkBlock))
	if err != nil {
		log.Warnf("Could not read initial LocalExitRoot: %v", err)
	}
	log.Infof("InitialLocalExitRoot: %s", initialLER.Hex())

	ler, metadatas, err := buildLiteTreeFromCertificate(ctx, cfg, certificate, forkBlock, gasTokenNetwork, gasTokenAddress)
	if err != nil {
		return nil, err
	}

	result := &StepGResult{
		InitialLocalExitRoot: initialLER,
		NewLocalExitRoot:     ler,
		BridgeExitCount:      uint64(len(certificate.BridgeExits)),
		BridgeExitMetadata:   metadatas,
	}
	log.Infof("Bridge exits processed: %d", result.BridgeExitCount)
	log.Infof("NewLocalExitRoot: %s", result.NewLocalExitRoot.Hex())
	log.Info("STEP G complete")
	return result, nil
}

// runStepG2ShadowFork computes the NewLocalExitRoot by replaying every bridge exit against an Anvil
// shadow-fork of the L2 chain at forkBlock, then verifies the lite exit tree (rebuilt from the
// replayed bridges on top of Step G1's genesis→fork bridges) against the contract's getRoot().
func runStepG2ShadowFork(
	ctx context.Context, cfg *Config, forkBlock uint64, certificate *agglayertypes.Certificate, lbtEntries []LBTEntry,
) (*StepGResult, error) {
	if err := checkAnvilAvailable(); err != nil {
		return nil, err
	}

	anvilURL, cleanup, err := startAnvil(ctx, cfg.L2RPCURL, forkBlock)
	if err != nil {
		return nil, fmt.Errorf("start anvil: %w", err)
	}
	defer cleanup()

	gasTokenNetwork, gasTokenAddress := fetchGasTokenInfoOrDefault(ctx, cfg)

	initialLER, err := readLocalExitRoot(ctx, anvilURL, cfg.L2BridgeAddress, "latest")
	if err != nil {
		return nil, fmt.Errorf("read initial local exit root: %w", err)
	}
	log.Infof("InitialLocalExitRoot: %s", initialLER.Hex())

	lbtMap := buildLBTTokenMap(lbtEntries)
	l2Tokens, err := resolveTokenAddresses(
		ctx, anvilURL, cfg.L2BridgeAddress, certificate.BridgeExits,
		cfg.L2NetworkID, gasTokenNetwork, gasTokenAddress, lbtMap,
	)
	if err != nil {
		return nil, fmt.Errorf("resolve token addresses: %w", err)
	}
	for k, v := range l2Tokens {
		log.Debugf("token map: origin(network=%d addr=%s) -> L2 wrapped %s", k.network, k.addr.Hex(), v.Hex())
	}
	log.Infof("Replaying %d bridge exits on Anvil with concurrency %d...",
		len(certificate.BridgeExits), max(cfg.Options.ConcurrencyLimit, 1))
	// Anvil mines on its own --block-time interval (see anvilBlockTimeSeconds); workers just send and
	// poll for receipts. By the time replayBridgeExits returns, every tx has been waited on and mined,
	// so getRoot below reflects all replayed exits.
	leaves, err := replayBridgeExits(
		ctx, cfg, anvilURL, certificate.BridgeExits, l2Tokens, gasTokenNetwork, gasTokenAddress,
	)
	if err != nil {
		return nil, err
	}

	// The bridge contract's getRoot() after replaying every exit is the authoritative NewLocalExitRoot.
	ler, err := readLocalExitRoot(ctx, anvilURL, cfg.L2BridgeAddress, "latest")
	if err != nil {
		return nil, fmt.Errorf("read local exit root: %w", err)
	}

	// Reorder the certificate to the canonical exit-tree order. The parallel replay assigned
	// depositCounts non-deterministically across exits; each replayed BridgeEvent carries the
	// depositCount the contract gave it, so sorting the exits by it aligns Certificate.BridgeExits with
	// the leaf order agglayer rebuilds the LER from. The reordered metadatas come from the same leaves.
	metadatas, err := reorderCertificateByDepositCount(certificate, leaves)
	if err != nil {
		return nil, fmt.Errorf("reorder certificate by deposit order: %w", err)
	}
	log.Infof("Reordered %d bridge exits to match the replay deposit order", len(certificate.BridgeExits))

	// Insert the replayed bridges into the lite DB directly (no further Anvil calls), on top of the
	// genesis→fork bridges Step G1 stored, build the whole exit tree once, and verify its root equals
	// the contract's getRoot — i.e. our BridgeEvent-only reconstruction matches the real exit tree. A
	// mismatch means the certificate would carry a wrong LER, so abort — except when
	// ignoreUnsupportedL2Events=true, where the lite syncer deliberately skipped events the contract
	// processed, so divergence is accepted (warn only).
	treeRoot, err := buildLiteTreeWithReplayed(ctx, cfg, leaves)
	if err != nil {
		return nil, err
	}
	switch {
	case treeRoot == ler:
		log.Infof("✅ lite exit tree root matches contract getRoot: %s", ler.Hex())
	case cfg.Options.IgnoreUnsupportedL2Events:
		log.Warnf("lite exit tree root %s does not match contract getRoot %s "+
			"(expected: ignoreUnsupportedL2Events=true skipped events the contract processed)",
			treeRoot.Hex(), ler.Hex())
	default:
		return nil, fmt.Errorf("lite exit tree root %s does not match contract getRoot %s: "+
			"the BridgeEvent-only reconstruction diverged from the on-chain exit tree",
			treeRoot.Hex(), ler.Hex())
	}

	result := &StepGResult{
		InitialLocalExitRoot: initialLER,
		NewLocalExitRoot:     ler,
		BridgeExitCount:      uint64(len(certificate.BridgeExits)),
		BridgeExitMetadata:   metadatas,
	}
	log.Infof("Bridge exits processed: %d", result.BridgeExitCount)
	log.Infof("NewLocalExitRoot: %s", result.NewLocalExitRoot.Hex())
	log.Info("STEP G complete")
	return result, nil
}

// fetchGasTokenInfoOrDefault returns the L2 gas token (network, address), falling back to standard
// ETH (network 0, zero address) with a warning if the lookup fails.
func fetchGasTokenInfoOrDefault(ctx context.Context, cfg *Config) (uint32, common.Address) {
	gasTokenNetwork, gasTokenAddress, err := fetchGasTokenInfo(ctx, cfg.L2RPCURL, cfg.L2BridgeAddress)
	if err != nil {
		log.Warnf("Failed to fetch gas token info (assuming standard ETH): %v", err)
		return 0, common.Address{}
	}
	return gasTokenNetwork, gasTokenAddress
}

// exitJob bundles a bridge exit with its index in Certificate.BridgeExits and the
// replay parameters resolved up front (native flag and L2 token address).
type exitJob struct {
	index       int
	bridge      *agglayertypes.BridgeExit
	isNative    bool
	l2TokenAddr common.Address
}

// sentTx pairs a sent bridgeAsset transaction with the exit that produced it, so the collect phase
// can fetch the receipt, detect reverts, and record the BridgeEvent metadata at the right index.
type sentTx struct {
	index int
	hash  common.Hash
	job   exitJob
}

// replayBridgeExits replays every bridge exit against the Anvil shadow-fork and returns the
// BridgeEvent metadata indexed by the original position in exits.
//
// It uses a send/collect pipeline rather than send-and-wait per tx: with Anvil on a --block-time
// interval, waiting for each tx's receipt before sending the next would cap throughput at
// ~concurrency/block-time. Instead, sender workers fire all of a sender's txs without waiting
// (pushing each onto a bounded channel), while collector workers pull those and fetch receipts in
// parallel. The channel's capacity (replayInFlightWindow) bounds how many txs sit unconfirmed in
// Anvil's mempool, so block size and memory stay bounded (sending all ~915k at once would mine one
// gigantic block). Each metadata is written to metadatas[index], keeping it aligned with
// Certificate.BridgeExits regardless of completion order; the canonical deposit order is recovered
// later from the emitted BridgeEvents.
//
// Within a sender's group txs are sent sequentially so Anvil assigns nonces in order (an ERC-20
// approve must precede its bridgeAsset). Balances/allowances are set generously once per sender (and
// per token) up front, so multiple exits from the same sender never underflow regardless of the
// order in which the batched block executes them.
func replayBridgeExits(
	ctx context.Context, cfg *Config, anvilURL string,
	exits []*agglayertypes.BridgeExit, l2Tokens map[tokenOriginKey]common.Address,
	gasTokenNetwork uint32, gasTokenAddress common.Address,
) ([]bridgesyncerlite.BridgeLeaf, error) {
	// leaves[i] holds the full BridgeEvent (leaf content + depositCount + block position) emitted by
	// the replay of exits[i]. The depositCount gives the canonical exit-tree order (used to reorder
	// the certificate), and the leaf is inserted into the lite DB directly — no second pass over the
	// fork is needed to recover either.
	leaves := make([]bridgesyncerlite.BridgeLeaf, len(exits))

	groupsBySender := make(map[common.Address][]exitJob)
	for i, bridge := range exits {
		isNative := isNativeBridgeExit(bridge.TokenInfo, gasTokenNetwork, gasTokenAddress)
		var l2TokenAddr common.Address
		if !isNative {
			addr, err := findTokenAddress(bridge, l2Tokens)
			if err != nil {
				return nil, fmt.Errorf("find token address: %w", err)
			}
			l2TokenAddr = addr
		}
		sender := bridge.DestinationAddress
		groupsBySender[sender] = append(groupsBySender[sender], exitJob{
			index: i, bridge: bridge, isNative: isNative, l2TokenAddr: l2TokenAddr,
		})
	}

	groups := make([][]exitJob, 0, len(groupsBySender))
	for _, g := range groupsBySender {
		groups = append(groups, g)
	}

	concurrency := max(cfg.Options.ConcurrencyLimit, 1)

	// Fail fast: cancel the shared context on the first error so senders and collectors stop, and
	// keep the real error in replayErr (the pipeline would otherwise surface context.Canceled).
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		replayErr  error
		replayOnce sync.Once
	)
	failFast := func(job exitJob, err error) error {
		replayOnce.Do(func() {
			replayErr = err
			// Persist the offending exit so it can be inspected after the run aborts.
			saveFailedExit(cfg.Options.OutputDir, job, err)
			cancel()
		})
		return err
	}

	total := len(exits)
	// Progress is reported as an aggregate %/ETA over collected receipts (~100 log lines) rather than
	// one line per bridge. A line is emitted on the first receipt, every logInterval, the last, and at
	// least every replayLogMaxGap, so there is always early and periodic feedback.
	start := time.Now()
	logInterval := max(total/replayProgressSteps, 1)
	// maybeLogProgress is called once per collected receipt from multiple goroutines. A single mutex
	// guards the counter and the last-log timestamp together, so the decision and the timestamp update
	// are atomic as a unit — no interleaving can emit a duplicate line. The lock is uncontended in
	// practice (the work between calls is a receipt fetch), so it is not on a hot path.
	var (
		progressMu sync.Mutex
		completed  int
		lastLog    time.Time
	)
	maybeLogProgress := func() {
		progressMu.Lock()
		defer progressMu.Unlock()
		completed++
		now := time.Now()
		// Log on the first receipt, every logInterval, the last receipt, or after replayLogMaxGap
		// elapsed without a line — whichever comes first.
		if completed == 1 || completed%logInterval == 0 || completed == total ||
			now.Sub(lastLog) >= replayLogMaxGap {
			lastLog = now
			logReplayProgress(completed, total, start)
		}
	}

	// pending carries sent txs from the sender workers to the collector workers; its capacity bounds
	// the number of unconfirmed txs in Anvil's mempool.
	pending := make(chan sentTx, replayInFlightWindow)

	// deferred collects exits whose receipt timed out in the main phase (Anvil could not mine their
	// block within receiptPollTimeout, typically a slow remote fork backend under load). Rather than
	// abort, they are retried after the send/collect phase drains (see retryDeferredExit).
	var (
		deferred   []sentTx
		deferredMu sync.Mutex
	)

	// Collectors: fetch each sent tx's receipt, detect reverts, and record its BridgeEvent metadata.
	var collectWg sync.WaitGroup
	for c := 0; c < concurrency; c++ {
		collectWg.Add(1)
		go func() {
			defer collectWg.Done()
			for s := range pending {
				logs, err := waitForReceipt(ctx, anvilURL, s.hash)
				if err != nil {
					switch {
					case isContextCanceled(err):
						// Replay already aborting; stop quietly.
					case errors.Is(err, errReceiptTimeout):
						// Block did not mine in time; defer for the retry pass instead of aborting.
						deferredMu.Lock()
						deferred = append(deferred, s)
						deferredMu.Unlock()
					default:
						_ = failFast(s.job, fmt.Errorf("get receipt %s for exit %d: %w", s.hash.Hex(), s.index+1, err))
					}
					continue
				}
				leaf, err := replayedLeafFromReceipt(logs, s.hash)
				if err != nil {
					_ = failFast(s.job, fmt.Errorf("parse BridgeEvent for exit %d (%s): %w", s.index+1, s.hash.Hex(), err))
					continue
				}
				leaves[s.index] = leaf
				maybeLogProgress()
			}
		}()
	}

	// Senders: for each sender, fund it and pre-approve its tokens once, then send all its bridge
	// txs (sequential for nonce order) onto pending without waiting for receipts.
	sendGroup := func(group []exitJob) (struct{}, error) {
		if len(group) == 0 {
			return struct{}{}, nil
		}
		sender := group[0].bridge.DestinationAddress
		if err := setSenderBalance(ctx, anvilURL, sender); err != nil {
			return struct{}{}, failFast(group[0], fmt.Errorf("set balance for %s: %w", sender.Hex(), err))
		}
		approved := make(map[common.Address]bool)
		for _, job := range group {
			if job.isNative || approved[job.l2TokenAddr] {
				continue
			}
			approved[job.l2TokenAddr] = true
			if err := prepareERC20Token(ctx, anvilURL, cfg.L2BridgeAddress, sender, job.l2TokenAddr); err != nil {
				return struct{}{}, failFast(job, fmt.Errorf("prepare ERC20 token %s: %w", job.l2TokenAddr.Hex(), err))
			}
		}
		for _, job := range group {
			log.Debugf("[exit %d/%d] send bridgeAsset [%d/%s] -> %s amount=%s isNative=%t",
				job.index+1, total, job.bridge.TokenInfo.OriginNetwork, job.bridge.TokenInfo.OriginTokenAddress.Hex(),
				job.bridge.DestinationAddress.Hex(), job.bridge.Amount.String(), job.isNative)
			hash, err := sendBridgeAssetTx(ctx, anvilURL, cfg.L2BridgeAddress, job.bridge, job.isNative, job.l2TokenAddr)
			if err != nil {
				return struct{}{}, failFast(job, fmt.Errorf("send bridge asset for exit %d: %w", job.index+1, err))
			}
			select {
			case pending <- sentTx{index: job.index, hash: hash, job: job}:
			case <-ctx.Done():
				return struct{}{}, ctx.Err()
			}
		}
		return struct{}{}, nil
	}

	log.Infof("Sending bridge exits (in-flight window %d) and collecting receipts...", replayInFlightWindow)
	sendErr := runWorkerPool(ctx, groups, concurrency, sendGroup, func(struct{}) {}, "")
	// All sends finished (or aborted): close pending so collectors drain and exit.
	close(pending)
	collectWg.Wait()

	// Retry exits whose receipt timed out. By now all sends are done and Anvil is draining its
	// backlog, so the original blocks have likely mined; recovering them here keeps every exit's leaf
	// at its original index without re-sending a tx that actually mined (see retryDeferredExit).
	if replayErr == nil && sendErr == nil && len(deferred) > 0 {
		log.Warnf("Retrying %d bridge exit(s) whose receipt timed out...", len(deferred))
		for _, s := range deferred {
			leaf, err := retryDeferredExit(ctx, anvilURL, cfg.L2BridgeAddress, s)
			if err != nil {
				_ = failFast(s.job, fmt.Errorf("retry exit %d (%s): %w", s.index+1, s.hash.Hex(), err))
				break
			}
			leaves[s.index] = leaf
			maybeLogProgress()
		}
	}

	if replayErr != nil {
		log.Errorf("Replay failed: %v", replayErr)
		return nil, replayErr
	}
	if sendErr != nil {
		log.Errorf("send phase failed: %v", sendErr)
		return nil, sendErr
	}

	return leaves, nil
}

// retryDeferredExit recovers one exit whose receipt timed out during the main send/collect phase.
//
// It runs after all sends are done and Anvil is idle, and retries **unbounded** until the exit mines:
// each iteration re-polls the current tx (under a slow remote fork backend a block can take longer
// than receiptPollTimeout to mine, so the receipt has very likely appeared by now — waitForReceipt
// returns as soon as it does, accounting for the block interval). Only if the receipt is *still*
// absent after that full poll window — meaning the tx never landed in Anvil's mempool — is the
// bridgeAsset re-sent, and the next iteration polls the new hash. A slow backend is therefore never
// abandoned; the only exits are success, a revert, or context cancellation.
//
// This re-poll-before-resend ordering is what keeps the exit tree correct: a bridgeAsset that did
// mine adds a leaf and bumps depositCount, so re-sending one that already mined would double-count
// the exit and diverge the reconstructed tree from the contract's getRoot(). A revert (or any
// non-timeout error, including context cancellation) is returned as-is and is terminal — re-sending
// a reverting tx would not help, and a canceled context must break the loop.
func retryDeferredExit(
	ctx context.Context, anvilURL string, bridgeAddr common.Address, s sentTx,
) (bridgesyncerlite.BridgeLeaf, error) {
	hash := s.hash
	for attempt := 1; ; attempt++ {
		logs, err := waitForReceipt(ctx, anvilURL, hash)
		if err == nil {
			return replayedLeafFromReceipt(logs, hash)
		}
		if !errors.Is(err, errReceiptTimeout) {
			return bridgesyncerlite.BridgeLeaf{}, err
		}

		log.Warnf("exit %d (%s) still has no receipt after attempt %d; re-sending bridgeAsset",
			s.index+1, hash.Hex(), attempt)
		newHash, err := sendBridgeAssetTx(ctx, anvilURL, bridgeAddr, s.job.bridge, s.job.isNative, s.job.l2TokenAddr)
		if err != nil {
			return bridgesyncerlite.BridgeLeaf{}, fmt.Errorf("re-send bridge asset: %w", err)
		}
		hash = newHash
	}
}

// saveFailedExit writes the bridge exit whose replay aborted Step G to step-g-failed-exit.json in
// dir, so the offending exit can be inspected after the run fails. Best-effort: any write error is
// logged by saveJSON and does not mask the original replay error.
func saveFailedExit(dir string, job exitJob, replayErr error) {
	fe := FailedBridgeExit{
		Index:              job.index,
		Error:              replayErr.Error(),
		DestinationNetwork: job.bridge.DestinationNetwork,
		DestinationAddress: job.bridge.DestinationAddress.Hex(),
		Amount:             bigIntKey(job.bridge.Amount),
		IsNative:           job.isNative,
		L2TokenAddress:     job.l2TokenAddr.Hex(),
	}
	if job.bridge.TokenInfo != nil {
		fe.OriginNetwork = job.bridge.TokenInfo.OriginNetwork
		fe.OriginTokenAddress = job.bridge.TokenInfo.OriginTokenAddress.Hex()
	}
	saveJSON(dir, "step-g-failed-exit.json", fe)
}

// logReplayProgress logs the replay completion percentage, throughput, and ETA. start is the
// time the replay began; done is the number of exits replayed so far out of total.
func logReplayProgress(done, total int, start time.Time) {
	elapsed := time.Since(start)
	rate := float64(done) / elapsed.Seconds()
	eta := "—"
	if rate > 0 {
		remaining := total - done
		eta = (time.Duration(float64(remaining)/rate) * time.Second).Round(time.Second).String()
	}
	log.Infof("  bridgeAsset replay: %d/%d (%.1f%%) — %.0f exits/s — ETA %s",
		done, total, float64(done)/float64(total)*percentMultiplier, rate, eta)
}

func isNativeBridgeExit(
	ti *agglayertypes.TokenInfo, gasTokenNetwork uint32, gasTokenAddress common.Address,
) bool {
	return ti == nil ||
		ti.OriginTokenAddress == (common.Address{}) ||
		(ti.OriginNetwork == gasTokenNetwork && ti.OriginTokenAddress == gasTokenAddress)
}

// findTokenAddress looks up the L2 ERC-20 address for a bridge exit in the token map
// returned by resolveTokenAddresses.
func findTokenAddress(
	bridgeExit *agglayertypes.BridgeExit, tokenMap map[tokenOriginKey]common.Address,
) (common.Address, error) {
	if bridgeExit.TokenInfo == nil {
		return common.Address{}, fmt.Errorf("bridge exit has nil TokenInfo")
	}
	ti := bridgeExit.TokenInfo
	addr, ok := tokenMap[tokenOriginKey{ti.OriginNetwork, ti.OriginTokenAddress}]
	if !ok {
		return common.Address{}, fmt.Errorf("token (network=%d addr=%s) not found in token map",
			ti.OriginNetwork, ti.OriginTokenAddress.Hex())
	}
	return addr, nil
}

// prepareERC20Token makes sender able to bridge the L2 ERC-20 token any number of times: it patches
// a large balance via Anvil storage manipulation and sends a single approve(bridge, MaxUint256). It
// does NOT wait for the approve receipt — the approve has a lower nonce than the sender's bridge txs,
// so Anvil executes it first when the batched block is mined; an insufficient-allowance failure would
// surface as a revert on the bridge tx's receipt. Called once per (sender, token).
func prepareERC20Token(ctx context.Context, rpcURL string, bridgeAddr, sender, l2TokenAddr common.Address) error {
	if l2TokenAddr == (common.Address{}) {
		return fmt.Errorf("invalid L2 token address")
	}
	log.Debugf("Preparing ERC-20 L2 token %s for sender %s (balance + approve MaxUint256)",
		l2TokenAddr.Hex(), sender.Hex())

	// A large balance covers every exit of this token for this sender regardless of how many there
	// are or the order the batched block executes them; the per-exit burn amount is what affects the
	// token's totalSupply, not this balance.
	if err := ensureERC20Balance(ctx, rpcURL, l2TokenAddr, sender, maxUint256); err != nil {
		return fmt.Errorf("ensure ERC-20 balance: %w", err)
	}

	callData := encodeERC20ApproveCallRaw(bridgeAddr, maxUint256)
	if _, err := sendAnvilTransaction(ctx, rpcURL, sender, l2TokenAddr, nil, callData); err != nil {
		if !isContextCanceled(err) {
			log.Errorf("Failed to send approve for ERC-20 token %s: %v", l2TokenAddr.Hex(), err)
		}
		return fmt.Errorf("send approve ERC-20 token %s: %w", l2TokenAddr.Hex(), err)
	}
	return nil
}

// sendBridgeAssetTx sends (without waiting for the receipt) a bridgeAsset call replaying bridgeExit
// against the fork, returning the tx hash for the collect phase to fetch the receipt and metadata.
func sendBridgeAssetTx(ctx context.Context, rpcURL string,
	bridgeAddr common.Address,
	bridgeExit *agglayertypes.BridgeExit,
	isNative bool,
	l2TokenAddr common.Address) (common.Hash, error) {
	sender := bridgeExit.DestinationAddress

	var value *big.Int
	if isNative && bridgeExit.Amount != nil {
		value = bridgeExit.Amount
	}

	callData := encodeBridgeAssetCallRaw(
		bridgeExit.DestinationNetwork,
		bridgeExit.DestinationAddress,
		bridgeExit.Amount,
		l2TokenAddr,
	)

	txHash, err := sendAnvilTransaction(ctx, rpcURL, sender, bridgeAddr, value, callData)
	if err != nil {
		if !isContextCanceled(err) {
			log.Errorf("Failed to send bridge asset tx: %v", err)
		}
		return common.Hash{}, fmt.Errorf("send bridge asset tx: %w", err)
	}
	return txHash, nil
}

func checkAnvilAvailable() error {
	if _, err := exec.LookPath("anvil"); err != nil {
		return fmt.Errorf("anvil not found in $PATH — install the Foundry toolchain from https://getfoundry.sh")
	}
	return nil
}

func findFreePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected listener address type %T", ln.Addr())
	}
	return tcpAddr.Port, nil
}

func startAnvil(ctx context.Context, l2RPCURL string, targetBlock uint64) (string, func(), error) {
	port, err := findFreePort()
	if err != nil {
		return "", nil, fmt.Errorf("find free port: %w", err)
	}

	cmd := exec.CommandContext(ctx, "anvil",
		"--fork-url", l2RPCURL,
		"--fork-block-number", fmt.Sprintf("%d", targetBlock),
		"--port", fmt.Sprintf("%d", port),
		"--silent",
		// Batch mining: with auto-mine each bridgeAsset would mine its own block, so a mainnet replay
		// (hundreds of thousands of exits) accumulates that many blocks and Anvil degrades until
		// receipt polling times out. Instead Anvil mines on a fixed interval (--block-time), batching
		// all txs pending at each tick into one block. --disable-block-gas-limit lets a single block
		// hold every pending tx regardless of their (explicit) gas limits.
		"--block-time", strconv.Itoa(anvilBlockTimeSeconds),
		"--disable-block-gas-limit",
		// Accept eth_sendTransaction from any account without a per-tx anvil_impersonateAccount call.
		// The replay only needs each sender's balance set once (see replayBridgeExits), so this drops
		// two RPC round-trips per replayed tx.
		"--auto-impersonate",
		// Fork-backend resilience: replaying against a remote RPC triggers many lazy state fetches;
		// the upstream intermittently drops connections. Let Anvil retry those fetches with backoff
		// and a generous timeout before surfacing a Fork Error.
		"--retries", "10",
		"--fork-retry-backoff", "1000",
		"--timeout", "120000",
		// Anvil self-throttles requests to the fork backend to ~330 compute-units/s by default, which
		// caps cold-state fetches globally (independent of our concurrency) to a few exits/s. Disable
		// it so the replay is bound by the upstream RPC's real capacity, not Anvil's internal limiter.
		"--no-rate-limit",
	)
	if err := cmd.Start(); err != nil {
		return "", nil, fmt.Errorf("start anvil process: %w", err)
	}

	cleanup := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}

	anvilURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	if err := waitForAnvil(ctx, anvilURL); err != nil {
		cleanup()
		return "", nil, err
	}
	log.Infof("Anvil fork ready at %s (block %d)", anvilURL, targetBlock)
	return anvilURL, cleanup, nil
}

func waitForAnvil(ctx context.Context, anvilURL string) error {
	deadline := time.Now().Add(anvilReadyTimeout)
	for time.Now().Before(deadline) {
		if _, err := singleRPC(ctx, anvilURL, "eth_blockNumber", nil, 1); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(anvilPollInterval):
		}
	}
	return fmt.Errorf("anvil not ready after %s", anvilReadyTimeout)
}

// setSenderBalance funds sender with largeETHBalance so its bridgeAsset calls (native value + gas)
// never fail on insufficient funds. Anvil runs with --auto-impersonate, so no impersonation call is
// needed; the balance only has to be set once per sender (it stays large across that sender's exits).
func setSenderBalance(ctx context.Context, anvilURL string, sender common.Address) error {
	if _, err := singleRPC(ctx, anvilURL, "anvil_setBalance",
		[]any{sender.Hex(), largeETHBalance}, defaultRetries); err != nil {
		return fmt.Errorf("set balance: %w", err)
	}
	return nil
}

// buildLBTTokenMap builds a lookup map from (originNetwork, originToken) to wrapped address
// using the LBT entries produced by Step 0. Returns an empty map when entries is nil.
func buildLBTTokenMap(entries []LBTEntry) map[tokenOriginKey]common.Address {
	m := make(map[tokenOriginKey]common.Address, len(entries))
	for _, e := range entries {
		if e.WrappedTokenAddress != (common.Address{}) {
			m[tokenOriginKey{e.OriginNetwork, e.OriginTokenAddress}] = e.WrappedTokenAddress
		}
	}
	return m
}

// resolveTokenAddresses returns a map from origin token identity to its L2 ERC-20 address.
// Native tokens (ETH and custom gas token) are omitted — callers use isNativeBridgeExit to
// distinguish them. L2-native tokens map to their own address; external-origin tokens are
// resolved first from lbtMap (Step 0 output) and fall back to getTokenWrappedAddress on the
// bridge contract when not present.
func resolveTokenAddresses(
	ctx context.Context, anvilURL string, bridgeAddr common.Address,
	exits []*agglayertypes.BridgeExit, l2NetworkID uint32,
	gasTokenNetwork uint32, gasTokenAddress common.Address,
	lbtMap map[tokenOriginKey]common.Address,
) (map[tokenOriginKey]common.Address, error) {
	result := make(map[tokenOriginKey]common.Address)

	for _, be := range exits {
		ti := be.TokenInfo
		key := tokenOriginKey{ti.OriginNetwork, ti.OriginTokenAddress}
		if _, ok := result[key]; ok {
			continue // already resolved
		}
		// Skip native tokens — no ERC-20 address to look up.
		if isNativeBridgeExit(ti, gasTokenNetwork, gasTokenAddress) {
			continue
		}
		// L2-native token — its L2 address is the origin address itself.
		if ti.OriginNetwork == l2NetworkID {
			result[key] = ti.OriginTokenAddress
			continue
		}
		// External-origin wrapped token — prefer the LBT map (already accounts for
		// SetSovereignTokenAddress overrides), fall back to the bridge contract.
		if wrapped, ok := lbtMap[key]; ok {
			log.Debugf("token resolved from LBT: origin(network=%d addr=%s) -> %s",
				ti.OriginNetwork, ti.OriginTokenAddress.Hex(), wrapped.Hex())
			result[key] = wrapped
			continue
		}
		wrapped, err := callGetTokenWrappedAddress(ctx, anvilURL, bridgeAddr, ti.OriginNetwork, ti.OriginTokenAddress)
		if err != nil {
			return nil, fmt.Errorf("getTokenWrappedAddress(net=%d addr=%s): %w",
				ti.OriginNetwork, ti.OriginTokenAddress.Hex(), err)
		}
		if wrapped == (common.Address{}) {
			return nil, fmt.Errorf("no wrapped token on L2 for origin network=%d addr=%s",
				ti.OriginNetwork, ti.OriginTokenAddress.Hex())
		}
		log.Debugf("token resolved from contract: origin(network=%d addr=%s) -> %s",
			ti.OriginNetwork, ti.OriginTokenAddress.Hex(), wrapped.Hex())
		result[key] = wrapped
	}
	return result, nil
}

func callGetTokenWrappedAddress(
	ctx context.Context, anvilURL string, bridgeAddr common.Address,
	originNetwork uint32, originTokenAddr common.Address,
) (common.Address, error) {
	callData, err := bridgeABI.Pack("getTokenWrappedAddress", originNetwork, originTokenAddr)
	if err != nil {
		return common.Address{}, fmt.Errorf("pack getTokenWrappedAddress: %w", err)
	}
	raw, err := singleRPC(ctx, anvilURL, "eth_call", []any{
		map[string]any{"to": bridgeAddr.Hex(), "data": "0x" + hex.EncodeToString(callData)},
		"latest",
	}, defaultRetries)
	if err != nil {
		return common.Address{}, err
	}
	var hexStr string
	if err := json.Unmarshal(raw, &hexStr); err != nil {
		return common.Address{}, fmt.Errorf("parse eth_call result: %w", err)
	}
	b, err := hex.DecodeString(strings.TrimPrefix(hexStr, "0x"))
	if err != nil {
		return common.Address{}, fmt.Errorf("decode hex result: %w", err)
	}
	results, err := bridgeABI.Unpack("getTokenWrappedAddress", b)
	if err != nil {
		return common.Address{}, fmt.Errorf("unpack getTokenWrappedAddress: %w", err)
	}
	addr, ok := results[0].(common.Address)
	if !ok {
		return common.Address{}, fmt.Errorf("unexpected return type for getTokenWrappedAddress")
	}
	return addr, nil
}

// erc20NamespacedStorageLocation is the ERC-20 storage namespace for OZ v5 upgradeable tokens.
var erc20NamespacedStorageLocation = common.HexToHash(
	"0x52c63247e1f47db19d5ce0460030c497f067ca4cebf71ba98eeadabe20bace00",
)

// ensureERC20Balance checks the ERC-20 balance of account on tokenAddr.
// If insufficient it patches _balances[account] via hardhat_setStorageAt.
// Tries two storage layouts in order, verifying balanceOf after each patch:
//  1. OZ v4 non-upgradeable: _balances at mapping slot 0
//  2. OZ v5 upgradeable: _balances inside the namespaced ERC20Storage struct
func ensureERC20Balance(
	ctx context.Context, rpcURL string, tokenAddr, account common.Address, required *big.Int,
) error {
	balanceOf := func() (*big.Int, error) {
		callData := make([]byte, abiFuncSelectorSize+abiWordBytes)
		copy(callData, crypto.Keccak256([]byte("balanceOf(address)"))[:abiFuncSelectorSize])
		copy(callData[abiFuncSelectorSize:], common.LeftPadBytes(account.Bytes(), abiWordBytes))
		raw, err := singleRPC(ctx, rpcURL, "eth_call", []any{
			map[string]any{"to": tokenAddr.Hex(), "data": "0x" + hex.EncodeToString(callData)},
			"latest",
		}, defaultRetries)
		if err != nil {
			return nil, fmt.Errorf("balanceOf(%s): %w", account.Hex(), err)
		}
		var hexBal string
		if err := json.Unmarshal(raw, &hexBal); err != nil {
			return nil, fmt.Errorf("parse balanceOf result: %w", err)
		}
		bal, ok := new(big.Int).SetString(strings.TrimPrefix(hexBal, "0x"), hexBase)
		if !ok {
			return nil, fmt.Errorf("invalid balanceOf hex: %s", hexBal)
		}
		return bal, nil
	}

	bal, err := balanceOf()
	if err != nil {
		return err
	}
	if bal.Cmp(required) >= 0 {
		log.Debugf("ERC-20 %s balance of %s is sufficient (%s >= %s)", tokenAddr.Hex(), account.Hex(), bal, required)
		return nil
	}

	log.Debugf("ERC-20 %s balance of %s insufficient (%s < %s) — patching via storage slot",
		tokenAddr.Hex(), account.Hex(), bal, required)

	valueHex := "0x" + hex.EncodeToString(common.LeftPadBytes(required.Bytes(), abiWordBytes))

	// erc20BalanceSlot returns keccak256(abi.encode(account, mapSlot)),
	// which is the Solidity storage slot for _balances[account] when _balances
	// is a mapping located at mapSlot.
	erc20BalanceSlot := func(mapSlot common.Hash) string {
		preimage := append(
			common.LeftPadBytes(account.Bytes(), abiWordBytes),
			mapSlot.Bytes()...,
		)
		return "0x" + hex.EncodeToString(crypto.Keccak256(preimage))
	}

	// Try OZ v4 (slot 0) first, then OZ v5 upgradeable (namespaced storage).
	candidates := []string{
		erc20BalanceSlot(common.Hash{}),                  // OZ v4: _balances at slot 0
		erc20BalanceSlot(erc20NamespacedStorageLocation), // OZ v5 upgradeable
	}

	for _, slotHex := range candidates {
		if _, err := singleRPC(ctx, rpcURL, "hardhat_setStorageAt",
			[]any{tokenAddr.Hex(), slotHex, valueHex}, defaultRetries); err != nil {
			return fmt.Errorf("set ERC-20 balance storage slot: %w", err)
		}
		newBal, err := balanceOf()
		if err != nil {
			return err
		}
		if newBal.Cmp(required) >= 0 {
			log.Debugf("✅ ERC-20 %s balance of %s patched to %s (slot %s)",
				tokenAddr.Hex(), account.Hex(), required, slotHex)
			return nil
		}
		log.Debugf("slot %s did not update balanceOf — trying next layout", slotHex)
	}

	return fmt.Errorf("could not patch ERC-20 balance for token %s account %s: "+
		"no storage layout matched (tried OZ v4 slot-0 and OZ v5 upgradeable)",
		tokenAddr.Hex(), account.Hex())
}

// encodeERC20ApproveCallRaw ABI-encodes an ERC-20 approve(spender, amount) call.
// Selector: keccak256("approve(address,uint256)")[:4] = 0x095ea7b3
func encodeERC20ApproveCallRaw(spender common.Address, amount *big.Int) []byte {
	if amount == nil {
		amount = new(big.Int)
	}
	selector := crypto.Keccak256([]byte("approve(address,uint256)"))[:4]
	encodedSpender := common.LeftPadBytes(spender.Bytes(), abiWordBytes)
	encodedAmount := common.LeftPadBytes(amount.Bytes(), abiWordBytes)
	return append(selector, append(encodedSpender, encodedAmount...)...)
}

func encodeBridgeAssetCallRaw(
	destNetwork uint32, destAddr common.Address, amount *big.Int, tokenAddr common.Address,
) []byte {
	if amount == nil {
		amount = new(big.Int)
	}
	// forceUpdateGlobalExitRoot=false (per the Step G spec): the local exit tree leaf — and thus
	// getRoot()/NewLocalExitRoot — is inserted regardless of this flag. Setting it true would push a
	// GlobalExitRoot update (extra, variable-cost SSTOREs) on every exit, inflating gas and the
	// estimation variance for no benefit here.
	data, err := bridgeABI.Pack("bridgeAsset", destNetwork, destAddr, amount, tokenAddr, false, []byte{})
	if err != nil {
		// Static types match the ABI; Pack only fails on type mismatches, which cannot happen here.
		panic(fmt.Sprintf("pack bridgeAsset: %v", err))
	}
	return data
}

func sendAnvilTransaction(
	ctx context.Context, anvilURL string,
	from, to common.Address, value *big.Int, data []byte,
) (common.Hash, error) {
	tx := map[string]any{
		"from": from.Hex(),
		"to":   to.Hex(),
		"data": "0x" + hex.EncodeToString(data),
		// Explicit gas limit: do not let Anvil auto-estimate (see anvilTxGasLimit) — concurrent
		// estimation races the global depositCount and under-estimates, causing out-of-gas reverts.
		"gas": anvilTxGasLimit,
	}
	if value != nil && value.Sign() > 0 {
		tx["value"] = "0x" + value.Text(hexBase)
	}
	var result json.RawMessage
	var err error
	for attempt := 1; ; attempt++ {
		result, err = singleRPC(ctx, anvilURL, "eth_sendTransaction", []any{tx}, defaultRetries)
		if err == nil {
			break
		}
		// A remote fork backend can drop a fetch while Anvil resolves state for this tx; the send
		// never landed, so retrying is safe. Bounded retries with backoff; real errors fail at once.
		if !isTransientForkError(err) || attempt >= forkRetryAttempts {
			return common.Hash{}, err
		}
		log.Debugf("transient fork error sending tx (attempt %d/%d, retrying): %v", attempt, forkRetryAttempts, err)
		select {
		case <-ctx.Done():
			return common.Hash{}, ctx.Err()
		case <-time.After(forkRetryBackoff):
		}
	}
	log.Debugf("eth_sendTransaction raw result: %s", string(result))
	var txHashHex string
	if err := json.Unmarshal(result, &txHashHex); err != nil {
		return common.Hash{}, fmt.Errorf("parse tx hash: %w", err)
	}
	return common.HexToHash(txHashHex), nil
}

func waitForReceipt(ctx context.Context, anvilURL string, txHash common.Hash) ([]rpcLog, error) {
	deadline := time.Now().Add(receiptPollTimeout)
	for time.Now().Before(deadline) {
		result, err := singleRPC(ctx, anvilURL, "eth_getTransactionReceipt",
			[]any{txHash.Hex()}, defaultRetries)
		if err != nil {
			// A remote fork backend hiccups under load (dropped connection / timeout); these are
			// transient, so keep polling within the deadline instead of aborting the whole replay.
			if isTransientForkError(err) {
				log.Debugf("transient fork error polling receipt %s (retrying): %v", txHash.Hex(), err)
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(receiptPollInterval):
					continue
				}
			}
			return nil, err
		}
		if len(result) == 0 || string(result) == "null" {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(receiptPollInterval):
				continue
			}
		}
		var receipt struct {
			Status      string   `json:"status"`
			BlockNumber string   `json:"blockNumber"`
			Logs        []rpcLog `json:"logs"`
		}
		if err := json.Unmarshal(result, &receipt); err != nil {
			return nil, fmt.Errorf("parse receipt: %w", err)
		}
		if receipt.Status == "0x0" {
			reason := fetchRevertReason(ctx, anvilURL, txHash, receipt.BlockNumber)
			return nil, fmt.Errorf("transaction %s reverted: %s", txHash.Hex(), reason)
		}
		return receipt.Logs, nil
	}
	return nil, fmt.Errorf("%w of %s", errReceiptTimeout, txHash.Hex())
}

// replayedLeafFromReceipt finds the BridgeEvent log in a replayed bridgeAsset's receipt logs and
// builds the bridgesyncerlite.BridgeLeaf for it, carrying the on-chain depositCount (the canonical
// exit-tree position), the leaf content, the metadata, and the block position. txHash is the
// replaying transaction. The leaf is both inserted into the lite DB (no second fork pass) and used
// to reorder the certificate by depositCount.
func replayedLeafFromReceipt(logs []rpcLog, txHash common.Hash) (bridgesyncerlite.BridgeLeaf, error) {
	for _, l := range logs {
		event, matched, err := parseBridgeEventLog(l.Topics, l.Data)
		if err != nil {
			return bridgesyncerlite.BridgeLeaf{}, err
		}
		if !matched {
			continue
		}
		return bridgesyncerlite.BridgeLeaf{
			BlockNum:           hexToUint64(l.BlockNumber),
			BlockPos:           hexToUint64(l.LogIndex),
			LeafType:           event.LeafType,
			OriginNetwork:      event.OriginNetwork,
			OriginAddress:      event.OriginAddress,
			DestinationNetwork: event.DestinationNetwork,
			DestinationAddress: event.DestinationAddress,
			Amount:             event.Amount,
			Metadata:           event.Metadata,
			DepositCount:       event.DepositCount,
			TxHash:             txHash,
		}, nil
	}
	return bridgesyncerlite.BridgeLeaf{}, fmt.Errorf("BridgeEvent not found in receipt logs")
}

// parseBridgeEventLog decodes a single log's topics/data into a bridgeEventLog. It returns
// matched=false (with no error) when the log is not a BridgeEvent, so callers can skip it.
func parseBridgeEventLog(topics []string, data string) (*bridgeEventLog, bool, error) {
	if len(topics) == 0 || !strings.EqualFold(topics[0], bridgeEventTopicHash.Hex()) {
		return nil, false, nil
	}
	raw, err := hex.DecodeString(strings.TrimPrefix(data, "0x"))
	if err != nil {
		return nil, false, fmt.Errorf("decode BridgeEvent data: %w", err)
	}
	values, err := bridgeABI.Events["BridgeEvent"].Inputs.UnpackValues(raw)
	if err != nil {
		return nil, false, fmt.Errorf("unpack BridgeEvent: %w", err)
	}
	if len(values) != bridgeEventFields {
		return nil, false, fmt.Errorf("expected %d BridgeEvent fields, got %d", bridgeEventFields, len(values))
	}
	leafType, ok0 := values[0].(uint8)
	originNetwork, ok1 := values[1].(uint32)
	originAddress, ok2 := values[2].(common.Address)
	destNetwork, ok3 := values[3].(uint32)
	destAddress, ok4 := values[4].(common.Address)
	amount, ok5 := values[5].(*big.Int)
	metadata, ok6 := values[6].([]byte)
	depositCount, ok7 := values[7].(uint32)
	if !ok0 || !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || !ok6 || !ok7 {
		return nil, false, fmt.Errorf("unexpected field types in BridgeEvent values")
	}
	return &bridgeEventLog{
		LeafType:           leafType,
		OriginNetwork:      originNetwork,
		OriginAddress:      originAddress,
		DestinationNetwork: destNetwork,
		DestinationAddress: destAddress,
		Amount:             amount,
		Metadata:           metadata,
		DepositCount:       depositCount,
	}, true, nil
}

// knownErrors maps 4-byte selector (hex, no 0x) to signature and argument decoder.
var knownErrors = map[string]struct {
	sig    string
	decode func(args []byte) string
}{
	// LocalBalanceTreeUnderflow(uint32,address,uint256,uint256)
	"14603c01": {
		sig: "LocalBalanceTreeUnderflow(uint32,address,uint256,uint256)",
		decode: func(args []byte) string {
			if len(args) < fourABIWords {
				return ""
			}
			network := uint32(new(big.Int).SetBytes(args[0:32]).Uint64())
			addr := common.BytesToAddress(args[32:64])
			balance := new(big.Int).SetBytes(args[64:96])
			available := new(big.Int).SetBytes(args[96:128])
			return fmt.Sprintf("network=%d addr=%s balance=%s available=%s",
				network, addr.Hex(), balance, available)
		},
	},
}

// decodeRevertData tries to match the 4-byte selector of hexData against knownErrors
// and returns a human-readable string. Falls back to the raw hex if unknown.
func decodeRevertData(hexData string) string {
	data, err := hex.DecodeString(strings.TrimPrefix(hexData, "0x"))
	if err != nil || len(data) < 4 {
		return hexData
	}
	selector := hex.EncodeToString(data[:4])
	entry, ok := knownErrors[selector]
	if !ok {
		return fmt.Sprintf("unknown selector 0x%s data=%s", selector, hexData)
	}
	decoded := entry.decode(data[4:])
	if decoded == "" {
		return fmt.Sprintf("%s [0x%s] (raw: %s)", entry.sig, selector, hexData)
	}
	return fmt.Sprintf("%s [0x%s]: %s", entry.sig, selector, decoded)
}

// fetchRevertReason replays the failed transaction via eth_call at the block it was
// mined in order to extract the revert reason from the JSON-RPC error message.
func fetchRevertReason(ctx context.Context, anvilURL string, txHash common.Hash, blockNumber string) string {
	raw, err := singleRPC(ctx, anvilURL, "eth_getTransactionByHash", []any{txHash.Hex()}, 1)
	if err != nil {
		return fmt.Sprintf("(could not fetch tx: %v)", err)
	}
	var tx struct {
		From  string `json:"from"`
		To    string `json:"to"`
		Input string `json:"input"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &tx); err != nil {
		return fmt.Sprintf("(could not parse tx: %v)", err)
	}
	callParams := map[string]any{
		"from": tx.From,
		"to":   tx.To,
		"data": tx.Input,
	}
	if tx.Value != "" && tx.Value != "0x0" && tx.Value != "0x" {
		callParams["value"] = tx.Value
	}
	block := blockNumber
	if block == "" {
		block = "latest"
	}
	_, callErr := singleRPC(ctx, anvilURL, "eth_call", []any{callParams, block}, 1)
	if callErr == nil {
		return "no revert reason available"
	}
	var rpcErr *RPCExecutionError
	if errors.As(callErr, &rpcErr) && rpcErr.Data != "" {
		return decodeRevertData(rpcErr.Data)
	}
	return callErr.Error()
}

// readLocalExitRoot calls getRoot() on the bridge contract to get the LER at blockTag.
func readLocalExitRoot(
	ctx context.Context, rpcURL string, bridgeAddr common.Address, blockTag string,
) (common.Hash, error) {
	callData, err := bridgeABI.Pack("getRoot")
	if err != nil {
		return common.Hash{}, fmt.Errorf("pack getRoot: %w", err)
	}
	raw, err := singleRPC(ctx, rpcURL, "eth_call", []any{
		map[string]any{
			"to":   bridgeAddr.Hex(),
			"data": "0x" + hex.EncodeToString(callData),
		},
		blockTag,
	}, defaultRetries)
	if err != nil {
		return common.Hash{}, err
	}
	var hexStr string
	if err := json.Unmarshal(raw, &hexStr); err != nil {
		return common.Hash{}, fmt.Errorf("parse getRoot result: %w", err)
	}
	b, err := hex.DecodeString(strings.TrimPrefix(hexStr, "0x"))
	if err != nil {
		return common.Hash{}, fmt.Errorf("decode getRoot hex: %w", err)
	}
	results, err := bridgeABI.Unpack("getRoot", b)
	if err != nil {
		return common.Hash{}, fmt.Errorf("unpack getRoot: %w", err)
	}
	hash, ok := results[0].([32]byte)
	if !ok {
		return common.Hash{}, fmt.Errorf("unexpected return type for getRoot")
	}
	return common.Hash(hash), nil
}
