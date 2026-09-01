package sources

import (
	"context"
	"errors"
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/bridgetracker"
	trackertypes "github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
)

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
	// certificate's NewLocalExitRoot into a deposit-count position (see rootIndexFor)
	services *bridgeServiceClients
	// clients resolves L1's JSON-RPC client, used to locate a settled certificate's settlement
	// tx once it is visible there (see settlementBlockInfo)
	clients EthClientResolver
	logger  aggkitcommon.Logger
}

// NewCertificateSource returns a CertificateSource fetching certificate headers through client,
// resolving local exit root positions through the per-network bridge service clients finder
// resolves, and locating settlement txs on L1 through clients
func NewCertificateSource(
	client CertificateHeaderClient, finder NetworkURLResolver, clients EthClientResolver, logger aggkitcommon.Logger,
) *CertificateSource {
	return &CertificateSource{
		client: client, services: newBridgeServiceClients(finder), clients: clients, logger: logger,
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
		covers, err := s.covers(ctx, bridge, settled.NewLocalExitRoot)
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

// covers reports whether ler (a settled or pending certificate's NewLocalExitRoot) already
// includes bridge: the local exit tree is append-only, so this holds once ler's resolved
// deposit-count position (see rootIndexFor) is at or past bridge.DepositCount
func (s *CertificateSource) covers(
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
