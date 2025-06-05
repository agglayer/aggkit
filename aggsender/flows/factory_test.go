package flows

import (
	"context"
	"testing"

	"github.com/agglayer/aggkit/aggoracle/chaingerreader"
	aggoracletypes "github.com/agglayer/aggkit/aggoracle/types"
	"github.com/agglayer/aggkit/aggsender/config"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/optimistic"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/log"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewFlow(t *testing.T) {
	t.Parallel()
	keyConfig := signertypes.SignerConfig{
		Method: signertypes.MethodLocal,
		Config: map[string]any{
			"password": "password",
			"path":     "../../test/config/key_trusted_sequencer.keystore",
		},
	}
	testCases := []struct {
		name          string
		cfg           config.Config
		expectedError string
	}{
		{
			name: "success with PessimisticProofMode",
			cfg: config.Config{
				Mode:                string(types.PessimisticProofMode),
				AggsenderPrivateKey: signertypes.SignerConfig{Method: signertypes.MethodNone},
				MaxCertSize:         100,
			},
		},
		{
			name: "error creating signer in PessimisticProofMode",
			cfg: config.Config{
				Mode: string(types.PessimisticProofMode),
				AggsenderPrivateKey: signertypes.SignerConfig{
					Method: signertypes.MethodLocal,
				},
			},
			expectedError: "error signer.Initialize",
		},
		{
			name: "error creating signer in AggchainProofMode",
			cfg: config.Config{
				Mode: string(types.AggchainProofMode),
				AggsenderPrivateKey: signertypes.SignerConfig{
					Method: signertypes.MethodLocal,
				},
			},
			expectedError: "error signer.Initialize",
		},
		{
			name: "error missing AggchainProofURL in AggchainProofMode",
			cfg: config.Config{
				Mode:                string(types.AggchainProofMode),
				AggsenderPrivateKey: signertypes.SignerConfig{Method: signertypes.MethodNone},
			},
			expectedError: "aggchain prover mode requires AggchainProofURL",
		},
		{
			name: "unsupported Aggsender mode",
			cfg: config.Config{
				Mode: "unsupported-mode",
			},
			expectedError: "unsupported Aggsender mode: unsupported-mode",
		},
		{
			name: "error optimistic mode creating TrustedSequencerContract AggchainProofMode",
			cfg: config.Config{
				Mode:                string(types.AggchainProofMode),
				AggsenderPrivateKey: keyConfig,
				AggchainProofURL:    "http://127.0.0.1",
				OptimisticModeConfig: optimistic.Config{
					TrustedSequencerKey: keyConfig,
				},
			},
			expectedError: "error aggchainFEPContract",
		},
	}
	funcNewEVMChainGERReader = func(_ common.Address, _ aggoracletypes.EthClienter) (*chaingerreader.EVMChainGERReader, error) {
		return &chaingerreader.EVMChainGERReader{}, nil
	}
	funcGetL2StartBlock = func(_ common.Address, _ types.EthClient) (uint64, error) {
		return 100, nil
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			mockStorage := new(mocks.AggSenderStorage)
			mockL1Client := new(mocks.EthClient)
			mockL2Client := new(mocks.EthClient)
			mockL1InfoTreeSyncer := new(mocks.L1InfoTreeSyncer)
			mockL2BridgeSyncer := new(mocks.L2BridgeSyncer)
			mockL2BridgeSyncer.EXPECT().OriginNetwork().Return(1)
			mockLogger := log.WithFields("test", "NewFlow")

			mockL1Client.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).Return([]byte{1, 2, 3}, nil).Maybe()
			mockL1Client.EXPECT().CodeAt(mock.Anything, mock.Anything, mock.Anything).Return([]byte{1, 2, 3}, nil).Maybe()
			mockL2Client.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).Return([]byte{1, 2, 3}, nil).Maybe()
			mockL2Client.EXPECT().CodeAt(mock.Anything, mock.Anything, mock.Anything).Return([]byte{1, 2, 3}, nil).Maybe()
			flow, err := NewFlow(
				ctx,
				tc.cfg,
				mockLogger,
				mockStorage,
				mockL1Client,
				mockL2Client,
				mockL1InfoTreeSyncer,
				mockL2BridgeSyncer,
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
