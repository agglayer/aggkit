package query

import (
	"context"
	"fmt"
	"math"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/l1infotreesync"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

var _ types.GERQuerier = (*gerDataQuerier)(nil)

// gerDataQuerier is a struct that holds the logic to query the GER (Global Exit Root) data
type gerDataQuerier struct {
	l1InfoTreeQuerier types.L1InfoTreeDataQuerier
	chainGERReader    types.ChainGERReader
}

// NewGERDataQuerier returns a new instance of the GERQuerier
func NewGERDataQuerier(
	l1InfoTreeQuerier types.L1InfoTreeDataQuerier,
	chainGERReader types.ChainGERReader) types.GERQuerier {
	return &gerDataQuerier{
		l1InfoTreeQuerier: l1InfoTreeQuerier,
		chainGERReader:    chainGERReader,
	}
}

// GetInjectedGERsProofs retrieves proofs for injected GERs (Global Exit Roots) within a specified block range.
// It queries the chain for injected GERs and generates proofs for each GER using the finalized L1 info tree root.
//
// Parameters:
//   - ctx: The context for managing request deadlines and cancellations.
//   - finalizedL1InfoTreeRoot: The root of the finalized L1 info tree used for proof generation.
//   - fromBlock: The starting block number of the range to query for injected GERs.
//   - toBlock: The ending block number of the range to query for injected GERs.
//
// Returns:
//   - A map where the key is the hash of the GER and the value is a ProvenInsertedGERWithBlockNumber containing
//     the proof and associated block information.
//   - An error if any issues occur during the retrieval or proof generation process.
//
// Errors:
//   - Returns an error if there is an issue querying the chain for injected GERs.
//   - Returns an error if there is an issue generating proofs for any GER.
func (g *gerDataQuerier) GetInjectedGERsProofs(
	ctx context.Context,
	finalizedL1InfoTreeRootHash common.Hash,
	fromBlock, toBlock uint64) (map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber, error) {
	injectedGERs, err := g.chainGERReader.GetInjectedGERsForRange(ctx, fromBlock, toBlock)
	if err != nil {
		return nil, fmt.Errorf("error getting injected GERs for range %d : %d: %w",
			fromBlock, toBlock, err)
	}

	proofs := make(map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber, len(injectedGERs))

	for ger, injectedGER := range injectedGERs {
		var info *l1infotreesync.L1InfoTreeLeaf
		var proof treetypes.Proof
		var err error
		if injectedGER.L1InfoTreeIndex == math.MaxUint32 {
			// make a dummy info and proof for the GER
			info = &l1infotreesync.L1InfoTreeLeaf{
				L1InfoTreeIndex:   math.MaxUint32,
				RollupExitRoot:    common.HexToHash("0x0"),
				MainnetExitRoot:   common.HexToHash("0x0"),
				PreviousBlockHash: common.HexToHash("0x0"),
				Timestamp:         0,
				GlobalExitRoot:    ger,
			}
			proof = treetypes.Proof{}
		} else {
			info, proof, err = g.l1InfoTreeQuerier.GetProofForGER(ctx, ger, finalizedL1InfoTreeRootHash)
			if err != nil {
				return nil, fmt.Errorf("error getting proof for GER: %s: %w", ger.String(), err)
			}
		}

		if injectedGER.BlockPosition == nil {
			return nil, fmt.Errorf("block position for GER %s is undefined", ger.String())
		}

		proofs[ger] = &agglayertypes.ProvenInsertedGERWithBlockNumber{
			BlockNumber: injectedGER.BlockNum,
			LogIndex:    *injectedGER.BlockPosition,
			ProvenInsertedGERLeaf: agglayertypes.ProvenInsertedGER{
				ProofGERToL1Root: &agglayertypes.MerkleProof{Root: finalizedL1InfoTreeRootHash, Proof: proof},
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

// GetRemovedGERsForRange returns the removed GlobalExitRoots for the given block range
func (g *gerDataQuerier) GetRemovedGERsForRange(ctx context.Context,
	fromBlock, toBlock uint64) ([]*agglayertypes.RemovedGER, error) {
	removedGERs, err := g.chainGERReader.GetRemovedGERsForRange(ctx, fromBlock, toBlock)
	if err != nil {
		return nil, fmt.Errorf("error getting removed GERs for range %d : %d: %w",
			fromBlock, toBlock, err)
	}
	return removedGERs, nil
}
