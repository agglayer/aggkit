package aggsender

import (
	"context"
	"fmt"
	"math/big"

	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/grpc"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/etherman"
	"github.com/agglayer/aggkit/l1infotreesync"
	treeTypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

var finalizedBlockBigInt = big.NewInt(int64(etherman.Finalized))

// aggchainProverFlow is a struct that holds the logic for the AggchainProver prover type flow
type aggchainProverFlow struct {
	*baseFlow

	l1Client            types.EthClient
	aggchainProofClient grpc.AggchainProofClientInterface
}

// newAggchainProverFlow returns a new instance of the aggchainProverFlow
func newAggchainProverFlow(log types.Logger,
	cfg Config,
	aggkitProverClient grpc.AggchainProofClientInterface,
	storage db.AggSenderStorage,
	l1InfoTreeSyncer types.L1InfoTreeSyncer,
	l2Syncer types.L2BridgeSyncer,
	l1Client types.EthClient) *aggchainProverFlow {
	return &aggchainProverFlow{
		l1Client:            l1Client,
		aggchainProofClient: aggkitProverClient,
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
// What differentiates this function from the regular PP flow is that,
// if the last sent certificate is in error, we need to resend the exact same certificate
// also, it calls the aggchain prover to get the aggchain proof
func (a *aggchainProverFlow) GetCertificateBuildParams(ctx context.Context) (*types.CertificateBuildParams, error) {
	lastSentCertificateInfo, err := a.storage.GetLastSentCertificate()
	if err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error getting last sent certificate: %w", err)
	}

	var (
		buildParams *types.CertificateBuildParams
	)

	if lastSentCertificateInfo != nil && lastSentCertificateInfo.Status == agglayer.InError {
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
		buildParams, err = a.baseFlow.GetCertificateBuildParams(ctx)
		if err != nil {
			return nil, err
		}
	}

	proof, leaf, root, err := a.getFinalizedL1InfoTreeData(ctx)
	if err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error getting finalized L1 Info tree data: %w", err)
	}

	// set the root from which to generate merkle proofs for each claim
	// this is crucial since Aggchain Prover will use this root to generate the proofs as well
	buildParams.L1InfoTreeRootFromWhichToProve = root

	injectedGERsProofs, err := a.getInjectedGERsProofs(ctx, root, buildParams.FromBlock, buildParams.ToBlock)
	if err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error getting injected GERs proofs: %w", err)
	}

	// TODO - get imported bridge exits
	aggchainProof, err := a.aggchainProofClient.GenerateAggchainProof(
		buildParams.FromBlock, buildParams.ToBlock, root.Hash, *leaf, proof,
		injectedGERsProofs, make([]*agglayer.ImportedBridgeExit, 0))
	if err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error fetching aggchain proof for block range %d : %d : %w",
			buildParams.FromBlock, buildParams.ToBlock, err)
	}

	a.log.Infof("aggchainProverFlow - fetched auth proof: %s Range %d : %d from aggchain prover. Requested range: %d : %d",
		aggchainProof.Proof, aggchainProof.StartBlock, aggchainProof.EndBlock,
		buildParams.FromBlock, buildParams.ToBlock)

	buildParams.AggchainProof = aggchainProof.Proof

	buildParams, err = adjustBlockRange(buildParams, buildParams.ToBlock, aggchainProof.EndBlock)
	if err != nil {
		return nil, err
	}

	return buildParams, nil
}

// getInjectedGERsProofs returns the proofs for the injected GERs in the given block range
// created from the last finalized L1 Info tree root
func (a *aggchainProverFlow) getInjectedGERsProofs(
	ctx context.Context,
	finalizedL1InfoTreeRoot *treeTypes.Root,
	fromBlock, toBlock uint64) (map[common.Hash]treeTypes.Proof, error) {
	return make(map[common.Hash]treeTypes.Proof, 0), nil
}

// getFinalizedL1InfoTreeData returns the L1 Info tree data for the last finalized processed block
// l1InfoTreeData is:
// - the leaf data of the highest index leaf on that block and root
// - merkle proof of given l1 info tree leaf
// - the root of the l1 info tree on that block
func (a *aggchainProverFlow) getFinalizedL1InfoTreeData(ctx context.Context,
) (treeTypes.Proof, *l1infotreesync.L1InfoTreeLeaf, *treeTypes.Root, error) {
	lastFinalizedProcessedBlock, err := a.getLatestProcessedFinalizedBlock(ctx)
	if err != nil {
		return treeTypes.Proof{}, nil, nil,
			fmt.Errorf("aggchainProverFlow - error getting latest processed finalized block: %w", err)
	}

	root, err := a.l1InfoTreeSyncer.GetLastL1InfoTreeRootByBlockNum(ctx, lastFinalizedProcessedBlock)
	if err != nil {
		return treeTypes.Proof{}, nil, nil,
			fmt.Errorf("aggchainProverFlow - error getting last L1 Info tree root by block num %d: %w",
				lastFinalizedProcessedBlock, err)
	}

	leaf, err := a.l1InfoTreeSyncer.GetInfoByIndex(ctx, root.Index)
	if err != nil {
		return treeTypes.Proof{}, nil, nil,
			fmt.Errorf("aggchainProverFlow - error getting L1 Info tree leaf by index %d: %w", root.Index, err)
	}

	proof, err := a.l1InfoTreeSyncer.GetL1InfoTreeMerkleProofFromIndexToRoot(ctx, root.Index, root.Hash)
	if err != nil {
		return treeTypes.Proof{}, nil, nil,
			fmt.Errorf("aggchainProverFlow - error getting L1 Info tree merkle proof from index %d to root %s: %w",
				root.Index, root.Hash.String(), err)
	}

	return proof, leaf, root, nil
}

// getLatestProcessedFinalizedBlock returns the latest processed finalized block from the l1infotreesyncer
func (a *aggchainProverFlow) getLatestProcessedFinalizedBlock(ctx context.Context) (uint64, error) {
	lastFinalizedL1Block, err := a.l1Client.HeaderByNumber(ctx, finalizedBlockBigInt)
	if err != nil {
		return 0, fmt.Errorf("aggchainProverFlow - error getting latest finalized L1 block: %w", err)
	}

	lastProcessedBlockNum, lastProcessedBlockHash, err := a.l1InfoTreeSyncer.GetProcessedBlockUntil(ctx,
		lastFinalizedL1Block.Number.Uint64())
	if err != nil {
		return 0, fmt.Errorf("aggchainProverFlow - error getting latest processed block from l1infotreesyncer: %w", err)
	}

	if lastProcessedBlockNum == 0 {
		return 0, fmt.Errorf("aggchainProverFlow - l1infotreesyncer did not process any block yet")
	}

	if lastFinalizedL1Block.Number.Uint64() > lastProcessedBlockNum {
		// syncer has a lower block than the finalized block, so we need to get that block from the l1 node
		lastFinalizedL1Block, err = a.l1Client.HeaderByNumber(ctx, new(big.Int).SetUint64(lastProcessedBlockNum))
		if err != nil {
			return 0, fmt.Errorf("aggchainProverFlow - error getting latest processed finalized block: %d: %w",
				lastProcessedBlockNum, err)
		}
	}

	if (lastProcessedBlockHash == common.Hash{}) || (lastProcessedBlockHash == lastFinalizedL1Block.Hash()) {
		// if the hash is empty it means that this is an old block that was processed before this
		// feature was added, so we will consider it finalized
		return lastFinalizedL1Block.Number.Uint64(), nil
	}

	return 0, fmt.Errorf("aggchainProverFlow - l1infotreesyncer returned a different hash for "+
		"the latest finalized block: %d. Might be that syncer did not process a reorg yet. "+
		"Expected hash: %s, got: %s", lastProcessedBlockNum,
		lastFinalizedL1Block.Hash().String(), lastProcessedBlockHash.String())
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
