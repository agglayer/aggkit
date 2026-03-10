package aggsenderrpc

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/0xPolygon/cdk-rpc/rpc"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/log"
)

type AggsenderStorer interface {
	GetCertificateByHeight(height uint64) (*types.Certificate, error)
	GetLastSentCertificate() (*types.Certificate, error)
	SaveLastSentCertificate(ctx context.Context, certificate types.Certificate) error
}

type AggsenderInterface interface {
	Info() types.AggsenderInfo
	ForceTriggerCertificate()
}

// AggsenderRPC is the RPC interface for the aggsender
type AggsenderRPC struct {
	logger    *log.Logger
	storage   AggsenderStorer
	aggsender AggsenderInterface
}

// NewAggsenderRPC creates a new AggsenderRPC instance.
func NewAggsenderRPC(
	logger *log.Logger,
	storage AggsenderStorer,
	aggsender AggsenderInterface,
) *AggsenderRPC {
	return &AggsenderRPC{
		logger:    logger,
		storage:   storage,
		aggsender: aggsender,
	}
}

// Status returns the status of the aggsender
// curl -X POST http://localhost:5576/ "Content-Type: application/json" \
// -d '{"method":"aggsender_status", "params":[], "id":1}'
func (b *AggsenderRPC) Status() (interface{}, rpc.Error) {
	info := b.aggsender.Info()
	return info, nil
}

// TriggerCertificate forces the publication of an epoch event to trigger certificate creation
// curl -X POST http://localhost:5576/ "Content-Type: application/json" \
// -d '{"method":"aggsender_triggerCertificate", "params":[], "id":1}'
func (b *AggsenderRPC) TriggerCertificate() (interface{}, rpc.Error) {
	b.aggsender.ForceTriggerCertificate()
	return nil, nil
}

// GetCertificateHeaderPerHeight returns the certificate header for the given height
// if param is `nil` it returns the last sent certificate
// latest:
//
//	curl -X POST http://localhost:5576/ -H "Content-Type: application/json" \
//	 -d '{"method":"aggsender_getCertificateHeaderPerHeight", "params":[], "id":1}'
//
// specific height:
//
// curl -X POST http://localhost:5576/ -H "Content-Type: application/json" \
// -d '{"method":"aggsender_getCertificateHeaderPerHeight", "params":[$height], "id":1}'
func (b *AggsenderRPC) GetCertificateHeaderPerHeight(height *uint64) (interface{}, rpc.Error) {
	var (
		cert *types.Certificate
		err  error
	)
	if height == nil {
		cert, err = b.storage.GetLastSentCertificate()
	} else {
		cert, err = b.storage.GetCertificateByHeight(*height)
	}
	if err != nil {
		return nil, rpc.NewRPCError(rpc.DefaultErrorCode, fmt.Sprintf("error getting certificate by height: %v", err))
	}
	if cert == nil {
		return nil, rpc.NewRPCError(rpc.NotFoundErrorCode, "certificate not found")
	}

	return cert, nil
}

// GetCertificateBridgeExits returns the bridge exits for the certificate at the given height.
// If height is nil, returns the bridge exits of the last sent certificate.
//
// curl -X POST http://localhost:5576/ -H "Content-Type: application/json" \
//
//	-d '{"method":"aggsender_getCertificateBridgeExits", "params":[], "id":1}'
//
// curl -X POST http://localhost:5576/ -H "Content-Type: application/json" \
//
//	-d '{"method":"aggsender_getCertificateBridgeExits", "params":[42], "id":1}'
func (b *AggsenderRPC) GetCertificateBridgeExits(height *uint64) (interface{}, rpc.Error) {
	var resolvedHeight uint64
	if height == nil {
		cert, err := b.storage.GetLastSentCertificate()
		if err != nil {
			return nil, rpc.NewRPCError(rpc.DefaultErrorCode,
				fmt.Sprintf("error getting last sent certificate: %v", err))
		}
		if cert == nil {
			return nil, rpc.NewRPCError(rpc.NotFoundErrorCode, "no certificate found")
		}
		resolvedHeight = cert.Header.Height
	} else {
		resolvedHeight = *height
	}
	cert, err := b.storage.GetCertificateByHeight(resolvedHeight)
	if err != nil {
		return nil, rpc.NewRPCError(rpc.DefaultErrorCode,
			fmt.Sprintf("error getting certificate at height %d: %v", resolvedHeight, err))
	}
	if cert == nil || cert.SignedCertificate == nil {
		return nil, rpc.NewRPCError(rpc.NotFoundErrorCode,
			fmt.Sprintf("certificate not found at height %d", resolvedHeight))
	}
	// Certs recovered from agglayer use a placeholder signed certificate ("na/agglayer header").
	// We don't have the actual signed cert data for these certs, so return not found.
	if cert.Header != nil && cert.Header.CertSource == types.CertificateSourceAggLayer {
		return nil, rpc.NewRPCError(rpc.NotFoundErrorCode,
			fmt.Sprintf("certificate not found at height %d", resolvedHeight))
	}
	var agglayerCert agglayertypes.Certificate
	if err := json.Unmarshal([]byte(*cert.SignedCertificate), &agglayerCert); err != nil {
		return nil, rpc.NewRPCError(rpc.DefaultErrorCode,
			fmt.Sprintf("failed to unmarshal certificate at height %d: %v", resolvedHeight, err))
	}
	if agglayerCert.BridgeExits == nil {
		return nil, rpc.NewRPCError(rpc.NotFoundErrorCode,
			fmt.Sprintf("certificate not found at height %d", resolvedHeight))
	}
	return agglayerCert.BridgeExits, nil
}
