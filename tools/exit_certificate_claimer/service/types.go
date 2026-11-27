package claimer

import (
	"encoding/hex"
	"math/big"

	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
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

// Claim methods of AgglayerBridge, as returned by API v2 (claim_method).
const (
	ClaimMethodAsset   = "claimAsset"
	ClaimMethodMessage = "claimMessage"
)

// claimMethod returns the AgglayerBridge function that claims a leaf of the given type.
func claimMethod(leafType uint8) string {
	if leafType == leafTypeMessage {
		return ClaimMethodMessage
	}
	return ClaimMethodAsset
}

// BridgeExitViewV2 is a bridge exit as returned by API v2: the exit fields plus the metadata hash, the
// claim method and, for a Message exit decoded through a configured message route, its beneficiary,
// the amount it is paid and the token it is paid in.
//
// Metadata is the raw bridge metadata (for certificate exits and pending bridges alike) and MetadataHash
// is its keccak256 hash.
type BridgeExitViewV2 struct {
	BridgeExitView
	MetadataHash  string        `json:"metadata_hash"`
	ClaimMethod   string        `json:"claim_method"`
	Beneficiary   string        `json:"beneficiary,omitempty"`
	DecodedAmount string        `json:"decoded_amount,omitempty"`
	DisplayToken  *DisplayToken `json:"display_token,omitempty"`
}

// ClaimParamsV2 holds the claim arguments of one exit as returned by API v2: the claim arguments (claimAsset
// and claimMessage take the same 11 arguments, in the same order) plus the keccak256 hash of the
// metadata and the method to call.
type ClaimParamsV2 struct {
	ClaimAssetParams
	MetadataHash string `json:"metadata_hash"`
	ClaimMethod  string `json:"claim_method"`
}

// BridgesResponseV2 is the body returned by GET /claimer/v2/bridges.
type BridgesResponseV2 struct {
	APIVersion int                `json:"api_version"`
	NetworkID  uint32             `json:"network_id"`
	Address    string             `json:"address"`
	Bridges    []BridgeExitViewV2 `json:"bridges"`
}

// ClaimParamsResponseV2 is the body returned by GET /claimer/v2/claim-params.
type ClaimParamsResponseV2 struct {
	APIVersion int             `json:"api_version"`
	NetworkID  uint32          `json:"network_id"`
	Address    string          `json:"address"`
	Claims     []ClaimParamsV2 `json:"claims"`
}

// HealthResponseV2 is the body returned by GET /claimer/v2/health.
type HealthResponseV2 struct {
	APIVersion  int         `json:"api_version"`
	Status      string      `json:"status"`
	NetworkID   uint32      `json:"network_id"`
	Version     VersionInfo `json:"version"`
	APIVersions []int       `json:"api_versions"`
	// Run and Follow: see HealthResponse.
	Run    *RunInfo      `json:"run,omitempty"`
	Follow *FollowStatus `json:"follow,omitempty"`
}

// errorResponseV2 is the JSON body API v2 returns on error.
type errorResponseV2 struct {
	APIVersion int    `json:"api_version"`
	Error      string `json:"error"`
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

// HealthResponse is the body returned by GET /claimer/v1/health, kept so existing probes keep working
// after API v1 was removed.
type HealthResponse struct {
	Status    string      `json:"status"`
	NetworkID uint32      `json:"network_id"`
	Version   VersionInfo `json:"version"`
	// APIVersions lists the API major versions this claimer serves (/claimer/v<N>/): only 2.
	APIVersions []int `json:"api_versions"`
	// Run describes the exit_certificate run being served.
	Run *RunInfo `json:"run,omitempty"`
	// Follow is the state of follow mode; absent when it is disabled.
	Follow *FollowStatus `json:"follow,omitempty"`
}

// RunInfo describes the exit_certificate run a claimer serves, as reported by /health.
type RunInfo struct {
	// Source is the archive prefix of the run (gs://<bucket>/<prefix>); absent when the claimer serves
	// local files.
	Source string `json:"source,omitempty"`
	// Height and CertificateHash are those of the run's certificate.
	Height          uint64 `json:"height"`
	CertificateHash string `json:"certificate_hash"`
	// DataVersion is the data version of the archived run; absent when the claimer serves local files.
	DataVersion int `json:"data_version,omitempty"`
}

// Results of a follow mode check, as reported by /health (FollowStatus.LastCheckResult).
const (
	// FollowResultNoNewRun: the run of the next height is not archived (yet).
	FollowResultNoNewRun = "no_new_run"
	// FollowResultSwitched: the claimer switched to the run of the next height.
	FollowResultSwitched = "switched"
	// FollowResultUnsupportedVersion: the next run has a manifest or data version this claimer does not
	// support; it keeps serving the current one.
	FollowResultUnsupportedVersion = "unsupported_version"
	// FollowResultLookupFailed: the archive could not be read.
	FollowResultLookupFailed = "lookup_failed"
	// FollowResultDownloadFailed: the next run could not be downloaded or did not match its manifest.
	FollowResultDownloadFailed = "download_failed"
	// FollowResultLoadFailed: the files of the next run could not be loaded, or are not consistent.
	FollowResultLoadFailed = "load_failed"
	// FollowResultL1SyncFailed: the settlement of the next run could not be indexed in the L1 Info Tree.
	FollowResultL1SyncFailed = "l1_sync_failed"
)

// FollowStatus is the state of follow mode, as reported by /health.
type FollowStatus struct {
	IntervalSeconds int `json:"interval_seconds"`
	// NextRun is the archive prefix the claimer looks for the next run at, and NextHeight its height.
	NextRun    string `json:"next_run"`
	NextHeight uint64 `json:"next_height"`
	// LastCheckTime (RFC 3339, UTC) and LastCheckResult (one of the FollowResult* values) describe the
	// last finished check; both are absent before the first one.
	LastCheckTime   string `json:"last_check_time,omitempty"`
	LastCheckResult string `json:"last_check_result,omitempty"`
	// BlockedByUnsupportedVersion is true while a newer run exists that this claimer cannot serve because
	// of its manifest or data version: the claimer needs an upgrade.
	BlockedByUnsupportedVersion bool `json:"blocked_by_unsupported_version"`
	// Preparing is true while a newer run is being downloaded, loaded or synced before the switch.
	Preparing bool `json:"preparing"`
	// LastSwitchTime (RFC 3339, UTC) is when the claimer last switched run; absent if it never did.
	LastSwitchTime string `json:"last_switch_time,omitempty"`
}

// apiRemovedResponse is the 410 Gone body of every removed /claimer/v1 endpoint.
type apiRemovedResponse struct {
	Error       string `json:"error"`
	APIVersions []int  `json:"api_versions"`
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

// metadataHashHex renders the keccak256 hash of a raw metadata blob as a 0x-prefixed hex string. It is
// the metadata slot of the exit-tree leaf (and what a certificate carries as the exit's metadata).
func metadataHashHex(rawMetadata []byte) string {
	return crypto.Keccak256Hash(rawMetadata).Hex()
}

// metadataHex renders a metadata byte blob as a 0x-prefixed hex string ("0x" for empty).
func metadataHex(b []byte) string {
	return "0x" + hex.EncodeToString(b)
}
