// Package waiter gates a validated correlator job until the AggLayer certificate
// covering the bridge leaf is settled on L1 AND the resulting GER is visible on
// the destination chain.  Only when both conditions hold is the job released to
// the proof builder.
package waiter

import (
	"context"
	"time"

	"github.com/agglayer/aggkit/dvnworker/correlator"
	"github.com/agglayer/aggkit/log"
)

const (
	defaultPollInterval = 5 * time.Second
	logProgressEvery    = 30 * time.Second
)

// CertificateChecker can answer whether the bridge leaf identified by
// (networkID, depositCount) is covered by a certificate that has been
// settled on L1.
type CertificateChecker interface {
	IsLeafSettled(ctx context.Context, networkID uint32, depositCount uint32) (bool, error)
}

// GERChecker can answer whether a Global Exit Root whose L1 info-tree index
// is >= atOrAfterL1InfoTreeIndex has already been injected on the destination
// chain.
type GERChecker interface {
	IsGERInjected(ctx context.Context, atOrAfterL1InfoTreeIndex uint32) (bool, error)
}

// Job carries a validated correlator result together with the decoded
// globalIndex fields needed by the waiter.
type Job struct {
	correlator.ValidationResult

	// SourceBridgeNetwork is the AggLayer network ID of the source chain,
	// decoded from the OFT packet's globalIndex.
	SourceBridgeNetwork uint32

	// DepositCount is the local leaf index on the source chain, decoded from
	// the OFT packet's globalIndex.
	DepositCount uint32

	// L1InfoTreeIndex is the L1 info-tree index that must be injected on the
	// destination chain before the GER condition is satisfied.
	L1InfoTreeIndex uint32
}

// Waiter blocks a job until both settlement conditions are satisfied.
type Waiter struct {
	certChecker  CertificateChecker
	gerChecker   GERChecker
	pollInterval time.Duration
	log          *log.Logger
}

// New creates a Waiter.  pollInterval controls how often the two conditions are
// rechecked; pass 0 to use the default (5 s).
func New(
	certChecker CertificateChecker, gerChecker GERChecker, pollInterval time.Duration, logger *log.Logger,
) *Waiter {
	if pollInterval <= 0 {
		pollInterval = defaultPollInterval
	}
	return &Waiter{
		certChecker:  certChecker,
		gerChecker:   gerChecker,
		pollInterval: pollInterval,
		log:          logger,
	}
}

// Wait blocks until:
//  1. The AggLayer certificate that covers job.DepositCount on job.SourceBridgeNetwork
//     has been settled on L1, AND
//  2. A GER whose L1 info-tree index is >= job.L1InfoTreeIndex is visible on
//     the destination chain.
//
// It returns nil when both conditions are met, or ctx.Err() if the context is
// cancelled first.
func (w *Waiter) Wait(ctx context.Context, job Job) error {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	logTimer := time.NewTimer(logProgressEvery)
	defer logTimer.Stop()

	certSettled := false
	gerInjected := false

	for {
		var err error

		if !certSettled {
			certSettled, err = w.certChecker.IsLeafSettled(ctx, job.SourceBridgeNetwork, job.DepositCount)
			if err != nil {
				w.log.Warnw("dvnworker waiter: error checking certificate settlement",
					"networkID", job.SourceBridgeNetwork,
					"depositCount", job.DepositCount,
					"err", err,
				)
			}
		}

		if !gerInjected {
			gerInjected, err = w.gerChecker.IsGERInjected(ctx, job.L1InfoTreeIndex)
			if err != nil {
				w.log.Warnw("dvnworker waiter: error checking GER injection",
					"l1InfoTreeIndex", job.L1InfoTreeIndex,
					"err", err,
				)
			}
		}

		if certSettled && gerInjected {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-logTimer.C:
			w.log.Infow("dvnworker waiter: still waiting for settlement/GER",
				"networkID", job.SourceBridgeNetwork,
				"depositCount", job.DepositCount,
				"l1InfoTreeIndex", job.L1InfoTreeIndex,
				"certSettled", certSettled,
				"gerInjected", gerInjected,
			)
			logTimer.Reset(logProgressEvery)
		case <-ticker.C:
		}
	}
}
