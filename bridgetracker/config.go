package bridgetracker

import (
	"fmt"
	"time"

	"github.com/agglayer/aggkit/agglayer"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

// DefaultRetentionPeriod is the default Config.RetentionPeriod (see
// DefaultEngineRetentionPeriod for the semantics; both must stay in sync with the [Tracker]
// section of the proxy's default config)
var DefaultRetentionPeriod = types.Duration{Duration: DefaultEngineRetentionPeriod}

// defaultRegisterResolveTimeoutDuration is the time.Duration backing DefaultRegisterResolveTimeout
const defaultRegisterResolveTimeoutDuration = 3 * time.Second

// DefaultRegisterResolveTimeout is the default Config.RegisterResolveTimeout (must stay in
// sync with the [Tracker] section of the proxy's default config)
var DefaultRegisterResolveTimeout = types.Duration{Duration: defaultRegisterResolveTimeoutDuration}

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

// DefaultIdleTimeout is the default Config.IdleTimeout (see DefaultEngineIdleTimeout for the
// semantics; both must stay in sync with the [Tracker] section of the proxy's default config)
var DefaultIdleTimeout = types.Duration{Duration: DefaultEngineIdleTimeout}

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

	// IdleTimeout is how long a bridge — terminal or still active — stays in the registry once
	// nobody has read it (no Get/GetAndAwait) and it has no active WebSocket subscriber. Unlike
	// RetentionPeriod, this applies regardless of TrackingStatus: a bridge that never resolves
	// and that nobody is polling or subscribed to would otherwise stay supervised (and in
	// memory) forever. A value <= 0 falls back to DefaultIdleTimeout
	IdleTimeout types.Duration `mapstructure:"IdleTimeout"`

	// RegisterResolveTimeout is how long the GetTxStatus endpoint waits, the first time a tx is
	// registered, for the tracking engine's immediate resolution attempt (triggered right away
	// instead of on the next poll tick, see Registry.GetAndAwait) to produce an update before
	// answering. A value <= 0 disables the wait: the first response is always the bare
	// Registered snapshot, exactly as before this field existed. Looking up an already-registered
	// tx never waits, regardless of this setting.
	RegisterResolveTimeout types.Duration `mapstructure:"RegisterResolveTimeout"`

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

	// L1GlobalExitRootAddress is the L1 GlobalExitRoot contract address (see sources.GERSource)
	L1GlobalExitRootAddress common.Address `mapstructure:"L1GlobalExitRootAddress"`

	// MaxTrackedBridges bounds how many distinct bridges the in-memory registry (see Registry)
	// accepts at once; a request that would exceed it fails instead of registering the bridge —
	// reaching the cap never evicts an existing entry to make room, so RetentionPeriod and
	// IdleTimeout are what keep the registry under it during normal operation. A value <= 0
	// falls back to DefaultMaxTrackedBridges. Only applies to the default in-memory adapter —
	// ignored when Registry is set to a custom implementation.
	MaxTrackedBridges int `mapstructure:"MaxTrackedBridges"`

	// AgglayerClient configures the client used to query the agglayer for a bridge's covering
	// certificate and that certificate's current status (see sources.CertificateSource)
	AgglayerClient agglayer.ClientConfig `mapstructure:"AgglayerClient"`

	Logger aggkitcommon.Logger `mapstructure:"-"`

	// ConfigSHA1 is the sha1sum (hex) of the configuration the binary was started with,
	// exposed by the health endpoint to check that all instances behind a proxy run the
	// same configuration
	ConfigSHA1 string `mapstructure:"-"`

	// Registry is the supervised-bridges subsystem to use. Leave nil to get the in-memory
	// adapter (single instance); inject a shared-store implementation so several tracker
	// instances behind a proxy answer for any registered tx
	Registry SupervisedRegistry `mapstructure:"-"`

	// CORS mirrors the proxy's REST.CORS: it governs which origins may open a WebSocket
	// connection to this tracker's endpoints (see aggkitcommon.CORSConfig.OriginAllowed for
	// why WebSocket needs its own check instead of reusing the REST CORS headers). Wired
	// programmatically from REST.CORS by the binary, not read directly from [Tracker].
	CORS aggkitcommon.CORSConfig `mapstructure:"-"`

	// ActivityScanner and ActivityClaims wire the optional GET /activity/from/{from_address}
	// endpoint (see ActivityCache): ActivityScanner scans every configured bridge service for
	// bridges sent by an address, ActivityClaims resolves each one's claim state. Both are
	// wired programmatically by the binary (see sources.ActivitySource, which implements
	// both); leaving either nil leaves the endpoint unregistered entirely.
	ActivityScanner ActivityBridgeScanner `mapstructure:"-"`
	ActivityClaims  ActivityClaimChecker  `mapstructure:"-"`

	// ActivityIdleTimeout is how long a from_address's activity cache (see ActivityCache) stays
	// in memory with no GET /activity/from/{from_address} call for it, before being forgotten
	// entirely (bridges, claim state, everything cached for it). Same semantics as IdleTimeout,
	// a separate knob because it governs a different cache. A value <= 0 falls back to
	// DefaultIdleTimeout.
	ActivityIdleTimeout types.Duration `mapstructure:"ActivityIdleTimeout"`
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if c.L1GlobalExitRootAddress == (common.Address{}) {
		return fmt.Errorf("[Tracker].L1GlobalExitRootAddress is not set (zero address): " +
			"the L1 GlobalExitRoot contract address is required for L1->L2 bridge tracking to work")
	}
	return nil
}
