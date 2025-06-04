package flows

import (
	"context"
	"errors"
	"fmt"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/aggchainfep"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggoracle/chaingerreader"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	aggkitcommon "github.com/agglayer/aggkit/common"
	treetypes "github.com/agglayer/aggkit/tree/types"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/common"
	"google.golang.org/grpc/codes"
)

var errNoProofBuiltYet = &aggkitcommon.GRPCError{
	Code:    codes.Unavailable,
	Message: "Proposer service has not built any proof yet",
}

// AggchainProverFlow is a struct that holds the logic for the AggchainProver prover type flow
type AggchainProverFlow struct {
	baseFlow types.AggsenderFlowBaser

	log     types.Logger
	storage db.AggSenderStorage

	aggchainProofClient   types.AggchainProofClientInterface
	gerQuerier            types.GERQuerier
	requireNoFEPBlockGap  bool
	signer                signertypes.Signer
	optimisticModeQuerier types.OptimisticModeQuerier
	optimisticSigner      types.OptimisticSigner
}

func getL2StartBlock(sovereignRollupAddr common.Address, l1Client types.EthClient) (uint64, error) {
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

var funcNewEVMChainGERReader = chaingerreader.NewEVMChainGERReader

type AggchainProverFlowConfig struct {
	baseFlowConfig       BaseFlowConfig
	requireNoFEPBlockGap bool
}

func NewAggchainProverFlowConfigDefault() AggchainProverFlowConfig {
	return AggchainProverFlowConfig{
		baseFlowConfig:       NewBaseFlowConfigDefault(),
		requireNoFEPBlockGap: true, // default to true, can be set to false for testing purposes
	}
}

func NewAggchainProverFlowConfig(
	baseFlowConfig BaseFlowConfig,
	requireNoFEPBlockGap bool,
) AggchainProverFlowConfig {
	return AggchainProverFlowConfig{
		baseFlowConfig:       baseFlowConfig,
		requireNoFEPBlockGap: requireNoFEPBlockGap,
	}
}

// NewAggchainProverFlow returns a new instance of the AggchainProverFlow
// and create an instance of baseFlow with the given config
func NewAggchainProverFlow(log types.Logger,
	aggChainProverConfig AggchainProverFlowConfig,
	aggkitProverClient types.AggchainProofClientInterface,
	storage db.AggSenderStorage,
	l1InfoTreeQuerier types.L1InfoTreeDataQuerier,
	l2BridgeQuerier types.BridgeQuerier,
	gerQuerier types.GERQuerier,
	l1Client types.EthClient,
	signer signertypes.Signer,
	optimisticModeQuerier types.OptimisticModeQuerier,
	optimisticSigner types.OptimisticSigner,
) *AggchainProverFlow {
	return &AggchainProverFlow{
		log:                  log,
		storage:              storage,
		aggchainProofClient:  aggkitProverClient,
		gerQuerier:           gerQuerier,
		requireNoFEPBlockGap: aggChainProverConfig.requireNoFEPBlockGap,
		baseFlow: &baseFlow{
			log:                   log,
			l2BridgeQuerier:       l2BridgeQuerier,
			storage:               storage,
			l1InfoTreeDataQuerier: l1InfoTreeQuerier,
			cfg:                   aggChainProverConfig.baseFlowConfig,
		},
		signer:                signer,
		optimisticModeQuerier: optimisticModeQuerier,
		optimisticSigner:      optimisticSigner,
	}
}

// NewAggchainProverFlowInjectedBaseFlow returns a new instance of the AggchainProverFlow injecting baseFlow instead of
// creating it
func NewAggchainProverFlowInjectedBaseFlow(
	log types.Logger,
	aggChainProverConfig AggchainProverFlowConfig,
	aggkitProverClient types.AggchainProofClientInterface,
	storage db.AggSenderStorage,
	gerQuerier types.GERQuerier,
	l1Client types.EthClient,
	signer signertypes.Signer,
	optimisticModeQuerier types.OptimisticModeQuerier,
	optimisticSigner types.OptimisticSigner,
	baseFlow types.AggsenderFlowBaser,
) *AggchainProverFlow {
	return &AggchainProverFlow{
		log:                   log,
		storage:               storage,
		aggchainProofClient:   aggkitProverClient,
		gerQuerier:            gerQuerier,
		requireNoFEPBlockGap:  aggChainProverConfig.requireNoFEPBlockGap,
		signer:                signer,
		optimisticModeQuerier: optimisticModeQuerier,
		optimisticSigner:      optimisticSigner,
		baseFlow:              baseFlow,
	}
}

// CheckInitialStatus checks that initial status is correct.
// For AggchainProverFlow checks that starting block and last certificate match
func (a *AggchainProverFlow) CheckInitialStatus(ctx context.Context) error {
	lastSentCertificate, err := a.storage.GetLastSentCertificateHeader()
	if err != nil {
		return fmt.Errorf("aggchainProverFlow - error getting last sent certificate: %w", err)
	}
	return a.sanityCheckNoBlockGaps(lastSentCertificate)
}

// sanityCheckNoBlockGaps checks that there are no gaps in the block range for next certificate
// #436. Don't allow gaps updating from PP to FEP
func (a *AggchainProverFlow) sanityCheckNoBlockGaps(lastSentCertificate *types.CertificateHeader) error {
	lastSentCertficateStr := types.NilStr
	if lastSentCertificate != nil {
		lastSentCertficateStr = fmt.Sprintf("cert from:%d, to:%d", lastSentCertificate.FromBlock, lastSentCertificate.ToBlock)
	}
	msg := fmt.Sprintf("aggchainProverFlow - sanityCheckNoBlockGaps - last sent certificate: %s, startL2Block:%d",
		lastSentCertficateStr, a.baseFlow.StartL2Block())

	if lastSentCertificate != nil && lastSentCertificate.ToBlock+1 < a.baseFlow.StartL2Block() {
		err := fmt.Errorf("gap of blocks detected: lastSentCertificate.ToBlock: %d, startL2Block: %d",
			lastSentCertificate.ToBlock, a.baseFlow.StartL2Block())
		if a.requireNoFEPBlockGap {
			a.log.Error("%s. Err: %s", msg+" fails!", err.Error())
			return err
		}
		// The sanity check is disabled
		a.log.Warnf("%s. Ignoring block gaps due to RequireNoFEPBlockGap. Err: %w", msg, err)
		return nil
	}
	a.log.Infof("%s. Passed check.", msg)

	return nil
}

// getCertificateTypeToGenerate returns the type of certificate to generate
func (a *AggchainProverFlow) getCertificateTypeToGenerate() (types.CertificateType, error) {
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

// GetCertificateBuildParams returns the parameters to build a certificate
// this function is the implementation of the FlowManager interface
// What differentiates this function from the regular PP flow is that,
// if the last sent certificate is in error, we need to resend the exact same certificate
// also, it calls the aggchain prover to get the aggchain proof
func (a *AggchainProverFlow) GetCertificateBuildParams(ctx context.Context) (*types.CertificateBuildParams, error) {
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

		bridges, claims, err := a.baseFlow.L2BridgeQuerier().GetBridgesAndClaims(
			ctx, fromBlock,
			toBlock,
			true,
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
	// This line is just for emit a warning
	if lastSentCert != nil && lastSentCert.Status.IsInError() && lastSentCert.CertType != typeCert {
		a.log.Warnf("aggchainProverFlow - next cert is a retry but type %s is != from current one %s. "+
			" So it going to generate a totally new certificate",
			lastSentCert.CertType, typeCert)
	}

	buildParams, err := a.baseFlow.GetCertificateBuildParamsInternal(ctx, true, typeCert)
	if err != nil {
		if errors.Is(err, errNoNewBlocks) {
			// no new blocks to send a certificate
			// this is a valid case, so just return nil without error
			return nil, nil
		}

		return nil, err
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
func (a *AggchainProverFlow) verifyBuildParamsAndGenerateProof(
	ctx context.Context, buildParams *types.CertificateBuildParams) (*types.CertificateBuildParams, error) {
	if err := a.baseFlow.VerifyBuildParams(buildParams); err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error verifying build params: %w", err)
	}
	if err := a.sanityCheckNoBlockGaps(buildParams.LastSentCertificate); err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error checking for block gaps: %w", err)
	}

	lastProvenBlock := a.getLastProvenBlock(buildParams.FromBlock, buildParams.LastSentCertificate)

	aggchainProof, rootFromWhichToProveClaims, err := a.GenerateAggchainProof(
		ctx, lastProvenBlock, buildParams.ToBlock, buildParams)
	if err != nil {
		if errors.Is(err, errNoProofBuiltYet) {
			a.log.Infof("aggchainProverFlow - no proof built yet for lastProvenBlock: %d, maxEndBlock: %d",
				lastProvenBlock, buildParams.ToBlock)
			return nil, nil
		}

		return nil, fmt.Errorf("aggchainProverFlow - error generating aggchain proof: %w", err)
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
func (a *AggchainProverFlow) BuildCertificate(ctx context.Context,
	buildParams *types.CertificateBuildParams) (*agglayertypes.Certificate, error) {
	cert, err := a.baseFlow.BuildCertificate(ctx, buildParams, buildParams.LastSentCertificate, true)
	if err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error building certificate: %w", err)
	}

	cert.AggchainData = &agglayertypes.AggchainDataProof{
		Proof:          buildParams.AggchainProof.SP1StarkProof.Proof,
		Version:        buildParams.AggchainProof.SP1StarkProof.Version,
		Vkey:           buildParams.AggchainProof.SP1StarkProof.Vkey,
		AggchainParams: buildParams.AggchainProof.AggchainParams,
		Context:        buildParams.AggchainProof.Context,
	}

	cert.CustomChainData = buildParams.AggchainProof.CustomChainData

	signedCert, err := a.signCertificate(ctx, cert)
	if err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error signing certificate: %w", err)
	}

	return signedCert, nil
}

// getImportedBridgeExitsForProver converts the claims to imported bridge exits
// so that the aggchain prover can use them to generate the aggchain proof
func (a *AggchainProverFlow) getImportedBridgeExitsForProver(
	claims []bridgesync.Claim) ([]*agglayertypes.ImportedBridgeExitWithBlockNumber, error) {
	importedBridgeExits := make([]*agglayertypes.ImportedBridgeExitWithBlockNumber, 0, len(claims))
	for _, claim := range claims {
		// we do not need claim data and proofs here, only imported bridge exit data like:
		// - bridge exit
		// - token info
		// - global index
		ibe, err := a.baseFlow.ConvertClaimToImportedBridgeExit(claim)
		if err != nil {
			return nil, fmt.Errorf("aggchainProverFlow - error converting claim to imported bridge exit: %w", err)
		}
		importedBridgeExits = append(importedBridgeExits, &agglayertypes.ImportedBridgeExitWithBlockNumber{
			ImportedBridgeExit: ibe,
			BlockNumber:        claim.BlockNum,
		})
	}

	return importedBridgeExits, nil
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

// GenerateAggchainProof calls the aggkit prover to generate the aggchain proof for the given block range
func (a *AggchainProverFlow) GenerateAggchainProof(
	ctx context.Context,
	lastProvenBlock, toBlock uint64,
	certBuildParams *types.CertificateBuildParams,
) (*types.AggchainProof, *treetypes.Root, error) {
	proof, leaf, root, err := a.baseFlow.L1InfoTreeDataQuerier().GetFinalizedL1InfoTreeData(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("aggchainProverFlow - error getting finalized L1 Info tree data: %w", err)
	}
	claims := certBuildParams.Claims
	if err := a.baseFlow.L1InfoTreeDataQuerier().CheckIfClaimsArePartOfFinalizedL1InfoTree(
		root, claims); err != nil {
		return nil, nil, fmt.Errorf("aggchainProverFlow - error checking if claims are part of "+
			"finalized L1 Info tree root: %s with index: %d: %w", root.Hash, root.Index, err)
	}

	fromBlock := lastProvenBlock + 1
	injectedGERsProofs, err := a.gerQuerier.GetInjectedGERsProofs(ctx, root, fromBlock, toBlock)
	if err != nil {
		return nil, nil, fmt.Errorf("aggchainProverFlow - error getting injected GERs proofs: %w", err)
	}

	importedBridgeExits, err := a.getImportedBridgeExitsForProver(claims)
	if err != nil {
		return nil, nil, fmt.Errorf("aggchainProverFlow - error getting imported bridge exits for prover: %w", err)
	}
	var aggchainProof *types.AggchainProof
	request := &types.AggchainProofRequest{
		LastProvenBlock:    lastProvenBlock,
		RequestedEndBlock:  toBlock,
		L1InfoTreeRootHash: root.Hash,
		L1InfoTreeLeaf:     *leaf,
		L1InfoTreeMerkleProof: agglayertypes.MerkleProof{
			Root:  root.Hash,
			Proof: proof,
		},
		GERLeavesWithBlockNumber:           injectedGERsProofs,
		ImportedBridgeExitsWithBlockNumber: importedBridgeExits,
	}
	// It decide if must generate optimistic proof using CertType
	optimisticMode := certBuildParams.CertificateType == types.CertificateTypeOptimistic
	a.log.Infof("aggchainProverFlow - requesting proof lastProvenBlock: %d, maxEndBlock: %d, optimisticMode: %t",
		lastProvenBlock, toBlock, optimisticMode)
	if !optimisticMode {
		aggchainProof, err = a.aggchainProofClient.GenerateAggchainProof(request)
	} else {
		newLER, err := a.baseFlow.GetNewLocalExitRoot(ctx, certBuildParams)
		if err != nil {
			return nil, nil, fmt.Errorf("aggchainProverFlow - error getting new local exit root: %w", err)
		}
		sign, extraData, err := a.optimisticSigner.Sign(ctx, *request, newLER, certBuildParams)
		if err != nil {
			return nil, nil, fmt.Errorf("aggchainProverFlow - error signing aggchain proof request: %w", err)
		}
		certBuildParams.ExtraData = extraData
		a.log.Infof("aggchainProverFlow - signed aggchain proof request with new local exit root: %s",
			request.String())
		aggchainProof, err = a.aggchainProofClient.GenerateOptimisticAggchainProof(request, sign)
		if err != nil {
			return nil, nil, fmt.Errorf("aggchainProverFlow - error generating optimistic aggchain proof: %w", err)
		}
		a.log.Infof("aggchainProverFlow - generated optimistic aggchain proof for lastProvenBlock: %d, maxEndBlock: %d",
			lastProvenBlock, toBlock)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("error fetching aggchain proof (optimisticMode: %t) for lastProvenBlock: %d, "+
			"maxEndBlock: %d: %w. Message sent: %s", optimisticMode, lastProvenBlock, toBlock, err, request.String(),
		)
	}

	return aggchainProof, root, nil
}

func (a *AggchainProverFlow) getLastProvenBlock(fromBlock uint64, lastCertificate *types.CertificateHeader) uint64 {
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

// signCertificate signs a certificate with the aggsender key
func (a *AggchainProverFlow) signCertificate(
	ctx context.Context, cert *agglayertypes.Certificate) (*agglayertypes.Certificate, error) {
	aggchainData, ok := cert.AggchainData.(*agglayertypes.AggchainDataProof)
	if !ok {
		return nil, fmt.Errorf("aggchainProverFlow - signCertificate - AggchainData is not of type AggchainDataProof")
	}

	hashToSign := cert.FEPHashToSign()
	sig, err := a.signer.SignHash(ctx, hashToSign)
	if err != nil {
		return nil, err
	}

	aggchainData.Signature = sig

	a.log.Infof("aggchainProverFlow - Signed certificate. Sequencer address: %s. "+
		"New local exit root: %s. Aggchain Params: %s. Height: %d Hash signed: %s",
		a.signer.PublicAddress().String(),
		cert.NewLocalExitRoot.String(),
		aggchainData.AggchainParams.String(),
		cert.Height,
		hashToSign.String(),
	)

	return cert, nil
}
