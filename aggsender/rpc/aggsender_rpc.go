package aggsenderrpc

import (
	"context"
	"errors"

	"github.com/0xPolygon/cdk-rpc/rpc"
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

// AggsenderRPC is the JSON-RPC wrapper of the aggsender API. Its handlers live
// in aggsender_api.go; this type is kept until the JSON-RPC server is removed.
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
func (b *AggsenderRPC) Status() (interface{}, rpc.Error) {
	return b.aggsender.Info(), nil
}

// TriggerCertificate forces the publication of an epoch event to trigger certificate creation
func (b *AggsenderRPC) TriggerCertificate() (interface{}, rpc.Error) {
	b.aggsender.ForceTriggerCertificate()
	return nil, nil
}

// GetCertificateHeaderPerHeight returns the certificate header for the given height
// if param is `nil` it returns the last sent certificate
func (b *AggsenderRPC) GetCertificateHeaderPerHeight(height *uint64) (interface{}, rpc.Error) {
	cert, err := certificateHeaderPerHeight(b.storage, height)
	if err != nil {
		return nil, toRPCError(err)
	}

	return cert, nil
}

// GetCertificateBridgeExits returns the bridge exits for the certificate at the given height.
// If height is nil, returns the bridge exits of the last sent certificate.
func (b *AggsenderRPC) GetCertificateBridgeExits(height *uint64) (interface{}, rpc.Error) {
	bridgeExits, err := certificateBridgeExits(b.storage, height)
	if err != nil {
		return nil, toRPCError(err)
	}

	return bridgeExits, nil
}

// toRPCError maps a handler error to the JSON-RPC error code it had before the
// REST API existed.
func toRPCError(err error) rpc.Error {
	var notFound notFoundError
	if errors.As(err, &notFound) {
		return rpc.NewRPCError(rpc.NotFoundErrorCode, err.Error())
	}

	return rpc.NewRPCError(rpc.DefaultErrorCode, err.Error())
}
