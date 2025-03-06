package types

import (
	"github.com/agglayer/aggkit/l1infotreesync"
	tree "github.com/agglayer/aggkit/tree/types"
)

type ClaimProof struct {
	ProofLocalExitRoot  tree.Proof                    `json:"proof_local_exit_root"`
	ProofRollupExitRoot tree.Proof                    `json:"proof_rollup_exit_root"`
	L1InfoTreeLeaf      l1infotreesync.L1InfoTreeLeaf `json:"l1_info_tree_leaf"`
}
