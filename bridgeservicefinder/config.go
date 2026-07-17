package bridgeservicefinder

import (
	"time"

	"github.com/agglayer/aggkit/config/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

const (
	// DefaultBridgeServicePort is the bridge REST service port substituted into a trusted-sequencer
	// URL when it is used as source #3. It matches the repo's default bridge REST Port (config/default.go).
	DefaultBridgeServicePort = 5577

	// MetadataBridgeServiceURLKey is the aggchainMetadata mapping key holding the bridge service URL
	// (source #2). Live AggchainMetadataSet events are matched on keccak256 of this string.
	MetadataBridgeServiceURLKey = "BRIDGE_SERVICE_URL"

	// DefaultBlockFinality is the default finality tag bounding the event-scan upper block.
	DefaultBlockFinality = "FinalizedBlock"
	// DefaultBlockChunkSize is the default number of blocks scanned per FilterLogs request.
	DefaultBlockChunkSize = uint64(10_000)
	// DefaultHealthCheckPath is the default HTTP path probed to assert a bridge service is alive.
	DefaultHealthCheckPath = "/health"
	// DefaultRequireAllHealthyOnStart controls whether Start fails if any resolved service is
	// unreachable during initial cache construction. Default is false: record the unhealthy state
	// but keep the entry so live updates can heal it.
	DefaultRequireAllHealthyOnStart = false
)

var (
	// DefaultPollInterval is the default period between event-scan iterations.
	DefaultPollInterval = types.Duration{Duration: 30 * time.Second} //nolint:mnd
	// DefaultHealthCheckTimeout is the default per-probe timeout for the health check HTTP request.
	DefaultHealthCheckTimeout = types.Duration{Duration: 5 * time.Second} //nolint:mnd
)

// Config holds the configuration for the bridge service finder.
//
// Defaults for each field are defined as the Default* constants in this file; wiring them into the
// global config defaults (config/default.go) is done in a later step.
type Config struct {
	// RollupManagerAddr is the address of the RollupManager / AgglayerManager contract on L1 from which
	// the set of attached networks (rollups 1..RollupCount) is enumerated.
	RollupManagerAddr common.Address `mapstructure:"RollupManagerAddr"`

	// URLs is the highest-priority (SourceConfig) static override map from networkID to bridge service
	// URL. Any networkID present here is served verbatim and is never overwritten by on-chain events.
	// This is also the only way to provide a URL for network 0 (L1), which is not enumerated on-chain.
	URLs map[uint32]string `mapstructure:"URLs"`

	// BlockFinality is the finality level used to bound the upper block of each event scan, so the
	// finder does not react to logs that may still be reorged away. See aggkittypes.BlockNumberFinality.
	BlockFinality aggkittypes.BlockNumberFinality `jsonschema:"enum=PendingBlock,enum=LatestBlock,enum=SafeBlock,enum=FinalizedBlock,enum=EarliestBlock" mapstructure:"BlockFinality"` //nolint:lll

	// PollInterval is the period between event-scan iterations of the listener loop.
	PollInterval types.Duration `mapstructure:"PollInterval"`

	// BlockChunkSize is the maximum number of blocks queried per FilterLogs request while scanning.
	BlockChunkSize uint64 `mapstructure:"BlockChunkSize"`

	// HealthCheckPath is the HTTP path appended to a resolved bridge service URL to probe liveness.
	HealthCheckPath string `mapstructure:"HealthCheckPath"`

	// HealthCheckTimeout is the timeout applied to each health-check HTTP request.
	HealthCheckTimeout types.Duration `mapstructure:"HealthCheckTimeout"`

	// RequireAllHealthyOnStart, when true, makes Start return an error if any resolved bridge service
	// is unreachable during initial cache construction. When false, unreachable services are cached
	// with healthy=false and may be healed by a later on-chain update per the health-gating rule.
	RequireAllHealthyOnStart bool `mapstructure:"RequireAllHealthyOnStart"`
}
