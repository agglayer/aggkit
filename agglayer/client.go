package agglayer

import (
	"context"
	"errors"

	"github.com/agglayer/aggkit/agglayer/grpc"
	"github.com/agglayer/aggkit/agglayer/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
)

// NOTE:  errCodeAgglayerRateLimitExceeded is -10007

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
	SendCertificate(ctx context.Context, certificate *types.Certificate) (common.Hash, error)
	GetCertificateHeader(ctx context.Context, certificateHash common.Hash) (*types.CertificateHeader, error)
	GetNetworkInfo(ctx context.Context, networkID uint32) (types.NetworkInfo, error)
	AggLayerClientGetEpochConfiguration
	AggLayerClientRecoveryQuerier
	AggLayerClientCertificateIDQuerier
}

func NewAgglayerClient(cfg ClientConfig, logger aggkitcommon.Logger) (AgglayerClientInterface, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	var client AgglayerClientInterface
	client, err := grpc.NewAgglayerGRPCClient(cfg.GRPC)
	if err != nil {
		return nil, err
	}
	if cfg.Cached {
		client = NewCertificateCache(
			client, cfg.ConfigurationCache.TTL.Duration, cfg.ConfigurationCache.Capacity)
	}

	// Apply rate limiting wrapper if any rate limits are configured
	if len(cfg.APIRateLimits) > 0 {
		client = NewRateLimitWrapper(client, cfg, logger)
	}

	return client, nil
}
