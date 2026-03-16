package bridgesync

import (
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Type aliases to maintain backward compatibility after types were moved to claimsync/types.

// Claim is an alias for claimsynctypes.Claim.
type Claim = claimsynctypes.Claim

// ClaimType is an alias for claimsynctypes.ClaimType.
type ClaimType = claimsynctypes.ClaimType

// UnsetClaim is an alias for claimsynctypes.UnsetClaim.
type UnsetClaim = claimsynctypes.UnsetClaim

// SetClaim is an alias for claimsynctypes.SetClaim.
type SetClaim = claimsynctypes.SetClaim

const (
	// ClaimEvent is an alias for claimsynctypes.ClaimEvent.
	ClaimEvent ClaimType = claimsynctypes.ClaimEvent
	// DetailedClaimEvent is an alias for claimsynctypes.DetailedClaimEvent.
	DetailedClaimEvent ClaimType = claimsynctypes.DetailedClaimEvent
)

var (
	// claim event signatures (moved to claimsync package, re-exported here for test compatibility)
	claimEventSignature = crypto.Keccak256Hash([]byte("ClaimEvent(uint256,uint32,address,address,uint256)"))
	claimEventSignaturePreEtrog = crypto.Keccak256Hash([]byte("ClaimEvent(uint32,uint32,address,address,uint256)"))
	detailedClaimEventSignature = crypto.Keccak256Hash([]byte(
		"DetailedClaimEvent(bytes32[32],bytes32[32]," +
			"uint256,bytes32,bytes32,uint8,uint32," +
			"address,uint32,address,uint256,bytes)",
	))
	unsetClaimEventSignature = crypto.Keccak256Hash([]byte(
		"UpdatedUnsetGlobalIndexHashChain(bytes32,bytes32)",
	))
	setClaimEventSignature = crypto.Keccak256Hash([]byte(
		"SetClaim(bytes32)",
	))

	// claim method IDs (moved to claimsync package, re-exported here for test compatibility)
	claimAssetEtrogMethodID      = common.Hex2Bytes("ccaa2d11")
	claimMessageEtrogMethodID    = common.Hex2Bytes("f5efcd79")
	claimAssetPreEtrogMethodID   = common.Hex2Bytes("2cffd02e")
	claimMessagePreEtrogMethodID = common.Hex2Bytes("2d2c9d94")
)
