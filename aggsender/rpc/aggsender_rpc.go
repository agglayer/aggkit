package aggsenderrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/0xPolygon/cdk-rpc/rpc"
	"github.com/agglayer/aggkit/agglayer"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/log"
	ethCommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type AggsenderStorer interface {
	GetCertificateByHeight(height uint64) (*types.Certificate, error)
	GetLastSentCertificate() (*types.Certificate, error)
	GetCertificateBridgeExits(height uint64) ([]*agglayertypes.BridgeExit, error)
	SaveLastSentCertificate(ctx context.Context, certificate types.Certificate) error
}

type AggsenderInterface interface {
	Info() types.AggsenderInfo
	ForceTriggerCertificate()
}

// DebugSendCertificateRequest is the request body for the debug send certificate endpoint.
type DebugSendCertificateRequest struct {
	Certificate agglayertypes.Certificate `json:"certificate"`
	Signature   []byte                   `json:"signature"` // 65-byte Ethereum signature
}

// AggsenderRPC is the RPC interface for the aggsender
type AggsenderRPC struct {
	logger           *log.Logger
	storage          AggsenderStorer
	aggsender        AggsenderInterface
	enableDebug      bool
	debugAuthAddress ethCommon.Address
	agglayerClient   agglayer.AgglayerClientInterface
}

// NewAggsenderRPC creates a new AggsenderRPC instance.
func NewAggsenderRPC(
	logger *log.Logger,
	storage AggsenderStorer,
	aggsender AggsenderInterface,
	enableDebug bool,
	debugAuthAddress ethCommon.Address,
	agglayerClient agglayer.AgglayerClientInterface,
) *AggsenderRPC {
	return &AggsenderRPC{
		logger:           logger,
		storage:          storage,
		aggsender:        aggsender,
		enableDebug:      enableDebug,
		debugAuthAddress: debugAuthAddress,
		agglayerClient:   agglayerClient,
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
	exits, err := b.storage.GetCertificateBridgeExits(resolvedHeight)
	if err != nil {
		return nil, rpc.NewRPCError(rpc.DefaultErrorCode,
			fmt.Sprintf("error getting certificate bridge exits at height %d: %v", resolvedHeight, err))
	}
	if exits == nil {
		return nil, rpc.NewRPCError(rpc.NotFoundErrorCode,
			fmt.Sprintf("certificate not found at height %d", resolvedHeight))
	}
	return exits, nil
}

// DebugSendCertificate sends an arbitrary certificate to AggLayer (test-only endpoint).
// Requires EnableDebugSendCertificate=true in config and a valid Ethereum signature.
func (b *AggsenderRPC) DebugSendCertificate(signedRequest DebugSendCertificateRequest) (interface{}, rpc.Error) {
	if !b.enableDebug {
		return nil, rpc.NewRPCError(rpc.DefaultErrorCode, "debug send certificate endpoint is disabled")
	}
	hash, err := HashCertificateForDebugAuth(&signedRequest.Certificate)
	if err != nil {
		return nil, rpc.NewRPCError(rpc.DefaultErrorCode, fmt.Sprintf("error hashing certificate: %v", err))
	}
	pubKey, err := crypto.SigToPub(hash.Bytes(), signedRequest.Signature)
	if err != nil {
		return nil, rpc.NewRPCError(rpc.DefaultErrorCode, fmt.Sprintf("error recovering signer: %v", err))
	}
	signer := crypto.PubkeyToAddress(*pubKey)
	if signer != b.debugAuthAddress {
		return nil, rpc.NewRPCError(rpc.DefaultErrorCode,
			fmt.Sprintf("unauthorized: signer %s does not match auth address %s", signer.Hex(), b.debugAuthAddress.Hex()))
	}
	ctx := context.Background()
	certHash, err := b.agglayerClient.SendCertificate(ctx, &signedRequest.Certificate)
	if err != nil {
		return nil, rpc.NewRPCError(rpc.DefaultErrorCode, fmt.Sprintf("error sending certificate to AggLayer: %v", err))
	}
	// Store in DB so getCertificateBridgeExits can later retrieve the bridge exits
	jsonCert, err := json.Marshal(&signedRequest.Certificate)
	if err != nil {
		b.logger.Warnf("debug: failed to marshal certificate for storage: %v", err)
	} else {
		jsonCertStr := string(jsonCert)
		now := uint32(time.Now().Unix())
		cert := types.Certificate{
			Header: &types.CertificateHeader{
				Height:           signedRequest.Certificate.Height,
				CertificateID:    signedRequest.Certificate.CertificateID(),
				NewLocalExitRoot: signedRequest.Certificate.NewLocalExitRoot,
				Status:           agglayertypes.Pending,
				CreatedAt:        now,
				UpdatedAt:        now,
				CertSource:       types.CertificateSourceLocal,
			},
			SignedCertificate: &jsonCertStr,
		}
		if err := b.storage.SaveLastSentCertificate(ctx, cert); err != nil {
			b.logger.Warnf("debug: failed to store certificate in DB: %v", err)
		}
	}
	return certHash, nil
}
