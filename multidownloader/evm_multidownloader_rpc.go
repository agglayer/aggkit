package multidownloader

import (
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
// -d '{"method":"l1infotreesync_status", "params":[], "id":1}'
func (b *EVMMultidownloaderRPC) Status() (interface{}, rpc.Error) {
	info := struct {
		Status string `json:"status"`
	}{
		Status: "running",
	}
	return info, nil
}
