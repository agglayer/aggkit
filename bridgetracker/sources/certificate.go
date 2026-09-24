package sources

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	bridgeserviceclient "github.com/agglayer/aggkit/bridgeservice/client"
	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/bridgetracker"
	trackertypes "github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
)

// verifyBatchesTrustedAggregatorDataLen is the byte length of VerifyBatchesTrustedAggregator's
// non-indexed args (numBatch uint64, stateRoot bytes32, exitRoot bytes32, each word-padded to 32
// bytes): earliestSettlementTxCoveringViaLogs decodes exitRoot straight off this without an ABI
// decoder, the same way SettlementSource decodes UpdateL1InfoTree's topics
const verifyBatchesTrustedAggregatorDataLen = 96

// CertificateHeaderClient is the slice of the agglayer client CertificateSource needs: the
// latest settled/pending certificate of a network (certificateIDFor) and a known certificate's
// current header (status, settlement tx hash — certificateHeaderFor)
type CertificateHeaderClient interface {
	GetCertificateHeader(ctx context.Context, certificateID common.Hash) (*agglayertypes.CertificateHeader, error)
	GetLatestSettledCertificateHeader(ctx context.Context, networkID uint32) (*agglayertypes.CertificateHeader, error)
	GetLatestPendingCertificateHeader(ctx context.Context, networkID uint32) (*agglayertypes.CertificateHeader, error)
}

// CertificateSource implements bridgetracker.CertificateSource over the agglayer: which
// certificate covers a bridge (certificateIDFor), and that certificate's current status
// (certificateHeaderFor)
type CertificateSource struct {
	client CertificateHeaderClient
	// services resolves bridge.NetworkID's own aggkit bridge service, used to translate a
	// certificate's NewLocalExitRoot into a deposit-count position (see rootIndexFor), and to
	// read its settlement history (see earliestSettlementTxCoveringViaBridgeService)
	services *bridgeServiceClients
	// clients resolves L1's JSON-RPC client, used to locate a settled certificate's settlement
	// tx once it is visible there (see settlementBlockInfo), and, as EarliestSettlementTxCovering's
	// fallback, to read VerifyBatchesTrustedAggregator logs directly
	clients EthClientResolver
	// rollupManagerAddress is the L1 RollupManager contract address
	// earliestSettlementTxCoveringViaLogs reads VerifyBatchesTrustedAggregator logs from, when
	// the bridge-service instance being asked predates GET /bridge/v1/settlements (see #1817)
	rollupManagerAddress common.Address
	logger               aggkitcommon.Logger
}

// NewCertificateSource returns a CertificateSource fetching certificate headers through client,
// resolving local exit root positions and settlement history through the per-network bridge
// service clients finder resolves, and locating settlement txs/logs on L1 (rollupManagerAddress)
// through clients
func NewCertificateSource(
	client CertificateHeaderClient, finder NetworkURLResolver, clients EthClientResolver,
	rollupManagerAddress common.Address, logger aggkitcommon.Logger,
) *CertificateSource {
	return &CertificateSource{
		client: client, services: newBridgeServiceClients(finder, 0), clients: clients,
		rollupManagerAddress: rollupManagerAddress, logger: logger,
	}
}

// CertificateFor implements bridgetracker.CertificateSource: it resolves the certificate that
// covers bridge (see certificateIDFor) and returns its current header, so
// domain.CertificatePendingResolver can tell when it reaches Settled
func (s *CertificateSource) CertificateFor(
	ctx context.Context, bridge *bridgetracker.BridgeInfo,
) (*trackertypes.CertificateInclusionData, error) {
	certID, err := s.certificateIDFor(ctx, bridge)
	if err != nil {
		return nil, err
	}
	if certID == nil {
		return nil, nil
	}
	return s.certificateHeaderFor(ctx, *certID)
}

// certificateIDFor resolves the certificate that covers bridge, or the most recently submitted
// one on its origin network if bridge is not covered by any certificate yet: an open,
// not-yet-covering certificate still lets StepCertificatePending surface its progress, instead
// of showing nothing at all while bridge waits for the next certificate to even open.
//
// A settled certificate is only ever returned if it actually covers bridge: unlike a pending
// one, CertificatePendingResolver treats Settled as "done", so surfacing a settled certificate
// that does not cover bridge would make the tracker think the step completed when it did not.
// A pending certificate has no such risk (it is never terminal), so it is always surfaced once
// found, whether or not it covers bridge yet.
func (s *CertificateSource) certificateIDFor(
	ctx context.Context, bridge *bridgetracker.BridgeInfo,
) (*common.Hash, error) {
	settled, err := s.client.GetLatestSettledCertificateHeader(ctx, bridge.NetworkID)
	if err != nil {
		return nil, fmt.Errorf("fetching latest settled certificate of network %d: %w", bridge.NetworkID, err)
	}
	if settled != nil {
		s.logger.Debugf("latest settled certificate of network %d -> status: %s height: %d newLER: %s id: %s ",
			bridge.NetworkID, settled.Status.String(), settled.Height, settled.NewLocalExitRoot, settled.CertificateID)
		covers, err := s.Covers(ctx, bridge, settled.NewLocalExitRoot)
		if err != nil {
			return nil, err
		}
		if covers {
			return &settled.CertificateID, nil
		}
	}

	pending, err := s.client.GetLatestPendingCertificateHeader(ctx, bridge.NetworkID)
	if err != nil {
		return nil, fmt.Errorf("fetching latest pending certificate of network %d: %w", bridge.NetworkID, err)
	}
	if pending != nil {
		return &pending.CertificateID, nil
	}

	return nil, nil // not covered by any certificate, and none is in flight either
}

// Covers implements domain.SettlementHistorySource: it reports whether ler (a settled or
// pending certificate's NewLocalExitRoot) already includes bridge -- the local exit tree is
// append-only, so this holds once ler's resolved deposit-count position (see rootIndexFor) is at
// or past bridge.DepositCount.
//
// ler == bridgesynctypes.EmptyLER (the network's default initial LER, before its first-ever
// certificate) short-circuits to false without asking the bridge service at all: that root is
// never written to the local exit tree's root table (bridgesync's processor special-cases it the
// same way, see its own sanityCheckLatestLER/handleForwardLETEvent), so rootIndexFor would 404 on
// every retry and leave the caller stuck forever instead of recognizing the empty tree covers no
// deposit at all
func (s *CertificateSource) Covers(
	ctx context.Context, bridge *bridgetracker.BridgeInfo, ler common.Hash,
) (bool, error) {
	if ler == bridgesynctypes.EmptyLER {
		return false, nil
	}
	index, err := s.rootIndexFor(ctx, bridge.NetworkID, ler)
	if err != nil {
		return false, err
	}
	return bridge.DepositCount <= index, nil
}

// rootIndexFor resolves ler's deposit-count position in networkID's local exit tree, asking
// networkID's own aggkit bridge service (which syncs that network's bridge events and tracks
// every historical root it has produced) instead of walking or syncing the tree itself
func (s *CertificateSource) rootIndexFor(ctx context.Context, networkID uint32, ler common.Hash) (uint32, error) {
	svc, err := s.services.aggkitBridgeClientFor(networkID)
	if err != nil {
		return 0, err // transient: URL resolution failure, retried by the engine
	}
	root, err := svc.GetRootByLER(ctx, networkID, ler.Hex())
	if err != nil {
		return 0, fmt.Errorf("resolving root index of LER %s on network %d: %w", ler, networkID, err)
	}
	return root.Index, nil
}

// certificateHeaderFor fetches certificateID's current header from the agglayer and maps it
// into the tracker's trackertypes.CertificateInclusionData. Once the certificate is settled,
// also resolves its settlement tx's block on L1 (see settlementBlockInfo) — nil/nil while that
// tx is not visible there yet, which CertificatePendingResolver treats as still pending
func (s *CertificateSource) certificateHeaderFor(
	ctx context.Context, certificateID common.Hash,
) (*trackertypes.CertificateInclusionData, error) {
	header, err := s.client.GetCertificateHeader(ctx, certificateID)
	if err != nil {
		return nil, fmt.Errorf("fetching certificate header %s: %w", certificateID, err)
	}

	var errMsg string
	if header.Error != nil {
		errMsg = header.Error.Error()
	}

	var blockNumber, blockTimestamp *uint64
	if header.Status.IsSettled() && header.SettlementTxHash != nil {
		blockNumber, blockTimestamp, err = s.settlementBlockInfo(ctx, *header.SettlementTxHash)
		if err != nil {
			return nil, err
		}
	}

	s.logger.Debugf("certificate %s status: %s (settlementTxHash=%s, error=%q)",
		certificateID, header.Status, header.SettlementTxHash, errMsg)
	return &trackertypes.CertificateInclusionData{
		CertificateData: trackertypes.CertificateData{
			CertificateID:    header.CertificateID,
			Status:           header.Status,
			Error:            errMsg,
			SettlementTxHash: header.SettlementTxHash,
			BlockNumber:      blockNumber,
			BlockTimestamp:   blockTimestamp,
		},
		PreviousLocalExitRoot: header.PreviousLocalExitRoot,
		NewLocalExitRoot:      header.NewLocalExitRoot,
	}, nil
}

// settlementBlockInfo resolves settlementTxHash's block number and timestamp on L1, or nil/nil
// if its receipt is not visible there yet: unlike SettlementSource (StepWaitL1SettledGER), this
// is not gated by L1 finality — it only needs to know where the tx landed, not to validate what
// it did there
func (s *CertificateSource) settlementBlockInfo(
	ctx context.Context, settlementTxHash common.Hash,
) (*uint64, *uint64, error) {
	client, err := s.clients.RPCClientFor(ctx, 0) // a certificate always settles on L1
	if err != nil {
		return nil, nil, fmt.Errorf("resolving L1 JSON-RPC client: %w", err)
	}

	receipt, err := client.TransactionReceipt(ctx, settlementTxHash)
	if errors.Is(err, ethereum.NotFound) {
		return nil, nil, nil // not mined/visible on L1 yet
	}
	if err != nil {
		return nil, nil, fmt.Errorf("fetching settlement tx receipt %s: %w", settlementTxHash, err)
	}
	if receipt.BlockNumber == nil {
		return nil, nil, nil // defensive: a mined receipt always carries one, but just in case
	}

	timestamp, err := blockTimestamp(ctx, client, receipt.BlockHash)
	if err != nil {
		return nil, nil, err
	}
	number := receipt.BlockNumber.Uint64()
	return &number, &timestamp, nil
}

// EarliestSettlementTxCovering implements domain.SettlementHistorySource (see issue #1817): it
// finds the tx hash of the earliest L1 settlement whose new local exit root already covers
// bridge -- the one right after the last settlement that does not cover it yet -- rather than
// whichever certificate certificateIDFor happens to report as "latest settled that covers" by
// the time WaitL1SettledGERResolver asks. It prefers bridge.NetworkID's own bridge-service
// history (GET /bridge/v1/settlements), falling back to reading the same
// VerifyBatchesTrustedAggregator events straight off L1 -- backwards from fromBlock, the
// currently-tracked (too-recent) certificate's own settlement block -- when that bridge-service
// instance predates the endpoint, or the exact entry it finds there carries no recorded tx hash
// (a settlement synced before that field existed either; see l1infotreesync.VerifyBatches.TxHash)
func (s *CertificateSource) EarliestSettlementTxCovering(
	ctx context.Context, bridge *bridgetracker.BridgeInfo, fromBlock uint64,
	resume *trackertypes.SettlementSearchProgress,
) (*common.Hash, *trackertypes.SettlementSearchProgress, error) {
	if resume == nil {
		txHash, needsLogFallback, err := s.earliestSettlementTxCoveringViaBridgeService(ctx, bridge)
		if err != nil {
			return nil, nil, err
		}
		if !needsLogFallback {
			return txHash, nil, nil
		}
	}
	return s.earliestSettlementTxCoveringViaLogs(ctx, bridge, fromBlock, resume)
}

// earliestSettlementTxCoveringViaBridgeService finds, within bridge.NetworkID's own
// bridge-service settlement history (GET /bridge/v1/settlements, most recent first), the oldest
// entry whose new LER still covers bridge -- the earliest settlement that does -- by binary
// search over its absolute index rather than a linear walk: the local exit tree is append-only,
// so coverage is monotone across the history (true for every entry at or after the exact
// transition, false before it), the exact shape binary search needs. A linear walk would need
// one HTTP round-trip per settlement; on a mature network with thousands of them that can far
// exceed one engine tick's own resolve timeout (see EngineConfig.ResolveTimeout), and unlike
// earliestSettlementTxCoveringViaLogs' fallback this primary path has no resumable cursor of its
// own -- binary search sidesteps the problem instead of needing one, resolving in O(log N)
// requests regardless of history size.
//
// needsLogFallback is true when the answer cannot be resolved through bridge-service alone: the
// endpoint does not exist on this instance yet (client.ErrNotFound), or the exact entry found
// carries no recorded tx hash (l1infotreesync.VerifyBatches.TxHash is optional, never backfilled
// for rows synced before issue #1817) and its own recorded BlockNumber does not resolve one
// either (see verifyBatchesTxHashAtBlock) -- both cases the caller resolves by falling back to a
// full L1 log scan instead
func (s *CertificateSource) earliestSettlementTxCoveringViaBridgeService(
	ctx context.Context, bridge *bridgetracker.BridgeInfo,
) (txHash *common.Hash, needsLogFallback bool, err error) {
	svc, err := s.services.aggkitBridgeClientFor(bridge.NetworkID)
	if err != nil {
		return nil, false, err // transient: URL resolution failure, retried by the engine
	}

	newest, count, notFound, err := fetchSettlementAt(ctx, svc, bridge.NetworkID, 0)
	switch {
	case notFound:
		return nil, true, nil // bridge-service instance predates GET /bridge/v1/settlements
	case err != nil:
		return nil, false, err
	case newest == nil:
		return nil, false, nil // no settlements synced yet at all: transient, retried by the engine
	}
	newestCovers, err := s.entryCovers(ctx, bridge, newest)
	if err != nil {
		return nil, false, err
	}
	if !newestCovers {
		// the caller only asks once fromBlock's own certificate is already known to cover bridge,
		// so the newest settlement not covering it yet is a fresh read racing behind that
		return nil, false, nil
	}

	// binary search [0, count-1] (most-recent-first order) for the largest index whose settlement
	// still covers bridge -- the oldest, i.e. earliest, one that does, right after the
	// covering/non-covering transition. Index 0 (newest) is already known to cover, from above
	answer := newest
	for lo, hi := 1, count-1; lo <= hi; {
		mid := lo + (hi-lo)/2 //nolint:mnd
		entry, _, _, err := fetchSettlementAt(ctx, svc, bridge.NetworkID, mid)
		if err != nil {
			return nil, false, err
		}
		covers, err := s.entryCovers(ctx, bridge, entry)
		if err != nil {
			return nil, false, err
		}
		if covers {
			answer = entry
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}

	if answer.TxHash != nil {
		h := common.HexToHash(string(*answer.TxHash))
		return &h, false, nil
	}
	// legacy row: no recorded tx hash, but its own BlockNumber is -- resolve the tx hash with a
	// single-block FilterLogs there instead of falling all the way back to a genesis-ward scan
	fromBlock, err := s.verifyBatchesTxHashAtBlock(ctx, bridge, answer.BlockNumber)
	if err != nil {
		return nil, false, err
	}
	if fromBlock != nil {
		return fromBlock, false, nil
	}
	return nil, true, nil // could not resolve at that exact block either: fall back to the log scan
}

// fetchSettlementAt fetches exactly the settlement at index (0 = newest) of bridge-service's
// GET /bridge/v1/settlements for networkID, via a single-entry page (page_size=1,
// page_number=index+1) -- earliestSettlementTxCoveringViaBridgeService's binary search only ever
// needs one entry at a time, never a range. notFound is true when this bridge-service instance
// predates the endpoint (HTTP 404, surfaced as client.ErrNotFound); entry is nil (with no error)
// if index is at or past count, e.g. count itself being 0 (no settlements synced yet)
func fetchSettlementAt(
	ctx context.Context, svc *bridgeserviceclient.Client, networkID uint32, index int,
) (entry *bridgeservicetypes.SettlementResponse, count int, notFound bool, err error) {
	pageNumber := uint32(index) + 1 // index is always a valid slice/count bound, never negative or huge
	pageSize := uint32(1)
	page, err := svc.GetSettlements(ctx, bridgeserviceclient.GetSettlementsParams{
		PageNumber: &pageNumber, PageSize: &pageSize,
	})
	if err != nil {
		if errors.Is(err, bridgeserviceclient.ErrNotFound) {
			return nil, 0, true, nil
		}
		return nil, 0, false, fmt.Errorf(
			"fetching settlement history entry %d for network %d: %w", index, networkID, err)
	}
	if len(page.Settlements) == 0 {
		return nil, page.Count, false, nil
	}
	return page.Settlements[0], page.Count, false, nil
}

// entryCovers is Covers applied to entry's own NewLocalExitRoot, for
// earliestSettlementTxCoveringViaBridgeService's binary search
func (s *CertificateSource) entryCovers(
	ctx context.Context, bridge *bridgetracker.BridgeInfo, entry *bridgeservicetypes.SettlementResponse,
) (bool, error) {
	return s.Covers(ctx, bridge, common.HexToHash(string(entry.NewLocalExitRoot)))
}

// verifyBatchesTxHashAtBlock resolves the tx hash of bridge.NetworkID's own
// VerifyBatchesTrustedAggregator log at exactly blockNumber, via a single-block FilterLogs --
// used when earliestSettlementTxCoveringViaBridgeService's binary search lands on a legacy row
// with a known BlockNumber but no recorded TxHash, so the exact tx resolves directly instead of
// falling all the way back to earliestSettlementTxCoveringViaLogs' genesis-ward scan. Returns
// nil (not an error) if that block does not carry a matching log either, leaving the caller free
// to fall back further
func (s *CertificateSource) verifyBatchesTxHashAtBlock(
	ctx context.Context, bridge *bridgetracker.BridgeInfo, blockNumber uint64,
) (*common.Hash, error) {
	client, err := s.clients.RPCClientFor(ctx, 0) // VerifyBatchesTrustedAggregator is always on L1
	if err != nil {
		return nil, fmt.Errorf("resolving L1 JSON-RPC client: %w", err)
	}

	rollupIDTopic := common.BigToHash(new(big.Int).SetUint64(uint64(bridge.NetworkID)))
	logs, err := client.FilterLogs(ctx, ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(blockNumber),
		ToBlock:   new(big.Int).SetUint64(blockNumber),
		Addresses: []common.Address{s.rollupManagerAddress},
		Topics:    [][]common.Hash{{verifyBatchesTrustedAggregatorSignature}, {rollupIDTopic}},
	})
	if err != nil {
		return nil, fmt.Errorf(
			"fetching VerifyBatchesTrustedAggregator log for network %d at block %d: %w",
			bridge.NetworkID, blockNumber, err)
	}
	if len(logs) == 0 {
		return nil, nil
	}
	txHash := logs[0].TxHash
	return &txHash, nil
}

// earliestSettlementTxCoveringSafetyMargin is how much time earliestSettlementTxCoveringViaLogs
// leaves itself, before ctx's own deadline (the engine's per-tick resolve timeout -- see
// EngineConfig.ResolveTimeout), to stop searching and hand back a resumable
// types.SettlementSearchProgress cleanly -- instead of racing FilterLogs itself getting cut off
// mid-flight by ctx expiring, which would surface as a plain context.DeadlineExceeded error and
// lose the cursor entirely (see issue #1817's own resumable-search fix)
const earliestSettlementTxCoveringSafetyMargin = 2 * time.Second

// earliestSettlementTxCoveringViaLogs is EarliestSettlementTxCovering's fallback: it reads
// VerifyBatchesTrustedAggregator logs straight off L1 (filtered to bridge's own rollupID, i.e.
// bridge.NetworkID -- the RollupManager's indexed topic for it) instead of through bridge-service,
// walking backwards from fromBlock (or resume.NextToBlock, continuing an earlier call -- see
// resume's own doc) in l1InfoTreeBackwardsSearchChunkSize chunks -- the same pattern
// SettlementSource.findEventUpdateL1InfoTreeBackwards uses -- for the transition where coverage
// flips from true to false, returning the last (most recent) log seen that still covers.
//
// An old bridge's search can need far more chunks than fit in one engine tick (ctx's own
// deadline). Rather than let that tick's ctx cancellation abort FilterLogs mid-chunk -- which
// would surface as a hard error and discard how far the search got, forcing the next tick to
// restart from fromBlock all over again -- this checks ctx's remaining budget before starting
// each chunk (earliestSettlementTxCoveringSafetyMargin) and, once too little is left, returns
// cleanly with a *types.SettlementSearchProgress cursor instead: the caller persists it and
// passes it back as resume on the next tick, so the search always keeps moving forward
func (s *CertificateSource) earliestSettlementTxCoveringViaLogs(
	ctx context.Context, bridge *bridgetracker.BridgeInfo, fromBlock uint64,
	resume *trackertypes.SettlementSearchProgress,
) (*common.Hash, *trackertypes.SettlementSearchProgress, error) {
	client, err := s.clients.RPCClientFor(ctx, 0) // VerifyBatchesTrustedAggregator is always on L1
	if err != nil {
		return nil, nil, fmt.Errorf("resolving L1 JSON-RPC client: %w", err)
	}

	rollupIDTopic := common.BigToHash(new(big.Int).SetUint64(uint64(bridge.NetworkID)))
	toBlock := fromBlock
	var lastCoveringTxHash *common.Hash
	if resume != nil {
		toBlock = resume.NextToBlock
		lastCoveringTxHash = resume.LastCoveringTxHash
	}

	for {
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < earliestSettlementTxCoveringSafetyMargin {
			return nil, &trackertypes.SettlementSearchProgress{
				NextToBlock: toBlock, LastCoveringTxHash: lastCoveringTxHash,
			}, nil
		}

		fromBlockChunk := uint64(0)
		if toBlock >= l1InfoTreeBackwardsSearchChunkSize {
			fromBlockChunk = toBlock - l1InfoTreeBackwardsSearchChunkSize + 1
		}

		logs, err := client.FilterLogs(ctx, ethereum.FilterQuery{
			FromBlock: new(big.Int).SetUint64(fromBlockChunk),
			ToBlock:   new(big.Int).SetUint64(toBlock),
			Addresses: []common.Address{s.rollupManagerAddress},
			Topics:    [][]common.Hash{{verifyBatchesTrustedAggregatorSignature}, {rollupIDTopic}},
		})
		if err != nil {
			return nil, nil, fmt.Errorf(
				"fetching VerifyBatchesTrustedAggregator logs for network %d from block %d to %d: %w",
				bridge.NetworkID, fromBlockChunk, toBlock, err)
		}

		// FilterLogs returns logs in ascending block/log-index order; walk this chunk back to
		// front (most recent first) to find where coverage flips from true to false
		for i := len(logs) - 1; i >= 0; i-- {
			l := logs[i]
			if len(l.Data) < verifyBatchesTrustedAggregatorDataLen {
				continue // malformed/unexpected log, ignore
			}
			exitRoot := common.BytesToHash(l.Data[len(l.Data)-common.HashLength:])
			covers, err := s.Covers(ctx, bridge, exitRoot)
			if err != nil {
				return nil, nil, err
			}
			if !covers {
				return lastCoveringTxHash, nil, nil
			}
			txHash := l.TxHash
			lastCoveringTxHash = &txHash
		}

		if fromBlockChunk == 0 {
			// reached genesis without ever finding a non-covering settlement: bridge has been
			// covered since the network's very first certificate
			return lastCoveringTxHash, nil, nil
		}
		toBlock = fromBlockChunk - 1
	}
}
