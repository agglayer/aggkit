package flows

import (
	"context"
	"errors"
	"fmt"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/aggchainfep"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/query"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/common"
)

var _ types.AggsenderBuilderFlow = (*AggchainProverBuilderFlow)(nil)

// AggchainProverBuilderFlow is a struct that holds the logic for the AggchainProver prover type flow
type AggchainProverBuilderFlow struct {
	baseFlow types.AggsenderFlowBaser

	log                   types.Logger
	storage               db.AggSenderStorage
	l1InfoTreeDataQuerier types.L1InfoTreeDataQuerier
	l2BridgeQuerier       types.BridgeQuerier

	certificateSigner     signertypes.Signer
	optimisticModeQuerier types.OptimisticModeQuerier
	aggchainProofQuerier  types.AggchainProofQuerier
	config                AggchainProverFlowConfig
	featureMaxL2Block     types.MaxL2BlockNumberLimiterInterface
}

func getL2StartBlock(sovereignRollupAddr common.Address, l1Client aggkittypes.BaseEthereumClienter) (uint64, error) {
	aggChainFEPContract, err := aggchainfep.NewAggchainfepCaller(sovereignRollupAddr, l1Client)
	if err != nil {
		return 0, fmt.Errorf("aggchainProverFlow - error creating sovereign rollup caller (%s): %w",
			sovereignRollupAddr.String(), err)
	}

	startL2Block, err := aggChainFEPContract.StartingBlockNumber(nil)
	if err != nil {
		return 0, fmt.Errorf("aggchainProverFlow - error ggChainFEPContract.StartingBlockNumber (%s): %w",
			sovereignRollupAddr.String(), err)
	}

	return startL2Block.Uint64(), nil
}

// AggchainProverFlowConfig holds the configuration for the AggchainProverFlow
type AggchainProverFlowConfig struct {
	maxL2BlockNumber uint64
}

// NewAggchainProverFlowConfigDefault returns a default configuration for the AggchainProverFlow
func NewAggchainProverFlowConfigDefault() AggchainProverFlowConfig {
	return AggchainProverFlowConfig{
		maxL2BlockNumber: 0,
	}
}

// NewAggchainProverFlowConfig creates a new AggchainProverFlowConfig with the given base flow config
func NewAggchainProverFlowConfig(
	maxL2BlockNumber uint64) AggchainProverFlowConfig {
	return AggchainProverFlowConfig{
		maxL2BlockNumber: maxL2BlockNumber,
	}
}

// NewAggchainProverBuilderFlow returns a new instance of the AggchainProverBuilderFlow injecting baseFlow instead of
// creating it
func NewAggchainProverBuilderFlow(
	log types.Logger,
	aggChainProverConfig AggchainProverFlowConfig,
	baseFlow types.AggsenderFlowBaser,
	storage db.AggSenderStorage,
	l1InfoTreeQuerier types.L1InfoTreeDataQuerier,
	l2BridgeQuerier types.BridgeQuerier,
	l1Client aggkittypes.BaseEthereumClienter,
	signer signertypes.Signer,
	optimisticModeQuerier types.OptimisticModeQuerier,
	aggchainProofQuerier types.AggchainProofQuerier,
) *AggchainProverBuilderFlow {
	feature := NewMaxL2BlockNumberLimiter(
		aggChainProverConfig.maxL2BlockNumber,
		log,
		false, // AggchainProverFlow allows to resize retry certs
		false, // AggchainProverFlow allows to send no bridges certs
	)
	return &AggchainProverBuilderFlow{
		log:                   log,
		storage:               storage,
		l1InfoTreeDataQuerier: l1InfoTreeQuerier,
		l2BridgeQuerier:       l2BridgeQuerier,
		config:                aggChainProverConfig,
		certificateSigner:     signer,
		optimisticModeQuerier: optimisticModeQuerier,
		aggchainProofQuerier:  aggchainProofQuerier,
		baseFlow:              baseFlow,
		featureMaxL2Block:     feature,
	}
}

// CheckInitialStatus checks that initial status is correct.
// For AggchainProverFlow checks that starting block and last certificate match
func (a *AggchainProverBuilderFlow) CheckInitialStatus(ctx context.Context) error {
	lastSentCertificate, err := a.storage.GetLastSentCertificateHeader()
	if err != nil {
		return fmt.Errorf("aggchainProverFlow - error getting last sent certificate: %w", err)
	}

	// we check if there are gaps between start L2 block and last sent certificate on startup
	// if there are gaps with bridge transactions, we can not allow the start of aggsender
	startL2Block := a.baseFlow.StartL2Block()

	// we need to wait for the syncer to catch up to the start L2 block (start FEP block)
	// in order to check if there are any bridge transactions in the gap
	if err := a.l2BridgeQuerier.WaitForSyncerToCatchUp(ctx, startL2Block); err != nil {
		return fmt.Errorf("aggchainProverFlow - error waiting for syncer to catch up: %w", err)
	}

	if err := a.baseFlow.VerifyBlockRangeGaps(
		ctx, lastSentCertificate, startL2Block, startL2Block); err != nil {
		return fmt.Errorf("aggchainProverFlow - error verifying block range gaps on startup. Err: %w", err)
	}

	return nil
}

// getCertificateTypeToGenerate returns the type of certificate to generate
func (a *AggchainProverBuilderFlow) getCertificateTypeToGenerate() (types.CertificateType, error) {
	// AggchainProverFlow only supports FEP certificates
	optimisticMode, err := a.optimisticModeQuerier.IsOptimisticModeOn()
	if err != nil {
		return types.CertificateTypeUnknown,
			fmt.Errorf("getCertificateTypeToGenerate - error getting optimistic mode: %w", err)
	}
	if optimisticMode {
		return types.CertificateTypeOptimistic, nil
	}
	return types.CertificateTypeFEP, nil
}

// GeneratePreBuildParams generates the pre-build parameters for the AggchainProverFlow
// Only used in aggsender validator
func (a *AggchainProverBuilderFlow) GenerateBuildParams(ctx context.Context,
	preParams *types.CertificatePreBuildParams) (*types.CertificateBuildParams, error) {
	if preParams == nil {
		return nil, fmt.Errorf("aggchainProverFlow - preParams is nil")
	}

	params, err := a.baseFlow.GenerateBuildParams(ctx, *preParams)
	if err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error generating build params: %w", err)
	}

	// we do not limit the size of the certificate in FEP flow,
	// it was already resized and limited by the prover when proposer called it
	// when building the certificate, so the block range is already limited
	// when it gets to the validator

	return params, nil
}

// GetCertificateBuildParams returns the parameters to build a certificate
// this function is the implementation of the FlowManager interface
// What differentiates this function from the regular PP flow is that,
// if the last sent certificate is in error, we need to resend the exact same certificate
// also, it calls the aggchain prover to get the aggchain proof
func (a *AggchainProverBuilderFlow) GetCertificateBuildParams(
	ctx context.Context) (*types.CertificateBuildParams, error) {
	lastSentCert, proof, err := a.storage.GetLastSentCertificateHeaderWithProofIfInError(ctx)
	if err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error checking if last sent certificate is InError: %w", err)
	}
	typeCert, err := a.getCertificateTypeToGenerate()
	if err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error getting certificate type to generate: %w", err)
	}

	if lastSentCert != nil && lastSentCert.Status.IsInError() && lastSentCert.CertType == typeCert {
		a.log.Infof("resending the same InError certificate: %s", lastSentCert.String())
		fromBlock := lastSentCert.FromBlock
		toBlock := lastSentCert.ToBlock

		lastProvenBlock := a.getLastProvenBlock(fromBlock, lastSentCert)
		if lastSentCert.FromBlock != lastProvenBlock+1 {
			a.log.Warnf("aggchainProverFlow - last sent certificate is InError and its fromBlock: %d doesn't match "+
				"lastProvenBlock: %d + 1. Check update process 😅", lastSentCert.FromBlock, lastProvenBlock)
		}

		bridges, claims, err := a.l2BridgeQuerier.GetBridgesAndClaims(
			ctx, fromBlock,
			toBlock,
		)
		if err != nil {
			return nil, fmt.Errorf("aggchainProverFlow - error getting bridges and claims: %w", err)
		}

		buildParams := &types.CertificateBuildParams{
			FromBlock:           fromBlock,
			ToBlock:             toBlock,
			RetryCount:          lastSentCert.RetryCount + 1,
			Bridges:             bridges,
			Claims:              claims,
			LastSentCertificate: lastSentCert,
			CreatedAt:           lastSentCert.CreatedAt,
			CertificateType:     typeCert,
		}
		if a.featureMaxL2Block != nil {
			// If the feature is enabled, we need to adapt the build params
			buildParams, err = a.featureMaxL2Block.AdaptCertificate(buildParams)
			if err != nil {
				return nil, fmt.Errorf("aggchainProverFlow - error adapting certificate to MaxL2Block.Err: %w", err)
			}
		}

		if proof == nil {
			// this can happen if the aggsender db was deleted, so the aggsender
			// got the last sent certificate from agglayer, but in that data we do not have
			// the aggchain proof that was generated before, so we need to call the prover again

			return a.verifyBuildParamsAndGenerateProof(ctx, buildParams)
		}

		// if we have the aggchain proof, we need to set it in the build params
		// and set the root from which to prove the imported bridge exits
		// no need to call the prover again
		buildParams.AggchainProof = proof
		buildParams.L1InfoTreeRootFromWhichToProve = *lastSentCert.FinalizedL1InfoTreeRoot
		buildParams.L1InfoTreeLeafCount = lastSentCert.L1InfoTreeLeafCount

		return buildParams, nil
	}
	// This line is just for emitting a warning
	if lastSentCert != nil && lastSentCert.Status.IsInError() && lastSentCert.CertType != typeCert {
		a.log.Warnf("aggchainProverFlow - next cert is a retry but type %s is != from current one %s. "+
			" So it going to generate a totally new certificate",
			lastSentCert.CertType, typeCert)
	}

	buildParams, err := a.baseFlow.GetCertificateBuildParamsInternal(ctx, typeCert)
	if err != nil {
		if errors.Is(err, errNoNewBlocks) {
			// no new blocks to send a certificate
			// this is a valid case, so just return nil without error
			return nil, nil
		}
		return nil, err
	}
	if a.featureMaxL2Block != nil {
		// If the feature is enabled, we need to adapt the build params
		buildParams, err = a.featureMaxL2Block.AdaptCertificate(buildParams)
		if err != nil {
			return nil, fmt.Errorf("aggchainProverFlow - error adapting certificate to MaxL2Block. Err: %w", err)
		}
	}

	lastProvenBlock := a.getLastProvenBlock(buildParams.FromBlock, lastSentCert)
	if buildParams.FromBlock != lastProvenBlock+1 {
		a.log.Infof("aggchainProverFlow - getCertificateBuildParams - setting fromBlock to %d instead of %d",
			lastProvenBlock+1, buildParams.FromBlock)
		buildParams.FromBlock = lastProvenBlock + 1
	}

	return a.verifyBuildParamsAndGenerateProof(ctx, buildParams)
}

// verifyBuildParams verifies the certificate build params and returns an error if they are not valid
// it also calls the prover to get the aggchain proof
func (a *AggchainProverBuilderFlow) verifyBuildParamsAndGenerateProof(
	ctx context.Context, buildParams *types.CertificateBuildParams) (*types.CertificateBuildParams, error) {
	if err := a.baseFlow.VerifyBuildParams(ctx, buildParams); err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error verifying build params: %w", err)
	}

	lastProvenBlock := a.getLastProvenBlock(buildParams.FromBlock, buildParams.LastSentCertificate)

	aggchainProof, rootFromWhichToProveClaims, err := a.aggchainProofQuerier.GenerateAggchainProof(
		ctx, lastProvenBlock, buildParams.ToBlock, buildParams)
	if err != nil {
		if errors.Is(err, query.ErrNoProofBuiltYet) {
			a.log.Infof("aggchainProverFlow - no proof built yet for lastProvenBlock: %d, maxEndBlock: %d",
				lastProvenBlock, buildParams.ToBlock)
			return nil, nil
		}
		errNew := fmt.Errorf("aggchainProverFlow - error generating aggchain proof: %w", err)
		return nil, errNew
	}

	a.log.Infof("aggchainProverFlow - fetched auth proof for lastProvenBlock: %d, maxEndBlock: %d "+
		"from aggchain prover. End block gotten from the prover: %d. Proof length: %d",
		lastProvenBlock, buildParams.ToBlock, aggchainProof.EndBlock, len(aggchainProof.SP1StarkProof.Proof))

	// set the root from which to generate merkle proofs for each claim
	// this is crucial since Aggchain Prover will use this root to generate the proofs as well
	buildParams.L1InfoTreeRootFromWhichToProve = rootFromWhichToProveClaims.Hash
	buildParams.AggchainProof = aggchainProof
	buildParams.L1InfoTreeLeafCount = rootFromWhichToProveClaims.Index + 1

	return adjustBlockRange(buildParams, buildParams.ToBlock, aggchainProof.EndBlock)
}

// BuildCertificate builds a certificate based on the buildParams
// this function is the implementation of the FlowManager interface
func (a *AggchainProverBuilderFlow) BuildCertificate(ctx context.Context,
	buildParams *types.CertificateBuildParams) (*agglayertypes.Certificate, error) {
	cert, err := a.baseFlow.BuildCertificate(ctx, buildParams, buildParams.LastSentCertificate, true)
	if err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error building certificate: %w", err)
	}

	if buildParams.AggchainProof != nil {
		// this case can happen only when aggsender validator calls this function
		// since the validator does not call the aggchain prover to get the proof
		cert.AggchainData = &agglayertypes.AggchainDataProof{
			Proof:          buildParams.AggchainProof.SP1StarkProof.Proof,
			Version:        buildParams.AggchainProof.SP1StarkProof.Version,
			Vkey:           buildParams.AggchainProof.SP1StarkProof.Vkey,
			AggchainParams: buildParams.AggchainProof.AggchainParams,
			Context:        buildParams.AggchainProof.Context,
		}

		cert.CustomChainData = buildParams.AggchainProof.CustomChainData
	}

	return cert, nil
}

// UpdateAggchainData updates the AggchainData field in certificate with the multisig if provided.
func (a *AggchainProverBuilderFlow) UpdateAggchainData(
	cert *agglayertypes.Certificate,
	multisig *agglayertypes.Multisig,
) error {
	if multisig == nil {
		// Multisig not enabled, nothing to do
		return nil
	}

	var proof *agglayertypes.AggchainDataProof

	switch data := cert.AggchainData.(type) {
	case *agglayertypes.AggchainDataProof:
		proof = data
	case *agglayertypes.AggchainDataMultisigWithProof:
		proof = data.AggchainProof
	default:
		return fmt.Errorf("aggchainProverFlow: AggchainData of unknown type %T received", data)
	}

	cert.AggchainData = &agglayertypes.AggchainDataMultisigWithProof{
		Multisig:      multisig,
		AggchainProof: proof,
	}

	return nil
}

// adjustBlockRange adjusts the block range of the certificate to match the range returned by the aggchain prover
func adjustBlockRange(buildParams *types.CertificateBuildParams,
	requestedToBlock, aggchainProverToBlock uint64) (*types.CertificateBuildParams, error) {
	var err error
	if requestedToBlock != aggchainProverToBlock {
		// if the toBlock was adjusted, we need to adjust the bridges and claims
		// to only include the ones in the new range that aggchain prover returned
		buildParams, err = buildParams.Range(buildParams.FromBlock, aggchainProverToBlock)
		if err != nil {
			return nil, fmt.Errorf("aggchainProverFlow - error adjusting the range of the certificate: %w", err)
		}
	}

	return buildParams, nil
}

func (a *AggchainProverBuilderFlow) getLastProvenBlock(
	fromBlock uint64, lastCertificate *types.CertificateHeader) uint64 {
	if fromBlock == 0 {
		// if this is the first certificate, we need to start from the starting L2 block
		// that we got from the sovereign rollup
		a.log.Infof("aggchainProverFlow - getLastProvenBlock - fromBlock is 0, returns startL2Block: %d",
			a.baseFlow.StartL2Block())
		return a.baseFlow.StartL2Block()
	}
	if lastCertificate != nil && lastCertificate.ToBlock < a.baseFlow.StartL2Block() {
		// if the last certificate is settled on PP, the last proven block is the starting L2 block
		a.log.Infof("aggchainProverFlow - getLastProvenBlock. Last certificate block: %d < startL2Block: %d",
			lastCertificate.ToBlock, a.baseFlow.StartL2Block())
		return a.baseFlow.StartL2Block()
	}
	if fromBlock-1 < a.baseFlow.StartL2Block() {
		// if the fromBlock is less than the starting L2 block, we need to start from the starting L2 block
		a.log.Infof("aggchainProverFlow - getLastProvenBlock. FromBlock: %d < startL2Block: %d",
			fromBlock, a.baseFlow.StartL2Block())
		return a.baseFlow.StartL2Block()
	}

	return fromBlock - 1
}

// Signer returns the signer used to sign the certificate
func (a *AggchainProverBuilderFlow) Signer() signertypes.Signer {
	return a.certificateSigner
}
