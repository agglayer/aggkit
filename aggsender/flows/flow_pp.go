package flows

import (
	"context"
	"errors"
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/certificatebuild"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/types"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/common"
)

// PPFlow is a struct that holds the logic for the regular pessimistic proof flow
type PPFlow struct {
	signer                signertypes.Signer
	log                   types.Logger
	l1InfoTreeDataQuerier types.L1InfoTreeDataQuerier

	certificateBuilder  types.CertificateBuilder
	certificateVerifier types.CertificateBuildVerifier
	forceOneBridgeExit  bool
}

// NewPPFlow returns a new instance of the PPFlow
func NewPPFlow(log types.Logger,
	storage db.AggSenderStorage,
	l1InfoTreeQuerier types.L1InfoTreeDataQuerier,
	l2BridgeQuerier types.BridgeQuerier,
	certificateBuilder types.CertificateBuilder,
	certificateVerifier types.CertificateBuildVerifier,
	signer signertypes.Signer,
	forceOneBridgeExit bool) *PPFlow {
	return &PPFlow{
		signer:                signer,
		log:                   log,
		l1InfoTreeDataQuerier: l1InfoTreeQuerier,
		certificateBuilder:    certificateBuilder,
		certificateVerifier:   certificateVerifier,
		forceOneBridgeExit:    forceOneBridgeExit,
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
	buildParams, err := p.certificateBuilder.GetCertificateBuildParams(ctx, types.CertificateTypePP)
	if err != nil {
		if errors.Is(err, certificatebuild.ErrNoNewBlocks) {
			// no new blocks to send a certificate
			// this is a valid case, so just return nil without error
			return nil, nil
		}

		return nil, err
	}

	if p.forceOneBridgeExit && buildParams.NumberOfBridges() == 0 {
		// if forceOneBridgeExit is true, we need to ensure that there is at least one bridge exit
		p.log.Infof("PPFlow - forceOneBridgeExit is true, but no bridges found, "+
			"so no certificate will be built for range: %d - %d",
			buildParams.FromBlock, buildParams.ToBlock)
		return nil, nil
	}

	if buildParams.IsEmpty() {
		p.log.Infof("PPFlow - no bridges or claims found for range: %d - %d, so no certificate will be built",
			buildParams.FromBlock, buildParams.ToBlock)
		return nil, nil
	}

	if err := p.certificateVerifier.VerifyBuildParams(buildParams); err != nil {
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

// BuildCertificate builds a certificate based on the buildParams
// this function is the implementation of the FlowManager interface
func (p *PPFlow) BuildCertificate(ctx context.Context,
	buildParams *types.CertificateBuildParams) (*agglayertypes.Certificate, error) {
	certificate, err := p.certificateBuilder.BuildCertificate(ctx, buildParams, buildParams.LastSentCertificate, false)
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
