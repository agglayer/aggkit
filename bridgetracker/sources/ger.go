package sources

import (
	"context"
	"errors"
	"fmt"

	"github.com/agglayer/aggkit/bridgetracker"
	trackertypes "github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
)

// errNotCoveredYet marks a bridge not covered by any L1 info tree leaf yet, as opposed to a
// transient failure (URL resolution, network). It never escapes this package
var errNotCoveredYet = errors.New("bridge not covered by any L1 info tree leaf yet")

// GERSource implements bridgetracker.GERSource over the destination network's aggkit bridge
// service: every aggkit bridge service syncs the L1 info tree, and the destination's one is
// the instance whose injected GERs matter for the claim
type GERSource struct {
	services *bridgeServiceClients
}

// NewGERSource returns a GERSource resolving per-network bridge service clients through the
// given finder
func NewGERSource(finder NetworkURLResolver) *GERSource {
	return &GERSource{services: newBridgeServiceClients(finder)}
}

// OriginGER implements bridgetracker.GERSource. Only called for L1-originated bridges: the
// bridge is covered by a GER update on L1 once some L1 info tree leaf includes it
// (`l1-info-tree-index` resolves for its origin network + deposit count). Returns nil while
// not covered.
//
// Once covered, the leaf itself is fetched with a direct index lookup (`network_id=0`, per
// REFERENCE_API.md) to populate the resulting GER and the block it was updated in
func (s *GERSource) OriginGER(
	ctx context.Context, bridge *bridgetracker.BridgeInfo,
) (*trackertypes.GERData, error) {
	svc, err := s.services.clientFor(bridge.DestinationNetwork)
	if err != nil {
		return nil, err // transient: URL resolution failure, retried by the engine
	}

	leafIndex, err := s.coveringLeafIndex(ctx, bridge)
	if errors.Is(err, errNotCoveredYet) {
		return nil, nil // not covered by any GER update yet
	}
	if err != nil {
		return nil, err
	}

	leaf, err := svc.GetInjectedL1InfoLeaf(ctx, 0, int(leafIndex))
	if err != nil {
		return nil, fmt.Errorf("fetching L1 info tree leaf %d: %w", leafIndex, err)
	}

	ger := common.HexToHash(string(leaf.GlobalExitRoot))
	blockNumber := leaf.BlockNumber
	return &trackertypes.GERData{
		NetworkID:   bridge.Key.NetworkID,
		GER:         &ger,
		LERType:     trackertypes.LERTypeMainnet,
		BlockNumber: &blockNumber,
	}, nil
}

// InjectedGER implements bridgetracker.GERSource: a covering GER is injected on the
// destination network once `injected-l1-info-leaf` resolves for the covering leaf index.
// Returns nil while the covering leaf (or a later one) has not been injected
func (s *GERSource) InjectedGER(
	ctx context.Context, bridge *bridgetracker.BridgeInfo,
) (*trackertypes.GERData, error) {
	leafIndex, err := s.coveringLeafIndex(ctx, bridge)
	if errors.Is(err, errNotCoveredYet) {
		return nil, nil // not even covered on the origin yet
	}
	if err != nil {
		return nil, err
	}

	svc, err := s.services.clientFor(bridge.DestinationNetwork)
	if err != nil {
		return nil, err
	}
	leaf, err := svc.GetInjectedL1InfoLeaf(ctx, int(bridge.DestinationNetwork), int(leafIndex))
	if isNotFound(err) {
		return nil, nil // covering leaf not injected on the destination yet
	}
	if err != nil {
		return nil, fmt.Errorf("fetching injected L1 info leaf %d on network %d: %w",
			leafIndex, bridge.DestinationNetwork, err)
	}

	// bridgeservice types.Hash is a hex string
	ger := common.HexToHash(string(leaf.GlobalExitRoot))
	mer := common.HexToHash(string(leaf.MainnetExitRoot))
	rer := common.HexToHash(string(leaf.RollupExitRoot))
	return &trackertypes.GERData{
		NetworkID: bridge.DestinationNetwork,
		GER:       &ger,
		MER:       &mer,
		RER:       &rer,
		LERType:   trackertypes.LERTypeNA,
	}, nil
}

// coveringLeafIndex resolves the L1 info tree index whose leaf covers the bridge, asking
// the destination network's bridge service (which syncs the L1 info tree)
func (s *GERSource) coveringLeafIndex(
	ctx context.Context, bridge *bridgetracker.BridgeInfo,
) (uint32, error) {
	svc, err := s.services.clientFor(bridge.DestinationNetwork)
	if err != nil {
		return 0, err // transient: URL resolution failure, retried by the engine
	}
	index, err := svc.GetL1InfoTreeIndex(ctx, int(bridge.Key.NetworkID), int(bridge.DepositCount))
	if isNotFound(err) {
		return 0, errNotCoveredYet
	}
	if err != nil {
		return 0, fmt.Errorf("fetching L1 info tree index for network %d deposit %d: %w",
			bridge.Key.NetworkID, bridge.DepositCount, err)
	}
	return index, nil
}
