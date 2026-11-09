package aggsenderrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/log"
	"github.com/gin-gonic/gin"
)

const (
	// AggsenderV1Prefix is the URL prefix of the aggsender REST API
	AggsenderV1Prefix = "/aggsender/v1"

	heightQueryParam = "height"
)

// AggsenderStorer is the certificate storage required by the aggsender API.
type AggsenderStorer interface {
	GetCertificateByHeight(height uint64) (*types.Certificate, error)
	GetLastSentCertificate() (*types.Certificate, error)
	SaveLastSentCertificate(ctx context.Context, certificate types.Certificate) error
}

// AggsenderInterface is the aggsender behaviour required by the aggsender API.
type AggsenderInterface interface {
	Info() types.AggsenderInfo
	ForceTriggerCertificate()
}

// notFoundError marks a query that matched nothing. The REST handlers map it
// to HTTP 404.
type notFoundError struct {
	msg string
}

func (e notFoundError) Error() string { return e.msg }

func newNotFoundError(format string, args ...any) error {
	return notFoundError{msg: fmt.Sprintf(format, args...)}
}

// AggsenderAPI is the REST API of the aggsender. Read-only queries are
// registered on the public server and operator actions on the admin one.
type AggsenderAPI struct {
	logger    *log.Logger
	storage   AggsenderStorer
	aggsender AggsenderInterface
}

// NewAggsenderAPI creates a new AggsenderAPI instance.
func NewAggsenderAPI(
	logger *log.Logger,
	storage AggsenderStorer,
	aggsender AggsenderInterface,
) *AggsenderAPI {
	return &AggsenderAPI{
		logger:    logger,
		storage:   storage,
		aggsender: aggsender,
	}
}

// RegisterPublicRoutes registers the read-only aggsender routes on router:
//
//	GET /aggsender/v1/status
//	GET /aggsender/v1/certificate-header[?height=h]
//	GET /aggsender/v1/certificate-bridge-exits[?height=h]
func (a *AggsenderAPI) RegisterPublicRoutes(router gin.IRouter) {
	group := router.Group(AggsenderV1Prefix)
	{
		group.GET("/status", a.status)
		group.GET("/certificate-header", a.certificateHeaderPerHeight)
		group.GET("/certificate-bridge-exits", a.certificateBridgeExits)
	}
}

// RegisterAdminRoutes registers the operator routes of the aggsender on router:
//
//	POST /aggsender/v1/trigger-certificate
func (a *AggsenderAPI) RegisterAdminRoutes(router gin.IRouter) {
	router.Group(AggsenderV1Prefix).POST("/trigger-certificate", a.triggerCertificate)
}

// status answers the aggsender status. The response body is the same
// AggsenderInfo the JSON-RPC status method returned.
func (a *AggsenderAPI) status(c *gin.Context) {
	c.JSON(http.StatusOK, a.aggsender.Info())
}

// triggerCertificate forces the publication of an epoch event to trigger
// certificate creation.
func (a *AggsenderAPI) triggerCertificate(c *gin.Context) {
	a.aggsender.ForceTriggerCertificate()
	c.JSON(http.StatusOK, nil)
}

// certificateHeaderPerHeight returns the certificate header at the given
// height, or the last sent certificate when height is omitted.
func (a *AggsenderAPI) certificateHeaderPerHeight(c *gin.Context) {
	height, ok := parseHeightQuery(c)
	if !ok {
		return
	}

	cert, err := certificateHeaderPerHeight(a.storage, height)
	if err != nil {
		writeAPIError(c, err)
		return
	}
	c.JSON(http.StatusOK, cert)
}

// certificateBridgeExits returns the bridge exits of the certificate at the
// given height, or of the last sent certificate when height is omitted.
func (a *AggsenderAPI) certificateBridgeExits(c *gin.Context) {
	height, ok := parseHeightQuery(c)
	if !ok {
		return
	}

	bridgeExits, err := certificateBridgeExits(a.storage, height)
	if err != nil {
		writeAPIError(c, err)
		return
	}
	c.JSON(http.StatusOK, bridgeExits)
}

// parseHeightQuery parses the optional height query parameter. It returns a nil
// height when the parameter is absent, and writes a 400 response when it is
// present but not a valid uint64.
func parseHeightQuery(c *gin.Context) (*uint64, bool) {
	raw, present := c.GetQuery(heightQueryParam)
	if !present {
		return nil, true
	}

	height, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest,
			gin.H{"error": fmt.Sprintf("invalid %s parameter: %s", heightQueryParam, raw)})
		return nil, false
	}
	return &height, true
}

// writeAPIError writes err as a JSON error response: 404 when nothing matched,
// 500 for any other failure.
func writeAPIError(c *gin.Context, err error) {
	code := http.StatusInternalServerError
	var notFound notFoundError
	if errors.As(err, &notFound) {
		code = http.StatusNotFound
	}
	c.JSON(code, gin.H{"error": err.Error()})
}

// certificateHeaderPerHeight resolves the certificate at height, or the last
// sent certificate when height is nil.
func certificateHeaderPerHeight(storage AggsenderStorer, height *uint64) (*types.Certificate, error) {
	var (
		cert *types.Certificate
		err  error
	)
	if height == nil {
		cert, err = storage.GetLastSentCertificate()
	} else {
		cert, err = storage.GetCertificateByHeight(*height)
	}
	if err != nil {
		return nil, fmt.Errorf("error getting certificate by height: %w", err)
	}
	if cert == nil {
		return nil, newNotFoundError("certificate not found")
	}

	return cert, nil
}

// certificateBridgeExits resolves the bridge exits of the certificate at
// height, or of the last sent certificate when height is nil.
func certificateBridgeExits(storage AggsenderStorer, height *uint64) ([]*agglayertypes.BridgeExit, error) {
	var resolvedHeight uint64
	if height == nil {
		cert, err := storage.GetLastSentCertificate()
		if err != nil {
			return nil, fmt.Errorf("error getting last sent certificate: %w", err)
		}
		if cert == nil {
			return nil, newNotFoundError("no certificate found")
		}
		resolvedHeight = cert.Header.Height
	} else {
		resolvedHeight = *height
	}

	cert, err := storage.GetCertificateByHeight(resolvedHeight)
	if err != nil {
		return nil, fmt.Errorf("error getting certificate at height %d: %w", resolvedHeight, err)
	}
	if cert == nil || cert.SignedCertificate == nil {
		return nil, newNotFoundError("certificate not found at height %d", resolvedHeight)
	}
	// Certs recovered from agglayer use a placeholder signed certificate ("na/agglayer header").
	// We don't have the actual signed cert data for these certs, so return not found.
	if cert.Header != nil && cert.Header.CertSource == types.CertificateSourceAggLayer {
		return nil, newNotFoundError("certificate not found at height %d", resolvedHeight)
	}

	var agglayerCert agglayertypes.Certificate
	if err := json.Unmarshal([]byte(*cert.SignedCertificate), &agglayerCert); err != nil {
		return nil, fmt.Errorf("failed to unmarshal certificate at height %d: %w", resolvedHeight, err)
	}
	if agglayerCert.BridgeExits == nil {
		return nil, newNotFoundError("certificate not found at height %d", resolvedHeight)
	}

	return agglayerCert.BridgeExits, nil
}
