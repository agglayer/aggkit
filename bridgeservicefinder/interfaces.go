package bridgeservicefinder

import (
	"context"
	"errors"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayermanager"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// Source identifies which of the three prioritised sources produced a cached bridge service URL.
// The order of the constants encodes priority: a lower value outranks a higher value. This is used
// both for initial resolution and to decide whether a live on-chain event is allowed to overwrite
// an existing cache entry.
type Source uint8

const (
	// SourceConfig is the highest-priority source: a user-provided static networkID -> url entry.
	// Entries from this source are terminal and are never overwritten by on-chain events.
	SourceConfig Source = iota
	// SourceMetadata is the aggchain contract's aggchainMetadata["BRIDGE_SERVICE_URL"] value.
	// It outranks the derived trusted-sequencer URL.
	SourceMetadata
	// SourceSequencerURL is the lowest-priority source: the rollup's trustedSequencerURL() with the
	// port replaced by DefaultBridgeServicePort. Available on legacy rollups as the universal fallback.
	SourceSequencerURL
)

var (
	// ErrURLNotFound is returned by GetURL when no bridge service URL is cached for the network.
	ErrURLNotFound = errors.New("bridge service url not found for network")
	// ErrNoSourceAvailable is returned by the resolver when none of the three sources yields a URL.
	ErrNoSourceAvailable = errors.New("no bridge service url source available for network")
	// ErrSourceNotAvailable is returned by a RollupContractReader method when the underlying contract
	// call reverts or the method does not exist on that rollup type (e.g. AggchainMetadata on a legacy
	// rollup). Callers MUST treat this as "fall through to the next source", NOT as a hard failure.
	// Genuine transport/RPC errors are returned as other (wrapped) errors and should abort resolution.
	ErrSourceNotAvailable = errors.New("bridge service url source not available on this contract")
)

// NetworkURLs bundles the two endpoints the finder resolves for a network: the bridge service REST
// URL and the network's JSON-RPC endpoint. The JSON-RPC endpoint is the Config.RPCURLs override if
// present, otherwise the rollup's on-chain trustedSequencerURL() served verbatim (no port
// substitution); it may be empty when neither source is available (e.g. a config-only network such
// as L1, or a legacy rollup whose call reverts). BridgeURL is always non-empty on a successful
// GetURL.
type NetworkURLs struct {
	// BridgeURL is the bridge service REST URL resolved via the three-source priority algorithm.
	BridgeURL string
	// JSONRPCURL is the network's JSON-RPC endpoint (config override or raw trustedSequencerURL).
	JSONRPCURL string
}

// Finder resolves and serves the bridge service URL and JSON-RPC endpoint for every network
// attached to a rollup manager. It is the package's public type. See doc.go for the design.
type Finder interface {
	// Start builds the initial networkID -> url cache for all attached networks, probes each resolved
	// service via /health, and starts the finality-based event-polling loop. It blocks until the
	// initial cache is built (or ctx is cancelled) and then runs the poller in the background until
	// ctx is done. Depending on Config.RequireAllHealthyOnStart it may return an error if any resolved
	// service is unreachable at startup.
	Start(ctx context.Context) error
	// GetURL returns the currently cached URLs (bridge service + JSON-RPC) for the given networkID, or
	// ErrURLNotFound if nothing is cached. networkID follows the mapping documented in doc.go
	// (networkID == rollupID; network 0 is L1 and is only served if provided via Config.BridgeURLs).
	GetURL(networkID uint32) (NetworkURLs, error)
	// NetworkIDs returns the networkIDs of every network currently resolved — i.e. every network
	// GetURL would presently succeed for. Used by callers that need to enumerate every configured
	// bridge service rather than query one network at a time (e.g. the bridge tracker's activity
	// scanner). Order is unspecified.
	NetworkIDs() []uint32
	// BridgeAddress returns the bridge contract address for networkID, in priority order:
	// Config.BridgeAddress[networkID] if set; else Config.BridgeAddress[0] if set (network 0's
	// override doubles as the default for every network without its own, since it is typically the
	// shared L1 bridge address); else the rollup manager's own on-chain BridgeAddress() — resolved
	// once and cached forever, since it is an immutable constructor parameter of the rollup manager.
	// A network whose bridge contract differs from that default needs its own
	// Config.BridgeAddress override. Returns an error only when no override applies and the
	// on-chain default could not be resolved (e.g. a transport failure) — such a failure is not
	// cached, so the next call retries.
	BridgeAddress(ctx context.Context, networkID uint32) (common.Address, error)
}

// RollupManagerQuerier enumerates the rollups attached to a rollup manager and reads their data.
// It abstracts the agglayermanager binding so the finder can iterate networks and locate each
// rollup's contract address. networkID == rollupID for the returned rollups; iteration covers
// rollupID = 1 .. RollupCount().
type RollupManagerQuerier interface {
	// RollupCount returns the number of rollups (N) attached to the rollup manager. Valid rollup ids
	// are 1..N inclusive; id 0 is reserved for L1 and is never returned here.
	RollupCount(opts *bind.CallOpts) (uint32, error)
	// RollupIDToRollupData returns the on-chain data for the given rollupID. The RollupContract field
	// holds the address of the rollup's consensus contract, which is also the aggchain contract on
	// aggchain-type rollups. ChainID is the rollup's L2 chain id.
	RollupIDToRollupData(opts *bind.CallOpts, rollupID uint32) (
		agglayermanager.AgglayerManagerRollupDataReturn, error)
	// BridgeAddress returns the bridge contract address the rollup manager was constructed with: an
	// immutable constructor parameter, so the same value for the lifetime of the contract.
	BridgeAddress(opts *bind.CallOpts) (common.Address, error)
}

// RollupContractReader reads the two on-chain sources (metadata and trusted-sequencer URL) from a
// single rollup's contract address. Implementations wrap the aggchainbase and polygonrollupbaseetrog
// bindings. Methods MUST return ErrSourceNotAvailable (not a generic error) when the call reverts or
// the method is absent on that rollup type, so the resolver can fall through cleanly.
type RollupContractReader interface {
	// AggchainMetadata reads the aggchainMetadata(string) mapping for the given key (source #2). Returns
	// ErrSourceNotAvailable if the contract is not an aggchain (method absent / reverts). An empty
	// returned value with a nil error means the key is unset and should be treated as a fall-through.
	AggchainMetadata(ctx context.Context, key string) (string, error)
	// TrustedSequencerURL reads trustedSequencerURL() (source #3, before port substitution). Returns
	// ErrSourceNotAvailable if the method is absent / reverts on this contract.
	TrustedSequencerURL(ctx context.Context) (string, error)
}

// RollupContractReaderFactory builds a RollupContractReader bound to a specific rollup contract
// address using the shared eth client. It mirrors the RollupManagerFactoryFunc pattern used in
// etherman/querier so the concrete binding construction is injectable for tests.
type RollupContractReaderFactory func(addr common.Address, client aggkittypes.BaseEthereumClienter) (
	RollupContractReader, error)

// LogFilterer is the minimal eth-client surface the listener needs: the ability to fetch logs over
// a block range and to resolve a BlockNumberFinality tag (e.g. FinalizedBlock) to a concrete block
// number. It is a strict subset of aggkittypes.BaseEthereumClienter, kept narrow so tests can mock it
// in isolation. aggkittypes.BaseEthereumClienter (the default eth client) satisfies it.
type LogFilterer interface {
	// FilterLogs returns the logs matching the query. Used to scan for the watched events over a
	// finality-bounded, chunked block range.
	FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error)
	// BlockNumber returns the current chain head. Kept for callers that need the latest head directly.
	BlockNumber(ctx context.Context) (uint64, error)
	// CustomHeaderByNumber resolves a block finality tag (with any offset) to its block header, whose
	// Number bounds the upper block of each scan window. It is what makes Config.BlockFinality (e.g.
	// FinalizedBlock/SafeBlock) honoured rather than always scanning to the latest head.
	CustomHeaderByNumber(ctx context.Context, number *aggkittypes.BlockNumberFinality) (
		*aggkittypes.BlockHeader, error)
}

// HealthChecker probes whether a resolved bridge service URL is reachable and healthy. The default
// implementation issues an HTTP GET against Config.HealthCheckPath with Config.HealthCheckTimeout and
// reports healthy iff the response status is 2xx. It is injectable so tests can stub health results.
type HealthChecker interface {
	// IsHealthy reports whether the bridge service reachable at baseURL is healthy. It must never block
	// longer than its configured timeout and returns false (rather than an error) for any failure,
	// so callers can use the boolean directly in the health-gating rule.
	IsHealthy(ctx context.Context, baseURL string) bool
}
