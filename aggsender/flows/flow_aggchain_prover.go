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
	"github.com/agglayer/aggkit/aggsender/l1infotreequery"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

// AggchainProverFlow is a struct that holds the logic for the AggchainProver prover type flow
type AggchainProverFlow struct {
	*baseFlow

	aggchainProofClient grpc.AggchainProofClientInterface
	gerReader           types.ChainGERReader
}

func getL2StartBlock(sovereignRollupAddr common.Address, l1Client types.EthClient) (uint64, error) {
	a, err := aggchainfep.NewAggchainfepCaller(sovereignRollupAddr, l1Client)
	if err != nil {
		return 0, fmt.Errorf("aggchainProverFlow - error creating sovereign rollup caller: %w", err)
	}

	startL2Block, err := a.StartingBlockNumber(nil)
	if err != nil {
		return 0, fmt.Errorf("aggchainProverFlow - error getting starting block number: %w", err)
	}

	return startL2Block.Uint64(), nil
}

// NewAggchainProverFlow returns a new instance of the AggchainProverFlow
func NewAggchainProverFlow(log types.Logger,
	maxCertSize uint,
	bridgeMetaDataAsHash bool,
	gerL2Address common.Address,
	sovereignRollupAddr common.Address,
	aggkitProverClient grpc.AggchainProofClientInterface,
	storage db.AggSenderStorage,
	l1InfoTreeSyncer types.L1InfoTreeSyncer,
	l2Syncer types.L2BridgeSyncer,
	l1Client types.EthClient,
	l2Client types.EthClient) (*AggchainProverFlow, error) {
	gerReader, err := chaingerreader.NewEVMChainGERReader(gerL2Address, l2Client)
	if err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error creating L2Etherman: %w", err)
	}

	startL2Block, err := getL2StartBlock(sovereignRollupAddr, l1Client)
	if err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error reading sovereign rollup: %w", err)
	}

	return &AggchainProverFlow{
		gerReader:           gerReader,
		aggchainProofClient: aggkitProverClient,
		baseFlow: &baseFlow{
			log:                   log,
			l2Syncer:              l2Syncer,
			storage:               storage,
			l1InfoTreeDataQuerier: l1infotreequery.NewL1InfoTreeDataQuerier(l1Client, l1InfoTreeSyncer),
			maxCertSize:           maxCertSize,
			startL2Block:          startL2Block,
			bridgeMetaDataAsHash:  bridgeMetaDataAsHash,
		},
	}, nil
}

// GetCertificateBuildParams returns the parameters to build a certificate
// this function is the implementation of the FlowManager interface
// What differentiates this function from the regular PP flow is that,
// if the last sent certificate is in error, we need to resend the exact same certificate
// also, it calls the aggchain prover to get the aggchain proof
func (a *AggchainProverFlow) GetCertificateBuildParams(ctx context.Context) (*types.CertificateBuildParams, error) {
	lastSentCertificateInfo, err := a.storage.GetLastSentCertificate()
	if err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error getting last sent certificate: %w", err)
	}

	if lastSentCertificateInfo != nil && lastSentCertificateInfo.Status == agglayertypes.InError {
		// if the last certificate was in error, we need to resend it
		a.log.Infof("resending the same InError certificate: %s", lastSentCertificateInfo.String())

		bridges, claims, err := a.getBridgesAndClaims(
			ctx, lastSentCertificateInfo.FromBlock,
			lastSentCertificateInfo.ToBlock,
			true,
		)
		if err != nil {
			return nil, fmt.Errorf("aggchainProverFlow - error getting bridges and claims: %w", err)
		}

		return &types.CertificateBuildParams{
			FromBlock:                      lastSentCertificateInfo.FromBlock,
			ToBlock:                        lastSentCertificateInfo.ToBlock,
			RetryCount:                     lastSentCertificateInfo.RetryCount + 1,
			Bridges:                        bridges,
			Claims:                         claims,
			LastSentCertificate:            lastSentCertificateInfo,
			CreatedAt:                      lastSentCertificateInfo.CreatedAt,
			AggchainProof:                  lastSentCertificateInfo.AggchainProof,
			L1InfoTreeRootFromWhichToProve: *lastSentCertificateInfo.FinalizedL1InfoTreeRoot,
			L1InfoTreeLeafCount:            lastSentCertificateInfo.L1InfoTreeLeafCount,
		}, nil
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

	if err := a.verifyBuildParams(buildParams); err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error verifying build params: %w", err)
	}

	lastProvenBlock := a.getLastProvenBlock(buildParams.FromBlock)

	aggchainProof, rootFromWhichToProveClaims, err := a.GenerateAggchainProof(
		ctx, lastProvenBlock, buildParams.ToBlock, buildParams.Claims)
	if err != nil {
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

	buildParams, err = adjustBlockRange(buildParams, buildParams.ToBlock, aggchainProof.EndBlock)
	if err != nil {
		return nil, err
	}

	return buildParams, nil
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

// getInjectedGERsProofs returns the proofs for the injected GERs in the given block range
// created from the last finalized L1 Info tree root
func (a *AggchainProverFlow) getInjectedGERsProofs(
	ctx context.Context,
	finalizedL1InfoTreeRoot *treetypes.Root,
	fromBlock, toBlock uint64) (map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber, error) {
	injectedGERs, err := a.gerReader.GetInjectedGERsForRange(ctx, fromBlock, toBlock)
	if err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error getting injected GERs for range %d : %d: %w",
			fromBlock, toBlock, err)
	}

	proofs := make(map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber, len(injectedGERs))

	for ger, injectedGER := range injectedGERs {
		info, proof, err := a.l1InfoTreeDataQuerier.GetProofForGER(ctx, ger, finalizedL1InfoTreeRoot.Hash)
		if err != nil {
			return nil, fmt.Errorf("aggchainProverFlow - error getting proof for GER: %s: %w", ger.String(), err)
		}

		proofs[ger] = &agglayertypes.ProvenInsertedGERWithBlockNumber{
			BlockNumber: injectedGER.BlockNumber,
			BlockIndex:  injectedGER.BlockIndex,
			ProvenInsertedGERLeaf: agglayertypes.ProvenInsertedGER{
				ProofGERToL1Root: &agglayertypes.MerkleProof{Root: finalizedL1InfoTreeRoot.Hash, Proof: proof},
				L1Leaf: &agglayertypes.L1InfoTreeLeaf{
					L1InfoTreeIndex: info.L1InfoTreeIndex,
					RollupExitRoot:  info.RollupExitRoot,
					MainnetExitRoot: info.MainnetExitRoot,
					Inner: &agglayertypes.L1InfoTreeLeafInner{
						GlobalExitRoot: info.GlobalExitRoot,
						BlockHash:      info.PreviousBlockHash,
						Timestamp:      info.Timestamp,
					},
				},
			},
		}
	}

	return proofs, nil
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
	injectedGERsProofs, err := a.getInjectedGERsProofs(ctx, root, fromBlock, toBlock)
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
