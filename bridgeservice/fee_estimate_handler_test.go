package bridgeservice

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgeservice/feeestimate"
	mocks "github.com/agglayer/aggkit/bridgeservice/mocks"
	bridgetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	aggkitsync "github.com/agglayer/aggkit/sync"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var feeEstimateTokenAddr = common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")

// newBridgeWithFeeEstimators is a bridge service router with Config.FeeEstimators set, for the fee
// estimate handler tests. Everything else mirrors newBridgeWithMocks.
func newBridgeWithFeeEstimators(t *testing.T, networkID uint32,
	feeEstimators map[uint32]feeestimate.FeeEstimator) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	logger := log.WithFields("module", "test bridge service")
	cfg := &Config{
		Logger:        logger,
		ReadTimeout:   time.Second,
		WriteTimeout:  0,
		NetworkID:     networkID,
		FeeEstimators: feeEstimators,
	}
	b := New(cfg,
		mocks.NewAgglayerManagerUpgradeQuerier(t),
		mocks.NewL1InfoTreeSyncer(t),
		mocks.NewL2GERSyncer(t),
		mocks.NewBridger(t),
		mocks.NewClaimer(t),
		mocks.NewBridger(t),
		mocks.NewClaimer(t))
	router := gin.New()
	b.RegisterRoutes(router)
	return router
}

// testFeeEstimateGasPrice is the gas price snapshot carried by both fee estimate fixtures.
func testFeeEstimateGasPrice() feeestimate.GasPriceInfo {
	baseFee := bridgetypes.BigIntString("15000000000")
	priorityFee := bridgetypes.BigIntString("2000000000")
	return feeestimate.GasPriceInfo{
		BaseFee:     &baseFee,
		PriorityFee: &priorityFee,
		GasPrice:    bridgetypes.BigIntString("17000000000"),
		BlockNumber: 123,
	}
}

// testClaimFeeEstimateResponse is the estimator response the claim handler success cases pass
// through, with the applicable repeat-claim estimate filled in (D5 shape).
func testClaimFeeEstimateResponse(networkID uint32) *feeestimate.ClaimFeeEstimateResponse {
	wrapperExists := true
	return &feeestimate.ClaimFeeEstimateResponse{
		NetworkID:          networkID,
		OriginNetwork:      0,
		OriginTokenAddress: bridgetypes.Address(feeEstimateTokenAddr.Hex()),
		TokenClass:         feeestimate.WrappedERC20,
		WrapperExists:      &wrapperExists,
		Applicable:         feeestimate.RepeatClaim,
		GasPrice:           testFeeEstimateGasPrice(),
		FirstClaim:         nil,
		RepeatClaim: &feeestimate.FeeEstimate{
			GasUnits:    110000,
			Cost:        "1870000000000000",
			SampleCount: 37,
			Source:      "class",
		},
		Unavailable: map[feeestimate.Applicable]string{
			feeestimate.FirstClaim: "wrapper already deployed; the first-claim estimate does not apply",
		},
		ComputedAt: 1760000000,
	}
}

// testBridgeFeeEstimateResponse is the estimator response the bridge handler success cases pass
// through, one estimate for the resolved mechanism (D6 shape).
func testBridgeFeeEstimateResponse(networkID uint32,
	token common.Address) *feeestimate.BridgeFeeEstimateResponse {
	return &feeestimate.BridgeFeeEstimateResponse{
		NetworkID:    networkID,
		TokenAddress: bridgetypes.Address(token.Hex()),
		Mechanism:    feeestimate.Native,
		GasPrice:     testFeeEstimateGasPrice(),
		Estimate: feeestimate.FeeEstimate{
			GasUnits:    65000,
			Cost:        "1105000000000000",
			SampleCount: 42,
			Source:      "mechanism",
		},
		ComputedAt: 1760000000,
	}
}

// decodeFeeEstimateErrorBody decodes the {"error": "..."} body both fee estimate endpoints answer
// errors with.
func decodeFeeEstimateErrorBody(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return body.Error
}

// feeEstimateHandlerTestCase is one table case shared by both fee estimate handler tests. Cases
// that must not reach an estimator hold one with no expectations: any call would fail the mock.
type feeEstimateHandlerTestCase struct {
	description   string
	estimator     *mocks.FeeEstimator
	estimatorNet  uint32
	setup         func(m *mocks.FeeEstimator)
	url           string
	expectedCode  int
	checkMetric   bool
	expectedError string
	// expectedResult, when set, is the estimator response the 200 body must decode to.
	expectedResult any
}

// runFeeEstimateHandlerTestCases is the shared table loop for the two fee estimate handler tests:
// per case it registers the estimator for its network, sets the mock expectations, performs the
// request and asserts the status, the request counter (when checkMetric) and the body -- decoded
// with decode, which knows the endpoint's response type.
func runFeeEstimateHandlerTestCases(t *testing.T, networkID uint32, handlerID string,
	testCases []feeEstimateHandlerTestCase, decode func(*testing.T, *httptest.ResponseRecorder) any) {
	t.Helper()

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			var feeEstimators map[uint32]feeestimate.FeeEstimator
			if tc.estimator != nil {
				feeEstimators = map[uint32]feeestimate.FeeEstimator{tc.estimatorNet: tc.estimator}
			}
			if tc.setup != nil {
				tc.setup(tc.estimator)
			}
			router := newBridgeWithFeeEstimators(t, networkID, feeEstimators)

			var before float64
			if tc.checkMetric {
				before = requestMetricCount(t, handlerID, tc.expectedCode)
			}
			w := performRequest(t, router, tc.url)
			require.Equal(t, tc.expectedCode, w.Code)
			if tc.checkMetric {
				require.Equal(t, before+1, requestMetricCount(t, handlerID, tc.expectedCode))
			}

			if tc.expectedResult != nil {
				require.Equal(t, tc.expectedResult, decode(t, w))
			} else {
				require.Contains(t, decodeFeeEstimateErrorBody(t, w), tc.expectedError)
			}
		})
	}
}

// TestGetClaimFeeEstimateHandler covers GET /bridge/v1/claim-fee-estimate: the 200 pass-through of
// the estimator's response for L1 and L2, the 400 validation of every parameter (including an
// unsupported network), the fail-closed 503s and the unexpected-error 500.
func TestGetClaimFeeEstimateHandler(t *testing.T) {
	const claimFeeEstimatePath = "/bridge/v1/claim-fee-estimate"

	unusedEstimator := mocks.NewFeeEstimator(t)
	unavailableErr := fmt.Errorf("%w: no samples for bucket wrapped_erc20 (network %d, last 200 claims)",
		feeestimate.ErrFeeEstimateUnavailable, l2NetworkID)
	badRequestErr := fmt.Errorf("%w: token cannot be resolved", feeestimate.ErrFeeEstimateBadRequest)

	testCases := []feeEstimateHandlerTestCase{
		{
			description:  "success on L2 (network_id=10)",
			estimator:    mocks.NewFeeEstimator(t),
			estimatorNet: l2NetworkID,
			setup: func(m *mocks.FeeEstimator) {
				m.EXPECT().EstimateClaimFee(mock.Anything, uint32(0), feeEstimateTokenAddr).
					Return(testClaimFeeEstimateResponse(l2NetworkID), nil).Once()
			},
			url: fmt.Sprintf("%s?network_id=%d&origin_network=0&origin_token_address=%s",
				claimFeeEstimatePath, l2NetworkID, feeEstimateTokenAddr.Hex()),
			expectedCode:   http.StatusOK,
			checkMetric:    true,
			expectedResult: testClaimFeeEstimateResponse(l2NetworkID),
		},
		{
			description:  "success on L1 (network_id=0)",
			estimator:    mocks.NewFeeEstimator(t),
			estimatorNet: mainnetNetworkID,
			setup: func(m *mocks.FeeEstimator) {
				m.EXPECT().EstimateClaimFee(mock.Anything, l2NetworkID, feeEstimateTokenAddr).
					Return(testClaimFeeEstimateResponse(mainnetNetworkID), nil).Once()
			},
			url: fmt.Sprintf("%s?network_id=0&origin_network=%d&origin_token_address=%s",
				claimFeeEstimatePath, l2NetworkID, feeEstimateTokenAddr.Hex()),
			expectedCode:   http.StatusOK,
			checkMetric:    true,
			expectedResult: testClaimFeeEstimateResponse(mainnetNetworkID),
		},
		{
			description:   "missing network_id",
			estimator:     unusedEstimator,
			estimatorNet:  l2NetworkID,
			url:           fmt.Sprintf("%s?origin_network=0&origin_token_address=%s", claimFeeEstimatePath, feeEstimateTokenAddr.Hex()),
			expectedCode:  http.StatusBadRequest,
			expectedError: "network_id is mandatory",
		},
		{
			description:   "invalid network_id",
			estimator:     unusedEstimator,
			estimatorNet:  l2NetworkID,
			url:           fmt.Sprintf("%s?network_id=abc&origin_network=0&origin_token_address=%s", claimFeeEstimatePath, feeEstimateTokenAddr.Hex()),
			expectedCode:  http.StatusBadRequest,
			expectedError: "invalid network_id parameter",
		},
		{
			description:  "network_id out of uint32 range",
			estimator:    unusedEstimator,
			estimatorNet: l2NetworkID,
			url: fmt.Sprintf("%s?network_id=4294967296&origin_network=0&origin_token_address=%s",
				claimFeeEstimatePath, feeEstimateTokenAddr.Hex()),
			expectedCode:  http.StatusBadRequest,
			expectedError: "network_id value out of range for uint32",
		},
		{
			description:   "unsupported network_id",
			estimator:     unusedEstimator,
			estimatorNet:  l2NetworkID,
			url:           fmt.Sprintf("%s?network_id=99&origin_network=0&origin_token_address=%s", claimFeeEstimatePath, feeEstimateTokenAddr.Hex()),
			expectedCode:  http.StatusBadRequest,
			expectedError: "unsupported network id: 99",
		},
		{
			description:   "missing origin_network",
			estimator:     unusedEstimator,
			estimatorNet:  l2NetworkID,
			url:           fmt.Sprintf("%s?network_id=%d&origin_token_address=%s", claimFeeEstimatePath, l2NetworkID, feeEstimateTokenAddr.Hex()),
			expectedCode:  http.StatusBadRequest,
			expectedError: "origin_network is mandatory",
		},
		{
			description:   "invalid origin_network",
			estimator:     unusedEstimator,
			estimatorNet:  l2NetworkID,
			url:           fmt.Sprintf("%s?network_id=%d&origin_network=xyz&origin_token_address=%s", claimFeeEstimatePath, l2NetworkID, feeEstimateTokenAddr.Hex()),
			expectedCode:  http.StatusBadRequest,
			expectedError: "invalid origin_network parameter",
		},
		{
			description:   "missing origin_token_address",
			estimator:     unusedEstimator,
			estimatorNet:  l2NetworkID,
			url:           fmt.Sprintf("%s?network_id=%d&origin_network=0", claimFeeEstimatePath, l2NetworkID),
			expectedCode:  http.StatusBadRequest,
			expectedError: "origin_token_address is mandatory",
		},
		{
			description:  "invalid origin_token_address",
			estimator:    unusedEstimator,
			estimatorNet: l2NetworkID,
			url: fmt.Sprintf("%s?network_id=%d&origin_network=0&origin_token_address=0xzz",
				claimFeeEstimatePath, l2NetworkID),
			expectedCode:  http.StatusBadRequest,
			expectedError: "invalid origin_token_address parameter, it should be a hex address",
		},
		{
			description:  "no estimator configured for the requested network",
			estimator:    nil,
			estimatorNet: 0,
			url: fmt.Sprintf("%s?network_id=%d&origin_network=0&origin_token_address=%s",
				claimFeeEstimatePath, l2NetworkID, feeEstimateTokenAddr.Hex()),
			expectedCode:  http.StatusServiceUnavailable,
			checkMetric:   true,
			expectedError: fmt.Sprintf(errFeeEstimationNotConfigured, l2NetworkID),
		},
		{
			description:  "estimator configured for L1 only, requested L2",
			estimator:    unusedEstimator,
			estimatorNet: mainnetNetworkID,
			url: fmt.Sprintf("%s?network_id=%d&origin_network=0&origin_token_address=%s",
				claimFeeEstimatePath, l2NetworkID, feeEstimateTokenAddr.Hex()),
			expectedCode:  http.StatusServiceUnavailable,
			checkMetric:   true,
			expectedError: fmt.Sprintf(errFeeEstimationNotConfigured, l2NetworkID),
		},
		{
			description:  "estimate unavailable (no samples) fails closed with 503",
			estimator:    mocks.NewFeeEstimator(t),
			estimatorNet: l2NetworkID,
			setup: func(m *mocks.FeeEstimator) {
				m.EXPECT().EstimateClaimFee(mock.Anything, uint32(0), feeEstimateTokenAddr).
					Return(nil, unavailableErr).Once()
			},
			url: fmt.Sprintf("%s?network_id=%d&origin_network=0&origin_token_address=%s",
				claimFeeEstimatePath, l2NetworkID, feeEstimateTokenAddr.Hex()),
			expectedCode:  http.StatusServiceUnavailable,
			checkMetric:   true,
			expectedError: "fee estimate unavailable: no samples",
		},
		{
			description:  "estimator bad request maps to 400",
			estimator:    mocks.NewFeeEstimator(t),
			estimatorNet: l2NetworkID,
			setup: func(m *mocks.FeeEstimator) {
				m.EXPECT().EstimateClaimFee(mock.Anything, uint32(0), feeEstimateTokenAddr).
					Return(nil, badRequestErr).Once()
			},
			url: fmt.Sprintf("%s?network_id=%d&origin_network=0&origin_token_address=%s",
				claimFeeEstimatePath, l2NetworkID, feeEstimateTokenAddr.Hex()),
			expectedCode:  http.StatusBadRequest,
			checkMetric:   true,
			expectedError: "fee estimate bad request: token cannot be resolved",
		},
		{
			description:  "halted syncer surfaced by the estimator maps to 503",
			estimator:    mocks.NewFeeEstimator(t),
			estimatorNet: l2NetworkID,
			setup: func(m *mocks.FeeEstimator) {
				m.EXPECT().EstimateClaimFee(mock.Anything, uint32(0), feeEstimateTokenAddr).
					Return(nil, fmt.Errorf("claims syncer halted: %w", aggkitsync.ErrInconsistentState)).Once()
			},
			url: fmt.Sprintf("%s?network_id=%d&origin_network=0&origin_token_address=%s",
				claimFeeEstimatePath, l2NetworkID, feeEstimateTokenAddr.Hex()),
			expectedCode:  http.StatusServiceUnavailable,
			checkMetric:   true,
			expectedError: "claims syncer halted",
		},
		{
			description:  "unexpected estimator error maps to 500",
			estimator:    mocks.NewFeeEstimator(t),
			estimatorNet: l2NetworkID,
			setup: func(m *mocks.FeeEstimator) {
				m.EXPECT().EstimateClaimFee(mock.Anything, uint32(0), feeEstimateTokenAddr).
					Return(nil, errors.New("boom")).Once()
			},
			url: fmt.Sprintf("%s?network_id=%d&origin_network=0&origin_token_address=%s",
				claimFeeEstimatePath, l2NetworkID, feeEstimateTokenAddr.Hex()),
			expectedCode:  http.StatusInternalServerError,
			checkMetric:   true,
			expectedError: "failed to estimate claim fee: boom",
		},
		{
			description:  "nil estimate with nil error fails closed with 500",
			estimator:    mocks.NewFeeEstimator(t),
			estimatorNet: l2NetworkID,
			setup: func(m *mocks.FeeEstimator) {
				m.EXPECT().EstimateClaimFee(mock.Anything, uint32(0), feeEstimateTokenAddr).
					Return(nil, nil).Once()
			},
			url: fmt.Sprintf("%s?network_id=%d&origin_network=0&origin_token_address=%s",
				claimFeeEstimatePath, l2NetworkID, feeEstimateTokenAddr.Hex()),
			expectedCode:  http.StatusInternalServerError,
			checkMetric:   true,
			expectedError: "fee estimator returned no claim fee estimate",
		},
	}

	runFeeEstimateHandlerTestCases(t, l2NetworkID, getClaimFeeEstimateReq, testCases,
		func(t *testing.T, w *httptest.ResponseRecorder) any {
			t.Helper()
			var got feeestimate.ClaimFeeEstimateResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
			return &got
		})
}

// TestGetBridgeFeeEstimateHandler covers GET /bridge/v1/bridge-fee-estimate: the 200 pass-through
// of the estimator's response for L1 and L2 (including the 0x0 native gas token), the 400
// validation of every parameter, the fail-closed 503s and the unexpected-error 500. Cases that
// must not reach an estimator hold one with no expectations: any call would fail the mock.
func TestGetBridgeFeeEstimateHandler(t *testing.T) {
	const bridgeFeeEstimatePath = "/bridge/v1/bridge-fee-estimate"

	unusedEstimator := mocks.NewFeeEstimator(t)
	unavailableErr := fmt.Errorf("%w: no samples for mechanism native (network %d, last 200 bridges)",
		feeestimate.ErrFeeEstimateUnavailable, l2NetworkID)
	badRequestErr := fmt.Errorf("%w: token is not resolvable to a mechanism", feeestimate.ErrFeeEstimateBadRequest)

	testCases := []feeEstimateHandlerTestCase{
		{
			description:  "success on L2 (network_id=10)",
			estimator:    mocks.NewFeeEstimator(t),
			estimatorNet: l2NetworkID,
			setup: func(m *mocks.FeeEstimator) {
				m.EXPECT().EstimateBridgeFee(mock.Anything, feeEstimateTokenAddr).
					Return(testBridgeFeeEstimateResponse(l2NetworkID, feeEstimateTokenAddr), nil).Once()
			},
			url: fmt.Sprintf("%s?network_id=%d&token_address=%s",
				bridgeFeeEstimatePath, l2NetworkID, feeEstimateTokenAddr.Hex()),
			expectedCode:   http.StatusOK,
			checkMetric:    true,
			expectedResult: testBridgeFeeEstimateResponse(l2NetworkID, feeEstimateTokenAddr),
		},
		{
			description:  "success on L1 (network_id=0)",
			estimator:    mocks.NewFeeEstimator(t),
			estimatorNet: mainnetNetworkID,
			setup: func(m *mocks.FeeEstimator) {
				m.EXPECT().EstimateBridgeFee(mock.Anything, feeEstimateTokenAddr).
					Return(testBridgeFeeEstimateResponse(mainnetNetworkID, feeEstimateTokenAddr), nil).Once()
			},
			url:            fmt.Sprintf("%s?network_id=0&token_address=%s", bridgeFeeEstimatePath, feeEstimateTokenAddr.Hex()),
			expectedCode:   http.StatusOK,
			checkMetric:    true,
			expectedResult: testBridgeFeeEstimateResponse(mainnetNetworkID, feeEstimateTokenAddr),
		},
		{
			description:  "success for the native gas token (token_address=0x0)",
			estimator:    mocks.NewFeeEstimator(t),
			estimatorNet: l2NetworkID,
			setup: func(m *mocks.FeeEstimator) {
				m.EXPECT().EstimateBridgeFee(mock.Anything, common.Address{}).
					Return(testBridgeFeeEstimateResponse(l2NetworkID, common.Address{}), nil).Once()
			},
			url: fmt.Sprintf("%s?network_id=%d&token_address=0x0000000000000000000000000000000000000000",
				bridgeFeeEstimatePath, l2NetworkID),
			expectedCode:   http.StatusOK,
			checkMetric:    true,
			expectedResult: testBridgeFeeEstimateResponse(l2NetworkID, common.Address{}),
		},
		{
			description:   "missing network_id",
			estimator:     unusedEstimator,
			estimatorNet:  l2NetworkID,
			url:           fmt.Sprintf("%s?token_address=%s", bridgeFeeEstimatePath, feeEstimateTokenAddr.Hex()),
			expectedCode:  http.StatusBadRequest,
			expectedError: "network_id is mandatory",
		},
		{
			description:   "invalid network_id",
			estimator:     unusedEstimator,
			estimatorNet:  l2NetworkID,
			url:           fmt.Sprintf("%s?network_id=abc&token_address=%s", bridgeFeeEstimatePath, feeEstimateTokenAddr.Hex()),
			expectedCode:  http.StatusBadRequest,
			expectedError: "invalid network_id parameter",
		},
		{
			description:   "unsupported network_id",
			estimator:     unusedEstimator,
			estimatorNet:  l2NetworkID,
			url:           fmt.Sprintf("%s?network_id=99&token_address=%s", bridgeFeeEstimatePath, feeEstimateTokenAddr.Hex()),
			expectedCode:  http.StatusBadRequest,
			expectedError: "unsupported network id: 99",
		},
		{
			description:   "missing token_address",
			estimator:     unusedEstimator,
			estimatorNet:  l2NetworkID,
			url:           fmt.Sprintf("%s?network_id=%d", bridgeFeeEstimatePath, l2NetworkID),
			expectedCode:  http.StatusBadRequest,
			expectedError: "token_address is mandatory",
		},
		{
			description:   "invalid token_address",
			estimator:     unusedEstimator,
			estimatorNet:  l2NetworkID,
			url:           fmt.Sprintf("%s?network_id=%d&token_address=0xnotanaddress", bridgeFeeEstimatePath, l2NetworkID),
			expectedCode:  http.StatusBadRequest,
			expectedError: "invalid token_address parameter, it should be a hex address",
		},
		{
			description:  "no estimator configured for the requested network",
			estimator:    nil,
			estimatorNet: 0,
			url: fmt.Sprintf("%s?network_id=%d&token_address=%s",
				bridgeFeeEstimatePath, l2NetworkID, feeEstimateTokenAddr.Hex()),
			expectedCode:  http.StatusServiceUnavailable,
			checkMetric:   true,
			expectedError: fmt.Sprintf(errFeeEstimationNotConfigured, l2NetworkID),
		},
		{
			description:  "estimator configured for L1 only, requested L2",
			estimator:    unusedEstimator,
			estimatorNet: mainnetNetworkID,
			url: fmt.Sprintf("%s?network_id=%d&token_address=%s",
				bridgeFeeEstimatePath, l2NetworkID, feeEstimateTokenAddr.Hex()),
			expectedCode:  http.StatusServiceUnavailable,
			checkMetric:   true,
			expectedError: fmt.Sprintf(errFeeEstimationNotConfigured, l2NetworkID),
		},
		{
			description:  "estimate unavailable (no samples) fails closed with 503",
			estimator:    mocks.NewFeeEstimator(t),
			estimatorNet: l2NetworkID,
			setup: func(m *mocks.FeeEstimator) {
				m.EXPECT().EstimateBridgeFee(mock.Anything, feeEstimateTokenAddr).
					Return(nil, unavailableErr).Once()
			},
			url: fmt.Sprintf("%s?network_id=%d&token_address=%s",
				bridgeFeeEstimatePath, l2NetworkID, feeEstimateTokenAddr.Hex()),
			expectedCode:  http.StatusServiceUnavailable,
			checkMetric:   true,
			expectedError: "fee estimate unavailable: no samples",
		},
		{
			description:  "estimator bad request maps to 400",
			estimator:    mocks.NewFeeEstimator(t),
			estimatorNet: l2NetworkID,
			setup: func(m *mocks.FeeEstimator) {
				m.EXPECT().EstimateBridgeFee(mock.Anything, feeEstimateTokenAddr).
					Return(nil, badRequestErr).Once()
			},
			url: fmt.Sprintf("%s?network_id=%d&token_address=%s",
				bridgeFeeEstimatePath, l2NetworkID, feeEstimateTokenAddr.Hex()),
			expectedCode:  http.StatusBadRequest,
			checkMetric:   true,
			expectedError: "fee estimate bad request: token is not resolvable",
		},
		{
			description:  "unexpected estimator error maps to 500",
			estimator:    mocks.NewFeeEstimator(t),
			estimatorNet: l2NetworkID,
			setup: func(m *mocks.FeeEstimator) {
				m.EXPECT().EstimateBridgeFee(mock.Anything, feeEstimateTokenAddr).
					Return(nil, errors.New("boom")).Once()
			},
			url: fmt.Sprintf("%s?network_id=%d&token_address=%s",
				bridgeFeeEstimatePath, l2NetworkID, feeEstimateTokenAddr.Hex()),
			expectedCode:  http.StatusInternalServerError,
			checkMetric:   true,
			expectedError: "failed to estimate bridge fee: boom",
		},
		{
			description:  "nil estimate with nil error fails closed with 500",
			estimator:    mocks.NewFeeEstimator(t),
			estimatorNet: l2NetworkID,
			setup: func(m *mocks.FeeEstimator) {
				m.EXPECT().EstimateBridgeFee(mock.Anything, feeEstimateTokenAddr).
					Return(nil, nil).Once()
			},
			url: fmt.Sprintf("%s?network_id=%d&token_address=%s",
				bridgeFeeEstimatePath, l2NetworkID, feeEstimateTokenAddr.Hex()),
			expectedCode:  http.StatusInternalServerError,
			checkMetric:   true,
			expectedError: "fee estimator returned no bridge fee estimate",
		},
	}

	runFeeEstimateHandlerTestCases(t, l2NetworkID, getBridgeFeeEstimateReq, testCases,
		func(t *testing.T, w *httptest.ResponseRecorder) any {
			t.Helper()
			var got feeestimate.BridgeFeeEstimateResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
			return &got
		})
}

// TestHTTPStatusForSyncerError_FeeEstimateSentinels covers the fee estimate sentinel mappings in
// httpStatusForSyncerError that the fee estimate handlers rely on. The sentinels are checked
// before the syncer ones, so a fail-closed estimate stays 503 even when the underlying failure was
// a not-found or a halted syncer.
func TestHTTPStatusForSyncerError_FeeEstimateSentinels(t *testing.T) {
	testCases := []struct {
		description    string
		err            error
		expectedStatus int
	}{
		{
			description:    "ErrFeeEstimateBadRequest is a bad request",
			err:            feeestimate.ErrFeeEstimateBadRequest,
			expectedStatus: http.StatusBadRequest,
		},
		{
			description:    "ErrFeeEstimateBadRequest survives wrapping",
			err:            fmt.Errorf("claim: token cannot be resolved: %w", feeestimate.ErrFeeEstimateBadRequest),
			expectedStatus: http.StatusBadRequest,
		},
		{
			description:    "ErrFeeEstimateUnavailable fails closed with 503",
			err:            feeestimate.ErrFeeEstimateUnavailable,
			expectedStatus: http.StatusServiceUnavailable,
		},
		{
			description:    "ErrFeeEstimateUnavailable survives wrapping",
			err:            fmt.Errorf("claim: computing the claim statistics: %w", feeestimate.ErrFeeEstimateUnavailable),
			expectedStatus: http.StatusServiceUnavailable,
		},
		{
			description:    "ErrFeeEstimateUnavailable wins over a wrapped db.ErrNotFound (fail closed, not 404)",
			err:            fmt.Errorf("%w: paging claims: %w", feeestimate.ErrFeeEstimateUnavailable, db.ErrNotFound),
			expectedStatus: http.StatusServiceUnavailable,
		},
		{
			description:    "ErrFeeEstimateUnavailable wins over a wrapped halted syncer (still 503)",
			err:            fmt.Errorf("%w: claims syncer halted: %w", feeestimate.ErrFeeEstimateUnavailable, aggkitsync.ErrInconsistentState),
			expectedStatus: http.StatusServiceUnavailable,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			require.Equal(t, tc.expectedStatus, httpStatusForSyncerError(tc.err))
		})
	}
}
