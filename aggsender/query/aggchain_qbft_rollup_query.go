package query

import (
	"context"
	"fmt"
	"math/big"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/aggchainbase"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	"github.com/agglayer/aggkit/aggsender/query/contracts/aggchainqbft"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

var (
	_ types.AggchainFEPRollupQuerier = (*aggchainQbftRollupQuerier)(nil)
	_ types.QBFTContractQuerier      = (*aggchainqbft.AggchainqbftCaller)(nil)
	_ types.QBFTChainQuerier         = (*qbftChainQuery)(nil)
)

// aggchainQbftRollupQuerier queries the AggchainQBFT rollup contract.
// It implements types.AggchainFEPRollupQuerier because the aggchain-proof flow (shared with FEP) only needs
// the "starting" and "last settled" L2 block of the rollup contract. For QBFT both come from
// lastProvenBlockNumber (the contract initializes it with the starting block number).
type aggchainQbftRollupQuerier struct {
	startL2BlockNum uint64
	qbftAddr        common.Address
	qbftCaller      types.QBFTContractQuerier
}

// NewQbftQuerier creates a new AggchainQBFT rollup querier. It reads lastProvenBlockNumber() from
// the AggchainQBFT contract where the FEP querier reads startingBlockNumber.
func NewQbftQuerier(
	logger *log.Logger,
	qbftAddr common.Address,
	l1Client aggkittypes.BaseEthereumClienter) (types.AggchainFEPRollupQuerier, error) {
	if qbftAddr == aggkitcommon.ZeroAddress {
		return nil, fmt.Errorf("aggchainProverFlow - AggchainQBFT contract address is zero")
	}
	qbftContract, err := aggchainqbft.NewAggchainqbftCaller(qbftAddr, l1Client)
	if err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error creating AggchainQBFT rollup caller (%s): %w",
			qbftAddr.String(), err)
	}

	return newQbftQuerier(logger, qbftAddr, qbftContract)
}

func newQbftQuerier(
	logger *log.Logger,
	qbftAddr common.Address,
	qbftCaller types.QBFTContractQuerier,
) (types.AggchainFEPRollupQuerier, error) {
	startL2Block, err := qbftCaller.LastProvenBlockNumber(nil)
	if err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error AggchainQBFT.LastProvenBlockNumber (%s): %w",
			qbftAddr.String(), err)
	}

	logger.Infof("aggchainProverFlow - AggchainQBFT contract (%s), last proven block: %d",
		qbftAddr.String(), startL2Block)

	return &aggchainQbftRollupQuerier{
		startL2BlockNum: startL2Block,
		qbftAddr:        qbftAddr,
		qbftCaller:      qbftCaller,
	}, nil
}

// IsFEP returns true: QBFT networks send aggchain-proof certificates, like FEP ones
func (a *aggchainQbftRollupQuerier) IsFEP() bool {
	return true
}

// StartL2Block returns the L2 block number the contract had proven when the querier was created.
func (a *aggchainQbftRollupQuerier) StartL2Block() uint64 {
	return a.startL2BlockNum
}

// GetLastSettledL2Block retrieves lastProvenBlockNumber from the AggchainQBFT contract.
func (a *aggchainQbftRollupQuerier) GetLastSettledL2Block() (uint64, error) {
	lastProven, err := a.qbftCaller.LastProvenBlockNumber(nil)
	if err != nil {
		return 0, fmt.Errorf("aggchainProverFlow - error getting lastProvenBlockNumber from AggchainQBFT (%s): %w",
			a.qbftAddr.String(), err)
	}
	return lastProven, nil
}

// NewRollupQuerier returns the rollup querier matching the aggchain type of the rollup contract:
// the QBFT querier for AggchainQBFT (0x0002) and the FEP querier otherwise.
func NewRollupQuerier(
	logger *log.Logger,
	committeeQuerier types.MultisigQuerier,
	aggsenderMode types.AggsenderMode,
	sovereignRollupAddr common.Address,
	l1Client aggkittypes.BaseEthereumClienter) (types.AggchainFEPRollupQuerier, error) {
	if aggsenderMode == types.AggchainProofMode && sovereignRollupAddr != aggkitcommon.ZeroAddress {
		aggchainType, err := committeeQuerier.AggchainType()
		if err != nil {
			return nil, fmt.Errorf("aggchainProverFlow - error getting aggchain type: %w", err)
		}
		if aggchainType == types.AggchainTypeQBFT {
			return NewQbftQuerier(logger, sovereignRollupAddr, l1Client)
		}
	}
	return NewAggchainFEPQuerier(logger, aggsenderMode, sovereignRollupAddr, l1Client)
}

// qbftChainQuery implements types.QBFTChainQuerier on top of the validator's own L2 RPC (block hash,
// bridge root) and the rollup contract on L1 (selector to aggchain type).
type qbftChainQuery struct {
	l2Client     aggkittypes.CustomEthereumClienter
	bridgeL2     bridgeRootGetter
	aggchainBase aggchainTypeGetter
}

type bridgeRootGetter interface {
	GetRoot(opts *bind.CallOpts) ([32]byte, error)
}

type aggchainTypeGetter interface {
	GetAggchainTypeFromSelector(opts *bind.CallOpts, aggchainVKeySelector [4]byte) ([2]byte, error)
}

// NewQBFTChainQuery creates the querier used by the verifier flow of AggchainQBFT networks.
// The L2 client must return the block hash as sent by the node (HashFromJSON = true), because the Besu QBFT
// block hash can not be recomputed from the header the way go-ethereum does it.
func NewQBFTChainQuery(
	l1Client aggkittypes.BaseEthereumClienter,
	l2Client aggkittypes.BaseEthereumClienter,
	sovereignRollupAddr common.Address,
	bridgeL2Addr common.Address) (types.QBFTChainQuerier, error) {
	bridgeL2, err := agglayerbridgel2.NewAgglayerbridgel2Caller(bridgeL2Addr, l2Client)
	if err != nil {
		return nil, fmt.Errorf("error creating L2 bridge caller (%s): %w", bridgeL2Addr.String(), err)
	}
	aggchainBase, err := aggchainbase.NewAggchainbaseCaller(sovereignRollupAddr, l1Client)
	if err != nil {
		return nil, fmt.Errorf("error creating rollup contract caller (%s): %w", sovereignRollupAddr.String(), err)
	}
	return &qbftChainQuery{l2Client: l2Client, bridgeL2: bridgeL2, aggchainBase: aggchainBase}, nil
}

// GetBlockHash returns the L2 block hash as reported by the node
func (q *qbftChainQuery) GetBlockHash(ctx context.Context, blockNumber uint64) (common.Hash, error) {
	header, err := q.l2Client.CustomHeaderByNumber(ctx, aggkittypes.NewBlockNumber(blockNumber))
	if err != nil {
		return common.Hash{}, fmt.Errorf("error getting L2 block %d: %w", blockNumber, err)
	}
	return header.Hash, nil
}

// GetBridgeRootAtBlock returns the L2 bridge getRoot() at the given block
func (q *qbftChainQuery) GetBridgeRootAtBlock(ctx context.Context, blockNumber uint64) (common.Hash, error) {
	root, err := q.bridgeL2.GetRoot(&bind.CallOpts{Context: ctx, BlockNumber: new(big.Int).SetUint64(blockNumber)})
	if err != nil {
		return common.Hash{}, fmt.Errorf("error getting L2 bridge root at block %d: %w", blockNumber, err)
	}
	return common.Hash(root), nil
}

// GetAggchainTypeFromSelector returns the aggchain type encoded in the vkey selector
func (q *qbftChainQuery) GetAggchainTypeFromSelector(selector [4]byte) (types.AggchainType, error) {
	aggchainType, err := q.aggchainBase.GetAggchainTypeFromSelector(nil, selector)
	if err != nil {
		return "", fmt.Errorf("error getting aggchain type from selector %x: %w", selector, err)
	}
	return aggchainTypeFromBytes(aggchainType)
}
