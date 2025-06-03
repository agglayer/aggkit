package optimistichash

import (
	"crypto/sha256"
	"fmt"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// This file calculate the hash of AggregationProofPublicValues

// // AggregationProofPublicValues represents the public values used in the aggregation proof.
type AggregationProofPublicValues struct {
	L1Head           common.Hash
	L2PreRoot        common.Hash
	ClaimRoot        common.Hash
	L2BlockNumber    uint64
	RollupConfigHash common.Hash
	MultiBlockVKey   common.Hash
	ProverAddress    common.Address
}

// String returns a string representation of the AggregationProofPublicValues.
func (a *AggregationProofPublicValues) String() string {
	return fmt.Sprintf(
		"AggregationProofPublicValues{l1Head: %s, l2PreRoot: %s, claimRoot: %s, l2BlockNumber: %d, rollupConfigHash: %s, multiBlockVKey: %s, proverAddress: %s}",
		a.L1Head.Hex(),
		a.L2PreRoot.Hex(),
		a.ClaimRoot.Hex(),
		a.L2BlockNumber,
		a.RollupConfigHash.Hex(),
		a.MultiBlockVKey.Hex(),
		a.ProverAddress.Hex(),
	)
}

// Hash calculates the hash of the AggregationProofPublicValues using ABI encoding.
func (s *AggregationProofPublicValues) Hash() (common.Hash, error) {
	// Crear tipos ABI uno por uno
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

	// Definir la secuencia de argumentos ABI
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
		s.ProverAddress,
	)
	if err != nil {
		return common.Hash{}, fmt.Errorf("failed to pack arguments: %w", err)
	}
	shaDigest := sha256.Sum256(packed)
	return common.BytesToHash(shaDigest[:]), nil
}
