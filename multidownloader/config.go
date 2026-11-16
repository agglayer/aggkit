package multidownloader

import (
	"errors"
	"fmt"
	"path"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
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
	// ErrInvalidRetryAfterError is returned when the RetryAfterError policy is invalid
	ErrInvalidRetryAfterError = errors.New("MultidownloaderConfig.RetryAfterError")
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
	// RetryAfterError is the policy that spaces the retries of a step (or a reorg processing) that keeps
	// failing (e.g. the RPC is down or rate limiting), reset on success: RetryMode = "delays" (Delays, a list
	// of concrete delays, the last one is reused) or "backoff" (InitialBackoff * BackoffMultiplier^n up to
	// MaxBackoff). MaxRetries must be -1: the multidownloader never gives up. If RetryMode is not set, the
	// default is used: delays 1s, 2s, 4s, 8s, 16s, 32s, 1m4s, 2m8s, 4m16s, 5m
	RetryAfterError aggkitcommon.RetryPolicyGenericConfig `mapstructure:"RetryAfterError"`
}

const (
	defaultBlockChunkSize                  = 10000
	defaultMaxParallelBlockHeaderRetrieval = 30
	defaultWaitPeriodToCheckCatchUp        = time.Second * 10
	defaultPeriodToCheckReorgs             = time.Second * 5
)

// defaultRetryAfterErrorDelays are the default delays between retries of a failing step: exponential (x2)
// from 1s, capped at 5m (the last one is reused)
var defaultRetryAfterErrorDelays = []time.Duration{
	time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second,
	64 * time.Second, 128 * time.Second, 256 * time.Second, 5 * time.Minute,
}

// newDefaultRetryAfterError returns the default RetryAfterError policy
func newDefaultRetryAfterError() aggkitcommon.RetryPolicyGenericConfig {
	delays := make([]types.Duration, len(defaultRetryAfterErrorDelays))
	for i, d := range defaultRetryAfterErrorDelays {
		delays[i] = types.NewDuration(d)
	}
	return aggkitcommon.RetryPolicyGenericConfig{
		Mode:       aggkitcommon.RetryConfigModeDelays,
		MaxRetries: aggkitcommon.MaxAttemptsInfinite,
		Delays:     delays,
	}
}

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
		RetryAfterError:                 newDefaultRetryAfterError(),
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
	if _, err := cfg.newRetryBackoff(); err != nil {
		return err
	}
	if cfg.BlockFinality.IsEmpty() {
		return fmt.Errorf("MultidownloaderConfig.BlockFinality: block finality cannot be empty")
	}
	return nil
}

// retryAfterError returns the RetryAfterError policy, or the default one if RetryMode is not set
func (cfg *Config) retryAfterError() *aggkitcommon.RetryPolicyGenericConfig {
	if cfg.RetryAfterError.Mode == aggkitcommon.RetryConfigModeNoRetries {
		def := newDefaultRetryAfterError()
		return &def
	}
	return &cfg.RetryAfterError
}

// newRetryBackoff returns the backoff used to space the retries of a failing step
func (cfg *Config) newRetryBackoff() (*retryBackoff, error) {
	policy := cfg.retryAfterError()
	if policy.MaxRetries != aggkitcommon.MaxAttemptsInfinite {
		return nil, fmt.Errorf("%w: MaxRetries must be %d (the multidownloader never gives up), got %d",
			ErrInvalidRetryAfterError, aggkitcommon.MaxAttemptsInfinite, policy.MaxRetries)
	}
	handler, err := policy.NewRetryHandler()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidRetryAfterError, err)
	}
	return newRetryBackoff(handler), nil
}

func (cfg *Config) String() string {
	return fmt.Sprintf("MultidownloaderConfig{Enabled:%t, BlockChunkSize:%d, "+
		"MaxParallelBlockHeaderRetrieval:%d, BlockFinality:%s, WaitPeriodToCheckCatchUp:%s, "+
		"PeriodToCheckReorgs:%s, RetryAfterError:%s}",
		cfg.Enabled,
		cfg.BlockChunkSize,
		cfg.MaxParallelBlockHeaderRetrieval,
		cfg.BlockFinality.String(),
		cfg.WaitPeriodToCheckCatchUp.String(),
		cfg.PeriodToCheckReorgs.String(),
		cfg.retryAfterError().String())
}
