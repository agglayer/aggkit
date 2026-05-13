package exit_certificate

import (
	"context"
	"fmt"
	"time"

	"github.com/agglayer/aggkit/agglayer"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	aggkitgrpc "github.com/agglayer/aggkit/grpc"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
)

const (
	waitPollInterval = 5 * time.Second
)

// RunStepWait waits for the submitted certificate (and any currently-pending one) to reach a
// final state. It runs in two phases:
//
//  1. If the agglayer reports a pending certificate for this network that is different from the
//     submitted one, wait until that pending certificate reaches a final state (Settled or
//     InError) before proceeding.
//
//  2. Poll the submitted certificate by hash until it is Settled (success) or InError (error).
//
// Requires options.agglayerGrpcUrl.
func RunStepWait(ctx context.Context, cfg *Config, certHash common.Hash) (*StepWaitResult, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP WAIT - Wait for certificate settlement")
	log.Info("═══════════════════════════════════════════")

	if cfg.Options.AgglayerGRPCURL == "" {
		return nil, fmt.Errorf("agglayerGrpcUrl is required for step wait")
	}

	grpcConfig := aggkitgrpc.DefaultConfig()
	grpcConfig.URL = cfg.Options.AgglayerGRPCURL
	client, err := agglayer.NewAgglayerClient(agglayer.ClientConfig{
		GRPC: grpcConfig,
	}, log.GetDefaultLogger())
	if err != nil {
		return nil, fmt.Errorf("create agglayer gRPC client: %w", err)
	}

	start := time.Now()
	result := &StepWaitResult{CertificateHash: certHash}

	// Phase 1 — check for any pending cert on the network that is not our submitted one.
	// This can happen when a previous certificate is still being processed.
	pending, err := client.GetLatestPendingCertificateHeader(ctx, cfg.L2NetworkID)
	if err != nil {
		log.Warnf("Could not check for pending certificate on network %d: %v", cfg.L2NetworkID, err)
	} else if pending != nil && pending.CertificateID != certHash {
		log.Infof("Found pending certificate on network %d: hash=%s height=%d — waiting for it to settle first",
			cfg.L2NetworkID, pending.CertificateID.Hex(), pending.Height)
		pendingFinal, err := waitUntilFinal(ctx, client, pending.CertificateID)
		if err != nil {
			return nil, fmt.Errorf("wait for pending certificate %s: %w", pending.CertificateID.Hex(), err)
		}
		log.Infof("Pending certificate %s reached final state: %s (elapsed: %s)",
			pending.CertificateID.Hex(), pendingFinal.Status, time.Since(start).Round(time.Second))
		if pendingFinal.Status.IsInError() {
			errMsg := ""
			if pendingFinal.Error != nil {
				errMsg = pendingFinal.Error.Error()
			}
			log.Warnf("Pending certificate %s is in error: %s", pending.CertificateID.Hex(), errMsg)
		}
		id := pending.CertificateID
		result.PendingCertWaited = &id
	}

	// Phase 2 — wait for our submitted certificate.
	log.Infof("Polling submitted certificate %s every %s...", certHash.Hex(), waitPollInterval)
	finalHeader, err := waitUntilFinal(ctx, client, certHash)
	if err != nil {
		return nil, err
	}

	elapsed := time.Since(start)
	result.FinalStatus = finalHeader.Status
	result.SettlementTxHash = finalHeader.SettlementTxHash
	result.ElapsedSeconds = elapsed.Seconds()

	if finalHeader.Status.IsSettled() {
		log.Infof("Certificate settled in %s", elapsed.Round(time.Second))
		if finalHeader.SettlementTxHash != nil {
			log.Infof("Settlement tx: %s", finalHeader.SettlementTxHash.Hex())
		}
		log.Info("STEP WAIT complete")
		return result, nil
	}

	// IsInError
	errMsg := ""
	if finalHeader.Error != nil {
		errMsg = finalHeader.Error.Error()
	}
	log.Errorf("Certificate entered InError after %s: %s", elapsed.Round(time.Second), errMsg)
	return nil, fmt.Errorf("certificate %s is in error after %s: %s",
		certHash.Hex(), elapsed.Round(time.Second), errMsg)
}

// waitUntilFinal polls GetCertificateHeader every waitPollInterval until the certificate
// reaches a closed state (Settled or InError) and returns the final header.
func waitUntilFinal(ctx context.Context, client agglayer.AgglayerClientInterface, certHash common.Hash) (*agglayertypes.CertificateHeader, error) {
	var lastStatus agglayertypes.CertificateStatus = -1
	start := time.Now()

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled after %s: %w", time.Since(start).Round(time.Second), ctx.Err())
		case <-time.After(waitPollInterval):
		}

		header, err := client.GetCertificateHeader(ctx, certHash)
		if err != nil {
			log.Warnf("GetCertificateHeader(%s) error (will retry): %v", certHash.Hex(), err)
			continue
		}

		if header.Status != lastStatus {
			log.Infof("[%s] status: %s (elapsed: %s)",
				certHash.Hex()[:10], header.Status, time.Since(start).Round(time.Second))
			lastStatus = header.Status
		}

		if header.Status.IsClosed() {
			return header, nil
		}
	}
}
