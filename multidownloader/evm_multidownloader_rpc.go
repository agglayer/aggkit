package multidownloader

import (
	"context"

	"github.com/0xPolygon/cdk-rpc/rpc"
	aggkitcommon "github.com/agglayer/aggkit/common"
)

type EVMMultidownloaderRPC struct {
	logger     aggkitcommon.Logger
	downloader *EVMMultidownloader
}

func NewEVMMultidownloaderRPC(
	logger aggkitcommon.Logger,
	downloader *EVMMultidownloader,
) *EVMMultidownloaderRPC {
	return &EVMMultidownloaderRPC{
		logger:     logger,
		downloader: downloader,
	}
}

// Status returns the status of the L1InfoTreeSync component
// curl -X POST http://localhost:5576/ "Content-Type: application/json" \
// -d '{"method":"multidownloader-l1_status", "params":[], "id":1}'
func (b *EVMMultidownloaderRPC) Status() (interface{}, rpc.Error) {
	finalizedBlockNumber, err := b.downloader.GetFinalizedBlockNumber(context.Background())
	if err != nil {
		return nil, rpc.NewRPCError(rpc.DefaultErrorCode,
			"EVMMultidownloaderRPC.Status: getting finalized block number: %v", err)
	}
	latestBlockNumber, err := b.downloader.GetLatestBlockNumber(context.Background())
	if err != nil {
		return nil, rpc.NewRPCError(rpc.DefaultErrorCode,
			"EVMMultidownloaderRPC.Status: getting latest block number: %v", err)
	}
	b.downloader.mutex.Lock()
	defer b.downloader.mutex.Unlock()

	info := struct {
		Status               string `json:"status"`
		State                string `json:"state,omitempty"`
		Pending              string `json:"pending,omitempty"`
		FinalizedBlockNumber uint64 `json:"finalizedBlockNumber,omitempty"`
		LatestBlockNumber    uint64 `json:"latestBlockNumber,omitempty"`
	}{
		Status:               "running",
		State:                b.downloader.state.String(),
		FinalizedBlockNumber: finalizedBlockNumber,
		LatestBlockNumber:    latestBlockNumber,
	}
	return info, nil
}
