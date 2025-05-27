//nolint:dupl
package grpc

import (
	"context"
	"errors"
	"math/big"
	"testing"

	v1nodetypes "buf.build/gen/go/agglayer/agglayer/protocolbuffers/go/agglayer/node/types/v1"
	node "buf.build/gen/go/agglayer/agglayer/protocolbuffers/go/agglayer/node/v1"
	v1types "buf.build/gen/go/agglayer/interop/protocolbuffers/go/agglayer/interop/types/v1"
	"github.com/agglayer/aggkit/agglayer/mocks"
	"github.com/agglayer/aggkit/agglayer/types"
	aggkitgrpc "github.com/agglayer/aggkit/grpc"
	"github.com/agglayer/aggkit/tree"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestGetEpochConfiguration(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("returns error", func(t *testing.T) {
		t.Parallel()

		cfgServiceMock := mocks.NewConfigurationServiceClient(t)
		client := &AgglayerGRPCClient{
			cfgService: cfgServiceMock,
			cfg:        aggkitgrpc.DefaultConfig(),
		}

		cfgServiceMock.EXPECT().GetEpochConfiguration(ctx, mock.Anything).Return(nil, errors.New("test error"))

		_, err := client.GetEpochConfiguration(ctx)
		require.ErrorContains(t, err, "test error")
	})

	t.Run("returns response", func(t *testing.T) {
		t.Parallel()

		cfgServiceMock := mocks.NewConfigurationServiceClient(t)
		client := &AgglayerGRPCClient{
			cfgService: cfgServiceMock,
			cfg:        aggkitgrpc.DefaultConfig(),
		}

		expectedResponse := &node.GetEpochConfigurationResponse{
			EpochConfiguration: &v1nodetypes.EpochConfiguration{
				GenesisBlock:  1000,
				EpochDuration: 10,
			},
		}

		cfgServiceMock.EXPECT().GetEpochConfiguration(ctx, mock.Anything).Return(expectedResponse, nil)

		resp, err := client.GetEpochConfiguration(ctx)
		require.NoError(t, err)
		require.Equal(t, expectedResponse.EpochConfiguration.EpochDuration, resp.EpochDuration)
		require.Equal(t, expectedResponse.EpochConfiguration.GenesisBlock, resp.GenesisBlock)
	})
}

func TestGetLatestPendingCertificateHeader(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	networkID := uint32(1)

	t.Run("returns error", func(t *testing.T) {
		t.Parallel()

		networkStateServiceMock := mocks.NewNodeStateServiceClient(t)
		client := &AgglayerGRPCClient{
			networkStateService: networkStateServiceMock,
			cfg:                 aggkitgrpc.DefaultConfig(),
		}

		networkStateServiceMock.EXPECT().GetLatestCertificateHeader(ctx, mock.Anything).Return(nil, errors.New("test error"))

		_, err := client.GetLatestPendingCertificateHeader(ctx, networkID)
		require.ErrorContains(t, err, "test error")
	})

	t.Run("returns response", func(t *testing.T) {
		t.Parallel()

		networkStateServiceMock := mocks.NewNodeStateServiceClient(t)
		client := &AgglayerGRPCClient{
			networkStateService: networkStateServiceMock,
			cfg:                 aggkitgrpc.DefaultConfig(),
		}

		epoch := uint64(10)
		certificateIndex := uint64(1)

		expectedResponse := &node.GetLatestCertificateHeaderResponse{
			CertificateHeader: &v1nodetypes.CertificateHeader{
				NetworkId:        networkID,
				Height:           100,
				EpochNumber:      &epoch,
				CertificateIndex: &certificateIndex,
				CertificateId: &v1nodetypes.CertificateId{
					Value: &v1types.FixedBytes32{
						Value: common.HexToHash("0x010203").Bytes(),
					},
				},
				PrevLocalExitRoot: &v1types.FixedBytes32{
					Value: common.HexToHash("0x010201").Bytes(),
				},
				NewLocalExitRoot: &v1types.FixedBytes32{
					Value: common.HexToHash("0x010202").Bytes(),
				},
				Status: v1nodetypes.CertificateStatus_CERTIFICATE_STATUS_PENDING,
				Metadata: &v1types.FixedBytes32{
					Value: common.HexToHash("0x011201").Bytes(),
				},
			},
		}

		networkStateServiceMock.EXPECT().GetLatestCertificateHeader(ctx, mock.Anything).Return(expectedResponse, nil)

		resp, err := client.GetLatestPendingCertificateHeader(ctx, networkID)
		require.NoError(t, err)

		require.Equal(t, expectedResponse.CertificateHeader.NetworkId, resp.NetworkID)
		require.Equal(t, expectedResponse.CertificateHeader.Height, resp.Height)
		require.Equal(t, expectedResponse.CertificateHeader.EpochNumber, resp.EpochNumber)
		require.Equal(t, expectedResponse.CertificateHeader.CertificateIndex, resp.CertificateIndex)
		require.Equal(t, expectedResponse.CertificateHeader.CertificateId.Value.Value, resp.CertificateID.Bytes())
		require.Equal(t, expectedResponse.CertificateHeader.PrevLocalExitRoot.Value, resp.PreviousLocalExitRoot.Bytes())
		require.Equal(t, expectedResponse.CertificateHeader.NewLocalExitRoot.Value, resp.NewLocalExitRoot.Bytes())
		require.Equal(t, certificateStatusFromProto(expectedResponse.CertificateHeader.Status), resp.Status)
		require.Equal(t, expectedResponse.CertificateHeader.Metadata.Value, resp.Metadata.Bytes())
	})
}

func TestGetLatestSettledCertificateHeader(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	networkID := uint32(1)

	t.Run("returns error", func(t *testing.T) {
		t.Parallel()

		networkStateServiceMock := mocks.NewNodeStateServiceClient(t)
		client := &AgglayerGRPCClient{
			networkStateService: networkStateServiceMock,
			cfg:                 aggkitgrpc.DefaultConfig(),
		}

		networkStateServiceMock.EXPECT().GetLatestCertificateHeader(ctx, mock.Anything).Return(nil, errors.New("test error"))

		_, err := client.GetLatestSettledCertificateHeader(ctx, networkID)
		require.ErrorContains(t, err, "test error")
	})

	t.Run("returns response", func(t *testing.T) {
		t.Parallel()

		networkStateServiceMock := mocks.NewNodeStateServiceClient(t)
		client := &AgglayerGRPCClient{
			networkStateService: networkStateServiceMock,
			cfg:                 aggkitgrpc.DefaultConfig(),
		}

		epoch := uint64(10)
		certificateIndex := uint64(1)

		expectedResponse := &node.GetLatestCertificateHeaderResponse{
			CertificateHeader: &v1nodetypes.CertificateHeader{
				NetworkId:        networkID,
				Height:           100,
				EpochNumber:      &epoch,
				CertificateIndex: &certificateIndex,
				CertificateId: &v1nodetypes.CertificateId{
					Value: &v1types.FixedBytes32{
						Value: common.HexToHash("0x010203").Bytes(),
					},
				},
				PrevLocalExitRoot: &v1types.FixedBytes32{
					Value: common.HexToHash("0x010201").Bytes(),
				},
				NewLocalExitRoot: &v1types.FixedBytes32{
					Value: common.HexToHash("0x010202").Bytes(),
				},
				Status: v1nodetypes.CertificateStatus_CERTIFICATE_STATUS_SETTLED,
				Metadata: &v1types.FixedBytes32{
					Value: common.HexToHash("0x011201").Bytes(),
				},
			},
		}

		networkStateServiceMock.EXPECT().GetLatestCertificateHeader(ctx, mock.Anything).Return(expectedResponse, nil)

		resp, err := client.GetLatestSettledCertificateHeader(ctx, networkID)
		require.NoError(t, err)

		require.Equal(t, expectedResponse.CertificateHeader.NetworkId, resp.NetworkID)
		require.Equal(t, expectedResponse.CertificateHeader.Height, resp.Height)
		require.Equal(t, expectedResponse.CertificateHeader.EpochNumber, resp.EpochNumber)
		require.Equal(t, expectedResponse.CertificateHeader.CertificateIndex, resp.CertificateIndex)
		require.Equal(t, expectedResponse.CertificateHeader.CertificateId.Value.Value, resp.CertificateID.Bytes())
		require.Equal(t, expectedResponse.CertificateHeader.PrevLocalExitRoot.Value, resp.PreviousLocalExitRoot.Bytes())
		require.Equal(t, expectedResponse.CertificateHeader.NewLocalExitRoot.Value, resp.NewLocalExitRoot.Bytes())
		require.Equal(t, certificateStatusFromProto(expectedResponse.CertificateHeader.Status), resp.Status)
		require.Equal(t, expectedResponse.CertificateHeader.Metadata.Value, resp.Metadata.Bytes())
	})
}

func TestGetCertificateHeader(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	certificateID := common.HexToHash("0x010203")

	t.Run("returns error", func(t *testing.T) {
		t.Parallel()

		networkStateServiceMock := mocks.NewNodeStateServiceClient(t)
		client := &AgglayerGRPCClient{
			networkStateService: networkStateServiceMock,
			cfg:                 aggkitgrpc.DefaultConfig(),
		}

		networkStateServiceMock.EXPECT().GetCertificateHeader(ctx, mock.Anything).Return(nil, errors.New("test error"))

		_, err := client.GetCertificateHeader(ctx, certificateID)
		require.ErrorContains(t, err, "test error")
	})

	t.Run("returns response", func(t *testing.T) {
		t.Parallel()

		networkStateServiceMock := mocks.NewNodeStateServiceClient(t)
		client := &AgglayerGRPCClient{
			networkStateService: networkStateServiceMock,
			cfg:                 aggkitgrpc.DefaultConfig(),
		}

		epoch := uint64(10)
		certificateIndex := uint64(1)

		expectedResponse := &node.GetCertificateHeaderResponse{
			CertificateHeader: &v1nodetypes.CertificateHeader{
				NetworkId:        1,
				Height:           100,
				EpochNumber:      &epoch,
				CertificateIndex: &certificateIndex,
				CertificateId: &v1nodetypes.CertificateId{
					Value: &v1types.FixedBytes32{
						Value: certificateID.Bytes(),
					},
				},
				PrevLocalExitRoot: &v1types.FixedBytes32{
					Value: common.HexToHash("0x010201").Bytes(),
				},
				NewLocalExitRoot: &v1types.FixedBytes32{
					Value: common.HexToHash("0x010202").Bytes(),
				},
				Status: v1nodetypes.CertificateStatus_CERTIFICATE_STATUS_SETTLED,
				Metadata: &v1types.FixedBytes32{
					Value: common.HexToHash("0x011201").Bytes(),
				},
			},
		}

		networkStateServiceMock.EXPECT().GetCertificateHeader(ctx, mock.Anything).Return(expectedResponse, nil)

		resp, err := client.GetCertificateHeader(ctx, certificateID)
		require.NoError(t, err)

		require.Equal(t, expectedResponse.CertificateHeader.NetworkId, resp.NetworkID)
		require.Equal(t, expectedResponse.CertificateHeader.Height, resp.Height)
		require.Equal(t, expectedResponse.CertificateHeader.EpochNumber, resp.EpochNumber)
		require.Equal(t, expectedResponse.CertificateHeader.CertificateIndex, resp.CertificateIndex)
		require.Equal(t, expectedResponse.CertificateHeader.CertificateId.Value.Value, resp.CertificateID.Bytes())
		require.Equal(t, expectedResponse.CertificateHeader.PrevLocalExitRoot.Value, resp.PreviousLocalExitRoot.Bytes())
		require.Equal(t, expectedResponse.CertificateHeader.NewLocalExitRoot.Value, resp.NewLocalExitRoot.Bytes())
		require.Equal(t, certificateStatusFromProto(expectedResponse.CertificateHeader.Status), resp.Status)
		require.Equal(t, expectedResponse.CertificateHeader.Metadata.Value, resp.Metadata.Bytes())
	})
}

func TestSendCertificate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("returns error when AggchainData not defined", func(t *testing.T) {
		t.Parallel()

		client := &AgglayerGRPCClient{
			cfg: aggkitgrpc.DefaultConfig(),
		}

		certificate := &types.Certificate{}

		_, err := client.SendCertificate(ctx, certificate)
		require.ErrorIs(t, err, errUndefinedAggchainData)
	})

	t.Run("returns error from submission service", func(t *testing.T) {
		t.Parallel()

		submissionServiceMock := mocks.NewCertificateSubmissionServiceClient(t)
		client := &AgglayerGRPCClient{
			submissionService: submissionServiceMock,
			cfg:               aggkitgrpc.DefaultConfig(),
		}

		certificate := &types.Certificate{
			AggchainData: &types.AggchainDataSignature{
				Signature: []byte{0x01},
			},
		}

		submissionServiceMock.EXPECT().SubmitCertificate(ctx, mock.Anything).Return(nil, errors.New("test error"))

		_, err := client.SendCertificate(ctx, certificate)
		require.ErrorContains(t, err, "test error")
	})

	t.Run("returns certificate ID on success", func(t *testing.T) {
		t.Parallel()

		submissionServiceMock := mocks.NewCertificateSubmissionServiceClient(t)
		client := &AgglayerGRPCClient{
			submissionService: submissionServiceMock,
			cfg:               aggkitgrpc.DefaultConfig(),
		}

		certificate := &types.Certificate{
			AggchainData: &types.AggchainDataProof{
				Proof:          []byte{0x01},
				AggchainParams: common.HexToHash("0x010203"),
			},
			NetworkID:           1,
			Height:              100,
			PrevLocalExitRoot:   common.HexToHash("0x010201"),
			NewLocalExitRoot:    common.HexToHash("0x010202"),
			Metadata:            common.HexToHash("0x011201"),
			CustomChainData:     []byte{0x1, 0x2, 0x3},
			L1InfoTreeLeafCount: 11,
			BridgeExits: []*types.BridgeExit{
				{
					LeafType: types.LeafTypeAsset,
					TokenInfo: &types.TokenInfo{
						OriginNetwork:      2,
						OriginTokenAddress: common.HexToAddress("0x010203"),
					},
					DestinationNetwork: 1,
					DestinationAddress: common.HexToAddress("0x010204"),
					Amount:             big.NewInt(100),
				},
			},
			ImportedBridgeExits: []*types.ImportedBridgeExit{
				{
					BridgeExit: &types.BridgeExit{
						LeafType: types.LeafTypeAsset,
						TokenInfo: &types.TokenInfo{
							OriginNetwork:      1,
							OriginTokenAddress: common.HexToAddress("0x01111"),
						},
						DestinationNetwork: 2,
						DestinationAddress: common.HexToAddress("0x011112"),
						Amount:             big.NewInt(101),
					},
					GlobalIndex: &types.GlobalIndex{
						MainnetFlag: true,
						RollupIndex: 0,
						LeafIndex:   1,
					},
					ClaimData: &types.ClaimFromMainnnet{
						ProofLeafMER: &types.MerkleProof{
							Root:  common.HexToHash("0x010203"),
							Proof: tree.EmptyProof,
						},
						ProofGERToL1Root: &types.MerkleProof{
							Root:  common.HexToHash("0x0102011"),
							Proof: tree.EmptyProof,
						},
						L1Leaf: &types.L1InfoTreeLeaf{
							L1InfoTreeIndex: 1,
							RollupExitRoot:  common.HexToHash("0x0102012"),
							MainnetExitRoot: common.HexToHash("0x0102013"),
							Inner: &types.L1InfoTreeLeafInner{
								GlobalExitRoot: common.HexToHash("0x0102014"),
								BlockHash:      common.HexToHash("0x0102015"),
								Timestamp:      1234567890,
							},
						},
					},
				},
				{
					BridgeExit: &types.BridgeExit{
						LeafType: types.LeafTypeMessage,
						TokenInfo: &types.TokenInfo{
							OriginNetwork:      11,
							OriginTokenAddress: common.HexToAddress("0x011"),
						},
						DestinationNetwork: 22,
						DestinationAddress: common.HexToAddress("0x012"),
					},
					GlobalIndex: &types.GlobalIndex{
						MainnetFlag: false,
						RollupIndex: 11,
						LeafIndex:   2,
					},
					ClaimData: &types.ClaimFromRollup{
						ProofLeafLER: &types.MerkleProof{
							Root:  common.HexToHash("0x0112"),
							Proof: tree.EmptyProof,
						},
						ProofGERToL1Root: &types.MerkleProof{
							Root:  common.HexToHash("0x0122"),
							Proof: tree.EmptyProof,
						},
						ProofLERToRER: &types.MerkleProof{
							Root:  common.HexToHash("0x0123"),
							Proof: tree.EmptyProof,
						},
						L1Leaf: &types.L1InfoTreeLeaf{
							L1InfoTreeIndex: 2,
							RollupExitRoot:  common.HexToHash("0x11"),
							MainnetExitRoot: common.HexToHash("0x12"),
							Inner: &types.L1InfoTreeLeafInner{
								GlobalExitRoot: common.HexToHash("0x13"),
								BlockHash:      common.HexToHash("0x14"),
								Timestamp:      122222,
							},
						},
					},
				},
			},
		}

		expectedResponse := &node.SubmitCertificateResponse{
			CertificateId: &v1nodetypes.CertificateId{
				Value: &v1types.FixedBytes32{
					Value: common.HexToHash("0x010203").Bytes(),
				},
			},
		}

		submissionServiceMock.EXPECT().SubmitCertificate(ctx, mock.Anything).Return(expectedResponse, nil)

		resp, err := client.SendCertificate(ctx, certificate)
		require.NoError(t, err)
		require.Equal(t, expectedResponse.CertificateId.Value.Value, resp.Bytes())
	})
}

func TestLeafTypeToProto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    types.LeafType
		expected v1types.LeafType
	}{
		{
			name:     "LeafTypeAsset",
			input:    types.LeafTypeAsset,
			expected: v1types.LeafType_LEAF_TYPE_TRANSFER,
		},
		{
			name:     "LeafTypeMessage",
			input:    types.LeafTypeMessage,
			expected: v1types.LeafType_LEAF_TYPE_MESSAGE,
		},
		{
			name:     "Default case",
			input:    types.LeafType(99), // some undefined leaf type
			expected: v1types.LeafType_LEAF_TYPE_UNSPECIFIED,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := leafTypeToProto(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestCertificateStatusFromProto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    v1nodetypes.CertificateStatus
		expected types.CertificateStatus
	}{
		{
			name:     "Pending status",
			input:    v1nodetypes.CertificateStatus_CERTIFICATE_STATUS_PENDING,
			expected: types.Pending,
		},
		{
			name:     "Proven status",
			input:    v1nodetypes.CertificateStatus_CERTIFICATE_STATUS_PROVEN,
			expected: types.Proven,
		},
		{
			name:     "Candidate status",
			input:    v1nodetypes.CertificateStatus_CERTIFICATE_STATUS_CANDIDATE,
			expected: types.Candidate,
		},
		{
			name:     "InError status",
			input:    v1nodetypes.CertificateStatus_CERTIFICATE_STATUS_IN_ERROR,
			expected: types.InError,
		},
		{
			name:     "Settled status",
			input:    v1nodetypes.CertificateStatus_CERTIFICATE_STATUS_SETTLED,
			expected: types.Settled,
		},
		{
			name:     "Default status",
			input:    v1nodetypes.CertificateStatus_CERTIFICATE_STATUS_UNSPECIFIED,
			expected: types.Pending,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := certificateStatusFromProto(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}
