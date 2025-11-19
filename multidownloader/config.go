package multidownloader

import (
	"errors"
	"fmt"
	"path"
	"time"

	"github.com/agglayer/aggkit/config/types"
	aggkittypes "github.com/agglayer/aggkit/types"
)

var (
	// ErrInvalidBlockChunkSize is returned when the block chunk size is invalid
	ErrInvalidBlockChunkSize = errors.New("MultidownloaderConfig.BlockChunkSize: block chunk size must be greater than 0")
	// ErrInvalidMaxParallelBlockHeaderRetrieval is returned when the max parallel block header retrieval is invalid
	ErrInvalidMaxParallelBlockHeaderRetrieval = errors.New("MultidownloaderConfig.MaxParallelBlockHeaderRetrieval:" +
		" max parallel block header retrieval must be greater than 0")
	// ErrInvalidWaitPeriodToCheckCatchUp is returned when the wait period to check catch up is invalid
	ErrInvalidWaitPeriodToCheckCatchUp = errors.New("MultidownloaderConfig.WaitPeriodToCheckCatchUp: " +
		"wait period to check catch up must be greater than 0")
)

type Config struct {
	// Enabled indicates if the multidownloader is enabled
	Enabled bool
	// StoragePath is the path to the storage
	StoragePath string
	// BlockChunkSize is the number of blocks to query in each FilterLogs call
	BlockChunkSize uint32
	// MaxParallelBlockHeaderRetrieval is the maximum number of parallel RPC calls to retrieve block headers
	MaxParallelBlockHeaderRetrieval int
	// BlockFinality is which block consider final (typically finalizedBlock)
	BlockFinality aggkittypes.BlockNumberFinality
	// WaitPeriodToCheckCatchUp is the wait applied when a request is still not available to check again
	WaitPeriodToCheckCatchUp types.Duration
}

const defaultBlockChunkSize = 10000
const defaultMaxParallelBlockHeaderRetrieval = 30
const defaultWaitPeriodToCheckCatchUp = time.Second * 10

func NewConfigDefault(name string, basePathDB string) Config {
	if basePathDB == "" {
		basePathDB = "/tmp/aggkit/"
	}
	dbPath := path.Join(basePathDB, fmt.Sprintf("%s_multidownloader.sqlite", name))
	return Config{
		Enabled:                         false,
		StoragePath:                     dbPath,
		BlockChunkSize:                  defaultBlockChunkSize,
		MaxParallelBlockHeaderRetrieval: defaultMaxParallelBlockHeaderRetrieval,
		BlockFinality:                   aggkittypes.FinalizedBlock,
		WaitPeriodToCheckCatchUp:        types.NewDuration(defaultWaitPeriodToCheckCatchUp),
	}
}

func (cfg *Config) Validate() error {
	if cfg.BlockChunkSize == 0 {
		return ErrInvalidBlockChunkSize
	}
	if cfg.MaxParallelBlockHeaderRetrieval == 0 {
		return ErrInvalidMaxParallelBlockHeaderRetrieval
	}
	if cfg.WaitPeriodToCheckCatchUp.Duration <= 0 {
		return ErrInvalidWaitPeriodToCheckCatchUp
	}
	if cfg.BlockFinality.IsEmpty() {
		return fmt.Errorf("MultidownloaderConfig.BlockFinality: block finality cannot be empty")
	}
	return nil
}

func (cfg *Config) String() string {
	return fmt.Sprintf("MultidownloaderConfig{Enabled:%t, BlockChunkSize:%d, "+
		"MaxParallelBlockHeaderRetrieval:%d, BlockFinality:%s, WaitPeriodToCheckCatchUp:%s}",
		cfg.Enabled,
		cfg.BlockChunkSize,
		cfg.MaxParallelBlockHeaderRetrieval,
		cfg.BlockFinality.String(),
		cfg.WaitPeriodToCheckCatchUp.String())
}
