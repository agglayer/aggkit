// Package proofbuilder assembles an AggLayerClaim from local syncer data (no remote HTTP calls).
package proofbuilder

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"

	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
)

// ErrNotReady is returned when local state doesn't yet have the needed data (caller should retry).
var ErrNotReady = errors.New("proofbuilder: local state not yet sufficient")

// BridgeSyncer is the narrow interface the ProofBuilder needs from bridgesync.
type BridgeSyncer interface {
	GetBridgeByDepositCount(ctx context.Context, depositCount uint32) (*bridgesync.Bridge, error)
	GetProof(ctx context.Context, depositCount uint32, localExitRoot common.Hash) (treetypes.Proof, error)
	GetExitRootByIndex(ctx context.Context, index uint32) (treetypes.Root, error)
}

// L1InfoTreer is the narrow interface the ProofBuilder needs from l1infotreesync.
type L1InfoTreer interface {
	GetLastInfo() (*l1infotreesync.L1InfoTreeLeaf, error)
	GetLocalExitRoot(ctx context.Context, networkID uint32, rollupExitRoot common.Hash) (common.Hash, error)
	GetRollupExitTreeMerkleProof(ctx context.Context, networkID uint32, root common.Hash) (treetypes.Proof, error)
}

// AggLayerClaim mirrors the Solidity AggLayerClaim struct field-for-field.
type AggLayerClaim struct {
	SMTProofLocalExitRoot  [32][32]byte
	SMTProofRollupExitRoot [32][32]byte
	GlobalIndex            *big.Int
	MainnetExitRoot        [32]byte
	RollupExitRoot         [32]byte
	OriginNetwork          uint32
	OriginTokenAddress     common.Address
	DestinationNetwork     uint32
	DestinationAddress     common.Address
	Amount                 *big.Int
	Metadata               []byte
}

// ProofBuilder assembles an AggLayerClaim from local syncer data.
type ProofBuilder struct {
	bridgeSyncer BridgeSyncer
	l1InfoTree   L1InfoTreer
	networkID    uint32
	logger       *log.Logger
}

// New creates a ProofBuilder.
// networkID is the AggLayer network ID of the chain whose bridgeSyncer is provided (0 = mainnet, >0 = rollup).
// If logger is nil the package-level default logger is used.
func New(bridgeSyncer BridgeSyncer, l1InfoTree L1InfoTreer, networkID uint32, logger *log.Logger) *ProofBuilder {
	if logger == nil {
		logger = log.GetDefaultLogger()
	}
	return &ProofBuilder{
		bridgeSyncer: bridgeSyncer,
		l1InfoTree:   l1InfoTree,
		networkID:    networkID,
		logger:       logger,
	}
}

// Build returns a complete AggLayerClaim or ErrNotReady when local state lacks the needed data.
//
// Steps:
//  1. Decode globalIndex → (originNetwork, depositCount).
//  2. Get BridgeEvent from bridgeSyncer for depositCount.
//  3. Get the latest L1InfoTreeLeaf to obtain MainnetExitRoot + RollupExitRoot.
//  4. Derive localExitRoot for the origin network and get SMT proof for the leaf.
//  5. Get SMT proof from rollup exit tree for the origin network.
func (pb *ProofBuilder) Build(ctx context.Context, globalIndex *big.Int) (AggLayerClaim, error) {
	originNetwork, depositCount, ok := decodeGlobalIndex(globalIndex)
	if !ok {
		return AggLayerClaim{}, fmt.Errorf("proofbuilder: %w: globalIndex is nil", ErrNotReady)
	}

	bridge, err := pb.bridgeSyncer.GetBridgeByDepositCount(ctx, depositCount)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			pb.logger.Debugw("proofbuilder: bridge not found yet", "depositCount", depositCount)
			return AggLayerClaim{}, fmt.Errorf("proofbuilder: %w: bridge depositCount=%d not found", ErrNotReady, depositCount)
		}
		return AggLayerClaim{}, fmt.Errorf("proofbuilder: get bridge depositCount=%d: %w", depositCount, err)
	}

	info, err := pb.l1InfoTree.GetLastInfo()
	if err != nil {
		if errors.Is(err, db.ErrNotFound) || errors.Is(err, l1infotreesync.ErrNotFound) {
			pb.logger.Debugw("proofbuilder: l1infotreesync not yet synced")
			return AggLayerClaim{}, fmt.Errorf("proofbuilder: %w: l1infotreesync has no data yet", ErrNotReady)
		}
		return AggLayerClaim{}, fmt.Errorf("proofbuilder: get last l1 info: %w", err)
	}

	// Derive the local exit root for the origin network, then build the SMT proof for the leaf.
	localExitRoot, err := pb.localExitRootFor(ctx, originNetwork, info)
	if err != nil {
		if errors.Is(err, ErrNotReady) {
			return AggLayerClaim{}, err
		}
		return AggLayerClaim{}, fmt.Errorf("proofbuilder: local exit root for network=%d: %w", originNetwork, err)
	}

	proofLocal, err := pb.bridgeSyncer.GetProof(ctx, depositCount, localExitRoot)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return AggLayerClaim{}, fmt.Errorf("proofbuilder: %w: local exit proof not found", ErrNotReady)
		}
		return AggLayerClaim{}, fmt.Errorf("proofbuilder: get local exit proof: %w", err)
	}

	// For mainnet (networkID==0) the rollup exit proof is an empty proof (bridgeservice convention).
	// For rollups, get the SMT proof from the rollup exit tree.
	proofRollup, err := pb.l1InfoTree.GetRollupExitTreeMerkleProof(ctx, originNetwork, info.RollupExitRoot)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) || errors.Is(err, l1infotreesync.ErrNotFound) {
			return AggLayerClaim{}, fmt.Errorf("proofbuilder: %w: rollup exit proof not found", ErrNotReady)
		}
		return AggLayerClaim{}, fmt.Errorf("proofbuilder: get rollup exit proof: %w", err)
	}

	amount := bridge.Amount
	if amount == nil {
		amount = new(big.Int)
	}

	claim := AggLayerClaim{
		SMTProofLocalExitRoot:  proofToArray(proofLocal),
		SMTProofRollupExitRoot: proofToArray(proofRollup),
		GlobalIndex:            new(big.Int).Set(globalIndex),
		MainnetExitRoot:        info.MainnetExitRoot,
		RollupExitRoot:         info.RollupExitRoot,
		OriginNetwork:          bridge.OriginNetwork,
		OriginTokenAddress:     bridge.OriginAddress,
		DestinationNetwork:     bridge.DestinationNetwork,
		DestinationAddress:     bridge.DestinationAddress,
		Amount:                 amount,
		Metadata:               bridge.Metadata,
	}
	pb.logger.Debugw("proofbuilder: claim built",
		"globalIndex", globalIndex.String(),
		"originNetwork", originNetwork,
		"depositCount", depositCount,
	)
	return claim, nil
}

// localExitRootFor returns the local exit root for the origin network at the given L1InfoTreeLeaf.
// For mainnet (networkID==0) it returns info.MainnetExitRoot directly.
// For rollups it queries the rollup exit tree.
func (pb *ProofBuilder) localExitRootFor(
	ctx context.Context,
	originNetwork uint32,
	info *l1infotreesync.L1InfoTreeLeaf,
) (common.Hash, error) {
	if originNetwork == 0 {
		return info.MainnetExitRoot, nil
	}
	ler, err := pb.l1InfoTree.GetLocalExitRoot(ctx, originNetwork, info.RollupExitRoot)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) || errors.Is(err, l1infotreesync.ErrNotFound) {
			return common.Hash{}, fmt.Errorf("proofbuilder: %w: local exit root for network=%d not found", ErrNotReady, originNetwork)
		}
		return common.Hash{}, err
	}
	return ler, nil
}

// decodeGlobalIndex decodes a globalIndex into (originNetwork, depositCount).
// This mirrors correlator.decodeGlobalIndex exactly (kept package-private to avoid import cycles).
//
// Bit layout (from bridgesync/processor.go):
//
//	bit 64 = mainnet flag
//	bits 63-32 = rollup index (zero when mainnet flag set)
//	bits 31-0  = deposit count (local leaf index)
func decodeGlobalIndex(globalIndex *big.Int) (originNetwork uint32, depositCount uint32, ok bool) {
	if globalIndex == nil {
		return 0, 0, false
	}
	mainnetFlagMask := new(big.Int).Lsh(big.NewInt(1), 64)
	if new(big.Int).And(globalIndex, mainnetFlagMask).Sign() != 0 {
		depositCount = uint32(globalIndex.Uint64())
		return 0, depositCount, true
	}
	upper := new(big.Int).Rsh(globalIndex, 32)
	rollupIndex := uint32(upper.Uint64())
	depositCount = uint32(globalIndex.Uint64())
	return rollupIndex + 1, depositCount, true
}

// proofToArray converts a tree.Proof ([32]common.Hash) to [32][32]byte (Solidity layout).
func proofToArray(p treetypes.Proof) [32][32]byte {
	var out [32][32]byte
	for i, h := range p {
		out[i] = h
	}
	return out
}
