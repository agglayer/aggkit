// Package sources implements the driven fact ports of the bridge tracker engine
// (bridgetracker.BridgeEventSource, GERSource, ClaimSource) over the real backends: the
// per-network JSON-RPC endpoints and the aggkit bridge service REST API, both resolved per
// network through the bridgeservicefinder.
//
// Current coverage: L1 -> L2 bridges. The CertificateSource (needed by L2-originated
// bridges) is not implemented yet; NotImplementedCertificateSource stubs it so the engine
// can be wired — L2-origin bridges fail their resolution (and are retried) until the real
// adapter lands.
package sources

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/agglayer/aggkit/bridgeservice/client"
	"github.com/agglayer/aggkit/bridgeservicefinder"
	"github.com/agglayer/aggkit/bridgetracker"
	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/etherman"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	aggkittypes "github.com/agglayer/aggkit/types"
)

// NetworkURLResolver is the slice of the bridgeservicefinder.Finder the sources need: the
// per-network bridge service / JSON-RPC endpoints
type NetworkURLResolver interface {
	GetURL(networkID uint32) (bridgeservicefinder.NetworkURLs, error)
}

// EthClientResolver resolves the JSON-RPC client of a network
type EthClientResolver interface {
	ClientFor(ctx context.Context, networkID uint32) (aggkittypes.BaseEthereumClienter, error)
}

// StaticClients is a fixed networkID -> client EthClientResolver (e.g. {0: the proxy's L1
// client}). The map never changes at runtime, so a network absent from it resolves to a
// permanent bridgetracker.ErrSourceUnavailable, not retried by the engine
type StaticClients map[uint32]aggkittypes.BaseEthereumClienter

// ClientFor implements EthClientResolver
func (s StaticClients) ClientFor(_ context.Context, networkID uint32) (aggkittypes.BaseEthereumClienter, error) {
	c, ok := s[networkID]
	if !ok {
		return nil, fmt.Errorf("%w: no JSON-RPC client configured for network %d",
			bridgetracker.ErrSourceUnavailable, networkID)
	}
	return c, nil
}

// FinderClients is an EthClientResolver that resolves each network's JSON-RPC endpoint
// through the bridgeservicefinder and dials (and caches, keyed by URL) one client per
// endpoint. Overrides pin specific networks to pre-built clients — e.g. {0: the binary's L1
// client, which carries its own retry configuration} — and are never asked to the finder.
//
// Unlike StaticClients, a network the finder cannot resolve is a transient failure (no
// bridgetracker.ErrSourceUnavailable): the finder discovers rollups live, so the engine
// keeps retrying until the network appears or the bridge exhausts its timeout
type FinderClients struct {
	finder    NetworkURLResolver
	overrides StaticClients

	mu      sync.Mutex
	clients map[string]aggkittypes.BaseEthereumClienter
	// dial builds the client of one URL, injectable for tests
	dial func(ctx context.Context, url string) (aggkittypes.BaseEthereumClienter, error)
}

// NewFinderClients returns a FinderClients resolving per-network JSON-RPC endpoints through
// finder, with overrides (may be nil) taking precedence
func NewFinderClients(
	logger aggkitcommon.Logger, finder NetworkURLResolver, overrides StaticClients,
) *FinderClients {
	return &FinderClients{
		finder:    finder,
		overrides: overrides,
		clients:   make(map[string]aggkittypes.BaseEthereumClienter),
		dial: func(ctx context.Context, url string) (aggkittypes.BaseEthereumClienter, error) {
			cfg := ethermanconfig.NewDefaultRPCClientConfig()
			cfg.URL = url
			return etherman.NewRPCClient(ctx, logger, *cfg)
		},
	}
}

// ClientFor implements EthClientResolver. URLs are re-resolved on every call (the finder
// refreshes them from on-chain events); the cache only avoids re-dialing a stable URL
func (f *FinderClients) ClientFor(ctx context.Context, networkID uint32) (aggkittypes.BaseEthereumClienter, error) {
	if c, ok := f.overrides[networkID]; ok {
		return c, nil
	}

	urls, err := f.finder.GetURL(networkID)
	if err != nil {
		return nil, fmt.Errorf("resolving JSON-RPC URL for network %d: %w", networkID, err)
	}
	if urls.JSONRPCURL == "" {
		return nil, fmt.Errorf("no JSON-RPC URL resolved for network %d", networkID)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.clients[urls.JSONRPCURL]; ok {
		return c, nil
	}
	c, err := f.dial(ctx, urls.JSONRPCURL)
	if err != nil {
		return nil, fmt.Errorf("dialing JSON-RPC client of network %d at %s: %w",
			networkID, urls.JSONRPCURL, err)
	}
	f.clients[urls.JSONRPCURL] = c
	return c, nil
}

// bridgeServiceClients resolves and caches one bridge service REST client per resolved base
// URL. URLs are re-resolved on every call (the finder refreshes them from on-chain events);
// the cache only avoids rebuilding http.Clients for a stable URL
type bridgeServiceClients struct {
	finder NetworkURLResolver

	mu      sync.Mutex
	clients map[string]*client.Client
}

func newBridgeServiceClients(finder NetworkURLResolver) *bridgeServiceClients {
	return &bridgeServiceClients{
		finder:  finder,
		clients: make(map[string]*client.Client),
	}
}

// aggkitBridgeClientFor returns the aggkit bridge service client of the given network
func (b *bridgeServiceClients) aggkitBridgeClientFor(networkID uint32) (*client.Client, error) {
	urls, err := b.finder.GetURL(networkID)
	if err != nil {
		return nil, fmt.Errorf("resolving bridge service URL for network %d: %w", networkID, err)
	}
	if urls.BridgeURL == "" {
		return nil, fmt.Errorf("no bridge service URL resolved for network %d", networkID)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if c, ok := b.clients[urls.BridgeURL]; ok {
		return c, nil
	}
	c := client.New(client.Config{BaseURL: urls.BridgeURL})
	b.clients[urls.BridgeURL] = c
	return c, nil
}

// isNotFound reports whether a bridge service error means "the resource does not exist
// (yet)" as opposed to a transient failure. Besides the typed client.ErrNotFound (HTTP 404),
// the l1-info-tree-index and injected-l1-info-leaf endpoints currently answer 500 with a
// "not found" message while the bridge is not covered / the leaf not injected, so the
// message is also matched.
// TODO: make those endpoints answer 404 so the string match can be dropped
func isNotFound(err error) bool {
	return err != nil &&
		(errors.Is(err, client.ErrNotFound) || strings.Contains(err.Error(), "not found"))
}

// NotImplementedCertificateSource stubs bridgetracker.CertificateSource until the agglayer
// adapter lands: every call errors, so L2-originated bridges keep being retried by the
// engine without ever advancing past the certificate steps
type NotImplementedCertificateSource struct{}

// CertificateFor implements bridgetracker.CertificateSource
func (NotImplementedCertificateSource) CertificateFor(
	_ context.Context, _ *bridgetracker.BridgeInfo,
) (*types.CertificateData, error) {
	return nil, errors.New("certificate source not implemented yet (L2-originated bridges are not supported)")
}

// NotImplementedLERSource stubs bridgetracker.LERSource until a bridgeservice endpoint
// resolves the Local Exit Root covering an L2 deposit: every call errors, so L2-originated
// bridges keep being retried by the engine without ever advancing past WaitingLERUpdate
type NotImplementedLERSource struct{}

// OriginLER implements bridgetracker.LERSource
func (NotImplementedLERSource) OriginLER(
	_ context.Context, _ *bridgetracker.BridgeInfo,
) (*types.LERUpdateResult, error) {
	return nil, errors.New("LER source not implemented yet (L2-originated bridges are not supported)")
}
