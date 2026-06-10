package exit_certificate

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/agglayer"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
)

// StepSubmitResult holds the output of the SUBMIT step.
type StepSubmitResult struct {
	CertificateHash common.Hash `json:"certificateHash"`
	// L1LatestBlockBeforeSubmittingCertificate is the latest L1 block number
	// captured right before the certificate was sent to the agglayer. It marks
	// the L1 starting point from which to look for the block where the agglayer
	// settles this certificate on L1 (e.g. for the exit certificate claimer).
	L1LatestBlockBeforeSubmittingCertificate uint64 `json:"l1LatestBlockBeforeSubmittingCertificate"`
}

// RunStepSubmit sends the signed certificate to the agglayer via gRPC and
// returns the certificate hash assigned by the agglayer.
// Requires options.agglayerClient.grpc.url.
func RunStepSubmit(ctx context.Context, cfg *Config, cert *agglayertypes.Certificate) (*StepSubmitResult, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP SUBMIT - Send certificate to agglayer")
	log.Info("═══════════════════════════════════════════")

	agglayerClientCfg := cfg.Options.AgglayerClient
	if agglayerClientCfg.GRPC == nil || agglayerClientCfg.GRPC.URL == "" {
		return nil, fmt.Errorf("agglayerClient.grpc.url is required for step submit")
	}

	client, err := agglayer.NewAgglayerClient(agglayerClientCfg, log.GetDefaultLogger())
	if err != nil {
		return nil, fmt.Errorf("create agglayer gRPC client: %w", err)
	}

	log.Infof("Checking for pending certificate on network %d...", cfg.L2NetworkID)
	pending, err := client.GetLatestPendingCertificateHeader(ctx, cfg.L2NetworkID)
	if err != nil {
		return nil, fmt.Errorf("check pending certificate for network %d: %w", cfg.L2NetworkID, err)
	}
	if pending != nil && !pending.Status.IsClosed() {
		return nil, fmt.Errorf(
			"network %d already has a pending certificate (hash: %s, height: %d, status: %s)"+
				" — wait for it to settle before submitting a new one",
			cfg.L2NetworkID, pending.CertificateID.Hex(), pending.Height, pending.Status,
		)
	}
	if pending != nil {
		log.Infof("Latest certificate on network %d is already closed (hash: %s, status: %s), proceeding with submission",
			cfg.L2NetworkID, pending.CertificateID.Hex(), pending.Status)
	} else {
		log.Info("No pending certificate found, proceeding with submission")
	}

	if cfg.L1RPCURL == "" {
		return nil, fmt.Errorf("l1RpcUrl is required for step submit to capture the latest L1 block")
	}
	l1LatestBlock, err := resolveLatestBlock(ctx, cfg.L1RPCURL)
	if err != nil {
		return nil, fmt.Errorf("capture latest L1 block before submission: %w", err)
	}
	log.Infof("Captured latest L1 block before submission: %d", l1LatestBlock)

	certHash, err := client.SendCertificate(ctx, cert)
	if err != nil {
		return nil, fmt.Errorf("send certificate to agglayer: %w", err)
	}

	log.Infof("Certificate accepted by agglayer. Hash: %s", certHash.Hex())
	log.Info("STEP SUBMIT complete")
	return &StepSubmitResult{
		CertificateHash:                          certHash,
		L1LatestBlockBeforeSubmittingCertificate: l1LatestBlock,
	}, nil
}
