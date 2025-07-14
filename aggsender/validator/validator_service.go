package validator

import (
	"context"

	"github.com/agglayer/aggkit"
	"github.com/agglayer/aggkit/agglayer"
	agglayergrpc "github.com/agglayer/aggkit/agglayer/grpc"
	"github.com/agglayer/aggkit/aggsender/types"
	v1 "github.com/agglayer/aggkit/aggsender/validator/proto/v1"
	"github.com/agglayer/aggkit/grpc"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/emptypb"
)

type AgglayerClientInterface = agglayer.AggLayerClientCertificateIDQuerier

// ValidatorService implements the gRPC server for the AggsenderValidator service.
type ValidatorService struct {
	// Embed the generated server interface to ensure forward compatibility
	v1.UnimplementedAggsenderValidatorServer

	validator      types.CertificateValidator
	agglayerClient AgglayerClientInterface
}

func NewValidatorService(validator types.CertificateValidator,
	agglayerClient AgglayerClientInterface) *ValidatorService {
	return &ValidatorService{
		validator:      validator,
		agglayerClient: agglayerClient,
	}
}

// Implementa el método Status
func (s *ValidatorService) Status(ctx context.Context, in *emptypb.Empty) (*v1.StatusResponse, error) {
	version := aggkit.GetVersion()
	return &v1.StatusResponse{
		Version: version.Brief(),
		Status:  "OK",
	}, nil
}

// ValidateCertificate validates a new certificate
func (s *ValidatorService) ValidateCertificate(
	ctx context.Context, req *v1.ValidateCertificateRequest) (*v1.ValidateCertificateResponse, error) {
	// TODO: implement actual logic here
	if req != nil && req.Certificate != nil {
		log.Infof("Received certificate network:%d,  height: %d", req.Certificate.NetworkId, req.Certificate.Height)
	}
	params := VerifyIncommingRequests{}
	if req.PreviousCertificateId != nil && req.PreviousCertificateId.Value != nil {
		previousCertificateID := common.BytesToHash(req.PreviousCertificateId.Value.Value)
		log.Debugf("Previous certificate ID: %s", previousCertificateID.Hex())
		certHeader, err := s.agglayerClient.GetCertificateHeader(ctx, previousCertificateID)
		if err != nil {
			log.Errorf("Error getting certificate header: %v", err)
			return nil, grpc.GRPCError{
				Code:    codes.NotFound,
				Message: "fail to request certificate header to agglayer: " + err.Error(),
			}
		}
		if certHeader == nil {
			log.Errorf("Certificate header not found for ID: %s", previousCertificateID.Hex())
			return nil, grpc.GRPCError{
				Code:    codes.NotFound,
				Message: "Certificate header not found in agglayer",
			}
		}
		params.PreviousCertificate = certHeader
	}
	cert, err := agglayergrpc.ConvertProtoCertToAgglayer(req.Certificate)
	if err != nil {
		log.Errorf("Error converting certificate: %v", err)
		return nil, grpc.GRPCError{
			Code:    codes.InvalidArgument,
			Message: "Invalid certificate format: " + err.Error(),
		}
	}
	params.Certificate = cert
	err = s.validator.ValidateCertificate(ctx, params)
	if err != nil {
		log.Errorf("Certificate validation failed: %v", err)
		return nil, grpc.GRPCError{
			Code:    codes.Internal,
			Message: "Certificate validation failed: " + err.Error(),
		}
	}
	return &v1.ValidateCertificateResponse{}, nil
}
