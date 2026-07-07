package proof

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/agglayer/aggkit/autoclaim/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/tree"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

// maxLeafSelectionAdvances bounds how many consecutive L1 info tree leaves the rollup preparer
// walks forward while looking for the first leaf that covers the source network's local exit root.
// The common case advances zero times (the first leaf at/after the verify block is an exact match);
// the bound only guards against an unexpectedly long run of not-yet-covering leaves.
const maxLeafSelectionAdvances = 256

// RollupL1InfoTreeSyncer exposes the l1infotreesync methods needed to prepare rollup-origin claim
// proofs. It is a superset of the L1-origin preparer's needs (it adds GetLocalExitRoot, which resolves
// a source network's LER from a rollup exit root) and is satisfied by *l1infotreesync.L1InfoTreeSync.
type RollupL1InfoTreeSyncer interface {
	GetInfoByIndex(ctx context.Context, index uint32) (*l1infotreesync.L1InfoTreeLeaf, error)
	GetFirstInfoAfterBlock(blockNum uint64) (*l1infotreesync.L1InfoTreeLeaf, error)
	GetLocalExitRoot(ctx context.Context, networkID uint32, rollupExitRoot common.Hash) (common.Hash, error)
	GetRollupExitTreeMerkleProof(ctx context.Context, networkID uint32, root common.Hash) (treetypes.Proof, error)
}

// LeafProofRefresher refreshes a stale leaf-to-LER Merkle proof from a source network's bridge
// service. It is used only when the stored proof was built against a LER that has since been
// superseded. The production implementation resolves the source network's bridge service URL via
// bridgeservicefinder.Finder and calls bridgeservice/client.Client.GetClaimProof(sourceNetwork,
// leafIndex, depositCount), converting the returned proof_local_exit_root to a tree proof.
type LeafProofRefresher interface {
	RefreshLeafProof(
		ctx context.Context, sourceNetwork, leafIndex, depositCount uint32,
	) (treetypes.Proof, error)
}

// RollupPreparer prepares bridge claim proof inputs for rollup-origin Auto Claim requests
// (requests whose Bridge.SourceNetwork is a rollup, i.e. != 0). It implements types.ProofPreparer.
type RollupPreparer struct {
	l1InfoTree RollupL1InfoTreeSyncer
	// gerSyncer gates readiness on the destination L2's injected GER state. It is nil for an
	// L1-destination claimer (network 0), where l1infotreesync having the leaf is sufficient.
	gerSyncer L2GERSyncer
	// refresher fetches a fresh leaf-to-LER proof from the source bridge service on staleness.
	refresher LeafProofRefresher
	now       func() time.Time
}

// NewRollupPreparer creates a rollup-origin proof preparer. Pass gerSyncer=nil for an
// L1-destination claimer (network 0), which has no destination GER injection to gate on.
func NewRollupPreparer(
	l1InfoTree RollupL1InfoTreeSyncer,
	gerSyncer L2GERSyncer,
	refresher LeafProofRefresher,
) *RollupPreparer {
	return &RollupPreparer{
		l1InfoTree: l1InfoTree,
		gerSyncer:  gerSyncer,
		refresher:  refresher,
		now:        time.Now,
	}
}

// PrepareProof implements types.ProofPreparer. It returns nil, nil when the request is not ready yet.
func (p *RollupPreparer) PrepareProof(
	ctx context.Context, request types.AutoClaimRequest,
) (*types.ClaimProof, error) {
	result, err := p.Prepare(ctx, request)
	if err != nil {
		return nil, err
	}
	if result == nil || !result.Ready {
		return nil, nil
	}
	return result.Proof, nil
}

// Prepare builds a claim proof for a rollup-initiated bridge and reports whether it is ready for
// submission. It selects the L1 info tree leaf that covers the source network's local exit root,
// gates on destination readiness, resolves (refreshing when stale) the leaf-to-LER proof, builds the
// LER-to-rollup-exit-root proof locally, and verifies both proofs before returning them.
func (p *RollupPreparer) Prepare(ctx context.Context, request types.AutoClaimRequest) (*Result, error) {
	if p.l1InfoTree == nil {
		return nil, fmt.Errorf("L1 info tree syncer is not available")
	}

	sourceNetwork := request.Bridge.SourceNetwork
	if sourceNetwork == types.L1OriginNetwork {
		return nil, fmt.Errorf("rollup preparer requires a rollup source network, got network 0")
	}

	// 1 + 2: select the L1 info tree leaf and gate on destination readiness.
	finalIndex, ready, err := p.selectRollupLeafIndex(ctx, request)
	if err != nil {
		return nil, err
	}
	if !ready {
		return &Result{Ready: false}, nil
	}

	info, err := p.l1InfoTree.GetInfoByIndex(ctx, finalIndex)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
			return &Result{Ready: false}, nil
		}
		return nil, fmt.Errorf("get L1 info tree leaf at index %d: %w", finalIndex, err)
	}
	if info == nil {
		return nil, fmt.Errorf("get L1 info tree leaf at index %d: empty result", finalIndex)
	}
	if !claimableL1InfoTreeLeaf(info) {
		return &Result{Ready: false}, nil
	}

	// 3: resolve the source network's LER at the chosen leaf and detect staleness.
	actualLER, err := p.l1InfoTree.GetLocalExitRoot(ctx, sourceNetwork, info.RollupExitRoot)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
			// The chosen leaf's rollup exit root does not yet contain the source network's LER.
			return &Result{Ready: false}, nil
		}
		return nil, fmt.Errorf("get local exit root for source network %d: %w", sourceNetwork, err)
	}
	if actualLER == (common.Hash{}) {
		// Source network not yet present in this rollup exit root — not ready.
		return &Result{Ready: false}, nil
	}

	proofLocalExitRoot, ready, err := p.resolveLeafProof(ctx, request, finalIndex, actualLER)
	if err != nil {
		return nil, err
	}
	if !ready {
		return &Result{Ready: false}, nil
	}

	// 4: build the LER-to-rollup-exit-root proof locally.
	proofRollupExitRoot, err := p.l1InfoTree.GetRollupExitTreeMerkleProof(ctx, sourceNetwork, info.RollupExitRoot)
	if err != nil {
		return nil, fmt.Errorf("get rollup exit root proof: %w", err)
	}

	// 5: verify both proofs locally before persisting; a mismatch is a hard error, not a retry.
	if err := p.verifyProofs(request, info, actualLER, proofLocalExitRoot, proofRollupExitRoot); err != nil {
		return nil, err
	}

	return &Result{
		Ready: true,
		Proof: &types.ClaimProof{
			L1InfoTreeIndex:     finalIndex,
			L1InfoTreeLeaf:      info,
			MainnetExitRoot:     info.MainnetExitRoot,
			RollupExitRoot:      info.RollupExitRoot,
			GlobalExitRoot:      info.GlobalExitRoot,
			ProofLocalExitRoot:  proofLocalExitRoot,
			ProofRollupExitRoot: proofRollupExitRoot,
			ABILocalExitRoot:    types.ProofToABIProof(proofLocalExitRoot),
			ABIRollupExitRoot:   types.ProofToABIProof(proofRollupExitRoot),
			PreparedAt:          p.now(),
		},
	}, nil
}

// selectRollupLeafIndex selects the final L1 info tree leaf index to build the claim against.
// When the request carries a preset index it is used as-is (destination readiness was established in a
// prior cycle). Otherwise it walks forward from the first leaf at/after the verify block to the first
// leaf whose rollup exit root covers the source network's LER, then applies the destination readiness
// gate: for an L2 destination the returned index is the first injected GER at/after that leaf; for an
// L1 destination (gerSyncer nil) the covering leaf itself is used.
func (p *RollupPreparer) selectRollupLeafIndex(
	ctx context.Context, request types.AutoClaimRequest,
) (uint32, bool, error) {
	if request.L1InfoTreeIndex != nil {
		return *request.L1InfoTreeIndex, true, nil
	}

	coveringIndex, ready, err := p.firstCoveringLeafIndex(ctx, request)
	if err != nil || !ready {
		return 0, ready, err
	}

	if p.gerSyncer == nil {
		// L1 destination: no GER injection to wait for; the covering leaf is sufficient.
		return coveringIndex, true, nil
	}

	gerInfo, err := p.gerSyncer.GetFirstGERAfterL1InfoTreeIndex(ctx, coveringIndex)
	if errors.Is(err, db.ErrNotFound) {
		log.Debugf("autoclaim rollup proof: source=%d deposit=%d: no injected GER with l1InfoTreeIndex>=%d yet",
			request.Bridge.SourceNetwork, request.Bridge.DepositCount, coveringIndex)
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("get first injected GER after index %d: %w", coveringIndex, err)
	}

	return gerInfo.L1InfoTreeIndex, true, nil
}

// firstCoveringLeafIndex returns the index of the first L1 info tree leaf, at or after the request's
// verify block, whose rollup exit root contains a LER of the source network that covers the bridge
// (i.e. equal to the stored LER or a later LER of that network). Because the rollup exit tree is
// append-only, any leaf strictly after the verify block carries the stored LER or a newer one, which
// still covers the bridge. Same-block leaves that carry a different, non-stored LER are ambiguous
// (they may predate the verify within the block), so the selector advances past them.
func (p *RollupPreparer) firstCoveringLeafIndex(
	ctx context.Context, request types.AutoClaimRequest,
) (uint32, bool, error) {
	sourceNetwork := request.Bridge.SourceNetwork

	candidate, err := p.l1InfoTree.GetFirstInfoAfterBlock(request.VerifyBlockNum)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("get first L1 info tree leaf after block %d: %w", request.VerifyBlockNum, err)
	}

	for advances := 0; advances <= maxLeafSelectionAdvances; advances++ {
		if candidate == nil {
			return 0, false, nil
		}

		actualLER, lerErr := p.l1InfoTree.GetLocalExitRoot(ctx, sourceNetwork, candidate.RollupExitRoot)
		switch {
		case errors.Is(lerErr, db.ErrNotFound) || errors.Is(lerErr, sql.ErrNoRows):
			// Rollup exit root not synced into the rollup exit tree yet — advance.
		case lerErr != nil:
			return 0, false, fmt.Errorf("get local exit root for source network %d: %w", sourceNetwork, lerErr)
		case actualLER == request.LER:
			// Exact match: the stored leaf proof applies directly.
			return candidate.L1InfoTreeIndex, true, nil
		case actualLER == (common.Hash{}):
			// Source network not yet verified as of this leaf's rollup exit root — advance.
		case candidate.BlockNumber > request.VerifyBlockNum:
			// Strictly after the verify block: append-only guarantees this LER is newer than the
			// stored one and still covers the bridge. Use it (staleness handled downstream).
			return candidate.L1InfoTreeIndex, true, nil
		default:
			// Same block as the verify and a different, non-stored LER: it may predate the verify
			// within the block, so it is not safe to use. Advance to the next leaf.
		}

		next, nextErr := p.l1InfoTree.GetInfoByIndex(ctx, candidate.L1InfoTreeIndex+1)
		if nextErr != nil {
			if errors.Is(nextErr, db.ErrNotFound) || errors.Is(nextErr, sql.ErrNoRows) {
				// No later leaf synced yet — retry next cycle.
				return 0, false, nil
			}
			return 0, false, fmt.Errorf("get L1 info tree leaf at index %d: %w", candidate.L1InfoTreeIndex+1, nextErr)
		}
		candidate = next
	}

	// Exhausted the advance budget without finding a covering leaf — retry next cycle.
	log.Debugf("autoclaim rollup proof: source=%d deposit=%d: no covering L1 info leaf within %d advances",
		sourceNetwork, request.Bridge.DepositCount, maxLeafSelectionAdvances)
	return 0, false, nil
}

// resolveLeafProof returns the leaf-to-LER proof to use for the claim. When the LER at the chosen leaf
// matches the stored one, the stored proof is returned. Otherwise a newer LER has superseded the
// stored one (staleness): a fresh proof is fetched from the source network's bridge service and
// returned so it is persisted with the claim proof. A transient refresh failure yields ready=false
// (retry next cycle) rather than an error, so it does not consume the claim retry budget.
func (p *RollupPreparer) resolveLeafProof(
	ctx context.Context, request types.AutoClaimRequest, finalIndex uint32, actualLER common.Hash,
) (treetypes.Proof, bool, error) {
	if actualLER == request.LER {
		return request.LeafProof, true, nil
	}

	if p.refresher == nil {
		return treetypes.Proof{}, false, fmt.Errorf(
			"stored LER superseded for source network %d but no leaf proof refresher is configured",
			request.Bridge.SourceNetwork)
	}

	refreshed, err := p.refresher.RefreshLeafProof(
		ctx, request.Bridge.SourceNetwork, finalIndex, request.Bridge.DepositCount)
	if err != nil {
		// Transient failure (source not synced yet, network error): retry next cycle without
		// burning the claim retry budget.
		log.Debugf("autoclaim rollup proof: source=%d deposit=%d: leaf proof refresh not ready: %v",
			request.Bridge.SourceNetwork, request.Bridge.DepositCount, err)
		return treetypes.Proof{}, false, nil
	}

	return refreshed, true, nil
}

// verifyProofs validates both Merkle proofs locally against the chosen leaf before the claim proof is
// persisted. It confirms the bridge leaf is included in the source network's LER, and that the LER is
// included in the leaf's rollup exit root at the source network's position. A mismatch is a hard error.
func (p *RollupPreparer) verifyProofs(
	request types.AutoClaimRequest,
	info *l1infotreesync.L1InfoTreeLeaf,
	actualLER common.Hash,
	proofLocalExitRoot, proofRollupExitRoot treetypes.Proof,
) error {
	bridgeLeafHash := bridgeExitLeafHash(request.Bridge)
	if err := tree.VerifyProof(
		bridgeLeafHash, proofLocalExitRoot, request.Bridge.DepositCount, actualLER,
	); err != nil {
		return fmt.Errorf("verify leaf-to-LER proof for source network %d deposit %d: %w",
			request.Bridge.SourceNetwork, request.Bridge.DepositCount, err)
	}

	if err := tree.VerifyProof(
		actualLER, proofRollupExitRoot, request.Bridge.SourceNetwork-1, info.RollupExitRoot,
	); err != nil {
		return fmt.Errorf("verify LER-to-rollup-exit-root proof for source network %d: %w",
			request.Bridge.SourceNetwork, err)
	}

	return nil
}

// bridgeExitLeafHash computes the bridge exit tree leaf hash for a BridgeExit, matching the encoding
// used by bridgesync.Bridge.Hash so the leaf-to-LER proof can be verified.
func bridgeExitLeafHash(bridge types.BridgeExit) common.Hash {
	b := bridgesync.Bridge{
		LeafType:           uint8(bridge.LeafType),
		OriginNetwork:      bridge.OriginNetwork,
		OriginAddress:      bridge.OriginAddress,
		DestinationNetwork: bridge.DestinationNetwork,
		DestinationAddress: bridge.DestinationAddress,
		Amount:             bridge.Amount,
		Metadata:           bridge.Metadata,
	}
	return b.Hash()
}

// Compile-time assertion: RollupPreparer implements types.ProofPreparer.
var _ types.ProofPreparer = (*RollupPreparer)(nil)
