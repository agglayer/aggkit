package proof

import (
	"context"
	"errors"
	"fmt"

	"github.com/agglayer/aggkit/bridgeservice/client"
	bridgetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgeservicefinder"
)

// injectedLeafClient is the subset of bridgeservice/client.Client the gate needs.
type injectedLeafClient interface {
	GetInjectedL1InfoLeaf(ctx context.Context, networkID, leafIndex int) (*bridgetypes.L1InfoTreeLeafResponse, error)
}

// BridgeServiceGERGate implements L2GERSyncer by querying the DESTINATION network's bridge service
// GET /bridge/v1/injected-l1-info-leaf endpoint. The destination network id is per-claimer, resolved
// through a bridgeservicefinder.Finder on every call rather than cached at construction.
type BridgeServiceGERGate struct {
	finder    urlResolver
	newClient func(baseURL string) injectedLeafClient
	networkID uint32 // destination network of the owning claimer
}

var _ L2GERSyncer = (*BridgeServiceGERGate)(nil)

// NewBridgeServiceGERGate builds a gate for a destination network reachable through finder.
func NewBridgeServiceGERGate(finder bridgeservicefinder.Finder, networkID uint32) *BridgeServiceGERGate {
	return &BridgeServiceGERGate{
		finder: finder,
		newClient: func(baseURL string) injectedLeafClient {
			return client.New(client.Config{BaseURL: baseURL})
		},
		networkID: networkID,
	}
}

// GetFirstGERAfterL1InfoTreeIndex implements L2GERSyncer. It resolves the destination network's bridge
// service URL and asks it for the first injected L1 info leaf at or after atOrAfterL1InfoTreeIndex.
// A 404 (no injected GER covers the index yet) is mapped to ErrGERNotInjected so the caller retries;
// any other error is a hard failure.
func (g *BridgeServiceGERGate) GetFirstGERAfterL1InfoTreeIndex(
	ctx context.Context, atOrAfterL1InfoTreeIndex uint32,
) (uint32, error) {
	baseURL, err := g.finder.GetURL(g.networkID)
	if err != nil {
		return 0, fmt.Errorf("resolve bridge service url for destination network %d: %w", g.networkID, err)
	}

	resp, err := g.newClient(baseURL).GetInjectedL1InfoLeaf(ctx, int(g.networkID), int(atOrAfterL1InfoTreeIndex))
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			return 0, ErrGERNotInjected
		}
		return 0, fmt.Errorf(
			"get injected l1 info leaf from destination network %d bridge service: %w", g.networkID, err)
	}
	if resp == nil {
		return 0, fmt.Errorf(
			"get injected l1 info leaf from destination network %d bridge service: empty response", g.networkID)
	}

	return resp.L1InfoTreeIndex, nil
}
