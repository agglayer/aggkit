package l1infotreesync

import (
	"context"
	"fmt"

	"github.com/0xPolygon/cdk-rpc/rpc"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
)

type StatusInfo struct {
	Status               string   `json:"status"`
	CompletionPercentage *float64 `json:"completionPercentage,omitempty"`
}
type L1InfoTreeSyncer interface {
	GetInfoByGlobalExitRoot(ger common.Hash) (*L1InfoTreeLeaf, error)
	GetLatestL1InfoLeaf(ctx context.Context) (*L1InfoTreeLeaf, error)
	GetInfoByRoot(ger common.Hash) (*L1InfoTreeLeaf, error)
	GetCompletionPercentage() *float64
}

// L1InfoTreeSyncRPC is the RPC interface for the L1InfoTreeSync
type L1InfoTreeSyncRPC struct {
	logger           aggkitcommon.Logger
	l1InfoTreeSyncer L1InfoTreeSyncer
}

func NewL1InfoTreeSyncRPC(
	logger aggkitcommon.Logger,
	l1InfoTreeSyncer L1InfoTreeSyncer,
) *L1InfoTreeSyncRPC {
	return &L1InfoTreeSyncRPC{
		logger:           logger,
		l1InfoTreeSyncer: l1InfoTreeSyncer,
	}
}

// Status returns the status of the L1InfoTreeSync component
// curl -X POST http://localhost:5576/ "Content-Type: application/json" \
// -d '{"method":"l1infotreesync_status", "params":[], "id":1}'
func (b *L1InfoTreeSyncRPC) Status() (interface{}, rpc.Error) {
	info := StatusInfo{
		Status:               "running",
		CompletionPercentage: b.l1InfoTreeSyncer.GetCompletionPercentage(),
	}
	return info, nil
}

// GetInfoByGlobalExitRoot returns a leaf for the given global exit root
// if param is `nil` it returns the last leaf
// latest:
//
//	curl -X POST http://localhost:5576/ -H "Content-Type: application/json" \
//	 -d '{"method":"l1infotreesync_getInfoByGlobalExitRoot", "params":[], "id":1}'
//
// specific height:
//
// curl -X POST http://localhost:5576/ -H "Content-Type: application/json" \
// -d '{"method":"l1infotreesync_getInfoByGlobalExitRoot", "params":[$ger], "id":1}'
func (b *L1InfoTreeSyncRPC) GetInfoByGlobalExitRoot(inputGER *string) (interface{}, rpc.Error) {
	var (
		leaf *L1InfoTreeLeaf
		err  error
		ger  common.Hash
	)
	if inputGER == nil {
		b.logger.Infof("RPC call: l1infotreesync_getInfoByGlobalExitRoot(nil) getting last leaf")
		leaf, err = b.l1InfoTreeSyncer.GetLatestL1InfoLeaf(context.Background())
	} else {
		ger = common.HexToHash(*inputGER)
		b.logger.Infof("RPC call: l1infotreesync_getInfoByGlobalExitRoot %s", ger.Hex())
		leaf, err = b.l1InfoTreeSyncer.GetInfoByGlobalExitRoot(ger)
	}
	if err != nil {
		return nil, rpc.NewRPCError(rpc.DefaultErrorCode, fmt.Sprintf("error getting leaf by root: %v", err))
	}
	if leaf == nil {
		return nil, rpc.NewRPCError(rpc.NotFoundErrorCode, "leaf not found")
	}

	return leaf, nil
}

// GetInfoByRoot returns a leaf for the given root
// if param is `nil` it returns the last leaf
// latest:
//
//	curl -X POST http://localhost:5576/ -H "Content-Type: application/json" \
//	 -d '{"method":"l1infotreesync_getInfoByRoot", "params":[], "id":1}'
//
// specific height:
//
// curl -X POST http://localhost:5576/ -H "Content-Type: application/json" \
// -d '{"method":"l1infotreesync_getInfoByRoot", "params":[$root], "id":1}'
func (b *L1InfoTreeSyncRPC) GetInfoByRoot(inputRoot *string) (interface{}, rpc.Error) {
	var (
		leaf *L1InfoTreeLeaf
		err  error
	)
	if inputRoot == nil {
		b.logger.Infof("RPC call: l1infotreesync_getInfoByRoot(nil) getting last leaf")
		leaf, err = b.l1InfoTreeSyncer.GetLatestL1InfoLeaf(context.Background())
	} else {
		root := common.HexToHash(*inputRoot)
		b.logger.Infof("RPC call: l1infotreesync_getInfoByRoot(%s)", root.Hex())
		leaf, err = b.l1InfoTreeSyncer.GetInfoByRoot(root)
	}
	if err != nil {
		return nil, rpc.NewRPCError(rpc.DefaultErrorCode, fmt.Sprintf("error getting leaf by root: %v", err))
	}
	if leaf == nil {
		return nil, rpc.NewRPCError(rpc.NotFoundErrorCode, "leaf not found")
	}

	return leaf, nil
}
