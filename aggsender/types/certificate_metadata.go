package types

import (
	"encoding/binary"
	"log"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

const (
	CertificateMetadataV0 = uint8(0) // Pre v1 metadata, only ToBlock is stored
	CertificateMetadataV1 = uint8(1) // Post v1 metadata, FromBlock, Offset, CreatedAt are stored
	CertificateMetadataV2 = uint8(2) // Same V1 + typeCert
)

type CertificateMetadata struct {
	// ToBlock contains the pre v1 value stored in the metadata certificate field
	// is not stored in the hash post v1
	ToBlock uint64

	// FromBlock is the block number from which the certificate contains data
	FromBlock uint64

	// Offset is the number of blocks from the FromBlock that the certificate contains
	Offset uint32

	// CreatedAt is the timestamp when the certificate was created
	CreatedAt uint32

	// Version is the version of the metadata
	Version uint8

	// CertType is the type of certificate
	CertType uint8 // version >= V2
}

// NewCertificateMetadata returns a new CertificateMetadata from the given hash
func NewCertificateMetadata(fromBlock uint64, offset uint32, createdAt uint32, certType uint8) *CertificateMetadata {
	return &CertificateMetadata{
		FromBlock: fromBlock,
		Offset:    offset,
		CreatedAt: createdAt,
		CertType:  certType,
		Version:   CertificateMetadataV2,
	}
}

// NewCertificateMetadataFromHash returns a new CertificateMetadata from the given hash
func NewCertificateMetadataFromHash(hash common.Hash) *CertificateMetadata {
	b := hash.Bytes()
	version := b[0]
	if version == CertificateMetadataV0 {
		return &CertificateMetadata{
			ToBlock: hash.Big().Uint64(),
		}
	} else if version == CertificateMetadataV1 {
		return &CertificateMetadata{
			Version:   version,
			FromBlock: binary.BigEndian.Uint64(b[1:9]),
			Offset:    binary.BigEndian.Uint32(b[9:13]),
			CreatedAt: binary.BigEndian.Uint32(b[13:17]),
		}
	} else if version == CertificateMetadataV2 {
		return &CertificateMetadata{
			Version:   version,
			FromBlock: binary.BigEndian.Uint64(b[1:9]),
			Offset:    binary.BigEndian.Uint32(b[9:13]),
			CreatedAt: binary.BigEndian.Uint32(b[13:17]),
			CertType:  b[17],
		}
	} else {
		// Unsupported version, return nil
		log.Panicf("aggsender: unsupported certificate metadata version: %d", version)
		return nil
	}
}

// ToHash returns the hash of the metadata
func (c *CertificateMetadata) ToHash() common.Hash {
	b := make([]byte, common.HashLength) // 32-byte hash

	// Encode version
	if c.Version == CertificateMetadataV0 {
		// For v0, we only store ToBlock
		return common.BigToHash(new(big.Int).SetUint64(c.ToBlock))
	}
	b[0] = c.Version
	// CertificateMetadataV1
	// Encode fromBlock
	binary.BigEndian.PutUint64(b[1:9], c.FromBlock)

	// Encode offset
	binary.BigEndian.PutUint32(b[9:13], c.Offset)

	// Encode createdAt
	binary.BigEndian.PutUint32(b[13:17], c.CreatedAt)

	if c.Version == CertificateMetadataV2 {
		// Encode typeCert
		b[17] = c.CertType
	}
	return common.BytesToHash(b)
}
