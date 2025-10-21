package flows

import (
	"context"
	"errors"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func Test_AggchainProverFlow_VerifyCertificate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	requestedEndBlock := uint64(100)
	lastProvenBlock := uint64(50)

	validL1InfoLeaf := &l1infotreesync.L1InfoTreeLeaf{
		Hash: common.HexToHash("0x123"),
	}

	validAggregationProofPublicValues := &types.AggchainParams{
		AggregationProofPublicValues: types.AggregationProofPublicValues{
			L1Head:              common.HexToHash("0x1"),
			L2PreRoot:           common.HexToHash("0x2"),
			ClaimRoot:           common.HexToHash("0x3"),
			L2BlockNumber:       100,
			RollupConfigHash:    common.HexToHash("0x4"),
			MultiBlockVKey:      common.HexToHash("0x5"),
			TrustedSigner:       common.HexToAddress("0x6"),
			AggregationVKeyHash: common.HexToHash("0x7"),
		},
		OptimisticMode: false,
	}

	expectedAggchainParams, err := validAggregationProofPublicValues.Hash()
	require.NoError(t, err)

	testCases := []struct {
		name          string
		certificate   *agglayertypes.Certificate
		mockFn        func(*mocks.L1InfoTreeDataQuerier, *mocks.FEPInputsQuerier)
		expectedError string
	}{
		{
			name: "certificate AggchainData is nil",
			certificate: &agglayertypes.Certificate{
				AggchainData: nil,
			},
			expectedError: "aggchainProverFlow: certificate AggchainData is nil",
		},
		{
			name: "certificate AggchainData is of unknown type",
			certificate: &agglayertypes.Certificate{
				AggchainData: &agglayertypes.AggchainDataSignature{}, // wrong type
			},
			expectedError: "aggchainProverFlow: certificate AggchainData is of unknown type *types.AggchainDataSignature",
		},
		{
			name: "error getting L1InfoLeaf by index",
			certificate: &agglayertypes.Certificate{
				L1InfoTreeLeafCount: 10,
				AggchainData: &agglayertypes.AggchainDataProof{
					AggchainParams: expectedAggchainParams,
				},
			},
			mockFn: func(mockL1InfoTreeDataQuerier *mocks.L1InfoTreeDataQuerier, mockFEPInputsQuerier *mocks.FEPInputsQuerier) {
				mockL1InfoTreeDataQuerier.EXPECT().GetInfoByIndex(ctx, uint32(9)).Return(nil, errors.New("l1info error")).Once()
			},
			expectedError: "aggchainProverFlow - error getting L1InfoLeaf by index 9: l1info error",
		},
		{
			name: "error getting expected aggchain proof public values",
			certificate: &agglayertypes.Certificate{
				L1InfoTreeLeafCount: 10,
				AggchainData: &agglayertypes.AggchainDataProof{
					AggchainParams: expectedAggchainParams,
				},
			},
			mockFn: func(mockL1InfoTreeDataQuerier *mocks.L1InfoTreeDataQuerier, mockFEPInputsQuerier *mocks.FEPInputsQuerier) {
				mockL1InfoTreeDataQuerier.EXPECT().GetInfoByIndex(ctx, uint32(9)).Return(validL1InfoLeaf, nil).Once()
				mockFEPInputsQuerier.EXPECT().GetAggchainParams(
					lastProvenBlock, requestedEndBlock, validL1InfoLeaf.Hash).
					Return(nil, errors.New("aggchain error")).Once()
			},
			expectedError: "aggchainProverFlow - error getting expected aggchain proof public values: aggchain error",
		},
		{
			name: "aggchain params do not match",
			certificate: &agglayertypes.Certificate{
				L1InfoTreeLeafCount: 10,
				AggchainData: &agglayertypes.AggchainDataProof{
					AggchainParams: common.HexToHash("0xwrong"), // different from expected
				},
			},
			mockFn: func(mockL1InfoTreeDataQuerier *mocks.L1InfoTreeDataQuerier, mockFEPInputsQuerier *mocks.FEPInputsQuerier) {
				mockL1InfoTreeDataQuerier.EXPECT().GetInfoByIndex(ctx, uint32(9)).Return(validL1InfoLeaf, nil).Once()
				mockFEPInputsQuerier.EXPECT().GetAggchainParams(
					lastProvenBlock, requestedEndBlock, validL1InfoLeaf.Hash).
					Return(validAggregationProofPublicValues, nil).Once()
			},
			expectedError: "aggchainProverFlow - aggchain params do not match",
		},
		{
			name: "successful verification with AggchainDataProof",
			certificate: &agglayertypes.Certificate{
				L1InfoTreeLeafCount: 10,
				AggchainData: &agglayertypes.AggchainDataProof{
					AggchainParams: expectedAggchainParams,
				},
			},
			mockFn: func(mockL1InfoTreeDataQuerier *mocks.L1InfoTreeDataQuerier, mockFEPInputsQuerier *mocks.FEPInputsQuerier) {
				mockL1InfoTreeDataQuerier.EXPECT().GetInfoByIndex(ctx, uint32(9)).Return(validL1InfoLeaf, nil).Once()
				mockFEPInputsQuerier.EXPECT().GetAggchainParams(
					lastProvenBlock, requestedEndBlock, validL1InfoLeaf.Hash).
					Return(validAggregationProofPublicValues, nil).Once()
			},
		},
		{
			name: "successful verification with AggchainDataMultisigWithProof",
			certificate: &agglayertypes.Certificate{
				L1InfoTreeLeafCount: 10,
				AggchainData: &agglayertypes.AggchainDataMultisigWithProof{
					Multisig: &agglayertypes.Multisig{},
					AggchainProof: &agglayertypes.AggchainDataProof{
						AggchainParams: expectedAggchainParams,
					},
				},
			},
			mockFn: func(mockL1InfoTreeDataQuerier *mocks.L1InfoTreeDataQuerier, mockFEPInputsQuerier *mocks.FEPInputsQuerier) {
				mockL1InfoTreeDataQuerier.EXPECT().GetInfoByIndex(ctx, uint32(9)).Return(validL1InfoLeaf, nil).Once()
				mockFEPInputsQuerier.EXPECT().GetAggchainParams(
					lastProvenBlock, requestedEndBlock, validL1InfoLeaf.Hash).
					Return(validAggregationProofPublicValues, nil).Once()
			},
		},
		{
			name: "successful verification with edge case - L1InfoTreeLeafCount is 1",
			certificate: &agglayertypes.Certificate{
				L1InfoTreeLeafCount: 1,
				AggchainData: &agglayertypes.AggchainDataProof{
					AggchainParams: expectedAggchainParams,
				},
			},
			mockFn: func(mockL1InfoTreeDataQuerier *mocks.L1InfoTreeDataQuerier, mockFEPInputsQuerier *mocks.FEPInputsQuerier) {
				mockL1InfoTreeDataQuerier.EXPECT().GetInfoByIndex(ctx, uint32(0)).Return(validL1InfoLeaf, nil).Once()
				mockFEPInputsQuerier.EXPECT().GetAggchainParams(
					lastProvenBlock, requestedEndBlock, validL1InfoLeaf.Hash).
					Return(validAggregationProofPublicValues, nil).Once()
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockL1InfoTreeDataQuerier := mocks.NewL1InfoTreeDataQuerier(t)
			mockFEPInputsQuery := mocks.NewFEPInputsQuerier(t)
			logger := log.WithFields("flowManager", "Test_AggchainProverFlow_VerifyAggchainData")

			flow := &AggchainProverVerifierFlow{
				AggchainProverBuilderFlow: &AggchainProverBuilderFlow{
					log:                   logger,
					l1InfoTreeDataQuerier: mockL1InfoTreeDataQuerier,
				},
				fepInputsQuery: mockFEPInputsQuery,
			}

			if tc.mockFn != nil {
				tc.mockFn(mockL1InfoTreeDataQuerier, mockFEPInputsQuery)
			}

			err := flow.VerifyCertificate(ctx, tc.certificate, requestedEndBlock, lastProvenBlock)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
			}

			mockL1InfoTreeDataQuerier.AssertExpectations(t)
			mockFEPInputsQuery.AssertExpectations(t)
		})
	}
}
