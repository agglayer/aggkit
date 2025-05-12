package bridgeservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"time"

	"github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/claimsponsor"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	tree "github.com/agglayer/aggkit/tree/types"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

const (
	// BRIDGE is the namespace of the bridge service
	BRIDGE    = "bridge"
	meterName = "github.com/agglayer/aggkit/bridgeservice"

	networkIDParam    = "network_id"
	networkIDsParam   = "network_ids"
	pageNumberParam   = "page_number"
	pageSizeParam     = "page_size"
	depositCountParam = "deposit_count"
	leafIndexParam    = "leaf_index"
	globalIndexParam  = "global_index"

	binarySearchDivider = 2
	mainnetNetworkID    = 0
)

var (
	ErrNotOnL1Info = errors.New("this bridge has not been included on the L1 Info Tree yet")
)

type Config struct {
	Logger       *log.Logger
	WriteTimeout time.Duration
	ReadTimeout  time.Duration
	NetworkID    uint32
}

// BridgeService contains implementations for the bridge service endpoints
type BridgeService struct {
	logger       *log.Logger
	meter        metric.Meter
	readTimeout  time.Duration
	writeTimeout time.Duration
	networkID    uint32
	sponsor      ClaimSponsorer
	l1InfoTree   L1InfoTreer
	injectedGERs LastGERer
	bridgeL1     Bridger
	bridgeL2     Bridger

	router *gin.Engine
}

// New returns instance of BridgeService
func New(
	cfg *Config,
	sponsor ClaimSponsorer,
	l1InfoTree L1InfoTreer,
	injectedGERs LastGERer,
	bridgeL1 Bridger,
	bridgeL2 Bridger,
) *BridgeService {
	meter := otel.Meter(meterName)
	cfg.Logger.Infof("starting bridge service (network id=%d)", cfg.NetworkID)

	b := &BridgeService{
		logger:       cfg.Logger,
		meter:        meter,
		readTimeout:  cfg.ReadTimeout,
		writeTimeout: cfg.WriteTimeout,
		networkID:    cfg.NetworkID,
		sponsor:      sponsor,
		l1InfoTree:   l1InfoTree,
		injectedGERs: injectedGERs,
		bridgeL1:     bridgeL1,
		bridgeL2:     bridgeL2,
		router:       gin.Default(),
	}

	b.registerRoutes()

	return b
}

// registerRoutes registers the routes for the bridge service
func (b *BridgeService) registerRoutes() {
	b.router.GET("/bridges", b.GetBridgesHandler)
	b.router.GET("/claims", b.GetClaimsHandler)
	b.router.GET("/token-mappings", b.GetTokenMappingsHandler)
	b.router.GET("/legacy-token-migrations", b.GetLegacyTokenMigrationsHandler)
	b.router.GET("/l1-info-tree-index", b.L1InfoTreeIndexForBridgeHandler)
	b.router.GET("/injected-l1-info-leaf", b.InjectedL1InfoLeafHandler)
	b.router.GET("/claim-proof", b.ClaimProofHandler)
	b.router.POST("/sponsor-claim", b.SponsorClaimHandler)
	b.router.GET("/sponsored-claim-status", b.GetSponsoredClaimStatusHandler)
	b.router.GET("/last-reorg-event", b.GetLastReorgEventHandler)

	// Swagger docs endpoint
	b.router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
}

// Start starts the HTTP bridge service
func (b *BridgeService) Start(ctx context.Context, address string) error {
	srv := &http.Server{
		Addr:         address,
		Handler:      b.router,
		ReadTimeout:  b.readTimeout,
		WriteTimeout: b.writeTimeout,
	}

	go func() {
		b.logger.Infof("Bridge service listening on %s", address)
		err := srv.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			b.logger.Fatalf("listen error: %v", err)
		}
	}()

	<-ctx.Done()

	b.logger.Info("Shutting down bridge service...")

	var parentCtx context.Context
	if ctx.Err() == nil {
		parentCtx = ctx
	} else {
		parentCtx = context.Background()
	}

	ctx, cancel := context.WithTimeout(parentCtx, b.readTimeout)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		b.logger.Errorf("Server shutdown error: %v", err)
		return err
	}

	b.logger.Info("Bridge service exited gracefully")
	return nil
}

// GetBridgesHandler retrieves paginated bridge data for the specified network.
//
// @Summary Get bridges
// @Description Returns a paginated list of bridge events for the specified network.
// @Tags bridges
// @Param network_id query uint32 true "Target network ID"
// @Param page_number query uint32 false "Page number (default 1)"
// @Param page_size query uint32 false "Page size (default 100)"
// @Param deposit_count query uint64 false "Filter by deposit count"
// @Param network_ids query []uint32 false "Filter by one or more network IDs"
// @Produce json
// @Success 200 {object} types.BridgesResult
// @Failure 400 {object} gin.H "Invalid request parameters"
// @Failure 500 {object} gin.H "Internal server error"
// @Router /bridges [get]
func (b *BridgeService) GetBridgesHandler(c *gin.Context) {
	b.logger.Debugf("GetBridges request received (network id=%s, page number=%s, page size=%s)",
		c.Query(networkIDParam), c.Query(pageNumberParam), c.Query(pageSizeParam))

	networkID, err := parseUintQuery(c, networkIDParam, true, uint32(0))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	depositCount, err := parseUintQuery(c, depositCountParam, false, uint64(math.MaxUint64))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var depositCountPtr *uint64
	if depositCount != math.MaxUint64 {
		depositCountPtr = &depositCount
	}

	networkIDs, err := parseUint32SliceParam(c, networkIDsParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid network_ids: %s", err)})
		return
	}

	ctx, cancel, pageNumber, pageSize, setupErr := b.setupRequest(c, "get_bridges")
	if setupErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": setupErr.Error()})
		return
	}
	defer cancel()

	b.logger.Debugf("fetching bridges (network id=%d, page=%d, size=%d, deposit_count=%v, network_ids=%v)",
		networkID, pageNumber, pageSize, depositCountPtr, networkIDs)

	var (
		bridges []*bridgesync.BridgeResponse
		count   int
	)

	switch {
	case networkID == mainnetNetworkID:
		bridges, count, err = b.bridgeL1.GetBridgesPaged(ctx, pageNumber, pageSize, depositCountPtr, networkIDs)
		if err != nil {
			c.JSON(http.StatusInternalServerError,
				gin.H{"error": fmt.Sprintf("failed to get bridges for the L1 network, error: %s", err)})
			return
		}
	case networkID == b.networkID:
		bridges, count, err = b.bridgeL2.GetBridgesPaged(ctx, pageNumber, pageSize, depositCountPtr, networkIDs)
		if err != nil {
			c.JSON(http.StatusInternalServerError,
				gin.H{"error": fmt.Sprintf("failed to get bridges for the L2 network (ID=%d), error: %s", networkID, err)})
			return
		}
	default:
		c.JSON(http.StatusBadRequest,
			gin.H{"error": fmt.Sprintf("failed to get bridges unsupported network %d", networkID)})
		return
	}

	c.JSON(http.StatusOK,
		types.BridgesResult{
			Bridges: bridges,
			Count:   count,
		})
}

// GetClaimsHandler retrieves paginated claims for a given network.
//
// @Summary Get claims
// @Description Returns a paginated list of claims for the specified network.
// @Tags claims
// @Param network_id query uint32 true "Target network ID"
// @Param page_number query uint32 false "Page number (default 1)"
// @Param page_size query uint32 false "Page size (default 100)"
// @Param network_ids query []uint32 false "Filter by one or more network IDs"
// @Produce json
// @Success 200 {object} types.ClaimsResult
// @Failure 400 {object} gin.H "Invalid request parameters"
// @Failure 500 {object} gin.H "Internal server error"
// @Router /claims [get]
func (b *BridgeService) GetClaimsHandler(c *gin.Context) {
	b.logger.Debugf("GetClaims request received (network id=%s, page number=%s, page size=%s)",
		c.Query(networkIDParam), c.Query(pageNumberParam), c.Query(pageSizeParam))

	networkID, err := parseUintQuery(c, networkIDParam, true, uint32(0))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	networkIDs, err := parseUint32SliceParam(c, networkIDsParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel, pageNumber, pageSize, setupErr := b.setupRequest(c, "get_claims")
	if setupErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": setupErr.Error()})
		return
	}
	defer cancel()

	b.logger.Debugf("fetching claims (network id=%d, page=%d, size=%d, network_ids=%v)",
		networkID, pageNumber, pageSize, networkIDs)

	var (
		claims []*bridgesync.ClaimResponse
		count  int
	)

	switch {
	case networkID == mainnetNetworkID:
		claims, count, err = b.bridgeL1.GetClaimsPaged(ctx, pageNumber, pageSize, networkIDs)
		if err != nil {
			c.JSON(http.StatusInternalServerError,
				gin.H{"error": fmt.Sprintf("failed to get claims for the L1 network, error: %s", err)})
			return
		}
	case networkID == b.networkID:
		claims, count, err = b.bridgeL2.GetClaimsPaged(ctx, pageNumber, pageSize, networkIDs)
		if err != nil {
			c.JSON(http.StatusInternalServerError,
				gin.H{"error": fmt.Sprintf("failed to get claims for the L2 network (ID=%d), error: %s", networkID, err)})
			return
		}
	default:
		c.JSON(http.StatusBadRequest,
			gin.H{"error": fmt.Sprintf("failed to get claims, unsupported network %d", networkID)})
		return
	}

	c.JSON(http.StatusOK,
		types.ClaimsResult{
			Claims: claims,
			Count:  count,
		})
}

// @Summary Get token mappings
// @Description Returns token mappings for the given network, paginated
// @Tags token-mappings
// @Param network_id query int true "Network ID"
// @Param page_number query int false "Page number"
// @Param page_size query int false "Page size"
// @Produce json
// @Success 200 {object} TokenMappingsResult
// @Failure 400 {object} gin.H
// @Failure 500 {object} gin.H
// @Router /token-mappings [get]
//
//nolint:dupl
func (b *BridgeService) GetTokenMappingsHandler(c *gin.Context) {
	b.logger.Debugf("GetTokenMappings request received (network id=%s, page number=%s, page size=%s)",
		c.Query(networkIDParam), c.Query(pageNumberParam), c.Query(pageSizeParam))

	networkID, err := parseUintQuery(c, networkIDParam, true, uint32(0))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel, pageNumber, pageSize, setupErr := b.setupRequest(c, "get_token_mappings")
	if setupErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": setupErr.Error()})
		return
	}
	defer cancel()

	var (
		tokenMappings      []*bridgesync.TokenMapping
		tokenMappingsCount int
	)

	switch {
	case networkID == mainnetNetworkID:
		tokenMappings, tokenMappingsCount, setupErr = b.bridgeL1.GetTokenMappings(ctx, pageNumber, pageSize)
	case b.networkID == networkID:
		tokenMappings, tokenMappingsCount, setupErr = b.bridgeL2.GetTokenMappings(ctx, pageNumber, pageSize)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("unsupported network id %d", networkID)})
		return
	}

	if setupErr != nil {
		c.JSON(http.StatusInternalServerError,
			gin.H{"error": fmt.Sprintf("failed to fetch token mappings: %s", setupErr.Error())})
		return
	}

	c.JSON(http.StatusOK,
		types.TokenMappingsResult{
			TokenMappings: tokenMappings,
			Count:         tokenMappingsCount,
		})
}

// @Summary Get legacy token migrations
// @Description Returns legacy token migrations for the given network, paginated
// @Tags legacy-token-migrations
// @Param network_id query int true "Network ID"
// @Param page_number query int false "Page number"
// @Param page_size query int false "Page size"
// @Produce json
// @Success 200 {object} types.LegacyTokenMigrationsResult
// @Failure 400 {object} gin.H
// @Failure 500 {object} gin.H
// @Router /legacy-token-migrations [get]
//
//nolint:dupl
func (b *BridgeService) GetLegacyTokenMigrationsHandler(c *gin.Context) {
	b.logger.Debugf("GetLegacyTokenMigrations request received (network id=%s, page number=%s, page size=%s)",
		c.Query(networkIDParam), c.Query(pageNumberParam), c.Query(pageSizeParam))

	networkID, err := parseUintQuery(c, networkIDParam, true, uint32(0))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel, pageNumber, pageSize, setupErr := b.setupRequest(c, "get_legacy_token_migrations")
	if setupErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": setupErr.Error()})
		return
	}
	defer cancel()

	var (
		tokenMigrations      []*bridgesync.LegacyTokenMigration
		tokenMigrationsCount int
	)

	switch {
	case networkID == mainnetNetworkID:
		tokenMigrations, tokenMigrationsCount, setupErr = b.bridgeL1.GetLegacyTokenMigrations(ctx, pageNumber, pageSize)
	case b.networkID == networkID:
		tokenMigrations, tokenMigrationsCount, setupErr = b.bridgeL2.GetLegacyTokenMigrations(ctx, pageNumber, pageSize)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("unsupported network id %d", networkID)})
		return
	}

	if setupErr != nil {
		c.JSON(http.StatusInternalServerError,
			gin.H{"error": fmt.Sprintf("failed to fetch legacy token migrations: %s", setupErr.Error())})
		return
	}

	c.JSON(http.StatusOK,
		types.LegacyTokenMigrationsResult{
			TokenMigrations: tokenMigrations,
			Count:           tokenMigrationsCount,
		})
}

// @Summary Get L1 Info Tree index for a bridge
// @Description Returns the first L1 Info Tree index after a given deposit count for the specified network
// @Tags l1-info-tree
// @Param network_id query int true "Network ID"
// @Param deposit_count query int true "Deposit count"
// @Produce json
// @Success 200 {object} uint32
// @Failure 400 {object} gin.H
// @Failure 500 {object} gin.H
// @Router /l1-info-tree-index [get]
func (b *BridgeService) L1InfoTreeIndexForBridgeHandler(c *gin.Context) {
	b.logger.Debugf("L1InfoTreeIndexForBridge request received (network id=%s, deposit count=%s)",
		c.Query(networkIDParam), c.Query(depositCountParam))

	networkID, err := parseUintQuery(c, networkIDParam, true, uint32(0))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	depositCount, err := parseUintQuery(c, depositCountParam, true, uint32(0))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c, b.readTimeout)
	defer cancel()

	cnt, merr := b.meter.Int64Counter("l1_info_tree_index_for_bridge")
	if merr != nil {
		b.logger.Warnf("failed to create l1_info_tree_index_for_bridge counter: %s", merr)
	}
	cnt.Add(ctx, 1)

	var l1InfoTreeIndex uint32

	switch {
	case networkID == mainnetNetworkID:
		l1InfoTreeIndex, err = b.getFirstL1InfoTreeIndexForL1Bridge(ctx, depositCount)
	case b.networkID == networkID:
		l1InfoTreeIndex, err = b.getFirstL1InfoTreeIndexForL2Bridge(ctx, depositCount)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("unsupported network id %d", networkID)})
		return
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError,
			gin.H{"error": fmt.Sprintf("failed to get l1 info tree index for network id %d and deposit count %d, error: %s",
				networkID, depositCount, err)})
		return
	}

	c.JSON(http.StatusOK, l1InfoTreeIndex)
}

// @Summary Get injected L1 info tree leaf after a given L1 info tree index
// @Description Returns the L1 info tree leaf either at the given index (for L1)
// or the first injected global exit root after the given index (for L2).
// @Tags injected-info
// @Param network_id query int true "Network ID"
// @Param l1_info_tree_index query int true "L1 Info Tree Index"
// @Produce json
// @Success 200 {object} l1infotreesync.L1InfoTreeLeaf
// @Failure 400 {object} gin.H
// @Failure 500 {object} gin.H
// @Router /injected-l1-info-leaf [get]
func (b *BridgeService) InjectedL1InfoLeafHandler(c *gin.Context) {
	b.logger.Debugf("InjectedInfoAfterIndex request received (network id=%s, leaf index=%s)",
		c.Query(networkIDParam), c.Query(leafIndexParam))

	networkID, err := parseUintQuery(c, networkIDParam, true, uint32(0))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	l1InfoTreeIndex, err := parseUintQuery(c, leafIndexParam, true, uint32(0))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c, b.readTimeout)
	defer cancel()

	cnt, merr := b.meter.Int64Counter("injected_info_after_index")
	if merr != nil {
		b.logger.Warnf("failed to create injected_info_after_index counter: %s", merr)
	}
	cnt.Add(ctx, 1)

	var l1InfoLeaf *l1infotreesync.L1InfoTreeLeaf

	switch {
	case networkID == mainnetNetworkID:
		l1InfoLeaf, err = b.l1InfoTree.GetInfoByIndex(ctx, l1InfoTreeIndex)
	case b.networkID == networkID:
		e, err := b.injectedGERs.GetFirstGERAfterL1InfoTreeIndex(ctx, l1InfoTreeIndex)
		if err != nil {
			c.JSON(http.StatusInternalServerError,
				gin.H{"error": fmt.Sprintf("failed to get injected global exit root for leaf index=%d, error: %s",
					l1InfoTreeIndex, err)})
			return
		}

		l1InfoLeaf, err = b.l1InfoTree.GetInfoByIndex(ctx, e.L1InfoTreeIndex)
		if err != nil {
			c.JSON(http.StatusInternalServerError,
				gin.H{"error": fmt.Sprintf("failed to get L1 info tree leaf (leaf index=%d), error: %s",
					e.L1InfoTreeIndex, err)})
			return
		}
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("unsupported network id %d", networkID)})
		return
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError,
			gin.H{"error": fmt.Sprintf("failed to get L1 info tree leaf (network id=%d, leaf index=%d), error: %s",
				networkID, l1InfoTreeIndex, err)})
		return
	}

	c.JSON(http.StatusOK, l1InfoLeaf)
}

// ClaimProofHandler returns the Merkle proofs required to verify a claim on the target network.
//
// @Summary Get claim proof
// @Description Returns the Merkle proofs (local and rollup exit root) and
// the corresponding L1 info tree leaf needed to verify a claim.
// @Tags claims
// @Param network_id query uint32 true "Target network ID"
// @Param l1_info_tree_index query uint32 true "Index in the L1 info tree"
// @Param deposit_count query uint32 true "Number of deposits in the bridge"
// @Produce json
// @Success 200 {object} types.ClaimProof "Merkle proofs and L1 info tree leaf"
// @Failure 400 {object} gin.H "Missing or invalid parameters"
// @Failure 500 {object} gin.H "Internal server error retrieving claim proof"
// @Router /claim-proof [get]
func (b *BridgeService) ClaimProofHandler(c *gin.Context) {
	b.logger.Debugf("ClaimProof request received (network id=%s, l1 info tree index=%s, deposit count=%s)",
		c.Query(networkIDParam), c.Query(leafIndexParam), c.Query(depositCountParam))
	ctx, cancel := context.WithTimeout(c, b.readTimeout)
	defer cancel()

	cnt, merr := b.meter.Int64Counter("claim_proof")
	if merr != nil {
		b.logger.Warnf("failed to create claim_proof counter: %s", merr)
	}
	cnt.Add(ctx, 1)

	networkID, err := parseUintQuery(c, networkIDParam, true, uint32(0))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	l1InfoTreeIndex, err := parseUintQuery(c, leafIndexParam, true, uint32(0))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	depositCount, err := parseUintQuery(c, depositCountParam, true, uint32(0))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	info, err := b.l1InfoTree.GetInfoByIndex(ctx, l1InfoTreeIndex)
	if err != nil {
		c.JSON(http.StatusInternalServerError,
			gin.H{"error": fmt.Sprintf("failed to get l1 info tree leaf for index %d: %s", l1InfoTreeIndex, err)})
		return
	}

	var proofLocalExitRoot tree.Proof
	switch {
	case networkID == mainnetNetworkID:
		proofLocalExitRoot, err = b.bridgeL1.GetProof(ctx, depositCount, info.MainnetExitRoot)
		if err != nil {
			c.JSON(http.StatusInternalServerError,
				gin.H{"error": fmt.Sprintf("failed to get local exit proof, error: %s", err)})
			return
		}

	case networkID == b.networkID:
		localExitRoot, err := b.l1InfoTree.GetLocalExitRoot(ctx, networkID, info.RollupExitRoot)
		if err != nil {
			c.JSON(http.StatusInternalServerError,
				gin.H{"error": fmt.Sprintf("failed to get local exit root from rollup exit tree, error: %s", err)})
			return
		}
		proofLocalExitRoot, err = b.bridgeL2.GetProof(ctx, depositCount, localExitRoot)
		if err != nil {
			c.JSON(http.StatusInternalServerError,
				gin.H{"error": fmt.Sprintf("failed to get local exit proof, error: %s", err)})
			return
		}

	default:
		c.JSON(http.StatusBadRequest,
			gin.H{"error": fmt.Sprintf("failed to get claim proof, unsupported network %d", networkID)})
		return
	}

	proofRollupExitRoot, err := b.l1InfoTree.GetRollupExitTreeMerkleProof(ctx, networkID, info.RollupExitRoot)
	if err != nil {
		c.JSON(http.StatusInternalServerError,
			gin.H{
				"error": fmt.Sprintf("failed to get rollup exit proof (network id=%d, leaf index=%d, deposit count=%d), error: %s",
					networkID, l1InfoTreeIndex, depositCount, err)})
		return
	}

	c.JSON(http.StatusOK, types.ClaimProof{
		ProofLocalExitRoot:  proofLocalExitRoot,
		ProofRollupExitRoot: proofRollupExitRoot,
		L1InfoTreeLeaf:      *info,
	})
}

// @Summary Sponsor a claim
// @Description Sponsors a claim to be processed by the bridge service.
// @Tags claims
// @Accept json
// @Produce json
// @Param Claim body claimsponsor.Claim true "Claim Request"
// @Success 200 {object} gin.H{"status": "claim sponsored"}
// @Failure 400 {object} gin.H{"error": "bad request"}
// @Failure 500 {object} gin.H{"error": "internal server error"}
// @Router /sponsor-claim [post]
func (b *BridgeService) SponsorClaimHandler(c *gin.Context) {
	rawBody, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}

	if len(rawBody) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "request body is empty"})
		return
	}

	var claim claimsponsor.Claim
	if err := json.Unmarshal(rawBody, &claim); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}

	// Validate required fields
	if claim.GlobalIndex == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "global_index is mandatory"})
		return
	}

	b.logger.Debugf("SponsorClaim request received (claim global index=%s)", claim.GlobalIndex.String())

	ctx, cancel := context.WithTimeout(c, b.writeTimeout)
	defer cancel()

	cnt, merr := b.meter.Int64Counter("sponsor_claim")
	if merr != nil {
		b.logger.Warnf("failed to create sponsor_claim counter: %s", merr)
	}
	cnt.Add(ctx, 1)

	if b.sponsor == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "this client does not support claim sponsoring"})
		return
	}

	if claim.DestinationNetwork != b.networkID {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("this client only sponsors claims for destination network %d", b.networkID),
		})
		return
	}

	if err := b.sponsor.AddClaimToQueue(&claim); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add claim to queue: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "claim sponsored"})
}

// GetSponsoredClaimStatusHandler returns the sponsorship status of a claim by its global index.
//
// @Summary Get sponsored claim status
// @Description Returns the sponsorship status of a claim identified by the given global index.
// Only available if claim sponsoring is enabled.
// @Tags claims
// @Param global_index query string true "Global index of the claim (big.Int format)"
// @Produce json
// @Success 200 {object} string "Claim sponsorship status"
// @Failure 400 {object} gin.H "Missing or invalid input, or sponsorship not supported"
// @Failure 500 {object} gin.H "Internal server error retrieving claim status"
// @Router /sponsored-claim-status [get]
func (b *BridgeService) GetSponsoredClaimStatusHandler(c *gin.Context) {
	globalIndexRaw := c.Query(globalIndexParam)

	b.logger.Debugf("GetSponsoredClaimStatus request received (claim global index=%s)", globalIndexRaw)
	ctx, cancel := context.WithTimeout(c, b.readTimeout)
	defer cancel()

	cnt, merr := b.meter.Int64Counter("get_sponsored_claim_status")
	if merr != nil {
		b.logger.Warnf("failed to create get_sponsored_claim_status counter: %s", merr)
	}
	cnt.Add(ctx, 1)

	if b.sponsor == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "this client does not support claim sponsoring"})
		return
	}

	if globalIndexRaw == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("%s is mandatory", globalIndexParam)})
		return
	}

	globalIndex, _ := new(big.Int).SetString(globalIndexRaw, 0)
	claim, err := b.sponsor.GetClaim(globalIndex)
	if err != nil {
		c.JSON(http.StatusInternalServerError,
			gin.H{"error": fmt.Sprintf("failed to get claim status for global index %d, error: %s", globalIndex, err)})
		return
	}

	c.JSON(http.StatusOK, claim.Status)
}

// GetLastReorgEventHandler returns the most recent reorganization event for the specified network.
//
// @Summary Get last reorg event
// @Description Retrieves the last known reorg event for either L1 or L2, based on the provided network ID.
// @Tags reorgs
// @Param network_id query int true "Network ID (e.g., 0 for L1, or the ID of the L2 network)"
// @Produce json
// @Success 200 {object} bridgesync.LastReorg "Details of the last reorg event"
// @Failure 400 {object} gin.H "Bad request due to missing or invalid parameters"
// @Failure 500 {object} gin.H "Internal server error retrieving reorg data"
// @Router /last-reorg-event [get]
func (b *BridgeService) GetLastReorgEventHandler(c *gin.Context) {
	b.logger.Debugf("GetLastReorgEvent request received (network id=%s)", c.Query(networkIDParam))
	ctx, cancel := context.WithTimeout(c, b.readTimeout)
	defer cancel()

	cnt, merr := b.meter.Int64Counter("last_reorg_event")
	if merr != nil {
		b.logger.Warnf("failed to create last_reorg_event counter: %s", merr)
	}
	cnt.Add(ctx, 1)

	networkID, err := parseUintQuery(c, networkIDParam, true, uint32(0))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var reorgEvent *bridgesync.LastReorg

	switch {
	case networkID == mainnetNetworkID:
		reorgEvent, err = b.bridgeL1.GetLastReorgEvent(ctx)
		if err != nil {
			c.JSON(http.StatusInternalServerError,
				gin.H{"error": fmt.Sprintf("failed to get last reorg event for the L1 network, error: %s", err)})
			return
		}
	case networkID == b.networkID:
		reorgEvent, err = b.bridgeL2.GetLastReorgEvent(ctx)
		if err != nil {
			c.JSON(http.StatusInternalServerError,
				gin.H{"error": fmt.Sprintf("failed to get last reorg event for the L2 network (ID=%d), error: %s", networkID, err)})
			return
		}
	default:
		c.JSON(http.StatusBadRequest,
			gin.H{"error": fmt.Sprintf("failed to get last reorg event, unsupported network %d", networkID)})
		return
	}

	c.JSON(http.StatusOK, reorgEvent)
}

func (b *BridgeService) getFirstL1InfoTreeIndexForL1Bridge(ctx context.Context, depositCount uint32) (uint32, error) {
	lastInfo, err := b.l1InfoTree.GetLastInfo()
	if err != nil {
		return 0, err
	}

	root, err := b.bridgeL1.GetRootByLER(ctx, lastInfo.MainnetExitRoot)
	if err != nil {
		return 0, err
	}
	if root.Index < depositCount {
		return 0, ErrNotOnL1Info
	}

	firstInfo, err := b.l1InfoTree.GetFirstInfo()
	if err != nil {
		return 0, err
	}

	// Binary search between the first and last blocks where L1 info tree was updated.
	// Find the smallest l1 info tree index that is greater than depositCount and matches with
	// a MER that is included on the l1 info tree
	bestResult := lastInfo
	lowerLimit := firstInfo.BlockNumber
	upperLimit := lastInfo.BlockNumber
	for lowerLimit <= upperLimit {
		targetBlock := lowerLimit + ((upperLimit - lowerLimit) / binarySearchDivider)
		targetInfo, err := b.l1InfoTree.GetFirstInfoAfterBlock(targetBlock)
		if err != nil {
			return 0, err
		}
		root, err := b.bridgeL1.GetRootByLER(ctx, targetInfo.MainnetExitRoot)
		if err != nil {
			return 0, err
		}
		if root.Index < depositCount {
			lowerLimit = targetBlock + 1
		} else if root.Index == depositCount {
			bestResult = targetInfo
			break
		} else {
			bestResult = targetInfo
			upperLimit = targetBlock - 1
		}
	}

	return bestResult.L1InfoTreeIndex, nil
}

func (b *BridgeService) getFirstL1InfoTreeIndexForL2Bridge(ctx context.Context, depositCount uint32) (uint32, error) {
	// NOTE: this code assumes that all the rollup exit roots
	// (produced by the smart contract call verifyBatches / verifyBatchesTrustedAggregator)
	// are included in the L1 info tree. As per the current implementation (smart contracts) of the protocol
	// this is true. This could change in the future
	lastVerified, err := b.l1InfoTree.GetLastVerifiedBatches(b.networkID)
	if err != nil {
		return 0, err
	}

	root, err := b.bridgeL2.GetRootByLER(ctx, lastVerified.ExitRoot)
	if err != nil {
		return 0, err
	}
	if root.Index < depositCount {
		return 0, ErrNotOnL1Info
	}

	firstVerified, err := b.l1InfoTree.GetFirstVerifiedBatches(b.networkID)
	if err != nil {
		return 0, err
	}

	// Binary search between the first and last blcoks where batches were verified.
	// Find the smallest deposit count that is greater than depositCount and matches with
	// a LER that is verified
	bestResult := lastVerified
	lowerLimit := firstVerified.BlockNumber
	upperLimit := lastVerified.BlockNumber
	for lowerLimit <= upperLimit {
		targetBlock := lowerLimit + ((upperLimit - lowerLimit) / binarySearchDivider)
		targetVerified, err := b.l1InfoTree.GetFirstVerifiedBatchesAfterBlock(b.networkID, targetBlock)
		if err != nil {
			return 0, err
		}
		root, err = b.bridgeL2.GetRootByLER(ctx, targetVerified.ExitRoot)
		if err != nil {
			return 0, err
		}
		if root.Index < depositCount {
			lowerLimit = targetBlock + 1
		} else if root.Index == depositCount {
			bestResult = targetVerified
			break
		} else {
			bestResult = targetVerified
			upperLimit = targetBlock - 1
		}
	}

	info, err := b.l1InfoTree.GetFirstL1InfoWithRollupExitRoot(bestResult.RollupExitRoot)
	if err != nil {
		return 0, err
	}
	return info.L1InfoTreeIndex, nil
}

// setupRequest parses the pagination parameters from the request context
func (b *BridgeService) setupRequest(
	c *gin.Context,
	counterName string) (context.Context, context.CancelFunc, uint32, uint32, error) {
	pageNumber, err := parseUintQuery(c, pageNumberParam, false, DefaultPage)
	if err != nil {
		return nil, nil, 0, 0, err
	}

	pageSize, err := parseUintQuery(c, pageSizeParam, false, DefaultPageSize)
	if err != nil {
		return nil, nil, 0, 0, err
	}

	err = validatePaginationParams(pageNumber, pageSize)
	if err != nil {
		return nil, nil, 0, 0, err
	}

	ctx, cancel := context.WithTimeout(c, b.readTimeout)
	counter, merr := b.meter.Int64Counter(counterName)
	if merr != nil {
		b.logger.Warnf("failed to create %s counter: %s", counterName, merr)
	}
	counter.Add(ctx, 1)

	return ctx, cancel, pageNumber, pageSize, nil
}
