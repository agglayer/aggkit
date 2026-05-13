package exit_certificate

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/agglayer"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	aggkitgrpc "github.com/agglayer/aggkit/grpc"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
)

// StepSubmitResult holds the output of the SUBMIT step.
type StepSubmitResult struct {
	CertificateHash common.Hash `json:"certificateHash"`
}

// RunStepSubmit sends the signed certificate to the agglayer via gRPC and
// returns the certificate hash assigned by the agglayer.
// Requires options.agglayerGrpcUrl.
func RunStepSubmit(ctx context.Context, cfg *Config, cert *agglayertypes.Certificate) (*StepSubmitResult, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP SUBMIT - Send certificate to agglayer")
	log.Info("═══════════════════════════════════════════")

	if cfg.Options.AgglayerGRPCURL == "" {
		return nil, fmt.Errorf("agglayerGrpcUrl is required for step submit")
	}

	grpcConfig := aggkitgrpc.DefaultConfig()
	grpcConfig.URL = cfg.Options.AgglayerGRPCURL
	client, err := agglayer.NewAgglayerClient(agglayer.ClientConfig{
		GRPC: grpcConfig,
	}, log.GetDefaultLogger())
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
			"network %d already has a pending certificate (hash: %s, height: %d, status: %s) — wait for it to settle before submitting a new one",
			cfg.L2NetworkID, pending.CertificateID.Hex(), pending.Height, pending.Status,
		)
	}
	if pending != nil {
		log.Infof("Latest certificate on network %d is already closed (hash: %s, status: %s), proceeding with submission",
			cfg.L2NetworkID, pending.CertificateID.Hex(), pending.Status)
	} else {
		log.Info("No pending certificate found, proceeding with submission")
	}

	certHash, err := client.SendCertificate(ctx, cert)
	if err != nil {
		return nil, fmt.Errorf("send certificate to agglayer: %w", err)
	}

	log.Infof("Certificate accepted by agglayer. Hash: %s", certHash.Hex())
	log.Info("STEP SUBMIT complete")
	return &StepSubmitResult{CertificateHash: certHash}, nil
}
