package prover

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

// AggchainProofGenV1Prefix is the URL prefix of the aggchain proof generation API
const AggchainProofGenV1Prefix = "/aggchainproofgen/v1"

// aggchainProofRequest is the body of the aggchain proof generation request
type aggchainProofRequest struct {
	LastProvenBlock   uint64 `json:"last_proven_block"`
	RequestedEndBlock uint64 `json:"requested_end_block"`
}

// AggchainProofGenAPI is the REST API of the aggchain proof generation tool.
type AggchainProofGenAPI struct {
	tool AggchainProofGeneration
}

// NewAggchainProofGenAPI creates a new AggchainProofGenAPI
func NewAggchainProofGenAPI(tool AggchainProofGeneration) *AggchainProofGenAPI {
	return &AggchainProofGenAPI{
		tool: tool,
	}
}

// RegisterAdminRoutes registers the aggchain proof generation route on router:
//
//	POST /aggchainproofgen/v1/aggchain-proof
func (a *AggchainProofGenAPI) RegisterAdminRoutes(router gin.IRouter) {
	router.Group(AggchainProofGenV1Prefix).POST("/aggchain-proof", a.generateAggchainProof)
}

// generateAggchainProof generates an aggchain proof for the request's block
// range and returns the proof JSON.
func (a *AggchainProofGenAPI) generateAggchainProof(c *gin.Context) {
	var req aggchainProofRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{
				"error": fmt.Sprintf("request body exceeds the %d-byte limit", maxBytesErr.Limit),
			})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid request body: %v", err)})
		return
	}

	proof, err := a.tool.GenerateAggchainProof(c.Request.Context(), req.LastProvenBlock, req.RequestedEndBlock)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, proof)
}
