package types

import (
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/l1infotreesync"
	tree "github.com/agglayer/aggkit/tree/types"
)

type ClaimProof struct {
	ProofLocalExitRoot  tree.Proof                    `json:"proof_local_exit_root"`
	ProofRollupExitRoot tree.Proof                    `json:"proof_rollup_exit_root"`
	L1InfoTreeLeaf      l1infotreesync.L1InfoTreeLeaf `json:"l1_info_tree_leaf"`
}

// TokenMappingsResult contains the token mappings and the total count of token mappings
type TokenMappingsResult struct {
	TokenMappings []*bridgesync.TokenMapping `json:"tokenMappings"`
	Count         int                        `json:"count"`
}

// LegacyTokenMigrationsResult contains the legacy token migrations and the total count of such migrations
type LegacyTokenMigrationsResult struct {
	TokenMigrations []*bridgesync.LegacyTokenMigration `json:"legacyTokenMigrations"`
	Count           int                                `json:"count"`
}

// BridgesResult contains the bridges and the total count of bridges
type BridgesResult struct {
	Bridges []*bridgesync.BridgeResponse `json:"bridges"`
	Count   int                          `json:"count"`
}

// ClaimsResult contains the claims and the total count of claims
type ClaimsResult struct {
	Claims []*bridgesync.ClaimResponse `json:"claims"`
	Count  int                         `json:"count"`
}
