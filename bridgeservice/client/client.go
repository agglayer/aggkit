package client

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
)

// ErrNotFound is returned when a resource is not found (HTTP 404)
var ErrNotFound = errors.New("not found")

const (
	// DefaultTimeout is the default HTTP client timeout
	DefaultTimeout = 30 * time.Second
)

// Client is a client for the bridgeservice REST API
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// Config contains configuration for the client
type Config struct {
	BaseURL string
	Timeout time.Duration
}

// New creates a new bridgeservice client
func New(cfg Config) *Client {
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}

	return &Client{
		baseURL: strings.TrimSuffix(cfg.BaseURL, "/"),
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
	}
}

// GetBridgesParams contains parameters for GetBridges
type GetBridgesParams struct {
	NetworkID    uint32
	PageNumber   *uint32
	PageSize     *uint32
	DepositCount *uint64
	FromAddress  *string
	NetworkIDs   []uint32
}

// GetClaimsParams contains parameters for GetClaims
type GetClaimsParams struct {
	NetworkID        uint32
	PageNumber       *uint32
	PageSize         *uint32
	NetworkIDs       []uint32
	IncludeAllFields *bool
	GlobalIndex      *big.Int
}

// GetUnsetClaimsParams contains parameters for GetUnsetClaims
type GetUnsetClaimsParams struct {
	PageNumber  *int
	PageSize    *int
	GlobalIndex *big.Int
}

// GetSetClaimsParams contains parameters for GetSetClaims
type GetSetClaimsParams struct {
	PageNumber  *int
	PageSize    *int
	GlobalIndex *big.Int
}

// GetTokenMappingsParams contains parameters for GetTokenMappings
type GetTokenMappingsParams struct {
	NetworkID          int
	PageNumber         *int
	PageSize           *int
	OriginTokenAddress *string
}

// GetLegacyTokenMigrationsParams contains parameters for GetLegacyTokenMigrations
type GetLegacyTokenMigrationsParams struct {
	NetworkID  int
	PageNumber *int
	PageSize   *int
}

// GetRemoveGEREventsParams contains parameters for GetRemoveGEREvents
type GetRemoveGEREventsParams struct {
	GlobalExitRoot *string
	Limit          *int
}

// HealthCheck performs a health check
func (c *Client) HealthCheck(ctx context.Context) (*types.HealthCheckResponse, error) {
	var resp types.HealthCheckResponse
	if err := c.doRequest(ctx, "/", &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetBridges retrieves paginated bridge events
func (c *Client) GetBridges(ctx context.Context, params GetBridgesParams) (*types.BridgesResult, error) {
	query := url.Values{}
	query.Set("network_id", strconv.FormatUint(uint64(params.NetworkID), 10))

	if params.PageNumber != nil {
		query.Set("page_number", strconv.FormatUint(uint64(*params.PageNumber), 10))
	}
	if params.PageSize != nil {
		query.Set("page_size", strconv.FormatUint(uint64(*params.PageSize), 10))
	}
	if params.DepositCount != nil {
		query.Set("deposit_count", strconv.FormatUint(*params.DepositCount, 10))
	}
	if params.FromAddress != nil {
		query.Set("from_address", *params.FromAddress)
	}
	if len(params.NetworkIDs) > 0 {
		ids := make([]string, 0, len(params.NetworkIDs))
		for _, id := range params.NetworkIDs {
			ids = append(ids, strconv.FormatUint(uint64(id), 10))
		}
		query.Set("network_ids", strings.Join(ids, ","))
	}

	var resp types.BridgesResult
	if err := c.doRequest(ctx, "/bridge/v1/bridges?"+query.Encode(), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetClaims retrieves paginated claims
func (c *Client) GetClaims(ctx context.Context, params GetClaimsParams) (*types.ClaimsResult, error) {
	query := url.Values{}
	query.Set("network_id", strconv.FormatUint(uint64(params.NetworkID), 10))

	if params.PageNumber != nil {
		query.Set("page_number", strconv.FormatUint(uint64(*params.PageNumber), 10))
	}
	if params.PageSize != nil {
		query.Set("page_size", strconv.FormatUint(uint64(*params.PageSize), 10))
	}
	if len(params.NetworkIDs) > 0 {
		ids := make([]string, 0, len(params.NetworkIDs))
		for _, id := range params.NetworkIDs {
			ids = append(ids, strconv.FormatUint(uint64(id), 10))
		}
		query.Set("network_ids", strings.Join(ids, ","))
	}
	if params.IncludeAllFields != nil {
		query.Set("include_all_fields", strconv.FormatBool(*params.IncludeAllFields))
	}
	if params.GlobalIndex != nil {
		query.Set("global_index", params.GlobalIndex.String())
	}

	var resp types.ClaimsResult
	if err := c.doRequest(ctx, "/bridge/v1/claims?"+query.Encode(), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetUnsetClaims retrieves unset claims (L2 only)
func (c *Client) GetUnsetClaims(ctx context.Context, params GetUnsetClaimsParams) (*types.UnsetClaimsResult, error) {
	query := url.Values{}

	if params.PageNumber != nil {
		query.Set("page_number", strconv.Itoa(*params.PageNumber))
	}
	if params.PageSize != nil {
		query.Set("page_size", strconv.Itoa(*params.PageSize))
	}
	if params.GlobalIndex != nil {
		query.Set("global_index", params.GlobalIndex.String())
	}

	var resp types.UnsetClaimsResult
	if err := c.doRequest(ctx, "/bridge/v1/unset-claims?"+query.Encode(), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetSetClaims retrieves set claims (L2 only)
func (c *Client) GetSetClaims(ctx context.Context, params GetSetClaimsParams) (*types.SetClaimsResult, error) {
	query := url.Values{}

	if params.PageNumber != nil {
		query.Set("page_number", strconv.Itoa(*params.PageNumber))
	}
	if params.PageSize != nil {
		query.Set("page_size", strconv.Itoa(*params.PageSize))
	}
	if params.GlobalIndex != nil {
		query.Set("global_index", params.GlobalIndex.String())
	}

	var resp types.SetClaimsResult
	if err := c.doRequest(ctx, "/bridge/v1/set-claims?"+query.Encode(), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetTokenMappings retrieves token mappings
func (c *Client) GetTokenMappings(
	ctx context.Context, params GetTokenMappingsParams,
) (*types.TokenMappingsResult, error) {
	query := url.Values{}
	query.Set("network_id", strconv.Itoa(params.NetworkID))

	if params.PageNumber != nil {
		query.Set("page_number", strconv.Itoa(*params.PageNumber))
	}
	if params.PageSize != nil {
		query.Set("page_size", strconv.Itoa(*params.PageSize))
	}
	if params.OriginTokenAddress != nil {
		query.Set("origin_token_address", *params.OriginTokenAddress)
	}

	var resp types.TokenMappingsResult
	if err := c.doRequest(ctx, "/bridge/v1/token-mappings?"+query.Encode(), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetLegacyTokenMigrations retrieves legacy token migrations
func (c *Client) GetLegacyTokenMigrations(
	ctx context.Context, params GetLegacyTokenMigrationsParams,
) (*types.LegacyTokenMigrationsResult, error) {
	query := url.Values{}
	query.Set("network_id", strconv.Itoa(params.NetworkID))

	if params.PageNumber != nil {
		query.Set("page_number", strconv.Itoa(*params.PageNumber))
	}
	if params.PageSize != nil {
		query.Set("page_size", strconv.Itoa(*params.PageSize))
	}

	var resp types.LegacyTokenMigrationsResult
	if err := c.doRequest(ctx, "/bridge/v1/legacy-token-migrations?"+query.Encode(), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetL1InfoTreeIndex retrieves the L1 Info Tree index for a bridge
func (c *Client) GetL1InfoTreeIndex(ctx context.Context, networkID, depositCount int) (uint32, error) {
	query := url.Values{}
	query.Set("network_id", strconv.Itoa(networkID))
	query.Set("deposit_count", strconv.Itoa(depositCount))

	var index uint32
	if err := c.doRequest(ctx, "/bridge/v1/l1-info-tree-index?"+query.Encode(), &index); err != nil {
		return 0, err
	}
	return index, nil
}

// GetInjectedL1InfoLeaf retrieves an injected L1 info tree leaf.
//
// Uses doRequestAllowNotFound: when the destination network has not yet injected a global exit
// root covering the requested leaf index, the endpoint returns HTTP 404, surfaced here as
// ErrNotFound so callers can treat it as "retry later" rather than a hard error.
func (c *Client) GetInjectedL1InfoLeaf(
	ctx context.Context, networkID, leafIndex int,
) (*types.L1InfoTreeLeafResponse, error) {
	query := url.Values{}
	query.Set("network_id", strconv.Itoa(networkID))
	query.Set("leaf_index", strconv.Itoa(leafIndex))

	var resp types.L1InfoTreeLeafResponse
	if err := c.doRequestAllowNotFound(ctx, "/bridge/v1/injected-l1-info-leaf?"+query.Encode(), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetClaimProof retrieves Merkle proofs for claim verification
func (c *Client) GetClaimProof(
	ctx context.Context, networkID, leafIndex, depositCount uint32,
) (*types.ClaimProof, error) {
	query := url.Values{}
	query.Set("network_id", strconv.FormatUint(uint64(networkID), 10))
	query.Set("leaf_index", strconv.FormatUint(uint64(leafIndex), 10))
	query.Set("deposit_count", strconv.FormatUint(uint64(depositCount), 10))

	var resp types.ClaimProof
	if err := c.doRequest(ctx, "/bridge/v1/claim-proof?"+query.Encode(), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetClaimCandidatesParams contains parameters for GetClaimCandidates
type GetClaimCandidatesParams struct {
	// DestinationNetworkIDs filters candidates by destination network (mandatory, max 5)
	DestinationNetworkIDs []uint32
	// ToLER is the local exit root the proofs are built against (mandatory, hex hash)
	ToLER string
	// FromLER is an exclusive lower-bound local exit root; nil means full history
	FromLER    *string
	PageNumber *uint32
	PageSize   *uint32
}

// GetClaimCandidates retrieves bridges originated on the bridge service's own source network
// (the network that bridge service instance itself syncs) that are candidates for claiming,
// together with the Merkle proof of their inclusion in the requested local exit root (ToLER).
//
// Unlike other list endpoints, DestinationNetworkIDs is sent as a repeated query parameter
// (destination_network_ids=1&destination_network_ids=2), not comma-separated.
//
// Uses doRequestAllowNotFound: an unresolvable ToLER/FromLER (not synced yet by the source
// bridge service) results in ErrNotFound, which callers should treat as "retry later" rather
// than a hard error.
func (c *Client) GetClaimCandidates(
	ctx context.Context, params GetClaimCandidatesParams,
) (*types.ClaimCandidatesResult, error) {
	query := url.Values{}
	for _, id := range params.DestinationNetworkIDs {
		query.Add("destination_network_ids", strconv.FormatUint(uint64(id), 10))
	}
	query.Set("to_ler", params.ToLER)
	if params.FromLER != nil {
		query.Set("from_ler", *params.FromLER)
	}
	if params.PageNumber != nil {
		query.Set("page_number", strconv.FormatUint(uint64(*params.PageNumber), 10))
	}
	if params.PageSize != nil {
		query.Set("page_size", strconv.FormatUint(uint64(*params.PageSize), 10))
	}

	var resp types.ClaimCandidatesResult
	if err := c.doRequestAllowNotFound(ctx, "/bridge/v1/claim-candidates?"+query.Encode(), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetLastReorgEvent retrieves the last reorganization event
func (c *Client) GetLastReorgEvent(ctx context.Context, networkID int) (*bridgesync.LastReorg, error) {
	query := url.Values{}
	query.Set("network_id", strconv.Itoa(networkID))

	var resp bridgesync.LastReorg
	if err := c.doRequest(ctx, "/bridge/v1/last-reorg-event?"+query.Encode(), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetSyncStatus retrieves the bridge synchronization status
func (c *Client) GetSyncStatus(ctx context.Context) (*types.SyncStatus, error) {
	var resp types.SyncStatus
	if err := c.doRequest(ctx, "/bridge/v1/sync-status", &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetRemoveGEREvents retrieves removed GER (Global Exit Root) events
func (c *Client) GetRemoveGEREvents(
	ctx context.Context, params GetRemoveGEREventsParams,
) (*types.RemoveGEREventsResult, error) {
	query := url.Values{}

	if params.GlobalExitRoot != nil {
		query.Set("global_exit_root", *params.GlobalExitRoot)
	}
	if params.Limit != nil {
		query.Set("limit", strconv.Itoa(*params.Limit))
	}

	var resp types.RemoveGEREventsResult
	if err := c.doRequest(ctx, "/bridge/v1/removed-gers?"+query.Encode(), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetBridgesByContentParams holds the content fields for GetBridgesByContent
type GetBridgesByContentParams struct {
	NetworkID          uint32
	LeafType           uint8
	OriginAddress      string
	DestinationNetwork uint32
	DestinationAddress string
	Amount             *big.Int
	Metadata           []byte
}

// GetClaimsByGER retrieves all claims matching the given global exit root (0x-prefixed hex hash)
// for the specified network (0 for L1, L2 network ID otherwise).
func (c *Client) GetClaimsByGER(ctx context.Context, networkID uint32, ger string) (*types.ClaimsByGERResult, error) {
	query := url.Values{}
	query.Set("network_id", strconv.FormatUint(uint64(networkID), 10))
	query.Set("global_exit_root", ger)

	var resp types.ClaimsByGERResult
	if err := c.doRequest(ctx, "/bridge/v1/claims-by-ger?"+query.Encode(), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetBridgeByDepositCount retrieves the bridge for the given network and deposit count.
// Returns ErrNotFound if the bridge does not exist in bridge or bridge_archive.
func (c *Client) GetBridgeByDepositCount(
	ctx context.Context, networkID uint32, depositCount uint32,
) (*types.BridgeResponse, error) {
	query := url.Values{}
	query.Set("network_id", strconv.FormatUint(uint64(networkID), 10))
	query.Set("deposit_count", strconv.FormatUint(uint64(depositCount), 10))

	var resp types.BridgeResponse
	if err := c.doRequestAllowNotFound(ctx, "/bridge/v1/bridge-by-deposit-count?"+query.Encode(), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetBridgesByContent retrieves bridges matching the given content fields for the specified network.
func (c *Client) GetBridgesByContent(
	ctx context.Context, params GetBridgesByContentParams,
) (*types.BridgesByContentResult, error) {
	query := url.Values{}
	query.Set("network_id", strconv.FormatUint(uint64(params.NetworkID), 10))
	query.Set("leaf_type", strconv.FormatUint(uint64(params.LeafType), 10))
	query.Set("origin_address", params.OriginAddress)
	query.Set("destination_network", strconv.FormatUint(uint64(params.DestinationNetwork), 10))
	query.Set("destination_address", params.DestinationAddress)
	if params.Amount != nil {
		query.Set("amount", params.Amount.String())
	} else {
		query.Set("amount", "0")
	}
	if len(params.Metadata) > 0 {
		query.Set("metadata", "0x"+hex.EncodeToString(params.Metadata))
	}

	var resp types.BridgesByContentResult
	if err := c.doRequest(ctx, "/bridge/v1/bridges-by-content?"+query.Encode(), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// doRequest performs an HTTP GET request and decodes the response
func (c *Client) doRequest(ctx context.Context, path string, result interface{}) error {
	reqURL := c.baseURL + path

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(respBody))
	}

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}

	return nil
}

// doRequestAllowNotFound performs an HTTP GET request and decodes the response.
// Returns ErrNotFound for HTTP 404 responses instead of an error with status code.
func (c *Client) doRequestAllowNotFound(ctx context.Context, path string, result interface{}) error {
	reqURL := c.baseURL + path

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(respBody))
	}

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}

	return nil
}
