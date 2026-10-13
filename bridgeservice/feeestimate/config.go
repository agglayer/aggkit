package feeestimate

import (
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// D2 defaults of bridge-fee-estimation-plan. Every zero (or otherwise invalid) Config field
// falls back to its default, mirroring how HealthCheckCacheTTL works elsewhere in the bridge
// service. There is deliberately no TOML section for these: they are estimator-internal
// constants that wiring code or tests may override, never part of the wire contract.
const (
	// defaultWindow is the number of latest claims (or bridges, on the s3 side) sampled. It
	// matches the bridge service's MaxPageSize.
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
)

// Config configures one network's estimator. The zero value is usable: every field falls
// back to its D2 default via withDefaults. Only the estimator consults it; none of it leaks
// into the response shapes.
type Config struct {
	// NetworkID is the network the estimated transactions run on: 0 for L1, or the
	// instance's L2 network id.
	NetworkID uint32
	// BridgeAddr is the bridge contract on NetworkID; only transactions sent directly to it
	// are sampled, and view methods are eth_called on it.
	BridgeAddr common.Address
	// Window is the number of latest indexed claims (bridges on the s3 side) sampled.
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
}

// withDefaults returns cfg with every zero or invalid field replaced by its D2 default.
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
	return c
}
