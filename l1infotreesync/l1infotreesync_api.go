package l1infotreesync

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
)

const (
	// L1InfoTreeSyncV1Prefix is the URL prefix of the L1 info tree API
	L1InfoTreeSyncV1Prefix = "/l1infotreesync/v1"

	globalExitRootQueryParam = "global_exit_root"
	rootQueryParam           = "root"

	// hexHashLength is the length of a 32-byte hash as hex, without the 0x prefix
	hexHashLength = 64
)

// StatusInfo is the status of the L1 info tree sync
type StatusInfo struct {
	Status               string   `json:"status"`
	CompletionPercentage *float64 `json:"completionPercentage,omitempty"`
}

// L1InfoTreeSyncer is the interface the L1 info tree API queries
type L1InfoTreeSyncer interface {
	GetInfoByGlobalExitRoot(ger common.Hash) (*L1InfoTreeLeaf, error)
	GetLatestL1InfoLeaf(ctx context.Context) (*L1InfoTreeLeaf, error)
	GetInfoByRoot(ger common.Hash) (*L1InfoTreeLeaf, error)
	GetCompletionPercentage() *float64
}

// L1InfoTreeSyncAPI serves the L1 info tree queries over HTTP.
type L1InfoTreeSyncAPI struct {
	logger           aggkitcommon.Logger
	l1InfoTreeSyncer L1InfoTreeSyncer
}

// NewL1InfoTreeSyncAPI returns an API that answers from l1InfoTreeSyncer.
func NewL1InfoTreeSyncAPI(
	logger aggkitcommon.Logger,
	l1InfoTreeSyncer L1InfoTreeSyncer,
) *L1InfoTreeSyncAPI {
	return &L1InfoTreeSyncAPI{
		logger:           logger,
		l1InfoTreeSyncer: l1InfoTreeSyncer,
	}
}

// RegisterRoutes registers the L1 info tree API routes on router:
//
//	GET /l1infotreesync/v1/status
//	GET /l1infotreesync/v1/info-by-global-exit-root[?global_exit_root=]
//	GET /l1infotreesync/v1/info-by-root[?root=]
func (a *L1InfoTreeSyncAPI) RegisterRoutes(router gin.IRouter) {
	group := router.Group(L1InfoTreeSyncV1Prefix)
	group.GET("/status", a.status)
	group.GET("/info-by-global-exit-root", a.infoByGlobalExitRoot)
	group.GET("/info-by-root", a.infoByRoot)
}

// status returns the status of the L1 info tree sync.
func (a *L1InfoTreeSyncAPI) status(c *gin.Context) {
	c.JSON(http.StatusOK, StatusInfo{
		Status:               "running",
		CompletionPercentage: a.l1InfoTreeSyncer.GetCompletionPercentage(),
	})
}

// infoByGlobalExitRoot returns the leaf for the given global exit root, or the latest leaf when
// the query parameter is absent. A malformed hash is a 400 and a leaf that does not exist is a 404.
func (a *L1InfoTreeSyncAPI) infoByGlobalExitRoot(c *gin.Context) {
	raw, hasParam := c.GetQuery(globalExitRootQueryParam)
	if !hasParam {
		a.logger.Debugf("getting latest leaf for l1infotreesync info-by-global-exit-root")
		leaf, err := a.l1InfoTreeSyncer.GetLatestL1InfoLeaf(c.Request.Context())
		writeLeaf(c, leaf, err)
		return
	}

	if !isValidHexHash(raw) {
		c.JSON(http.StatusBadRequest,
			gin.H{"error": fmt.Sprintf("invalid %s parameter: expected a 32-byte hex value", globalExitRootQueryParam)})
		return
	}

	ger := common.HexToHash(raw)
	a.logger.Debugf("getting leaf for l1infotreesync info-by-global-exit-root %s", ger.Hex())
	leaf, err := a.l1InfoTreeSyncer.GetInfoByGlobalExitRoot(ger)
	writeLeaf(c, leaf, err)
}

// infoByRoot returns the leaf for the given root, or the latest leaf when the query parameter is
// absent. A malformed hash is a 400 and a leaf that does not exist is a 404.
func (a *L1InfoTreeSyncAPI) infoByRoot(c *gin.Context) {
	raw, hasParam := c.GetQuery(rootQueryParam)
	if !hasParam {
		a.logger.Debugf("getting latest leaf for l1infotreesync info-by-root")
		leaf, err := a.l1InfoTreeSyncer.GetLatestL1InfoLeaf(c.Request.Context())
		writeLeaf(c, leaf, err)
		return
	}

	if !isValidHexHash(raw) {
		c.JSON(http.StatusBadRequest,
			gin.H{"error": fmt.Sprintf("invalid %s parameter: expected a 32-byte hex value", rootQueryParam)})
		return
	}

	root := common.HexToHash(raw)
	a.logger.Debugf("getting leaf for l1infotreesync info-by-root %s", root.Hex())
	leaf, err := a.l1InfoTreeSyncer.GetInfoByRoot(root)
	writeLeaf(c, leaf, err)
}

// writeLeaf writes a leaf lookup result: 500 on error, 404 when there is no leaf, 200 otherwise.
func writeLeaf(c *gin.Context, leaf *L1InfoTreeLeaf, err error) {
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("error getting leaf: %v", err)})
		return
	}
	if leaf == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "leaf not found"})
		return
	}
	c.JSON(http.StatusOK, leaf)
}

// isValidHexHash reports whether s is a 32-byte hash in hex, with or without the 0x prefix.
func isValidHexHash(s string) bool {
	hashStr := strings.TrimPrefix(s, "0x")
	if len(hashStr) != hexHashLength {
		return false
	}
	_, err := hex.DecodeString(hashStr)
	return err == nil
}
