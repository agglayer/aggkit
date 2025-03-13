package aggsender

import (
	"context"
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/signer"
	"github.com/ethereum/go-ethereum/common"
)

// ppFlow is a struct that holds the logic for the regular pessimistic proof flow
type ppFlow struct {
	*baseFlow

	signer signer.Signer
}

// newPPFlow returns a new instance of the ppFlow
func newPPFlow(log types.Logger,
	cfg Config,
	storage db.AggSenderStorage,
	l1InfoTreeSyncer types.L1InfoTreeSyncer,
	l2Syncer types.L2BridgeSyncer,
	l1Client types.EthClient,
	signer signer.Signer) *ppFlow {
	return &ppFlow{
		signer: signer,
		baseFlow: &baseFlow{
			log:              log,
			cfg:              cfg,
			l2Syncer:         l2Syncer,
			storage:          storage,
			l1Client:         l1Client,
			l1InfoTreeSyncer: l1InfoTreeSyncer,
		},
	}
}

// GetCertificateBuildParams returns the parameters to build a certificate
// this function is the implementation of the FlowManager interface
func (p *ppFlow) GetCertificateBuildParams(ctx context.Context) (*types.CertificateBuildParams, error) {
	buildParams, err := p.getCertificateBuildParamsInternal(ctx)
	if err != nil {
		return nil, err
	}

	if buildParams == nil {
		// no new blocks to send a certificate or no bridges
		return nil, nil
	}

	if len(buildParams.Claims) > 0 {
		root, err := p.getLatestFinalizedL1InfoRoot(ctx)
		if err != nil {
			return nil, fmt.Errorf("ppFlow - error getting latest finalized L1 info root: %w", err)
		}

		buildParams.L1InfoTreeRootFromWhichToProve = root
	}

	return buildParams, nil
}

// BuildCertificate builds a certificate based on the buildParams
// this function is the implementation of the FlowManager interface
func (p *ppFlow) BuildCertificate(ctx context.Context,
	buildParams *types.CertificateBuildParams) (*agglayertypes.Certificate, error) {
	certificate, err := p.buildCertificate(ctx, buildParams, buildParams.LastSentCertificate)
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
func (p *ppFlow) signCertificate(ctx context.Context,
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
