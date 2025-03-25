package flows

import (
	"context"
	"fmt"
	"math/big"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggoracle/chaingerreader"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/grpc"
	"github.com/agglayer/aggkit/aggsender/l1infotreequery"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/etherman"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

var finalizedBlockBigInt = big.NewInt(int64(etherman.Finalized))

// AggchainProverFlow is a struct that holds the logic for the AggchainProver prover type flow
type AggchainProverFlow struct {
	*BaseFlow

	aggchainProofClient grpc.AggchainProofClientInterface
	gerReader           types.ChainGERReader
}

// NewAggchainProverFlow returns a new instance of the aggchainProverFlow
func NewAggchainProverFlow(log types.Logger,
	maxCertSize uint,
	bridgeMetaDataAsHash bool,
	gerL2Address common.Address,
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

	return &AggchainProverFlow{
		gerReader:           gerReader,
		aggchainProofClient: aggkitProverClient,
		baseFlow: &baseFlow{
			log:                   log,
			l2Syncer:              l2Syncer,
			storage:               storage,
			l1InfoTreeDataQuerier: l1infotreequery.NewL1InfoTreeDataQuerier(l1Client, l1InfoTreeSyncer),
			maxCertSize:           maxCertSize,
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

	var (
		buildParams *types.CertificateBuildParams
	)

	if lastSentCertificateInfo != nil && lastSentCertificateInfo.Status == agglayertypes.InError {
		// if the last certificate was in error, we need to resend it
		a.log.Infof("resending the same InError certificate: %s", lastSentCertificateInfo.String())

		bridges, claims, err := a.getBridgesAndClaims(ctx, lastSentCertificateInfo.FromBlock, lastSentCertificateInfo.ToBlock)
		if err != nil {
			return nil, fmt.Errorf("aggchainProverFlow - error getting bridges and claims: %w", err)
		}

		if len(bridges) == 0 {
			// this should never happen, if it does, we need to investigate
			// (maybe someone deleted the bridge syncer db, so we might need to wait for it to catch up)
			// just keep return an error here
			return nil, fmt.Errorf("aggchainProverFlow - we have an InError certificate: %s, "+
				"but no bridges to resend the same certificate", lastSentCertificateInfo.String())
		}

		// we need to resend the same certificate
		buildParams = &types.CertificateBuildParams{
			FromBlock:           lastSentCertificateInfo.FromBlock,
			ToBlock:             lastSentCertificateInfo.ToBlock,
			RetryCount:          lastSentCertificateInfo.RetryCount + 1,
			Bridges:             bridges,
			Claims:              claims,
			LastSentCertificate: lastSentCertificateInfo,
			CreatedAt:           lastSentCertificateInfo.CreatedAt,
		}
	}

	if buildParams == nil {
		// use the old logic, where we build the new certificate
		buildParams, err = a.BaseFlow.getCertificateBuildParamsInternal(ctx)
		if err != nil {
			return nil, err
		}
	}

	if buildParams.NumberOfBridges() == 0 {
		// no bridges so no need to build the certificate
		return nil, nil
	}

	if err := a.verifyBuildParams(buildParams); err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error verifying build params: %w", err)
	}

	proof, leaf, root, err := a.l1InfoTreeDataQuerier.GetFinalizedL1InfoTreeData(ctx)
	if err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error getting finalized L1 Info tree data: %w", err)
	}

	// set the root from which to generate merkle proofs for each claim
	// this is crucial since Aggchain Prover will use this root to generate the proofs as well
	buildParams.L1InfoTreeRootFromWhichToProve = root

	if err := a.l1InfoTreeDataQuerier.CheckIfClaimsArePartOfFinalizedL1InfoTree(
		root, buildParams.Claims); err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error checking if claims are part of "+
			"finalized L1 Info tree root: %s with index: %d: %w", root.Hash, root.Index, err)
	}

	injectedGERsProofs, err := a.GetInjectedGERsProofs(ctx, root, buildParams.FromBlock, buildParams.ToBlock)
	if err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error getting injected GERs proofs: %w", err)
	}

	importedBridgeExits, err := a.GetImportedBridgeExitsForProver(buildParams.Claims)
	if err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error getting imported bridge exits for prover: %w", err)
	}

	lastProvenBlock := buildParams.FromBlock
	if lastProvenBlock > 0 {
		// if we had previous proofs, the last proven block is the one before the first block in the range
		lastProvenBlock--
	}

	aggchainProof, err := a.aggchainProofClient.GenerateAggchainProof(
		lastProvenBlock,
		buildParams.ToBlock,
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
		return nil, fmt.Errorf("aggchainProverFlow - error fetching aggchain proof for block range %d : %d : %w",
			buildParams.FromBlock, buildParams.ToBlock, err)
	}

	a.log.Infof("aggchainProverFlow - fetched auth proof: %s Range %d : %d from aggchain prover. Requested range: %d : %d",
		aggchainProof.Proof, buildParams.FromBlock, aggchainProof.EndBlock,
		buildParams.FromBlock, buildParams.ToBlock)

	buildParams.AggchainProof = aggchainProof.Proof
	buildParams.CustomChainData = aggchainProof.CustomChainData

	buildParams, err = adjustBlockRange(buildParams, buildParams.ToBlock, aggchainProof.EndBlock)
	if err != nil {
		return nil, err
	}

	if buildParams.NumberOfBridges() == 0 {
		// what can happen is that the aggchain prover returned a different range than the one requested
		// and the bridges were not in that range, so no need to build the certificate
		a.log.Infof("no bridges consumed, no need to send a certificate from block: %d to block: %d",
			buildParams.FromBlock, buildParams.ToBlock)
		return nil, nil
	}

	return buildParams, nil
}

// BuildCertificate builds a certificate based on the buildParams
// this function is the implementation of the FlowManager interface
func (a *AggchainProverFlow) BuildCertificate(ctx context.Context,
	buildParams *types.CertificateBuildParams) (*agglayertypes.Certificate, error) {
	cert, err := a.buildCertificate(ctx, buildParams, buildParams.LastSentCertificate)
	if err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error building certificate: %w", err)
	}

	cert.AggchainData = &agglayertypes.AggchainDataProof{
		Proof:          buildParams.AggchainProof,
		AggchainParams: common.Hash{},           // TODO - after prover proto is updated, we need to update this
		Context:        make(map[string][]byte), // TODO - after prover proto is updated, we need to update this
	}

	cert.CustomChainData = buildParams.CustomChainData

	return cert, nil
}

// GetInjectedGERsProofs returns the proofs for the injected GERs in the given block range
// created from the last finalized L1 Info tree root
func (a *AggchainProverFlow) GetInjectedGERsProofs(
	ctx context.Context,
	finalizedL1InfoTreeRoot *treetypes.Root,
	fromBlock, toBlock uint64) (map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber, error) {
	injectedGERs, err := a.gerReader.GetInjectedGERsForRange(ctx, fromBlock, toBlock)
	if err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error getting injected GERs for range %d : %d: %w",
			fromBlock, toBlock, err)
	}

	proofs := make(map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber, len(injectedGERs))

	for blockNum, gerHashes := range injectedGERs {
		for _, gerHash := range gerHashes {
			info, proof, err := a.l1InfoTreeDataQuerier.GetProofForGER(ctx, gerHash, finalizedL1InfoTreeRoot.Hash)
			if err != nil {
				return nil, fmt.Errorf("aggchainProverFlow - error getting proof for GER: %s: %w", gerHash.String(), err)
			}

			proofs[gerHash] = &agglayertypes.ProvenInsertedGERWithBlockNumber{
				BlockNumber: blockNum,
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
	}

	return proofs, nil
}

// getImportedBridgeExitsForProver converts the claims to imported bridge exits
// so that the aggchain prover can use them to generate the aggchain proof
func (a *AggchainProverFlow) GetImportedBridgeExitsForProver(
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
