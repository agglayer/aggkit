package validator

import (
	"context"
	"errors"
	"fmt"
	"strings"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/converters"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
)

var (
	ErrNilCertificate        = errors.New("aggsender-validator nil certificate")
	ErrMetadataNotCompatible = errors.New("aggsender-validator metadata not compatible with the current version")
	errGERNotExists          = errors.New("GER does not exist on L1 GER contract")
)

// CertificateValidator is a object to validate a certificate
type CertificateValidator struct {
	log                   aggkitcommon.Logger
	flow                  types.AggsenderVerifierFlow
	l1InfoTreeDataQuerier types.L1InfoTreeDataQuerier
	certQuerier           types.CertificateQuerier
	lerQuerier            types.LERQuerier

	l1GERQuerier types.L1GERQuerier
}

// NewAggsenderValidator creates a new CertificateValidator instance with the provided dependencies.
// It initializes the validator with a logger, verification flow, and various data queriers
// needed for certificate validation operations.
//
// Parameters:
//   - logger: Logger instance for recording validation operations and errors
//   - flow: AggsenderVerifierFlow that defines the verification workflow
//   - l1InfoTreeDataQuerier: Querier for L1 info tree data
//   - certQuerier: Querier for certificate data
//   - lerQuerier: Querier for LER (Local Exit Root) data
//   - l1GERQuerier: Querier for L1 GER (Global Exit Root) data
//
// Returns:
//   - *CertificateValidator: A new validator instance ready for certificate validation
func NewAggsenderValidator(logger aggkitcommon.Logger,
	flow types.AggsenderVerifierFlow,
	l1InfoTreeDataQuerier types.L1InfoTreeDataQuerier,
	certQuerier types.CertificateQuerier,
	lerQuerier types.LERQuerier,
	l1GERQuerier types.L1GERQuerier) *CertificateValidator {
	return &CertificateValidator{
		log:                   logger,
		flow:                  flow,
		l1InfoTreeDataQuerier: l1InfoTreeDataQuerier,
		certQuerier:           certQuerier,
		lerQuerier:            lerQuerier,
		l1GERQuerier:          l1GERQuerier,
	}
}

// ValidateGER validates the GlobalExitRoot that needs to be injected.
func (a *CertificateValidator) ValidateGER(ctx context.Context, ger common.Hash) error {
	doesExist, err := a.l1GERQuerier.DoesGERExistOnContract(ctx, ger)
	if err != nil {
		return fmt.Errorf("error checking GER existence on contract for GER %s: %w", ger.String(), err)
	}

	if !doesExist {
		return fmt.Errorf("%w: %s", errGERNotExists, ger.String())
	}

	return nil
}

// ValidateCertificate validates the incoming certificate against the previous one.
func (a *CertificateValidator) ValidateCertificate(ctx context.Context, params types.VerifyIncomingRequest) error {
	if params.Certificate == nil {
		return ErrNilCertificate
	}

	previousCertificateToBlock, err := a.certQuerier.GetLastSettledCertificateToBlock(ctx, params.PreviousCertificate)
	if err != nil {
		return fmt.Errorf("failed to get last settled certificate block: %w", err)
	}

	// Validate last L2 block in certificate
	if err := a.validateLastL2BlockInCert(ctx, params, previousCertificateToBlock); err != nil {
		return fmt.Errorf("failed to validate last L2 block in new certificate: %w", err)
	}

	// Between cert must be no gap because if there are could be an attack vector
	if err := a.checkContigousCertificates(params); err != nil {
		return fmt.Errorf("failed CheckContigousCertificates: %w", err)
	}

	// Check if the previous certificate is settled
	if err := a.checkIsPreviousCertificateSettled(params.PreviousCertificate); err != nil {
		return fmt.Errorf("failed CheckCertificatesContents: %w", err)
	}

	// Build corresponding certificate
	preBuildParams, err := a.getCertificatePreBuildParams(ctx, params, previousCertificateToBlock)
	if err != nil {
		return fmt.Errorf("failed to get certificate pre-build params: %w", err)
	}

	a.log.Debugf("aggsender-validator: preBuild: %s", preBuildParams.String())

	// Generate build params
	buildParams, err := a.flow.GenerateBuildParams(ctx, preBuildParams)
	if err != nil {
		return fmt.Errorf("failed flow.GenerateBuildParams: %w", err)
	}

	// Build the certificate
	certificate, err := a.flow.BuildCertificate(ctx, buildParams)
	if err != nil {
		return fmt.Errorf("failed flow.BuildCertificate: %w", err)
	}

	// Compare the incoming certificate with the one generated
	err = a.compareCertificates(params.Certificate, certificate)
	if err != nil {
		return fmt.Errorf("certificate not equal to expected: %w", err)
	}

	// Verify claim proofs
	if err := a.verifyClaimProofs(
		params.Certificate.ImportedBridgeExits,
		buildParams.L1InfoTreeRootFromWhichToProve); err != nil {
		return fmt.Errorf("failed to verify claim proofs: %w", err)
	}

	// Verify AggchainData specific to each flow
	if err := a.flow.VerifyCertificate(
		ctx,
		params.Certificate,
		params.LastL2BlockInCert,
		previousCertificateToBlock); err != nil {
		return fmt.Errorf("failed to verify certificate in flow: %w", err)
	}

	return nil
}

func (a *CertificateValidator) verifyClaimProofs(
	importedBridgeExits []*agglayertypes.ImportedBridgeExit,
	rootFromWhichToProve common.Hash,
) error {
	for _, ibe := range importedBridgeExits {
		if err := ibe.VerifyProofs(rootFromWhichToProve); err != nil {
			return fmt.Errorf("failed to verify imported bridge exit proof: %s. Err: %w", ibe.String(), err)
		}
	}

	return nil
}

// checkIsPreviousCertificateSettled checks if the previous certificate is settled
func (a *CertificateValidator) checkIsPreviousCertificateSettled(
	previousCertificate *agglayertypes.CertificateHeader) error {
	if previousCertificate != nil && !previousCertificate.Status.IsSettled() {
		return fmt.Errorf("previous certificate %s is not settled (status: %s)",
			previousCertificate.ID(), previousCertificate.Status.String())
	}

	return nil
}

// checkContigousCertificates checks if the incoming certificate is contiguous with the previous one.
func (a *CertificateValidator) checkContigousCertificates(params types.VerifyIncomingRequest) error {
	if params.PreviousCertificate == nil {
		return a.checkFirstCertificateBlocks(params)
	}
	if params.PreviousCertificate.Height+1 != params.Certificate.Height {
		return fmt.Errorf("certificate height not contigous, expected: %d, got: %d",
			params.PreviousCertificate.Height+1, params.Certificate.Height)
	}
	if params.Certificate.PrevLocalExitRoot != params.PreviousCertificate.NewLocalExitRoot {
		return fmt.Errorf("certificate PrevLocalExitRoot %s is not equal to previous certificate NewLocalExitRoot %s",
			params.Certificate.PrevLocalExitRoot.String(),
			params.PreviousCertificate.NewLocalExitRoot.String())
	}

	return nil
}

// compareCertificates compares the incoming certificate with the one generated.
func (a *CertificateValidator) compareCertificates(
	incomingCertificate *agglayertypes.Certificate,
	localCertificate *agglayertypes.Certificate) error {
	if incomingCertificate == nil || localCertificate == nil {
		return fmt.Errorf("one of the certificates is nil, incoming: %v, local: %v",
			incomingCertificate, localCertificate)
	}
	diffs := DiffsCertificate(incomingCertificate, localCertificate)
	diffStr := strings.Join(diffs, "\n")
	// This is redudant, but just in case
	if incomingCertificate.CertificateID() != localCertificate.CertificateID() {
		return fmt.Errorf("certificates ids mismatch, incoming: %s, local: %s.\n FullDiff: %s",
			incomingCertificate.CertificateID().Hex(), localCertificate.CertificateID().Hex(), diffStr)
	}
	if len(diffs) > 0 {
		return fmt.Errorf("certificates mismatch. FullDiff: %s", diffStr)
	}
	return nil
}

// checkFirstCertificateBlocks checks that the first certificate blocks are correct
func (a *CertificateValidator) checkFirstCertificateBlocks(params types.VerifyIncomingRequest) error {
	if params.Certificate.Height != 0 {
		// The first certificate must have height 0
		return fmt.Errorf("first certificate must have height 0, but got: %d",
			params.Certificate.Height)
	}
	startLER, err := a.lerQuerier.GetLastLocalExitRoot()
	if err != nil {
		return fmt.Errorf("failed to get start LER: %w", err)
	}
	if params.Certificate.PrevLocalExitRoot != startLER {
		return fmt.Errorf("first certificate must have correct starting PrevLocalExitRoot: %s, but got: %s",
			startLER.String(),
			params.Certificate.PrevLocalExitRoot.String())
	}
	return nil
}

// getCertificatePreBuildParams prepares the parameters needed to build a certificate based
// on incomming certificate
func (a *CertificateValidator) getCertificatePreBuildParams(
	ctx context.Context,
	params types.VerifyIncomingRequest,
	previousCertToBlock uint64) (*types.CertificatePreBuildParams, error) {
	if params.Certificate == nil {
		return nil, fmt.Errorf("preBuildParams. Err: %w", ErrNilCertificate)
	}
	lastSentCertificate, err := converters.ConvertAgglayerCertHeaderToAggsender(params.PreviousCertificate)
	if err != nil {
		return nil, fmt.Errorf("preBuildParams. failed to convert previous certificate to Aggsender format: %w", err)
	}

	blockRange := aggkitcommon.NewBlockRange(previousCertToBlock+1, params.LastL2BlockInCert)
	certType := a.certQuerier.CalculateCertificateType(params.Certificate, params.LastL2BlockInCert)

	l1InfoRoot, err := a.l1InfoTreeDataQuerier.GetL1InfoRootByLeafIndex(ctx, params.Certificate.L1InfoTreeLeafCount-1)
	if err != nil {
		return nil, fmt.Errorf("preBuildParams. failed to get L1 Info tree root by leaf count %d: %w",
			params.Certificate.L1InfoTreeLeafCount, err)
	}

	return &types.CertificatePreBuildParams{
		BlockRange:          blockRange,
		CertificateType:     certType,
		LastSentCertificate: lastSentCertificate,
		L1InfoTreeToProve: &types.CertificateL1InfoTreeData{
			L1InfoTreeRootToProve: l1InfoRoot.Hash,
			L1InfoTreeLeafCount:   params.Certificate.L1InfoTreeLeafCount,
		},
	}, nil
}

// validateLastL2BlockInCert checks that the provided last L2 block in the certificate by the proposer
// is greater or equal to the blocks we see in the new certificate
func (a *CertificateValidator) validateLastL2BlockInCert(
	ctx context.Context,
	req types.VerifyIncomingRequest,
	lastSettledBlock uint64) error {
	if req.LastL2BlockInCert <= lastSettledBlock {
		return fmt.Errorf("the last L2 block in the certificate (%d) must be greater than the last settled block (%d)",
			req.LastL2BlockInCert, lastSettledBlock)
	}

	newCertToBlock, err := a.certQuerier.GetNewCertificateToBlock(ctx, req.Certificate)
	if err != nil {
		return fmt.Errorf("failed to get new certificate to block: %w", err)
	}

	if newCertToBlock > req.LastL2BlockInCert {
		return fmt.Errorf("new certificate to block %d must be less than or equal to last L2 block "+
			"provided by the proposer %d", newCertToBlock, req.LastL2BlockInCert)
	}

	return nil
}
