package grpc

import (
	"context"
	"errors"

	"github.com/agglayer/aggkit/agglayer/proto/node"
	protoTypes "github.com/agglayer/aggkit/agglayer/proto/types"
	"github.com/agglayer/aggkit/agglayer/types"
	aggkitCommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
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
	certificate *types.SignedCertificate) (common.Hash, error) {
	response, err := a.submissionService.SubmitCertificate(ctx, &node.SubmitCertificateRequest{
		Certificate: &protoTypes.Certificate{
			NetworkId: certificate.NetworkID,
			Height:    certificate.Height,
		},
	})

	if err != nil {
		return common.Hash{}, err
	}

	return common.BytesToHash(response.CertificateId.Value.Value), nil
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
		// AggchainProof - TODO
	}

	if response.Error != nil && response.Error.Message != nil {
		header.Error = errors.New(string(response.Error.Message))
	}

	return header
}

// nullableBytesToHash converts a nullable byte slice to a hash pointer
func nullableBytesToHash(b []byte) *common.Hash {
	if len(b) == 0 {
		return nil
	}

	hash := common.BytesToHash(b)
	return &hash
}
