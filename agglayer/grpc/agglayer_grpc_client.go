package grpc

import (
	"context"
	"errors"
	"fmt"

	node "buf.build/gen/go/agglayer/agglayer/grpc/go/agglayer/node/v1/nodev1grpc"
	v1nodetypes "buf.build/gen/go/agglayer/agglayer/protocolbuffers/go/agglayer/node/types/v1"
	v1 "buf.build/gen/go/agglayer/agglayer/protocolbuffers/go/agglayer/node/v1"
	v1types "buf.build/gen/go/agglayer/interop/protocolbuffers/go/agglayer/interop/types/v1"
	"github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/bridgesync"
	aggkitgrpc "github.com/agglayer/aggkit/grpc"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

var (
	errUndefinedAggchainData = errors.New("undefined aggchain data parameter")
	errUnknownAggchainData   = errors.New("unknown aggchain data type")
)

type AgglayerGRPCClient struct {
	cfg                 *aggkitgrpc.ClientConfig
	networkStateService node.NodeStateServiceClient
	cfgService          node.ConfigurationServiceClient
	submissionService   node.CertificateSubmissionServiceClient
}

// NewAggchainProofClient initializes a new AggchainProof instance
func NewAgglayerGRPCClient(cfg *aggkitgrpc.ClientConfig) (*AgglayerGRPCClient, error) {
	grpcClient, err := aggkitgrpc.NewClient(cfg)
	if err != nil {
		return nil, err
	}

	return &AgglayerGRPCClient{
		cfg:                 cfg,
		networkStateService: node.NewNodeStateServiceClient(grpcClient.Conn()),
		cfgService:          node.NewConfigurationServiceClient(grpcClient.Conn()),
		submissionService:   node.NewCertificateSubmissionServiceClient(grpcClient.Conn()),
	}, nil
}

// GetEpochConfiguration returns the epoch configuration from the AggLayer
func (a *AgglayerGRPCClient) GetEpochConfiguration(ctx context.Context) (*types.ClockConfiguration, error) {
	ctx, cancel := context.WithTimeout(ctx, a.cfg.RequestTimeout.Duration)
	defer cancel()

	response, err := a.cfgService.GetEpochConfiguration(ctx, &v1.GetEpochConfigurationRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to get epoch configuration: %w", aggkitgrpc.RepackGRPCErrorWithDetails(err))
	}

	return &types.ClockConfiguration{
		GenesisBlock:  response.EpochConfiguration.GenesisBlock,
		EpochDuration: response.EpochConfiguration.EpochDuration,
	}, nil
}

// SendCertificate sends a certificate to the AggLayer
// It returns the certificate ID
func (a *AgglayerGRPCClient) SendCertificate(ctx context.Context,
	certificate *types.Certificate) (common.Hash, error) {
	aggchainDataProto, err := convertAggchainData(certificate.AggchainData)
	if err != nil {
		return common.Hash{}, err
	}

	protoCert := &v1nodetypes.Certificate{
		NetworkId:           certificate.NetworkID,
		Height:              certificate.Height,
		L1InfoTreeLeafCount: &certificate.L1InfoTreeLeafCount,
		PrevLocalExitRoot: &v1types.FixedBytes32{
			Value: certificate.PrevLocalExitRoot.Bytes(),
		},
		NewLocalExitRoot: &v1types.FixedBytes32{
			Value: certificate.NewLocalExitRoot.Bytes(),
		},
		Metadata: &v1types.FixedBytes32{
			Value: certificate.Metadata.Bytes(),
		},
		CustomChainData:     certificate.CustomChainData,
		AggchainData:        aggchainDataProto,
		BridgeExits:         make([]*v1types.BridgeExit, 0, len(certificate.BridgeExits)),
		ImportedBridgeExits: make([]*v1types.ImportedBridgeExit, 0, len(certificate.ImportedBridgeExits)),
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

	ctx, cancel := context.WithTimeout(ctx, a.cfg.RequestTimeout.Duration)
	defer cancel()

	response, err := a.submissionService.SubmitCertificate(ctx,
		&v1.SubmitCertificateRequest{
			Certificate: protoCert,
		})
	if err != nil {
		return common.Hash{}, fmt.Errorf("failed to submit certificate: %w", aggkitgrpc.RepackGRPCErrorWithDetails(err))
	}

	return common.BytesToHash(response.CertificateId.Value.Value), nil
}

// GetLatestPendingCertificateHeader returns the latest pending certificate header from the AggLayer
func (a *AgglayerGRPCClient) GetLatestSettledCertificateHeader(
	ctx context.Context, networkID uint32) (*types.CertificateHeader, error) {
	ctx, cancel := context.WithTimeout(ctx, a.cfg.RequestTimeout.Duration)
	defer cancel()

	response, err := a.networkStateService.GetLatestCertificateHeader(
		ctx,
		&v1.GetLatestCertificateHeaderRequest{
			NetworkId: networkID,
			Type:      v1.LatestCertificateRequestType_LATEST_CERTIFICATE_REQUEST_TYPE_SETTLED,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get latest settled certificate header: %w",
			aggkitgrpc.RepackGRPCErrorWithDetails(err))
	}

	return convertProtoCertificateHeader(response.CertificateHeader), nil
}

// GetLatestPendingCertificateHeader returns the latest pending certificate header from the AggLayer
func (a *AgglayerGRPCClient) GetLatestPendingCertificateHeader(
	ctx context.Context, networkID uint32) (*types.CertificateHeader, error) {
	ctx, cancel := context.WithTimeout(ctx, a.cfg.RequestTimeout.Duration)
	defer cancel()

	response, err := a.networkStateService.GetLatestCertificateHeader(
		ctx,
		&v1.GetLatestCertificateHeaderRequest{
			NetworkId: networkID,
			Type:      v1.LatestCertificateRequestType_LATEST_CERTIFICATE_REQUEST_TYPE_PENDING,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get latest pending certificate header: %w",
			aggkitgrpc.RepackGRPCErrorWithDetails(err))
	}

	return convertProtoCertificateHeader(response.CertificateHeader), nil
}

// GetCertificateHeader returns the certificate header from the AggLayer for the given certificate ID
func (a *AgglayerGRPCClient) GetCertificateHeader(
	ctx context.Context, certificateID common.Hash) (*types.CertificateHeader, error) {
	ctx, cancel := context.WithTimeout(ctx, a.cfg.RequestTimeout.Duration)
	defer cancel()

	response, err := a.networkStateService.GetCertificateHeader(ctx,
		&v1.GetCertificateHeaderRequest{CertificateId: &v1nodetypes.CertificateId{
			Value: &v1types.FixedBytes32{
				Value: certificateID.Bytes(),
			},
		}},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get certificate header: %w", aggkitgrpc.RepackGRPCErrorWithDetails(err))
	}

	return convertProtoCertificateHeader(response.CertificateHeader), nil
}

// convertAggchainData converts the aggchain data to a proto aggchain data
func convertAggchainData(aggchainData types.AggchainData) (*v1types.AggchainData, error) {
	if aggchainData == nil {
		return nil, errUndefinedAggchainData
	}

	switch ad := aggchainData.(type) {
	case *types.AggchainDataProof:
		return &v1types.AggchainData{
			Data: &v1types.AggchainData_Generic{
				Generic: &v1types.AggchainProof{
					Proof: &v1types.AggchainProof_Sp1Stark{
						Sp1Stark: &v1types.SP1StarkProof{
							Version: ad.Version,
							Proof:   ad.Proof,
							Vkey:    ad.Vkey,
						},
					},
					AggchainParams: &v1types.FixedBytes32{
						Value: ad.AggchainParams.Bytes(),
					},
					Context: ad.Context,
					Signature: &v1types.FixedBytes65{
						Value: ad.Signature,
					},
				},
			},
		}, nil
	case *types.AggchainDataSignature:
		return &v1types.AggchainData{
			Data: &v1types.AggchainData_Signature{
				Signature: &v1types.FixedBytes65{
					Value: ad.Signature,
				},
			},
		}, nil
	default:
		return nil, errUnknownAggchainData
	}
}

// convertProtoCertificateHeader converts a proto certificate header to a types certificate header
func convertProtoCertificateHeader(response *v1nodetypes.CertificateHeader) *types.CertificateHeader {
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
func convertToProtoBridgeExit(be *types.BridgeExit) *v1types.BridgeExit {
	if be == nil {
		return nil
	}

	protoBridgeExit := &v1types.BridgeExit{
		LeafType:    leafTypeToProto(be.LeafType),
		DestNetwork: be.DestinationNetwork,
		DestAddress: &v1types.FixedBytes20{
			Value: be.DestinationAddress.Bytes(),
		},
		TokenInfo: &v1types.TokenInfo{
			OriginNetwork: be.TokenInfo.OriginNetwork,
			OriginTokenAddress: &v1types.FixedBytes20{
				Value: be.TokenInfo.OriginTokenAddress.Bytes(),
			},
		},
	}

	if be.Amount != nil {
		protoBridgeExit.Amount = &v1types.FixedBytes32{
			Value: common.BigToHash(be.Amount).Bytes(),
		}
	}

	if len(be.Metadata) > 0 {
		protoBridgeExit.Metadata = &v1types.FixedBytes32{
			Value: common.BytesToHash(be.Metadata).Bytes(),
		}
	}

	return protoBridgeExit
}

func convertToProtoImportedBridgeExit(ibe *types.ImportedBridgeExit) (*v1types.ImportedBridgeExit, error) {
	if ibe == nil {
		return nil, nil
	}

	importedBridgeExit := &v1types.ImportedBridgeExit{
		BridgeExit: convertToProtoBridgeExit(ibe.BridgeExit),
		GlobalIndex: &v1types.FixedBytes32{
			Value: common.BigToHash(bridgesync.GenerateGlobalIndex(
				ibe.GlobalIndex.MainnetFlag,
				ibe.GlobalIndex.RollupIndex,
				ibe.GlobalIndex.LeafIndex)).Bytes(),
		},
	}

	switch claimData := ibe.ClaimData.(type) {
	case *types.ClaimFromMainnnet:
		importedBridgeExit.Claim = &v1types.ImportedBridgeExit_Mainnet{
			Mainnet: &v1types.ClaimFromMainnet{
				ProofLeafMer: &v1types.MerkleProof{
					Root: &v1types.FixedBytes32{
						Value: claimData.ProofLeafMER.Root.Bytes(),
					},
					Siblings: convertToProtoSiblings(claimData.ProofLeafMER.Proof),
				},
				ProofGerL1Root: &v1types.MerkleProof{
					Root: &v1types.FixedBytes32{
						Value: claimData.ProofGERToL1Root.Root.Bytes(),
					},
					Siblings: convertToProtoSiblings(claimData.ProofGERToL1Root.Proof),
				},
				L1Leaf: &v1types.L1InfoTreeLeafWithContext{
					L1InfoTreeIndex: claimData.L1Leaf.L1InfoTreeIndex,
					Rer: &v1types.FixedBytes32{
						Value: claimData.L1Leaf.RollupExitRoot.Bytes(),
					},
					Mer: &v1types.FixedBytes32{
						Value: claimData.L1Leaf.MainnetExitRoot.Bytes(),
					},
					Inner: &v1types.L1InfoTreeLeaf{
						GlobalExitRoot: &v1types.FixedBytes32{
							Value: claimData.L1Leaf.Inner.GlobalExitRoot.Bytes(),
						},
						BlockHash: &v1types.FixedBytes32{
							Value: claimData.L1Leaf.Inner.BlockHash.Bytes(),
						},
						Timestamp: claimData.L1Leaf.Inner.Timestamp,
					},
				},
			},
		}
	case *types.ClaimFromRollup:
		importedBridgeExit.Claim = &v1types.ImportedBridgeExit_Rollup{
			Rollup: &v1types.ClaimFromRollup{
				ProofLeafLer: &v1types.MerkleProof{
					Root: &v1types.FixedBytes32{
						Value: claimData.ProofLeafLER.Root.Bytes(),
					},
					Siblings: convertToProtoSiblings(claimData.ProofLeafLER.Proof),
				},
				ProofLerRer: &v1types.MerkleProof{
					Root: &v1types.FixedBytes32{
						Value: claimData.ProofLERToRER.Root.Bytes(),
					},
					Siblings: convertToProtoSiblings(claimData.ProofLERToRER.Proof),
				},
				ProofGerL1Root: &v1types.MerkleProof{
					Root: &v1types.FixedBytes32{
						Value: claimData.ProofGERToL1Root.Root.Bytes(),
					},
					Siblings: convertToProtoSiblings(claimData.ProofGERToL1Root.Proof),
				},
				L1Leaf: &v1types.L1InfoTreeLeafWithContext{
					L1InfoTreeIndex: claimData.L1Leaf.L1InfoTreeIndex,
					Rer: &v1types.FixedBytes32{
						Value: claimData.L1Leaf.RollupExitRoot.Bytes(),
					},
					Mer: &v1types.FixedBytes32{
						Value: claimData.L1Leaf.MainnetExitRoot.Bytes(),
					},
					Inner: &v1types.L1InfoTreeLeaf{
						GlobalExitRoot: &v1types.FixedBytes32{
							Value: claimData.L1Leaf.Inner.GlobalExitRoot.Bytes(),
						},
						BlockHash: &v1types.FixedBytes32{
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
func convertToProtoSiblings(siblings treetypes.Proof) []*v1types.FixedBytes32 {
	protoSiblings := make([]*v1types.FixedBytes32, len(siblings))

	for i, sibling := range siblings {
		protoSiblings[i] = &v1types.FixedBytes32{
			Value: sibling.Bytes(),
		}
	}

	return protoSiblings
}

// nullableBytesToHash converts a nullable byte slice to a hash pointer
func nullableBytesToHash(b *v1types.FixedBytes32) *common.Hash {
	if b == nil || len(b.Value) == 0 {
		return nil
	}

	hash := common.BytesToHash(b.Value)
	return &hash
}

// leafTypeToProto converts a leaf type to a proto leaf type
func leafTypeToProto(leafType types.LeafType) v1types.LeafType {
	switch leafType {
	case types.LeafTypeAsset:
		return v1types.LeafType_LEAF_TYPE_TRANSFER
	case types.LeafTypeMessage:
		return v1types.LeafType_LEAF_TYPE_MESSAGE
	default:
		return v1types.LeafType_LEAF_TYPE_UNSPECIFIED
	}
}

// certificateStatusFromProto converts a proto certificate status to a certificate status
func certificateStatusFromProto(status v1nodetypes.CertificateStatus) types.CertificateStatus {
	switch status {
	case v1nodetypes.CertificateStatus_CERTIFICATE_STATUS_PENDING:
		return types.Pending
	case v1nodetypes.CertificateStatus_CERTIFICATE_STATUS_PROVEN:
		return types.Proven
	case v1nodetypes.CertificateStatus_CERTIFICATE_STATUS_CANDIDATE:
		return types.Candidate
	case v1nodetypes.CertificateStatus_CERTIFICATE_STATUS_IN_ERROR:
		return types.InError
	case v1nodetypes.CertificateStatus_CERTIFICATE_STATUS_SETTLED:
		return types.Settled
	default:
		return types.Pending
	}
}
