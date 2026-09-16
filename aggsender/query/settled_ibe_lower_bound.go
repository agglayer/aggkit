package query

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitdb "github.com/agglayer/aggkit/db"
)

const (
	// maxSettledIBELowerBoundWalk is the maximum number of certificates walked downwards, starting
	// from aggsender's last settled certificate, while looking for the certificate that imported a
	// given settled imported bridge exit (IBE). It bounds the cost (and worst-case latency) of the
	// walk: if the limit is reached without a match, storageIBELowerBounder gives up and reports
	// found=false so the caller falls back to a safe default (block 0) instead of scanning forever.
	maxSettledIBELowerBoundWalk = 512
)

// storageIBELowerBounder implements SettledIBELowerBounder using aggsender's own certificate
// storage. Starting at the last settled certificate, it walks certificate heights downwards
// looking for the certificate whose ImportedBridgeExits contains the target settled IBE, and
// returns that certificate's FromBlock as a provable lower bound: the certificate that imported
// the IBE necessarily covers (in its [FromBlock, ToBlock] range) the L2 block in which the IBE
// claim happened, so FromBlock <= true IBE block.
//
// The walk gives up (found=false) as soon as it can no longer trust the data at a given height:
// a missing certificate (pruned by the retention policy), an AggLayer-sourced header (whose
// FromBlock is not trustworthy, since it was recovered rather than locally produced), a nil
// SignedCertificate, or a FromBlock of 0 (nothing tighter than the existing 0 fallback). It never
// guesses past one of these points.
type storageIBELowerBounder struct {
	storage db.AggSenderStorage
}

// NewStorageIBELowerBounder creates a SettledIBELowerBounder backed by the given aggsender
// certificate storage.
func NewStorageIBELowerBounder(storage db.AggSenderStorage) SettledIBELowerBounder {
	return &storageIBELowerBounder{storage: storage}
}

// LowerBoundForSettledIBE implements SettledIBELowerBounder. See the storageIBELowerBounder
// doc comment for the search strategy and the stopping conditions.
func (b *storageIBELowerBounder) LowerBoundForSettledIBE(
	ctx context.Context,
	settledIBE *agglayertypes.SettledImportedBridgeExit,
) (uint64, bool, error) {
	if settledIBE == nil || settledIBE.GlobalIndex == nil {
		return 0, false, nil
	}

	lastSettled, err := b.storage.GetLastSettledCertificate()
	if err != nil {
		if errors.Is(err, aggkitdb.ErrNotFound) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("error getting last settled certificate: %w", err)
	}
	if lastSettled == nil {
		return 0, false, nil
	}

	height := lastSettled.Height
	for walked := 0; walked < maxSettledIBELowerBoundWalk; walked++ {
		if err := ctx.Err(); err != nil {
			return 0, false, fmt.Errorf("context canceled while walking certificates for settled IBE lower bound: %w", err)
		}
		cert, err := b.storage.GetCertificateByHeight(height)
		if err != nil {
			if errors.Is(err, aggkitdb.ErrNotFound) {
				// Certificate pruned/missing (retention gap): can't verify further back, give up
				// rather than guess.
				return 0, false, nil
			}
			return 0, false, fmt.Errorf("error getting certificate at height %d: %w", height, err)
		}
		if cert == nil || cert.Header == nil {
			return 0, false, nil
		}
		if cert.Header.CertSource == types.CertificateSourceAggLayer {
			// AggLayer-sourced headers are recovered from agglayer with a placeholder signed
			// certificate and carry no trustworthy FromBlock: give up rather than guess.
			return 0, false, nil
		}
		if cert.SignedCertificate == nil {
			return 0, false, nil
		}
		if cert.Header.FromBlock == 0 {
			return 0, false, nil
		}

		var agglayerCert agglayertypes.Certificate
		if err := json.Unmarshal([]byte(*cert.SignedCertificate), &agglayerCert); err != nil {
			return 0, false, fmt.Errorf("error unmarshalling signed certificate at height %d: %w", height, err)
		}

		if certificateImportsSettledIBE(&agglayerCert, settledIBE) {
			return cert.Header.FromBlock, true, nil
		}

		if height == 0 {
			break
		}
		height--
	}

	return 0, false, nil
}

// certificateImportsSettledIBE reports whether cert.ImportedBridgeExits contains an entry
// matching target both by GlobalIndex and by bridge-exit hash. Both must match: GlobalIndex alone
// identifies a position in the exit tree, not the exit's content, so matching on it alone could
// produce a false positive (e.g. a different, un-related certificate reusing the same index after
// a reorg-like edge case).
func certificateImportsSettledIBE(
	cert *agglayertypes.Certificate,
	target *agglayertypes.SettledImportedBridgeExit,
) bool {
	for _, ibe := range cert.ImportedBridgeExits {
		if ibe == nil || ibe.GlobalIndex == nil || ibe.BridgeExit == nil {
			continue
		}
		if ibe.GlobalIndex.ToBigInt().Cmp(target.GlobalIndex) != 0 {
			continue
		}
		if ibe.BridgeExit.Hash() != target.BridgeExitHash {
			continue
		}
		return true
	}
	return false
}
