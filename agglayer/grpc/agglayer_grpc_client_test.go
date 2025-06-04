// //nolint:dupl
package grpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"testing"

	v1nodetypes "buf.build/gen/go/agglayer/agglayer/protocolbuffers/go/agglayer/node/types/v1"
	node "buf.build/gen/go/agglayer/agglayer/protocolbuffers/go/agglayer/node/v1"
	v1types "buf.build/gen/go/agglayer/interop/protocolbuffers/go/agglayer/interop/types/v1"
	"github.com/agglayer/aggkit/agglayer/mocks"
	"github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/tree"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestGetEpochConfiguration(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("returns error", func(t *testing.T) {
		t.Parallel()

		cfgServiceMock := mocks.NewConfigurationServiceClient(t)
		client := &AgglayerGRPCClient{
			cfgService: cfgServiceMock,
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

		client := &AgglayerGRPCClient{}

		certificate := &types.Certificate{}

		_, err := client.SendCertificate(ctx, certificate)
		require.ErrorIs(t, err, errUndefinedAggchainData)
	})

	t.Run("returns error from submission service", func(t *testing.T) {
		t.Parallel()

		submissionServiceMock := mocks.NewCertificateSubmissionServiceClient(t)
		client := &AgglayerGRPCClient{
			submissionService: submissionServiceMock,
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

func TestExploratory_EstimateCertSize(t *testing.T) {
	t.Skip("This test is for exploratory purposes to check the size of certificate")

	certJSON := `{"network_id":1,"height":2,"prev_local_exit_root":"0x6a0ec7af851e56f48631f0fcb72fc62d52d241dc53fdc1fa4b4ca76829c8d700","new_local_exit_root":"0xa5383122f3e98f64a0f8f9e5a8b1a103df2416101a778d70d464d4f0e1aaa817","bridge_exits":[{"leaf_type":"Transfer","token_info":{"origin_network":0,"origin_token_address":"0x0000000000000000000000000000000000000000"},"dest_network":0,"dest_address":"0xbafa5a34dc4967eb0d60bf3d44dfbad3fa8b4001","amount":"1748855837","metadata":null}],"imported_bridge_exits":[{"bridge_exit":{"leaf_type":"Transfer","token_info":{"origin_network":0,"origin_token_address":"0x0000000000000000000000000000000000000000"},"dest_network":1,"dest_address":"0xbafa5a34dc4967eb0d60bf3d44dfbad3fa8b4001","amount":"1748855810","metadata":null},"claim_data":{"Mainnet":{"l1_leaf":{"l1_info_tree_index":4,"rer":"0x0000000000000000000000000000000000000000000000000000000000000000","mer":"0x663adc4b2dc606cc2893e8a9a95d6b2f7145e2a6af0b43e56562c5eb266d6770","inner":{"global_exit_root":"0x49b56878dfe65ee417ce698d6629cd81b11d3cc70020c79ea9062a0749b0f451","block_hash":"0x587914277496768ae72e6c37f5648071ccac41875c0eafcfe116fef4c2fdd6bb","timestamp":1748855818}},"proof_ger_l1root":{"root":"0x74287dd187efa4e0cd1e1061c57a0e1a20f3f0c372096b48e4719627e22d3e05","proof":{"siblings":["0x1a4ce783d5c9f18bf2faa61be2a2dc98eac1fdea71460253804436af03911f1e","0x686d07ccb1f64cc7ab16f89a1a0fa683584b7d855644e1d60d7ca5dcea3a0c3a","0x9f49e854458fc0559b84af68f60cf2b84362b30bd31deaa526b666c2087fc4aa","0x020fb8d0311ed81d4b82fa438968aaddd8c3642853c87453f11629d447aecb1d","0x7acc76de3faf926278e05c1e1e6be42b9695dd6ef3a2511af963936a0763f537","0x8258c703d66c7751f8d2f47bb7c0aeebc9aba766c2ef2216648813775293e00d","0x8757b1ad77709d6070e79f04b75ae20de22b509f9dfe2d8158d7b33971525d56","0xffd70157e48063fc33c97a050f7f640233bf646cc98d9524c6b92bcf3ab56f83","0x9867cc5f7f196b93bae1e27e6320742445d290f2263827498b54fec539f756af","0xcefad4e508c098b9a7e1d8feb19955fb02ba9675585078710969d3440f5054e0","0xf9dc3e7fe016e050eff260334f18a5d4fe391d82092319f5964f2e2eb7c1c3a5","0xf8b13a49e282f609c317a833fb8d976d11517c571d1221a265d25af778ecf892","0x3490c6ceeb450aecdc82e28293031d10c7d73bf85e57bf041a97360aa2c5d99c","0xc1df82d9c4b87413eae2ef048f94b4d3554cea73d92b0f7af96e0271c691e2bb","0x5c67add7c6caf302256adedf7ab114da0acfe870d449a3a489f781d659e8becc","0xda7bce9f4e8618b6bd2f4132ce798cdc7a60e7e1460a7299e3c6342a579626d2","0x2733e50f526ec2fa19a22b31e8ed50f23cd1fdf94c9154ed3a7609a2f1ff981f","0xe1d3b5c807b281e4683cc6d6315cf95b9ade8641defcb32372f1c126e398ef7a","0x5a2dce0a8a7f68bb74560f8f71837c2c2ebbcbf7fffb42ae1896f13f7c7479a0","0xb46a28b6f55540f89444f63de0378e3d121be09e06cc9ded1c20e65876d36aa0","0xc65e9645644786b620e2dd2ad648ddfcbf4a7e5b1a3a4ecfe7f64667a3f0b7e2","0xf4418588ed35a2458cffeb39b93d26f18d2ab13bdce6aee58e7b99359ec2dfd9","0x5a9c16dc00d6ef18b7933a6f8dc65ccb55667138776f7dea101070dc8796e377","0x4df84f40ae0c8229d0d6069e5c8f39a7c299677a09d367fc7b05e3bc380ee652","0xcdc72595f74c7b1043d0e1ffbab734648c838dfb0527d971b602bc216c9619ef","0x0abf5ac974a1ed57f4050aa510dd9c74f508277b39d7973bb2dfccc5eeb0618d","0xb8cd74046ff337f0a7bf2c8e03e10f642c1886798d71806ab1e888d9e5ee87d0","0x838c5655cb21c6cb83313b5a631175dff4963772cce9108188b34ac87c81c41e","0x662ee4dd2dd7b2bc707961b1e646c4047669dcb6584f0d8d770daf5d7e7deb2e","0x388ab20e2573d171a88108e79d820e98f26c0b84aa8b2f4aa4968dbb818ea322","0x93237c50ba75ee485f4c22adf2f741400bdf8d6a9cc7df7ecae576221665d735","0x8448818bb4ae4562849e949e17ac16e0be16688e156b5cf15e098c627c0056a9"]}},"proof_leaf_mer":{"root":"0x663adc4b2dc606cc2893e8a9a95d6b2f7145e2a6af0b43e56562c5eb266d6770","proof":{"siblings":["0x2392eaeec750a4b48db8947746b358d9501a221d656abbd6e03cd908782d20e5","0xab82dd4534cc7d8184880e4eb9bb1554d6560628e431bceeb5ddf93e704e36bc","0xa86b005a4a694582760abcf1cb0fb79bea68bdf23f61b550c800c6e717676abf","0x21ddb9a356815c3fac1026b6dec5df3124afbadb485c9ba5a3e3398a04b7ba85","0xe58769b32a1beaf1ea27375a44095a0d1fb664ce2dd358e7fcbfb78c26a19344","0x0eb01ebfc9ed27500cd4dfc979272d1f0913cc9f66540d7e8005811109e1cf2d","0x887c22bd8750d34016ac3c66b5ff102dacdd73f6b014e710b51e8022af9a1968","0xffd70157e48063fc33c97a050f7f640233bf646cc98d9524c6b92bcf3ab56f83","0x9867cc5f7f196b93bae1e27e6320742445d290f2263827498b54fec539f756af","0xcefad4e508c098b9a7e1d8feb19955fb02ba9675585078710969d3440f5054e0","0xf9dc3e7fe016e050eff260334f18a5d4fe391d82092319f5964f2e2eb7c1c3a5","0xf8b13a49e282f609c317a833fb8d976d11517c571d1221a265d25af778ecf892","0x3490c6ceeb450aecdc82e28293031d10c7d73bf85e57bf041a97360aa2c5d99c","0xc1df82d9c4b87413eae2ef048f94b4d3554cea73d92b0f7af96e0271c691e2bb","0x5c67add7c6caf302256adedf7ab114da0acfe870d449a3a489f781d659e8becc","0xda7bce9f4e8618b6bd2f4132ce798cdc7a60e7e1460a7299e3c6342a579626d2","0x2733e50f526ec2fa19a22b31e8ed50f23cd1fdf94c9154ed3a7609a2f1ff981f","0xe1d3b5c807b281e4683cc6d6315cf95b9ade8641defcb32372f1c126e398ef7a","0x5a2dce0a8a7f68bb74560f8f71837c2c2ebbcbf7fffb42ae1896f13f7c7479a0","0xb46a28b6f55540f89444f63de0378e3d121be09e06cc9ded1c20e65876d36aa0","0xc65e9645644786b620e2dd2ad648ddfcbf4a7e5b1a3a4ecfe7f64667a3f0b7e2","0xf4418588ed35a2458cffeb39b93d26f18d2ab13bdce6aee58e7b99359ec2dfd9","0x5a9c16dc00d6ef18b7933a6f8dc65ccb55667138776f7dea101070dc8796e377","0x4df84f40ae0c8229d0d6069e5c8f39a7c299677a09d367fc7b05e3bc380ee652","0xcdc72595f74c7b1043d0e1ffbab734648c838dfb0527d971b602bc216c9619ef","0x0abf5ac974a1ed57f4050aa510dd9c74f508277b39d7973bb2dfccc5eeb0618d","0xb8cd74046ff337f0a7bf2c8e03e10f642c1886798d71806ab1e888d9e5ee87d0","0x838c5655cb21c6cb83313b5a631175dff4963772cce9108188b34ac87c81c41e","0x662ee4dd2dd7b2bc707961b1e646c4047669dcb6584f0d8d770daf5d7e7deb2e","0x388ab20e2573d171a88108e79d820e98f26c0b84aa8b2f4aa4968dbb818ea322","0x93237c50ba75ee485f4c22adf2f741400bdf8d6a9cc7df7ecae576221665d735","0x8448818bb4ae4562849e949e17ac16e0be16688e156b5cf15e098c627c0056a9"]}}}},"global_index":{"mainnet_flag":true,"rollup_index":0,"leaf_index":3}}],"metadata":"0x0200000000000000b60000003b683d6dfc020000000000000000000000000000","custom_chain_data":"AAEAAQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABJfA8i5Wss88lHvSU0brOK1y8heM93vJx4mNGaQ6OpOwAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAADx","aggchain_data":{"proof":"0000000000000000000000000000000000000000000000000000000000000000000000000143421305623730095048800168872402096980078569670187537634cc321d435ea2e421d87e97032424500e489aa01a61b9bb6da39f75000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000","aggchain_params":"0x5e6a400f254d7616de9de65c3eb214c99e3e2f2ae63e305bd7fb8c505591fc35","context":{"end_block":"AAAAAAAAAPE=","ger/0x0071ea0669728c7b231ab2639af38c149cbf4186b565400cda10bd8d422de812/block_index":"AAAAAAAAAAA=","ger/0x0071ea0669728c7b231ab2639af38c149cbf4186b565400cda10bd8d422de812/block_number":"AAAAAAAAARk=","ger/0x0071ea0669728c7b231ab2639af38c149cbf4186b565400cda10bd8d422de812/l1_leaf_index":"AAAAFg==","ger/0x0e9f71a52e53dd4dfd6fb14b362fa78aaa5a721747cf568ee94265ba299884df/block_index":"AAAAAAAAAAA=","ger/0x0e9f71a52e53dd4dfd6fb14b362fa78aaa5a721747cf568ee94265ba299884df/block_number":"AAAAAAAAAMk=","ger/0x0e9f71a52e53dd4dfd6fb14b362fa78aaa5a721747cf568ee94265ba299884df/l1_leaf_index":"AAAACQ==","ger/0x116a568376414ccc671c9e8a573712015f5013623bdf1cf200363de117843f81/block_index":"AAAAAAAAAAA=","ger/0x116a568376414ccc671c9e8a573712015f5013623bdf1cf200363de117843f81/block_number":"AAAAAAAAAS0=","ger/0x116a568376414ccc671c9e8a573712015f5013623bdf1cf200363de117843f81/l1_leaf_index":"AAAAGg==","ger/0x399ca550640908111bebbbdfb02701ce56d86bd6ef06f854007790fb043bc730/block_index":"AAAAAAAAAAA=","ger/0x399ca550640908111bebbbdfb02701ce56d86bd6ef06f854007790fb043bc730/block_number":"AAAAAAAAANM=","ger/0x399ca550640908111bebbbdfb02701ce56d86bd6ef06f854007790fb043bc730/l1_leaf_index":"AAAACw==","ger/0x47c84fc5bad2d75503a021c29e4c613e515b6e541f9f8a3f0b189b5390e8f502/block_index":"AAAAAAAAAAA=","ger/0x47c84fc5bad2d75503a021c29e4c613e515b6e541f9f8a3f0b189b5390e8f502/block_number":"AAAAAAAAAQU=","ger/0x47c84fc5bad2d75503a021c29e4c613e515b6e541f9f8a3f0b189b5390e8f502/l1_leaf_index":"AAAAEw==","ger/0x50f9e0942ae1d3e5ff39a74017cd0ab577ca11802c01f7b67ff9a23eb6a1b886/block_index":"AAAAAAAAAAA=","ger/0x50f9e0942ae1d3e5ff39a74017cd0ab577ca11802c01f7b67ff9a23eb6a1b886/block_number":"AAAAAAAAASM=","ger/0x50f9e0942ae1d3e5ff39a74017cd0ab577ca11802c01f7b67ff9a23eb6a1b886/l1_leaf_index":"AAAAGA==","ger/0x7ce5463f42e81b705cd61e445557bf0505602d2d6b9fe6fddd6b6d9203690bd2/block_index":"AAAAAAAAAAA=","ger/0x7ce5463f42e81b705cd61e445557bf0505602d2d6b9fe6fddd6b6d9203690bd2/block_number":"AAAAAAAAAL8=","ger/0x7ce5463f42e81b705cd61e445557bf0505602d2d6b9fe6fddd6b6d9203690bd2/l1_leaf_index":"AAAABw==","ger/0x825a4f4e7327c18cd1168f6662e3dd779427695f1657ac0fa061617d24808aad/block_index":"AAAAAAAAAAA=","ger/0x825a4f4e7327c18cd1168f6662e3dd779427695f1657ac0fa061617d24808aad/block_number":"AAAAAAAAAPs=","ger/0x825a4f4e7327c18cd1168f6662e3dd779427695f1657ac0fa061617d24808aad/l1_leaf_index":"AAAAEQ==","ger/0xacfc9654d6e79012c009a1cad7d223c6dccf8c13c0c6bc16009232fd601ad37e/block_index":"AAAAAAAAAAA=","ger/0xacfc9654d6e79012c009a1cad7d223c6dccf8c13c0c6bc16009232fd601ad37e/block_number":"AAAAAAAAATc=","ger/0xacfc9654d6e79012c009a1cad7d223c6dccf8c13c0c6bc16009232fd601ad37e/l1_leaf_index":"AAAAGw==","ger/0xb523f508b42e852eaeb2e0c76393ed0b2ff8dc88a18cad3c5b4ada579d521089/block_index":"AAAAAAAAAAQ=","ger/0xb523f508b42e852eaeb2e0c76393ed0b2ff8dc88a18cad3c5b4ada579d521089/block_number":"AAAAAAAAAUw=","ger/0xb523f508b42e852eaeb2e0c76393ed0b2ff8dc88a18cad3c5b4ada579d521089/l1_leaf_index":"AAAAHw==","ger/0xcf1384fb1505d59306cd93e7bb2c34f2ca4c5f7babddf39bff5d27e54f8a2f69/block_index":"AAAAAAAAAAA=","ger/0xcf1384fb1505d59306cd93e7bb2c34f2ca4c5f7babddf39bff5d27e54f8a2f69/block_number":"AAAAAAAAAN0=","ger/0xcf1384fb1505d59306cd93e7bb2c34f2ca4c5f7babddf39bff5d27e54f8a2f69/l1_leaf_index":"AAAADA==","ger/0xd500e2da0e7dfa364e6dc20b1a72cdd1f803ca9978312e81039877b0f38a02e2/block_index":"AAAAAAAAAAA=","ger/0xd500e2da0e7dfa364e6dc20b1a72cdd1f803ca9978312e81039877b0f38a02e2/block_number":"AAAAAAAAAOc=","ger/0xd500e2da0e7dfa364e6dc20b1a72cdd1f803ca9978312e81039877b0f38a02e2/l1_leaf_index":"AAAADg==","ger/0xdfb30f4b26cb1b5e25e61b9b29be6dac9de02e47c7815f3139f4842d51282198/block_index":"AAAAAAAAAAA=","ger/0xdfb30f4b26cb1b5e25e61b9b29be6dac9de02e47c7815f3139f4842d51282198/block_number":"AAAAAAAAAQ8=","ger/0xdfb30f4b26cb1b5e25e61b9b29be6dac9de02e47c7815f3139f4842d51282198/l1_leaf_index":"AAAAFQ==","ger/0xf13446585cbecaaa4abda7c87e1bc7c16ee0d33a3c6c2d9f31bd09cdaad5902d/block_index":"AAAAAAAAAAA=","ger/0xf13446585cbecaaa4abda7c87e1bc7c16ee0d33a3c6c2d9f31bd09cdaad5902d/block_number":"AAAAAAAAAUE=","ger/0xf13446585cbecaaa4abda7c87e1bc7c16ee0d33a3c6c2d9f31bd09cdaad5902d/l1_leaf_index":"AAAAHQ==","ger/0xfe2b9de0d0a75b4e6535393fb3c8041f6996b9051f91727e488eff35c1050404/block_index":"AAAAAAAAAAA=","ger/0xfe2b9de0d0a75b4e6535393fb3c8041f6996b9051f91727e488eff35c1050404/block_number":"AAAAAAAAAPE=","ger/0xfe2b9de0d0a75b4e6535393fb3c8041f6996b9051f91727e488eff35c1050404/l1_leaf_index":"AAAAEA==","ibe/0/block_number":"AAAAAAAAALY=","ibe/0/bridge_exit_hash":"Q8PWKklBcX5KQffELacel8BTy+sZw8X4K0wgoWecaas=","ibe/0/global_index":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAM=","ibe/1/block_number":"AAAAAAAAALY=","ibe/1/bridge_exit_hash":"ixH5bLOuYiDjh7bBvP/sQmGHWanYE7Pnz69mfAtF8Lg=","ibe/1/global_index":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAQ=","ibe/10/block_number":"AAAAAAAAAPI=","ibe/10/bridge_exit_hash":"XmTjKsz0ZKHNR/Ui+RbEg9g+oUZOSVV/xIB3NiqpSaM=","ibe/10/global_index":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAAA0=","ibe/11/block_number":"AAAAAAAAAPI=","ibe/11/bridge_exit_hash":"jA8p/Fw994XbLlLsrnKbN3I+B7tJEJ45dWQFppCv7+w=","ibe/11/global_index":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAAA4=","ibe/12/block_number":"AAAAAAAAAPw=","ibe/12/bridge_exit_hash":"jToXJQyBSMpkcmB+bul6aPMkoPt34Iba7waeWFEnd8k=","ibe/12/global_index":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAAA8=","ibe/13/block_number":"AAAAAAAAAPw=","ibe/13/bridge_exit_hash":"kTyq6Nndo91CBhbZQhDO/R3lZKhsSlwZhRqXiO6Sfko=","ibe/13/global_index":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAABA=","ibe/14/block_number":"AAAAAAAAAQY=","ibe/14/bridge_exit_hash":"irjq95L/h5iyShSGluTihVqy1uH5d4A4fOuVdPBSUQI=","ibe/14/global_index":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAABE=","ibe/15/block_number":"AAAAAAAAARA=","ibe/15/bridge_exit_hash":"CX2G81Tqj6mbkbzJERAqg8ZLKidadnjLlv3js98AR+E=","ibe/15/global_index":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAABI=","ibe/16/block_number":"AAAAAAAAARA=","ibe/16/bridge_exit_hash":"HcoL9T8eKxzvibvLrLSzGsy9cWxN43/IBtrXbGB2iow=","ibe/16/global_index":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAABM=","ibe/17/block_number":"AAAAAAAAARo=","ibe/17/bridge_exit_hash":"y7JcMymKSCNEoGxw3bWJjJdDk4nVrWO54Or9GmOKzr4=","ibe/17/global_index":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAABQ=","ibe/18/block_number":"AAAAAAAAARo=","ibe/18/bridge_exit_hash":"86+ez41di/Dlj0pmshiK1cVaUs43gG4HeOAjanoI/7U=","ibe/18/global_index":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAABU=","ibe/19/block_number":"AAAAAAAAASQ=","ibe/19/bridge_exit_hash":"o0AOYtdHRcARgq6y6zm0pC67i0e3uOgZpNMLK+gY5ro=","ibe/19/global_index":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAABY=","ibe/2/block_number":"AAAAAAAAAMA=","ibe/2/bridge_exit_hash":"rVB+K6UxmozIFEy30UxsdWBDsAfKHyJqijUWYBcZC2c=","ibe/2/global_index":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAU=","ibe/20/block_number":"AAAAAAAAAS4=","ibe/20/bridge_exit_hash":"aff/M1g8wzAQ2DHSEHo/1vz9nTcC0+tnv4t5/eb4oxo=","ibe/20/global_index":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAABc=","ibe/21/block_number":"AAAAAAAAAS4=","ibe/21/bridge_exit_hash":"6H0wTJUhspkC1+iQaJqWfLk2kFg+wUYCfJwYzXz7y3Y=","ibe/21/global_index":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAABg=","ibe/22/block_number":"AAAAAAAAATg=","ibe/22/bridge_exit_hash":"qq8OigXrSigfF7XhDB8C+4n0LipVg5C5u90ukcQkucc=","ibe/22/global_index":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAABk=","ibe/23/block_number":"AAAAAAAAATg=","ibe/23/bridge_exit_hash":"BsGgBKOyfVW8+SgwSG+lAnHNsHSUjGJhaYy9ap/ZyxM=","ibe/23/global_index":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAABo=","ibe/24/block_number":"AAAAAAAAAUI=","ibe/24/bridge_exit_hash":"lMiAJKEmPX++9EYXdXSnrL8f3z9N3UmQcYDIQNxFjNQ=","ibe/24/global_index":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAABs=","ibe/25/block_number":"AAAAAAAAAUw=","ibe/25/bridge_exit_hash":"wByWcYBbyAIR4akpHOB4JT5CN2nn2FYde+VTYkk975Y=","ibe/25/global_index":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAABw=","ibe/26/block_number":"AAAAAAAAAUw=","ibe/26/bridge_exit_hash":"5aUTYOPi3Y204mQ1QrMbeutIbF/2zbvbSBwf92E1HDg=","ibe/26/global_index":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAAB0=","ibe/3/block_number":"AAAAAAAAAMA=","ibe/3/bridge_exit_hash":"hvTuHASzq3qcOyoaWwR9M8UkEiXsNTTmdod42q5C1lk=","ibe/3/global_index":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAY=","ibe/4/block_number":"AAAAAAAAAMo=","ibe/4/bridge_exit_hash":"2XfLgtiZ4CyNtVieGOBhAdMwiS0H1ZPkBHy2zwApl5E=","ibe/4/global_index":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAc=","ibe/5/block_number":"AAAAAAAAANQ=","ibe/5/bridge_exit_hash":"oCk/0A1HFqIm+s7P4dy1TskdvBWUBcrkcHLx3dYUdoA=","ibe/5/global_index":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAg=","ibe/6/block_number":"AAAAAAAAANQ=","ibe/6/bridge_exit_hash":"RGhC9UGPShO72gMBTP6fHeaRX0usR8YYpbT7OFLTY+g=","ibe/6/global_index":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAk=","ibe/7/block_number":"AAAAAAAAAN4=","ibe/7/bridge_exit_hash":"hOMHhHIiyDKX9itnHR7xsyOzB1L2BqsbPCOG9ZnB46k=","ibe/7/global_index":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAo=","ibe/8/block_number":"AAAAAAAAAN4=","ibe/8/bridge_exit_hash":"PpkZfte2s5gV6p9h6XlpubGKvV5hejFxIoLwPPwu7xo=","ibe/8/global_index":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAs=","ibe/9/block_number":"AAAAAAAAAOg=","ibe/9/bridge_exit_hash":"nCREJUfqdEoH4QSblWJ0bWWTMK2KBq4wupY09r2Phso=","ibe/9/global_index":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAw=","l1_info_tree_block_hash":"XjdLsV5LxR7ZRpSSgGLf8s99FuFn7D5lA4NfPDVQclc=","l1_info_tree_ger":"WG5Hbf5i74Ckh4BGtFxz7idCis6J5Ch44a85wySHz1Y=","l1_info_tree_index":"AAAAWQ==","l1_info_tree_mer":"3/TKefUqOq1G7C9hEDJsSIRQxZ7ZvpdHZBdunuxI+LU=","l1_info_tree_rer":"PbdYN9+5viwU0eKsQ8FHJ/0DYnk54RcliZJDrfvONoE=","l1_info_tree_root_hash":"dCh90YfvpODNHhBhxXoOGiDz8MNyCWtI5HGWJ+ItPgU=","l1_info_tree_timestamp":"AAAAAGg9bfw=","last_proven_block":"AAAAAAAAALU=","local_exit_root_hash":"pTgxIvPpj2Sg+PnlqLGhA98kFhAad41w1GTU8OGqqBc=","public_values":"ag7Hr4UeVvSGMfD8ty/GLVLSQdxT/cH6S0ynaCnI1wClODEi8+mPZKD4+eWosaED3yQWEBp3jXDUZNTw4aqoF3QofdGH76TgzR4QYcV6Dhog8/DDcglrSORxlifiLT4FAQAAANTSWEZEMBPvveUenkNXTahDRKIUWVE1TAtGeN48kRIQXmpADyVNdhbeneZcPrIUyZ4+LyrmPjBb1/uMUFWR/DU=","requested_end_block":"AAAAAAAAAVU="},"version":"v4.0.0-rc.3","vkey":"2801fe7e695256da62c97bc91118a67440c1ed5d07b5a31a630da0db11b7c7e10022b0fc4b1598c936f7d0723bab9ffb1f29a5923f90029a270875463b3038f2324a03f16f56b9aa4d4611ab5fb4fa1755e69c502f7d90c04fc34b340000000000000002000000000000000750726f6772616d000000000000001400000001000000000000000e0000000000100000000000000000000442797465000000000000001000000001000000000000000b00000000000100000000000000000002000000000000000750726f6772616d00000000000000000000000000000004427974650000000000000001","signature":"9ba1cae4266ef1640408666b9366a27a93ade3e28e754ab431d285ee56cac76713bdb1f4c07a6849dcce2c8f6a10b6ef3d3844297b383d90696caed129af124c00"},"l1_info_tree_leaf_count":90}`

	var certificate types.Certificate
	require.NoError(t, json.Unmarshal([]byte(certJSON), &certificate))

	// estimate agchain proof data size
	aggchainProofData, err := convertAggchainData(certificate.AggchainData)
	require.NoError(t, err)
	size := estimateRequestSize(t, aggchainProofData)
	fmt.Printf("aggchain proof data size in bytes: %d\n", size)

	// estimate bridge exit size
	bridgeExit := convertToProtoBridgeExit(certificate.BridgeExits[0])
	size = estimateRequestSize(t, bridgeExit)
	fmt.Printf("bridge exit size in bytes: %d\n", size)

	importedBridgeExit, err := convertToProtoImportedBridgeExit(certificate.ImportedBridgeExits[0])
	require.NoError(t, err)
	size = estimateRequestSize(t, importedBridgeExit)
	fmt.Printf("imported bridge exit size in bytes: %d\n", size)

	// estimate aggchain data signature size
	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	sig, err := crypto.Sign(common.BytesToHash([]byte("test")).Bytes(), key)
	require.NoError(t, err)

	aggchainSigData := &v1types.AggchainData{
		Data: &v1types.AggchainData_Signature{
			Signature: &v1types.FixedBytes65{Value: sig},
		},
	}
	size = estimateRequestSize(t, aggchainSigData)
	fmt.Printf("aggchain data signature size: %d bytes\n", size)
}

func estimateRequestSize(t *testing.T, req proto.Message) int {
	t.Helper()

	data, err := proto.Marshal(req)
	require.NoError(t, err)

	return len(data)
}
