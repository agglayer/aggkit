package types

import (
	"fmt"
	"math/big"

	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	// nilStr holds nil string
	nilStr = "nil"
)

// ClaimType represents the type of a claim event
type ClaimType string

const (
	ClaimEvent         ClaimType = "ClaimEvent"
	DetailedClaimEvent ClaimType = "DetailedClaimEvent"
)

// Claim representation of a claim event
type Claim struct {
	BlockNum            uint64          `meddler:"block_num"`
	BlockPos            uint64          `meddler:"block_pos"`
	TxHash              common.Hash     `meddler:"tx_hash,hash"`
	GlobalIndex         *big.Int        `meddler:"global_index,bigint"`
	OriginNetwork       uint32          `meddler:"origin_network"`
	OriginAddress       common.Address  `meddler:"origin_address"`
	DestinationAddress  common.Address  `meddler:"destination_address"`
	Amount              *big.Int        `meddler:"amount,bigint"`
	ProofLocalExitRoot  treetypes.Proof `meddler:"proof_local_exit_root,merkleproof"`
	ProofRollupExitRoot treetypes.Proof `meddler:"proof_rollup_exit_root,merkleproof"`
	MainnetExitRoot     common.Hash     `meddler:"mainnet_exit_root,hash"`
	RollupExitRoot      common.Hash     `meddler:"rollup_exit_root,hash"`
	GlobalExitRoot      common.Hash     `meddler:"global_exit_root,hash"`
	DestinationNetwork  uint32          `meddler:"destination_network"`
	Metadata            []byte          `meddler:"metadata"`
	IsMessage           bool            `meddler:"is_message"`
	BlockTimestamp      uint64          `meddler:"block_timestamp"`
	Type                ClaimType       `meddler:"type"`
}

// String returns a string representation of the Claim.
func (c *Claim) String() string {
	globalIndexStr := nilStr
	if c.GlobalIndex != nil {
		globalIndexStr = c.GlobalIndex.String()
	}

	amountStr := nilStr
	if c.Amount != nil {
		amountStr = c.Amount.String()
	}

	return fmt.Sprintf("Claim{BlockNum: %d, BlockPos: %d, TxHash: %s, GlobalIndex: %s, "+
		"OriginNetwork: %d, OriginAddress: %s, DestinationAddress: %s, Amount: %s, "+
		"ProofLocalExitRoot: %v, ProofRollupExitRoot: %v, MainnetExitRoot: %s, "+
		"RollupExitRoot: %s, GlobalExitRoot: %s, DestinationNetwork: %d, Metadata: %x, "+
		"IsMessage: %t, BlockTimestamp: %d, Type: %s}",
		c.BlockNum, c.BlockPos, c.TxHash.String(), globalIndexStr,
		c.OriginNetwork, c.OriginAddress.String(), c.DestinationAddress.String(), amountStr,
		c.ProofLocalExitRoot.String(), c.ProofRollupExitRoot.String(), c.MainnetExitRoot.String(),
		c.RollupExitRoot.String(), c.GlobalExitRoot.String(), c.DestinationNetwork, c.Metadata,
		c.IsMessage, c.BlockTimestamp, c.Type)
}

// DecodeEtrogCalldata decodes claim calldata for Etrog fork
func (c *Claim) DecodeEtrogCalldata(data []any) (bool, error) {
	// Unpack method inputs. Note that both claimAsset and claimMessage have the same interface
	// for the relevant parts
	// claimAsset/claimMessage(
	// 	0: smtProofLocalExitRoot,
	// 	1: smtProofRollupExitRoot,
	// 	2: globalIndex,
	// 	3: mainnetExitRoot,
	// 	4: rollupExitRoot,
	// 	5: originNetwork,
	// 	6: originTokenAddress/originAddress,
	// 	7: destinationNetwork,
	// 	8: destinationAddress,
	// 	9: amount,
	// 	10: metadata,
	// )

	actualGlobalIndex, ok := data[2].(*big.Int)
	if !ok {
		return false, fmt.Errorf("unexpected type for actualGlobalIndex, expected *big.Int got '%T'", data[2])
	}
	if actualGlobalIndex.Cmp(c.GlobalIndex) != 0 {
		// not the claim we're looking for
		return false, nil
	}

	rawLERProof, ok := data[0].([treetypes.DefaultHeight][common.HashLength]byte)
	if !ok {
		return false, fmt.Errorf("unexpected type for rawLERProof, expected [32][32]byte got '%T'", data[0])
	}

	rawRERProof, ok := data[1].([treetypes.DefaultHeight][common.HashLength]byte)
	if !ok {
		return false, fmt.Errorf("unexpected type for rawRERProof, expected [32][32]byte got '%T'", data[1])
	}

	c.ProofLocalExitRoot = treetypes.NewProof(rawLERProof)
	c.ProofRollupExitRoot = treetypes.NewProof(rawRERProof)

	c.MainnetExitRoot, ok = data[3].([common.HashLength]byte)
	if !ok {
		return false, fmt.Errorf("unexpected type for 'MainnetExitRoot'. Expected '[32]byte', got '%T'", data[3])
	}

	c.RollupExitRoot, ok = data[4].([common.HashLength]byte)
	if !ok {
		return false, fmt.Errorf("unexpected type for 'RollupExitRoot'. Expected '[32]byte', got '%T'", data[4])
	}

	c.DestinationNetwork, ok = data[7].(uint32)
	if !ok {
		return false, fmt.Errorf("unexpected type for 'DestinationNetwork'. Expected 'uint32', got '%T'", data[7])
	}

	c.Metadata, ok = data[10].([]byte)
	if !ok {
		return false, fmt.Errorf("unexpected type for 'claim Metadata'. Expected '[]byte', got '%T'", data[10])
	}

	c.GlobalExitRoot = crypto.Keccak256Hash(c.MainnetExitRoot.Bytes(), c.RollupExitRoot.Bytes())

	return true, nil
}

// DecodePreEtrogCalldata decodes the claim calldata for pre-Etrog forks
func (c *Claim) DecodePreEtrogCalldata(data []any) (bool, error) {
	// claimMessage/claimAsset(
	// 	0: bytes32[32] smtProof,
	// 	1: uint32 index,
	// 	2: bytes32 mainnetExitRoot,
	// 	3: bytes32 rollupExitRoot,
	// 	4: uint32 originNetwork,
	// 	5: address originTokenAddress,
	// 	6: uint32 destinationNetwork,
	// 	7: address destinationAddress,
	// 	8: uint256 amount,
	// 	9: bytes metadata
	// )
	actualGlobalIndex, ok := data[1].(uint32)
	if !ok {
		return false, fmt.Errorf("unexpected type for actualGlobalIndex, expected uint32 got '%T'", data[1])
	}

	if new(big.Int).SetUint64(uint64(actualGlobalIndex)).Cmp(c.GlobalIndex) != 0 {
		// not the claim we're looking for
		return false, nil
	}

	rawLERProof, ok := data[0].([treetypes.DefaultHeight][common.HashLength]byte)
	if !ok {
		return false, fmt.Errorf("unexpected type for proofLERBytes, expected [32][32]byte got '%T'", data[0])
	}

	c.ProofLocalExitRoot = treetypes.NewProof(rawLERProof)

	c.MainnetExitRoot, ok = data[2].([common.HashLength]byte)
	if !ok {
		return false, fmt.Errorf("unexpected type for 'MainnetExitRoot'. Expected '[32]byte', got '%T'", data[2])
	}

	c.RollupExitRoot, ok = data[3].([common.HashLength]byte)
	if !ok {
		return false, fmt.Errorf("unexpected type for 'RollupExitRoot'. Expected '[32]byte', got '%T'", data[3])
	}

	c.DestinationNetwork, ok = data[6].(uint32)
	if !ok {
		return false, fmt.Errorf("unexpected type for 'DestinationNetwork'. Expected 'uint32', got '%T'", data[6])
	}

	c.Metadata, ok = data[9].([]byte)
	if !ok {
		return false, fmt.Errorf("unexpected type for 'Metadata'. Expected '[]byte', got '%T'", data[9])
	}

	c.GlobalExitRoot = crypto.Keccak256Hash(c.MainnetExitRoot.Bytes(), c.RollupExitRoot.Bytes())

	return true, nil
}

// UnsetClaim representation of an UpdatedUnsetGlobalIndexHashChain event,
// that is emitted by the bridge contract when a claim is unset.
type UnsetClaim struct {
	BlockNum                  uint64      `meddler:"block_num"`
	BlockPos                  uint64      `meddler:"block_pos"`
	TxHash                    common.Hash `meddler:"tx_hash,hash"`
	GlobalIndex               *big.Int    `meddler:"global_index,bigint"`
	UnsetGlobalIndexHashChain common.Hash `meddler:"unset_global_index_hash_chain,hash"`
	CreatedAt                 uint64      `meddler:"created_at"`
}

// TODO: Why this struct is duplicated??
// Unclaim: this was in file bridgesync/types/types.go
// UnsetClaim: this was in file bridgesync/processor.go
type Unclaim struct {
	GlobalIndex *big.Int `json:"global_index"`
	BlockNumber uint64   `json:"block_number"`
	LogIndex    uint64   `json:"log_index"`
}

// String returns a string representation of the UnsetClaim.
func (u *UnsetClaim) String() string {
	globalIndexStr := nilStr
	if u.GlobalIndex != nil {
		globalIndexStr = u.GlobalIndex.String()
	}

	return fmt.Sprintf("UnsetClaim{BlockNum: %d, BlockPos: %d, TxHash: %s, "+
		"GlobalIndex: %s, UnsetGlobalIndexHashChain: %s, CreatedAt: %d}",
		u.BlockNum, u.BlockPos, u.TxHash.String(),
		globalIndexStr, u.UnsetGlobalIndexHashChain.String(), u.CreatedAt)
}

// SetClaim representation of a SetClaim event,
// that is emitted by the L2 bridge contract when a claim is set.
type SetClaim struct {
	BlockNum    uint64      `meddler:"block_num"`
	BlockPos    uint64      `meddler:"block_pos"`
	TxHash      common.Hash `meddler:"tx_hash,hash"`
	GlobalIndex *big.Int    `meddler:"global_index,bigint"`
	CreatedAt   uint64      `meddler:"created_at"`
}

// String returns a string representation of the SetClaim.
func (s *SetClaim) String() string {
	globalIndexStr := nilStr
	if s.GlobalIndex != nil {
		globalIndexStr = s.GlobalIndex.String()
	}
	return fmt.Sprintf("SetClaim{BlockNum: %d, BlockPos: %d, TxHash: %s, "+
		"GlobalIndex: %s, CreatedAt: %d}",
		s.BlockNum, s.BlockPos, s.TxHash.String(),
		globalIndexStr, s.CreatedAt)
}
