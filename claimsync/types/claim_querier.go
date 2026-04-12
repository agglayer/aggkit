package types

import (
	"context"

	dbtypes "github.com/agglayer/aggkit/db/types"
)

// ClaimQuerier is used by event handlers to check the DetailedClaimEvent boundary.
type ClaimQuerier interface {
	GetBoundaryBlockForClaimType(ctx context.Context, tx dbtypes.Querier, claimType ClaimType) (uint64, error)
}
