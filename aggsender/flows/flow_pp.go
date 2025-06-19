package flows

import (
	"context"
	"errors"
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/db"
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

	forceOneBridgeExit bool
	featureMaxL2Block  types.MaxL2BlockNumberLimiterInterface
}

// NewPPFlow returns a new instance of the PPFlow
func NewPPFlow(log types.Logger,
	baseFlow types.AggsenderFlowBaser,
	storage db.AggSenderStorage,
	l1InfoTreeQuerier types.L1InfoTreeDataQuerier,
	l2BridgeQuerier types.BridgeQuerier,
	signer signertypes.Signer,
	forceOneBridgeExit bool,
	maxL2BlockNumber uint64) *PPFlow {
	feature := NewMaxL2BlockNumberLimiter(
		maxL2BlockNumber,
		log,
		true,
		forceOneBridgeExit,
	)
	return &PPFlow{
		signer:                signer,
		log:                   log,
		l1InfoTreeDataQuerier: l1InfoTreeQuerier,
		baseFlow:              baseFlow,
		forceOneBridgeExit:    forceOneBridgeExit,
		featureMaxL2Block:     feature,
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
	buildParams, err := p.baseFlow.GetCertificateBuildParamsInternal(ctx, types.CertificateTypePP)
	if err != nil {
		if errors.Is(err, errNoNewBlocks) {
			// no new blocks to send a certificate,
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
	if p.featureMaxL2Block != nil {
		// If the feature is enabled, we need to adapt the build params
		buildParams, err = p.featureMaxL2Block.AdaptCertificate(buildParams)
		if err != nil {
			return nil, fmt.Errorf("ppFlow - error adapting  certificate to MaxL2Block. Err: %w", err)
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
