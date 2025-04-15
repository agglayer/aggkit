package flows

import (
	"context"
	"errors"
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/l1infotreequery"
	"github.com/agglayer/aggkit/aggsender/types"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/common"
)

// PPFlow is a struct that holds the logic for the regular pessimistic proof flow
type PPFlow struct {
	*baseFlow

	signer signertypes.Signer
}

// NewPPFlow returns a new instance of the PPFlow
func NewPPFlow(log types.Logger,
	maxCertSize uint,
	bridgeMetaDataAsHash bool,
	storage db.AggSenderStorage,
	l1InfoTreeSyncer types.L1InfoTreeSyncer,
	l2Syncer types.L2BridgeSyncer,
	l1Client types.EthClient,
	signer signertypes.Signer) *PPFlow {
	return &PPFlow{
		signer: signer,
		baseFlow: &baseFlow{
			log:                   log,
			l2Syncer:              l2Syncer,
			storage:               storage,
			l1InfoTreeDataQuerier: l1infotreequery.NewL1InfoTreeDataQuerier(l1Client, l1InfoTreeSyncer),
			maxCertSize:           maxCertSize,
			bridgeMetaDataAsHash:  bridgeMetaDataAsHash,
		},
	}
}

// GetCertificateBuildParams returns the parameters to build a certificate
// this function is the implementation of the FlowManager interface
func (p *PPFlow) GetCertificateBuildParams(ctx context.Context) (*types.CertificateBuildParams, error) {
	buildParams, err := p.getCertificateBuildParamsInternal(ctx, false)
	if err != nil {
		if errors.Is(err, errNoNewBlocks) {
			// no new blocks to send a certificate
			// this is a valid case, so just return nil without error
			return nil, nil
		}

		return nil, err
	}

	if buildParams == nil {
		// no new blocks to send a certificate or no bridges
		return nil, nil
	}

	if err := p.verifyBuildParams(buildParams); err != nil {
		return nil, fmt.Errorf("ppFlow - error verifying build params: %w", err)
	}

	if len(buildParams.Claims) > 0 {
		root, _, err := p.l1InfoTreeDataQuerier.GetLatestFinalizedL1InfoRoot(ctx)
		if err != nil {
			return nil, fmt.Errorf("ppFlow - error getting latest finalized L1 info root: %w", err)
		}

		buildParams.L1InfoTreeRootFromWhichToProve = root.Hash
	}

	return buildParams, nil
}

// BuildCertificate builds a certificate based on the buildParams
// this function is the implementation of the FlowManager interface
func (p *PPFlow) BuildCertificate(ctx context.Context,
	buildParams *types.CertificateBuildParams) (*agglayertypes.Certificate, error) {
	certificate, err := p.buildCertificate(ctx, buildParams, buildParams.LastSentCertificate, false)
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
	hashToSign := certificate.HashToSign()
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
