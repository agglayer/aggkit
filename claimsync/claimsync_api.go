package claimsync

import (
	"context"
	"fmt"
	"math/big"
	"net/http"
	"strconv"

	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
	"github.com/gin-gonic/gin"
)

const (
	// DefaultAPIMaxBlockRange is the default maximum number of blocks a claims request can span
	DefaultAPIMaxBlockRange uint64 = 10000
	// DefaultAPIMaxClaims is the default maximum number of claims a claims request can return
	DefaultAPIMaxClaims uint64 = 1000

	fromBlockParam   = "from_block"
	toBlockParam     = "to_block"
	globalIndexParam = "global_index"
)

// ClaimsAPIBackend is the interface required by ClaimSyncAPI.
type ClaimsAPIBackend interface {
	GetLastProcessedBlock(ctx context.Context) (uint64, bool, error)
	GetFirstProcessedBlock(ctx context.Context) (uint64, bool, error)
	// GetClaimsWithLimit returns at most limit claims indexed between fromBlock and toBlock
	GetClaimsWithLimit(ctx context.Context, fromBlock, toBlock, limit uint64) ([]claimsynctypes.Claim, error)
	GetClaimsByGlobalIndex(ctx context.Context, globalIndex *big.Int) ([]claimsynctypes.Claim, error)
}

// ClaimSyncStatus is the response of the status endpoint.
type ClaimSyncStatus struct {
	FirstProcessedBlock *uint64 `json:"firstProcessedBlock,omitempty"`
	LastProcessedBlock  *uint64 `json:"lastProcessedBlock"`
}

// ClaimSyncAPI serves the REST API of the ClaimSync component.
type ClaimSyncAPI struct {
	logger        aggkitcommon.Logger
	backend       ClaimsAPIBackend
	name          string
	maxBlockRange uint64
	maxClaims     uint64
}

// NewClaimSyncAPI creates a new ClaimSyncAPI. Zero limits fall back to the defaults.
func NewClaimSyncAPI(
	logger aggkitcommon.Logger, backend ClaimsAPIBackend, name string, maxBlockRange, maxClaims uint64,
) *ClaimSyncAPI {
	if maxBlockRange == 0 {
		maxBlockRange = DefaultAPIMaxBlockRange
	}
	if maxClaims == 0 {
		maxClaims = DefaultAPIMaxClaims
	}
	return &ClaimSyncAPI{
		logger:        logger,
		backend:       backend,
		name:          name,
		maxBlockRange: maxBlockRange,
		maxClaims:     maxClaims,
	}
}

// RegisterRoutes registers the routes of the API under /<name>/v1.
func (a *ClaimSyncAPI) RegisterRoutes(router gin.IRouter) {
	group := router.Group("/" + a.name + "/v1")
	group.GET("/status", a.Status)
	group.GET("/claims", a.GetClaims)
	group.GET("/claims-by-global-index", a.GetClaimsByGlobalIndex)
}

// Status returns the sync status of the ClaimSync component.
func (a *ClaimSyncAPI) Status(c *gin.Context) {
	ctx := c.Request.Context()
	lastBlock, foundLast, err := a.backend.GetLastProcessedBlock(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError,
			gin.H{"error": fmt.Sprintf("getting last processed block: %v", err)})
		return
	}
	firstBlock, foundFirst, err := a.backend.GetFirstProcessedBlock(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError,
			gin.H{"error": fmt.Sprintf("getting first processed block: %v", err)})
		return
	}
	info := ClaimSyncStatus{}
	if foundFirst {
		info.FirstProcessedBlock = &firstBlock
	}
	if foundLast {
		info.LastProcessedBlock = &lastBlock
	}
	c.JSON(http.StatusOK, info)
}

// GetClaims returns the claims indexed between from_block and to_block (both included).
// The block range and the number of claims returned are bounded so responses stay small.
func (a *ClaimSyncAPI) GetClaims(c *gin.Context) {
	fromBlock, err := parseUint64Query(c, fromBlockParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	toBlock, err := parseUint64Query(c, toBlockParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if toBlock < fromBlock {
		c.JSON(http.StatusBadRequest,
			gin.H{"error": fmt.Sprintf("%s (%d) must not be lower than %s (%d)",
				toBlockParam, toBlock, fromBlockParam, fromBlock)})
		return
	}
	// the span is toBlock-fromBlock+1; this form can't overflow
	if toBlock-fromBlock >= a.maxBlockRange {
		c.JSON(http.StatusBadRequest,
			gin.H{"error": fmt.Sprintf("block range too wide: at most %d blocks can be requested", a.maxBlockRange)})
		return
	}

	a.logger.Debugf("GET claims [%d, %d]", fromBlock, toBlock)
	// one more than the maximum is requested to detect that the result doesn't fit
	claims, err := a.backend.GetClaimsWithLimit(c.Request.Context(), fromBlock, toBlock, a.maxClaims+1)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("getting claims: %v", err)})
		return
	}
	if uint64(len(claims)) > a.maxClaims {
		c.JSON(http.StatusBadRequest,
			gin.H{"error": fmt.Sprintf("too many claims in the requested range: at most %d claims are returned, "+
				"narrow the block range", a.maxClaims)})
		return
	}
	c.JSON(http.StatusOK, claims)
}

// GetClaimsByGlobalIndex returns the claims of the given global index.
func (a *ClaimSyncAPI) GetClaimsByGlobalIndex(c *gin.Context) {
	globalIndexStr := c.Query(globalIndexParam)
	const decimalBase = 10
	globalIndex, ok := new(big.Int).SetString(globalIndexStr, decimalBase)
	if !ok {
		c.JSON(http.StatusBadRequest,
			gin.H{"error": fmt.Sprintf("invalid %s parameter: %q", globalIndexParam, globalIndexStr)})
		return
	}

	a.logger.Debugf("GET claims by global index %s", globalIndexStr)
	claims, err := a.backend.GetClaimsByGlobalIndex(c.Request.Context(), globalIndex)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("getting claims: %v", err)})
		return
	}
	if len(claims) == 0 {
		c.JSON(http.StatusNotFound,
			gin.H{"error": fmt.Sprintf("no claims found for global index %s", globalIndexStr)})
		return
	}
	c.JSON(http.StatusOK, claims)
}

func parseUint64Query(c *gin.Context, name string) (uint64, error) {
	value := c.Query(name)
	if value == "" {
		return 0, fmt.Errorf("missing %s parameter", name)
	}
	const decimalBase, bitSize = 10, 64
	n, err := strconv.ParseUint(value, decimalBase, bitSize)
	if err != nil {
		return 0, fmt.Errorf("invalid %s parameter: %q", name, value)
	}
	return n, nil
}

// RegisterRoutes registers the REST routes of the ClaimSync component.
func (c *ClaimSync) RegisterRoutes(router gin.IRouter) {
	name := "l1claimsync"
	if c.syncerID == claimsynctypes.L2ClaimSyncer {
		name = "l2claimsync"
	}
	logger := log.WithFields("module", name+"-api")
	NewClaimSyncAPI(logger, c, name, c.cfg.APIMaxBlockRange, c.cfg.APIMaxClaims).RegisterRoutes(router)
}
