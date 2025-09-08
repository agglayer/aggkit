package optimistic

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"

	optimisticmocks "github.com/agglayer/aggkit/aggsender/optimistic/mocks"
	optimistichash "github.com/agglayer/aggkit/aggsender/optimistic/optimistichash"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/go_signer/signer"
	"github.com/agglayer/go_signer/signer/mocks"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewOptimisticSignatureCalculatorImpl(t *testing.T) {
	ctx := context.Background()
	signerKey, err := crypto.GenerateKey()
	require.NoError(t, err)

	signerAddr := crypto.PubkeyToAddress(signerKey.PublicKey)
	chainID := uint64(1337)

	keyConfig := signertypes.SignerConfig{
		Method: signertypes.MethodMock,
		Config: map[string]any{
			signer.FieldMockPrivateKey: hex.EncodeToString(crypto.FromECDSA(signerKey)),
		},
	}

	tests := []struct {
		name        string
		setupMock   func(m *optimisticmocks.FEPContractQuerier)
		cfg         Config
		expectedErr string
	}{
		{
			name: "happy path with signer in list",
			setupMock: func(m *optimisticmocks.FEPContractQuerier) {
				m.EXPECT().
					GetAggchainSigners(mock.Anything).
					Return([]common.Address{signerAddr}, nil)
			},
			cfg: Config{
				RequireKeyMatchTrustedSequencer: true,
				TrustedSequencerKey:             keyConfig,
			},
		},
		{
			name: "aggchainFEPContract returns error and RequireKeyMatchTrustedSequencer = true",
			setupMock: func(m *optimisticmocks.FEPContractQuerier) {
				m.EXPECT().
					GetAggchainSigners(mock.Anything).
					Return(nil, errors.New("internal error"))
			},
			cfg: Config{
				RequireKeyMatchTrustedSequencer: true,
				TrustedSequencerKey:             keyConfig,
			},
			expectedErr: "failed to fetch the aggchain signers",
		},
		{
			name: "aggchainFEPContract returns empty list and RequireKeyMatchTrustedSequencer = true",
			setupMock: func(m *optimisticmocks.FEPContractQuerier) {
				m.EXPECT().
					GetAggchainSigners(mock.Anything).
					Return([]common.Address{}, nil)
			},
			cfg: Config{
				RequireKeyMatchTrustedSequencer: true,
				TrustedSequencerKey:             keyConfig,
			},
			expectedErr: "there should be at least one aggchain signer",
		},
		{
			name: "signer not found in list",
			setupMock: func(m *optimisticmocks.FEPContractQuerier) {
				m.EXPECT().
					GetAggchainSigners(mock.Anything).
					Return([]common.Address{common.HexToAddress("0xdeadbeef")}, nil)
			},
			cfg: Config{
				RequireKeyMatchTrustedSequencer: true,
				TrustedSequencerKey:             signer.NewMockSignerConfig(""),
			},
			expectedErr: "not found in the AggchainFEP contract signers",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockFEP := optimisticmocks.NewFEPContractQuerier(t)
			tt.setupMock(mockFEP)

			impl, err := NewOptimisticSignatureCalculatorImpl(
				ctx,
				log.GetDefaultLogger(),
				mockFEP,
				chainID,
				tt.cfg,
			)

			if tt.expectedErr != "" {
				require.Contains(t, err.Error(), tt.expectedErr)
				require.Nil(t, impl)
			} else {
				require.NoError(t, err)
				require.NotNil(t, impl)
			}
		})
	}
}

func TestOptimisticSignatureCalculatorImpl_Sign(t *testing.T) {
	aggchainReq := types.AggchainProofRequest{
		LastProvenBlock:   100,
		RequestedEndBlock: 200,
		L1InfoTreeLeaf: l1infotreesync.L1InfoTreeLeaf{
			BlockNumber:       150,
			PreviousBlockHash: common.HexToHash("0xabc"),
		},
	}
	aggProof := &optimistichash.AggregationProofPublicValues{
		L1Head:           common.HexToHash("0x123"),
		L2PreRoot:        common.HexToHash("0x456"),
		ClaimRoot:        common.HexToHash("0x789"),
		L2BlockNumber:    150,
		RollupConfigHash: [32]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10},
		MultiBlockVKey:   [32]byte{0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f, 0x20},
		ProverAddress:    common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
	}

	newLocalExitRoot := common.HexToHash("0xdef")
	certBuildParams := &types.CertificateBuildParams{
		Claims: []bridgesync.Claim{},
	}

	testCases := []struct {
		name                  string
		mockQueryReturn       *optimistichash.AggregationProofPublicValues
		mockQueryError        error
		mockSignerReturn      []byte
		mockSignerError       error
		expectedSignData      []byte
		expectedExtraData     string
		expectedErrorContains string
	}{
		{
			name:                  "success case",
			mockQueryReturn:       aggProof,
			mockQueryError:        nil,
			mockSignerReturn:      []byte("signed_data"),
			mockSignerError:       nil,
			expectedSignData:      []byte("signed_data"),
			expectedExtraData:     "aggregationProofPublicValues: ",
			expectedErrorContains: "",
		},
		{
			name:                  "error in GetAggregationProofPublicValuesData",
			mockQueryReturn:       nil,
			mockQueryError:        errors.New("query error"),
			mockSignerReturn:      nil,
			mockSignerError:       nil,
			expectedSignData:      nil,
			expectedExtraData:     "",
			expectedErrorContains: "query error",
		},
		{
			name:                  "error in SignHash",
			mockQueryReturn:       aggProof,
			mockQueryError:        nil,
			mockSignerReturn:      nil,
			mockSignerError:       errors.New("signing error"),
			expectedSignData:      nil,
			expectedExtraData:     "",
			expectedErrorContains: "signing error",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			realLogger := log.WithFields("module", "test_logger") // Replace mockLogger with a real logger
			mockSigner := mocks.NewHashSigner(t)
			mockQuery := optimisticmocks.NewOptimisticAggregationProofPublicValuesQuerier(t)
			calculator := &OptimisticSignatureCalculatorImpl{
				queryAggregationProofPublicValues: mockQuery,
				signer:                            mockSigner,
				logger:                            realLogger, // Use realLogger here
			}

			ctx := context.Background()

			mockQuery.On("GetAggregationProofPublicValuesData", aggchainReq.LastProvenBlock, aggchainReq.RequestedEndBlock, aggchainReq.L1InfoTreeLeaf.PreviousBlockHash).
				Return(tc.mockQueryReturn, tc.mockQueryError)

			mockSigner.On("SignHash", ctx, mock.Anything).Return(tc.mockSignerReturn, tc.mockSignerError).Maybe()

			signData, extraData, err := calculator.Sign(ctx, aggchainReq, newLocalExitRoot, certBuildParams.Claims)

			if tc.expectedErrorContains != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectedErrorContains)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedSignData, signData)
				require.Contains(t, extraData, tc.expectedExtraData)
			}
		})
	}
}
