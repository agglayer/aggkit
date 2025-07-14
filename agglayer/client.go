package agglayer

import (
	"context"
	"errors"
	"time"

	"github.com/agglayer/aggkit/agglayer/grpc"
	"github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
)

const errCodeAgglayerRateLimitExceeded int = -10007

var ErrAgglayerRateLimitExceeded = errors.New("agglayer rate limit exceeded")

type AggLayerClientGetEpochConfiguration interface {
	GetEpochConfiguration(ctx context.Context) (*types.ClockConfiguration, error)
}

type AggLayerClientRecoveryQuerier interface {
	GetLatestSettledCertificateHeader(ctx context.Context, networkID uint32) (*types.CertificateHeader, error)
	GetLatestPendingCertificateHeader(ctx context.Context, networkID uint32) (*types.CertificateHeader, error)
}

type AggLayerClientCertificateIDQuerier interface {
	GetCertificateHeader(ctx context.Context, certificateID common.Hash) (*types.CertificateHeader, error)
}

// AgglayerClientInterface is the interface that defines the methods that the AggLayerClient will implement
type AgglayerClientInterface interface {
	SendCertificate(ctx context.Context, certificate *types.Certificate, validatorSignature []byte) (common.Hash, error)
	GetCertificateHeader(ctx context.Context, certificateHash common.Hash) (*types.CertificateHeader, error)
	AggLayerClientGetEpochConfiguration
	AggLayerClientRecoveryQuerier
	AggLayerClientCertificateIDQuerier
}

func NewAgglayerClient(cfg ClientConfig) (AgglayerClientInterface, error) {
	var client AgglayerClientInterface
	client, err := grpc.NewAgglayerGRPCClient(cfg.GRPC)
	if err != nil {
		return nil, err
	}
	if cfg.Cached {
		client = NewCertificateCache(
			client, time.Second, 100)
	}
	return client, nil
}
