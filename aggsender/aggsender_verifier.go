package aggsender

import (
	"context"
	"errors"
	"fmt"

	jRPC "github.com/0xPolygon/cdk-rpc/rpc"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/config"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

var (
	ErrNotImplemented        = errors.New("aggsender-verifier not implemented")
	ErrNilCertificate        = errors.New("aggsender-verfivier nil certificate")
	ErrMetadataNotCompatible = errors.New("aggsender-verifier metadata not compatible with the current version")
)

type AggsenderVerifier struct {
	log aggkitcommon.Logger
}

func NewAggsenderVerifier(ctx context.Context,
	logger *log.Logger,
	cfg config.Config,
	l1InfoTreeSyncer *l1infotreesync.L1InfoTreeSync,
	l2Syncer types.L2BridgeSyncer,
	l1Client aggkittypes.BaseEthereumClienter,
	l2Client aggkittypes.BaseEthereumClienter,
	rollupDataQuerier types.RollupDataQuerier) (*AggsenderVerifier, error) {
	return nil, nil
}
func (a *AggsenderVerifier) Start(ctx context.Context) {
}

func (a *AggsenderVerifier) GetRPCServices() []jRPC.Service {
	return []jRPC.Service{}
}

type VerifyIncommingRequests struct {
	certificate         *agglayertypes.Certificate
	previousCertificate *agglayertypes.CertificateHeader
}

func (a *AggsenderVerifier) VerifyCertificate(params VerifyIncommingRequests) error {
	if params.certificate == nil {
		return ErrNilCertificate
	}
	// Between cert must be no gap because if there are could be a attack vector
	if err := a.CheckContigousCertificates(params); err != nil {
		return err
	}
	return ErrNotImplemented
}

func (a *AggsenderVerifier) CheckContigousCertificates(params VerifyIncommingRequests) error {
	if params.certificate == nil {
		return ErrNilCertificate
	}
	if params.previousCertificate == nil {
		// TODO: Check that the first certificate start at the begining of genesis
		return a.CheckFirstCerficateBlocks(params)
	}
	// certificate != nil && previousCertificate != nil
	currentBlockRange, err := getBlockRangeFromMetadata(params.certificate.Metadata)
	if err != nil {
		return fmt.Errorf("failed to get block range from certificate metadata: %w", err)
	}
	if currentBlockRange.IsEmpty() {
		return fmt.Errorf("certificate block range %s have no block! , certificate: %s",
			currentBlockRange.String(),
			params.certificate.ID())
	}
	previousBlockRange, err := getBlockRangeFromMetadata(params.previousCertificate.Metadata)
	if err != nil {
		return fmt.Errorf("failed to get block range from previous certificate metadata: %w", err)
	}
	if previousBlockRange.IsNextContigousBlock(currentBlockRange) {
		// No more check required is just the next one
		return nil
	}
	// TODO: Check logic for gaps (no data beetween certificates)
	return nil
}

// CheckFirstCerficateBlocks checks that the first certificate blocks are correct
// so it's starts from genesis?!?
func (a *AggsenderVerifier) CheckFirstCerficateBlocks(params VerifyIncommingRequests) error {
	// TODO: fill it
	return ErrNotImplemented
}

func getBlockRangeFromMetadata(metadata common.Hash) (types.BlockRange, error) {
	emptyBlockRange := types.BlockRange{}
	metadataUnmarshal, err := types.NewCertificateMetadataFromHash(metadata)
	if err != nil {
		return emptyBlockRange, ErrMetadataNotCompatible
	}
	return metadataUnmarshal.BlockRange()
}
