package flows

import (
	"context"
	"errors"
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/types"
	signertypes "github.com/agglayer/go_signer/signer/types"
)

var _ types.AggsenderBuilderFlow = (*PPBuilderFlow)(nil)

// PPBuilderFlow is a struct that holds the logic for the regular pessimistic proof flow
type PPBuilderFlow struct {
	baseFlow              types.AggsenderFlowBaser
	certificateSigner     signertypes.Signer
	log                   types.Logger
	l1InfoTreeDataQuerier types.L1InfoTreeDataQuerier

	forceOneBridgeExit bool
	maxL2BlockLimiter  types.MaxL2BlockNumberLimiterInterface
}

// NewPPBuilderFlow returns a new instance of the PPBuilderFlow
func NewPPBuilderFlow(log types.Logger,
	baseFlow types.AggsenderFlowBaser,
	storage db.AggSenderStorage,
	l1InfoTreeQuerier types.L1InfoTreeDataQuerier,
	l2BridgeQuerier types.BridgeQuerier,
	signer signertypes.Signer,
	forceOneBridgeExit bool,
	maxL2BlockNumber uint64) *PPBuilderFlow {
	feature := NewMaxL2BlockNumberLimiter(
		maxL2BlockNumber,
		log,
		true,
		forceOneBridgeExit,
	)
	return &PPBuilderFlow{
		certificateSigner:     signer,
		log:                   log,
		l1InfoTreeDataQuerier: l1InfoTreeQuerier,
		baseFlow:              baseFlow,
		forceOneBridgeExit:    forceOneBridgeExit,
		maxL2BlockLimiter:     feature,
	}
}

// CheckInitialStatus checks that initial status is correct.
// For PPFlow  there are no special checks to do, so it just returns nil
func (p *PPBuilderFlow) CheckInitialStatus(ctx context.Context) error {
	return nil
}

func (p *PPBuilderFlow) GenerateBuildParams(ctx context.Context,
	preParams *types.CertificatePreBuildParams) (*types.CertificateBuildParams, error) {
	if preParams == nil {
		return nil, fmt.Errorf("ppFlow - preParams is nil")
	}
	params, err := p.baseFlow.GenerateBuildParams(ctx, *preParams)
	if err != nil {
		return nil, fmt.Errorf("ppFlow - error generating build params: %w", err)
	}
	params, err = p.baseFlow.LimitCertSize(params)
	if err != nil {
		return nil, fmt.Errorf("error applying limit size: %w", err)
	}
	return params, nil
}

// GetCertificateBuildParams returns the parameters to build a certificate
// this function is the implementation of the FlowManager interface
func (p *PPBuilderFlow) GetCertificateBuildParams(ctx context.Context) (*types.CertificateBuildParams, error) {
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
	if p.maxL2BlockLimiter != nil {
		// If the feature is enabled, we need to adapt the build params
		buildParams, err = p.maxL2BlockLimiter.AdaptCertificate(buildParams)
		if err != nil {
			return nil, fmt.Errorf("ppFlow - error adapting  certificate to MaxL2Block. Err: %w", err)
		}
	}

	if err := p.baseFlow.VerifyBuildParams(ctx, buildParams); err != nil {
		return nil, fmt.Errorf("ppFlow - error verifying build params: %w", err)
	}
	return buildParams, nil
}

// BuildCertificate builds a certificate based on the buildParams
// this function is the implementation of the FlowManager interface
func (p *PPBuilderFlow) BuildCertificate(ctx context.Context,
	buildParams *types.CertificateBuildParams) (*agglayertypes.Certificate, error) {
	certificate, err := p.baseFlow.BuildCertificate(ctx, buildParams, buildParams.LastSentCertificate, false)
	if err != nil {
		return nil, fmt.Errorf("ppFlow - error building certificate: %w", err)
	}

	return certificate, nil
}

// UpdateAggchainData updates the AggchainData field in certificate with the multisig if needed
func (p *PPBuilderFlow) UpdateAggchainData(
	cert *agglayertypes.Certificate,
	multisig *agglayertypes.Multisig) error {
	if multisig == nil {
		// multisig not turned on, we don't need to update the certificate
		return nil
	}

	// update the aggchain data with multisig
	cert.AggchainData = &agglayertypes.AggchainDataMultisig{
		Multisig: multisig,
	}

	return nil
}

// Signer returns the signer used to sign the certificate
func (p *PPBuilderFlow) Signer() signertypes.Signer {
	return p.certificateSigner
}
