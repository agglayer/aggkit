package query

import (
	"context"
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

var _ types.GERQuerier = (*gerQuerier)(nil)

// gerQuerier is a struct that holds the logic to query the GER (Global Exit Root) data
type gerQuerier struct {
	l1InfoTreeQuerier types.L1InfoTreeDataQuerier
	chainGERReader    types.ChainGERReader
}

// NewGERQuerier returns a new instance of the GERQuerier
func NewGERQuerier(
	l1InfoTreeQuerier types.L1InfoTreeDataQuerier,
	chainGERReader types.ChainGERReader) types.GERQuerier {
	return &gerQuerier{
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
func (g *gerQuerier) GetInjectedGERsProofs(
	ctx context.Context,
	finalizedL1InfoTreeRoot *treetypes.Root,
	fromBlock, toBlock uint64) (map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber, error) {
	injectedGERs, err := g.chainGERReader.GetInjectedGERsForRange(ctx, fromBlock, toBlock)
	if err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error getting injected GERs for range %d : %d: %w",
			fromBlock, toBlock, err)
	}

	proofs := make(map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber, len(injectedGERs))

	for ger, injectedGER := range injectedGERs {
		info, proof, err := g.l1InfoTreeQuerier.GetProofForGER(ctx, ger, finalizedL1InfoTreeRoot.Hash)
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
