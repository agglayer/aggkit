package aggsender

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/log"
	"github.com/stretchr/testify/require"
)

func TestGetBridgesAndClaims(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	mockL2Syncer := mocks.NewL2BridgeSyncer(t)
	fm := &flowManager{
		l2Syncer: mockL2Syncer,
		log:      log.WithFields("flowManager", "TestGetBridgesAndClaims"),
	}

	testCases := []struct {
		name            string
		fromBlock       uint64
		toBlock         uint64
		mockFn          func()
		expectedBridges []bridgesync.Bridge
		expectedClaims  []bridgesync.Claim
		expectedError   string
	}{
		{
			name:      "error getting bridges",
			fromBlock: 1,
			toBlock:   10,
			mockFn: func() {
				mockL2Syncer.On("GetBridgesPublished", ctx, uint64(1), uint64(10)).Return(nil, errors.New("some error"))
			},
			expectedError: "some error",
		},
		{
			name:      "no bridges consumed",
			fromBlock: 1,
			toBlock:   10,
			mockFn: func() {
				mockL2Syncer.On("GetBridgesPublished", ctx, uint64(1), uint64(10)).Return([]bridgesync.Bridge{}, nil)
			},
			expectedBridges: nil,
			expectedClaims:  nil,
		},
		{
			name:      "error getting claims",
			fromBlock: 1,
			toBlock:   10,
			mockFn: func() {
				mockL2Syncer.On("GetBridgesPublished", ctx, uint64(1), uint64(10)).Return([]bridgesync.Bridge{{}}, nil)
				mockL2Syncer.On("GetClaims", ctx, uint64(1), uint64(10)).Return(nil, errors.New("some error"))
			},
			expectedError: "some error",
		},
		{
			name:      "no claims consumed",
			fromBlock: 1,
			toBlock:   10,
			mockFn: func() {
				mockL2Syncer.On("GetBridgesPublished", ctx, uint64(1), uint64(10)).Return([]bridgesync.Bridge{{}}, nil)
				mockL2Syncer.On("GetClaims", ctx, uint64(1), uint64(10)).Return([]bridgesync.Claim{}, nil)
			},
			expectedBridges: []bridgesync.Bridge{{}},
			expectedClaims:  []bridgesync.Claim{},
		},
		{
			name:      "success",
			fromBlock: 1,
			toBlock:   10,
			mockFn: func() {
				mockL2Syncer.On("GetBridgesPublished", ctx, uint64(1), uint64(10)).Return([]bridgesync.Bridge{{}}, nil)
				mockL2Syncer.On("GetClaims", ctx, uint64(1), uint64(10)).Return([]bridgesync.Claim{{}}, nil)
			},
			expectedBridges: []bridgesync.Bridge{{}},
			expectedClaims:  []bridgesync.Claim{{}},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			mockL2Syncer.ExpectedCalls = nil
			tc.mockFn()

			bridges, claims, err := fm.getBridgesAndClaims(ctx, tc.fromBlock, tc.toBlock)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedBridges, bridges)
				require.Equal(t, tc.expectedClaims, claims)
			}
		})
	}
}

func Test_PPFlow_GetCertificateBuildParams(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	mockStorage := mocks.NewAggSenderStorage(t)
	mockL2Syncer := mocks.NewL2BridgeSyncer(t)
	ppFlow := newPPFlow(log.WithFields("flowManager", "Test_PPFlow_GetCertificateBuildParams"), mockStorage, mockL2Syncer)

	testCases := []struct {
		name           string
		mockFn         func()
		expectedParams *types.CertificateBuildParams
		expectedError  string
	}{
		{
			name: "error getting last processed block",
			mockFn: func() {
				mockL2Syncer.On("GetLastProcessedBlock", ctx).Return(uint64(0), errors.New("some error"))
			},
			expectedError: "error getting last processed block from l2: some error",
		},
		{
			name: "error getting last sent certificate",
			mockFn: func() {
				mockL2Syncer.On("GetLastProcessedBlock", ctx).Return(uint64(10), nil)
				mockStorage.On("GetLastSentCertificate").Return(nil, errors.New("some error"))
			},
			expectedError: "some error",
		},
		{
			name: "no new blocks to send a certificate",
			mockFn: func() {
				mockL2Syncer.On("GetLastProcessedBlock", ctx).Return(uint64(10), nil)
				mockStorage.On("GetLastSentCertificate").Return(&types.CertificateInfo{ToBlock: 10}, nil)
			},
			expectedParams: nil,
		},
		{
			name: "error getting bridges and claims",
			mockFn: func() {
				mockL2Syncer.On("GetLastProcessedBlock", ctx).Return(uint64(10), nil)
				mockStorage.On("GetLastSentCertificate").Return(&types.CertificateInfo{ToBlock: 5}, nil)
				mockL2Syncer.On("GetBridgesPublished", ctx, uint64(6), uint64(10)).Return(nil, errors.New("some error"))
			},
			expectedError: "some error",
		},
		{
			name: "success",
			mockFn: func() {
				mockL2Syncer.On("GetLastProcessedBlock", ctx).Return(uint64(10), nil)
				mockStorage.On("GetLastSentCertificate").Return(&types.CertificateInfo{ToBlock: 5}, nil)
				mockL2Syncer.On("GetBridgesPublished", ctx, uint64(6), uint64(10)).Return([]bridgesync.Bridge{{}}, nil)
				mockL2Syncer.On("GetClaims", ctx, uint64(6), uint64(10)).Return([]bridgesync.Claim{{}}, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:              6,
				ToBlock:                10,
				RetryCount:             0,
				ShouldBuildCertificate: true,
				LastSentCertificate:    &types.CertificateInfo{ToBlock: 5},
				Bridges:                []bridgesync.Bridge{{}},
				Claims:                 []bridgesync.Claim{{}},
				CreatedAt:              uint32(time.Now().UTC().Unix()),
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			mockL2Syncer.ExpectedCalls = nil
			mockStorage.ExpectedCalls = nil
			tc.mockFn()

			params, err := ppFlow.GetCertificateBuildParams(ctx)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedParams, params)
			}
		})
	}
}

func TestGetLastSentBlockAndRetryCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                    string
		lastSentCertificateInfo *types.CertificateInfo
		expectedBlock           uint64
		expectedRetryCount      int
	}{
		{
			name:                    "No last sent certificate",
			lastSentCertificateInfo: nil,
			expectedBlock:           0,
			expectedRetryCount:      0,
		},
		{
			name: "Last sent certificate with no error",
			lastSentCertificateInfo: &types.CertificateInfo{
				ToBlock: 10,
				Status:  agglayer.Settled,
			},
			expectedBlock:      10,
			expectedRetryCount: 0,
		},
		{
			name: "Last sent certificate with error and non-zero FromBlock",
			lastSentCertificateInfo: &types.CertificateInfo{
				FromBlock:  5,
				ToBlock:    10,
				Status:     agglayer.InError,
				RetryCount: 1,
			},
			expectedBlock:      4,
			expectedRetryCount: 2,
		},
		{
			name: "Last sent certificate with error and zero FromBlock",
			lastSentCertificateInfo: &types.CertificateInfo{
				FromBlock:  0,
				ToBlock:    10,
				Status:     agglayer.InError,
				RetryCount: 1,
			},
			expectedBlock:      10,
			expectedRetryCount: 2,
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			block, retryCount := getLastSentBlockAndRetryCount(tt.lastSentCertificateInfo)

			require.Equal(t, tt.expectedBlock, block)
			require.Equal(t, tt.expectedRetryCount, retryCount)
		})
	}
}

func Test_AggchainProverFlow_GetCertificateBuildParams(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	mockStorage := mocks.NewAggSenderStorage(t)
	mockL2Syncer := mocks.NewL2BridgeSyncer(t)
	mockAggchainProofClient := mocks.NewAggchainProofClientInterface(t)
	aggchainFlow := newAggchainProverFlow(log.WithFields("flowManager", "Test_AggchainProverFlow_GetCertificateBuildParams"), mockAggchainProofClient, mockStorage, mockL2Syncer)

	testCases := []struct {
		name           string
		mockFn         func()
		expectedParams *types.CertificateBuildParams
		expectedError  string
	}{
		{
			name: "error getting last sent certificate",
			mockFn: func() {
				mockStorage.On("GetLastSentCertificate").Return(nil, errors.New("some error"))
			},
			expectedError: "some error",
		},
		{
			name: "resend InError certificate with no bridges",
			mockFn: func() {
				mockStorage.On("GetLastSentCertificate").Return(&types.CertificateInfo{
					FromBlock: 1,
					ToBlock:   10,
					Status:    agglayer.InError,
				}, nil)
				mockL2Syncer.On("GetBridgesPublished", ctx, uint64(1), uint64(10)).Return([]bridgesync.Bridge{}, nil)
			},
			expectedError: "no bridges to resend the same certificate",
		},
		{
			name: "resend InError certificate with no auth proof",
			mockFn: func() {
				mockStorage.On("GetLastSentCertificate").Return(&types.CertificateInfo{
					FromBlock: 1,
					ToBlock:   10,
					Status:    agglayer.InError,
				}, nil)
				mockL2Syncer.On("GetBridgesPublished", ctx, uint64(1), uint64(10)).Return([]bridgesync.Bridge{{}}, nil)
				mockL2Syncer.On("GetClaims", ctx, uint64(1), uint64(10)).Return([]bridgesync.Claim{{}}, nil)
				mockAggchainProofClient.On("FetchAggchainProof", uint64(1), uint64(10)).Return(&types.AggchainProof{Proof: "some-proof"}, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:              1,
				ToBlock:                10,
				RetryCount:             1,
				ShouldBuildCertificate: true,
				Bridges:                []bridgesync.Bridge{{}},
				Claims:                 []bridgesync.Claim{{}},
				LastSentCertificate: &types.CertificateInfo{
					FromBlock: 1,
					ToBlock:   10,
					Status:    agglayer.InError,
					AuthProof: "some-proof",
				},
			},
		},
		{
			name: "resend InError certificate with auth proof",
			mockFn: func() {
				mockStorage.On("GetLastSentCertificate").Return(&types.CertificateInfo{
					FromBlock: 1,
					ToBlock:   10,
					Status:    agglayer.InError,
					AuthProof: "existing-proof",
				}, nil)
				mockL2Syncer.On("GetBridgesPublished", ctx, uint64(1), uint64(10)).Return([]bridgesync.Bridge{{}}, nil)
				mockL2Syncer.On("GetClaims", ctx, uint64(1), uint64(10)).Return([]bridgesync.Claim{{}}, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:              1,
				ToBlock:                10,
				RetryCount:             1,
				ShouldBuildCertificate: true,
				Bridges:                []bridgesync.Bridge{{}},
				Claims:                 []bridgesync.Claim{{}},
				LastSentCertificate: &types.CertificateInfo{
					FromBlock: 1,
					ToBlock:   10,
					Status:    agglayer.InError,
					AuthProof: "existing-proof",
				},
			},
		},
		{
			name: "error fetching aggchain proof for new certificate",
			mockFn: func() {
				mockStorage.On("GetLastSentCertificate").Return(nil, nil).Twice()
				mockL2Syncer.On("GetLastProcessedBlock", ctx).Return(uint64(10), nil)
				mockL2Syncer.On("GetBridgesPublished", ctx, uint64(1), uint64(10)).Return([]bridgesync.Bridge{{}}, nil)
				mockL2Syncer.On("GetClaims", ctx, uint64(1), uint64(10)).Return([]bridgesync.Claim{{}}, nil)
				mockAggchainProofClient.On("FetchAggchainProof", uint64(1), uint64(10)).Return(nil, errors.New("some error"))
			},
			expectedError: "error fetching aggchain proof for block range 1 : 10 : some error",
		},
		{
			name: "success fetching aggchain proof for new certificate",
			mockFn: func() {
				mockStorage.On("GetLastSentCertificate").Return(&types.CertificateInfo{ToBlock: 5}, nil).Twice()
				mockL2Syncer.On("GetLastProcessedBlock", ctx).Return(uint64(10), nil)
				mockL2Syncer.On("GetBridgesPublished", ctx, uint64(6), uint64(10)).Return([]bridgesync.Bridge{{}}, nil)
				mockL2Syncer.On("GetClaims", ctx, uint64(6), uint64(10)).Return([]bridgesync.Claim{{}}, nil)
				mockAggchainProofClient.On("FetchAggchainProof", uint64(6), uint64(10)).Return(&types.AggchainProof{Proof: "some-proof"}, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:              6,
				ToBlock:                10,
				RetryCount:             0,
				ShouldBuildCertificate: true,
				LastSentCertificate:    &types.CertificateInfo{ToBlock: 5, AuthProof: "some-proof"},
				Bridges:                []bridgesync.Bridge{{}},
				Claims:                 []bridgesync.Claim{{}},
				CreatedAt:              uint32(time.Now().UTC().Unix()),
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			mockStorage.ExpectedCalls = nil
			mockL2Syncer.ExpectedCalls = nil
			mockAggchainProofClient.ExpectedCalls = nil
			tc.mockFn()

			params, err := aggchainFlow.GetCertificateBuildParams(ctx)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedParams, params)
			}
		})
	}
}
