package flows

import (
	"context"
	"testing"
	"time"

	"github.com/agglayer/aggkit/aggoracle/chaingerreader"
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
		expectedError string
	}{
		{
			name: "success with PessimisticProofMode",
			cfg: config.Config{
				Mode:                string(types.PessimisticProofMode),
				AggsenderPrivateKey: signertypes.SignerConfig{Method: signertypes.MethodNone},
				MaxCertSize:         100,
				AggkitProverClient:  aggkitgrpc.DefaultConfig(),
			},
		},
		{
			name: "error creating signer in PessimisticProofMode",
			cfg: config.Config{
				Mode: string(types.PessimisticProofMode),
				AggsenderPrivateKey: signertypes.SignerConfig{
					Method: signertypes.MethodLocal,
				},
				AggkitProverClient: aggkitgrpc.DefaultConfig(),
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
				AggkitProverClient: aggkitgrpc.DefaultConfig(),
			},
			expectedError: "error signer.Initialize",
		},
		{
			name: "error missing AggkitProverClient in AggchainProofMode",
			cfg: config.Config{
				Mode:                string(types.AggchainProofMode),
				AggsenderPrivateKey: signertypes.SignerConfig{Method: signertypes.MethodNone},
			},
			expectedError: "invalid aggkit prover client config: gRPC client configuration cannot be nil",
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
				AggkitProverClient: &aggkitgrpc.ClientConfig{
					URL:               "http://127.0.0.1",
					MinConnectTimeout: cfgtypes.Duration{Duration: 1 * time.Millisecond},
				},
				OptimisticModeConfig: optimistic.Config{
					TrustedSequencerKey:             keyConfig,
					RequireKeyMatchTrustedSequencer: true,
				},
			},
			expectedError: "error aggchainFEPContract",
		},
	}
	funcNewEVMChainGERReader = func(_ common.Address, _ aggkittypes.BaseEthereumClienter) (*chaingerreader.EVMChainGERReader, error) {
		return &chaingerreader.EVMChainGERReader{}, nil
	}
	funcGetL2StartBlock = func(_ common.Address, _ aggkittypes.BaseEthereumClienter) (uint64, error) {
		return 100, nil
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			mockStorage := mocks.NewAggSenderStorage(t)
			mockL1Client := typesmocks.NewBaseEthereumClienter(t)
			mockL2Client := typesmocks.NewBaseEthereumClienter(t)
			mockL1InfoTreeSyncer := mocks.NewL1InfoTreeSyncer(t)
			mockL2BridgeSyncer := mocks.NewL2BridgeSyncer(t)
			mockRollupDataQuerier := mocks.NewRollupDataQuerier(t)

			mockL2BridgeSyncer.EXPECT().OriginNetwork().Return(1).Maybe()
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
				mockRollupDataQuerier,
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
