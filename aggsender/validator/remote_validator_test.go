package validator

import (
	"errors"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestRemoteClient_ValidateCertificate(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	testCases := []struct {
		name          string
		certificate   *agglayertypes.Certificate
		mockFn        func(mockClient *mocks.ValidatorClient, mockStorage *mocks.AggSenderStorage)
		expectedSig   []byte
		expectedError string
	}{
		{
			name: "storage returns an error",
			certificate: &agglayertypes.Certificate{
				Height: 11,
			},
			mockFn: func(mockClient *mocks.ValidatorClient, mockStorage *mocks.AggSenderStorage) {
				mockStorage.EXPECT().GetCertificateHeaderByHeight(uint64(10)).Return(nil, errors.New("storage error"))
			},
			expectedError: "error getting previous certificate header by height 10: storage error",
		},
		{
			name: "client returns an error",
			certificate: &agglayertypes.Certificate{
				Height: 0,
			},
			mockFn: func(mockClient *mocks.ValidatorClient, mockStorage *mocks.AggSenderStorage) {
				var previousCertificateID *common.Hash
				mockClient.EXPECT().ValidateCertificate(
					ctx,
					previousCertificateID,
					&agglayertypes.Certificate{
						Height: 0,
					},
					mock.Anything,
				).Return(nil, errors.New("client error"))
			},
			expectedError: "error validating certificate on aggsender validator service: client error",
		},
		{
			name: "success, no previous certificate",
			certificate: &agglayertypes.Certificate{
				Height: 0,
			},
			mockFn: func(mockClient *mocks.ValidatorClient, mockStorage *mocks.AggSenderStorage) {
				var previousCertificateID *common.Hash
				mockClient.EXPECT().ValidateCertificate(
					ctx,
					previousCertificateID,
					&agglayertypes.Certificate{
						Height: 0,
					},
					mock.Anything,
				).Return([]byte{1, 2, 3}, nil)
			},
			expectedSig: []byte{1, 2, 3},
		},
		{
			name: "success, with previous certificate",
			certificate: &agglayertypes.Certificate{
				Height: 11,
			},
			mockFn: func(mockClient *mocks.ValidatorClient, mockStorage *mocks.AggSenderStorage) {
				previousCertID := common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
				mockStorage.EXPECT().GetCertificateHeaderByHeight(uint64(10)).Return(
					&types.CertificateHeader{
						CertificateID: previousCertID,
					}, nil,
				)
				mockClient.EXPECT().ValidateCertificate(
					ctx,
					&previousCertID,
					&agglayertypes.Certificate{
						Height: 11,
					},
					mock.Anything,
				).Return([]byte{4, 5, 6}, nil)
			},
			expectedSig: []byte{4, 5, 6},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockClient := mocks.NewValidatorClient(t)
			mockStorage := mocks.NewAggSenderStorage(t)

			if tc.mockFn != nil {
				tc.mockFn(mockClient, mockStorage)
			}

			remoteValidator := &RemoteValidator{
				client:  mockClient,
				storage: mockStorage,
			}

			signature, err := remoteValidator.ValidateAndSignCertificate(ctx, tc.certificate, 0)

			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, signature, tc.expectedSig)
			}

			mockClient.AssertExpectations(t)
			mockStorage.AssertExpectations(t)
		})
	}
}
