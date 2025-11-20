package query

import (
	"context"
	"fmt"
	"math/big"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerger"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

var createAgglayerGERL1func = func(gerAddr common.Address,
	l1Client aggkittypes.BaseEthereumClienter) (types.AgglayerGER, error) {
	return agglayerger.NewAgglayerger(gerAddr, l1Client)
}

var _ types.L2GERQuerier = (*l2GERDataQuerier)(nil)

// l2GERDataQuerier is a struct that holds the logic to query the GER (Global Exit Root) data
type l2GERDataQuerier struct {
	l1InfoTreeQuerier types.L1InfoTreeDataQuerier
	chainGERReader    types.ChainGERReader
}

// NewL2GERDataQuerier returns a new instance of the GERQuerier for L2 chains
func NewL2GERDataQuerier(
	l1InfoTreeQuerier types.L1InfoTreeDataQuerier,
	chainGERReader types.ChainGERReader) types.L2GERQuerier {
	return &l2GERDataQuerier{
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
func (g *l2GERDataQuerier) GetInjectedGERsProofs(
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
		info, proof, err := g.l1InfoTreeQuerier.GetProofForGER(ctx, ger, finalizedL1InfoTreeRootHash)
		if err != nil {
			return nil, fmt.Errorf("error getting proof for GER: %s: %w", ger.String(), err)
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
func (g *l2GERDataQuerier) GetRemovedGERsForRange(ctx context.Context,
	fromBlock, toBlock uint64) ([]*agglayertypes.RemovedGER, error) {
	removedGERs, err := g.chainGERReader.GetRemovedGERsForRange(ctx, fromBlock, toBlock)
	if err != nil {
		return nil, fmt.Errorf("error getting removed GERs for range %d : %d: %w",
			fromBlock, toBlock, err)
	}
	return removedGERs, nil
}

var _ types.L1GERQuerier = (*l1GERDataQuerier)(nil)

// l1GERDataQuerier is a struct that holds the logic to query the L1 GER (Global Exit Root) data
type l1GERDataQuerier struct {
	blockFinality aggkittypes.BlockNumberFinality
	agglayerGER   types.AgglayerGER
	l1Client      aggkittypes.BaseEthereumClienter
}

// NewL1GERDataQuerier returns a new instance of the L1GERDataQuerier
func NewL1GERDataQuerier(
	l1AgglayerGERAddr common.Address,
	blockFinality aggkittypes.BlockNumberFinality,
	l1Client aggkittypes.BaseEthereumClienter,
) (types.L1GERQuerier, error) {
	agglayerGER, err := createAgglayerGERL1func(l1AgglayerGERAddr, l1Client)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize L1 GER manager contract: %w", err)
	}

	return &l1GERDataQuerier{
		l1Client:      l1Client,
		agglayerGER:   agglayerGER,
		blockFinality: blockFinality,
	}, nil
}

// DoesGERExistOnContract checks if the given GER exists on the Agglayer GER contract
func (g *l1GERDataQuerier) DoesGERExistOnContract(ctx context.Context, ger common.Hash) (bool, error) {
	// TODO - maybe get the header and use block hash instead?
	blockNum, err := g.blockFinality.BlockNumber(ctx, g.l1Client)
	if err != nil {
		return false, fmt.Errorf("error getting block number for finality %s: %w", g.blockFinality.String(), err)
	}

	timestamp, err := g.agglayerGER.GlobalExitRootMap(
		&bind.CallOpts{
			Context:     ctx,
			BlockNumber: new(big.Int).SetUint64(blockNum),
		},
		ger,
	)
	if err != nil {
		return false, fmt.Errorf("error querying GER existence on contract: %w", err)
	}

	return timestamp.Cmp(common.Big0) != 0, nil
}
