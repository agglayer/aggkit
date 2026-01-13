package query

import (
	"context"
	"fmt"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerger"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/l1infotreesync"
	treetypes "github.com/agglayer/aggkit/tree/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

var _ types.L1InfoTreeDataQuerier = (*L1InfoTreeDataQuerier)(nil)

// L1InfoTreeDataQuerier is a struct that holds the logic to query the L1 Info tree data
type L1InfoTreeDataQuerier struct {
	l1Client                   aggkittypes.BaseEthereumClienter
	l1GERManager               *agglayerger.Agglayerger
	l1InfoTreeSyncer           types.L1InfoTreeSyncer
	blockFinalityForL1InfoTree aggkittypes.BlockNumberFinality
}

// NewL1InfoTreeDataQuerier returns a new instance of the L1InfoTreeDataQuery
func NewL1InfoTreeDataQuerier(
	l1Client aggkittypes.BaseEthereumClienter,
	l1GERAddr common.Address,
	l1InfoTreeSyncer types.L1InfoTreeSyncer,
	blockFinalityForL1InfoTree aggkittypes.BlockNumberFinality) (*L1InfoTreeDataQuerier, error) {
	l1GERManager, err := agglayerger.NewAgglayerger(l1GERAddr, l1Client)
	if err != nil {
		return nil, err
	}
	l1InfoTreeFinality := l1InfoTreeSyncer.Finality()
	lessFinal, err := blockFinalityForL1InfoTree.LessFinalThan(l1InfoTreeFinality)
	if err != nil {
		return nil, fmt.Errorf("error comparing block finalities (target: %s and l1infotreeFinality: %s): %w",
			blockFinalityForL1InfoTree.String(), l1InfoTreeFinality.String(), err)
	}
	if lessFinal {
		return nil, fmt.Errorf("block finality misconfiguration (%s): l1infotreeSyncer finality (%s) is lower; "+
			"will never be fulfilled",
			blockFinalityForL1InfoTree.String(), l1InfoTreeFinality.String())
	}

	return &L1InfoTreeDataQuerier{
		l1Client:                   l1Client,
		l1GERManager:               l1GERManager,
		l1InfoTreeSyncer:           l1InfoTreeSyncer,
		blockFinalityForL1InfoTree: blockFinalityForL1InfoTree,
	}, nil
}

// GetTargetL1InfoRoot returns the latest processed l1 info tree root
// based on the latest finalized l1 block
func (l *L1InfoTreeDataQuerier) GetTargetL1InfoRoot(ctx context.Context) (
	*treetypes.Root, *l1infotreesync.L1InfoTreeLeaf, error) {
	lastFinalizedProcessedBlock, err := l.getTargetL1BlockNumber(ctx)
	if err != nil {
		return nil, nil,
			fmt.Errorf("error getting getTargetL1BlockNumber: %w", err)
	}

	l1InfoLeaf, err := l.l1InfoTreeSyncer.GetLatestL1InfoLeafUntilBlock(ctx, lastFinalizedProcessedBlock)
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

// GetL1InfoRootByLeafIndex returns the L1 Info tree root tha corresponds to the given leaf index
func (l *L1InfoTreeDataQuerier) GetL1InfoRootByLeafIndex(ctx context.Context,
	leafIndex uint32) (*treetypes.Root, error) {
	// Get the latest finalized L1 Info tree root
	root, err := l.l1InfoTreeSyncer.GetL1InfoTreeRootByIndex(ctx, leafIndex)
	if err != nil {
		return nil, fmt.Errorf("error getting L1 Info tree root by leaf index %d: %w", leafIndex, err)
	}

	// If the root is empty, it means there are no leaves in the tree
	if root.Hash == aggkitcommon.ZeroHash {
		return nil, fmt.Errorf("no L1 Info tree root found for leaf index %d", leafIndex)
	}

	return &root, nil
}

// GetFinalizedL1InfoTreeData retrieves the L1 info tree leaf and its merkle proof for a finalized L1 info tree state.
// It takes the finalized L1 info tree root hash and leaf count to fetch the last leaf in the tree
// and generate a merkle proof from that leaf to the specified root hash.
//
// Parameters:
//   - ctx: Context for cancellation and timeouts
//   - finalizedL1InfoTreeRootHash: The root hash of the finalized L1 info tree
//   - finalizedL1InfoTreeLeafCount: The total number of leaves in the finalized L1 info tree
//
// Returns:
//   - treetypes.Proof: The merkle proof from the leaf to the root
//   - *l1infotreesync.L1InfoTreeLeaf: The last leaf in the finalized tree
//   - error: Any error that occurred during the operation
func (l *L1InfoTreeDataQuerier) GetFinalizedL1InfoTreeData(
	ctx context.Context,
	finalizedL1InfoTreeRootHash common.Hash,
	finalizedL1InfoTreeLeafCount uint32,
) (treetypes.Proof, *l1infotreesync.L1InfoTreeLeaf, error) {
	leafIndex := finalizedL1InfoTreeLeafCount - 1

	leaf, err := l.GetInfoByIndex(ctx, leafIndex)
	if err != nil {
		return treetypes.Proof{}, nil,
			fmt.Errorf("error getting L1 Info tree leaf by index %d: %w", leafIndex, err)
	}

	proof, err := l.l1InfoTreeSyncer.GetL1InfoTreeMerkleProofFromIndexToRoot(ctx,
		leafIndex, finalizedL1InfoTreeRootHash)
	if err != nil {
		return treetypes.Proof{}, nil,
			fmt.Errorf("error getting L1 Info tree merkle proof from index %d to root %s: %w",
				leafIndex, finalizedL1InfoTreeRootHash.String(), err)
	}

	return proof, leaf, nil
}

// GetProofForGER returns the L1 Info tree leaf and the merkle proof for the given GER
func (l *L1InfoTreeDataQuerier) GetProofForGER(
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

// getTargetL1BlockNumber returns the latest processed block from the l1infotreesyncer
// up to target block (blockFinalityForL1InfoTree)
func (l *L1InfoTreeDataQuerier) getTargetL1BlockNumber(ctx context.Context) (uint64, error) {
	lastFinalizedL1Block, err := l.l1Client.CustomHeaderByNumber(ctx, &l.blockFinalityForL1InfoTree)
	if err != nil {
		return 0, fmt.Errorf("error getting target block (%s) from L1: %w", l.blockFinalityForL1InfoTree.String(), err)
	}

	lastProcessedBlockNum, lastProcessedBlockHash, err := l.l1InfoTreeSyncer.GetProcessedBlockUntil(ctx,
		lastFinalizedL1Block.Number)
	if err != nil {
		return 0, fmt.Errorf("error getting latest processed block until %d from l1infotreesyncer: %w",
			lastFinalizedL1Block.Number, err)
	}

	if lastProcessedBlockNum == 0 {
		return 0, fmt.Errorf("l1infotreesyncer did not process any block yet")
	}

	if lastFinalizedL1Block.Number > lastProcessedBlockNum {
		// syncer has a lower block than the finalized block, so we need to get that block from the l1 node
		lastFinalizedL1Block, err = l.l1Client.CustomHeaderByNumber(ctx, aggkittypes.NewBlockNumber(lastProcessedBlockNum))
		if err != nil {
			return 0, fmt.Errorf("error getting latest processed finalized block: %d: %w",
				lastProcessedBlockNum, err)
		}
	}

	if (lastProcessedBlockHash == common.Hash{}) || (lastProcessedBlockHash == lastFinalizedL1Block.Hash) {
		// if the hash is empty it means that this is an old block that was processed before this
		// feature was added, so we will consider it finalized
		return lastFinalizedL1Block.Number, nil
	}

	return 0, fmt.Errorf("l1infotreesyncer returned a different hash for "+
		"the latest finalized block: %d. Might be that syncer did not process a reorg yet. "+
		"Expected hash: %s, got: %s", lastProcessedBlockNum,
		lastFinalizedL1Block.Hash.String(), lastProcessedBlockHash.String())
}

// GetInfoByIndex returns the L1 Info tree leaf for the given index
func (l *L1InfoTreeDataQuerier) GetInfoByIndex(
	ctx context.Context, index uint32) (*l1infotreesync.L1InfoTreeLeaf, error) {
	info, err := l.l1InfoTreeSyncer.GetInfoByIndex(ctx, index)
	if err != nil {
		return nil, fmt.Errorf("error getting L1 Info tree leaf by index %d: %w", index, err)
	}
	if info == nil {
		return nil, fmt.Errorf("no L1 Info tree leaf found for index %d", index)
	}
	return info, nil
}

// IsGERFinalized checks if the given global exit root is finalized
func (l *L1InfoTreeDataQuerier) IsGERFinalized(
	ger common.Hash,
	finalizedL1InfoLeafCount uint32) (bool, error) {
	info, err := l.l1InfoTreeSyncer.GetInfoByGlobalExitRoot(ger)
	if err != nil {
		return false, err
	}

	if info == nil {
		return false, fmt.Errorf("no L1 Info tree leaf found for global exit root %s", ger.String())
	}

	return info.L1InfoTreeIndex <= finalizedL1InfoLeafCount-1, nil
}

func (l *L1InfoTreeDataQuerier) DoesGERExistsOnL1(
	ger common.Hash,
) (bool, error) {
	gerIndex, err := l.l1GERManager.GlobalExitRootMap(&bind.CallOpts{Pending: false}, ger)
	if err != nil {
		return false, fmt.Errorf("error checking if GER %s exists on L1: %w", ger.String(), err)
	}
	return gerIndex.Cmp(common.Big0) == 1, nil
}
