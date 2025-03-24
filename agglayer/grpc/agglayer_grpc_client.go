package grpc

import (
	"context"
	"errors"
	"strings"

	node "buf.build/gen/go/agglayer/agglayer/grpc/go/agglayer/node/v1/nodev1grpc"
	v1 "buf.build/gen/go/agglayer/agglayer/protocolbuffers/go/agglayer/node/v1"
	v1Types "buf.build/gen/go/agglayer/agglayer/protocolbuffers/go/agglayer/protocol/types/v1"
	"github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/bridgesync"
	aggkitCommon "github.com/agglayer/aggkit/common"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

var (
	errUndefinedAggchainData = errors.New("undefined aggchain data parameter")
	errUnknownAggchainData   = errors.New("unknown aggchain data type")
)

type AgglayerGRPCClient struct {
	networkStateService node.NodeStateServiceClient
	cfgService          node.ConfigurationServiceClient
	submissionService   node.CertificateSubmissionServiceClient
}

// NewAggchainProofClient initializes a new AggchainProof instance
func NewAgglayerGRPCClient(serverAddr string) (*AgglayerGRPCClient, error) {
	// trim the http:// prefix if it exists in the URL because the go-grpc client expects it without it
	addr := strings.TrimPrefix(serverAddr, "http://")

	grpcClient, err := aggkitCommon.NewClient(addr)
	if err != nil {
		return nil, err
	}

	return &AgglayerGRPCClient{
		networkStateService: node.NewNodeStateServiceClient(grpcClient.Conn()),
		cfgService:          node.NewConfigurationServiceClient(grpcClient.Conn()),
		submissionService:   node.NewCertificateSubmissionServiceClient(grpcClient.Conn()),
	}, nil
}

// GetEpochConfiguration returns the epoch configuration from the AggLayer
func (a *AgglayerGRPCClient) GetEpochConfiguration(ctx context.Context) (*types.ClockConfiguration, error) {
	response, err := a.cfgService.GetEpochConfiguration(ctx, &v1.GetEpochConfigurationRequest{})
	if err != nil {
		return nil, err
	}

	return &types.ClockConfiguration{
		EpochDuration: response.EpochConfiguration.EpochDuration,
		GenesisBlock:  response.EpochConfiguration.GenesisBlock,
	}, nil
}

// SendCertificate sends a certificate to the AggLayer
// It returns the certificate ID
func (a *AgglayerGRPCClient) SendCertificate(ctx context.Context,
	certificate *types.Certificate) (common.Hash, error) {
	if certificate.AggchainData == nil {
		return common.Hash{}, errUndefinedAggchainData
	}

	var aggchainDataProto *v1Types.AggchainData

	switch ad := certificate.AggchainData.(type) {
	case *types.AggchainDataProof:
		aggchainDataProto = &v1Types.AggchainData{
			Data: &v1Types.AggchainData_Generic{
				Generic: &v1Types.AggchainProof{
					Proof: &v1Types.AggchainProof_Sp1Stark{
						Sp1Stark: ad.Proof,
					},
					AggchainParams: &v1Types.FixedBytes32{
						Value: ad.AggchainParams.Bytes(),
					},
					Context: ad.Context,
				},
			},
		}
	case *types.AggchainDataSignature:
		aggchainDataProto = &v1Types.AggchainData{
			Data: &v1Types.AggchainData_Signature{
				Signature: &v1Types.FixedBytes65{
					Value: ad.Signature,
				},
			},
		}
	default:
		return common.Hash{}, errUnknownAggchainData
	}

	protoCert := &v1Types.Certificate{
		NetworkId: certificate.NetworkID,
		Height:    certificate.Height,
		PrevLocalExitRoot: &v1Types.FixedBytes32{
			Value: certificate.PrevLocalExitRoot.Bytes(),
		},
		NewLocalExitRoot: &v1Types.FixedBytes32{
			Value: certificate.NewLocalExitRoot.Bytes(),
		},
		Metadata: &v1Types.FixedBytes32{
			Value: certificate.Metadata.Bytes(),
		},
		CustomChainData:     certificate.CustomChainData,
		AggchainData:        aggchainDataProto,
		BridgeExits:         make([]*v1Types.BridgeExit, 0, len(certificate.BridgeExits)),
		ImportedBridgeExits: make([]*v1Types.ImportedBridgeExit, 0, len(certificate.ImportedBridgeExits)),
	}

	for _, be := range certificate.BridgeExits {
		protoCert.BridgeExits = append(protoCert.BridgeExits, convertToProtoBridgeExit(be))
	}

	for _, ibe := range certificate.ImportedBridgeExits {
		protoImportedBridgeExit, err := convertToProtoImportedBridgeExit(ibe)
		if err != nil {
			return common.Hash{}, err
		}

		protoCert.ImportedBridgeExits = append(protoCert.ImportedBridgeExits, protoImportedBridgeExit)
	}

	response, err := a.submissionService.SubmitCertificate(ctx, &v1.SubmitCertificateRequest{
		Certificate: protoCert,
	})

	if err != nil {
		return common.Hash{}, err
	}

	return common.BytesToHash(response.CertificateId.Value.Value), nil
}

// GetLatestPendingCertificateHeader returns the latest pending certificate header from the AggLayer
func (a *AgglayerGRPCClient) GetLatestSettledCertificateHeader(
	ctx context.Context, networkID uint32) (*types.CertificateHeader, error) {
	response, err := a.networkStateService.GetLatestCertificateHeader(
		ctx,
		&v1.GetLatestCertificateHeaderRequest{
			NetworkId: networkID,
			Type:      v1.LatestCertificateRequestType_LATEST_CERTIFICATE_REQUEST_TYPE_SETTLED,
		},
	)
	if err != nil {
		return nil, err
	}

	return convertProtoCertificateHeader(response.CertificateHeader), nil
}

// GetLatestPendingCertificateHeader returns the latest pending certificate header from the AggLayer
func (a *AgglayerGRPCClient) GetLatestPendingCertificateHeader(
	ctx context.Context, networkID uint32) (*types.CertificateHeader, error) {
	response, err := a.networkStateService.GetLatestCertificateHeader(
		ctx,
		&v1.GetLatestCertificateHeaderRequest{
			NetworkId: networkID,
			Type:      v1.LatestCertificateRequestType_LATEST_CERTIFICATE_REQUEST_TYPE_PENDING,
		},
	)
	if err != nil {
		return nil, err
	}

	return convertProtoCertificateHeader(response.CertificateHeader), nil
}

// GetCertificateHeader returns the certificate header from the AggLayer for the given certificate ID
func (a *AgglayerGRPCClient) GetCertificateHeader(ctx context.Context,
	certificateID common.Hash) (*types.CertificateHeader, error) {
	response, err := a.networkStateService.GetCertificateHeader(ctx,
		&v1.GetCertificateHeaderRequest{CertificateId: &v1Types.CertificateId{
			Value: &v1Types.FixedBytes32{
				Value: certificateID.Bytes(),
			},
		},
		})
	if err != nil {
		return nil, err
	}

	return convertProtoCertificateHeader(response.CertificateHeader), nil
}

// convertProtoCertificateHeader converts a proto certificate header to a types certificate header
func convertProtoCertificateHeader(response *v1Types.CertificateHeader) *types.CertificateHeader {
	if response == nil {
		return nil
	}

	header := &types.CertificateHeader{
		NetworkID:             response.NetworkId,
		Height:                response.Height,
		EpochNumber:           response.EpochNumber,
		CertificateIndex:      response.CertificateIndex,
		CertificateID:         common.BytesToHash(response.CertificateId.Value.Value),
		PreviousLocalExitRoot: nullableBytesToHash(response.PrevLocalExitRoot),
		NewLocalExitRoot:      common.BytesToHash(response.NewLocalExitRoot.Value),
		Status:                certificateStatusFromProto(response.Status),
		Metadata:              common.BytesToHash(response.Metadata.Value),
		SettlementTxHash:      nullableBytesToHash(response.SettlementTxHash),
	}

	if response.Error != nil && response.Error.Message != nil {
		header.Error = errors.New(string(response.Error.Message))
	}

	return header
}

// convertToProtoBridgeExit converts a bridge exit to a proto bridge exit
func convertToProtoBridgeExit(be *types.BridgeExit) *v1Types.BridgeExit {
	if be == nil {
		return nil
	}

	protoBridgeExit := &v1Types.BridgeExit{
		LeafType:    leafTypeToProto(be.LeafType),
		DestNetwork: be.DestinationNetwork,
		DestAddress: &v1Types.FixedBytes20{
			Value: be.DestinationAddress.Bytes(),
		},
		Amount: &v1Types.FixedBytes32{
			Value: common.BigToHash(be.Amount).Bytes(),
		},
		TokenInfo: &v1Types.TokenInfo{
			OriginNetwork: be.TokenInfo.OriginNetwork,
			OriginTokenAddress: &v1Types.FixedBytes20{
				Value: be.TokenInfo.OriginTokenAddress.Bytes(),
			},
		},
	}

	if len(be.Metadata) > 0 {
		protoBridgeExit.Metadata = &v1Types.FixedBytes32{
			Value: common.BytesToHash(be.Metadata).Bytes(),
		}
	}

	return protoBridgeExit
}

func convertToProtoImportedBridgeExit(ibe *types.ImportedBridgeExit) (*v1Types.ImportedBridgeExit, error) {
	if ibe == nil {
		return nil, nil
	}

	importedBridgeExit := &v1Types.ImportedBridgeExit{
		BridgeExit: convertToProtoBridgeExit(ibe.BridgeExit),
		GlobalIndex: &v1Types.FixedBytes32{
			Value: common.BigToHash(bridgesync.GenerateGlobalIndex(
				ibe.GlobalIndex.MainnetFlag,
				ibe.GlobalIndex.RollupIndex,
				ibe.GlobalIndex.LeafIndex)).Bytes(),
		},
	}

	switch claimData := ibe.ClaimData.(type) {
	case *types.ClaimFromMainnnet:
		importedBridgeExit.Claim = &v1Types.ImportedBridgeExit_Mainnet{
			Mainnet: &v1Types.ClaimFromMainnet{
				ProofLeafMer: &v1Types.MerkleProof{
					Root: &v1Types.FixedBytes32{
						Value: claimData.ProofLeafMER.Root.Bytes(),
					},
					Siblings: convertToProtoSiblings(claimData.ProofLeafMER.Proof),
				},
				ProofGerL1Root: &v1Types.MerkleProof{
					Root: &v1Types.FixedBytes32{
						Value: claimData.ProofGERToL1Root.Root.Bytes(),
					},
					Siblings: convertToProtoSiblings(claimData.ProofGERToL1Root.Proof),
				},
				L1Leaf: &v1Types.L1InfoTreeLeafWithContext{
					L1InfoTreeIndex: claimData.L1Leaf.L1InfoTreeIndex,
					Rer: &v1Types.FixedBytes32{
						Value: claimData.L1Leaf.RollupExitRoot.Bytes(),
					},
					Mer: &v1Types.FixedBytes32{
						Value: claimData.L1Leaf.MainnetExitRoot.Bytes(),
					},
					Inner: &v1Types.L1InfoTreeLeaf{
						GlobalExitRoot: &v1Types.FixedBytes32{
							Value: claimData.L1Leaf.Inner.GlobalExitRoot.Bytes(),
						},
						BlockHash: &v1Types.FixedBytes32{
							Value: claimData.L1Leaf.Inner.BlockHash.Bytes(),
						},
						Timestamp: claimData.L1Leaf.Inner.Timestamp,
					},
				},
			},
		}
	case *types.ClaimFromRollup:
		importedBridgeExit.Claim = &v1Types.ImportedBridgeExit_Rollup{
			Rollup: &v1Types.ClaimFromRollup{
				ProofLeafLer: &v1Types.MerkleProof{
					Root: &v1Types.FixedBytes32{
						Value: claimData.ProofLeafLER.Root.Bytes(),
					},
					Siblings: convertToProtoSiblings(claimData.ProofLeafLER.Proof),
				},
				ProofLerRer: &v1Types.MerkleProof{
					Root: &v1Types.FixedBytes32{
						Value: claimData.ProofLERToRER.Root.Bytes(),
					},
					Siblings: convertToProtoSiblings(claimData.ProofLERToRER.Proof),
				},
				ProofGerL1Root: &v1Types.MerkleProof{
					Root: &v1Types.FixedBytes32{
						Value: claimData.ProofGERToL1Root.Root.Bytes(),
					},
					Siblings: convertToProtoSiblings(claimData.ProofGERToL1Root.Proof),
				},
				L1Leaf: &v1Types.L1InfoTreeLeafWithContext{
					L1InfoTreeIndex: claimData.L1Leaf.L1InfoTreeIndex,
					Rer: &v1Types.FixedBytes32{
						Value: claimData.L1Leaf.RollupExitRoot.Bytes(),
					},
					Mer: &v1Types.FixedBytes32{
						Value: claimData.L1Leaf.MainnetExitRoot.Bytes(),
					},
					Inner: &v1Types.L1InfoTreeLeaf{
						GlobalExitRoot: &v1Types.FixedBytes32{
							Value: claimData.L1Leaf.Inner.GlobalExitRoot.Bytes(),
						},
						BlockHash: &v1Types.FixedBytes32{
							Value: claimData.L1Leaf.Inner.BlockHash.Bytes(),
						},
						Timestamp: claimData.L1Leaf.Inner.Timestamp,
					},
				},
			},
		}
	default:
		return nil, errors.New("invalid claim type")
	}

	return importedBridgeExit, nil
}

// convertToProtoSiblings converts a slice of hashes to a slice of proto fixed bytes 32
func convertToProtoSiblings(siblings treetypes.Proof) []*v1Types.FixedBytes32 {
	protoSiblings := make([]*v1Types.FixedBytes32, len(siblings))

	for i, sibling := range siblings {
		protoSiblings[i] = &v1Types.FixedBytes32{
			Value: sibling.Bytes(),
		}
	}

	return protoSiblings
}

// nullableBytesToHash converts a nullable byte slice to a hash pointer
func nullableBytesToHash(b *v1Types.FixedBytes32) *common.Hash {
	if b == nil || len(b.Value) == 0 {
		return nil
	}

	hash := common.BytesToHash(b.Value)
	return &hash
}

// leafTypeToProto converts a leaf type to a proto leaf type
func leafTypeToProto(leafType types.LeafType) v1Types.LeafType {
	switch leafType {
	case types.LeafTypeAsset:
		return v1Types.LeafType_LEAF_TYPE_TRANSFER
	case types.LeafTypeMessage:
		return v1Types.LeafType_LEAF_TYPE_MESSAGE
	default:
		return v1Types.LeafType_LEAF_TYPE_UNSPECIFIED
	}
}

// certificateStatusFromProto converts a proto certificate status to a certificate status
func certificateStatusFromProto(status v1Types.CertificateStatus) types.CertificateStatus {
	switch status {
	case v1Types.CertificateStatus_CERTIFICATE_STATUS_PENDING:
		return types.Pending
	case v1Types.CertificateStatus_CERTIFICATE_STATUS_PROVEN:
		return types.Proven
	case v1Types.CertificateStatus_CERTIFICATE_STATUS_CANDIDATE:
		return types.Candidate
	case v1Types.CertificateStatus_CERTIFICATE_STATUS_IN_ERROR:
		return types.InError
	case v1Types.CertificateStatus_CERTIFICATE_STATUS_SETTLED:
		return types.Settled
	default:
		return types.Pending
	}
}
