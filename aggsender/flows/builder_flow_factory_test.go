package flows

import (
	"errors"
	"testing"
	"time"

	"github.com/agglayer/aggkit/aggsender/config"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/optimistic"
	"github.com/agglayer/aggkit/aggsender/types"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	aggkitgrpc "github.com/agglayer/aggkit/grpc"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	typesmocks "github.com/agglayer/aggkit/types/mocks"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewFlow(t *testing.T) {
	t.Parallel()
	keyConfig := signertypes.SignerConfig{
		Method: signertypes.MethodMock,
	}
	testCases := []struct {
		name          string
		cfg           config.Config
		mockFn        func(*mocks.MultisigQuerier)
		expectedError string
	}{
		{
			name: "success with PessimisticProofMode",
			cfg: config.Config{
				Mode:                       types.PessimisticProofMode,
				AggsenderPrivateKey:        signertypes.SignerConfig{Method: signertypes.MethodNone},
				MaxCertSize:                100,
				AggkitProverClient:         aggkitgrpc.DefaultConfig(),
				BlockFinalityForL1InfoTree: aggkittypes.FinalizedBlock,
			},
			mockFn: func(mockCommittee *mocks.MultisigQuerier) {
				committee, err := types.NewMultisigCommittee([]*types.SignerInfo{types.NewSignerInfo("", common.Address{})}, 1)
				require.NoError(t, err)

				mockCommittee.EXPECT().GetMultisigCommittee(mock.Anything, mock.Anything).Return(committee, nil).Maybe()
			},
		},
		{
			name: "error getting multisig committee when RequireCommitteeMembershipCheck is true",
			cfg: config.Config{
				Mode:                            types.PessimisticProofMode,
				AggsenderPrivateKey:             signertypes.SignerConfig{Method: signertypes.MethodNone},
				MaxCertSize:                     100,
				AggkitProverClient:              aggkitgrpc.DefaultConfig(),
				RequireCommitteeMembershipCheck: true,
				BlockFinalityForL1InfoTree:      aggkittypes.FinalizedBlock,
			},
			mockFn: func(mockCommittee *mocks.MultisigQuerier) {
				mockCommittee.EXPECT().GetMultisigCommittee(mock.Anything, mock.Anything).Return(nil, errors.New("test error")).Maybe()
			},
			expectedError: "error getting multisig committee: test error",
		},
		{
			name: "error getting multisig committee when RequireCommitteeMembershipCheck is false",
			cfg: config.Config{
				Mode:                            types.PessimisticProofMode,
				AggsenderPrivateKey:             signertypes.SignerConfig{Method: signertypes.MethodNone},
				MaxCertSize:                     100,
				AggkitProverClient:              aggkitgrpc.DefaultConfig(),
				RequireCommitteeMembershipCheck: false,
				BlockFinalityForL1InfoTree:      aggkittypes.FinalizedBlock,
			},
			mockFn: func(mockCommittee *mocks.MultisigQuerier) {
				mockCommittee.EXPECT().GetMultisigCommittee(mock.Anything, mock.Anything).Return(nil, errors.New("test error")).Maybe()
			},
		},
		{
			name: "committee membership check disabled with PessimisticProofMode",
			cfg: config.Config{
				Mode:                            types.PessimisticProofMode,
				AggsenderPrivateKey:             signertypes.SignerConfig{Method: signertypes.MethodNone},
				MaxCertSize:                     100,
				AggkitProverClient:              aggkitgrpc.DefaultConfig(),
				RequireCommitteeMembershipCheck: false,
				BlockFinalityForL1InfoTree:      aggkittypes.FinalizedBlock,
			},
			mockFn: func(mockCommittee *mocks.MultisigQuerier) {
				signers := []*types.SignerInfo{
					types.NewSignerInfo("http://signer2", common.HexToAddress("0x2222222222222222222222222222222222222222")),
					types.NewSignerInfo("http://signer3", common.HexToAddress("0x3333333333333333333333333333333333333333")),
					types.NewSignerInfo("http://signer4", common.HexToAddress("0x4444444444444444444444444444444444")),
				}

				committee, err := types.NewMultisigCommittee(signers, 2)
				require.NoError(t, err)
				mockCommittee.EXPECT().GetMultisigCommittee(mock.Anything, mock.Anything).Return(committee, nil).Maybe()
			},
		},
		{
			name: "not member of committee",
			cfg: config.Config{
				Mode:                            types.PessimisticProofMode,
				AggsenderPrivateKey:             signertypes.SignerConfig{Method: signertypes.MethodNone},
				MaxCertSize:                     100,
				AggkitProverClient:              aggkitgrpc.DefaultConfig(),
				RequireCommitteeMembershipCheck: true,
				BlockFinalityForL1InfoTree:      aggkittypes.FinalizedBlock,
			},
			mockFn: func(mockCommittee *mocks.MultisigQuerier) {
				signers := []*types.SignerInfo{
					types.NewSignerInfo("http://signer2", common.HexToAddress("0x2222222222222222222222222222222222222222")),
					types.NewSignerInfo("http://signer3", common.HexToAddress("0x3333333333333333333333333333333333333333")),
					types.NewSignerInfo("http://signer4", common.HexToAddress("0x4444444444444444444444444444444444444444")),
				}

				committee, err := types.NewMultisigCommittee(signers, 2)
				require.NoError(t, err)
				mockCommittee.EXPECT().GetMultisigCommittee(mock.Anything, mock.Anything).Return(committee, nil).Maybe()
			},
			expectedError: "signer address 0x0000000000000000000000000000000000000000 is not part of the multisig committee",
		},
		{
			name: "error creating signer in PessimisticProofMode",
			cfg: config.Config{
				Mode: types.PessimisticProofMode,
				AggsenderPrivateKey: signertypes.SignerConfig{
					Method: signertypes.MethodLocal,
				},
				AggkitProverClient:         aggkitgrpc.DefaultConfig(),
				BlockFinalityForL1InfoTree: aggkittypes.FinalizedBlock,
			},
			expectedError: "error signer.Initialize",
		},
		{
			name: "unsupported Aggsender mode",
			cfg: config.Config{
				Mode: "unsupported-mode",
			},
			expectedError: "unsupported Aggsender mode: unsupported-mode",
		},
		{
			name: "error optimistic mode fetching aggchain signers in AggchainProofMode",
			cfg: config.Config{
				Mode:                types.AggchainProofMode,
				AggsenderPrivateKey: keyConfig,
				AggkitProverClient: &aggkitgrpc.ClientConfig{
					URL:               "http://127.0.0.1",
					MinConnectTimeout: cfgtypes.Duration{Duration: 1 * time.Millisecond},
				},
				OptimisticModeConfig: optimistic.Config{
					TrustedSequencerKey:             keyConfig,
					RequireKeyMatchTrustedSequencer: true,
				},
				BlockFinalityForL1InfoTree: aggkittypes.FinalizedBlock,
			},
			expectedError: "failed to fetch the aggchain signers from the AggchainFEP contract",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()

			mockStorage := mocks.NewAggSenderStorage(t)
			mockL1Client := typesmocks.NewBaseEthereumClienter(t)
			mockL2Client := typesmocks.NewBaseEthereumClienter(t)
			mockL1InfoTreeSyncer := mocks.NewL1InfoTreeSyncer(t)
			mockL2BridgeSyncer := mocks.NewL2BridgeSyncer(t)
			mockRollupDataQuerier := mocks.NewRollupDataQuerier(t)
			mockCommitteeQuerier := mocks.NewMultisigQuerier(t)

			mockL2BridgeSyncer.EXPECT().OriginNetwork().Return(1).Maybe()
			mockLogger := log.WithFields("test", "NewFlow")
			mockL1InfoTreeSyncer.EXPECT().Finality().Return(aggkittypes.FinalizedBlock).Maybe()
			mockL1Client.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).Return([]byte{1, 2, 3}, nil).Maybe()
			mockL1Client.EXPECT().CodeAt(mock.Anything, mock.Anything, mock.Anything).Return([]byte{1, 2, 3}, nil).Maybe()
			mockL2Client.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).Return([]byte{1, 2, 3}, nil).Maybe()
			mockL2Client.EXPECT().CodeAt(mock.Anything, mock.Anything, mock.Anything).Return([]byte{1, 2, 3}, nil).Maybe()
			mockRollupDataQuerier.EXPECT().GetRollupChainID().Return(uint64(1234), nil).Maybe()

			if tc.mockFn != nil {
				tc.mockFn(mockCommitteeQuerier)
			}
			tc.cfg.OptimisticModeConfig.SovereignRollupAddr = common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
			tc.cfg.OptimisticModeConfig.OpNodeURL = "http://localhost:8545"
			flow, err := NewBuilderFlow(
				ctx,
				tc.cfg,
				mockLogger,
				mockStorage,
				mockL1Client,
				mockL2Client,
				mockL1InfoTreeSyncer,
				mockL2BridgeSyncer,
				nil, // l2ClaimSyncer
				mockRollupDataQuerier,
				mockCommitteeQuerier,
				nil, // certQuerier
				common.Hash{},
			)

			if tc.expectedError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectedError)
				require.Nil(t, flow)
			} else {
				require.NoError(t, err)
				require.NotNil(t, flow)
			}
		})
	}
}
