package sources

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	bridgeserviceclient "github.com/agglayer/aggkit/bridgeservice/client"
	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker"
	trackertypes "github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
)

// settlementsPageSize is how many rows EarliestSettlementTxCovering asks bridge-service's
// GET /bridge/v1/settlements for per page while walking a network's settlement history. A var,
// not a const, so tests can shrink it instead of needing settlementsPageSize+1 fake rows to
// exercise pagination
var settlementsPageSize = uint32(50) //nolint:mnd

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
// or past bridge.DepositCount
func (s *CertificateSource) Covers(
	ctx context.Context, bridge *bridgetracker.BridgeInfo, ler common.Hash,
) (bool, error) {
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
) (*common.Hash, error) {
	txHash, needsLogFallback, err := s.earliestSettlementTxCoveringViaBridgeService(ctx, bridge)
	if err != nil {
		return nil, err
	}
	if !needsLogFallback {
		return txHash, nil
	}
	return s.earliestSettlementTxCoveringViaLogs(ctx, bridge, fromBlock)
}

// earliestSettlementTxCoveringViaBridgeService walks bridge.NetworkID's own bridge-service
// settlement history (GET /bridge/v1/settlements), most recent first, for the transition where
// coverage flips from true to false, returning the tx hash of the last (most recent) entry that
// still covers bridge -- the earliest settlement that does. needsLogFallback is true when that
// entry cannot be resolved through bridge-service alone: the endpoint does not exist on that
// instance yet (client.ErrNotFound), or the exact entry found carries no recorded tx hash
// (l1infotreesync.VerifyBatches.TxHash is optional, never backfilled for rows synced before
// issue #1817) -- both cases the caller resolves by falling back to L1 logs instead
func (s *CertificateSource) earliestSettlementTxCoveringViaBridgeService(
	ctx context.Context, bridge *bridgetracker.BridgeInfo,
) (txHash *common.Hash, needsLogFallback bool, err error) {
	svc, err := s.services.aggkitBridgeClientFor(bridge.NetworkID)
	if err != nil {
		return nil, false, err // transient: URL resolution failure, retried by the engine
	}

	var lastCovering *bridgeservicetypes.SettlementResponse
	pageSize := settlementsPageSize
	for pageNumber := uint32(1); ; pageNumber++ {
		page, err := svc.GetSettlements(ctx, bridgeserviceclient.GetSettlementsParams{
			PageNumber: &pageNumber, PageSize: &pageSize,
		})
		if err != nil {
			if errors.Is(err, bridgeserviceclient.ErrNotFound) {
				return nil, true, nil // bridge-service instance predates GET /bridge/v1/settlements
			}
			return nil, false, fmt.Errorf(
				"fetching settlement history page %d for network %d: %w", pageNumber, bridge.NetworkID, err)
		}

		for _, entry := range page.Settlements {
			ler := common.HexToHash(string(entry.NewLocalExitRoot))
			covers, err := s.Covers(ctx, bridge, ler)
			if err != nil {
				return nil, false, err
			}
			if !covers {
				return settlementResponseTxHash(lastCovering)
			}
			lastCovering = entry
		}

		if len(page.Settlements) < int(pageSize) {
			// reached the oldest settlement without ever finding a non-covering one: bridge has
			// been covered since the network's very first certificate -- lastCovering (the oldest
			// one seen, if any) is the earliest possible answer
			return settlementResponseTxHash(lastCovering)
		}
	}
}

// settlementResponseTxHash extracts entry's tx hash for
// earliestSettlementTxCoveringViaBridgeService: nil entry means the search never even saw a
// covering settlement (transient -- the caller only calls this once previousLER is known to
// cover, so a fresh read racing behind that is the only explanation; retried by the engine), and
// a covering entry with no recorded TxHash asks the caller to fall back to L1 logs instead
func settlementResponseTxHash(entry *bridgeservicetypes.SettlementResponse) (*common.Hash, bool, error) {
	if entry == nil {
		return nil, false, nil
	}
	if entry.TxHash == nil {
		return nil, true, nil
	}
	h := common.HexToHash(string(*entry.TxHash))
	return &h, false, nil
}

// earliestSettlementTxCoveringViaLogs is EarliestSettlementTxCovering's fallback: it reads
// VerifyBatchesTrustedAggregator logs straight off L1 (filtered to bridge's own rollupID, i.e.
// bridge.NetworkID -- the RollupManager's indexed topic for it) instead of through bridge-service,
// walking backwards from fromBlock in l1InfoTreeBackwardsSearchChunkSize chunks -- the same
// pattern SettlementSource.findEventUpdateL1InfoTreeBackwards uses -- for the transition where
// coverage flips from true to false, returning the last (most recent) log seen that still covers
func (s *CertificateSource) earliestSettlementTxCoveringViaLogs(
	ctx context.Context, bridge *bridgetracker.BridgeInfo, fromBlock uint64,
) (*common.Hash, error) {
	client, err := s.clients.RPCClientFor(ctx, 0) // VerifyBatchesTrustedAggregator is always on L1
	if err != nil {
		return nil, fmt.Errorf("resolving L1 JSON-RPC client: %w", err)
	}

	rollupIDTopic := common.BigToHash(new(big.Int).SetUint64(uint64(bridge.NetworkID)))
	var lastCoveringTxHash *common.Hash
	toBlock := fromBlock
	for {
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
			return nil, fmt.Errorf(
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
				return nil, err
			}
			if !covers {
				return lastCoveringTxHash, nil
			}
			txHash := l.TxHash
			lastCoveringTxHash = &txHash
		}

		if fromBlockChunk == 0 {
			// reached genesis without ever finding a non-covering settlement: bridge has been
			// covered since the network's very first certificate
			return lastCoveringTxHash, nil
		}
		toBlock = fromBlockChunk - 1
	}
}
