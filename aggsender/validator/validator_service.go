package validator

import (
	"context"
	"fmt"

	v1types "buf.build/gen/go/agglayer/interop/protocolbuffers/go/agglayer/interop/types/v1"
	"github.com/agglayer/aggkit"
	"github.com/agglayer/aggkit/agglayer"
	agglayergrpc "github.com/agglayer/aggkit/agglayer/grpc"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
	v1 "github.com/agglayer/aggkit/aggsender/validator/proto/v1"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/grpc"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/common"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/emptypb"
)

var (
	errSignerNotInitialized = grpc.GRPCError{
		Code:    codes.Internal,
		Message: "Signer is not initialized",
	}
)

// ValidatorService implements the gRPC server for the AggsenderValidator service.
type ValidatorService struct {
	// Embed the generated server interface to ensure forward compatibility
	v1.UnimplementedAggsenderValidatorServer

	validator      types.CertificateValidator
	agglayerClient agglayer.AggLayerClientCertificateIDQuerier
	signer         signertypes.Signer
	log            aggkitcommon.Logger
}

func NewValidatorService(
	logger aggkitcommon.Logger,
	validator types.CertificateValidator,
	agglayerClient agglayer.AggLayerClientCertificateIDQuerier,
	signer signertypes.Signer) *ValidatorService {
	return &ValidatorService{
		log:            logger,
		validator:      validator,
		agglayerClient: agglayerClient,
		signer:         signer,
	}
}

// HealthCheck implements the HealthCheck method of the AggsenderValidator service.
func (s *ValidatorService) HealthCheck(ctx context.Context, in *emptypb.Empty) (*v1.HealthCheckResponse, error) {
	version := aggkit.GetVersion()
	return &v1.HealthCheckResponse{
		Version: version.Brief(),
		Status:  types.HealthCheckStatusOK,
		Reason:  "",
	}, nil
}

func (s *ValidatorService) ValidateGER(
	ctx context.Context,
	req *v1.ValidateGERRequest,
) (*v1.ValidateGERResponse, error) {
	if req == nil || req.Ger == nil {
		return nil, grpc.GRPCError{
			Code:    codes.NotFound,
			Message: "required a GlobalExitRoot",
		}
	}

	ger := common.BytesToHash(req.Ger.Value)
	s.log.Infof("Received GER to validate and sign: %s", ger.Hex())

	err := s.validator.ValidateGER(ctx, ger)
	if err != nil {
		s.log.Errorf("Error signing GER: %v", err)
		return nil, grpc.GRPCError{
			Code:    codes.Internal,
			Message: "Error signing GER: " + err.Error(),
		}
	}

	signature, err := s.signGER(ctx, ger)
	if err != nil {
		s.log.Errorf("Error signing GER: %v", err)
		return nil, grpc.GRPCError{
			Code:    codes.Internal,
			Message: "Error signing GER: " + err.Error(),
		}
	}

	s.log.Infof("GER %s validated and signed successfully: %s", ger.Hex(), common.Bytes2Hex(signature))

	return &v1.ValidateGERResponse{
		Signature: &v1types.FixedBytes65{
			Value: signature,
		},
	}, nil
}

// ValidateCertificate validates a new certificate
func (s *ValidatorService) ValidateCertificate(
	ctx context.Context, req *v1.ValidateCertificateRequest) (*v1.ValidateCertificateResponse, error) {
	if req == nil || req.Certificate == nil {
		return nil, grpc.GRPCError{
			Code:    codes.NotFound,
			Message: "required a certificate",
		}
	}

	s.log.Infof("Received certificate network:%d,  height: %d", req.Certificate.NetworkId, req.Certificate.Height)

	params := types.VerifyIncomingRequest{}
	if req.PreviousCertificateId != nil && req.PreviousCertificateId.Value != nil {
		previousCertificateID := common.BytesToHash(req.PreviousCertificateId.Value.Value)
		s.log.Debugf("Previous certificate ID: %s", previousCertificateID.Hex())
		certHeader, err := s.agglayerClient.GetCertificateHeader(ctx, previousCertificateID)
		if err != nil {
			msg := fmt.Sprintf("fails to request certificate header to agglayer for prevCertID %s.Err: %s",
				previousCertificateID.Hex(), err.Error())
			s.log.Errorf(msg)
			return nil, grpc.GRPCError{
				Code:    codes.NotFound,
				Message: msg,
			}
		}
		if certHeader == nil {
			s.log.Errorf("Certificate header is nil for ID: %s", previousCertificateID.Hex())
			return nil, grpc.GRPCError{
				Code:    codes.NotFound,
				Message: "Certificate header is nil in agglayer",
			}
		}
		params.PreviousCertificate = certHeader
	}
	cert, err := agglayergrpc.ConvertProtoCertToAgglayer(req.Certificate)
	if err != nil {
		s.log.Errorf("Error converting certificate: %v", err)
		return nil, grpc.GRPCError{
			Code:    codes.InvalidArgument,
			Message: "Invalid certificate conversion: " + err.Error(),
		}
	}
	params.Certificate = cert
	params.LastL2BlockInCert = req.LastL2BlockInCert
	err = s.validator.ValidateCertificate(ctx, params)
	if err != nil {
		s.log.Errorf("Certificate validation failed: %v", err)
		return nil, grpc.GRPCError{
			Code:    codes.Internal,
			Message: "Certificate validation failed: " + err.Error(),
		}
	}
	signature, err := s.signCertificate(ctx, cert)
	if err != nil {
		s.log.Errorf("Error signing certificate: %v", err)
		return nil, grpc.GRPCError{
			Code:    codes.Internal,
			Message: "Error signing certificate: " + err.Error(),
		}
	}

	s.log.Infof("Certificate validated and signed successfully: %s", cert.Brief())

	return &v1.ValidateCertificateResponse{
		Signature: &v1types.FixedBytes65{
			Value: signature,
		},
	}, nil
}

func (s *ValidatorService) signCertificate(ctx context.Context, cert *agglayertypes.Certificate) ([]byte, error) {
	if s.signer == nil {
		return nil, errSignerNotInitialized
	}
	hashToSign, err := HashCertificateToSign(cert)
	if err != nil {
		return nil, grpc.GRPCError{
			Code:    codes.Internal,
			Message: "Error hashing certificate: " + err.Error(),
		}
	}
	return s.signer.SignHash(ctx, hashToSign)
}

func (s *ValidatorService) signGER(ctx context.Context, ger common.Hash) ([]byte, error) {
	if s.signer == nil {
		return nil, errSignerNotInitialized
	}

	return s.signer.SignHash(ctx, ger)
}
