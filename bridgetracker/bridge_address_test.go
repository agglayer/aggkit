package bridgetracker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/agglayer/aggkit/bridgetracker/api"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

var (
	testBridgeAddressNetwork0 = common.HexToAddress("0x2222222222222222222222222222222222222222")
	testBridgeAddressNetwork1 = common.HexToAddress("0x3333333333333333333333333333333333333333")
)

// fakeBridgeAddressResolver is a hand-rolled BridgeAddressResolver for tests: addresses maps
// networkID -> address (BridgeAddress errors if networkID is absent, unless err is set, in
// which case every call fails with it), networkIDs is returned as-is by NetworkIDs.
type fakeBridgeAddressResolver struct {
	networkIDs []uint32
	addresses  map[uint32]common.Address
	err        error
}

func (f *fakeBridgeAddressResolver) NetworkIDs() []uint32 {
	return f.networkIDs
}

func (f *fakeBridgeAddressResolver) BridgeAddress(_ context.Context, networkID uint32) (common.Address, error) {
	if f.err != nil {
		return common.Address{}, f.err
	}
	addr, ok := f.addresses[networkID]
	if !ok {
		return common.Address{}, errors.New("no bridge contract address configured for network")
	}
	return addr, nil
}

// TestBridgeAddressHandlerNotRegisteredWithoutResolver verifies both bridge-address endpoints
// are absent (404) when Config.BridgeAddressResolver is left nil
func TestBridgeAddressHandlerNotRegisteredWithoutResolver(t *testing.T) {
	_, router := newTestTracker(t)

	resp := performRequest(t, router, http.MethodGet, api.TrackerV1Prefix+"/bridge-address")
	require.Equal(t, http.StatusNotFound, resp.Code)

	resp = performRequest(t, router, http.MethodGet, api.TrackerV1Prefix+"/bridge-address/1")
	require.Equal(t, http.StatusNotFound, resp.Code)
}

// TestBridgeAddressHandlerAllNetworks verifies GET /bridge-address reports the bridge contract
// address of every network the resolver currently knows about
func TestBridgeAddressHandlerAllNetworks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tracker := New(&Config{
		Logger:     log.WithFields("module", "bridgetracker_test"),
		ConfigSHA1: testConfigSHA1,
		BridgeAddressResolver: &fakeBridgeAddressResolver{
			networkIDs: []uint32{0, 1},
			addresses:  map[uint32]common.Address{0: testBridgeAddressNetwork0, 1: testBridgeAddressNetwork1},
		},
	})
	router := gin.New()
	tracker.API().RegisterRoutes(router)

	resp := performRequest(t, router, http.MethodGet, api.TrackerV1Prefix+"/bridge-address")
	require.Equal(t, http.StatusOK, resp.Code)

	var body api.BridgeAddressResponse
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))
	require.Equal(t, []api.BridgeAddressItem{
		{NetworkID: 0, BridgeAddress: testBridgeAddressNetwork0},
		{NetworkID: 1, BridgeAddress: testBridgeAddressNetwork1},
	}, body.Bridges)
}

// TestBridgeAddressHandlerSingleNetwork verifies GET /bridge-address/{network_id} reports only
// the requested network's bridge contract address
func TestBridgeAddressHandlerSingleNetwork(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tracker := New(&Config{
		Logger:     log.WithFields("module", "bridgetracker_test"),
		ConfigSHA1: testConfigSHA1,
		BridgeAddressResolver: &fakeBridgeAddressResolver{
			networkIDs: []uint32{0, 1},
			addresses:  map[uint32]common.Address{0: testBridgeAddressNetwork0, 1: testBridgeAddressNetwork1},
		},
	})
	router := gin.New()
	tracker.API().RegisterRoutes(router)

	resp := performRequest(t, router, http.MethodGet, api.TrackerV1Prefix+"/bridge-address/1")
	require.Equal(t, http.StatusOK, resp.Code)

	var body api.BridgeAddressItem
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))
	require.Equal(t, api.BridgeAddressItem{NetworkID: 1, BridgeAddress: testBridgeAddressNetwork1}, body)
}

// TestBridgeAddressHandlerInvalidNetworkID verifies a non-numeric network_id is rejected with 400
func TestBridgeAddressHandlerInvalidNetworkID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tracker := New(&Config{
		Logger:                log.WithFields("module", "bridgetracker_test"),
		ConfigSHA1:            testConfigSHA1,
		BridgeAddressResolver: &fakeBridgeAddressResolver{},
	})
	router := gin.New()
	tracker.API().RegisterRoutes(router)

	resp := performRequest(t, router, http.MethodGet, api.TrackerV1Prefix+"/bridge-address/foo")
	require.Equal(t, http.StatusBadRequest, resp.Code)

	var errData types.ErrorData
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &errData))
	require.Equal(t, http.StatusBadRequest, errData.Code)
	require.Contains(t, errData.Message, "network_id")
}

// TestBridgeAddressHandlerResolverFailure verifies a resolver failure surfaces as 500
func TestBridgeAddressHandlerResolverFailure(t *testing.T) {
	wantErr := errors.New("rollup manager call failed")

	gin.SetMode(gin.TestMode)
	tracker := New(&Config{
		Logger:     log.WithFields("module", "bridgetracker_test"),
		ConfigSHA1: testConfigSHA1,
		BridgeAddressResolver: &fakeBridgeAddressResolver{
			networkIDs: []uint32{1},
			err:        wantErr,
		},
	})
	router := gin.New()
	tracker.API().RegisterRoutes(router)

	resp := performRequest(t, router, http.MethodGet, api.TrackerV1Prefix+"/bridge-address/1")
	require.Equal(t, http.StatusInternalServerError, resp.Code)

	resp = performRequest(t, router, http.MethodGet, api.TrackerV1Prefix+"/bridge-address")
	require.Equal(t, http.StatusInternalServerError, resp.Code)
}
