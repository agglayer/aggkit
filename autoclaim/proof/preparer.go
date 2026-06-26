package proof

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/agglayer/aggkit/autoclaim/types"
	"github.com/agglayer/aggkit/bridgeservice"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/l2gersync"
	"github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

const binarySearchDivider = 2

// L1BridgeSyncer exposes the L1 bridge sync methods needed to prepare claim proofs.
type L1BridgeSyncer interface {
	GetProof(ctx context.Context, depositCount uint32, localExitRoot common.Hash) (treetypes.Proof, error)
	GetRootByLER(ctx context.Context, ler common.Hash) (*treetypes.Root, error)
	GetLastRoot(ctx context.Context) (*treetypes.Root, error)
}

// L1InfoTreeSyncer exposes the L1 info tree methods needed to prepare L1-origin claim proofs.
type L1InfoTreeSyncer interface {
	GetInfoByIndex(ctx context.Context, index uint32) (*l1infotreesync.L1InfoTreeLeaf, error)
	GetRollupExitTreeMerkleProof(ctx context.Context, networkID uint32, root common.Hash) (treetypes.Proof, error)
	GetLastInfo() (*l1infotreesync.L1InfoTreeLeaf, error)
	GetFirstInfo() (*l1infotreesync.L1InfoTreeLeaf, error)
	GetFirstInfoAfterBlock(blockNum uint64) (*l1infotreesync.L1InfoTreeLeaf, error)
}

// L2GERSyncer exposes the destination-L2 GER injection state needed to gate proof readiness.
// It is satisfied by the per-claimer l2gersync.L2GERSync instance, which correctly tracks injected
// GERs on both legacy (GlobalExitRootMap polling) and sovereign (event-based) L2 GER managers.
type L2GERSyncer interface {
	GetFirstGERAfterL1InfoTreeIndex(
		ctx context.Context, atOrAfterL1InfoTreeIndex uint32,
	) (l2gersync.GlobalExitRootInfo, error)
}

// Preparer prepares bridge claim proof inputs for Auto Claim.
type Preparer struct {
	bridgeL1   L1BridgeSyncer
	l1InfoTree L1InfoTreeSyncer
	gerSyncer  L2GERSyncer
	now        func() time.Time
}

// Result is the proof preparation outcome. Ready=false means the request should remain pending.
type Result struct {
	Ready bool
	Proof *types.ClaimProof
}

// NewPreparer creates an L1-origin proof preparer.
func NewPreparer(bridgeL1 L1BridgeSyncer, l1InfoTree L1InfoTreeSyncer, gerSyncer L2GERSyncer) *Preparer {
	return &Preparer{
		bridgeL1:   bridgeL1,
		l1InfoTree: l1InfoTree,
		gerSyncer:  gerSyncer,
		now:        time.Now,
	}
}

// PrepareProof implements types.ProofPreparer. It returns nil, nil when the bridge is not ready yet.
func (p *Preparer) PrepareProof(ctx context.Context, request types.AutoClaimRequest) (*types.ClaimProof, error) {
	result, err := p.Prepare(ctx, request)
	if err != nil {
		return nil, err
	}
	if result == nil || !result.Ready {
		return nil, nil
	}
	return result.Proof, nil
}

// Prepare prepares a claim proof for an L1-initiated bridge and reports whether it is ready for
// submission. All requests in the current scope originate on L1, so the bridged token's origin
// network (request.Bridge.OriginNetwork) is not constrained here.
func (p *Preparer) Prepare(ctx context.Context, request types.AutoClaimRequest) (*Result, error) {
	if p.bridgeL1 == nil {
		return nil, fmt.Errorf("L1 bridge syncer is not available")
	}
	if p.l1InfoTree == nil {
		return nil, fmt.Errorf("L1 info tree syncer is not available")
	}

	bridge := request.Bridge
	if request.L1InfoTreeIndex != nil {
		l1InfoTreeIndex := *request.L1InfoTreeIndex
		bridge.L1InfoTreeIndex = &l1InfoTreeIndex
	}

	l1InfoTreeIndex, ready, err := p.selectL1InfoTreeIndex(ctx, bridge)
	if err != nil {
		return nil, err
	}
	if !ready {
		return &Result{Ready: false}, nil
	}

	info, err := p.l1InfoTree.GetInfoByIndex(ctx, l1InfoTreeIndex)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
			return &Result{Ready: false}, nil
		}
		return nil, fmt.Errorf("get L1 info tree leaf at index %d: %w", l1InfoTreeIndex, err)
	}
	if info == nil {
		return nil, fmt.Errorf("get L1 info tree leaf at index %d: empty result", l1InfoTreeIndex)
	}
	if !claimableL1InfoTreeLeaf(info) {
		return &Result{Ready: false}, nil
	}
	if info.BlockNumber < request.Bridge.BlockNum {
		return &Result{Ready: false}, nil
	}

	proofLocalExitRoot, err := p.bridgeL1.GetProof(ctx, request.Bridge.DepositCount, info.MainnetExitRoot)
	if err != nil {
		return nil, fmt.Errorf("get L1 local exit root proof: %w", err)
	}

	proofRollupExitRoot, err := p.l1InfoTree.GetRollupExitTreeMerkleProof(
		ctx,
		types.L1OriginNetwork,
		info.RollupExitRoot,
	)
	if err != nil {
		return nil, fmt.Errorf("get rollup exit root proof: %w", err)
	}

	return &Result{
		Ready: true,
		Proof: &types.ClaimProof{
			L1InfoTreeIndex:     l1InfoTreeIndex,
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

// selectL1InfoTreeIndex chooses the L1 info tree leaf index to use for a bridge claim.
// When the bridge detector supplied a preset index, that exact index is used.
// Otherwise the selector computes the bridge inclusion index and, if a gerSyncer is available,
// waits until an injected GER on the destination L2 covers that index. It then returns the first
// (smallest) injected GER leaf index at or after the bridge index — the tightest valid leaf.
func (p *Preparer) selectL1InfoTreeIndex(
	ctx context.Context,
	bridge types.BridgeExit,
) (uint32, bool, error) {
	if bridge.L1InfoTreeIndex != nil {
		return *bridge.L1InfoTreeIndex, true, nil
	}

	bridgeL1InfoTreeIndex, err := p.firstL1InfoTreeIndexForL1Bridge(
		ctx,
		bridge.DepositCount,
		bridge.BlockNum,
	)
	if errors.Is(err, bridgeservice.ErrNotOnL1Info) {
		log.Debugf("autoclaim proof: deposit=%d block=%d: l1InfoTree not yet past bridge block (ErrNotOnL1Info)",
			bridge.DepositCount, bridge.BlockNum)
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("get first L1 info tree index for L1 bridge: %w", err)
	}

	log.Debugf("autoclaim proof: deposit=%d block=%d: bridgeL1InfoTreeIndex=%d",
		bridge.DepositCount, bridge.BlockNum, bridgeL1InfoTreeIndex)

	if p.gerSyncer == nil {
		return bridgeL1InfoTreeIndex, true, nil
	}

	gerInfo, err := p.gerSyncer.GetFirstGERAfterL1InfoTreeIndex(ctx, bridgeL1InfoTreeIndex)
	if errors.Is(err, db.ErrNotFound) {
		// No injected GER on the target L2 covers this bridge yet — not ready.
		log.Debugf("autoclaim proof: deposit=%d: gerSyncer has no GER with l1InfoTreeIndex>=%d yet",
			bridge.DepositCount, bridgeL1InfoTreeIndex)
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("get first injected GER after index %d: %w", bridgeL1InfoTreeIndex, err)
	}

	return gerInfo.L1InfoTreeIndex, true, nil
}

func (p *Preparer) firstL1InfoTreeIndexForL1Bridge(
	ctx context.Context,
	depositCount uint32,
	bridgeBlockNum uint64,
) (uint32, error) {
	lastInfo, err := p.l1InfoTree.GetLastInfo()
	if err != nil {
		return 0, err
	}
	if lastInfo == nil {
		return 0, fmt.Errorf("last L1 info tree leaf is empty")
	}
	if lastInfo.BlockNumber < bridgeBlockNum {
		return 0, bridgeservice.ErrNotOnL1Info
	}

	root, err := p.bridgeL1.GetRootByLER(ctx, lastInfo.MainnetExitRoot)
	if err != nil {
		root, err = p.bridgeL1.GetLastRoot(ctx)
		if err != nil {
			return 0, fmt.Errorf("failed to get last root for L1: %w", err)
		}
		if root == nil {
			return 0, fmt.Errorf("failed to get last root for L1: empty result")
		}
		lastInfo, err = p.l1InfoTree.GetInfoByIndex(ctx, root.Index)
		if err != nil {
			return 0, fmt.Errorf("failed to get last info for L1: %w", err)
		}
		if lastInfo == nil {
			return 0, fmt.Errorf("failed to get last info for L1: empty result")
		}
	}
	if root == nil {
		return 0, fmt.Errorf("root for L1 mainnet exit root is empty")
	}
	if root.Index < depositCount {
		return 0, bridgeservice.ErrNotOnL1Info
	}

	firstInfo, err := p.l1InfoTree.GetFirstInfo()
	if err != nil {
		return 0, err
	}
	if firstInfo == nil {
		return 0, fmt.Errorf("first L1 info tree leaf is empty")
	}

	bestResult := lastInfo
	lowerLimit := firstInfo.BlockNumber
	upperLimit := lastInfo.BlockNumber
	for lowerLimit <= upperLimit {
		targetBlock := lowerLimit + ((upperLimit - lowerLimit) / binarySearchDivider)
		targetInfo, err := p.l1InfoTree.GetFirstInfoAfterBlock(targetBlock)
		if err != nil {
			return 0, err
		}
		if targetInfo == nil {
			return 0, fmt.Errorf("first L1 info tree leaf after block %d is empty", targetBlock)
		}
		root, err := p.bridgeL1.GetRootByLER(ctx, targetInfo.MainnetExitRoot)
		if err != nil {
			return 0, err
		}
		if root == nil {
			return 0, fmt.Errorf("root for L1 mainnet exit root at block %d is empty", targetBlock)
		}
		switch {
		case root.Index < depositCount:
			lowerLimit = targetBlock + 1
		case root.Index == depositCount:
			bestResult = targetInfo
			return bestResult.L1InfoTreeIndex, nil
		default:
			bestResult = targetInfo
			if targetBlock == 0 {
				return bestResult.L1InfoTreeIndex, nil
			}
			upperLimit = targetBlock - 1
		}
	}

	return bestResult.L1InfoTreeIndex, nil
}

func claimableL1InfoTreeLeaf(info *l1infotreesync.L1InfoTreeLeaf) bool {
	if info == nil {
		return false
	}
	return info.MainnetExitRoot != (common.Hash{}) && info.GlobalExitRoot != (common.Hash{})
}
