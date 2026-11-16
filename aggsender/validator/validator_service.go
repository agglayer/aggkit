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
	"github.com/ethereum/go-ethereum/crypto"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/emptypb"
)

// ValidatorService implements the gRPC server for the AggsenderValidator service.
type ValidatorService struct {
	// Embed the generated server interface to ensure forward compatibility
	v1.UnimplementedAggsenderValidatorServer

	validator      types.CertificateValidator
	agglayerClient agglayer.AggLayerClientCertificateIDQuerier
	signer         signertypes.Signer
	proposerQuery  ProposerAddressProvider
	log            aggkitcommon.Logger
}

func NewValidatorService(
	logger aggkitcommon.Logger,
	validator types.CertificateValidator,
	agglayerClient agglayer.AggLayerClientCertificateIDQuerier,
	signer signertypes.Signer,
	proposerQuery ProposerAddressProvider) *ValidatorService {
	return &ValidatorService{
		log:            logger,
		validator:      validator,
		agglayerClient: agglayerClient,
		signer:         signer,
		proposerQuery:  proposerQuery,
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

	cert, err := agglayergrpc.ConvertProtoCertToAgglayer(req.Certificate)
	if err != nil {
		s.log.Errorf("Error converting certificate: %v", err)
		return nil, grpc.GRPCError{
			Code:    codes.InvalidArgument,
			Message: "Invalid certificate conversion: " + err.Error(),
		}
	}

	// The caller is authenticated before any expensive work: an unknown caller cannot make
	// the validator query agglayer or the chain.
	if err := s.authenticateProposer(ctx, req, cert); err != nil {
		return nil, err
	}

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
		return nil, grpc.GRPCError{
			Code:    codes.Internal,
			Message: "Signer is not initialized",
		}
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

// authenticateProposer checks that the request carries the signature of the chain's
// proposer over the certificate hash, so only the aggsender can ask for a validation.
func (s *ValidatorService) authenticateProposer(
	ctx context.Context, req *v1.ValidateCertificateRequest, cert *agglayertypes.Certificate) error {
	signature := req.GetProposerSignature().GetValue()
	if len(signature) != aggkitcommon.SignatureSize {
		s.log.Warnf("Rejected ValidateCertificate request with a missing or malformed proposer signature "+
			"(length %d) for network %d", len(signature), req.GetCertificate().GetNetworkId())
		return grpc.GRPCError{
			Code:    codes.Unauthenticated,
			Message: "missing or malformed proposer signature",
		}
	}

	if s.proposerQuery == nil {
		return grpc.GRPCError{
			Code:    codes.Internal,
			Message: "proposer address provider is not configured",
		}
	}

	hashToSign, err := HashCertificateToSign(cert)
	if err != nil {
		return grpc.GRPCError{
			Code:    codes.InvalidArgument,
			Message: "Invalid certificate: " + err.Error(),
		}
	}

	publicKey, err := crypto.SigToPub(hashToSign[:], signature)
	if err != nil {
		s.log.Warnf("Rejected ValidateCertificate request with an unusable proposer signature: %v", err)
		return grpc.GRPCError{
			Code:    codes.Unauthenticated,
			Message: "invalid proposer signature",
		}
	}
	recovered := crypto.PubkeyToAddress(*publicKey)

	proposer, err := s.proposerQuery.GetProposerAddress(ctx)
	if err != nil {
		s.log.Errorf("Cannot authenticate the proposer, rejecting the request: %v", err)
		return grpc.GRPCError{
			Code:    codes.Unavailable,
			Message: "cannot resolve the proposer address",
		}
	}

	if recovered != proposer {
		s.log.Warnf("Rejected ValidateCertificate request from %s, the proposer is %s",
			recovered.Hex(), proposer.Hex())
		return grpc.GRPCError{
			Code:    codes.Unauthenticated,
			Message: "the request is not signed by the proposer",
		}
	}

	return nil
}
