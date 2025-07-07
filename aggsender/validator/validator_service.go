package validator

import (
	"context"

	nodev1 "buf.build/gen/go/agglayer/agglayer/protocolbuffers/go/agglayer/node/types/v1"
	v1 "github.com/agglayer/aggkit/aggsender/validator/proto/v1"
	"github.com/agglayer/aggkit/log"
	"google.golang.org/protobuf/types/known/emptypb"
)

// ValidatorService implements the gRPC server for the AggsenderValidator service.
type ValidatorService struct {
	// Embed the generated server interface to ensure forward compatibility
	v1.UnimplementedAggsenderValidatorServer
}

func (s *ValidatorService) ValidateCertificate(ctx context.Context, cert *nodev1.Certificate) (*emptypb.Empty, error) {
	// TODO: implement actual logic here
	log.Infof("Received certificate with height: %d", cert.Height)

	return &emptypb.Empty{}, nil
}
