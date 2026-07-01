package claimer

import (
	"encoding/hex"
	"math/big"

	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

// leaf_type values as serialized in the signed exit certificate.
const (
	leafTypeTransferStr = "Transfer"
	leafTypeMessageStr  = "Message"

	leafTypeAsset   uint8 = 0
	leafTypeMessage uint8 = 1
)

// BridgeExitView is the public, JSON-friendly representation of a single bridge exit
// destined for a given address. It mirrors the certificate entry and is enriched with
// the deposit count (the exit-tree leaf index) resolved from the local exit tree DB.
type BridgeExitView struct {
	LeafType           uint8  `json:"leaf_type"`
	OriginNetwork      uint32 `json:"origin_network"`
	OriginTokenAddress string `json:"origin_token_address"`
	DestinationNetwork uint32 `json:"destination_network"`
	DestinationAddress string `json:"destination_address"`
	Amount             string `json:"amount"`
	Metadata           string `json:"metadata"`
	DepositCount       uint32 `json:"deposit_count"`
}

// ClaimAssetParams holds every argument required to call AgglayerBridge.claimAsset for a
// single bridge exit, serialized in a JSON/web-friendly form (hex strings, decimal amounts).
type ClaimAssetParams struct {
	SmtProofLocalExitRoot  [treetypes.DefaultHeight]string `json:"smt_proof_local_exit_root"`
	SmtProofRollupExitRoot [treetypes.DefaultHeight]string `json:"smt_proof_rollup_exit_root"`
	GlobalIndex            string                          `json:"global_index"`
	MainnetExitRoot        string                          `json:"mainnet_exit_root"`
	RollupExitRoot         string                          `json:"rollup_exit_root"`
	OriginNetwork          uint32                          `json:"origin_network"`
	OriginTokenAddress     string                          `json:"origin_token_address"`
	DestinationNetwork     uint32                          `json:"destination_network"`
	DestinationAddress     string                          `json:"destination_address"`
	Amount                 string                          `json:"amount"`
	Metadata               string                          `json:"metadata"`

	// Context fields (not claimAsset arguments) useful for callers and debugging.
	LeafType        uint8  `json:"leaf_type"`
	DepositCount    uint32 `json:"deposit_count"`
	L1InfoTreeIndex uint32 `json:"l1_info_tree_index"`
}

// BridgesResponse is the body returned by GET /bridges.
type BridgesResponse struct {
	NetworkID          uint32           `json:"network_id"`
	DestinationAddress string           `json:"destination_address"`
	Bridges            []BridgeExitView `json:"bridges"`
}

// ClaimParamsResponse is the body returned by GET /claim-params.
type ClaimParamsResponse struct {
	NetworkID          uint32             `json:"network_id"`
	DestinationAddress string             `json:"destination_address"`
	Claims             []ClaimAssetParams `json:"claims"`
}

// VersionInfo mirrors aggkit.FullVersion as a JSON-friendly payload.
type VersionInfo struct {
	Version   string `json:"version"`
	GitRev    string `json:"git_rev"`
	GitBranch string `json:"git_branch"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}

// HealthResponse is the body returned by GET /health.
type HealthResponse struct {
	Status    string      `json:"status"`
	NetworkID uint32      `json:"network_id"`
	Version   VersionInfo `json:"version"`
}

// errorResponse is the JSON body returned on error.
type errorResponse struct {
	Error string `json:"error"`
}

// proofToHex converts a tree.Proof (32 sibling hashes) into its hex-string representation.
func proofToHex(p treetypes.Proof) [treetypes.DefaultHeight]string {
	var out [treetypes.DefaultHeight]string
	for i := range p {
		out[i] = p[i].Hex()
	}
	return out
}

// bigToString renders a *big.Int as a decimal string, treating nil as "0".
func bigToString(v *big.Int) string {
	if v == nil {
		return "0"
	}
	return v.String()
}

// addrHex renders an address as a checksummed 0x string.
func addrHex(a common.Address) string {
	return a.Hex()
}

// metadataHex renders a metadata byte blob as a 0x-prefixed hex string ("0x" for empty).
func metadataHex(b []byte) string {
	return "0x" + hex.EncodeToString(b)
}
