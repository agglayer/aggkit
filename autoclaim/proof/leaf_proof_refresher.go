package proof

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/bridgeservice/client"
	bridgetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgeservicefinder"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

// urlResolver is the subset of bridgeservicefinder.Finder the refresher needs.
type urlResolver interface {
	GetURL(networkID uint32) (string, error)
}

// claimProofClient is the subset of bridgeservice/client.Client the refresher needs.
type claimProofClient interface {
	GetClaimProof(ctx context.Context, networkID, leafIndex, depositCount uint32) (*bridgetypes.ClaimProof, error)
}

// BridgeServiceLeafProofRefresher implements LeafProofRefresher by resolving a source network's bridge
// service URL through a bridgeservicefinder.Finder and fetching a fresh leaf-to-LER Merkle proof from
// its GET /bridge/v1/claim-proof endpoint. A single instance is shared by one (per-destination)
// claimer across every source network it may need to refresh a proof for, so the source URL is
// resolved dynamically on every call rather than cached at construction.
type BridgeServiceLeafProofRefresher struct {
	finder    urlResolver
	newClient func(baseURL string) claimProofClient
}

var _ LeafProofRefresher = (*BridgeServiceLeafProofRefresher)(nil)

// NewBridgeServiceLeafProofRefresher builds a LeafProofRefresher backed by finder. Each source
// network's bridge service is reached through a client constructed from its resolved URL.
func NewBridgeServiceLeafProofRefresher(finder bridgeservicefinder.Finder) *BridgeServiceLeafProofRefresher {
	return &BridgeServiceLeafProofRefresher{
		finder: finder,
		newClient: func(baseURL string) claimProofClient {
			return client.New(client.Config{BaseURL: baseURL})
		},
	}
}

// RefreshLeafProof implements LeafProofRefresher. It resolves sourceNetwork's bridge service URL and
// fetches a fresh leaf-to-LER proof for (leafIndex, depositCount) from that service's /claim-proof
// endpoint. Any resolution or fetch error (unresolved URL, transient network error, 404 because the
// source has not synced the requested leaf yet) is returned as-is; the caller (RollupPreparer) treats
// any refresh error as "not ready yet", not a hard failure.
func (r *BridgeServiceLeafProofRefresher) RefreshLeafProof(
	ctx context.Context, sourceNetwork, leafIndex, depositCount uint32,
) (treetypes.Proof, error) {
	baseURL, err := r.finder.GetURL(sourceNetwork)
	if err != nil {
		return treetypes.Proof{}, fmt.Errorf(
			"resolve bridge service url for source network %d: %w", sourceNetwork, err)
	}

	resp, err := r.newClient(baseURL).GetClaimProof(ctx, sourceNetwork, leafIndex, depositCount)
	if err != nil {
		return treetypes.Proof{}, fmt.Errorf(
			"get claim proof from source network %d bridge service: %w", sourceNetwork, err)
	}
	if resp == nil {
		return treetypes.Proof{}, fmt.Errorf(
			"get claim proof from source network %d bridge service: empty response", sourceNetwork)
	}

	return toTreeProof(resp.ProofLocalExitRoot), nil
}

// toTreeProof converts a bridge service Merkle proof DTO into the internal tree proof shape. Both
// types are fixed [tree.DefaultHeight] arrays, so copying up to len(out) never exceeds either bound.
func toTreeProof(proof bridgetypes.Proof) treetypes.Proof {
	var out treetypes.Proof
	for i := 0; i < len(out) && i < len(proof); i++ {
		out[i] = common.HexToHash(string(proof[i]))
	}
	return out
}
