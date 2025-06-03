package types

import (
	"math/big"

	tree "github.com/agglayer/aggkit/tree/types"
)

// Hash represents an Ethereum hash
// @Description A 32-byte Ethereum hash
// @example "0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"
type Hash string

// Address represents an Ethereum address
// @Description A 20-byte Ethereum address
// @example "0xabcdef1234567890abcdef1234567890abcdef12"
type Address string

// BigIntString is a wrapper type for big.Int for Swagger compatibility
// @Description Big integer represented as a decimal string
type BigIntString string

// ToBigInt converts the BigIntString to a big.Int
func (b BigIntString) ToBigInt() *big.Int {
	result := new(big.Int)
	result.SetString(string(b), 0)
	return result
}

// ErrorResponse defines a generic error structure.
// @Description Generic error response structure
type ErrorResponse struct {
	Error string `json:"error" example:"Error message"`
}

// TokenMappingType defines the type of token mapping
// @Description Enum for token mapping types
// @Enum TokenMappingType
type TokenMappingType uint8

const (
	WrappedToken = iota
	SovereignToken
)

func (l TokenMappingType) String() string {
	return [...]string{"WrappedToken", "SovereignToken"}[l]
}

// Proof represents a Merkle proof for a tree of a given height
// @Description Merkle proof structure for a tree of a given height
type Proof [tree.DefaultHeight]Hash

// ConvertToProofResponse converts a Merkle proof to a ProofResponse
// @Description Converts a Merkle proof to a ProofResponse
func ConvertToProofResponse(proof tree.Proof) Proof {
	var p Proof
	for i, h := range proof {
		if i >= len(p) {
			break
		}
		p[i] = Hash(h.Hex())
	}
	return p
}

// ClaimProof represents the Merkle proofs (local and rollup exit roots) and the L1 info tree leaf
// required to verify a claim in the bridge.
//
// @Description Claim proof structure for verifying claims in the bridge
type ClaimProof struct {
	// Merkle proof for the local exit root
	ProofLocalExitRoot Proof `json:"proof_local_exit_root" example:"[0x1, 0x2, 0x3...]"`

	// Merkle proof for the rollup exit root
	ProofRollupExitRoot Proof `json:"proof_rollup_exit_root" example:"[0x4, 0x5, 0x6...]"`

	// L1 info tree leaf data associated with the claim
	L1InfoTreeLeaf L1InfoTreeLeafResponse `json:"l1_info_tree_leaf"`
}

// BridgesResult contains the bridges and the total count of bridges
// @Description Paginated response of bridge events
type BridgesResult struct {
	// List of bridge events
	Bridges []*BridgeResponse `json:"bridges"`

	// Total number of bridge events
	Count int `json:"count" example:"42"`
}

// BridgeResponse represents a bridge event response
// @Description Detailed information about a bridge event
type BridgeResponse struct {
	// Block number where the bridge event was recorded
	BlockNum uint64 `json:"block_num" example:"1234"`

	// Position of the bridge event within the block
	BlockPos uint64 `json:"block_pos" example:"1"`

	// Address that initiated the bridge transaction
	FromAddress Address `json:"from_address" example:"0xabc1234567890abcdef1234567890abcdef1234"`

	// Hash of the transaction that included the bridge event
	TxHash Hash `json:"tx_hash" example:"0xdef4567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"`

	// Raw calldata submitted in the transaction
	Calldata string `json:"calldata" example:"deadbeef"`

	// Timestamp of the block containing the bridge event
	BlockTimestamp uint64 `json:"block_timestamp" example:"1684500000"`

	// Type of leaf (bridge event type) used in the tree structure
	LeafType uint8 `json:"leaf_type" example:"1"`

	// ID of the network where the bridge transaction originated
	OriginNetwork uint32 `json:"origin_network" example:"10"`

	// Address of the token sender on the origin network
	OriginAddress Address `json:"origin_address" example:"0xabc1234567890abcdef1234567890abcdef1234"`

	// ID of the network where the bridge transaction is destined
	DestinationNetwork uint32 `json:"destination_network" example:"42161"`

	// Address of the token receiver on the destination network
	DestinationAddress Address `json:"destination_address" example:"0xdef4567890abcdef1234567890abcdef12345678"`

	// Amount of tokens being bridged
	Amount BigIntString `json:"amount" example:"1000000000000000000"`

	// Optional metadata attached to the bridge event
	Metadata string `json:"metadata" example:"0xdeadbeef"`

	// Count of total deposits processed so far for the given token/address
	DepositCount uint32 `json:"deposit_count" example:"10"`

	// Indicates whether the bridged token is a native token (true) or wrapped (false)
	IsNativeToken bool `json:"is_native_token" example:"true"`

	// Unique hash representing the bridge event, often used as an identifier
	BridgeHash Hash `json:"bridge_hash" example:"0xabc1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcd"`
}

// ClaimsResult contains the list of claim records and the total count
// @Description Paginated response containing claim events and total count
type ClaimsResult struct {
	// List of claims matching the query
	Claims []*ClaimResponse `json:"claims"`

	// Total number of matching claims
	Count int `json:"count" example:"42"`
}

// ClaimResponse represents a claim event response
// @Description Detailed information about a claim event
type ClaimResponse struct {
	// Block number where the claim was processed
	BlockNum uint64 `json:"block_num" example:"1234"`

	// Timestamp of the block containing the claim
	BlockTimestamp uint64 `json:"block_timestamp" example:"1684500000"`

	// Transaction hash associated with the claim
	TxHash Hash `json:"tx_hash" example:"0xdef4567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"`

	// Global index of the claim
	GlobalIndex BigIntString `json:"global_index" example:"1000000000000000000"`

	// Address initiating the claim on the origin network
	OriginAddress Address `json:"origin_address" example:"0xabc1234567890abcdef1234567890abcdef1234"`

	// Origin network ID where the claim was initiated
	OriginNetwork uint32 `json:"origin_network" example:"10"`

	// Address receiving the claim on the destination network
	DestinationAddress Address `json:"destination_address" example:"0xdef4567890abcdef1234567890abcdef12345678"`

	// Destination network ID where the claim was processed
	DestinationNetwork uint32 `json:"destination_network" example:"42161"`

	// Amount claimed
	Amount BigIntString `json:"amount" example:"1000000000000000000"`

	// Address from which the claim originated
	FromAddress Address `json:"from_address" example:"0xabc1234567890abcdef1234567890abcdef1234"`
}

// TokenMappingsResult contains the token mappings and the total count of token mappings
// @Description Paginated response of token mapping records
type TokenMappingsResult struct {
	// List of token mapping entries
	TokenMappings []*TokenMappingResponse `json:"token_mappings"`

	// Total number of token mapping records
	Count int `json:"count" example:"27"`
}

// TokenMappingResponse represents a token mapping event
// @Description Detailed information about a token mapping between origin and wrapped networks
type TokenMappingResponse struct {
	// Block number where the token mapping was recorded
	BlockNum uint64 `json:"block_num" example:"123456"`

	// Position of the mapping event within the block
	BlockPos uint64 `json:"block_pos" example:"2"`

	// Timestamp of the block containing the mapping event
	BlockTimestamp uint64 `json:"block_timestamp" example:"1684501234"`

	// Transaction hash associated with the mapping event
	TxHash Hash `json:"tx_hash" example:"0xabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd"`

	// ID of the origin network where the original token resides
	OriginNetwork uint32 `json:"origin_network" example:"1"`

	// Address of the token on the origin network
	OriginTokenAddress Address `json:"origin_token_address" example:"0x1234567890abcdef1234567890abcdef12345678"`

	// Address of the wrapped token on the destination network
	WrappedTokenAddress Address `json:"wrapped_token_address" example:"0xabcdef1234567890abcdef1234567890abcdef12"`

	// Optional metadata associated with the token mapping
	Metadata string `json:"metadata" example:"0xdeadbeef"`

	// Indicates whether the wrapped token is not mintable (true = not mintable)
	IsNotMintable bool `json:"is_not_mintable" example:"false"`

	// Raw calldata submitted during the mapping
	Calldata string `json:"calldata" example:"0xfeedface"`

	// Type of the token mapping: 0 = WrappedToken, 1 = SovereignToken
	Type TokenMappingType `json:"token_type" example:"0"`
}

// LegacyTokenMigrationsResult contains the legacy token migrations and the total count of such migrations
// @Description Paginated response of legacy token migrations
type LegacyTokenMigrationsResult struct {
	// List of legacy token migration events
	TokenMigrations []*LegacyTokenMigrationResponse `json:"legacy_token_migrations"`

	// Total number of legacy token migration events
	Count int `json:"count" example:"12"`
}

// LegacyTokenMigrationResponse represents a MigrateLegacyToken event emitted by the sovereign chain bridge contract
// @Description Details of a legacy token migration event
type LegacyTokenMigrationResponse struct {
	// Block number where the migration occurred
	BlockNum uint64 `json:"block_num" example:"1234"`

	// Position of the transaction in the block
	BlockPos uint64 `json:"block_pos" example:"1"`

	// Timestamp of the block
	BlockTimestamp uint64 `json:"block_timestamp" example:"1684500000"`

	// Transaction hash of the migration event
	TxHash Hash `json:"tx_hash" example:"0xabc123..."`

	// Address of the sender initiating the migration
	Sender Address `json:"sender" example:"0xabc123..."`

	// Legacy token address being migrated
	LegacyTokenAddress Address `json:"legacy_token_address" example:"0xdef456..."`

	// New updated token address after migration
	UpdatedTokenAddress Address `json:"updated_token_address" example:"0xfeed789..."`

	// Amount of tokens migrated
	Amount BigIntString `json:"amount" example:"1000000000000000000"`

	// Raw calldata included in the migration transaction
	Calldata string `json:"calldata" example:"0xdeadbeef"`
}

// L1InfoTreeLeafResponse represents a leaf node in the L1 info tree used for bridge state verification.
//
// This includes references to the block and exit roots relevant to L1 and rollup state.
type L1InfoTreeLeafResponse struct {
	// Block number where the leaf was recorded
	BlockNumber uint64 `json:"block_num" example:"123456"`

	// Position of the leaf in the block (used for ordering)
	BlockPosition uint64 `json:"block_pos" example:"5"`

	// Index of this leaf in the L1 info tree
	L1InfoTreeIndex uint32 `json:"l1_info_tree_index" example:"42"`

	// Hash of the previous block in the tree
	PreviousBlockHash Hash `json:"previous_block_hash" example:"0xabc1...bcd"`

	// Timestamp of the block in seconds since the Unix epoch
	Timestamp uint64 `json:"timestamp" example:"1684500000"`

	// Mainnet exit root at this leaf
	MainnetExitRoot Hash `json:"mainnet_exit_root" example:"0xdefc...789"`

	// Rollup exit root at this leaf
	RollupExitRoot Hash `json:"rollup_exit_root" example:"0x7890...123"`

	// Global exit root computed from mainnet and rollup roots
	// @example "0x4567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef123"
	GlobalExitRoot Hash `json:"global_exit_root"`

	// Unique hash identifying this leaf node
	Hash Hash `json:"hash" example:"0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"`
}

// Claim represents a bridge claim submitted for processing.
// It includes proofs, root hashes, origin/destination networks, token info, and metadata.
// @Description Claim sponsoring structure
type ClaimRequest struct {
	// Type of leaf node (e.g., asset or message)
	LeafType uint8 `json:"leaf_type" example:"1"`

	// Merkle proof for local exit root
	ProofLocalExitRoot Proof `json:"proof_local_exit_root"`

	// Merkle proof for rollup exit root
	ProofRollupExitRoot Proof `json:"proof_rollup_exit_root"`

	// Global leaf index
	GlobalIndex BigIntString `json:"global_index" example:"123456789012345678901234567890"`

	// Mainnet exit root
	MainnetExitRoot Hash `json:"mainnet_exit_root" example:"0xabc123..."`

	// Rollup exit root
	RollupExitRoot Hash `json:"rollup_exit_root" example:"0xdef456..."`

	// Origin network ID where the claim originated
	OriginNetwork uint32 `json:"origin_network" example:"1"`

	// Origin token address
	OriginTokenAddress Address `json:"origin_token_address" example:"0x123..."`

	// ID of the network where the claim is being sent
	DestinationNetwork uint32 `json:"destination_network" example:"100"`

	// Recipient address on destination network
	DestinationAddress Address `json:"destination_address" example:"0x456..."`

	// Amount in wei (or token's smallest unit)
	Amount BigIntString `json:"amount" example:"1000000000000000000"`

	// Optional metadata for the claim
	Metadata []byte `json:"metadata"`
}

// SyncStatus represents the synchronization status of the bridge service for both L1 and L2 networks
// @Description Contains synchronization information for both L1 and L2 networks
// including deposit counts and sync status
// @example {"l1_info":{"contract_deposit_count":100,"bridge_deposit_count":100,"is_synced":true},
// "l2_info":{"contract_deposit_count":200,"bridge_deposit_count":200,"is_synced":true}}
type SyncStatus struct {
	L1Info *NetworkSyncInfo `json:"l1_info"`
	L2Info *NetworkSyncInfo `json:"l2_info"`
}

// NetworkSyncInfo represents the synchronization status of a single network (L1 or L2)
// @Description Contains network-specific synchronization information
// including contract and bridge deposit counts and sync status
// @example {"contract_deposit_count":100,"bridge_deposit_count":100,"is_synced":true}
type NetworkSyncInfo struct {
	ContractDepositCount uint32 `json:"contract_deposit_count"`
	BridgeDepositCount   uint32 `json:"bridge_deposit_count"`
	IsSynced             bool   `json:"is_synced"`
}
