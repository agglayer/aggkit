package l1infotreequery

import (
	"context"
	"fmt"
	"math/big"

	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/etherman"
	"github.com/agglayer/aggkit/l1infotreesync"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

var finalizedBlockBigInt = big.NewInt(int64(etherman.Finalized))

var _ types.L1InfoTreeDataQuery = (*L1InfoTreeDataQuery)(nil)

// L1InfoTreeDataQuery is a struct that holds the logic to query the L1 Info tree data
type L1InfoTreeDataQuery struct {
	l1Client         types.EthClient
	l1InfoTreeSyncer types.L1InfoTreeSyncer
}

// NewL1InfoTreeDataQuery returns a new instance of the L1InfoTreeDataQuery
func NewL1InfoTreeDataQuery(
	l1Client types.EthClient,
	l1InfoTreeSyncer types.L1InfoTreeSyncer) *L1InfoTreeDataQuery {
	return &L1InfoTreeDataQuery{
		l1Client:         l1Client,
		l1InfoTreeSyncer: l1InfoTreeSyncer,
	}
}

// GetLatestFinalizedL1InfoRoot returns the latest processed l1 info tree root
// based on the latest finalized l1 block
func (l *L1InfoTreeDataQuery) GetLatestFinalizedL1InfoRoot(ctx context.Context) (
	*treetypes.Root, *l1infotreesync.L1InfoTreeLeaf, error) {
	lastFinalizedProcessedBlock, err := l.getLatestProcessedFinalizedBlock(ctx)
	if err != nil {
		return nil, nil,
			fmt.Errorf("error getting latest processed finalized block: %w", err)
	}

	l1InfoLeaf, err := l.l1InfoTreeSyncer.GetLatestInfoUntilBlock(ctx, lastFinalizedProcessedBlock)
	if err != nil {
		return nil, nil,
			fmt.Errorf("error getting latest l1 info tree info until block num %d: %w",
				lastFinalizedProcessedBlock, err)
	}

	root, err := l.l1InfoTreeSyncer.GetL1InfoTreeRootByIndex(ctx, l1InfoLeaf.L1InfoTreeIndex)
	if err != nil {
		return nil, nil,
			fmt.Errorf("error getting L1 Info tree root by index %d: %w", l1InfoLeaf.L1InfoTreeIndex, err)
	}

	return &root, l1InfoLeaf, nil
}

// GetFinalizedL1InfoTreeData returns the L1 Info tree data for the last finalized processed block
// l1InfoTreeData is:
// - the leaf data of the highest index leaf on that block and root
// - merkle proof of given l1 info tree leaf
// - the root of the l1 info tree on that block
func (l *L1InfoTreeDataQuery) GetFinalizedL1InfoTreeData(ctx context.Context,
) (treetypes.Proof, *l1infotreesync.L1InfoTreeLeaf, *treetypes.Root, error) {
	root, leaf, err := l.GetLatestFinalizedL1InfoRoot(ctx)
	if err != nil {
		return treetypes.Proof{}, nil, nil,
			fmt.Errorf("aggchainProverFlow - error getting latest finalized L1 Info tree root: %w", err)
	}

	proof, err := l.l1InfoTreeSyncer.GetL1InfoTreeMerkleProofFromIndexToRoot(ctx, root.Index, root.Hash)
	if err != nil {
		return treetypes.Proof{}, nil, nil,
			fmt.Errorf("aggchainProverFlow - error getting L1 Info tree merkle proof from index %d to root %s: %w",
				root.Index, root.Hash.String(), err)
	}

	return proof, leaf, root, nil
}

// GetProofForGER returns the L1 Info tree leaf and the merkle proof for the given GER
func (l *L1InfoTreeDataQuery) GetProofForGER(
	ctx context.Context, ger, rootFromWhichToProve common.Hash) (
	*l1infotreesync.L1InfoTreeLeaf, treetypes.Proof, error) {
	l1Info, err := l.l1InfoTreeSyncer.GetInfoByGlobalExitRoot(ger)
	if err != nil {
		return nil, treetypes.Proof{}, fmt.Errorf("error getting info by global exit root: %w", err)
	}

	gerToL1Proof, err := l.l1InfoTreeSyncer.GetL1InfoTreeMerkleProofFromIndexToRoot(
		ctx, l1Info.L1InfoTreeIndex, rootFromWhichToProve,
	)
	if err != nil {
		return nil, treetypes.Proof{}, fmt.Errorf("error getting L1 Info tree merkle proof for GER: %w", err)
	}

	return l1Info, gerToL1Proof, nil
}

// CheckIfClaimsArePartOfFinalizedL1InfoTree checks if the claims are part of the finalized L1 Info tree
func (l *L1InfoTreeDataQuery) CheckIfClaimsArePartOfFinalizedL1InfoTree(
	finalizedL1InfoTreeRoot *treetypes.Root,
	claims []bridgesync.Claim) error {
	for _, claim := range claims {
		info, err := l.l1InfoTreeSyncer.GetInfoByGlobalExitRoot(claim.GlobalExitRoot)
		if err != nil {
			return fmt.Errorf("error getting claim info by global exit root: %s: %w", claim.GlobalExitRoot, err)
		}

		if info.L1InfoTreeIndex > finalizedL1InfoTreeRoot.Index {
			return fmt.Errorf("claim with global exit root: %s has L1 Info tree index: %d "+
				"higher than the last finalized l1 info tree root: %s index: %d",
				claim.GlobalExitRoot.String(), info.L1InfoTreeIndex,
				finalizedL1InfoTreeRoot.Hash, finalizedL1InfoTreeRoot.Index)
		}
	}

	return nil
}

// getLatestProcessedFinalizedBlock returns the latest processed finalized block from the l1infotreesyncer
func (l *L1InfoTreeDataQuery) getLatestProcessedFinalizedBlock(ctx context.Context) (uint64, error) {
	lastFinalizedL1Block, err := l.l1Client.HeaderByNumber(ctx, finalizedBlockBigInt)
	if err != nil {
		return 0, fmt.Errorf("error getting latest finalized L1 block: %w", err)
	}

	lastProcessedBlockNum, lastProcessedBlockHash, err := l.l1InfoTreeSyncer.GetProcessedBlockUntil(ctx,
		lastFinalizedL1Block.Number.Uint64())
	if err != nil {
		return 0, fmt.Errorf("error getting latest processed block from l1infotreesyncer: %w", err)
	}

	if lastProcessedBlockNum == 0 {
		return 0, fmt.Errorf("l1infotreesyncer did not process any block yet")
	}

	if lastFinalizedL1Block.Number.Uint64() > lastProcessedBlockNum {
		// syncer has a lower block than the finalized block, so we need to get that block from the l1 node
		lastFinalizedL1Block, err = l.l1Client.HeaderByNumber(ctx, new(big.Int).SetUint64(lastProcessedBlockNum))
		if err != nil {
			return 0, fmt.Errorf("error getting latest processed finalized block: %d: %w",
				lastProcessedBlockNum, err)
		}
	}

	if (lastProcessedBlockHash == common.Hash{}) || (lastProcessedBlockHash == lastFinalizedL1Block.Hash()) {
		// if the hash is empty it means that this is an old block that was processed before this
		// feature was added, so we will consider it finalized
		return lastFinalizedL1Block.Number.Uint64(), nil
	}

	return 0, fmt.Errorf("l1infotreesyncer returned a different hash for "+
		"the latest finalized block: %d. Might be that syncer did not process a reorg yet. "+
		"Expected hash: %s, got: %s", lastProcessedBlockNum,
		lastFinalizedL1Block.Hash().String(), lastProcessedBlockHash.String())
}
