package validator

import (
	"context"

	v1 "github.com/agglayer/aggkit/aggsender/validator/proto/v1"
	"github.com/agglayer/aggkit/log"
	"google.golang.org/protobuf/types/known/emptypb"
)

// ValidatorService implements the gRPC server for the AggsenderValidator service.
type ValidatorService struct {
	// Embed the generated server interface to ensure forward compatibility
	v1.UnimplementedAggsenderValidatorServer
}

// ValidateCertificate validates a new certificate
func (s *ValidatorService) ValidateCertificate(
	ctx context.Context, req *v1.ValidateCertificateRequest) (*emptypb.Empty, error) {
	// TODO: implement actual logic here
	log.Infof("Received certificate with height: %d", req.Certificate.Height)

	return &emptypb.Empty{}, nil
}
