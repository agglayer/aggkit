package types

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"math/big"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// This file calculate the hash of AggregationProofPublicValues

// AggregationProofPublicValues represents the public values used in the aggregation proof.
type AggregationProofPublicValues struct {
	L1Head              common.Hash
	L2PreRoot           common.Hash
	ClaimRoot           common.Hash
	L2BlockNumber       uint64
	RollupConfigHash    common.Hash
	MultiBlockVKey      common.Hash
	TrustedSigner       common.Address
	AggregationVKeyHash common.Hash
}

// String returns a string representation of the AggregationProofPublicValues.
func (s *AggregationProofPublicValues) String() string {
	return fmt.Sprintf(
		"AggregationProofPublicValues{l1Head: %s, l2PreRoot: %s, claimRoot: %s,"+
			" l2BlockNumber: %d, rollupConfigHash: %s, multiBlockVKey: %s, trustedSignerAddress: %s,"+
			" aggregationVKeyHash: %s}",
		s.L1Head.Hex(),
		s.L2PreRoot.Hex(),
		s.ClaimRoot.Hex(),
		s.L2BlockNumber,
		s.RollupConfigHash.Hex(),
		s.MultiBlockVKey.Hex(),
		s.TrustedSigner.Hex(),
		s.AggregationVKeyHash.Hex(),
	)
}

// Hash calculates the hash of the AggregationProofPublicValues using ABI encoding.
func (s *AggregationProofPublicValues) Hash() (common.Hash, error) {
	tBytes32, err := abi.NewType("bytes32", "", nil)
	if err != nil {
		return common.Hash{}, fmt.Errorf("failed to create bytes32 type: %w", err)
	}

	tUint64, err := abi.NewType("uint64", "", nil)
	if err != nil {
		return common.Hash{}, fmt.Errorf("failed to create uint64 type: %w", err)
	}

	tAddress, err := abi.NewType("address", "", nil)
	if err != nil {
		return common.Hash{}, fmt.Errorf("failed to create address type: %w", err)
	}

	args := abi.Arguments{
		{Type: tBytes32},
		{Type: tBytes32},
		{Type: tBytes32},
		{Type: tUint64},
		{Type: tBytes32},
		{Type: tBytes32},
		{Type: tAddress},
	}
	packed, err := args.Pack(
		s.L1Head,
		s.L2PreRoot,
		s.ClaimRoot,
		s.L2BlockNumber,
		s.RollupConfigHash,
		s.MultiBlockVKey,
		s.TrustedSigner,
	)
	if err != nil {
		return common.Hash{}, fmt.Errorf("failed to pack arguments: %w", err)
	}
	shaDigest := sha256.Sum256(packed)
	return common.BytesToHash(shaDigest[:]), nil
}

// AggchainParams represents the parameters required for aggchain proof verification.
type AggchainParams struct {
	AggregationProofPublicValues
	OptimisticMode bool
}

// String returns a string representation of the AggchainParams.
func (a *AggchainParams) String() string {
	return fmt.Sprintf(
		"AggchainParamsValues{ l2_pre_root: %s"+
			" claim_root: %s"+
			" claim_block_number: %d"+
			" rollup_config_hash: %s"+
			" optimistic_mode: %t"+
			" trusted_signer_address: %s"+
			" range_v_key_commitment: %s"+
			" aggregation_v_key_hash: %s"+
			"}",
		a.L2PreRoot.String(),
		a.ClaimRoot.String(),
		a.L2BlockNumber,
		a.RollupConfigHash.String(),
		a.OptimisticMode,
		a.TrustedSigner.String(),
		a.MultiBlockVKey.String(),
		a.AggregationVKeyHash.String(),
	)
}

// Hash calculates the hash of the AggchainParams using a custom encoding,
// the same as abi.EncodePacked, since go-ethereum does not provide a function for that.
func (a *AggchainParams) Hash() (common.Hash, error) {
	buf := new(bytes.Buffer)

	// bytes32 (32 bytes each)
	buf.Write(a.L2PreRoot[:])
	buf.Write(a.ClaimRoot[:])

	// uint256 (big endian 32 bytes)
	uintBuf := common.LeftPadBytes(new(big.Int).SetUint64(a.L2BlockNumber).Bytes(), aggkitcommon.HashSize)
	buf.Write(uintBuf)

	buf.Write(a.RollupConfigHash[:])

	// bool (1 byte)
	if a.OptimisticMode {
		buf.WriteByte(1)
	} else {
		buf.WriteByte(0)
	}

	// address (20 bytes)
	buf.Write(a.TrustedSigner.Bytes())

	// remaining bytes32s
	buf.Write(a.MultiBlockVKey[:])
	buf.Write(a.AggregationVKeyHash[:])

	return crypto.Keccak256Hash(buf.Bytes()), nil
}
