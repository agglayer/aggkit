package validator

import (
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// HashCertificateToSign is the hash of the certificate that the validator will sign
// before returning result to the aggsender
func HashCertificateToSign(cert *agglayertypes.Certificate) (common.Hash, error) {
	if err := cert.Validate(); err != nil {
		return common.Hash{}, err
	}

	claimsRawMetadata := make([]byte, 0,
		len(cert.ImportedBridgeExits)*(agglayertypes.GlobalIndexBytesSize+common.HashLength))
	for _, ibe := range cert.ImportedBridgeExits {
		claimsRawMetadata = append(claimsRawMetadata, aggkitcommon.BigIntToLittleEndianBytes(ibe.GlobalIndex.ToBigInt())...)
		claimsRawMetadata = append(claimsRawMetadata, ibe.BridgeExit.Hash().Bytes()...)
	}

	claimsHash := crypto.Keccak256(claimsRawMetadata)

	aggchainParams := getAggchainParams(cert)

	return crypto.Keccak256Hash(
		cert.NewLocalExitRoot.Bytes(),
		claimsHash,
		aggkitcommon.Uint64ToLittleEndianBytes(cert.Height),
		aggchainParams,
		cert.CertificateID().Bytes(),
	), nil
}

// getAggchainParams extracts the aggchain params field from the certificate
// with handling different types of aggchain data.
func getAggchainParams(cert *agglayertypes.Certificate) []byte {
	aggchainDataProof, ok := cert.AggchainData.(*agglayertypes.AggchainDataProof)
	if ok {
		return aggchainDataProof.AggchainParams.Bytes()
	}

	aggchainDataProofWithMultisig, ok := cert.AggchainData.(*agglayertypes.AggchainDataMultisigWithProof)
	if ok && aggchainDataProofWithMultisig.AggchainProof != nil {
		return aggchainDataProofWithMultisig.AggchainProof.AggchainParams.Bytes()
	}

	return aggkitcommon.ZeroHash.Bytes()
}
