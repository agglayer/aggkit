package grpc

import (
	"context"
	"errors"

	"github.com/agglayer/aggkit/agglayer/proto/node"
	protoTypes "github.com/agglayer/aggkit/agglayer/proto/types"
	"github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/bridgesync"
	aggkitCommon "github.com/agglayer/aggkit/common"
	treeTypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

var (
	errHasAggProofAndSig = errors.New("certificate can either have an aggchain proof or a signature, " +
		"it can not have both")
	errHasNoAggProofOrSig = errors.New("certificate must have an aggchain proof or a signature")
)

type AgglayerGRPCClient struct {
	networkStateService node.NodeStateServiceClient
	cfgService          node.ConfigurationServiceClient
	submissionService   node.CertificateSubmissionServiceClient
}

// NewAggchainProofClient initializes a new AggchainProof instance
func NewAgglayerGRPCClient(serverAddr string) (*AgglayerGRPCClient, error) {
	grpcClient, err := aggkitCommon.NewClient(serverAddr)
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
	response, err := a.cfgService.GetEpochConfiguration(ctx, &node.GetEpochConfigurationRequest{})
	if err != nil {
		return nil, err
	}

	return &types.ClockConfiguration{
		EpochDuration: response.EpochConfiguration.EpochDuration,
		GenesisBlock:  response.EpochConfiguration.GenesisBlock,
	}, nil
}

func (a *AgglayerGRPCClient) SendCertificate(ctx context.Context,
	certificate *types.Certificate) (common.Hash, error) {
	if len(certificate.AggchainProof) > 0 && len(certificate.Signature) > 0 {
		return common.Hash{}, errHasAggProofAndSig
	}

	var aggchainProof *protoTypes.AggchainProof
	if len(certificate.AggchainProof) > 0 {
		aggchainProof = &protoTypes.AggchainProof{
			Proof: &protoTypes.AggchainProof_Sp1Stark{
				Sp1Stark: &protoTypes.FixedBytes32{
					Value: certificate.AggchainProof,
				},
			},
		}
	} else if len(certificate.Signature) > 0 {
		aggchainProof = &protoTypes.AggchainProof{
			Proof: &protoTypes.AggchainProof_Signature{
				Signature: &protoTypes.FixedBytes65{
					Value: certificate.Signature,
				},
			},
		}
	} else {
		return common.Hash{}, errHasNoAggProofOrSig
	}

	protoCert := &protoTypes.Certificate{
		NetworkId: certificate.NetworkID,
		Height:    certificate.Height,
		PrevLocalExitRoot: &protoTypes.FixedBytes32{
			Value: certificate.PrevLocalExitRoot.Bytes(),
		},
		NewLocalExitRoot: &protoTypes.FixedBytes32{
			Value: certificate.NewLocalExitRoot.Bytes(),
		},
		Metadata: &protoTypes.FixedBytes32{
			Value: certificate.Metadata.Bytes(),
		},
		CustomChainData:     certificate.CustomChainData,
		AggchainProof:       aggchainProof,
		BridgeExits:         make([]*protoTypes.BridgeExit, 0, len(certificate.BridgeExits)),
		ImportedBridgeExits: make([]*protoTypes.ImportedBridgeExit, 0, len(certificate.ImportedBridgeExits)),
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

	response, err := a.submissionService.SubmitCertificate(ctx, &node.SubmitCertificateRequest{
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
	response, err := a.networkStateService.GetLatestSettledCertificateHeader(ctx,
		&node.GetLatestSettledCertificateHeaderRequest{NetworkId: networkID})
	if err != nil {
		return nil, err
	}

	return convertProtoCertificateHeader(response.CertificateHeader), nil
}

// GetLatestPendingCertificateHeader returns the latest pending certificate header from the AggLayer
func (a *AgglayerGRPCClient) GetLatestPendingCertificateHeader(
	ctx context.Context, networkID uint32) (*types.CertificateHeader, error) {
	response, err := a.networkStateService.GetLatestPendingCertificateHeader(ctx,
		&node.GetLatestPendingCertificateHeaderRequest{NetworkId: networkID})
	if err != nil {
		return nil, err
	}

	return convertProtoCertificateHeader(response.CertificateHeader), nil
}

// convertProtoCertificateHeader converts a proto certificate header to a types certificate header
func convertProtoCertificateHeader(response *protoTypes.CertificateHeader) *types.CertificateHeader {
	header := &types.CertificateHeader{
		NetworkID:             response.NetworkId,
		Height:                response.Height,
		EpochNumber:           response.EpochNumber,
		CertificateIndex:      response.CertificateIndex,
		CertificateID:         common.BytesToHash(response.CertificateId.Value.Value),
		PreviousLocalExitRoot: nullableBytesToHash(response.PrevLocalExitRoot.Value),
		NewLocalExitRoot:      common.BytesToHash(response.NewLocalExitRoot.Value),
		Status:                types.CertificateStatus(response.Status),
		Metadata:              common.BytesToHash(response.Metadata.Value),
	}

	if response.Error != nil && response.Error.Message != nil {
		header.Error = errors.New(string(response.Error.Message))
	}

	return header
}

func convertToProtoBridgeExit(be *types.BridgeExit) *protoTypes.BridgeExit {
	if be == nil {
		return nil
	}

	protoBridgeExit := &protoTypes.BridgeExit{
		LeafType:    protoTypes.LeafType(be.LeafType),
		DestNetwork: be.DestinationNetwork,
		DestAddress: &protoTypes.FixedBytes20{
			Value: be.DestinationAddress.Bytes(),
		},
		Amount: &protoTypes.FixedBytes32{
			Value: be.Amount.Bytes(),
		},
		TokenInfo: &protoTypes.TokenInfo{
			OriginNetwork: be.TokenInfo.OriginNetwork,
			OriginTokenAddress: &protoTypes.FixedBytes20{
				Value: be.TokenInfo.OriginTokenAddress.Bytes(),
			},
		},
	}

	if len(be.Metadata) > 0 {
		protoBridgeExit.Metadata = &protoTypes.FixedBytes32{
			Value: be.Metadata,
		}
	}

	return protoBridgeExit
}

func convertToProtoImportedBridgeExit(ibe *types.ImportedBridgeExit) (*protoTypes.ImportedBridgeExit, error) {
	if ibe == nil {
		return nil, nil
	}

	importedBridgeExit := &protoTypes.ImportedBridgeExit{
		BridgeExit: convertToProtoBridgeExit(ibe.BridgeExit),
		GlobalIndex: &protoTypes.FixedBytes32{
			Value: bridgesync.GenerateGlobalIndex(
				ibe.GlobalIndex.MainnetFlag,
				ibe.GlobalIndex.RollupIndex,
				ibe.GlobalIndex.LeafIndex).Bytes(),
		},
	}

	switch claimData := ibe.ClaimData.(type) {
	case *types.ClaimFromMainnnet:
		importedBridgeExit.Claim = &protoTypes.ImportedBridgeExit_Mainnet{
			Mainnet: &protoTypes.ClaimFromMainnet{
				ProofLeafMer: &protoTypes.MerkleProof{
					Root: &protoTypes.FixedBytes32{
						Value: claimData.ProofLeafMER.Root.Bytes(),
					},
					Siblings: convertToProtoSiblings(claimData.ProofLeafMER.Proof),
				},
				ProofGerL1Root: &protoTypes.MerkleProof{
					Root: &protoTypes.FixedBytes32{
						Value: claimData.ProofGERToL1Root.Root.Bytes(),
					},
					Siblings: convertToProtoSiblings(claimData.ProofGERToL1Root.Proof),
				},
				L1Leaf: &protoTypes.L1InfoTreeLeaf{
					L1InfoTreeIndex: claimData.L1Leaf.L1InfoTreeIndex,
					Rer: &protoTypes.FixedBytes32{
						Value: claimData.L1Leaf.RollupExitRoot.Bytes(),
					},
					Mer: &protoTypes.FixedBytes32{
						Value: claimData.L1Leaf.MainnetExitRoot.Bytes(),
					},
					Inner: &protoTypes.L1InfoTreeLeafInner{
						GlobalExitRoot: &protoTypes.FixedBytes32{
							Value: claimData.L1Leaf.Inner.GlobalExitRoot.Bytes(),
						},
						BlockHash: &protoTypes.FixedBytes32{
							Value: claimData.L1Leaf.Inner.BlockHash.Bytes(),
						},
						Timestamp: claimData.L1Leaf.Inner.Timestamp,
					},
				},
			},
		}
	case *types.ClaimFromRollup:
		importedBridgeExit.Claim = &protoTypes.ImportedBridgeExit_Rollup{
			Rollup: &protoTypes.ClaimFromRollup{
				ProofLeafLer: &protoTypes.MerkleProof{
					Root: &protoTypes.FixedBytes32{
						Value: claimData.ProofLeafLER.Root.Bytes(),
					},
					Siblings: convertToProtoSiblings(claimData.ProofLeafLER.Proof),
				},
				ProofLerRer: &protoTypes.MerkleProof{
					Root: &protoTypes.FixedBytes32{
						Value: claimData.ProofLERToRER.Root.Bytes(),
					},
					Siblings: convertToProtoSiblings(claimData.ProofLERToRER.Proof),
				},
				ProofGerL1Root: &protoTypes.MerkleProof{
					Root: &protoTypes.FixedBytes32{
						Value: claimData.ProofGERToL1Root.Root.Bytes(),
					},
					Siblings: convertToProtoSiblings(claimData.ProofGERToL1Root.Proof),
				},
				L1Leaf: &protoTypes.L1InfoTreeLeaf{
					L1InfoTreeIndex: claimData.L1Leaf.L1InfoTreeIndex,
					Rer: &protoTypes.FixedBytes32{
						Value: claimData.L1Leaf.RollupExitRoot.Bytes(),
					},
					Mer: &protoTypes.FixedBytes32{
						Value: claimData.L1Leaf.MainnetExitRoot.Bytes(),
					},
					Inner: &protoTypes.L1InfoTreeLeafInner{
						GlobalExitRoot: &protoTypes.FixedBytes32{
							Value: claimData.L1Leaf.Inner.GlobalExitRoot.Bytes(),
						},
						BlockHash: &protoTypes.FixedBytes32{
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

func convertToProtoSiblings(siblings treeTypes.Proof) []*protoTypes.FixedBytes32 {
	protoSiblings := make([]*protoTypes.FixedBytes32, len(siblings))

	for i, sibling := range siblings {
		protoSiblings[i] = &protoTypes.FixedBytes32{
			Value: sibling.Bytes(),
		}
	}

	return protoSiblings
}

// GetCertificateHeader returns the certificate header from the AggLayer for the given certificate ID
func (a *AgglayerGRPCClient) GetCertificateHeader(ctx context.Context,
	certificateID common.Hash) (*types.CertificateHeader, error) {
	response, err := a.networkStateService.GetCertificateHeader(ctx,
		&node.GetCertificateHeaderRequest{CertificateId: &protoTypes.CertificateId{
			Value: &protoTypes.FixedBytes32{
				Value: certificateID.Bytes(),
			},
		},
		})
	if err != nil {
		return nil, err
	}

	return convertProtoCertificateHeader(response.CertificateHeader), nil
}

// nullableBytesToHash converts a nullable byte slice to a hash pointer
func nullableBytesToHash(b []byte) *common.Hash {
	if len(b) == 0 {
		return nil
	}

	hash := common.BytesToHash(b)
	return &hash
}
