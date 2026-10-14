package feeestimate

import (
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// Estimator defaults. Every zero (or otherwise invalid) Config field falls back to its
// default, mirroring how HealthCheckCacheTTL works elsewhere in the bridge service. There is
// deliberately no TOML section for these: they are estimator-internal constants that wiring
// code or tests may override, never part of the wire contract.
const (
	// defaultWindow is the number of latest claims (or bridges, on the bridge side)
	// sampled. It matches the bridge service's MaxPageSize.
	defaultWindow = 200
	// defaultMinSamples is the minimum number of samples a bucket needs to serve an
	// estimate; a bucket with fewer is unavailable and the estimate fails closed.
	defaultMinSamples = 1
	// defaultPerTokenMinSamples is the per-token repeat-claim bucket size that promotes the
	// repeat estimate from the token-class fallback to the token itself.
	defaultPerTokenMinSamples = 3
	// defaultRPCConcurrency bounds the transaction and receipt RPC fan-out of one statistics
	// recompute.
	defaultRPCConcurrency = 8
	// defaultTxCacheSize is the number of transactions (with their receipts) kept in the LRU.
	defaultTxCacheSize = 2048
	// defaultPercentile is the gasUsed percentile quoted for buckets with enough samples.
	defaultPercentile = 90.0
	// maxPercentile clamps a configured percentile.
	maxPercentile = 100.0
	// defaultStatsTTL is how long a gas-unit statistics snapshot is reused before recompute.
	defaultStatsTTL = 60 * time.Second
	// defaultGasPriceTTL is how long a gas price snapshot is reused before re-reading it.
	defaultGasPriceTTL = 5 * time.Second
	// defaultComputeTimeout bounds one shared recompute behind the TTL caches, detached
	// from every caller's request context (see Config.ComputeTimeout). Sized generously
	// for a cold statistics start: the 200-claim window with its receipts is roughly 400
	// RPCs fanned out over 8 concurrent calls, i.e. ~50 sequential rounds.
	defaultComputeTimeout = 60 * time.Second
	// defaultTokenFactsTTL is how long a bridge view-method answer (wrapper existence,
	// wrapped-token origin) is reused. These are stable on-chain facts, so the TTL only
	// bounds how long a freshly deployed wrapper stays invisible to the claim estimate.
	defaultTokenFactsTTL = 30 * time.Second
)

// Config configures one network's estimator. The zero value is usable: every field falls
// back to its default via withDefaults. Only the estimator consults it; none of it leaks
// into the response shapes.
type Config struct {
	// NetworkID is the network the estimated transactions run on: 0 for L1, or the
	// instance's L2 network id.
	NetworkID uint32
	// BridgeAddr is the bridge contract on NetworkID; only transactions sent directly to it
	// are sampled, and view methods are eth_called on it.
	BridgeAddr common.Address
	// Window is the number of latest indexed claims (bridges on the bridge side) sampled.
	Window int
	// MinSamples is the minimum number of samples a bucket needs to serve an estimate.
	MinSamples int
	// PerTokenMinSamples is the per-token repeat-claim bucket size that promotes the repeat
	// estimate to source "token"; below it the estimate falls back to the token class.
	PerTokenMinSamples int
	// RPCConcurrency bounds the transaction and receipt RPC fan-out of one recompute.
	RPCConcurrency int
	// TxCacheSize is the capacity of the transaction and receipt LRU cache.
	TxCacheSize int
	// Percentile is the gasUsed percentile quoted for buckets with enough samples, in
	// (0, 100].
	Percentile float64
	// StatsTTL is how long a gas-unit statistics snapshot is reused.
	StatsTTL time.Duration
	// GasPriceTTL is how long a gas price snapshot is reused.
	GasPriceTTL time.Duration
	// ComputeTimeout bounds one shared recompute behind the TTL caches (the RPC fan-out of
	// a statistics window, or a gas price read), independently of any caller's request
	// context: concurrent callers share one computation, so one caller's read timeout or
	// disconnect must not abort it. Cold starts are the sizing case (see
	// defaultComputeTimeout).
	ComputeTimeout time.Duration
	// TokenFactsTTL is how long a bridge view-method answer — whether a wrapper exists for
	// an origin token, which origin a wrapped token maps back to — is reused before the
	// estimator eth_calls it again. A fixed (not sliding) TTL: a continuously queried
	// token still refreshes within one TTL.
	TokenFactsTTL time.Duration
}

// withDefaults returns cfg with every zero or invalid field replaced by its default.
func (c Config) withDefaults() Config {
	if c.Window <= 0 {
		c.Window = defaultWindow
	}
	if c.MinSamples <= 0 {
		c.MinSamples = defaultMinSamples
	}
	if c.PerTokenMinSamples <= 0 {
		c.PerTokenMinSamples = defaultPerTokenMinSamples
	}
	if c.RPCConcurrency <= 0 {
		c.RPCConcurrency = defaultRPCConcurrency
	}
	if c.TxCacheSize <= 0 {
		c.TxCacheSize = defaultTxCacheSize
	}
	if c.Percentile <= 0 {
		c.Percentile = defaultPercentile
	}
	if c.Percentile > maxPercentile {
		c.Percentile = maxPercentile
	}
	if c.StatsTTL <= 0 {
		c.StatsTTL = defaultStatsTTL
	}
	if c.GasPriceTTL <= 0 {
		c.GasPriceTTL = defaultGasPriceTTL
	}
	if c.ComputeTimeout <= 0 {
		c.ComputeTimeout = defaultComputeTimeout
	}
	if c.TokenFactsTTL <= 0 {
		c.TokenFactsTTL = defaultTokenFactsTTL
	}
	return c
}
