package query

import (
	"context"
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	treetypes "github.com/agglayer/aggkit/tree/types"
)

var _ types.AggchainProofQuerier = (*aggchainProofQuery)(nil)

// aggchainProofQuery encapsulates the dependencies required to perform proof queries
// against the aggregation chain. It holds references to various interfaces and services
// needed for querying proofs, converting bridge exits, accessing L1 info tree data,
// querying local exit roots, signing optimistically, and querying GER (Global Exit Root).
type aggchainProofQuery struct {
	log                         types.Logger
	aggchainProofClient         types.AggchainProofClientInterface
	importedBridgeExitConverter types.ImportedBridgeExitConverter
	l1InfoTreeDataQuerier       types.L1InfoTreeDataQuerier
	lerQuerier                  types.LocalExitRootQuery
	optimisticSigner            types.OptimisticSigner
	gerQuerier                  types.GERQuerier
}

// NewAggchainProofQuery creates a new instance of aggchainProofQuery with the provided dependencies.
func NewAggchainProofQuery(
	log types.Logger,
	aggchainproofclient types.AggchainProofClientInterface,
	importedBridgeExitConverter types.ImportedBridgeExitConverter,
	l1InfoTreeDataQuerier types.L1InfoTreeDataQuerier,
	optimisticSigner types.OptimisticSigner,
	lerQuerier types.LocalExitRootQuery,
	gerQuerier types.GERQuerier,
) *aggchainProofQuery {
	return &aggchainProofQuery{
		importedBridgeExitConverter: importedBridgeExitConverter,
		aggchainProofClient:         aggchainproofclient,
		l1InfoTreeDataQuerier:       l1InfoTreeDataQuerier,
		optimisticSigner:            optimisticSigner,
		gerQuerier:                  gerQuerier,
		lerQuerier:                  lerQuerier,
		log:                         log,
	}
}

// GenerateAggchainProof generates an Aggchain proof for a specified block range.
// It retrieves finalized L1 Info tree data, verifies that the provided claims are part of the finalized tree,
// fetches injected GERs proofs and imported bridge exits, and constructs an AggchainProofRequest.
// Depending on the certificate type, it either generates a standard or optimistic proof.
// Returns the generated AggchainProof, the L1 Info tree root, or an error if any step fails.
//
// Parameters:
//   - ctx: Context for controlling cancellation and deadlines.
//   - lastProvenBlock: The last block that has already been proven.
//   - toBlock: The target block up to which the proof should be generated.
//   - certBuildParams: Parameters required for building the certificate, including claims and certificate type.
//
// Returns:
//   - *types.AggchainProof: The generated Aggchain proof.
//   - *treetypes.Root: The root of the L1 Info tree used for the proof.
//   - error: An error if the proof generation fails at any step.
func (a *aggchainProofQuery) GenerateAggchainProof(
	ctx context.Context,
	lastProvenBlock, toBlock uint64,
	certBuildParams *types.CertificateBuildParams,
) (*types.AggchainProof, *treetypes.Root, error) {
	proof, leaf, root, err := a.l1InfoTreeDataQuerier.GetFinalizedL1InfoTreeData(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("aggchainProverFlow - error getting finalized L1 Info tree data: %w", err)
	}
	claims := certBuildParams.Claims
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
		aggchainProof, err = a.generateOptimisticAggchainProof(ctx, certBuildParams, request)
	}

	if err != nil {
		err := fmt.Errorf("aggchainProverFlow - error fetching aggchain proof (optimisticMode: %t) for lastProvenBlock: %d, "+
			"maxEndBlock: %d. Err: %w. Message sent: %s", optimisticMode, lastProvenBlock, toBlock, err, request.String(),
		)
		a.log.Error(err.Error())
		return nil, nil, err
	}

	a.log.Infof("aggchainProverFlow - aggkit-prover fetched aggchain proof (optimisticMode: %t) for lastProvenBlock: %d, "+
		"maxEndBlock: %d. root: %s.Message sent: %s", optimisticMode, lastProvenBlock, toBlock,
		root.String(), request.String())

	return aggchainProof, root, nil
}

// generateOptimisticAggchainProof fetch required data and call to aggkit-prover for optimistic aggchain proof
func (a *aggchainProofQuery) generateOptimisticAggchainProof(ctx context.Context,
	certBuildParams *types.CertificateBuildParams,
	request *types.AggchainProofRequest) (*types.AggchainProof, error) {
	if certBuildParams == nil {
		return nil, fmt.Errorf("generateOptimisticAggchainProof - certBuildParams is nil")
	}
	newLER, err := a.lerQuerier.GetNewLocalExitRoot(ctx, certBuildParams)
	if err != nil {
		return nil, fmt.Errorf("generateOptimisticAggchainProof - error getting new local exit root: %w", err)
	}
	sign, extraData, err := a.optimisticSigner.Sign(ctx, *request, newLER, certBuildParams.Claims)
	if err != nil {
		return nil, fmt.Errorf("generateOptimisticAggchainProof - error signing aggchain proof request: %w", err)
	}
	certBuildParams.ExtraData = extraData
	a.log.Infof("generateOptimisticAggchainProof - signed aggchain proof request with new local exit root: %s",
		request.String())
	aggchainProof, err := a.aggchainProofClient.GenerateOptimisticAggchainProof(request, sign)
	if err != nil {
		return nil, fmt.Errorf("generateOptimisticAggchainProof - error request aggkit-prover optimistic: %w", err)
	}
	return aggchainProof, nil
}

// getImportedBridgeExitsForProver converts the claims to imported bridge exits
// so that the aggchain prover can use them to generate the aggchain proof
func (a *aggchainProofQuery) getImportedBridgeExitsForProver(
	claims []bridgesync.Claim) ([]*agglayertypes.ImportedBridgeExitWithBlockNumber, error) {
	importedBridgeExits := make([]*agglayertypes.ImportedBridgeExitWithBlockNumber, 0, len(claims))
	for _, claim := range claims {
		// we do not need claim data and proofs here, only imported bridge exit data like:
		// - bridge exit
		// - token info
		// - global index
		ibe, err := a.importedBridgeExitConverter.ConvertToImportedBridgeExitWithoutClaimData(claim)
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
