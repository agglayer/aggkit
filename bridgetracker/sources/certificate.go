package sources

import (
	"context"
	"errors"
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/bridgetracker"
	trackertypes "github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
)

// ErrCertificateResolutionNotImplemented is returned by CertificateSource.CertificateFor until
// certificateIDFor is implemented: matching a bridge to the certificate that covers it (or the
// most recently submitted one, if it is not covered by any yet — see certificateIDFor's doc) is
// left for a follow-up change. Until then, L2-originated bridges keep being retried by the
// engine without ever advancing past the certificate steps
var ErrCertificateResolutionNotImplemented = errors.New(
	"certificate resolution not implemented yet (L2-originated bridges are not supported)")

// CertificateHeaderClient is the slice of the agglayer client CertificateSource needs: fetching
// a certificate's current header (status, settlement tx hash) by its ID
type CertificateHeaderClient interface {
	GetCertificateHeader(ctx context.Context, certificateID common.Hash) (*agglayertypes.CertificateHeader, error)
}

// CertificateSource implements bridgetracker.CertificateSource over the agglayer: which
// certificate covers a bridge (certificateIDFor), and that certificate's current status
// (certificateHeaderFor)
type CertificateSource struct {
	client CertificateHeaderClient
}

// NewCertificateSource returns a CertificateSource fetching certificate headers through client
func NewCertificateSource(client CertificateHeaderClient) *CertificateSource {
	return &CertificateSource{client: client}
}

// CertificateFor implements bridgetracker.CertificateSource: it resolves the certificate that
// covers bridge (see certificateIDFor) and returns its current header, so
// domain.CertificatePendingResolver can tell when it reaches Settled
func (s *CertificateSource) CertificateFor(
	ctx context.Context, bridge *bridgetracker.BridgeInfo,
) (*trackertypes.CertificateData, error) {
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
// TODO: not implemented yet — needs to fetch bridge.NetworkID's pending/settled certificates
// from the agglayer and check whether bridge's global index falls within their covered range
func (s *CertificateSource) certificateIDFor(
	_ context.Context, _ *bridgetracker.BridgeInfo,
) (*common.Hash, error) {
	return nil, ErrCertificateResolutionNotImplemented
}

// certificateHeaderFor fetches certificateID's current header from the agglayer and maps it
// into the tracker's trackertypes.CertificateData
func (s *CertificateSource) certificateHeaderFor(
	ctx context.Context, certificateID common.Hash,
) (*trackertypes.CertificateData, error) {
	header, err := s.client.GetCertificateHeader(ctx, certificateID)
	if err != nil {
		return nil, fmt.Errorf("fetching certificate header %s: %w", certificateID, err)
	}

	var errMsg string
	if header.Error != nil {
		errMsg = header.Error.Error()
	}
	return &trackertypes.CertificateData{
		CertificateID:    header.CertificateID,
		Status:           header.Status,
		Error:            errMsg,
		SettlementTxHash: header.SettlementTxHash,
	}, nil
}
