package aggsender

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/metrics"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/aggsender/validator"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/grpc"
	aggkittypes "github.com/agglayer/aggkit/types"
	signertypes "github.com/agglayer/go_signer/signer/types"
)

var _ types.ValidatorPoller = (*validatorPoller)(nil)

// validatorPoller handles certificate validation by a committee of validators
type validatorPoller struct {
	log aggkitcommon.Logger

	multisigQuerier    types.MultisigQuerier
	storage            db.AggSenderStorage
	proposerSigner     signertypes.Signer
	validatorClientCfg *grpc.ClientConfig
}

// NewValidatorPoller creates a new ValidatorCommittee instance
func NewValidatorPoller(
	log aggkitcommon.Logger,
	storage db.AggSenderStorage,
	proposerSigner signertypes.Signer,
	multisigQuerier types.MultisigQuerier,
	validatorClientCfg *grpc.ClientConfig,
) *validatorPoller {
	return &validatorPoller{
		log:                log,
		storage:            storage,
		proposerSigner:     proposerSigner,
		multisigQuerier:    multisigQuerier,
		validatorClientCfg: validatorClientCfg,
	}
}

// signResult represents the result from a single validator
type signResult struct {
	signature []byte
	err       error
	elapsed   time.Duration
	validator types.CertificateValidateAndSigner
}

func (s *signResult) String() string {
	if s == nil {
		return "<nil>"
	}
	if s.err != nil {
		return fmt.Sprintf("ERROR {validator: %s, err: %v, elapsed: %s}",
			s.validator.String(), s.err, s.elapsed.String())
	}
	return fmt.Sprintf("OK {validator: %s, signature length: %d, elapsed: %s}",
		s.validator.String(), len(s.signature), s.elapsed.String())
}

// PollValidators orchestrates the validation process across all committee members
func (vp *validatorPoller) PollValidators(
	ctx context.Context, req *types.ValidationRequest) (*agglayertypes.Multisig, error) {
	if err := vp.validateRequest(req); err != nil {
		return nil, err
	}

	vp.log.Infof("delegating certificate validation: %s", req.Certificate.Brief())

	validators, threshold, err := vp.getValidators(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get validators: %w", err)
	}

	return vp.executeRequest(ctx, req, threshold, validators)
}

// executeRequest runs the validation and processes the results
func (vp *validatorPoller) executeRequest(
	ctx context.Context,
	req *types.ValidationRequest,
	threshold uint64,
	validators []types.CertificateValidateAndSigner) (*agglayertypes.Multisig, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	resultsCh := vp.executeValidation(ctx, validators, req.Certificate, req.LastL2BlockInCert)
	return vp.processResults(resultsCh, threshold, req.Certificate, cancel)
}

// validateRequest validates the input parameters
func (vp *validatorPoller) validateRequest(req *types.ValidationRequest) error {
	if req == nil {
		return errors.New("validation request cannot be nil")
	}
	if req.Certificate == nil {
		return errors.New("certificate cannot be nil")
	}
	if req.LastL2BlockInCert == 0 {
		return errors.New("last L2 block in certificate cannot be zero")
	}
	return nil
}

// getValidators retrieves the actual multisig committee and creates a set of the validators
// that are going to validate the provided certificate
func (vp *validatorPoller) getValidators(ctx context.Context) ([]types.CertificateValidateAndSigner, uint64, error) {
	committee, err := vp.multisigQuerier.GetMultisigCommittee(ctx, big.NewInt(int64(aggkittypes.Latest)))
	if err != nil {
		return nil, 0, fmt.Errorf("failed to retrieve the latest multisig committee: %w", err)
	}

	validators := make([]types.CertificateValidateAndSigner, 0, committee.Size())
	for i, signer := range committee.Signers() {
		clientCfg := vp.validatorClientCfg.WithURL(signer.URL)
		validator, err := validator.NewRemoteValidator(&clientCfg, vp.storage, signer.Address, uint32(i))
		if err != nil {
			return nil, 0, fmt.Errorf("failed to create a remote validator for committee signer (Address=%s, URL=%s): %w",
				signer.Address, signer.URL, err)
		}

		validators = append(validators, validator)
	}

	if len(validators) == 0 {
		return nil, 0, errors.New("no validators available in the committee")
	}

	firstValidatorAddress := validators[0].Address()
	proposerAddress := vp.proposerSigner.PublicAddress()
	if firstValidatorAddress != proposerAddress {
		return nil, 0, fmt.Errorf("expected proposer %s to be the first member of the validator committee, got %s",
			proposerAddress, firstValidatorAddress)
	}
	return validators, committee.Threshold(), nil
}

// executeValidation runs validation across all validators concurrently
func (vp *validatorPoller) executeValidation(
	ctx context.Context,
	validators []types.CertificateValidateAndSigner,
	cert *agglayertypes.Certificate,
	lastL2BlockInCert uint64,
) <-chan signResult {
	resultsCh := make(chan signResult, len(validators))
	var wg sync.WaitGroup

	start := time.Now()

	for _, validator := range validators {
		wg.Add(1)
		go func(validator types.CertificateValidateAndSigner) {
			defer wg.Done()

			select {
			case <-ctx.Done():
				return
			default:
			}
			timeStart := time.Now()
			signature, err := vp.getSignatureFromValidator(ctx, validator, cert, lastL2BlockInCert)
			resultsCh <- signResult{signature: signature, validator: validator, err: err, elapsed: time.Since(timeStart)}
		}(validator)
	}

	go func() {
		wg.Wait()
		close(resultsCh)
		metrics.ValidateTime(time.Since(start).Seconds())
	}()

	return resultsCh
}

// getSignatureFromValidator gets signature from validator (either self-signing or remote validation)
func (vp *validatorPoller) getSignatureFromValidator(
	ctx context.Context,
	validator types.CertificateValidateAndSigner,
	cert *agglayertypes.Certificate,
	lastL2BlockInCert uint64,
) ([]byte, error) {
	if validator.Address() == vp.proposerSigner.PublicAddress() {
		// Self-signing: member of the committee is also the proposer
		return vp.signCertificateForMultisigAsProposer(ctx, cert)
	}

	// Remote validation: delegate to validator service
	return validator.ValidateAndSignCertificate(ctx, cert, lastL2BlockInCert)
}

// signCertificateForMultisigAsProposer signs the certificate as a proposer
func (vp *validatorPoller) signCertificateForMultisigAsProposer(
	ctx context.Context,
	cert *agglayertypes.Certificate,
) ([]byte, error) {
	hashToSign, err := validator.HashCertificateToSign(cert)
	if err != nil {
		return nil, fmt.Errorf("failed to hash certificate for proposer signing: %w", err)
	}

	return vp.proposerSigner.SignHash(ctx, hashToSign)
}

// processResults collects and validates all results from validators
func (vp *validatorPoller) processResults(
	resultsCh <-chan signResult,
	threshold uint64,
	cert *agglayertypes.Certificate,
	cancel context.CancelFunc,
) (*agglayertypes.Multisig, error) {
	multisig := &agglayertypes.Multisig{
		Signatures: make([]agglayertypes.ECDSAMultisigEntry, 0),
	}
	var errs []error

	for res := range resultsCh {
		if res.err != nil {
			errs = append(errs, res.err)
			vp.log.Errorf("validatorRequest returned an error: %s", res.String())
			metrics.ValidatorError(res.validator.Address())
			continue
		}

		if !vp.isValidSignature(res.signature) {
			err := fmt.Errorf("validatorRequest returned an invalid signature: %s",
				res.String())
			errs = append(errs, err)
			vp.log.Error(err.Error())
			metrics.ValidatorInvalidSignature(res.validator.Address())
			continue
		}
		vp.log.Infof("validatorRequest returned a valid signature: %s", res.String())
		multisig.Signatures = append(multisig.Signatures, agglayertypes.ECDSAMultisigEntry{
			Index:     res.validator.Index(),
			Signature: res.signature,
		})

		if uint64(len(multisig.Signatures)) >= threshold {
			vp.log.Infof("validatorRequest reach expected threshold with %d signatures", len(multisig.Signatures))
			cancel()
			break // signal other goroutines to stop early
		}
	}

	return vp.isThresholdReached(multisig, cert, threshold, errs)
}

// isValidSignature checks if the signature has the correct length
func (vp *validatorPoller) isValidSignature(signature []byte) bool {
	return len(signature) == aggkitcommon.SignatureSize
}

// isThresholdReached checks if enough signatures were collected
func (vp *validatorPoller) isThresholdReached(
	multisig *agglayertypes.Multisig,
	cert *agglayertypes.Certificate,
	threshold uint64,
	errs []error,
) (*agglayertypes.Multisig, error) {
	if uint64(len(multisig.Signatures)) < threshold {
		metrics.MultiSigThresholdNotReached()
		return nil, fmt.Errorf("validatorPoller threshold not reached: %d/%d. Errors: %w",
			len(multisig.Signatures), threshold, errors.Join(errs...))
	}

	vp.log.Infof("validatorPoller certificate validation passed with %d/%d signatures: %s",
		len(multisig.Signatures), threshold, cert.Brief())

	return multisig, nil
}
