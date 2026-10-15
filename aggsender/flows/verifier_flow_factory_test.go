package flows

import (
	"context"
	"fmt"
	"testing"

	"github.com/agglayer/aggkit/aggsender/config"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/optimistic"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/aggsender/validator"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	typesmocks "github.com/agglayer/aggkit/types/mocks"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewVerifierFlow(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name             string
		cfg              validator.Config
		aggchainType     types.AggchainType // defaults to FEP
		mockFn           func(*mocks.MultisigQuerier)
		expectedError    string
		expectedFlowType string
	}{
		{
			name: "success with PessimisticProofMode",
			cfg: validator.Config{
				Mode:                            types.PessimisticProofMode,
				Signer:                          signertypes.SignerConfig{Method: signertypes.MethodNone},
				RequireCommitteeMembershipCheck: true,
				BlockFinalityForL1InfoTree:      aggkittypes.FinalizedBlock,
			},
			mockFn: func(mockCommittee *mocks.MultisigQuerier) {
				committee, err := types.NewMultisigCommittee([]*types.SignerInfo{types.NewSignerInfo("", common.Address{})}, 1)
				require.NoError(t, err)

				mockCommittee.EXPECT().GetMultisigCommittee(mock.Anything, mock.Anything).Return(committee, nil).Once()
			},
		},
		{
			name: "error getting multisig committee when RequireCommitteeMembershipCheck is true",
			cfg: validator.Config{
				Mode:                            types.PessimisticProofMode,
				Signer:                          signertypes.SignerConfig{Method: signertypes.MethodNone},
				RequireCommitteeMembershipCheck: true,
				BlockFinalityForL1InfoTree:      aggkittypes.FinalizedBlock,
			},
			mockFn: func(mockCommittee *mocks.MultisigQuerier) {
				mockCommittee.EXPECT().GetMultisigCommittee(mock.Anything, mock.Anything).Return(nil, fmt.Errorf("test error")).Once()
			},
			expectedError: "error getting multisig committee: test error",
		},
		{
			name: "success with AggchainProofMode",
			cfg: validator.Config{
				Mode:   types.AggchainProofMode,
				Signer: signertypes.SignerConfig{Method: signertypes.MethodNone},
				FEPConfig: validator.FEPConfig{
					OpNodeURL: "http://localhost:8545",
				},
				BlockFinalityForL1InfoTree: aggkittypes.FinalizedBlock,
			},
			mockFn: func(mockCommittee *mocks.MultisigQuerier) {
				committee, err := types.NewMultisigCommittee([]*types.SignerInfo{types.NewSignerInfo("", common.Address{})}, 1)
				require.NoError(t, err)

				mockCommittee.EXPECT().GetMultisigCommittee(mock.Anything, mock.Anything).Return(committee, nil).Once()
			},
		},
		{
			name: "error getting multisig committee when RequireCommitteeMembershipCheck is true with AggchainProofMode",
			cfg: validator.Config{
				Mode:                            types.AggchainProofMode,
				Signer:                          signertypes.SignerConfig{Method: signertypes.MethodNone},
				RequireCommitteeMembershipCheck: true,
				FEPConfig: validator.FEPConfig{
					OpNodeURL: "http://localhost:8545",
				},
				BlockFinalityForL1InfoTree: aggkittypes.FinalizedBlock,
			},
			mockFn: func(mockCommittee *mocks.MultisigQuerier) {
				mockCommittee.EXPECT().GetMultisigCommittee(mock.Anything, mock.Anything).Return(nil, fmt.Errorf("test error")).Once()
			},
			expectedError: "error getting multisig committee: test error",
		},
		{
			name:         "success with AggchainProofMode and QBFT aggchain type",
			aggchainType: types.AggchainTypeQBFT,
			cfg: validator.Config{
				Mode:   types.AggchainProofMode,
				Signer: signertypes.SignerConfig{Method: signertypes.MethodNone},
				FEPConfig: validator.FEPConfig{
					SovereignRollupAddr: common.HexToAddress("0x1234"),
				},
				AgglayerBridgeL2Addr:       common.HexToAddress("0x5678"),
				BlockFinalityForL1InfoTree: aggkittypes.FinalizedBlock,
			},
			mockFn: func(mockCommittee *mocks.MultisigQuerier) {
				committee, err := types.NewMultisigCommittee([]*types.SignerInfo{types.NewSignerInfo("", common.Address{})}, 1)
				require.NoError(t, err)

				mockCommittee.EXPECT().GetMultisigCommittee(mock.Anything, mock.Anything).Return(committee, nil).Once()
			},
			expectedFlowType: "*flows.AggchainQBFTVerifierFlow",
		},
		{
			name:         "error with AggchainProofMode and QBFT aggchain type when OpNodeURL is set",
			aggchainType: types.AggchainTypeQBFT,
			cfg: validator.Config{
				Mode:   types.AggchainProofMode,
				Signer: signertypes.SignerConfig{Method: signertypes.MethodNone},
				FEPConfig: validator.FEPConfig{
					SovereignRollupAddr: common.HexToAddress("0x1234"),
					OpNodeURL:           "http://localhost:8545",
				},
				BlockFinalityForL1InfoTree: aggkittypes.FinalizedBlock,
			},
			mockFn: func(mockCommittee *mocks.MultisigQuerier) {
				committee, err := types.NewMultisigCommittee([]*types.SignerInfo{types.NewSignerInfo("", common.Address{})}, 1)
				require.NoError(t, err)

				mockCommittee.EXPECT().GetMultisigCommittee(mock.Anything, mock.Anything).Return(committee, nil).Once()
			},
			expectedError: "does not support optimistic mode",
		},
		{
			name: "error getting aggchain type with AggchainProofMode",
			cfg: validator.Config{
				Mode:                       types.AggchainProofMode,
				Signer:                     signertypes.SignerConfig{Method: signertypes.MethodNone},
				BlockFinalityForL1InfoTree: aggkittypes.FinalizedBlock,
			},
			aggchainType: "error",
			mockFn: func(mockCommittee *mocks.MultisigQuerier) {
				committee, err := types.NewMultisigCommittee([]*types.SignerInfo{types.NewSignerInfo("", common.Address{})}, 1)
				require.NoError(t, err)

				mockCommittee.EXPECT().GetMultisigCommittee(mock.Anything, mock.Anything).Return(committee, nil).Once()
			},
			expectedError: "failed to get aggchain type",
		},
		{
			name: "unsupported mode",
			cfg: validator.Config{
				Mode:                       "unsupported-mode",
				Signer:                     signertypes.SignerConfig{Method: signertypes.MethodNone},
				BlockFinalityForL1InfoTree: aggkittypes.FinalizedBlock,
			},
			expectedError: "unsupported Aggsender Validator mode: unsupported-mode",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			mockLogger := log.WithFields("test", "NewFlow")

			mockL1Client := typesmocks.NewBaseEthereumClienter(t)
			mockL1InfoTreeSyncer := mocks.NewL1InfoTreeSyncer(t)
			mockL2Syncer := mocks.NewL2BridgeSyncer(t)
			mockRollupDataQuerier := mocks.NewRollupDataQuerier(t)
			mockCommitteeQuerier := mocks.NewMultisigQuerier(t)

			mockL1InfoTreeSyncer.EXPECT().Finality().Return(aggkittypes.FinalizedBlock).Maybe()
			mockRollupDataQuerier.EXPECT().GetRollupChainID().Return(uint64(1234), nil).Maybe()
			mockL2Syncer.EXPECT().OriginNetwork().Return(1).Maybe()

			switch tc.aggchainType {
			case "error":
				mockCommitteeQuerier.EXPECT().AggchainType().Return("", fmt.Errorf("aggchain type error")).Maybe()
			case "":
				mockCommitteeQuerier.EXPECT().AggchainType().Return(types.AggchainTypeFEP, nil).Maybe()
			default:
				mockCommitteeQuerier.EXPECT().AggchainType().Return(tc.aggchainType, nil).Maybe()
			}

			if tc.mockFn != nil {
				tc.mockFn(mockCommitteeQuerier)
			}

			verifierFlow, commonComponents, err := NewVerifierFlow(
				ctx,
				tc.cfg,
				mockLogger,
				mockL1Client,
				nil,
				mockL1InfoTreeSyncer,
				mockL2Syncer,
				nil,
				mockRollupDataQuerier,
				mockCommitteeQuerier,
				common.Hash{},
			)

			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
				require.Nil(t, verifierFlow)
				require.Nil(t, commonComponents)
			} else {
				require.NoError(t, err)
				require.NotNil(t, verifierFlow)
				require.NotNil(t, commonComponents)
				if tc.expectedFlowType != "" {
					require.Equal(t, tc.expectedFlowType, fmt.Sprintf("%T", verifierFlow))
				}
			}
		})
	}
}

func TestNewLocalVerifier(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                     string
		mode                     types.AggsenderMode
		aggchainType             types.AggchainType // defaults to FEP
		builderFlow              types.AggsenderBuilderFlow
		cfg                      config.Config
		expectedErrorSubstr      string
		expectedVerifierFlowType string
	}{
		{
			name:                "Pessimistic mode with nil builder flow",
			mode:                types.PessimisticProofMode,
			builderFlow:         (types.AggsenderBuilderFlow)(nil),
			cfg:                 config.Config{Mode: types.PessimisticProofMode},
			expectedErrorSubstr: "expected PPBuilderFlow",
		},
		{
			name:                "Pessimistic mode with wrong builder type - AggchainProverBuilderFlow",
			mode:                types.PessimisticProofMode,
			builderFlow:         &AggchainProverBuilderFlow{},
			cfg:                 config.Config{Mode: types.PessimisticProofMode},
			expectedErrorSubstr: "expected PPBuilderFlow",
		},
		{
			name:                     "Pessimistic mode with correct PPBuilderFlow",
			mode:                     types.PessimisticProofMode,
			builderFlow:              &PPBuilderFlow{},
			cfg:                      config.Config{Mode: types.PessimisticProofMode},
			expectedVerifierFlowType: "*flows.PPVerifierFlow",
		},
		{
			name:                "Aggchain mode with nil builder flow",
			mode:                types.AggchainProofMode,
			builderFlow:         (types.AggsenderBuilderFlow)(nil),
			cfg:                 config.Config{Mode: types.AggchainProofMode},
			expectedErrorSubstr: "expected AggchainProverBuilderFlow",
		},
		{
			name:                "Aggchain mode with wrong builder type - PPBuilderFlow",
			mode:                types.AggchainProofMode,
			builderFlow:         &PPBuilderFlow{},
			cfg:                 config.Config{Mode: types.AggchainProofMode},
			expectedErrorSubstr: "expected AggchainProverBuilderFlow",
		},
		{
			name:                "Unsupported mode",
			mode:                "unsupported-mode",
			builderFlow:         (types.AggsenderBuilderFlow)(nil),
			cfg:                 config.Config{Mode: "unsupported-mode"},
			expectedErrorSubstr: "unsupported Aggsender Validator mode",
		},
		{
			name:                "Empty mode string",
			mode:                "",
			builderFlow:         (types.AggsenderBuilderFlow)(nil),
			cfg:                 config.Config{Mode: ""},
			expectedErrorSubstr: "unsupported Aggsender Validator mode",
		},
		{
			name:                "Invalid mode with special characters",
			mode:                "invalid@mode#123",
			builderFlow:         (types.AggsenderBuilderFlow)(nil),
			cfg:                 config.Config{Mode: "invalid@mode#123"},
			expectedErrorSubstr: "unsupported Aggsender Validator mode",
		},
		{
			name:        "Pessimistic mode with invalid config but correct builder flow",
			mode:        types.PessimisticProofMode,
			builderFlow: &PPBuilderFlow{},
			cfg: config.Config{
				Mode: types.PessimisticProofMode,
				// No additional config needed for PP mode
			},
			expectedVerifierFlowType: "*flows.PPVerifierFlow",
		},
		{
			name:        "Aggchain mode success",
			mode:        types.AggchainProofMode,
			builderFlow: &AggchainProverBuilderFlow{},
			cfg: config.Config{
				Mode:                 types.AggchainProofMode,
				SovereignRollupAddr:  common.HexToAddress("0x1"), // Valid address
				OptimisticModeConfig: optimistic.Config{OpNodeURL: ""},
			},
			expectedVerifierFlowType: "*flows.AggchainProverVerifierFlow",
		},
		{
			name:         "Aggchain mode QBFT success (no op-node nor optimistic config needed)",
			mode:         types.AggchainProofMode,
			aggchainType: types.AggchainTypeQBFT,
			builderFlow:  &AggchainProverBuilderFlow{},
			cfg: config.Config{
				Mode:                types.AggchainProofMode,
				SovereignRollupAddr: common.HexToAddress("0x1"),
			},
			expectedVerifierFlowType: "*flows.AggchainQBFTVerifierFlow",
		},
		{
			name:         "Aggchain mode QBFT with OpNodeURL set fails",
			mode:         types.AggchainProofMode,
			aggchainType: types.AggchainTypeQBFT,
			builderFlow:  &AggchainProverBuilderFlow{},
			cfg: config.Config{
				Mode:                 types.AggchainProofMode,
				SovereignRollupAddr:  common.HexToAddress("0x1"),
				OptimisticModeConfig: optimistic.Config{OpNodeURL: "http://localhost:8545"},
			},
			expectedErrorSubstr: "does not support optimistic mode",
		},
		{
			name:         "Aggchain mode QBFT with wrong builder type - PPBuilderFlow",
			mode:         types.AggchainProofMode,
			aggchainType: types.AggchainTypeQBFT,
			builderFlow:  &PPBuilderFlow{},
			cfg:          config.Config{Mode: types.AggchainProofMode},

			expectedErrorSubstr: "expected AggchainProverBuilderFlow",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			mockCommitteeQuerier := mocks.NewMultisigQuerier(t)
			aggchainType := tc.aggchainType
			if aggchainType == "" {
				aggchainType = types.AggchainTypeFEP
			}
			mockCommitteeQuerier.EXPECT().AggchainType().Return(aggchainType, nil).Maybe()

			if aggchainType != types.AggchainTypeQBFT {
				tc.cfg.OptimisticModeConfig.SovereignRollupAddr = common.HexToAddress("0x1")
				tc.cfg.OptimisticModeConfig.OpNodeURL = "http://localhost:8545"
			}
			verifierFlow, err := NewLocalVerifier(ctx, tc.cfg, nil, nil, mockCommitteeQuerier, tc.builderFlow)

			if tc.expectedErrorSubstr != "" {
				require.ErrorContains(t, err, tc.expectedErrorSubstr)
			} else {
				require.NoError(t, err)
				require.NotNil(t, verifierFlow)
				if tc.expectedVerifierFlowType != "" {
					actualType := fmt.Sprintf("%T", verifierFlow)
					if actualType != tc.expectedVerifierFlowType {
						t.Fatalf("expected verifier flow type %s, got %s", tc.expectedVerifierFlowType, actualType)
					}
				}
			}
		})
	}
}
