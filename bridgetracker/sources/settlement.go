package sources

import (
	"context"
	"errors"
	"fmt"

	"github.com/agglayer/aggkit/bridgetracker"
	trackertypes "github.com/agglayer/aggkit/bridgetracker/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

var (
	// updateL1InfoTreeSignature is the topic0 of the GlobalExitRoot contract's UpdateL1InfoTree
	// event (same signature l1infotreesync matches)
	updateL1InfoTreeSignature = crypto.Keccak256Hash([]byte("UpdateL1InfoTree(bytes32,bytes32)"))
	// updateL1InfoTreeV2Signature is the topic0 of UpdateL1InfoTreeV2, its aggchain successor
	updateL1InfoTreeV2Signature = crypto.Keccak256Hash(
		[]byte("UpdateL1InfoTreeV2(bytes32,uint32,uint256,uint64)"),
	)
	// verifyBatchesTrustedAggregatorSignature is the topic0 of the RollupManager's
	// VerifyBatchesTrustedAggregator event, emitted on every certificate settlement
	// (pessimistic/aggchain rollups included)
	verifyBatchesTrustedAggregatorSignature = crypto.Keccak256Hash(
		[]byte("VerifyBatchesTrustedAggregator(uint32,uint64,bytes32,bytes32,address)"),
	)
)

// SettlementSource implements bridgetracker.SettlementSource over L1's JSON-RPC endpoint
// (network 0): it fetches the certificate's settlement tx receipt and looks for the events
// that confirm the settlement propagated to the L1 Global Exit Root
type SettlementSource struct {
	clients EthClientResolver
	// l1Finality is the block finality the settlement tx must reach before its receipt is
	// accepted: the same reasoning as BridgeEventSource.l1Finality — a resolved bridge is
	// never re-checked (TrackingBridgeTx.IsDone), so accepting a receipt that later gets
	// reorged out would otherwise be permanent
	l1Finality aggkittypes.BlockNumberFinality
}

// NewSettlementSource returns a SettlementSource resolving the L1 (network 0) JSON-RPC client
// through clients, accepting a settlement tx's receipt only once it reaches l1Finality
func NewSettlementSource(clients EthClientResolver, l1Finality aggkittypes.BlockNumberFinality) *SettlementSource {
	return &SettlementSource{clients: clients, l1Finality: l1Finality}
}

// SettlementGERUpdate implements bridgetracker.SettlementSource: it fetches settlementTxHash's
// receipt on L1 and reports which of the three events it carries. Returns nil (not yet ready,
// retried by the engine) while the tx is not mined, has not reached l1Finality, or its receipt
// does not yet carry both mandatory events (VerifyBatchesTrustedAggregator and
// UpdateL1InfoTree — UpdateL1InfoTreeV2 is captured but not required)
func (s *SettlementSource) SettlementGERUpdate(
	ctx context.Context, _ *bridgetracker.BridgeInfo, settlementTxHash common.Hash,
) (*trackertypes.L1SettledGERResult, error) {
	client, err := s.clients.RPCClientFor(ctx, 0) // a certificate always settles on L1
	if err != nil {
		return nil, fmt.Errorf("resolving L1 JSON-RPC client: %w", err)
	}

	receipt, err := client.TransactionReceipt(ctx, settlementTxHash)
	if errors.Is(err, ethereum.NotFound) {
		return nil, nil // settlement tx not mined/visible yet
	}
	if err != nil {
		return nil, fmt.Errorf("fetching settlement tx receipt %s: %w", settlementTxHash, err)
	}

	finalized, err := client.CustomHeaderByNumber(ctx, &s.l1Finality)
	if err != nil {
		return nil, fmt.Errorf("fetching %s header for network 0: %w", s.l1Finality.String(), err)
	}
	if receipt.BlockNumber == nil || receipt.BlockNumber.Uint64() > finalized.Number {
		return nil, nil // mined, but not yet at the required finality
	}

	result := &trackertypes.L1SettledGERResult{TxHash: settlementTxHash, BlockNumber: receipt.BlockNumber.Uint64()}
	for _, l := range receipt.Logs {
		if len(l.Topics) == 0 {
			continue
		}
		switch l.Topics[0] {
		case verifyBatchesTrustedAggregatorSignature:
			result.HasVerifyBatchesTrustedAggregator = true
		case updateL1InfoTreeSignature:
			// both mainnetExitRoot and rollupExitRoot are indexed bytes32 params, so they sit
			// directly in the topics (fixed-size indexed values are not hashed, unlike dynamic
			// ones): Topics[1]/[2] are the raw values, no ABI decoding needed
			if len(l.Topics) < 3 { //nolint:mnd
				continue
			}
			mainnetExitRoot, rollupExitRoot := l.Topics[1], l.Topics[2]
			result.HasUpdateL1InfoTree = true
			result.GER = crypto.Keccak256Hash(mainnetExitRoot[:], rollupExitRoot[:])
		case updateL1InfoTreeV2Signature:
			result.HasUpdateL1InfoTreeV2 = true
			// leafCount is the only indexed param, so it sits directly in Topics[1] (a uint32
			// zero-padded to 32 bytes, same as any other indexed value below word size)
			if len(l.Topics) < 2 { //nolint:mnd
				continue
			}
			if leafCount := l.Topics[1].Big().Uint64(); leafCount > 0 {
				// V2 gives the leaf index straight away: StepWaitL1SettledGER can skip the
				// extra GER -> leaf lookup entirely (see WaitL1SettledGERResolver)
				index := uint32(leafCount - 1)
				result.L1InfoTreeIndex = &index
			}
		}
	}

	if !result.HasVerifyBatchesTrustedAggregator || !result.HasUpdateL1InfoTree {
		return nil, nil // mandatory evidence not there yet
	}
	return result, nil
}
