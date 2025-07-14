package validator

import (
	"context"

	v1types "buf.build/gen/go/agglayer/interop/protocolbuffers/go/agglayer/interop/types/v1"
	"github.com/agglayer/aggkit"
	"github.com/agglayer/aggkit/agglayer"
	agglayergrpc "github.com/agglayer/aggkit/agglayer/grpc"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
	v1 "github.com/agglayer/aggkit/aggsender/validator/proto/v1"
	"github.com/agglayer/aggkit/grpc"
	"github.com/agglayer/aggkit/log"
	signertypes "github.com/agglayer/go_signer/signer/types"
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
	signer         signertypes.Signer
}

func NewValidatorService(validator types.CertificateValidator,
	agglayerClient AgglayerClientInterface,
	signer signertypes.Signer) *ValidatorService {
	return &ValidatorService{
		validator:      validator,
		agglayerClient: agglayerClient,
		signer:         signer,
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
	if req == nil || req.Certificate == nil {
		return nil, grpc.GRPCError{
			Code:    codes.NotFound,
			Message: "requrired a certificate",
		}
	}

	log.Infof("Received certificate network:%d,  height: %d", req.Certificate.NetworkId, req.Certificate.Height)

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
			log.Errorf("Certificate header is nil for ID: %s", previousCertificateID.Hex())
			return nil, grpc.GRPCError{
				Code:    codes.NotFound,
				Message: "Certificate header is nil in agglayer",
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
	signature, err := s.signCertificate(ctx, cert)
	if err != nil {
		log.Errorf("Error signing certificate: %v", err)
		return nil, grpc.GRPCError{
			Code:    codes.Internal,
			Message: "Error signing certificate: " + err.Error(),
		}
	}
	return &v1.ValidateCertificateResponse{
		Signature: &v1types.FixedBytes65{
			Value: signature,
		},
	}, nil
}

func (s *ValidatorService) signCertificate(ctx context.Context, cert *agglayertypes.Certificate) ([]byte, error) {
	if s.signer == nil {
		return nil, grpc.GRPCError{
			Code:    codes.Internal,
			Message: "Signer is not initialized",
		}
	}
	hashToSign := HashCertificateToSign(cert)
	return s.signer.SignHash(ctx, hashToSign)
}
