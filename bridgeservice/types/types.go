package types

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/agglayer/aggkit/l1infotreesync"
	tree "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

// Hash represents an Ethereum hash
// @Description A 32-byte Ethereum hash
// @example "0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"
type Hash common.Hash

// Address represents an Ethereum address
// @Description A 20-byte Ethereum address
// @example "0xabcdef1234567890abcdef1234567890abcdef12"
type Address common.Address

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

// ClaimProof represents the Merkle proofs (local and rollup exit roots) and the L1 info tree leaf
// required to verify a claim in the bridge.
type ClaimProof struct {
	ProofLocalExitRoot  tree.Proof                    `json:"proof_local_exit_root"`
	ProofRollupExitRoot tree.Proof                    `json:"proof_rollup_exit_root"`
	L1InfoTreeLeaf      l1infotreesync.L1InfoTreeLeaf `json:"l1_info_tree_leaf"`
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
	Calldata []byte `json:"calldata" example:"0xdeadbeef"`

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
	Amount *big.Int `json:"amount" example:"1000000000000000000"`

	// Optional metadata attached to the bridge event
	Metadata []byte `json:"metadata" example:"0x1234abcd"`

	// Count of total deposits processed so far for the given token/address
	DepositCount uint32 `json:"deposit_count" example:"10"`

	// Indicates whether the bridged token is a native token (true) or wrapped (false)
	IsNativeToken bool `json:"is_native_token" example:"true"`

	// Unique hash representing the bridge event, often used as an identifier
	BridgeHash Hash `json:"bridge_hash" example:"0xabc1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcd"`
}

// MarshalJSON for hex-encoded fields
func (b *BridgeResponse) MarshalJSON() ([]byte, error) {
	type Alias BridgeResponse // Prevent recursion
	return json.Marshal(&struct {
		Metadata string `json:"metadata"`
		CallData string `json:"calldata"`
		*Alias
	}{
		Metadata: fmt.Sprintf("0x%s", hex.EncodeToString(b.Metadata)),
		CallData: fmt.Sprintf("0x%s", hex.EncodeToString(b.Calldata)),
		Alias:    (*Alias)(b),
	})
}

// UnmarshalJSON for hex-decoding fields
func (b *BridgeResponse) UnmarshalJSON(data []byte) error {
	type Alias BridgeResponse
	tmp := &struct {
		Metadata string `json:"metadata"`
		CallData string `json:"calldata"`
		*Alias
	}{
		Alias: (*Alias)(b),
	}

	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}

	if tmp.Metadata != "" {
		decodedMetadata, err := hex.DecodeString(strings.TrimPrefix(tmp.Metadata, "0x"))
		if err != nil {
			return fmt.Errorf("failed to decode metadata: %w", err)
		}
		b.Metadata = decodedMetadata
	}

	if tmp.CallData != "" {
		decodedCalldata, err := hex.DecodeString(strings.TrimPrefix(tmp.CallData, "0x"))
		if err != nil {
			return fmt.Errorf("failed to decode calldata: %w", err)
		}
		b.Calldata = decodedCalldata
	}

	return nil
}

// ClaimsResult contains the list of claim records and the total count
// @Description Paginated response containing claim events and total count
type ClaimsResult struct {
	// List of claims matching the query
	Claims []*ClaimResponse `json:"claims" example:"[{...}]"`

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
	GlobalIndex *big.Int `json:"global_index" example:"1000000000000000000"`

	// Address initiating the claim on the origin network
	OriginAddress Address `json:"origin_address" example:"0xabc1234567890abcdef1234567890abcdef1234"`

	// Origin network ID where the claim was initiated
	OriginNetwork uint32 `json:"origin_network" example:"10"`

	// Address receiving the claim on the destination network
	DestinationAddress Address `json:"destination_address" example:"0xdef4567890abcdef1234567890abcdef12345678"`

	// Destination network ID where the claim was processed
	DestinationNetwork uint32 `json:"destination_network" example:"42161"`

	// Amount claimed
	Amount *big.Int `json:"amount" example:"1000000000000000000"`

	// Address from which the claim originated
	FromAddress Address `json:"from_address" example:"0xabc1234567890abcdef1234567890abcdef1234"`
}

// TokenMappingsResult contains the token mappings and the total count of token mappings
// @Description Paginated response of token mapping records
type TokenMappingsResult struct {
	// List of token mapping entries
	TokenMappings []*TokenMappingResponse `json:"token_mappings" example:"[{...}]"`

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
	Metadata []byte `json:"metadata" example:"0xdeadbeef"`

	// Indicates whether the wrapped token is not mintable (true = not mintable)
	IsNotMintable bool `json:"is_not_mintable" example:"false"`

	// Raw calldata submitted during the mapping
	Calldata []byte `json:"calldata" example:"0xfeedface"`

	// Type of the token mapping: 0 = WrappedToken, 1 = SovereignToken
	Type TokenMappingType `json:"token_type" example:"WrappedToken"`
}

// MarshalJSON for hex-encoding Metadata and Calldata fields
func (t *TokenMappingResponse) MarshalJSON() ([]byte, error) {
	type Alias TokenMappingResponse // Prevent recursion
	return json.Marshal(&struct {
		Metadata string `json:"metadata"`
		Calldata string `json:"calldata"`
		*Alias
	}{
		Metadata: fmt.Sprintf("0x%s", hex.EncodeToString(t.Metadata)),
		Calldata: fmt.Sprintf("0x%s", hex.EncodeToString(t.Calldata)),
		Alias:    (*Alias)(t),
	})
}

// UnmarshalJSON for hex-decoding fields
func (t *TokenMappingResponse) UnmarshalJSON(data []byte) error {
	type Alias TokenMappingResponse
	tmp := &struct {
		Metadata string `json:"metadata"`
		CallData string `json:"calldata"`
		*Alias
	}{
		Alias: (*Alias)(t),
	}

	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}

	if tmp.Metadata != "" {
		decodedMetadata, err := hex.DecodeString(strings.TrimPrefix(tmp.Metadata, "0x"))
		if err != nil {
			return fmt.Errorf("failed to decode metadata: %w", err)
		}
		t.Metadata = decodedMetadata
	}

	if tmp.CallData != "" {
		decodedCalldata, err := hex.DecodeString(strings.TrimPrefix(tmp.CallData, "0x"))
		if err != nil {
			return fmt.Errorf("failed to decode calldata: %w", err)
		}
		t.Calldata = decodedCalldata
	}

	return nil
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
	Amount *big.Int `json:"amount" example:"1000000000000000000"`

	// Raw calldata included in the migration transaction
	Calldata []byte `json:"calldata" example:"0x..."`
}
