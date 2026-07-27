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
// client}). Networks absent from the map resolve to an error, retried by the engine
type StaticClients map[uint32]aggkittypes.BaseEthereumClienter

// ClientFor implements EthClientResolver
func (s StaticClients) ClientFor(_ context.Context, networkID uint32) (aggkittypes.BaseEthereumClienter, error) {
	c, ok := s[networkID]
	if !ok {
		return nil, fmt.Errorf("no JSON-RPC client configured for network %d", networkID)
	}
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

// clientFor returns the bridge service client of the given network
func (b *bridgeServiceClients) clientFor(networkID uint32) (*client.Client, error) {
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
