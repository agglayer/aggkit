package optimistic

import (
	"math/big"

	"github.com/agglayer/aggkit/agglayer/types"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/bridgesync"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// This file calculate the hash for ImportedBridgeExitCommitmentValues
// https://github.com/agglayer/interop/blob/4f3f5af54a0f962a67a2ba603bc5f84132592730/crates/unified-bridge/src/imported_bridge_exit.rs#L419-L429
// The public function is:
// CalculateCommitImportedBrdigeExitsHashFromClaims(...)

// CalculateCommitImportedBrdigeExitsHashFromClaims calculate hash from certBuildParams ([]bridgesync.Claim)
func CalculateCommitImportedBrdigeExitsHashFromClaims(claims []bridgesync.Claim) common.Hash {
	data := newCommitImportedBrigesData(claims)
	return data.hash()
}

type optimisticCommitImportedBrigesData struct {
	bridges []optimisticCommitImportedBrigeData
}

type optimisticCommitImportedBrigeData struct {
	globalIndex    *big.Int
	bridgeExitHash common.Hash
}

func newCommitImportedBrigesData(claims []bridgesync.Claim) *optimisticCommitImportedBrigesData {
	res := optimisticCommitImportedBrigesData{}
	res.bridges = make([]optimisticCommitImportedBrigeData, len(claims))
	for i, claim := range claims {
		res.bridges[i] = optimisticCommitImportedBrigeData{}
		res.bridges[i].globalIndex = claim.GlobalIndex
		res.bridges[i].setBridgeExitHash(&claim)
	}
	return &res
}
func (o *optimisticCommitImportedBrigesData) hash() common.Hash {
	var combined []byte
	for _, bridge := range o.bridges {
		combined = append(combined, aggkitcommon.BigIntToLittleEndianBytes(bridge.globalIndex)...)
		combined = append(combined, bridge.bridgeExitHash.Bytes()...)
	}
	return crypto.Keccak256Hash(combined)
}

func (o *optimisticCommitImportedBrigeData) setBridgeExitHash(claim *bridgesync.Claim) {
	leafType := agglayertypes.LeafTypeAsset
	if claim.IsMessage {
		leafType = agglayertypes.LeafTypeMessage
	}

	be := types.BridgeExit{
		LeafType: leafType,
		TokenInfo: &agglayertypes.TokenInfo{
			OriginNetwork:      claim.OriginNetwork,
			OriginTokenAddress: claim.OriginAddress,
		},
		DestinationNetwork: claim.DestinationNetwork,
		DestinationAddress: claim.DestinationAddress,
		Amount:             claim.Amount,
		Metadata:           claim.Metadata,
	}
	o.bridgeExitHash = be.Hash()
}
