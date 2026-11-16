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
	// ErrInvalidRetryAfterErrorDelay is returned when the retry delays are negative or max < initial
	ErrInvalidRetryAfterErrorDelay = errors.New("MultidownloaderConfig.RetryAfterErrorInitialDelay/" +
		"RetryAfterErrorMaxDelay: delays can't be negative and the max delay can't be lower than the initial one")
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
	// BlockFinality is which block to consider final (typically finalizedBlock)
	BlockFinality aggkittypes.BlockNumberFinality
	// WaitPeriodToCheckCatchUp is the duration to wait before checking again if logs are not yet available
	WaitPeriodToCheckCatchUp types.Duration
	// PeriodToCheckReorgs is the duration to wait before checking for reorgs
	// If is 0 reorgs are checked only when a new block appears
	PeriodToCheckReorgs types.Duration
	// DeveloperMode enables developer mode features like forcing reorgs
	DeveloperMode bool
	// RetryAfterErrorInitialDelay is the delay before retrying a step (or a reorg processing) that failed.
	// It doubles on each consecutive failure (with jitter) up to RetryAfterErrorMaxDelay and resets on success.
	// 0 means the default (500ms)
	RetryAfterErrorInitialDelay types.Duration
	// RetryAfterErrorMaxDelay is the maximum delay between retries of a step that keeps failing
	// (e.g. the RPC is down or rate limiting). 0 means the default (30s)
	RetryAfterErrorMaxDelay types.Duration
}

const (
	defaultBlockChunkSize                  = 10000
	defaultMaxParallelBlockHeaderRetrieval = 30
	defaultWaitPeriodToCheckCatchUp        = time.Second * 10
	defaultPeriodToCheckReorgs             = time.Second * 5
	defaultRetryAfterErrorInitialDelay     = time.Millisecond * 500
	defaultRetryAfterErrorMaxDelay         = time.Second * 30
)

func NewConfigDefault(name string, basePathDB string) Config {
	if basePathDB == "" {
		basePathDB = "/tmp/aggkit/"
	}
	dbPath := path.Join(basePathDB, fmt.Sprintf("%s_multidownloader.sqlite", name))
	return Config{
		Enabled:                         true,
		StoragePath:                     dbPath,
		BlockChunkSize:                  defaultBlockChunkSize,
		MaxParallelBlockHeaderRetrieval: defaultMaxParallelBlockHeaderRetrieval,
		BlockFinality:                   aggkittypes.FinalizedBlock,
		WaitPeriodToCheckCatchUp:        types.NewDuration(defaultWaitPeriodToCheckCatchUp),
		PeriodToCheckReorgs:             types.NewDuration(defaultPeriodToCheckReorgs),
		DeveloperMode:                   false,
		RetryAfterErrorInitialDelay:     types.NewDuration(defaultRetryAfterErrorInitialDelay),
		RetryAfterErrorMaxDelay:         types.NewDuration(defaultRetryAfterErrorMaxDelay),
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
	if cfg.RetryAfterErrorInitialDelay.Duration < 0 || cfg.RetryAfterErrorMaxDelay.Duration < 0 {
		return ErrInvalidRetryAfterErrorDelay
	}
	if cfg.RetryAfterErrorMaxDelay.Duration != 0 &&
		cfg.RetryAfterErrorMaxDelay.Duration < cfg.retryAfterErrorInitialDelay() {
		return ErrInvalidRetryAfterErrorDelay
	}
	if cfg.BlockFinality.IsEmpty() {
		return fmt.Errorf("MultidownloaderConfig.BlockFinality: block finality cannot be empty")
	}
	return nil
}

// retryAfterErrorInitialDelay returns RetryAfterErrorInitialDelay, or its default if it is not set
func (cfg *Config) retryAfterErrorInitialDelay() time.Duration {
	if cfg.RetryAfterErrorInitialDelay.Duration <= 0 {
		return defaultRetryAfterErrorInitialDelay
	}
	return cfg.RetryAfterErrorInitialDelay.Duration
}

// retryAfterErrorMaxDelay returns RetryAfterErrorMaxDelay, or its default if it is not set
func (cfg *Config) retryAfterErrorMaxDelay() time.Duration {
	if cfg.RetryAfterErrorMaxDelay.Duration <= 0 {
		return max(defaultRetryAfterErrorMaxDelay, cfg.retryAfterErrorInitialDelay())
	}
	return cfg.RetryAfterErrorMaxDelay.Duration
}

// newRetryBackoff returns the backoff used to space the retries of a failing step
func (cfg *Config) newRetryBackoff() *retryBackoff {
	return newRetryBackoff(cfg.retryAfterErrorInitialDelay(), cfg.retryAfterErrorMaxDelay())
}

func (cfg *Config) String() string {
	return fmt.Sprintf("MultidownloaderConfig{Enabled:%t, BlockChunkSize:%d, "+
		"MaxParallelBlockHeaderRetrieval:%d, BlockFinality:%s, WaitPeriodToCheckCatchUp:%s, "+
		"PeriodToCheckReorgs:%s, RetryAfterErrorInitialDelay:%s, RetryAfterErrorMaxDelay:%s}",
		cfg.Enabled,
		cfg.BlockChunkSize,
		cfg.MaxParallelBlockHeaderRetrieval,
		cfg.BlockFinality.String(),
		cfg.WaitPeriodToCheckCatchUp.String(),
		cfg.PeriodToCheckReorgs.String(),
		cfg.RetryAfterErrorInitialDelay.String(),
		cfg.RetryAfterErrorMaxDelay.String())
}
