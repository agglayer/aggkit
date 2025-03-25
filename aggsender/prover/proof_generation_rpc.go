package prover

import (
	"context"

	"github.com/0xPolygon/cdk-rpc/rpc"
)

// AggsenderRPC is the RPC interface for the aggsender
type AggchainProofGenerationToolRPC struct {
	tool AggchainProofGeneration
}

// NewAggchainProofGenerationToolRPC creates a new AggchainProofGenerationToolRPC
func NewAggchainProofGenerationToolRPC(
	tool AggchainProofGeneration) *AggchainProofGenerationToolRPC {
	return &AggchainProofGenerationToolRPC{
		tool: tool,
	}
}

// GenerateAggchainProof generates an Aggchain proof
// curl -X POST http://localhost:5576/ -H "Content-Type: application/json" \
// -d '{"method":"aggkit_generateAggchainProof", "params":[fromBlock, toBlock], "id":1}'
func (a *AggchainProofGenerationToolRPC) GenerateAggchainProof(fromBlock, toBlock uint64) (interface{}, rpc.Error) {
	proof, err := a.tool.GenerateAggchainProof(context.Background(), fromBlock, toBlock)
	if err != nil {
		return nil, rpc.NewRPCError(rpc.DefaultErrorCode, err.Error())
	}

	return proof, nil
}
