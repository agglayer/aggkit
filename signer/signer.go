package signer

import (
	"context"

	"github.com/ethereum/go-ethereum/common"
)

type Signer interface {
	// Initialize the signer
	Initialize(context.Context) error
	// SignHash signs the hash using the private key
	SignHash(context.Context, common.Hash) ([]byte, error)
	// PublicKey returns the public key of the signer
	PublicAddress() common.Address
	// String returns a string representation of the signer (no secrets)
	String() string
}
