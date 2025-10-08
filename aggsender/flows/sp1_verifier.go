package flows

import (
	"bytes"
	"fmt"

	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark/backend/groth16"
	"github.com/consensys/gnark/frontend"
)

// ProofVerifier is an interface that defines the methods for verifying a proof
type ProofVerifier interface {
	Verify(
		publicInput types.AggregationProofPublicValues,
		proof, vKey []byte) error
}

var _ ProofVerifier = (*SP1Verifier)(nil)

// SP1Verifier is a struct that holds the logic for verifying a SP1 proof
type SP1Verifier struct{}

// NewSP1Verifier creates a new SP1Verifier
func NewSP1Verifier() *SP1Verifier {
	return &SP1Verifier{}
}

// Verify verifies a Groth16 zk-SNARK proof for the SP1 aggregation circuit.
//
// Verify takes a pointer to AggregationProofPublicValues (publicInput) and two byte
// slices containing the serialized proof and verifying key (proof, vKey).
// It performs the following steps:
//  1. Deserializes the verifying key from vKey into a groth16.VerifyingKey.
//  2. Deserializes the proof from proof into a groth16.Proof.
//  3. Converts publicInput into a public witness suitable for verification.
//  4. Calls groth16.Verify with the deserialized proof, verifying key and public witness.
//
// The function returns nil on successful verification. It returns a non-nil error if:
//   - the verifying key or proof cannot be deserialized (malformed or wrong format),
//   - the public witness cannot be constructed from publicInput,
//   - or the underlying groth16.Verify fails (proof does not verify).
//
// Notes:
//   - The serialized proof and verifying key are expected to be in the binary format
//     produced by the corresponding gnark WriteTo methods (or compatible encoding).
//   - This method performs no side effects beyond reading its inputs and does not
//     modify the receiver.
func (s *SP1Verifier) Verify(
	publicInput types.AggregationProofPublicValues,
	proof, vKey []byte) error {
	deserializedVKey := groth16.NewVerifyingKey(ecc.BN254)
	if _, err := deserializedVKey.ReadFrom(bytes.NewReader(vKey)); err != nil {
		return fmt.Errorf("error reading vkey: %w", err)
	}

	deserializedProof := groth16.NewProof(ecc.BN254)
	if _, err := deserializedProof.ReadFrom(bytes.NewReader(proof)); err != nil {
		return fmt.Errorf("error reading proof: %w", err)
	}

	publicValuesForWitness := publicInput.ToWitness()
	publicWitness, err := frontend.NewWitness(publicValuesForWitness, ecc.BN254.ScalarField(), frontend.PublicOnly())
	if err != nil {
		return fmt.Errorf("error creating public witness: %w", err)
	}

	if err := groth16.Verify(deserializedProof, deserializedVKey, publicWitness); err != nil {
		return fmt.Errorf("error verifying aggchain proof: %w", err)
	}

	return nil
}
