package simulator

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/autoclaim/claimtx"
	"github.com/agglayer/aggkit/autoclaim/policy"
	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
)

const nestedBridgeDetectionSkipped = "skipped"

// GasEstimator is the narrow destination RPC surface required for claim simulation.
type GasEstimator interface {
	EstimateGas(ctx context.Context, msg ethereum.CallMsg) (uint64, error)
}

// Simulator estimates target-chain claim gas through normal JSON-RPC.
type Simulator struct {
	client GasEstimator
	target autoclaimtypes.ClaimerTarget
	from   common.Address
}

// New creates a target-chain claim simulator.
func New(
	client GasEstimator,
	proofPreparer autoclaimtypes.ProofPreparer,
	target autoclaimtypes.ClaimerTarget,
	from common.Address,
) (*Simulator, error) {
	if client == nil {
		return nil, fmt.Errorf("autoclaim simulator client is nil")
	}
	if proofPreparer == nil {
		return nil, fmt.Errorf("autoclaim simulator proof preparer is nil")
	}
	if target.BridgeAddr == (common.Address{}) {
		return nil, fmt.Errorf("autoclaim simulator target bridge address is empty")
	}
	if from == (common.Address{}) {
		return nil, fmt.Errorf("autoclaim simulator sender address is empty")
	}

	return &Simulator{
		client: client,
		target: target,
		from:   from,
	}, nil
}

// SimulateClaim estimates gas for a request with an already prepared and stored proof.
func (s *Simulator) SimulateClaim(
	ctx context.Context,
	request autoclaimtypes.AutoClaimRequest,
) (*policy.SimulationResult, error) {
	if request.Proof == nil {
		return nil, fmt.Errorf("prepared proof is required")
	}
	switch request.Bridge.LeafType {
	case bridgesynctypes.LeafTypeAsset, bridgesynctypes.LeafTypeMessage:
	default:
		return nil, fmt.Errorf("unsupported bridge leaf type %d", request.Bridge.LeafType.Uint8())
	}

	data, err := claimtx.PackClaim(request, *request.Proof)
	if err != nil {
		return nil, err
	}

	gas, err := s.client.EstimateGas(ctx, ethereum.CallMsg{
		From:  s.from,
		To:    &s.target.BridgeAddr,
		Value: common.Big0,
		Data:  data,
	})
	if err != nil {
		return nil, fmt.Errorf("estimate claim gas: %w", err)
	}

	return &policy.SimulationResult{
		GasUsed:          gas,
		NestedBridgeCall: policy.NestedBridgeCallNotDetected,
		Metadata: map[string]string{
			"nested_bridge_detection": nestedBridgeDetectionSkipped,
		},
	}, nil
}
