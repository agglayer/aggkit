package sources

import (
	"context"
	"fmt"
	"math/big"

	"github.com/agglayer/aggkit/bridgeservice/client"
	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
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
	return &ClaimSource{services: newBridgeServiceClients(finder, 0)}
}

// ClaimFor implements bridgetracker.ClaimSource: the bridge is claimed once the
// destination network's bridge service records a claim with the bridge's global index
// (derived from its origin network + deposit count). The claim is selected by that global index
// among the claims listed; a response that lists claims but not that one, or that reports a claim
// it does not list, is an error (see selectClaim), never the claim of another bridge
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
	claim, err := selectClaim(claims, claimQuery{
		globalIndex:        globalIndex,
		networkID:          bridge.NetworkID,
		destinationNetwork: bridge.DestinationNetwork,
		depositCount:       bridge.DepositCount,
	})
	if err != nil {
		return nil, fmt.Errorf("claims of global index %s on network %d: %w",
			globalIndex, bridge.DestinationNetwork, err)
	}
	if claim == nil {
		return nil, nil
	}
	return &trackertypes.ClaimResult{
		ClaimTx:           common.HexToHash(string(claim.TxHash)),
		BlockNumber:       claim.BlockNum,
		BlockTimestamp:    claim.BlockTimestamp,
		ReportedFinalized: claim.BlockFinalized,
	}, nil
}

// claimQuery identifies the claim of one bridge among the claims a destination network's bridge
// service listed
type claimQuery struct {
	// globalIndex is the global index the claims were requested by
	globalIndex *big.Int
	// networkID is the network the bridge-creating tx was sent to: never the bridge's
	// OriginNetwork, which is the origin of the bridged token and can be another network
	networkID uint32
	// destinationNetwork is the network the claims were requested from
	destinationNetwork uint32
	// depositCount is the bridge's index in the exit tree of networkID
	depositCount uint32
}

// selectClaim picks, from a claims listing, the claim q identifies: the first entry carrying the
// requested global index on the requested destination network. The listing is read as it is, the
// count it reports is never used to index it. It returns:
//   - (nil, nil) when the listing is empty and reports no claim: the claim is not indexed yet;
//   - an error wrapping client.ErrInvalidResponse when the listing is empty but reports a claim,
//     or when it holds claims and none of them is the requested one. Both are transient: the
//     step retries, and the response is never taken for the claim of another bridge
func selectClaim(
	res *bridgeservicetypes.ClaimsResult, q claimQuery,
) (*bridgeservicetypes.ClaimResponse, error) {
	if len(res.Claims) == 0 {
		if res.Count > 0 {
			return nil, fmt.Errorf("%w: it reports %d claims but lists none", client.ErrInvalidResponse, res.Count)
		}
		return nil, nil
	}
	for _, claim := range res.Claims {
		if claim != nil && q.matches(claim) {
			return claim, nil
		}
	}
	return nil, fmt.Errorf("%w: none of the %d claims listed has global index %s on network %d",
		client.ErrInvalidResponse, len(res.Claims), q.globalIndex, q.destinationNetwork)
}

// matches reports whether claim is the claim q identifies: it was made on the destination network
// and carries the requested global index, or, for a legacy zkEVM bridge, its bare deposit count
func (q claimQuery) matches(claim *bridgeservicetypes.ClaimResponse) bool {
	if claim.DestinationNetwork != q.destinationNetwork {
		return false
	}
	got, ok := parseGlobalIndex(claim.GlobalIndex)
	if !ok {
		return false
	}
	if got.Cmp(q.globalIndex) == 0 {
		return true
	}
	return q.isLegacyZkEVMBridge() && got.Cmp(new(big.Int).SetUint64(uint64(q.depositCount))) == 0
}

// isLegacyZkEVMBridge reports whether q is a bridge from mainnet to the legacy zkEVM rollup, whose
// pre-Etrog claims are identified by a bare deposit count instead of the encoded global index
// (see bridgesync.GlobalIndexForBridge). No other route uses that form
func (q claimQuery) isLegacyZkEVMBridge() bool {
	return q.networkID == MainnetNetworkID && q.destinationNetwork == bridgesync.LegacyZkEVMRollupNetworkID
}

// parseGlobalIndex reads a global index the way bridgeservicetypes.BigIntString.ToBigInt does
// (decimal, or 0x-prefixed hex), but reports whether the value is a valid number at all: ToBigInt
// reads an invalid one as zero, which would match the bare deposit count 0 of a legacy bridge
func parseGlobalIndex(globalIndex bridgeservicetypes.BigIntString) (*big.Int, bool) {
	return new(big.Int).SetString(string(globalIndex), 0)
}
