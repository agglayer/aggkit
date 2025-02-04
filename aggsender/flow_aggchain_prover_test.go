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
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func Test_AggchainProverFlow_GetCertificateBuildParams(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	testCases := []struct {
		name           string
		mockFn         func(*mocks.AggSenderStorage, *mocks.L2BridgeSyncer, *mocks.AggchainProofClientInterface)
		expectedParams *types.CertificateBuildParams
		expectedError  string
	}{
		{
			name: "error getting last sent certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockClient *mocks.AggchainProofClientInterface) {
				mockStorage.On("GetLastSentCertificate").Return(nil, errors.New("some error"))
			},
			expectedError: "some error",
		},
		{
			name: "resend InError certificate with no bridges",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockClient *mocks.AggchainProofClientInterface) {
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
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockClient *mocks.AggchainProofClientInterface) {
				mockStorage.On("GetLastSentCertificate").Return(&types.CertificateInfo{
					FromBlock: 1,
					ToBlock:   10,
					Status:    agglayer.InError,
				}, nil)
				mockL2Syncer.On("GetBridgesPublished", ctx, uint64(1), uint64(10)).Return([]bridgesync.Bridge{{}}, nil)
				mockL2Syncer.On("GetClaims", ctx, uint64(1), uint64(10)).Return([]bridgesync.Claim{{}}, nil)
				mockClient.On("GenerateAggchainProof", uint64(1), uint64(10), common.Hash{}, common.Hash{}, [32]common.Hash{}).Return(&types.AggchainProof{
					Proof: []byte("some-proof"), StartBlock: 1, EndBlock: 10}, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:     1,
				ToBlock:       10,
				RetryCount:    1,
				Bridges:       []bridgesync.Bridge{{}},
				Claims:        []bridgesync.Claim{{}},
				AggchainProof: []byte("some-proof"),
				LastSentCertificate: &types.CertificateInfo{
					FromBlock: 1,
					ToBlock:   10,
					Status:    agglayer.InError,
				},
			},
		},
		{
			name: "resend InError certificate with no auth proof - aggchain prover returned smaller range",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockClient *mocks.AggchainProofClientInterface) {
				mockStorage.On("GetLastSentCertificate").Return(&types.CertificateInfo{
					FromBlock: 1,
					ToBlock:   10,
					Status:    agglayer.InError,
				}, nil)
				mockL2Syncer.On("GetBridgesPublished", ctx, uint64(1), uint64(10)).Return([]bridgesync.Bridge{
					{BlockNum: 5}, {BlockNum: 10}}, nil)
				mockL2Syncer.On("GetClaims", ctx, uint64(1), uint64(10)).Return([]bridgesync.Claim{
					{BlockNum: 6}, {BlockNum: 9}}, nil)
				mockClient.On("GenerateAggchainProof", uint64(1), uint64(10), common.Hash{}, common.Hash{}, [32]common.Hash{}).Return(&types.AggchainProof{
					Proof: []byte("some-proof"), StartBlock: 1, EndBlock: 8}, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:     1,
				ToBlock:       8,
				RetryCount:    1,
				Bridges:       []bridgesync.Bridge{{BlockNum: 5}},
				Claims:        []bridgesync.Claim{{BlockNum: 6}},
				AggchainProof: []byte("some-proof"),
				LastSentCertificate: &types.CertificateInfo{
					FromBlock: 1,
					ToBlock:   10,
					Status:    agglayer.InError,
				},
			},
		},
		{
			name: "resend InError certificate with auth proof",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockClient *mocks.AggchainProofClientInterface) {
				mockStorage.On("GetLastSentCertificate").Return(&types.CertificateInfo{
					FromBlock:     1,
					ToBlock:       10,
					Status:        agglayer.InError,
					AggchainProof: []byte("existing-proof"),
				}, nil)
				mockL2Syncer.On("GetBridgesPublished", ctx, uint64(1), uint64(10)).Return([]bridgesync.Bridge{{}}, nil)
				mockL2Syncer.On("GetClaims", ctx, uint64(1), uint64(10)).Return([]bridgesync.Claim{{}}, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:     1,
				ToBlock:       10,
				RetryCount:    1,
				Bridges:       []bridgesync.Bridge{{}},
				Claims:        []bridgesync.Claim{{}},
				AggchainProof: []byte("existing-proof"),
				LastSentCertificate: &types.CertificateInfo{
					FromBlock:     1,
					ToBlock:       10,
					Status:        agglayer.InError,
					AggchainProof: []byte("existing-proof"),
				},
			},
		},
		{
			name: "error fetching aggchain proof for new certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockClient *mocks.AggchainProofClientInterface) {
				mockStorage.On("GetLastSentCertificate").Return(nil, nil).Twice()
				mockL2Syncer.On("GetLastProcessedBlock", ctx).Return(uint64(10), nil)
				mockL2Syncer.On("GetBridgesPublished", ctx, uint64(1), uint64(10)).Return([]bridgesync.Bridge{{}}, nil)
				mockL2Syncer.On("GetClaims", ctx, uint64(1), uint64(10)).Return([]bridgesync.Claim{{}}, nil)
				mockClient.On("GenerateAggchainProof", uint64(1), uint64(10), common.Hash{}, common.Hash{}, [32]common.Hash{}).Return(nil, errors.New("some error"))
			},
			expectedError: "error fetching aggchain proof for block range 1 : 10 : some error",
		},
		{
			name: "success fetching aggchain proof for new certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockClient *mocks.AggchainProofClientInterface) {
				mockStorage.On("GetLastSentCertificate").Return(&types.CertificateInfo{ToBlock: 5}, nil).Twice()
				mockL2Syncer.On("GetLastProcessedBlock", ctx).Return(uint64(10), nil)
				mockL2Syncer.On("GetBridgesPublished", ctx, uint64(6), uint64(10)).Return([]bridgesync.Bridge{{}}, nil)
				mockL2Syncer.On("GetClaims", ctx, uint64(6), uint64(10)).Return([]bridgesync.Claim{{}}, nil)
				mockClient.On("GenerateAggchainProof", uint64(6), uint64(10), common.Hash{}, common.Hash{}, [32]common.Hash{}).Return(&types.AggchainProof{
					Proof: []byte("some-proof"), StartBlock: 6, EndBlock: 10}, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:           6,
				ToBlock:             10,
				RetryCount:          0,
				LastSentCertificate: &types.CertificateInfo{ToBlock: 5},
				Bridges:             []bridgesync.Bridge{{}},
				Claims:              []bridgesync.Claim{{}},
				AggchainProof:       []byte("some-proof"),
				CreatedAt:           uint32(time.Now().UTC().Unix()),
			},
		},
		{
			name: "success fetching aggchain proof for new certificate - aggchain prover returns smaller range",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockClient *mocks.AggchainProofClientInterface) {
				mockStorage.On("GetLastSentCertificate").Return(&types.CertificateInfo{ToBlock: 5}, nil).Twice()
				mockL2Syncer.On("GetLastProcessedBlock", ctx).Return(uint64(10), nil)
				mockL2Syncer.On("GetBridgesPublished", ctx, uint64(6), uint64(10)).Return([]bridgesync.Bridge{
					{BlockNum: 6}, {BlockNum: 10}}, nil)
				mockL2Syncer.On("GetClaims", ctx, uint64(6), uint64(10)).Return([]bridgesync.Claim{
					{BlockNum: 8}, {BlockNum: 9}}, nil)
				mockClient.On("GenerateAggchainProof", uint64(6), uint64(10), common.Hash{}, common.Hash{}, [32]common.Hash{}).Return(&types.AggchainProof{
					Proof: []byte("some-proof"), StartBlock: 6, EndBlock: 8}, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:           6,
				ToBlock:             8,
				RetryCount:          0,
				LastSentCertificate: &types.CertificateInfo{ToBlock: 5},
				Bridges:             []bridgesync.Bridge{{BlockNum: 6}},
				Claims:              []bridgesync.Claim{{BlockNum: 8}},
				AggchainProof:       []byte("some-proof"),
				CreatedAt:           uint32(time.Now().UTC().Unix()),
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockStorage := mocks.NewAggSenderStorage(t)
			mockL2Syncer := mocks.NewL2BridgeSyncer(t)
			mockAggchainProofClient := mocks.NewAggchainProofClientInterface(t)
			aggchainFlow := newAggchainProverFlow(log.WithFields("flowManager", "Test_AggchainProverFlow_GetCertificateBuildParams"),
				Config{}, mockAggchainProofClient, mockStorage, nil, mockL2Syncer)

			tc.mockFn(mockStorage, mockL2Syncer, mockAggchainProofClient)

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
