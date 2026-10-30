package exit_certificate

import (
	"context"
	"fmt"
	"math/big"
	"path/filepath"
	"sort"

	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/tools/exit_certificate/bridgesyncerlite"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

// PendingBridge is an L2→L1 asset bridge emitted before the exit certificate that has not been
// claimed on L1. It is not part of the certificate's bridge_exits (it belongs to a previously
// settled certificate), but its leaf is in the local exit tree, so it is provable against the
// certificate's NewLocalExitRoot. Metadata is the raw bridge metadata (what claimAsset needs).
type PendingBridge struct {
	DepositCount       uint32         `json:"depositCount"`
	BlockNumber        uint64         `json:"blockNumber"`
	LeafType           uint8          `json:"leafType"`
	OriginNetwork      uint32         `json:"originNetwork"`
	OriginAddress      common.Address `json:"originAddress"`
	DestinationNetwork uint32         `json:"destinationNetwork"`
	DestinationAddress common.Address `json:"destinationAddress"`
	Amount             *big.Int       `json:"amount"`
	Metadata           hexutil.Bytes  `json:"metadata"`
	TxHash             common.Hash    `json:"txHash"`
}

// RunStepPending lists the L2→L1 asset bridges emitted before the exit certificate that are still
// unclaimed on L1.
//
// Approach:
//  1. Read every leaf of the local exit tree DB written by Step G2 (genesis→targetBlock bridges
//     followed by the certificate's exits). The last gResult.BridgeExitCount leaves belong to the
//     certificate and are skipped: the claimer already serves them.
//  2. Keep the assets (leaf_type=0) destined to L1 (destination_network=0). Messages need
//     claimMessage, which the claimer does not support.
//  3. Call isClaimed(depositCount, l2NetworkId) on the L1 bridge for each one and keep the unclaimed.
func RunStepPending(
	ctx context.Context, cfg *Config, dbPath string, gResult *StepGResult,
) ([]PendingBridge, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP PENDING_CLAIMS — Unclaimed pre-certificate L2→L1 bridges")
	log.Info("═══════════════════════════════════════════")

	if cfg.L1RPCURL == "" {
		return nil, fmt.Errorf("step pending_claims requires l1RpcUrl in parameters")
	}

	syncer, err := bridgesyncerlite.New(ctx, bridgesyncerlite.Config{DBPath: dbPath}, nil)
	if err != nil {
		return nil, fmt.Errorf("open local exit tree DB %q: %w", dbPath, err)
	}
	bridges, err := syncer.GetBridges(ctx)
	if closeErr := syncer.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, fmt.Errorf("read bridges from %q: %w", dbPath, err)
	}

	candidates, err := selectPendingCandidates(bridges, gResult.BridgeExitCount)
	if err != nil {
		return nil, err
	}
	log.Infof("Local exit tree: %d leaves, %d pre-certificate asset exits to L1 to check",
		len(bridges), len(candidates))

	claimedSet, err := checkClaimedOnL1(ctx, cfg, candidates)
	if err != nil {
		return nil, fmt.Errorf("check isClaimed on L1: %w", err)
	}
	pending := filterPendingBridges(candidates, claimedSet)
	log.Infof("Already claimed on L1: %d, pending: %d", len(claimedSet), len(pending))
	return pending, nil
}

// pendingDBPath is the local exit tree DB Step PENDING_CLAIMS reads: the one Step G2 wrote.
func pendingDBPath(dir string) string {
	return filepath.Join(dir, fileStepGLiteDB)
}

// selectPendingCandidates returns, ordered by deposit count, the asset bridges to L1 among the
// leaves emitted before the certificate. certificateExits is the number of leaves the certificate
// appended at the end of the tree (StepGResult.BridgeExitCount).
func selectPendingCandidates(bridges []bridgesyncerlite.BridgeLeaf, certificateExits uint64) ([]PendingBridge, error) {
	if certificateExits > uint64(len(bridges)) {
		return nil, fmt.Errorf("the certificate has %d bridge exits but the local exit tree has only %d leaves "+
			"(run step g first)", certificateExits, len(bridges))
	}
	preCertificate := uint64(len(bridges)) - certificateExits

	candidates := make([]PendingBridge, 0)
	for i := range bridges {
		b := bridges[i]
		if uint64(b.DepositCount) >= preCertificate {
			continue
		}
		if b.LeafType != 0 || b.DestinationNetwork != 0 {
			continue
		}
		candidates = append(candidates, PendingBridge{
			DepositCount:       b.DepositCount,
			BlockNumber:        b.BlockNum,
			LeafType:           b.LeafType,
			OriginNetwork:      b.OriginNetwork,
			OriginAddress:      b.OriginAddress,
			DestinationNetwork: b.DestinationNetwork,
			DestinationAddress: b.DestinationAddress,
			Amount:             b.Amount,
			Metadata:           b.Metadata,
			TxHash:             b.TxHash,
		})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].DepositCount < candidates[j].DepositCount })
	return candidates, nil
}

// checkClaimedOnL1 calls isClaimed(depositCount, l2NetworkId) on the L1 bridge for each candidate
// and returns the set of claimed deposit counts. The bridge computes
// globalIndex = leafIndex + sourceBridgeNetwork * 2^32, and a rollup's leaves are claimed with
// sourceBridgeNetwork = its network id.
func checkClaimedOnL1(
	ctx context.Context, cfg *Config, candidates []PendingBridge,
) (map[uint32]struct{}, error) {
	if len(candidates) == 0 {
		return map[uint32]struct{}{}, nil
	}

	deposits := make([]L1Deposit, len(candidates))
	calls := make([]RPCCall, len(candidates))
	for i, c := range candidates {
		deposits[i] = L1Deposit{DepositCount: c.DepositCount}
		calls[i] = RPCCall{
			Method: "eth_call",
			Params: []any{
				map[string]string{
					"to":   cfg.L1BridgeAddress.Hex(),
					"data": encodeIsClaimed(c.DepositCount, cfg.L2NetworkID),
				},
				"latest",
			},
		}
	}

	results, err := concurrentBatchRPC(
		ctx, cfg.L1RPCURL, calls, cfg.Options.RPCBatchSize, cfg.Options.ConcurrencyLimit,
		"L1 RPC/isClaimed",
	)
	if err != nil {
		return nil, fmt.Errorf("batch isClaimed: %w", err)
	}
	return parseClaimedResults(results, deposits)
}

// filterPendingBridges drops the candidates whose deposit count is in claimedSet.
func filterPendingBridges(candidates []PendingBridge, claimedSet map[uint32]struct{}) []PendingBridge {
	pending := make([]PendingBridge, 0)
	for _, c := range candidates {
		if _, claimed := claimedSet[c.DepositCount]; !claimed {
			pending = append(pending, c)
		}
	}
	return pending
}
