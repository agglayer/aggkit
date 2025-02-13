package types

import (
	"time"

	"github.com/agglayer/aggkit/etherman"
	"github.com/ethereum/go-ethereum/common"
)

type Block struct {
	Number     uint64
	Hash       common.Hash
	ParentHash common.Hash
	Timestamp  time.Time
}

type EventNewBlock struct {
	Block             Block
	BlockFinalityType etherman.BlockNumberFinality
	BlockRate         time.Duration
}

// BlockNotifier is the interface that wraps the basic methods to notify a new block.
type BlockNotifier interface {
	// NotifyEpochStarted notifies the epoch has started.
	Subscribe(id string) <-chan EventNewBlock
	GetCurrentBlockNumber() *Block
	GetBlockRate() (bool, time.Duration)
	String() string
}
