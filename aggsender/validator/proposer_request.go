package validator

import (
	"encoding/binary"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// proposerRequestDomain separates the proposer signature of a ValidateCertificate request
// from any other signature made with the same key, in particular the committee signature
// of the certificate hash that the proposer also produces.
const proposerRequestDomain = "aggkit.aggsender.validator.v1.ValidateCertificate"

// DefaultProposerRequestMaxClockSkew is how far the timestamp of a ValidateCertificate
// request may be from the validator's clock, in either direction.
const DefaultProposerRequestMaxClockSkew = time.Minute

// ProposerRequestDigest is the digest the proposer signs to authenticate a
// ValidateCertificate request. It binds the signature to the certificate, to the previous
// certificate id and the last L2 block sent with it, to the validator the request is
// addressed to and to the time it was made, so it cannot be taken from a certificate,
// replayed against another validator or reused later.
func ProposerRequestDigest(
	certificateHash common.Hash,
	previousCertificateID *common.Hash,
	lastL2BlockInCert uint64,
	validatorAddress common.Address,
	requestTimestamp uint64,
) common.Hash {
	var prevID common.Hash
	if previousCertificateID != nil {
		prevID = *previousCertificateID
	}

	var lastBlock, timestamp [8]byte
	binary.BigEndian.PutUint64(lastBlock[:], lastL2BlockInCert)
	binary.BigEndian.PutUint64(timestamp[:], requestTimestamp)

	return crypto.Keccak256Hash(
		[]byte(proposerRequestDomain),
		certificateHash.Bytes(),
		prevID.Bytes(),
		lastBlock[:],
		validatorAddress.Bytes(),
		timestamp[:],
	)
}

// normalizeRecoveryID turns a signature with a legacy recovery id (27 or 28) into the 0/1
// form that crypto.SigToPub expects. Other signatures are returned unchanged.
func normalizeRecoveryID(signature []byte) []byte {
	const legacyRecoveryOffset = 27
	if len(signature) != crypto.SignatureLength || signature[crypto.RecoveryIDOffset] < legacyRecoveryOffset {
		return signature
	}

	normalized := make([]byte, len(signature))
	copy(normalized, signature)
	normalized[crypto.RecoveryIDOffset] -= legacyRecoveryOffset

	return normalized
}
