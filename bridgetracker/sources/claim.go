package sources

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/bridgeservice/client"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/bridgetracker"
	trackertypes "github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
)

// ClaimSource implements bridgetracker.ClaimSource over the destination network's aggkit
// bridge service claims API, filtered by the bridge's global index
type ClaimSource struct {
	services *bridgeServiceClients
}

// NewClaimSource returns a ClaimSource resolving per-network bridge service clients through
// the given finder
func NewClaimSource(finder NetworkURLResolver) *ClaimSource {
	return &ClaimSource{services: newBridgeServiceClients(finder)}
}

// ClaimFor implements bridgetracker.ClaimSource: the bridge is claimed once the
// destination network's bridge service records a claim with the bridge's global index
// (derived from its origin network + deposit count)
func (s *ClaimSource) ClaimFor(
	ctx context.Context, bridge *bridgetracker.BridgeInfo,
) (*trackertypes.ClaimResult, error) {
	svc, err := s.services.aggkitBridgeClientFor(bridge.DestinationNetwork)
	if err != nil {
		return nil, err
	}

	globalIndex := bridgesync.GenerateGlobalIndexForNetworkID(bridge.NetworkID, bridge.DepositCount)
	claims, err := svc.GetClaims(ctx, client.GetClaimsParams{
		NetworkID:   bridge.DestinationNetwork,
		GlobalIndex: globalIndex,
	})
	if isNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fetching claims of global index %s on network %d: %w",
			globalIndex, bridge.DestinationNetwork, err)
	}
	if claims.Count == 0 {
		return nil, nil
	}
	return &trackertypes.ClaimResult{
		ClaimTx:        common.HexToHash(string(claims.Claims[0].TxHash)),
		BlockNumber:    claims.Claims[0].BlockNum,
		BlockTimestamp: claims.Claims[0].BlockTimestamp,
	}, nil
}
