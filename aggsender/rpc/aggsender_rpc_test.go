package aggsenderrpc

import (
	"fmt"
	"math/big"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestAggsenderRPCStatus(t *testing.T) {
	testData := newAggsenderData(t)
	testData.mockAggsender.EXPECT().Info().Return(types.AggsenderInfo{})
	res, err := testData.sut.Status()
	require.NoError(t, err)
	require.NotNil(t, res)
}
func TestAggsenderRPCTriggerCertificate(t *testing.T) {
	testData := newAggsenderData(t)
	testData.mockAggsender.EXPECT().ForceTriggerCertificate().Return()
	res, err := testData.sut.TriggerCertificate()
	require.NoError(t, err)
	require.Nil(t, res)
}

func TestAggsenderRPCGetCertificateHeaderPerHeight(t *testing.T) {
	testData := newAggsenderData(t)
	height := uint64(1)
	cases := []struct {
		name          string
		height        *uint64
		certResult    *types.Certificate
		certError     error
		expectedError string
		expectedNil   bool
	}{
		{
			name: "latest, no error",
			certResult: &types.Certificate{
				Header: &types.CertificateHeader{},
			},
			certError: nil,
		},
		{
			name:          "latest,no error, no cert",
			certResult:    nil,
			certError:     nil,
			expectedError: "not found",
			expectedNil:   true,
		},
		{
			name: "latest,error",
			certResult: &types.Certificate{
				Header: &types.CertificateHeader{},
			},
			certError:     fmt.Errorf("my_error"),
			expectedError: "my_error",
			expectedNil:   true,
		},
		{
			name:   "hight, no error",
			height: &height,
			certResult: &types.Certificate{
				Header: &types.CertificateHeader{},
			},
			certError: nil,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if tt.height == nil {
				testData.mockStore.EXPECT().GetLastSentCertificate().Return(tt.certResult, tt.certError).Once()
			} else {
				testData.mockStore.EXPECT().GetCertificateByHeight(*tt.height).Return(tt.certResult, tt.certError).Once()
			}
			res, err := testData.sut.GetCertificateHeaderPerHeight(tt.height)
			if tt.expectedError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectedError)
			} else {
				require.NoError(t, err)
			}
			if tt.expectedNil {
				require.Nil(t, res)
			} else {
				require.NotNil(t, res)
			}
		})
	}
}

func TestAggsenderRPCGetCertificateBridgeExits(t *testing.T) {
	height := uint64(42)
	bridgeExits := []*agglayertypes.BridgeExit{
		{
			LeafType:           0,
			DestinationNetwork: 1,
			DestinationAddress: common.HexToAddress("0xdeadbeef"),
			Amount:             big.NewInt(1000),
		},
	}

	cases := []struct {
		name                  string
		height                *uint64
		lastCertResult        *types.Certificate
		lastCertError         error
		bridgeExitsResult     []*agglayertypes.BridgeExit
		bridgeExitsError      error
		expectedErrorCode     int
		expectedErrorContains string
		expectNil             bool
	}{
		{
			name:              "nil height, resolves last cert then returns exits",
			height:            nil,
			lastCertResult:    &types.Certificate{Header: &types.CertificateHeader{Height: height}},
			bridgeExitsResult: bridgeExits,
		},
		{
			name:                  "nil height, GetLastSentCertificate error",
			height:                nil,
			lastCertError:         fmt.Errorf("db error"),
			expectedErrorContains: "db error",
			expectNil:             true,
		},
		{
			name:                  "nil height, no last cert found",
			height:                nil,
			lastCertResult:        nil,
			expectedErrorContains: "no certificate found",
			expectNil:             true,
		},
		{
			name:              "specific height, returns exits",
			height:            &height,
			bridgeExitsResult: bridgeExits,
		},
		{
			name:                  "specific height, bridge exits nil (not found)",
			height:                &height,
			bridgeExitsResult:     nil,
			expectedErrorContains: fmt.Sprintf("certificate not found at height %d", height),
			expectNil:             true,
		},
		{
			name:                  "specific height, storage error",
			height:                &height,
			bridgeExitsError:      fmt.Errorf("storage error"),
			expectedErrorContains: "storage error",
			expectNil:             true,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			testData := newAggsenderData(t)
			var resolvedHeight uint64
			if tt.height == nil {
				testData.mockStore.EXPECT().GetLastSentCertificate().
					Return(tt.lastCertResult, tt.lastCertError).Once()
				if tt.lastCertResult != nil && tt.lastCertError == nil {
					resolvedHeight = tt.lastCertResult.Header.Height
					testData.mockStore.EXPECT().GetCertificateBridgeExits(resolvedHeight).
						Return(tt.bridgeExitsResult, tt.bridgeExitsError).Once()
				}
			} else {
				testData.mockStore.EXPECT().GetCertificateBridgeExits(*tt.height).
					Return(tt.bridgeExitsResult, tt.bridgeExitsError).Once()
			}

			res, rpcErr := testData.sut.GetCertificateBridgeExits(tt.height)
			if tt.expectedErrorContains != "" {
				require.NotNil(t, rpcErr)
				require.Contains(t, rpcErr.Error(), tt.expectedErrorContains)
			} else {
				require.Nil(t, rpcErr)
			}
			if tt.expectNil {
				require.Nil(t, res)
			} else {
				require.NotNil(t, res)
			}
		})
	}
}

type aggsenderRPCTestData struct {
	sut           *AggsenderRPC
	mockStore     *mocks.AggsenderStorer
	mockAggsender *mocks.AggsenderInterface
}

func newAggsenderData(t *testing.T) *aggsenderRPCTestData {
	t.Helper()
	mockStore := mocks.NewAggsenderStorer(t)
	mockAggsender := mocks.NewAggsenderInterface(t)
	sut := NewAggsenderRPC(nil, mockStore, mockAggsender)
	return &aggsenderRPCTestData{sut, mockStore, mockAggsender}
}
