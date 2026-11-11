// Package signer signs the GERs with the key of the validator (a keystore, or a KMS) using
// github.com/agglayer/go_signer.
package signer

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"

	"github.com/agglayer/aggkit/tools/ger_validator_signer/config"
	gosigner "github.com/agglayer/go_signer/signer"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	// SignatureLength is r (32 bytes) + s (32 bytes) + v (1 byte)
	SignatureLength = 65
	rsLength        = 64
	vIndex          = rsLength
	// recoveryIDOffset is added to the recovery id (0 or 1) to get the v that the contract expects (27 or 28)
	recoveryIDOffset = 27
	signerName       = "ger-validator"
)

// Signer signs GERs as the AggOracleCommittee v2 contract verifies them
type Signer struct {
	hashSigner signertypes.HashSigner
	address    common.Address
}

// New creates the signer from its configuration (local keystore, GCP KMS or AWS KMS). chainID is only needed by
// go_signer to sign transactions, which the validator never does.
func New(ctx context.Context, cfg config.SignerConfig, chainID uint64, logger *slog.Logger) (*Signer, error) {
	goSigner, err := gosigner.NewSigner(ctx, chainID, signertypes.SignerConfig{
		Method: signertypes.SignMethod(cfg.Method()),
		Config: cfg.Params(),
	}, signerName, NewLogAdapter(logger))
	if err != nil {
		return nil, fmt.Errorf("failed to create the signer: %w", err)
	}
	if err := goSigner.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize the signer: %w", err)
	}
	return NewFromHashSigner(goSigner, goSigner.PublicAddress()), nil
}

// NewFromHashSigner creates a signer from something that signs hashes, and the address of its key
func NewFromHashSigner(hashSigner signertypes.HashSigner, address common.Address) *Signer {
	return &Signer{hashSigner: hashSigner, address: address}
}

// Address returns the address of the key. It is the address of the validator on the AggOracleCommittee
func (s *Signer) Address() common.Address {
	return s.address
}

// GERDigest returns what the validators sign, and the AggOracleCommittee contract verifies:
// keccak256(abi.encodePacked(ger)), the hash of the 32 bytes of the GER, without the EIP-191 prefix.
func GERDigest(ger common.Hash) common.Hash {
	return crypto.Keccak256Hash(ger.Bytes())
}

// SignGER signs the GER and returns the signature as the contract requires it: 65 bytes (r, s, v) with v 27 or 28
// and s in the lower half of the curve order, which makes the whole transaction revert otherwise.
// The signer can return v as 0/1 or 27/28, and a KMS can return a high s: the signature is normalized.
// It is verified before returning it: it must recover the address of the key, so a signer that misbehaves
// can never make the validator return a signature that is not its own.
func (s *Signer) SignGER(ctx context.Context, ger common.Hash) ([]byte, error) {
	digest := GERDigest(ger)
	raw, err := s.hashSigner.SignHash(ctx, digest)
	if err != nil {
		return nil, fmt.Errorf("failed to sign the GER: %w", err)
	}

	signature, err := normalize(raw)
	if err != nil {
		return nil, err
	}

	recovered, err := recoverAddress(digest, signature)
	if err != nil {
		return nil, err
	}
	if recovered != s.address {
		return nil, fmt.Errorf("the signature is from %s, not from the address of the signer %s", recovered, s.address)
	}
	return signature, nil
}

// normalize returns the signature with v as 27 or 28 and s in the lower half of the curve order
func normalize(raw []byte) ([]byte, error) {
	if len(raw) != SignatureLength {
		return nil, fmt.Errorf("the signer returned a signature of %d bytes, expected %d", len(raw), SignatureLength)
	}

	recoveryID := raw[vIndex]
	if recoveryID >= recoveryIDOffset {
		recoveryID -= recoveryIDOffset
	}
	if recoveryID > 1 {
		return nil, fmt.Errorf("the signer returned an invalid v value: %d", raw[vIndex])
	}

	curveOrder := crypto.S256().Params().N
	halfOrder := new(big.Int).Rsh(curveOrder, 1)
	s := new(big.Int).SetBytes(raw[32:rsLength])
	signature := make([]byte, SignatureLength)
	copy(signature, raw[:rsLength])
	if s.Cmp(halfOrder) > 0 {
		// (r, n-s, v^1) is the same signature, in the canonical form
		s.Sub(curveOrder, s)
		s.FillBytes(signature[32:rsLength])
		recoveryID ^= 1
	}
	signature[vIndex] = recoveryID + recoveryIDOffset
	return signature, nil
}

// recoverAddress returns the address that signed the digest
func recoverAddress(digest common.Hash, signature []byte) (common.Address, error) {
	ethSignature := make([]byte, SignatureLength)
	copy(ethSignature, signature)
	ethSignature[vIndex] -= recoveryIDOffset

	pubKey, err := crypto.Ecrecover(digest.Bytes(), ethSignature)
	if err != nil {
		return common.Address{}, fmt.Errorf("the signature of the signer is not valid: %w", err)
	}
	return common.BytesToAddress(crypto.Keccak256(pubKey[1:])[12:]), nil
}
