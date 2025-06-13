package flows

import (
	"context"
	"errors"
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/query"
	"github.com/agglayer/aggkit/aggsender/types"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/common"
)

// PPFlow is a struct that holds the logic for the regular pessimistic proof flow
type PPFlow struct {
	baseFlow              types.AggsenderFlowBaser
	signer                signertypes.Signer
	log                   types.Logger
	l1InfoTreeDataQuerier types.L1InfoTreeDataQuerier
	maxL2BlockNumber      uint64
}

// NewPPFlow returns a new instance of the PPFlow
func NewPPFlow(log types.Logger,
	baseFlow types.AggsenderFlowBaser,
	storage db.AggSenderStorage,
	l1InfoTreeQuerier types.L1InfoTreeDataQuerier,
	l2BridgeQuerier types.BridgeQuerier,
	signer signertypes.Signer,
	maxL2BlockNumber uint64) *PPFlow {
	return &PPFlow{
		signer:                signer,
		log:                   log,
		l1InfoTreeDataQuerier: l1InfoTreeQuerier,
		baseFlow:              baseFlow,
		maxL2BlockNumber:      maxL2BlockNumber,
	}
}

// CheckInitialStatus checks that initial status is correct.
// For PPFlow  there are no special checks to do, so it just returns nil
func (p *PPFlow) CheckInitialStatus(ctx context.Context) error {
	return nil
}

// GetCertificateBuildParams returns the parameters to build a certificate
// this function is the implementation of the FlowManager interface
func (p *PPFlow) GetCertificateBuildParams(ctx context.Context) (*types.CertificateBuildParams, error) {
	buildParams, err := p.baseFlow.GetCertificateBuildParamsInternal(ctx, false, types.CertificateTypePP)
	if err != nil {
		if errors.Is(err, errNoNewBlocks) || errors.Is(err, query.ErrNoBridgeExits) {
			// no new blocks to send a certificate, or no bridge exits consumed
			// this is a valid case, so just return nil without error
			return nil, nil
		}

		return nil, err
	}

	// We adjust the block range to don't exceed the maxL2BlockNumber
	if p.maxL2BlockNumber > 0 && buildParams.ToBlock > p.maxL2BlockNumber {
		if buildParams.FromBlock > p.maxL2BlockNumber {
			p.noMoreCertsArePossibleDueMaxL2BlockNumber(buildParams, "perfect match")
			return nil, nil
		}

		// if the toBlock is greater than the maxL2BlockNumber, we need to adjust it
		p.log.Warnf("PPFlow - getCertificateBuildParams - adjusting the toBlock from %d to maxL2BlockNumber: %d",
			buildParams.ToBlock, p.maxL2BlockNumber)
		buildParams, err = buildParams.Range(buildParams.FromBlock, p.maxL2BlockNumber)
		if err != nil {
			return nil, fmt.Errorf("PPFlow - error adjusting the range of the certificate, due maxL2BlockNumber: %w", err)
		}
		if buildParams.IsEmpty() || buildParams.NumberOfBridges() == 0 {
			if buildParams.NumberOfClaims() > 0 {
				err = fmt.Errorf("PPFlow - Can't send cert. We have submitted all permitted certificate for maxL2BlockNumber: %d"+
					"but the current reduced range [%d to %d] has claims only but have %d of ImportedBridges",
					p.maxL2BlockNumber, buildParams.FromBlock, buildParams.ToBlock, buildParams.NumberOfClaims())
				p.log.Error(err)
				return nil, err
			} else {
				p.log.Warnf("PPFlow - Nothing to do. We have submitted all permitted certificate for maxL2BlockNumber: %d",
					p.maxL2BlockNumber)
			}
			return nil, nil
		}
	}

	if err := p.baseFlow.VerifyBuildParams(buildParams); err != nil {
		return nil, fmt.Errorf("ppFlow - error verifying build params: %w", err)
	}

	root, _, err := p.l1InfoTreeDataQuerier.GetLatestFinalizedL1InfoRoot(ctx)
	if err != nil {
		return nil, fmt.Errorf("ppFlow - error getting latest finalized L1 info root: %w", err)
	}

	buildParams.L1InfoTreeRootFromWhichToProve = root.Hash
	buildParams.L1InfoTreeLeafCount = root.Index + 1

	return buildParams, nil
}

func (p *PPFlow) noMoreCertsArePossibleDueMaxL2BlockNumber(
	cert *types.CertificateBuildParams, desc string) {
	p.log.Warnf("Nothing to do. We have submitted all permitted certificate for maxL2BlockNumber: %d. %s. Next cert: %s",
		p.maxL2BlockNumber, desc, cert.String())
	// we can stop here the aggkit if it's required
}

// BuildCertificate builds a certificate based on the buildParams
// this function is the implementation of the FlowManager interface
func (p *PPFlow) BuildCertificate(ctx context.Context,
	buildParams *types.CertificateBuildParams) (*agglayertypes.Certificate, error) {
	certificate, err := p.baseFlow.BuildCertificate(ctx, buildParams, buildParams.LastSentCertificate, false)
	if err != nil {
		return nil, fmt.Errorf("ppFlow - error building certificate: %w", err)
	}

	signedCert, err := p.signCertificate(ctx, certificate)
	if err != nil {
		return nil, fmt.Errorf("ppFlow - error signing certificate: %w", err)
	}

	return signedCert, nil
}

// signCertificate signs a certificate with the aggsender key
func (p *PPFlow) signCertificate(ctx context.Context,
	certificate *agglayertypes.Certificate) (*agglayertypes.Certificate, error) {
	hashToSign := certificate.PPHashToSign()
	sig, err := p.signer.SignHash(ctx, hashToSign)
	if err != nil {
		return nil, err
	}

	p.log.Infof("ppFlow - Signed certificate. Sequencer address: %s. New local exit root: %s Hash signed: %s",
		p.signer.PublicAddress().String(),
		common.BytesToHash(certificate.NewLocalExitRoot[:]).String(),
		hashToSign.String(),
	)

	certificate.AggchainData = &agglayertypes.AggchainDataSignature{
		Signature: sig,
	}

	return certificate, nil
}
