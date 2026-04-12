package types

import (
	"context"

	"github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
)

type DownloadResult struct {
	Data sync.EVMBlocks
	// CompletionPercentage indicates the percent of completion of the download
	// 0 -> 0%, 100 -> 100%
	CompletionPercentage float64
}

type DownloaderInterface interface {
	// DownloadNextBlocks downloads the next blocks starting from fromBlockHeader
	// up to maxBlocks, according to the syncerConfig
	// parameters:
	// - fromBlockHeader: the block header to start downloading from (exclusive)
	//       If it's nil means that there are no previous blocks processed
	// - maxBlocks: the maximum number of blocks to return (it could return less or none)
	// - syncerConfig: the syncer configuration
	// returns:
	// - DownloadResult: the result of the download, containing the blocks and the percent complete
	//     DownloadResult is never nil
	//     DownloadResult.Data could be nil if no blocks were downloaded
	//     DownloadResult.CompletionPercentage indicates the percent of completion of the download
	//       0 -> 0%, 100 -> 100%
	// - error: if any error occurred during the download
	//   special error: errors.Is(err, ErrLogsNotAvailable) indicates that it works
	//    but there are no logs yet
	DownloadNextBlocks(ctx context.Context,
		fromBlockHeader *aggkittypes.BlockHeader,
		maxBlocks uint64,
		syncerConfig aggkittypes.SyncerConfig) (*DownloadResult, error)
	ChainID(ctx context.Context) (uint64, error)
}
