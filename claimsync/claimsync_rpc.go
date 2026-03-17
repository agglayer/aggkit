package claimsync

import (
	"context"
	"fmt"
	"math/big"

	jRPC "github.com/0xPolygon/cdk-rpc/rpc"
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
)

// ClaimSyncer is the interface required by ClaimSyncRPC.
type ClaimSyncer interface {
	GetLastProcessedBlock(ctx context.Context) (uint64, bool, error)
	GetClaims(ctx context.Context, fromBlock, toBlock uint64) ([]claimsynctypes.Claim, error)
	GetClaimsByGlobalIndex(ctx context.Context, globalIndex *big.Int) ([]claimsynctypes.Claim, error)
	SetNextRequiredBlock(ctx context.Context, blockNum uint64) error
}

// ClaimSyncRPC is the RPC interface for the ClaimSync component.
type ClaimSyncRPC struct {
	logger    aggkitcommon.Logger
	claimSync ClaimSyncer
}

// NewClaimSyncRPC creates a new ClaimSyncRPC.
func NewClaimSyncRPC(logger aggkitcommon.Logger, claimSync ClaimSyncer) *ClaimSyncRPC {
	return &ClaimSyncRPC{
		logger:    logger,
		claimSync: claimSync,
	}
}

// Status returns the sync status of the ClaimSync component.
// curl -X POST http://localhost:5576/ -H "Content-Type: application/json" \
// -d '{"method":"l2claimsync_status", "params":[], "id":1}'
func (r *ClaimSyncRPC) Status() (interface{}, jRPC.Error) {
	lastBlock, _, err := r.claimSync.GetLastProcessedBlock(context.Background())
	if err != nil {
		return nil, jRPC.NewRPCError(jRPC.DefaultErrorCode,
			"ClaimSyncRPC.Status: getting last processed block: %v", err)
	}
	info := struct {
		Status             string `json:"status"`
		LastProcessedBlock uint64 `json:"lastProcessedBlock"`
	}{
		Status:             "running",
		LastProcessedBlock: lastBlock,
	}
	return info, nil
}

// GetClaims returns claims indexed between fromBlock and toBlock.
// curl -X POST http://localhost:5576/ -H "Content-Type: application/json" \
// -d '{"method":"l2claimsync_getClaims", "params":[0, 1000], "id":1}'
func (r *ClaimSyncRPC) GetClaims(fromBlock, toBlock uint64) (interface{}, jRPC.Error) {
	r.logger.Infof("RPC call: lclaimsync_getClaims(%d, %d)", fromBlock, toBlock)
	claims, err := r.claimSync.GetClaims(context.Background(), fromBlock, toBlock)
	if err != nil {
		return nil, jRPC.NewRPCError(jRPC.DefaultErrorCode,
			fmt.Sprintf("ClaimSyncRPC.GetClaims: %v", err))
	}
	return claims, nil
}

// GetClaimsByGlobalIndex returns claims for the given global index.
// curl -X POST http://localhost:5576/ -H "Content-Type: application/json" \
// -d '{"method":"l2claimsync_getClaimsByGlobalIndex", "params":["123"], "id":1}'
func (r *ClaimSyncRPC) GetClaimsByGlobalIndex(globalIndexStr string) (interface{}, jRPC.Error) {
	r.logger.Infof("RPC call: lclaimsync_getClaimsByGlobalIndex(%s)", globalIndexStr)
	globalIndex := new(big.Int)
	const decimalBase = 10
	if _, ok := globalIndex.SetString(globalIndexStr, decimalBase); !ok {
		return nil, jRPC.NewRPCError(jRPC.DefaultErrorCode,
			"ClaimSyncRPC.GetClaimsByGlobalIndex: invalid global index: %s", globalIndexStr)
	}
	claims, err := r.claimSync.GetClaimsByGlobalIndex(context.Background(), globalIndex)
	if err != nil {
		return nil, jRPC.NewRPCError(jRPC.DefaultErrorCode,
			fmt.Sprintf("ClaimSyncRPC.GetClaimsByGlobalIndex: %v", err))
	}
	if len(claims) == 0 {
		return nil, jRPC.NewRPCError(jRPC.NotFoundErrorCode,
			"no claims found for global index %s", globalIndexStr)
	}
	return claims, nil
}

// SetNextRequiredBlock sets the next block number that the synchronizer must process.
// curl -X POST http://localhost:5576/ -H "Content-Type: application/json" \
// -d '{"method":"l2claimsync_setNextRequiredBlock", "params":[1000], "id":1}'
func (r *ClaimSyncRPC) SetNextRequiredBlock(blockNum uint64) (interface{}, jRPC.Error) {
	r.logger.Infof("RPC call: lclaimsync_setNextRequiredBlock(%d)", blockNum)
	if err := r.claimSync.SetNextRequiredBlock(context.Background(), blockNum); err != nil {
		return nil, jRPC.NewRPCError(jRPC.DefaultErrorCode,
			fmt.Sprintf("ClaimSyncRPC.SetNextRequiredBlock: %s", err.Error()))
	}
	return struct {
		Message string `json:"message"`
	}{
		Message: fmt.Sprintf("next required block set to %d", blockNum),
	}, nil
}

// GetRPCServices returns the RPC services exposed by ClaimSync.
func (c *ClaimSync) GetRPCServices() []jRPC.Service {
	name := "l1claimsync"
	if c.syncerID == claimsynctypes.L2ClaimSyncer {
		name = "l2claimsync"
	}
	logger := log.WithFields("module", name+"-rpc")
	return []jRPC.Service{
		{
			Name:    name,
			Service: NewClaimSyncRPC(logger, c),
		},
	}
}
