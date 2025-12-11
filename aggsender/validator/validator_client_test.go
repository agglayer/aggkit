package validator

import (
	"context"
	"errors"
	"math/big"
	"testing"

	typesv1 "buf.build/gen/go/agglayer/interop/protocolbuffers/go/agglayer/interop/types/v1"
	agglayergrpc "github.com/agglayer/aggkit/agglayer/grpc"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	v1 "github.com/agglayer/aggkit/aggsender/validator/proto/v1"
	bridgetypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestClient_ValidateCertificate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	prevCertHash := common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")
	certificate := &agglayertypes.Certificate{
		NetworkID:         12,
		Height:            11,
		PrevLocalExitRoot: common.HexToHash("0x1"),
		NewLocalExitRoot:  common.HexToHash("0x2"),
		BridgeExits: []*agglayertypes.BridgeExit{
			{
				LeafType: bridgetypes.LeafTypeAsset,
				TokenInfo: &agglayertypes.TokenInfo{
					OriginTokenAddress: common.HexToAddress("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890"),
					OriginNetwork:      12,
				},
				DestinationNetwork: 1,
				DestinationAddress: common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdef"),
				Amount:             big.NewInt(1000),
				Metadata:           []byte("bridge-exit-metadata"),
			},
		},
		ImportedBridgeExits: []*agglayertypes.ImportedBridgeExit{
			{
				GlobalIndex: &agglayertypes.GlobalIndex{
					MainnetFlag: false,
					RollupIndex: 111,
					LeafIndex:   11,
				},
				BridgeExit: &agglayertypes.BridgeExit{
					LeafType: bridgetypes.LeafTypeAsset,
					TokenInfo: &agglayertypes.TokenInfo{
						OriginNetwork:      1,
						OriginTokenAddress: common.HexToAddress("0x1211"),
					},
					DestinationNetwork: 12,
					DestinationAddress: common.HexToAddress("0x2232"),
					Amount:             big.NewInt(100),
					Metadata:           []byte("metadata-1"),
				},
				ClaimData: &agglayertypes.ClaimFromMainnet{
					ProofLeafMER: &agglayertypes.MerkleProof{
						Root:  common.HexToHash("0x1"),
						Proof: [common.HashLength]common.Hash{},
					},
					ProofGERToL1Root: &agglayertypes.MerkleProof{
						Root:  common.HexToHash("0x3"),
						Proof: [common.HashLength]common.Hash{},
					},
					L1Leaf: &agglayertypes.L1InfoTreeLeaf{
						L1InfoTreeIndex: 1,
						RollupExitRoot:  common.HexToHash("0x4"),
						MainnetExitRoot: common.HexToHash("0x5"),
						Inner: &agglayertypes.L1InfoTreeLeafInner{
							GlobalExitRoot: common.HexToHash("0x6"),
							BlockHash:      common.HexToHash("0x7"),
							Timestamp:      12321,
						},
					},
				},
			},
		},
		CustomChainData:     []byte("custom-data"),
		L1InfoTreeLeafCount: 100,
		AggchainData: &agglayertypes.AggchainDataSignature{
			Signature: []byte("valid-signature"),
		},
	}

	testCases := []struct {
		name                  string
		previousCertificateID *common.Hash
		certificate           *agglayertypes.Certificate
		mockFn                func(*mocks.AggsenderValidatorClient)
		expectedError         string
	}{
		{
			name:                  "Invalid certificate - nil certificate",
			previousCertificateID: nil,
			certificate:           nil,
			mockFn:                nil,
			expectedError:         "nil certificate provided for conversion to proto",
		},
		{
			name:                  "client returns an error",
			previousCertificateID: &prevCertHash,
			certificate:           certificate,
			mockFn: func(mockClient *mocks.AggsenderValidatorClient) {
				protoCert, err := agglayergrpc.ConvertCertToProtoCertificate(certificate)
				require.NoError(t, err)

				mockClient.EXPECT().ValidateCertificate(ctx, &v1.ValidateCertificateRequest{
					PreviousCertificateId: certIDToProtoNullable(&prevCertHash),
					Certificate:           protoCert,
				}).Return(nil, errors.New("some error"))
			},
			expectedError: "aggsender validator failed to successfully validate certificate: some error",
		},
		{
			name:                  "Valid certificate - no previous certificate",
			previousCertificateID: nil,
			certificate:           certificate,
			mockFn: func(mockClient *mocks.AggsenderValidatorClient) {
				protoCert, err := agglayergrpc.ConvertCertToProtoCertificate(certificate)
				require.NoError(t, err)

				mockClient.EXPECT().ValidateCertificate(ctx, &v1.ValidateCertificateRequest{
					PreviousCertificateId: nil,
					Certificate:           protoCert,
				}).Return(&v1.ValidateCertificateResponse{
					Signature: &typesv1.FixedBytes65{Value: []byte("valid-signature")},
				}, nil)
			},
			expectedError: "",
		},
		{
			name:                  "Valid certificate - with previous certificate",
			previousCertificateID: &prevCertHash,
			certificate:           certificate,
			mockFn: func(mockClient *mocks.AggsenderValidatorClient) {
				protoCert, err := agglayergrpc.ConvertCertToProtoCertificate(certificate)
				require.NoError(t, err)

				mockClient.EXPECT().ValidateCertificate(ctx, &v1.ValidateCertificateRequest{
					PreviousCertificateId: certIDToProtoNullable(&prevCertHash),
					Certificate:           protoCert,
				}).Return(&v1.ValidateCertificateResponse{
					Signature: &typesv1.FixedBytes65{Value: []byte("valid-signature")},
				}, nil)
			},
			expectedError: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockClient := mocks.NewAggsenderValidatorClient(t)
			if tc.mockFn != nil {
				tc.mockFn(mockClient)
			}

			validatorClient := &ValidatorClient{
				client: mockClient,
			}

			signature, err := validatorClient.ValidateCertificate(ctx, tc.previousCertificateID, tc.certificate, 0)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.NotNil(t, signature)
			}

			mockClient.AssertExpectations(t)
		})
	}
}
