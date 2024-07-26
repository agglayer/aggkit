package txbuilder

import (
	"github.com/0xPolygon/cdk/etherman"
	"github.com/0xPolygon/cdk/etherman/contracts"
	"github.com/0xPolygon/cdk/sequencesender/seqsendertypes"
	"github.com/0xPolygon/cdk/state/datastream"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

type TxBuilderElderberryBase struct {
	rollupContract contracts.RollupElderberryType
	opts           bind.TransactOpts
}

func NewTxBuilderElderberryBase(rollupContract contracts.RollupElderberryType, opts bind.TransactOpts) *TxBuilderElderberryBase {
	return &TxBuilderElderberryBase{
		rollupContract: rollupContract,
		opts:           opts,
	}
}

func (t *TxBuilderElderberryBase) NewSequence(batches []seqsendertypes.Batch, coinbase common.Address) (seqsendertypes.Sequence, error) {
	seq := ElderberrySequence{
		l2Coinbase: coinbase,
		batches:    batches,
	}
	return &seq, nil
}

func (t *TxBuilderElderberryBase) NewBatchFromL2Block(l2Block *datastream.L2Block) seqsendertypes.Batch {
	batch := &etherman.Batch{
		LastL2BLockTimestamp: l2Block.Timestamp,
		BatchNumber:          l2Block.BatchNumber,
		L1InfoTreeIndex:      l2Block.L1InfotreeIndex,
		LastCoinbase:         common.BytesToAddress(l2Block.Coinbase),
		GlobalExitRoot:       common.BytesToHash(l2Block.GlobalExitRoot),
	}
	return NewBananaBatch(batch)
}

func getLastSequencedBatchNumber(sequences seqsendertypes.Sequence) uint64 {
	if sequences.Len() == 0 {
		return 0
	}
	return sequences.FirstBatch().BatchNumber() - 1
}
