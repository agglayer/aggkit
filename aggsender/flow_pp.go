package aggsender

import (
	"context"
	"fmt"

	agglayerTypes "github.com/agglayer/aggkit/agglayer/types"
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
	signer signer.Signer) *ppFlow {
	return &ppFlow{
		signer: signer,
		baseFlow: &baseFlow{
			log:              log,
			cfg:              cfg,
			l2Syncer:         l2Syncer,
			storage:          storage,
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
		var greatestL1InfoTreeIndexUsed uint32

		for _, claim := range buildParams.Claims {
			info, err := p.l1InfoTreeSyncer.GetInfoByGlobalExitRoot(claim.GlobalExitRoot)
			if err != nil {
				return nil, fmt.Errorf("error getting info by global exit root: %s: %w", claim.GlobalExitRoot, err)
			}

			if info.L1InfoTreeIndex > greatestL1InfoTreeIndexUsed {
				greatestL1InfoTreeIndexUsed = info.L1InfoTreeIndex
			}
		}

		rt, err := p.l1InfoTreeSyncer.GetL1InfoTreeRootByIndex(ctx, greatestL1InfoTreeIndexUsed)
		if err != nil {
			return nil, fmt.Errorf("error getting L1 Info tree root by index: %d. Error: %w", greatestL1InfoTreeIndexUsed, err)
		}

		buildParams.L1InfoTreeRootFromWhichToProve = &rt
	}

	return buildParams, nil
}

// BuildCertificate builds a certificate based on the buildParams
// this function is the implementation of the FlowManager interface
func (p *ppFlow) BuildCertificate(ctx context.Context,
	buildParams *types.CertificateBuildParams) (*agglayerTypes.Certificate, error) {
	certificate, err := p.buildCertificate(ctx, buildParams, buildParams.LastSentCertificate)
	if err != nil {
		return nil, err
	}

	signedCert, err := p.signCertificate(ctx, certificate)
	if err != nil {
		return nil, fmt.Errorf("ppFlow - error signing certificate: %w", err)
	}

	return signedCert, nil
}

// signCertificate signs a certificate with the aggsender key
func (p *ppFlow) signCertificate(ctx context.Context,
	certificate *agglayerTypes.Certificate) (*agglayerTypes.Certificate, error) {
	hashToSign := certificate.HashToSign()
	sig, err := p.signer.SignHash(ctx, hashToSign)
	if err != nil {
		return nil, err
	}

	p.log.Infof("ppFlopw - Signed certificate. sequencer address: %s. New local exit root: %s Hash signed: %s",
		p.signer.PublicAddress().String(),
		common.BytesToHash(certificate.NewLocalExitRoot[:]).String(),
		hashToSign.String(),
	)

	certificate.Signature = sig

	return certificate, nil
}
