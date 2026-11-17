package grpc_test

import (
	"context"
	"net"
	"testing"
	"time"

	v1nodetypes "buf.build/gen/go/agglayer/agglayer/protocolbuffers/go/agglayer/node/types/v1"
	v1types "buf.build/gen/go/agglayer/interop/protocolbuffers/go/agglayer/interop/types/v1"
	agglayergrpc "github.com/agglayer/aggkit/agglayer/grpc"
	validatorv1 "github.com/agglayer/aggkit/aggsender/validator/proto/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

// survivalServer mirrors the conversion and error mapping of the validator's
// ValidateCertificate handler: it converts the incoming certificate and answers an
// invalid argument when the conversion fails. It covers ConvertProtoCertToAgglayer and the
// gRPC error mapping, not the real validator service.
type survivalServer struct {
	validatorv1.UnimplementedAggsenderValidatorServer
}

func (s *survivalServer) HealthCheck(
	context.Context, *emptypb.Empty,
) (*validatorv1.HealthCheckResponse, error) {
	return &validatorv1.HealthCheckResponse{Status: "ok"}, nil
}

func (s *survivalServer) ValidateCertificate(
	_ context.Context, req *validatorv1.ValidateCertificateRequest,
) (*validatorv1.ValidateCertificateResponse, error) {
	if req == nil || req.Certificate == nil {
		return nil, status.Error(codes.NotFound, "required a certificate")
	}
	if _, err := agglayergrpc.ConvertProtoCertToAgglayer(req.Certificate); err != nil {
		return nil, status.Error(codes.InvalidArgument, "Invalid certificate conversion: "+err.Error())
	}
	return &validatorv1.ValidateCertificateResponse{}, nil
}

// TestMalformedValidateCertificateRequestDoesNotKillServer sends the 88-byte request that
// used to panic the validator over a real gRPC connection and checks that the call is
// rejected and the server keeps serving.
func TestMalformedValidateCertificateRequestDoesNotKillServer(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	server := grpc.NewServer()
	validatorv1.RegisterAggsenderValidatorServer(server, &survivalServer{})
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	client := validatorv1.NewAggsenderValidatorClient(conn)

	// The certificate from the report: both exit roots, one L1 leaf and a multisig entry
	// that carries an index but no signature. On the wire the signature field is omitted
	// and decodes to nil.
	request := &validatorv1.ValidateCertificateRequest{
		Certificate: &v1nodetypes.Certificate{
			NetworkId:           1,
			Height:              1,
			PrevLocalExitRoot:   &v1types.FixedBytes32{Value: make([]byte, 32)},
			NewLocalExitRoot:    &v1types.FixedBytes32{Value: make([]byte, 32)},
			L1InfoTreeLeafCount: proto.Uint32(1),
			AggchainData: &v1types.AggchainData{
				Data: &v1types.AggchainData_Multisig{
					Multisig: &v1types.Multisig{
						Data: &v1types.Multisig_Ecdsa{
							Ecdsa: &v1types.ECDSAMultisig{
								Signatures: []*v1types.ECDSAMultisig_ECDSAMultisigEntry{{Index: 0}},
							},
						},
					},
				},
			},
		},
	}

	payload, err := proto.Marshal(request)
	require.NoError(t, err)
	require.Len(t, payload, 88, "the request from the report serializes to 88 bytes")

	// Send the bytes as received, so the test does not depend on the in-memory struct.
	var wireRequest validatorv1.ValidateCertificateRequest
	require.NoError(t, proto.Unmarshal(payload, &wireRequest))

	_, err = client.ValidateCertificate(ctx, &wireRequest)
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	// The server is still up: the panic is gone and the process did not exit.
	health, err := client.HealthCheck(ctx, &emptypb.Empty{})
	require.NoError(t, err)
	require.Equal(t, "ok", health.GetStatus())
}

// FuzzConvertProtoCertToAgglayer feeds arbitrary bytes to the conversion and checks that a
// certificate decoded from them can never panic it.
func FuzzConvertProtoCertToAgglayer(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0x1a, 0x22, 0x0a, 0x20})                         // a certificate with no aggchain data
	f.Add([]byte{0x42, 0x06, 0x1a, 0x04, 0x0a, 0x02, 0x1a, 0x00}) // a multisig entry without signature
	f.Fuzz(func(t *testing.T, data []byte) {
		var cert v1nodetypes.Certificate
		if err := proto.Unmarshal(data, &cert); err != nil {
			t.Skip()
		}
		require.NotPanics(t, func() {
			_, _ = agglayergrpc.ConvertProtoCertToAgglayer(&cert)
		})
	})
}
