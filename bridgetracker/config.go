package bridgetracker

import (
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

// DefaultRetentionPeriod is the default Config.RetentionPeriod (see
// DefaultEngineRetentionPeriod for the semantics; both must stay in sync with the [Tracker]
// section of the proxy's default config)
var DefaultRetentionPeriod = types.Duration{Duration: DefaultEngineRetentionPeriod}

// DefaultL1BlockFinality is the default Config.L1BlockFinality: an L1 bridge's creating tx is
// only accepted once its receipt reaches this finality, so a reorg cannot leave the tracker
// permanently following an orphaned deposit/block
var DefaultL1BlockFinality = aggkittypes.LatestBlock

// DefaultL2BlockFinality is the default Config.L2BlockFinality: an L2 bridge's creating tx is
// only accepted once its receipt reaches this finality, so a reorg cannot leave the tracker
// permanently following an orphaned deposit/block
var DefaultL2BlockFinality = aggkittypes.LatestBlock

// DefaultMaxTrackedBridges is the default Config.MaxTrackedBridges: how many distinct bridges
// the in-memory registry accepts before refusing new ones (see registry.go's memoryRegistry).
const DefaultMaxTrackedBridges = 100_000

// Config holds the configuration of the bridge tracker service. Only the mapstructure-tagged
// fields come from the configuration file; the rest are wired programmatically by the binary
// (see proxy/cmd)
type Config struct {
	// RetentionPeriod is how long a terminal bridge (Finished, or failed to ever resolve)
	// stays queryable before the tracker forgets it. Clients polling or subscribed observe
	// the terminal TrackingStatus during this window; once forgotten, a new request for the
	// same tx re-registers it and tracking restarts from scratch — the retry path for a tx
	// the tracker gave up on
	RetentionPeriod types.Duration `mapstructure:"RetentionPeriod"`

	// L1BlockFinality is the finality level a bridge's creating tx receipt must reach on L1
	// (network 0) before the tracker accepts it (see sources.BridgeEventSource): accepting
	// a receipt from a block that later gets reorged out would otherwise leave the tracker
	// permanently following an orphaned deposit count/block, since a resolved bridge is never
	// re-checked (see TrackingBridgeTx.IsDone)
	L1BlockFinality aggkittypes.BlockNumberFinality `jsonschema:"enum=PendingBlock,enum=LatestBlock,enum=SafeBlock,enum=FinalizedBlock,enum=EarliestBlock" mapstructure:"L1BlockFinality"` //nolint:lll

	// L2BlockFinality is the finality level a bridge's creating tx receipt must reach on any
	// L2 (non-zero network) before the tracker accepts it; see L1BlockFinality for the reasoning
	L2BlockFinality aggkittypes.BlockNumberFinality `jsonschema:"enum=PendingBlock,enum=LatestBlock,enum=SafeBlock,enum=FinalizedBlock,enum=EarliestBlock" mapstructure:"L2BlockFinality"` //nolint:lll

	// BridgeAddrs is the static networkID -> canonical bridge contract address map used to
	// reject a BridgeEvent log emitted by a contract other than the origin network's real
	// bridge (see sources.BridgeEventSource). A network absent from this map (the default,
	// empty map) still matches logs on the event signature alone.
	BridgeAddrs map[uint32]common.Address `mapstructure:"BridgeAddrs"`

	// MaxTrackedBridges bounds how many distinct bridges the in-memory registry (see Registry)
	// accepts at once; a request that would exceed it fails instead of registering the bridge.
	// A value <= 0 falls back to DefaultMaxTrackedBridges. Only applies to the default in-memory
	// adapter — ignored when Registry is set to a custom implementation.
	MaxTrackedBridges int `mapstructure:"MaxTrackedBridges"`

	Logger aggkitcommon.Logger `mapstructure:"-"`

	// ConfigSHA1 is the sha1sum (hex) of the configuration the binary was started with,
	// exposed by the health endpoint to check that all instances behind a proxy run the
	// same configuration
	ConfigSHA1 string `mapstructure:"-"`

	// Registry is the supervised-bridges subsystem to use. Leave nil to get the in-memory
	// adapter (single instance); inject a shared-store implementation so several tracker
	// instances behind a proxy answer for any registered tx
	Registry SupervisedRegistry `mapstructure:"-"`
}
