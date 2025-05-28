package flows

import (
	"context"
	"testing"

	"github.com/agglayer/aggkit/aggsender/config"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/log"
	typesmocks "github.com/agglayer/aggkit/types/mocks"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/stretchr/testify/require"
)

func TestNewFlow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

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
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockStorage := new(mocks.AggSenderStorage)
			mockL1Client := new(typesmocks.BaseEthereumClienter)
			mockL2Client := new(typesmocks.BaseEthereumClienter)
			mockL1InfoTreeSyncer := new(mocks.L1InfoTreeSyncer)
			mockL2BridgeSyncer := new(mocks.L2BridgeSyncer)
			mockL2BridgeSyncer.EXPECT().OriginNetwork().Return(1)
			mockLogger := log.WithFields("test", "NewFlow")

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
