package l2gersync

import (
	"context"
	"fmt"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerger"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayergerl2"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

var (
	// event UpdateHashChainValue(bytes32 indexed newGlobalExitRoot, bytes32 indexed newHashChainValue);
	insertGEREventSignature = crypto.Keccak256Hash([]byte("UpdateHashChainValue(bytes32,bytes32)"))

	// event UpdateRemovalHashChainValue(bytes32 indexed removedGlobalExitRoot,	bytes32 indexed newRemovalHashChainValue)
	removeGEREventSignature = crypto.Keccak256Hash([]byte("UpdateRemovalHashChainValue(bytes32,bytes32)"))
)

type downloaderSovereign struct {
	*sync.EVMDownloaderImplementation
	l2GERManager       *agglayergerl2.Agglayergerl2
	l2GERAddr          common.Address
	l1InfoTreeSync     L1InfoTreeQuerier
	l1GERManager       *agglayerger.Agglayerger
	l1Client           aggkittypes.BaseEthereumClienter
	rh                 *sync.RetryHandler
	syncBlockChunkSize uint64

	// removalScanMaxRange caches the eth_getLogs block-range cap learned from an RPC "query exceeds max
	// block range" error (see scanRemovedGERs). Zero means "not learned yet". Once learned, subsequent
	// removal scans go straight to the chunked query instead of repeating the unbounded call, which
	// would otherwise fail (and log an ERROR) again with the exact same error on every retry. Only
	// touched from the single Download() goroutine, so a plain field is safe.
	removalScanMaxRange uint64

	// missingGER is the last GER found missing from the L1 info tree, missingGERHits how many times in a row it
	// has been retried (to bound the logging of the retries), missingGERSince when it was first found missing
	// and missingGERInL1 if it is known to exist in the L1 contract (a GER on L1 doesn't go away, so the L1 read
	// is not repeated). See logGERMissingFromL1InfoTree. Only touched from the single Download() goroutine, so
	// plain fields are safe.
	missingGER      common.Hash
	missingGERHits  int
	missingGERSince time.Time
	missingGERInL1  bool

	logger aggkitcommon.Logger
	// now returns the current time; it can be replaced in tests
	now func() time.Time
}

func newDownloaderSovereign(
	l2Client aggkittypes.BaseEthereumClienter,
	l2GERAddr common.Address,
	l1InfoTreeSync L1InfoTreeQuerier,
	l1Client aggkittypes.BaseEthereumClienter,
	l1GERAddr common.Address,
	rh *sync.RetryHandler,
	blockFinality aggkittypes.BlockNumberFinality,
	waitForNewBlocksPeriod time.Duration,
	syncBlockChunkSize uint64) (*downloaderSovereign, error) {
	l2GERManager, err := agglayergerl2.NewAgglayergerl2(
		l2GERAddr, l2Client)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize L2 GER manager contract: %w", err)
	}

	l1GERManager, err := agglayerger.NewAgglayerger(
		l1GERAddr, l1Client)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize L1 GER manager contract: %w", err)
	}

	d := &downloaderSovereign{
		l2GERManager:       l2GERManager,
		l2GERAddr:          l2GERAddr,
		l1InfoTreeSync:     l1InfoTreeSync,
		l1GERManager:       l1GERManager,
		l1Client:           l1Client,
		rh:                 rh,
		syncBlockChunkSize: syncBlockChunkSize,
		logger:             log.WithFields("module", "l2gersync"),
		now:                time.Now,
	}

	evmDownloader := sync.NewEVMDownloaderImplementation(
		"l2GERSync", sync.NewAdapterEthClientToMultidownloader(l2Client), blockFinality,
		waitForNewBlocksPeriod, nil, []common.Address{l2GERAddr},
		rh, nil, nil, "l2GERSync")
	// The appenders are contextual: the insert handler performs L1/L2 RPCs (L1 GER map, removal scan,
	// L2 GER map) that must observe the active download context, so a cancelled download generation
	// (e.g. on a reorg, which the driver joins before rolling back) never blocks on a stalled RPC.
	evmDownloader.InstallContextualLogAppenders(d.buildAppender(l2GERManager))

	d.EVMDownloaderImplementation = evmDownloader

	return d, nil
}

// RuntimeData returns the runtime data: chainID + addresses to query
func (d *downloaderSovereign) RuntimeData(ctx context.Context) (sync.RuntimeData, error) {
	chainID, err := d.ChainID(ctx)
	if err != nil {
		return sync.RuntimeData{}, err
	}
	return sync.RuntimeData{
		ChainID:   chainID,
		Addresses: []common.Address{d.l2GERAddr},
	}, nil
}

func (d *downloaderSovereign) Download(
	ctx context.Context, fromBlock uint64, downloadedCh chan sync.EVMBlock, _ *uint64, _ bool,
) {
	for {
		select {
		case <-ctx.Done():
			log.Debug("aborting the l2GERSync downloader...")
			close(downloadedCh)
			return
		default:
		}

		// Wait for new blocks and get current head
		latestBlock := d.WaitForNewBlocks(ctx, fromBlock)
		toBlock := min(fromBlock+d.syncBlockChunkSize-1, latestBlock)
		log.Debugf("processing chunk [%d to %d] (chunk size: %d)", fromBlock, toBlock, d.syncBlockChunkSize)

		blocks := d.GetEventsByBlockRange(ctx, fromBlock, toBlock)
		for _, block := range blocks {
			// The send must be cancellation-aware: the driver cancels a generation (e.g. on a reorg)
			// and then waits for this goroutine to return, while the consumer has already stopped.
			select {
			case <-ctx.Done():
				log.Debug("aborting the l2GERSync downloader while sending a block...")
				close(downloadedCh)
				return
			case downloadedCh <- *block:
			}
		}

		fromBlock = toBlock + 1
	}
}

// buildAppender creates the contextual log appenders for the downloader.
// It parses the logs emitted by the L2 GER manager and populates the block events
// with the corresponding events. The appenders receive the active download context.
func (d *downloaderSovereign) buildAppender(
	l2GERManager *agglayergerl2.Agglayergerl2) sync.ContextualLogAppenderMap {
	appender := make(sync.ContextualLogAppenderMap)

	appender[removeGEREventSignature] = func(_ context.Context, b *sync.EVMBlock, l types.Log) error {
		removeGEREvent, err := l2GERManager.ParseUpdateRemovalHashChainValue(l)
		if err != nil {
			return fmt.Errorf("error parsing UpdateRemovalHashChainValue event log %+v: %w", l, err)
		}

		b.Events = append(b.Events, newEvent(
			newGlobalExitRootInfo(
				removeGEREvent.RemovedGlobalExitRoot,
				0,
				b.Num,
				uint64(l.Index)),
			GEREventTypeRemove))
		return nil
	}

	appender[insertGEREventSignature] = func(ctx context.Context, b *sync.EVMBlock, l types.Log) error {
		insertGEREvent, err := l2GERManager.ParseUpdateHashChainValue(l)
		if err != nil {
			return fmt.Errorf("error parsing UpdateHashChainValue event log %+v: %w", l, err)
		}

		l1InfoTreeLeaf, err := d.l1InfoTreeSync.GetInfoByGlobalExitRoot(insertGEREvent.NewGlobalExitRoot)
		if err != nil {
			gerHash := common.Hash(insertGEREvent.NewGlobalExitRoot)

			expectedRace := d.logGERMissingFromL1InfoTree(ctx, gerHash, b.Num, err)

			if d.isGERRemovedFromL2(ctx, b.Num, gerHash) {
				d.logger.Infof("GER %s got removed from L2, skipping stale insert event", gerHash.Hex())
				d.resetMissingGER()
				return nil
			}

			if expectedRace {
				// the downloader logs the retries of the expected race at WARN, not ERROR
				return fmt.Errorf("failed to fetch l1 info tree for global exit root %s (%w): %w",
					gerHash.Hex(), sync.ErrAppendNotReadyYet, err)
			}
			return fmt.Errorf("failed to fetch l1 info tree for global exit root %s: %w",
				gerHash.Hex(), err)
		}
		d.resetMissingGER()

		gerInfo := newGlobalExitRootInfo(
			insertGEREvent.NewGlobalExitRoot,
			l1InfoTreeLeaf.L1InfoTreeIndex,
			b.Num,
			uint64(l.Index))
		timestamp := b.Timestamp
		gerInfo.Timestamp = &timestamp

		b.Events = append(b.Events, newEvent(gerInfo, GEREventTypeInsert))
		return nil
	}

	return appender
}

// missingGERCause is why a GER injected on L2 is missing from the L1 info tree, according to the L1 contract
type missingGERCause int

const (
	// missingGERL1InfoTreeBehind: the GER exists in the L1 contract, the L1 info tree syncer just hasn't
	// indexed it yet (expected race, it resolves by itself)
	missingGERL1InfoTreeBehind missingGERCause = iota
	// missingGERNotInL1: the GER doesn't exist in the L1 contract (e.g. a fake GER injected on L2)
	missingGERNotInL1
	// missingGERUnknown: the L1 contract couldn't be read
	missingGERUnknown
)

// missingGERCauseFromL1 reads the L1 GER manager globalExitRootMap to tell why gerHash is missing from the
// L1 info tree. The error is the L1 read error (cause missingGERUnknown)
func (d *downloaderSovereign) missingGERCauseFromL1(ctx context.Context,
	gerHash common.Hash) (missingGERCause, error) {
	timestamp, err := d.l1GERManager.GlobalExitRootMap(&bind.CallOpts{Pending: false, Context: ctx}, gerHash)
	if err != nil {
		return missingGERUnknown, err
	}
	if timestamp == nil || timestamp.Sign() == 0 {
		return missingGERNotInL1, nil
	}
	return missingGERL1InfoTreeBehind, nil
}

// missingGERStuckAfter is how long a GER that exists in the L1 contract can be missing from the L1 info tree
// before it is no longer considered the expected race (the L1 info tree syncer may be stuck or misconfigured).
// It is well above the lag of an L1 info tree syncer that follows the finalized block
const missingGERStuckAfter = 30 * time.Minute

// logGERMissingFromL1InfoTree logs that gerHash (inserted on L2 at blockNum) is missing from the L1 info tree,
// and returns true if it is the expected race (the GER exists in the L1 contract and the L1 info tree syncer is
// just behind, or the L1 contract can't be read and the L1 info tree is not synced yet).
// The level depends on the L1 contract: if the GER exists on L1 the syncer is behind (WARN), unless it has been
// missing for more than missingGERStuckAfter (ERROR); if it doesn't exist on L1 it may be a fake GER (ERROR). If
// the L1 contract can't be read, it falls back to the L1 info tree sync status (WARN if it is not synced yet,
// ERROR otherwise).
// The lookup is retried until it resolves, so only the first retries and then one of every
// aggkitcommon.RetryErrorLogInterval are logged at that level; the rest are logged at DEBUG, without reading
// the L1 contract again unless it is needed.
// This only decides the logging: the skip decision is entirely L2-side (see isGERRemovedFromL2).
func (d *downloaderSovereign) logGERMissingFromL1InfoTree(ctx context.Context, gerHash common.Hash,
	blockNum uint64, lookupErr error) bool {
	now := d.now()
	if gerHash != d.missingGER {
		d.resetMissingGER()
		d.missingGER = gerHash
		d.missingGERSince = now
	}
	d.missingGERHits++
	attempt := d.missingGERHits
	missingFor := now.Sub(d.missingGERSince).Round(time.Second)
	mustLog := aggkitcommon.ShouldLogRetryAtError(attempt)
	warnf, errorf := d.logger.Warnf, d.logger.Errorf
	if !mustLog {
		warnf, errorf = d.logger.Debugf, d.logger.Debugf
	}

	cause := missingGERL1InfoTreeBehind
	var l1Err error
	if !d.missingGERInL1 {
		if !mustLog {
			// Not worth an L1 call just to log at DEBUG
			d.logger.Debugf("global exit root %s (block: %d) is not in the l1 info tree yet (attempt %d): %v",
				gerHash.Hex(), blockNum, attempt, lookupErr)
			return false
		}
		cause, l1Err = d.missingGERCauseFromL1(ctx, gerHash)
		d.missingGERInL1 = cause == missingGERL1InfoTreeBehind
	}

	switch cause {
	case missingGERL1InfoTreeBehind:
		if missingFor > missingGERStuckAfter {
			errorf("global exit root %s (block: %d) exists in the L1 contract but is still not in the l1 info "+
				"tree after %s: the l1 info tree syncer may be stuck or misconfigured (attempt %d): %v",
				gerHash.Hex(), blockNum, missingFor, attempt, lookupErr)
			return false
		}
		warnf("l1 info tree not synced yet, cannot resolve global exit root %s (block: %d), it exists in "+
			"the L1 contract, will retry (attempt %d): %v", gerHash.Hex(), blockNum, attempt, lookupErr)
		return true
	case missingGERNotInL1:
		errorf("failed to fetch l1 info tree for global exit root %s (block: %d), GER not found in L1 contract "+
			"globalExitRootMap either (attempt %d): %v", gerHash.Hex(), blockNum, attempt, lookupErr)
		return false
	default: // missingGERUnknown
		upToDate, syncErr := d.l1InfoTreeSync.IsUpToDate(ctx, d.l1Client)
		switch {
		case syncErr != nil:
			errorf("failed to fetch l1 info tree for global exit root %s (block: %d); GER lookup failed in L1 "+
				"contract (%v) and could not check whether the l1 info tree is synced (%v) (attempt %d): %v",
				gerHash.Hex(), blockNum, l1Err, syncErr, attempt, lookupErr)
			return false
		case !upToDate:
			warnf("l1 info tree not synced yet, cannot resolve global exit root %s (block: %d), will retry; "+
				"GER lookup failed in L1 contract: %v (attempt %d): %v",
				gerHash.Hex(), blockNum, l1Err, attempt, lookupErr)
			return true
		default:
			errorf("failed to fetch l1 info tree for global exit root %s (block: %d); GER lookup failed in L1 "+
				"contract: %v (attempt %d): %v", gerHash.Hex(), blockNum, l1Err, attempt, lookupErr)
			return false
		}
	}
}

// resetMissingGER forgets the GER being retried, once it is resolved or skipped
func (d *downloaderSovereign) resetMissingGER() {
	d.missingGER = common.Hash{}
	d.missingGERHits = 0
	d.missingGERSince = time.Time{}
	d.missingGERInL1 = false
}

// isGERRemovedFromL2 reports whether ger has genuinely been removed on-chain. Two corroborating signals,
// both pinned to the current L2 head, are required (AND):
//
//   - (S-log) a durable UpdateRemovalHashChainValue event for ger exists anywhere from fromBlock (the
//     insert block, so an unrelated older removal can never be mistaken for this one) to latest;
//   - (S-map) the L2 globalExitRootMap entry for ger currently reads 0.
//
// Requiring both defends against the two false-unstick vectors: a transient/stale zero map read with no
// actual removal (S-log would be empty), and a removal later reversed by re-injection (S-map would be
// non-zero). Any read/scan error is treated as "not removed" (never dereferences a nil value), so the
// caller keeps retrying rather than skipping a stale insert incorrectly.
func (d *downloaderSovereign) isGERRemovedFromL2(ctx context.Context, fromBlock uint64, ger common.Hash) bool {
	removedEvents, err := d.scanRemovedGERs(ctx, fromBlock, ger)
	if err != nil {
		log.Errorf("failed to scan for GER %s removal events from block %d: %v", ger.Hex(), fromBlock, err)
		return false
	}
	if len(removedEvents) == 0 {
		return false
	}

	timestampL2, err := d.l2GERManager.GlobalExitRootMap(&bind.CallOpts{Pending: false, Context: ctx}, ger)
	if err != nil || timestampL2 == nil {
		log.Errorf("failed to read L2 globalExitRootMap for GER %s: %v", ger.Hex(), err)
		return false
	}

	return timestampL2.Cmp(common.Big0) == 0
}

// scanRemovedGERs scans for UpdateRemovalHashChainValue events matching ger from fromBlock to the
// current head. The scan is open-ended (toBlock == nil, i.e. "latest") because the insert block can be
// arbitrarily far behind the head. It adapts to the RPC provider's eth_getLogs block-range cap purely by
// parsing the "query exceeds max block range" error via aggkitcommon.ParseMaxRangeFromError - no config
// parameter involved - the same pattern used by L2EVMGERReader.GetRemovedGERsForRange and
// AgglayerBridgeL2Reader.fetchUnsetClaimsWithFallbackChunking/getUnsetClaimsInChunks.
//
// The one deviation from those siblings: this is invoked on every appender retry while a GER stays
// unresolved, and fromBlock keeps being just as far behind an ever-growing head on every retry, so their
// "always try the full range first, chunk on error" approach would repeat the doomed unbounded call -
// and its ERROR log - forever. d.removalScanMaxRange caches the learned cap across calls so that, once
// learned, later calls skip straight to the chunked path in fetchRemovedGERsChunk below.
func (d *downloaderSovereign) scanRemovedGERs(
	ctx context.Context, fromBlock uint64, ger common.Hash,
) ([]*agglayertypes.RemovedGER, error) {
	gers := [][common.HashLength]byte{ger}

	if d.removalScanMaxRange == 0 {
		removedEvents, err := filterRemovedGERs(ctx, d.l2GERManager, fromBlock, nil, gers)
		if err == nil {
			return removedEvents, nil
		}

		maxRange, isMaxRangeErr := aggkitcommon.ParseMaxRangeFromError(err.Error())
		if !isMaxRangeErr {
			return nil, err
		}
		d.removalScanMaxRange = maxRange
	}

	toBlock, headErr := d.GetLastFinalizedBlock(ctx)
	if headErr != nil {
		return nil, fmt.Errorf("failed to resolve chain head for chunked removal scan: %w", headErr)
	}
	if fromBlock > toBlock {
		// fromBlock (the insert block) is already ahead of the resolved head; nothing to scan yet.
		return nil, nil
	}

	log.Debugf("scanning for GER %s removal in chunks of max %d blocks over range [%d, %d]",
		ger.Hex(), d.removalScanMaxRange, fromBlock, toBlock)

	return aggkitcommon.ChunkedRangeQuery(ctx, fromBlock, toBlock, d.removalScanMaxRange,
		func(ctx context.Context, from, to uint64) ([]*agglayertypes.RemovedGER, error) {
			return d.fetchRemovedGERsChunk(ctx, from, to, gers)
		},
		func(all, chunk []*agglayertypes.RemovedGER) []*agglayertypes.RemovedGER {
			return append(all, chunk...)
		},
		[]*agglayertypes.RemovedGER{},
	)
}

// fetchRemovedGERsChunk fetches one [fromBlock, toBlock] chunk directly and, on a "range too large"
// error, re-learns the cap and recurses through ChunkedRangeQuery - mirroring
// AgglayerBridgeL2Reader.fetchUnsetClaimsWithFallbackChunking/getUnsetClaimsInChunks - so a chunk that is
// itself still too large (e.g. the provider's cap shrank since it was first learned) keeps adapting
// instead of failing the whole scan outright.
func (d *downloaderSovereign) fetchRemovedGERsChunk(
	ctx context.Context, fromBlock, toBlock uint64, gers [][common.HashLength]byte,
) ([]*agglayertypes.RemovedGER, error) {
	removedEvents, err := filterRemovedGERs(ctx, d.l2GERManager, fromBlock, &toBlock, gers)
	if err == nil {
		return removedEvents, nil
	}

	maxRange, isMaxRangeErr := aggkitcommon.ParseMaxRangeFromError(err.Error())
	if !isMaxRangeErr {
		return nil, err
	}
	d.removalScanMaxRange = maxRange

	return aggkitcommon.ChunkedRangeQuery(ctx, fromBlock, toBlock, maxRange,
		func(ctx context.Context, from, to uint64) ([]*agglayertypes.RemovedGER, error) {
			return d.fetchRemovedGERsChunk(ctx, from, to, gers)
		},
		func(all, chunk []*agglayertypes.RemovedGER) []*agglayertypes.RemovedGER {
			return append(all, chunk...)
		},
		[]*agglayertypes.RemovedGER{},
	)
}
