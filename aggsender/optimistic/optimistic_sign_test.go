package optimistic

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"

	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/go_signer/signer"
	signermocks "github.com/agglayer/go_signer/signer/mocks"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewOptimisticSignatureCalculatorImpl(t *testing.T) {
	ctx := t.Context()
	signerKey, err := crypto.GenerateKey()
	require.NoError(t, err)

	signerAddr := crypto.PubkeyToAddress(signerKey.PublicKey)
	chainID := uint64(1337)

	signerKeyCfg := signertypes.SignerConfig{
		Method: signertypes.MethodMock,
		Config: map[string]any{
			signer.FieldMockPrivateKey: hex.EncodeToString(crypto.FromECDSA(signerKey)),
		},
	}

	tests := []struct {
		name        string
		setupMock   func(m *mocks.FEPContractQuerier)
		cfg         Config
		expectedErr string
	}{
		{
			name: "happy path with signer in list",
			setupMock: func(m *mocks.FEPContractQuerier) {
				m.EXPECT().
					GetAggchainSigners(mock.Anything).
					Return([]common.Address{signerAddr}, nil)
			},
			cfg: Config{
				RequireKeyMatchTrustedSequencer: true,
				TrustedSequencerKey:             signerKeyCfg,
			},
		},
		{
			name: "aggchainFEPContract returns error and RequireKeyMatchTrustedSequencer = true",
			setupMock: func(m *mocks.FEPContractQuerier) {
				m.EXPECT().
					GetAggchainSigners(mock.Anything).
					Return(nil, errors.New("internal error"))
			},
			cfg: Config{
				RequireKeyMatchTrustedSequencer: true,
				TrustedSequencerKey:             signerKeyCfg,
			},
			expectedErr: "failed to fetch the aggchain signers",
		},
		{
			name: "aggchainFEPContract returns empty list and RequireKeyMatchTrustedSequencer = true",
			setupMock: func(m *mocks.FEPContractQuerier) {
				m.EXPECT().
					GetAggchainSigners(mock.Anything).
					Return([]common.Address{}, nil)
			},
			cfg: Config{
				RequireKeyMatchTrustedSequencer: true,
				TrustedSequencerKey:             signerKeyCfg,
			},
			expectedErr: "should be at least one signer",
		},
		{
			name: "signer differs from trusted sequencer address and RequireKeyMatchTrustedSequencer = false",
			setupMock: func(m *mocks.FEPContractQuerier) {
				m.EXPECT().
					GetAggchainSigners(mock.Anything).
					Return([]common.Address{common.HexToAddress("0xdeadbeef"), signerAddr}, nil)
			},
			cfg: Config{
				RequireKeyMatchTrustedSequencer: true,
				TrustedSequencerKey:             signerKeyCfg,
			},
			expectedErr: fmt.Sprintf("configured trusted signer address (%s) differs from the one initialized on the AggchainFEP contract (%s)",
				signerAddr.Hex(), common.HexToAddress("0xdeadbeef").Hex()),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockFEP := mocks.NewFEPContractQuerier(t)
			tt.setupMock(mockFEP)
			tt.cfg.SovereignRollupAddr = common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
			tt.cfg.OpNodeURL = "http://localhost:8545"
			impl, err := NewOptimisticSignatureCalculatorImpl(
				ctx,
				log.GetDefaultLogger(),
				mockFEP,
				chainID,
				tt.cfg,
			)

			if tt.expectedErr != "" {
				require.ErrorContains(t, err, tt.expectedErr)
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
	aggProof := &types.AggregationProofPublicValues{
		L1Head:           common.HexToHash("0x123"),
		L2PreRoot:        common.HexToHash("0x456"),
		ClaimRoot:        common.HexToHash("0x789"),
		L2BlockNumber:    150,
		RollupConfigHash: [common.HashLength]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10},
		MultiBlockVKey:   [common.HashLength]byte{0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f, 0x20},
		TrustedSigner:    common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
	}

	newLocalExitRoot := common.HexToHash("0xdef")
	certBuildParams := &types.CertificateBuildParams{
		Claims: []bridgesync.Claim{},
	}

	testCases := []struct {
		name                  string
		mockQueryReturn       *types.AggregationProofPublicValues
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
			mockSigner := signermocks.NewHashSigner(t)
			mockQuery := mocks.NewAggProofPublicValuesQuerier(t)
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

func TestOptimisticSignatureCalculatorImpl_ValidateConfig(t *testing.T) {
	_, err := NewOptimisticSignatureCalculatorImpl(t.Context(), nil, nil, 0, Config{})
	require.Error(t, err)
}
