package claimsync

import (
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
)

// ClaimType is an alias for claimsynctypes.ClaimType
type ClaimType = claimsynctypes.ClaimType

const (
	ClaimEvent         ClaimType = claimsynctypes.ClaimEvent
	DetailedClaimEvent ClaimType = claimsynctypes.DetailedClaimEvent
)

// Claim is an alias for claimsynctypes.Claim
type Claim = claimsynctypes.Claim

// UnsetClaim is an alias for claimsynctypes.UnsetClaim
type UnsetClaim = claimsynctypes.UnsetClaim

// SetClaim is an alias for claimsynctypes.SetClaim
type SetClaim = claimsynctypes.SetClaim
