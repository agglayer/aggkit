package gervalidator

import (
	"bytes"
	"fmt"
	"math/big"
	"sort"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	// recoveryIDOffset is added to the recovery id (0 or 1) to get the v of the contract (27 or 28)
	recoveryIDOffset = 27
	rLength          = 32
	sLength          = 32
	vIndex           = rLength + sLength
)

// GERDigest returns the digest that the validators sign and the AggOracleCommittee contract verifies:
// keccak256(abi.encodePacked(ger)). There is no EIP-191 prefix.
func GERDigest(ger common.Hash) common.Hash {
	return crypto.Keccak256Hash(ger.Bytes())
}

// RecoverSigner validates a signature of the GER and returns it normalized (v is 27 or 28, as the contract
// requires) together with the address that signed. The signature is rejected if it doesn't have 65 bytes,
// if v is not 0, 1, 27 or 28, if r or s are out of range or if s is in the upper half of the curve order
// (malleable): the contract reverts the whole transaction on any of them.
func RecoverSigner(ger common.Hash, signature []byte) ([]byte, common.Address, error) {
	if len(signature) != SignatureLength {
		return nil, common.Address{}, fmt.Errorf("%w: signature has %d bytes, expected %d",
			ErrInvalidResponse, len(signature), SignatureLength)
	}

	recoveryID := signature[vIndex]
	if recoveryID >= recoveryIDOffset {
		recoveryID -= recoveryIDOffset
	}
	if recoveryID > 1 {
		return nil, common.Address{}, fmt.Errorf("%w: invalid v value %d", ErrInvalidResponse, signature[vIndex])
	}

	r := new(big.Int).SetBytes(signature[:rLength])
	s := new(big.Int).SetBytes(signature[rLength:vIndex])
	if !crypto.ValidateSignatureValues(recoveryID, r, s, true) {
		return nil, common.Address{}, fmt.Errorf("%w: r or s out of range, or s is not in the lower half of the curve order",
			ErrInvalidResponse)
	}

	ethSignature := make([]byte, SignatureLength)
	copy(ethSignature, signature[:vIndex])
	ethSignature[vIndex] = recoveryID
	pubKey, err := crypto.Ecrecover(GERDigest(ger).Bytes(), ethSignature)
	if err != nil {
		return nil, common.Address{}, fmt.Errorf("%w: cannot recover the signer: %w", ErrInvalidResponse, err)
	}

	normalized := ethSignature
	normalized[vIndex] = recoveryID + recoveryIDOffset
	return normalized, common.BytesToAddress(crypto.Keccak256(pubKey[1:])[12:]), nil
}

// signedGER is a valid signature of a GER and the address that signed it
type signedGER struct {
	signer    common.Address
	signature []byte
}

// packSignatures concatenates the signatures ordered by signer address in strictly ascending order, as the
// AggOracleCommittee requires (it reverts with ValidatorsNotOrdered otherwise). It returns the signatures
// and the signers in that order. A signer can't appear twice.
func packSignatures(signed []signedGER) ([]byte, []common.Address, error) {
	sorted := make([]signedGER, len(signed))
	copy(sorted, signed)
	sort.Slice(sorted, func(i, j int) bool {
		return bytes.Compare(sorted[i].signer.Bytes(), sorted[j].signer.Bytes()) < 0
	})

	packed := make([]byte, 0, len(sorted)*SignatureLength)
	signers := make([]common.Address, 0, len(sorted))
	for i, entry := range sorted {
		if i > 0 && entry.signer == sorted[i-1].signer {
			return nil, nil, fmt.Errorf("signer %s is repeated", entry.signer)
		}
		packed = append(packed, entry.signature...)
		signers = append(signers, entry.signer)
	}
	return packed, signers, nil
}
