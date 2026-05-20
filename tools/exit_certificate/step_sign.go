package exit_certificate

import (
	"context"
	"encoding/json"
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/validator"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/go_signer/signer"
)

// RunStepSign signs the certificate with the configured keystore and sets AggchainData
// to an AggchainDataMultisig containing the ECDSA signature.
func RunStepSign(
	ctx context.Context, cfg *Config, cert *agglayertypes.Certificate,
) (*agglayertypes.Certificate, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP SIGN — Sign exit certificate")
	log.Info("═══════════════════════════════════════════")

	if cfg.SignerConfig.Method == "" {
		return nil, fmt.Errorf("signerConfig.Method is required for signing")
	}

	chainID, err := fetchL2ChainID(ctx, cfg.L2RPCURL)
	if err != nil {
		return nil, fmt.Errorf("fetch L2 chain ID: %w", err)
	}

	certSigner, err := signer.NewSigner(ctx, chainID, cfg.SignerConfig, "exit-certificate", log.GetDefaultLogger())
	if err != nil {
		return nil, fmt.Errorf("create signer (method=%s): %w", cfg.SignerConfig.Method, err)
	}
	if err := certSigner.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("initialize signer: %w", err)
	}
	log.Infof("Signer public address: %s", certSigner.PublicAddress().Hex())

	log.Infof("Certificate to sign: networkID=%d height=%d prevLER=%s newLER=%s bridgeExits=%d importedBridgeExits=%d",
		cert.NetworkID, cert.Height,
		cert.PrevLocalExitRoot.Hex(), cert.NewLocalExitRoot.Hex(),
		len(cert.BridgeExits), len(cert.ImportedBridgeExits))
	log.Infof("CertificateID: %s", cert.CertificateID().Hex())

	hashToSign, err := validator.HashCertificateToSign(cert)
	if err != nil {
		return nil, fmt.Errorf("hash certificate to sign: %w", err)
	}
	log.Infof("Hash to sign:  %s", hashToSign.Hex())

	sig, err := certSigner.SignHash(ctx, hashToSign)
	if err != nil {
		return nil, fmt.Errorf("sign certificate hash: %w", err)
	}

	cert.AggchainData = &agglayertypes.AggchainDataMultisig{
		Multisig: &agglayertypes.Multisig{
			Signatures: []agglayertypes.ECDSAMultisigEntry{
				{Index: 0, Signature: sig},
			},
		},
	}

	log.Info("STEP SIGN complete: certificate signed")
	return cert, nil
}

func fetchL2ChainID(ctx context.Context, rpcURL string) (uint64, error) {
	result, err := singleRPC(ctx, rpcURL, "eth_chainId", nil, defaultRetries)
	if err != nil {
		return 0, err
	}
	var hexStr string
	if err := json.Unmarshal(result, &hexStr); err != nil {
		return 0, fmt.Errorf("parse chain ID: %w", err)
	}
	return hexToUint64(hexStr), nil
}
