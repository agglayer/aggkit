package aggsenderrpc

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newAPITestData(t *testing.T) (*AggsenderAPI, *mocks.AggsenderStorer, *mocks.AggsenderInterface) {
	t.Helper()

	mockStore := mocks.NewAggsenderStorer(t)
	mockAggsender := mocks.NewAggsenderInterface(t)
	return NewAggsenderAPI(nil, mockStore, mockAggsender), mockStore, mockAggsender
}

func newAPITestRouter(t *testing.T, api *AggsenderAPI) *gin.Engine {
	t.Helper()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	api.RegisterPublicRoutes(router)
	api.RegisterAdminRoutes(router)
	return router
}

func performRequest(t *testing.T, router *gin.Engine, method, path string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestStatusRoute(t *testing.T) {
	api, _, mockAggsender := newAPITestData(t)
	router := newAPITestRouter(t, api)

	expected := types.AggsenderInfo{
		AggsenderStatus: types.AggsenderStatus{Status: types.StatusNone},
		TriggerStatus:   "trigger",
		NetworkID:       7,
		Mode:            types.PessimisticProofMode,
	}
	mockAggsender.EXPECT().Info().Return(expected).Once()

	rec := performRequest(t, router, http.MethodGet, AggsenderV1Prefix+"/status")
	require.Equal(t, http.StatusOK, rec.Code)

	var actual types.AggsenderInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &actual))
	require.Equal(t, expected.NetworkID, actual.NetworkID)
	require.Equal(t, expected.TriggerStatus, actual.TriggerStatus)
	require.Equal(t, expected.Mode, actual.Mode)
}

func TestTriggerCertificateRoute(t *testing.T) {
	api, _, mockAggsender := newAPITestData(t)
	router := newAPITestRouter(t, api)

	mockAggsender.EXPECT().ForceTriggerCertificate().Once()

	rec := performRequest(t, router, http.MethodPost, AggsenderV1Prefix+"/trigger-certificate")
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, "null", rec.Body.String())
}

func TestCertificateHeaderRoute(t *testing.T) {
	height := uint64(1)
	header := &types.CertificateHeader{Height: height, ToBlock: 10}

	cases := []struct {
		name          string
		path          string
		setup         func(*mocks.AggsenderStorer)
		expectedCode  int
		expectedError string
	}{
		{
			name: "latest certificate",
			path: AggsenderV1Prefix + "/certificate-header",
			setup: func(storer *mocks.AggsenderStorer) {
				storer.EXPECT().GetLastSentCertificate().Return(
					&types.Certificate{Header: header}, nil).Once()
			},
			expectedCode: http.StatusOK,
		},
		{
			name: "certificate by height",
			path: AggsenderV1Prefix + "/certificate-header?height=1",
			setup: func(storer *mocks.AggsenderStorer) {
				storer.EXPECT().GetCertificateByHeight(height).Return(
					&types.Certificate{Header: header}, nil).Once()
			},
			expectedCode: http.StatusOK,
		},
		{
			name:          "invalid height",
			path:          AggsenderV1Prefix + "/certificate-header?height=abc",
			expectedCode:  http.StatusBadRequest,
			expectedError: "invalid height parameter: abc",
		},
		{
			name: "not found",
			path: AggsenderV1Prefix + "/certificate-header",
			setup: func(storer *mocks.AggsenderStorer) {
				storer.EXPECT().GetLastSentCertificate().Return(nil, nil).Once()
			},
			expectedCode:  http.StatusNotFound,
			expectedError: "certificate not found",
		},
		{
			name: "certificate without header",
			path: AggsenderV1Prefix + "/certificate-header",
			setup: func(storer *mocks.AggsenderStorer) {
				storer.EXPECT().GetLastSentCertificate().Return(&types.Certificate{}, nil).Once()
			},
			expectedCode:  http.StatusNotFound,
			expectedError: "certificate not found",
		},
		{
			name: "storage error",
			path: AggsenderV1Prefix + "/certificate-header?height=1",
			setup: func(storer *mocks.AggsenderStorer) {
				storer.EXPECT().GetCertificateByHeight(height).Return(nil, fmt.Errorf("db error")).Once()
			},
			expectedCode:  http.StatusInternalServerError,
			expectedError: "db error",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			api, mockStore, _ := newAPITestData(t)
			router := newAPITestRouter(t, api)
			if tt.setup != nil {
				tt.setup(mockStore)
			}

			rec := performRequest(t, router, http.MethodGet, tt.path)
			require.Equal(t, tt.expectedCode, rec.Code)
			if tt.expectedError != "" {
				require.Contains(t, rec.Body.String(), tt.expectedError)
			}
			if tt.expectedCode != http.StatusOK {
				return
			}

			// The public route answers the header only: no signed payload, proof or extra data.
			var actual types.CertificateHeader
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &actual))
			require.Equal(t, *header, actual)
			var raw map[string]any
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
			require.NotContains(t, raw, "SignedCertificate")
			require.NotContains(t, raw, "Header")
		})
	}
}

func TestAdminCertificateRoute(t *testing.T) {
	height := uint64(7)
	signed := `{"network_id":1,"height":7}`
	cert := &types.Certificate{
		Header:            &types.CertificateHeader{Height: height},
		SignedCertificate: &signed,
		ExtraData:         "debug",
	}

	api, mockStore, _ := newAPITestData(t)
	admin := gin.New()
	api.RegisterAdminRoutes(admin)

	mockStore.EXPECT().GetCertificateByHeight(height).Return(cert, nil).Once()

	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		AggsenderV1Prefix+"/certificate?height=7", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var actual types.Certificate
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &actual))
	require.NotNil(t, actual.SignedCertificate)
	require.Equal(t, signed, *actual.SignedCertificate)
	require.Equal(t, "debug", actual.ExtraData)
	require.Equal(t, height, actual.Header.Height)
}

func TestPublicRoutesExposeOnlyReadOnlyCertificateData(t *testing.T) {
	api, _, _ := newAPITestData(t)

	public := gin.New()
	api.RegisterPublicRoutes(public)
	admin := gin.New()
	api.RegisterAdminRoutes(admin)

	publicRoutes := map[string]struct{}{}
	for _, route := range public.Routes() {
		publicRoutes[route.Method+" "+route.Path] = struct{}{}
	}
	require.Contains(t, publicRoutes, "GET "+AggsenderV1Prefix+"/certificate-header")
	require.NotContains(t, publicRoutes, "GET "+AggsenderV1Prefix+"/certificate")
	require.NotContains(t, publicRoutes, "POST "+AggsenderV1Prefix+"/trigger-certificate")

	adminRoutes := map[string]struct{}{}
	for _, route := range admin.Routes() {
		adminRoutes[route.Method+" "+route.Path] = struct{}{}
	}
	require.Contains(t, adminRoutes, "GET "+AggsenderV1Prefix+"/certificate")
	require.Contains(t, adminRoutes, "POST "+AggsenderV1Prefix+"/trigger-certificate")
	require.NotContains(t, adminRoutes, "GET "+AggsenderV1Prefix+"/certificate-header")
	require.NotContains(t, adminRoutes, "GET "+AggsenderV1Prefix+"/status")
}

func TestCertificateBridgeExitsRoute(t *testing.T) {
	height := uint64(42)
	destinationAddress := common.HexToAddress("0xdeadbeef")
	bridgeExits := []*agglayertypes.BridgeExit{
		{
			LeafType:           0,
			DestinationNetwork: 1,
			DestinationAddress: destinationAddress,
			Amount:             big.NewInt(1000),
		},
	}

	marshalCert := func(cert agglayertypes.Certificate) *string {
		raw, err := json.Marshal(cert)
		require.NoError(t, err)
		signed := string(raw)
		return &signed
	}

	certWithExits := &types.Certificate{
		Header: &types.CertificateHeader{Height: height},
		SignedCertificate: marshalCert(agglayertypes.Certificate{
			NetworkID:   1,
			Height:      height,
			BridgeExits: bridgeExits,
		}),
	}
	certWithoutExits := &types.Certificate{
		Header: &types.CertificateHeader{Height: height},
		SignedCertificate: marshalCert(agglayertypes.Certificate{
			NetworkID: 1,
			Height:    height,
		}),
	}
	notFoundAtHeight := fmt.Sprintf("certificate not found at height %d", height)

	cases := []struct {
		name          string
		path          string
		setup         func(*mocks.AggsenderStorer)
		expectedCode  int
		expectedError string
		expectedExits bool
	}{
		{
			name: "latest certificate",
			path: AggsenderV1Prefix + "/certificate-bridge-exits",
			setup: func(storer *mocks.AggsenderStorer) {
				storer.EXPECT().GetLastSentCertificate().Return(
					&types.Certificate{Header: &types.CertificateHeader{Height: height}}, nil).Once()
				storer.EXPECT().GetCertificateByHeight(height).Return(certWithExits, nil).Once()
			},
			expectedCode:  http.StatusOK,
			expectedExits: true,
		},
		{
			name: "latest certificate error",
			path: AggsenderV1Prefix + "/certificate-bridge-exits",
			setup: func(storer *mocks.AggsenderStorer) {
				storer.EXPECT().GetLastSentCertificate().Return(nil, fmt.Errorf("db error")).Once()
			},
			expectedCode:  http.StatusInternalServerError,
			expectedError: "db error",
		},
		{
			name: "no latest certificate",
			path: AggsenderV1Prefix + "/certificate-bridge-exits",
			setup: func(storer *mocks.AggsenderStorer) {
				storer.EXPECT().GetLastSentCertificate().Return(nil, nil).Once()
			},
			expectedCode:  http.StatusNotFound,
			expectedError: "no certificate found",
		},
		{
			name: "certificate by height",
			path: AggsenderV1Prefix + "/certificate-bridge-exits?height=42",
			setup: func(storer *mocks.AggsenderStorer) {
				storer.EXPECT().GetCertificateByHeight(height).Return(certWithExits, nil).Once()
			},
			expectedCode:  http.StatusOK,
			expectedExits: true,
		},
		{
			name: "certificate by height not found",
			path: AggsenderV1Prefix + "/certificate-bridge-exits?height=42",
			setup: func(storer *mocks.AggsenderStorer) {
				storer.EXPECT().GetCertificateByHeight(height).Return(nil, nil).Once()
			},
			expectedCode:  http.StatusNotFound,
			expectedError: notFoundAtHeight,
		},
		{
			name: "certificate without signed certificate",
			path: AggsenderV1Prefix + "/certificate-bridge-exits?height=42",
			setup: func(storer *mocks.AggsenderStorer) {
				storer.EXPECT().GetCertificateByHeight(height).Return(
					&types.Certificate{Header: &types.CertificateHeader{Height: height}}, nil).Once()
			},
			expectedCode:  http.StatusNotFound,
			expectedError: notFoundAtHeight,
		},
		{
			name: "certificate without bridge exits",
			path: AggsenderV1Prefix + "/certificate-bridge-exits?height=42",
			setup: func(storer *mocks.AggsenderStorer) {
				storer.EXPECT().GetCertificateByHeight(height).Return(certWithoutExits, nil).Once()
			},
			expectedCode:  http.StatusNotFound,
			expectedError: notFoundAtHeight,
		},
		{
			name: "certificate by height storage error",
			path: AggsenderV1Prefix + "/certificate-bridge-exits?height=42",
			setup: func(storer *mocks.AggsenderStorer) {
				storer.EXPECT().GetCertificateByHeight(height).Return(nil, fmt.Errorf("storage error")).Once()
			},
			expectedCode:  http.StatusInternalServerError,
			expectedError: "storage error",
		},
		{
			name: "invalid signed certificate",
			path: AggsenderV1Prefix + "/certificate-bridge-exits?height=42",
			setup: func(storer *mocks.AggsenderStorer) {
				invalid := "not-json"
				storer.EXPECT().GetCertificateByHeight(height).Return(
					&types.Certificate{
						Header:            &types.CertificateHeader{Height: height},
						SignedCertificate: &invalid,
					}, nil).Once()
			},
			expectedCode:  http.StatusInternalServerError,
			expectedError: "failed to unmarshal certificate",
		},
		{
			name:          "invalid height",
			path:          AggsenderV1Prefix + "/certificate-bridge-exits?height=abc",
			expectedCode:  http.StatusBadRequest,
			expectedError: "invalid height parameter: abc",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			api, mockStore, _ := newAPITestData(t)
			router := newAPITestRouter(t, api)
			if tt.setup != nil {
				tt.setup(mockStore)
			}

			rec := performRequest(t, router, http.MethodGet, tt.path)
			require.Equal(t, tt.expectedCode, rec.Code)
			if tt.expectedError != "" {
				require.Contains(t, rec.Body.String(), tt.expectedError)
			}
			if tt.expectedExits {
				var actual []*agglayertypes.BridgeExit
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &actual))
				require.Len(t, actual, 1)
				require.Equal(t, destinationAddress, actual[0].DestinationAddress)
			}
		})
	}
}

func TestCertificateImportedBridgeExitsRoute(t *testing.T) {
	height := uint64(42)
	globalIndex := &agglayertypes.GlobalIndex{RollupIndex: 1, LeafIndex: 3}
	importedExits := []*agglayertypes.ImportedBridgeExit{
		{GlobalIndex: globalIndex},
	}

	marshalCert := func(cert agglayertypes.Certificate) *string {
		raw, err := json.Marshal(cert)
		require.NoError(t, err)
		signed := string(raw)
		return &signed
	}

	certWithExits := &types.Certificate{
		Header: &types.CertificateHeader{Height: height},
		SignedCertificate: marshalCert(agglayertypes.Certificate{
			NetworkID:           1,
			Height:              height,
			ImportedBridgeExits: importedExits,
		}),
	}
	certWithoutExits := &types.Certificate{
		Header: &types.CertificateHeader{Height: height},
		SignedCertificate: marshalCert(agglayertypes.Certificate{
			NetworkID: 1,
			Height:    height,
		}),
	}
	notFoundAtHeight := fmt.Sprintf("certificate not found at height %d", height)

	cases := []struct {
		name          string
		path          string
		setup         func(*mocks.AggsenderStorer)
		expectedCode  int
		expectedError string
		expectedExits bool
	}{
		{
			name: "latest certificate",
			path: AggsenderV1Prefix + "/certificate-imported-bridge-exits",
			setup: func(storer *mocks.AggsenderStorer) {
				storer.EXPECT().GetLastSentCertificate().Return(
					&types.Certificate{Header: &types.CertificateHeader{Height: height}}, nil).Once()
				storer.EXPECT().GetCertificateByHeight(height).Return(certWithExits, nil).Once()
			},
			expectedCode:  http.StatusOK,
			expectedExits: true,
		},
		{
			name: "certificate by height",
			path: AggsenderV1Prefix + "/certificate-imported-bridge-exits?height=42",
			setup: func(storer *mocks.AggsenderStorer) {
				storer.EXPECT().GetCertificateByHeight(height).Return(certWithExits, nil).Once()
			},
			expectedCode:  http.StatusOK,
			expectedExits: true,
		},
		{
			name: "no latest certificate",
			path: AggsenderV1Prefix + "/certificate-imported-bridge-exits",
			setup: func(storer *mocks.AggsenderStorer) {
				storer.EXPECT().GetLastSentCertificate().Return(nil, nil).Once()
			},
			expectedCode:  http.StatusNotFound,
			expectedError: "no certificate found",
		},
		{
			name: "certificate by height not found",
			path: AggsenderV1Prefix + "/certificate-imported-bridge-exits?height=42",
			setup: func(storer *mocks.AggsenderStorer) {
				storer.EXPECT().GetCertificateByHeight(height).Return(nil, nil).Once()
			},
			expectedCode:  http.StatusNotFound,
			expectedError: notFoundAtHeight,
		},
		{
			name: "certificate recovered from agglayer",
			path: AggsenderV1Prefix + "/certificate-imported-bridge-exits?height=42",
			setup: func(storer *mocks.AggsenderStorer) {
				storer.EXPECT().GetCertificateByHeight(height).Return(&types.Certificate{
					Header: &types.CertificateHeader{
						Height:     height,
						CertSource: types.CertificateSourceAggLayer,
					},
					SignedCertificate: certWithExits.SignedCertificate,
				}, nil).Once()
			},
			expectedCode:  http.StatusNotFound,
			expectedError: notFoundAtHeight,
		},
		{
			name: "certificate without imported bridge exits",
			path: AggsenderV1Prefix + "/certificate-imported-bridge-exits?height=42",
			setup: func(storer *mocks.AggsenderStorer) {
				storer.EXPECT().GetCertificateByHeight(height).Return(certWithoutExits, nil).Once()
			},
			expectedCode:  http.StatusNotFound,
			expectedError: notFoundAtHeight,
		},
		{
			name:          "invalid height",
			path:          AggsenderV1Prefix + "/certificate-imported-bridge-exits?height=abc",
			expectedCode:  http.StatusBadRequest,
			expectedError: "invalid height parameter: abc",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			api, mockStore, _ := newAPITestData(t)
			router := newAPITestRouter(t, api)
			if tt.setup != nil {
				tt.setup(mockStore)
			}

			rec := performRequest(t, router, http.MethodGet, tt.path)
			require.Equal(t, tt.expectedCode, rec.Code)
			if tt.expectedError != "" {
				require.Contains(t, rec.Body.String(), tt.expectedError)
			}
			if tt.expectedExits {
				var actual []*agglayertypes.ImportedBridgeExit
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &actual))
				require.Len(t, actual, 1)
				require.Equal(t, globalIndex, actual[0].GlobalIndex)
			}
		})
	}
}
