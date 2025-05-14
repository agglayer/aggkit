package flows

import (
	"context"
	"errors"
	"fmt"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/aggchainfep"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggoracle/chaingerreader"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/grpc"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	aggkitcommon "github.com/agglayer/aggkit/common"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"google.golang.org/grpc/codes"
)

var errNoProofBuiltYet = &aggkitcommon.GRPCError{
	Code:    codes.Unavailable,
	Message: "Proposer service has not built any proof yet",
}

// AggchainProverFlow is a struct that holds the logic for the AggchainProver prover type flow
type AggchainProverFlow struct {
	*baseFlow

	aggchainProofClient grpc.AggchainProofClientInterface
	gerQuerier          types.GERQuerier
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

// NewAggchainProverFlow returns a new instance of the AggchainProverFlow
func NewAggchainProverFlow(log types.Logger,
	maxCertSize uint,
	startL2Block uint64,
	aggkitProverClient grpc.AggchainProofClientInterface,
	storage db.AggSenderStorage,
	l1InfoTreeQuerier types.L1InfoTreeDataQuerier,
	l2BridgeQuerier types.BridgeQuerier,
	gerQuerier types.GERQuerier,
	l1Client types.EthClient) *AggchainProverFlow {
	return &AggchainProverFlow{
		aggchainProofClient: aggkitProverClient,
		gerQuerier:          gerQuerier,
		baseFlow: &baseFlow{
			log:                   log,
			l2BridgeQuerier:       l2BridgeQuerier,
			storage:               storage,
			l1InfoTreeDataQuerier: l1InfoTreeQuerier,
			maxCertSize:           maxCertSize,
			startL2Block:          startL2Block,
		},
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
	if lastSentCertificate != nil && lastSentCertificate.ToBlock+1 < a.startL2Block {
		return fmt.Errorf("gap of blocks detected: lastSentCertificate.ToBlock: %d, startL2Block: %d",
			lastSentCertificate.ToBlock, a.startL2Block)
	}
	return nil
}

// GetCertificateBuildParams returns the parameters to build a certificate
// this function is the implementation of the FlowManager interface
// What differentiates this function from the regular PP flow is that,
// if the last sent certificate is in error, we need to resend the exact same certificate
// also, it calls the aggchain prover to get the aggchain proof
func (a *AggchainProverFlow) GetCertificateBuildParams(ctx context.Context) (*types.CertificateBuildParams, error) {
	isLastSentCertificateInError, err := a.storage.IsLastSentCertificateInError()
	if err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error checking if last sent certificate is InError: %w", err)
	}

	if isLastSentCertificateInError {
		// if the last certificate was in error, we need to resend it
		lastSentCert, err := a.storage.GetLastSentCertificate()
		if err != nil {
			return nil, fmt.Errorf("aggchainProverFlow - error getting last sent certificate: %w", err)
		}

		a.log.Infof("resending the same InError certificate: %s", lastSentCert.String())

		bridges, claims, err := a.l2BridgeQuerier.GetBridgesAndClaims(
			ctx, lastSentCert.Header.FromBlock,
			lastSentCert.Header.ToBlock,
			true,
		)
		if err != nil {
			return nil, fmt.Errorf("aggchainProverFlow - error getting bridges and claims: %w", err)
		}

		buildParams := &types.CertificateBuildParams{
			FromBlock:           lastSentCert.Header.FromBlock,
			ToBlock:             lastSentCert.Header.ToBlock,
			RetryCount:          lastSentCert.Header.RetryCount + 1,
			Bridges:             bridges,
			Claims:              claims,
			LastSentCertificate: lastSentCert.Header,
			CreatedAt:           lastSentCert.Header.CreatedAt,
		}

		if lastSentCert.AggchainProof == nil {
			// this can happen if the aggsender db was deleted, so the aggsender
			// got the last sent certificate from agglayer, but in that data we do not have
			// the aggchain proof that was generated before, so we need to call the prover again

			return a.verifyBuildParamsAndGenerateProof(ctx, buildParams)
		}

		// if we have the aggchain proof, we need to set it in the build params
		// and set the root from which to prove the imported bridge exits
		// no need to call the prover again
		buildParams.AggchainProof = lastSentCert.AggchainProof
		buildParams.L1InfoTreeRootFromWhichToProve = *lastSentCert.Header.FinalizedL1InfoTreeRoot
		buildParams.L1InfoTreeLeafCount = lastSentCert.Header.L1InfoTreeLeafCount

		return buildParams, nil
	}

	buildParams, err := a.baseFlow.getCertificateBuildParamsInternal(ctx, true)
	if err != nil {
		if errors.Is(err, errNoNewBlocks) {
			// no new blocks to send a certificate
			// this is a valid case, so just return nil without error
			return nil, nil
		}

		return nil, err
	}

	return a.verifyBuildParamsAndGenerateProof(ctx, buildParams)
}

// verifyBuildParams verifies the certificate build params and returns an error if they are not valid
// it also calls the prover to get the aggchain proof
func (a *AggchainProverFlow) verifyBuildParamsAndGenerateProof(
	ctx context.Context, buildParams *types.CertificateBuildParams) (*types.CertificateBuildParams, error) {
	if err := a.verifyBuildParams(buildParams); err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error verifying build params: %w", err)
	}
	if err := a.sanityCheckNoBlockGaps(buildParams.LastSentCertificate); err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error checking for block gaps: %w", err)
	}

	lastProvenBlock := a.getLastProvenBlock(buildParams.FromBlock)

	aggchainProof, rootFromWhichToProveClaims, err := a.GenerateAggchainProof(
		ctx, lastProvenBlock, buildParams.ToBlock, buildParams.Claims)
	if err != nil {
		if errors.As(err, errNoProofBuiltYet) {
			a.log.Infof("aggchainProverFlow - no proof built yet for lastProvenBlock: %d, maxEndBlock: %d",
				lastProvenBlock, buildParams.ToBlock)
			return nil, nil
		}

		return nil, fmt.Errorf("aggchainProverFlow - error generating aggchain proof: %w", err)
	}

	a.log.Infof("aggchainProverFlow - fetched auth proof for lastProvenBlock: %d, maxEndBlock: %d "+
		"from aggchain prover. End block gotten from the prover: %d",
		lastProvenBlock, buildParams.ToBlock, aggchainProof.EndBlock)

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
	cert, err := a.buildCertificate(ctx, buildParams, buildParams.LastSentCertificate, true)
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

	return cert, nil
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
		ibe, err := a.convertClaimToImportedBridgeExit(claim)
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
	claims []bridgesync.Claim,
) (*types.AggchainProof, *treetypes.Root, error) {
	proof, leaf, root, err := a.l1InfoTreeDataQuerier.GetFinalizedL1InfoTreeData(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("aggchainProverFlow - error getting finalized L1 Info tree data: %w", err)
	}

	if err := a.l1InfoTreeDataQuerier.CheckIfClaimsArePartOfFinalizedL1InfoTree(
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

	aggchainProof, err := a.aggchainProofClient.GenerateAggchainProof(
		lastProvenBlock,
		toBlock,
		root.Hash,
		*leaf,
		agglayertypes.MerkleProof{
			Root:  root.Hash,
			Proof: proof,
		},
		injectedGERsProofs,
		importedBridgeExits,
	)
	if err != nil {
		return nil, nil, fmt.Errorf(`error fetching aggchain proof for lastProvenBlock: %d, maxEndBlock: %d: %w. 
			Message sent: 
			lastProvenBlock: %d,
			toBlock: %d,
			root.Hash: %s,
			*leaf: %+v,
			agglayertypes.MerkleProof{
				Root:  %s,
				Proof: %+v,
			},
			injectedGERsProofs: %+v,
			importedBridgeExits: %+v,
		`,
			lastProvenBlock, toBlock, err,
			lastProvenBlock,
			toBlock,
			root.Hash,
			*leaf,
			root.Hash,
			proof,
			injectedGERsProofs,
			importedBridgeExits,
		)
	}

	return aggchainProof, root, nil
}

func (a *AggchainProverFlow) getLastProvenBlock(fromBlock uint64) uint64 {
	if fromBlock == 0 {
		// if this is the first certificate, we need to start from the starting L2 block
		// that we got from the sovereign rollup
		return a.startL2Block
	}

	return fromBlock - 1
}
