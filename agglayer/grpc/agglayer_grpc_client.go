package grpc

import (
	"github.com/agglayer/aggkit/agglayer/proto/node"
	"github.com/agglayer/aggkit/common"
)

type AgglayerGRPCClient struct {
	cfgService          node.AgglayerConfigurationServiceClient
	networkStateService node.AgglayerNetworkStateServiceClient
	submissionService   node.AgglayerCertificateSubmissionServiceClient
}

// NewAggchainProofClient initializes a new AggchainProof instance
func NewAgglayerGRPCClient(serverAddr string) (*AgglayerGRPCClient, error) {
	grpcClient, err := common.NewClient(serverAddr)
	if err != nil {
		return nil, err
	}
	return &AgglayerGRPCClient{
		cfgService:          node.NewAgglayerConfigurationServiceClient(grpcClient.Conn()),
		networkStateService: node.NewAgglayerNetworkStateServiceClient(grpcClient.Conn()),
		submissionService:   node.NewAgglayerCertificateSubmissionServiceClient(grpcClient.Conn()),
	}, nil
}
