package multidownloader

import (
	"fmt"
	"net/http"
	"strconv"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
)

const (
	// MultidownloaderV1Prefix is the URL prefix of the multidownloader API
	MultidownloaderV1Prefix = "/multidownloader/v1"

	blockQueryParam = "block"
	decimalBase     = 10
	uint64BitSize   = 64
)

// EVMMultidownloaderAPI serves a multidownloader's status and developer helpers over HTTP.
type EVMMultidownloaderAPI struct {
	logger     aggkitcommon.Logger
	downloader *EVMMultidownloader
}

// NewEVMMultidownloaderAPI returns an API that answers from downloader.
func NewEVMMultidownloaderAPI(
	logger aggkitcommon.Logger,
	downloader *EVMMultidownloader,
) *EVMMultidownloaderAPI {
	return &EVMMultidownloaderAPI{
		logger:     logger,
		downloader: downloader,
	}
}

// RegisterPublicRoutes registers the read-only routes on router:
//
//	GET /multidownloader/v1/<name>/status
func (a *EVMMultidownloaderAPI) RegisterPublicRoutes(router gin.IRouter) {
	router.Group(a.prefix()).GET("/status", a.status)
}

// RegisterAdminRoutes registers the operator routes on router. The reorg route is only
// registered when the downloader runs in developer mode:
//
//	POST /multidownloader/v1/<name>/reorg?block=
func (a *EVMMultidownloaderAPI) RegisterAdminRoutes(router gin.IRouter) {
	if a.downloader.debug == nil {
		return
	}
	router.Group(a.prefix()).POST("/reorg", a.reorg)
}

// prefix returns the routes' prefix, which carries the downloader's name.
func (a *EVMMultidownloaderAPI) prefix() string {
	return MultidownloaderV1Prefix + "/" + a.downloader.name
}

// status returns the status of the multidownloader.
func (a *EVMMultidownloaderAPI) status(c *gin.Context) {
	if !a.downloader.IsInitialized() {
		c.JSON(http.StatusInternalServerError,
			gin.H{"error": "multidownloader not initialized"})
		return
	}

	ctx := c.Request.Context()
	finalizedBlockNumber, err := a.downloader.GetFinalizedBlockNumber(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError,
			gin.H{"error": fmt.Sprintf("getting finalized block number: %v", err)})
		return
	}

	latestBlockNumber, err := a.downloader.GetLatestBlockNumber(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError,
			gin.H{"error": fmt.Sprintf("getting latest block number: %v", err)})
		return
	}

	// Snapshot the state under the mutex, then release it before writing: a slow client must
	// not hold the downloader's lock while the response is encoded and sent, since the
	// downloader's own loop takes the same lock.
	a.downloader.mutex.Lock()
	completationPercentage := a.downloader.state.CompletionPercentage()
	state := a.downloader.state.String()
	a.downloader.mutex.Unlock()

	minPercent := 100.0
	for _, percent := range completationPercentage {
		if percent < minPercent {
			minPercent = percent
		}
	}
	c.JSON(http.StatusOK, struct {
		Status                       string                     `json:"status"`
		State                        string                     `json:"state,omitempty"`
		Pending                      string                     `json:"pending,omitempty"`
		FinalizedBlockNumber         uint64                     `json:"finalizedBlockNumber,omitempty"`
		LatestBlockNumber            uint64                     `json:"latestBlockNumber,omitempty"`
		CompletionPercentage         float64                    `json:"completionPercentage,omitempty"`
		CompletionPercentageDetailed map[common.Address]float64 `json:"completionPercentageDetailed,omitempty"`
	}{
		Status:                       "running",
		State:                        state,
		FinalizedBlockNumber:         finalizedBlockNumber,
		LatestBlockNumber:            latestBlockNumber,
		CompletionPercentage:         minPercent,
		CompletionPercentageDetailed: completationPercentage,
	})
}

// reorg forces a reorg at the given block, for developer use.
func (a *EVMMultidownloaderAPI) reorg(c *gin.Context) {
	if a.downloader.debug == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "reorg is only available in developer mode"})
		return
	}

	raw := c.Query(blockQueryParam)
	block, err := strconv.ParseUint(raw, decimalBase, uint64BitSize)
	if err != nil {
		c.JSON(http.StatusBadRequest,
			gin.H{"error": fmt.Sprintf("invalid %s parameter: expected an unsigned integer", blockQueryParam)})
		return
	}

	a.downloader.debug.ForceReorg(block)
	c.JSON(http.StatusOK, gin.H{"message": "Reorg forced successfully"})
}
