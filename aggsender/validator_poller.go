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
	validator types.CertificateValidateAndSigner
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
	threshold *big.Int,
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
func (vp *validatorPoller) getValidators(ctx context.Context) ([]types.CertificateValidateAndSigner, *big.Int, error) {
	committee, err := vp.multisigQuerier.GetMultisigCommittee(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to retrieve the latest multisig committee: %w", err)
	}

	validators := make([]types.CertificateValidateAndSigner, 0, committee.Size())
	for i, signer := range committee.Signers() {
		clientCfg := vp.validatorClientCfg.WithURL(signer.URL)
		validator, err := validator.NewRemoteValidator(&clientCfg, vp.storage, signer.Address, uint32(i))
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create a remote validator for committee signer (Address=%s, URL=%s): %w",
				signer.Address, signer.URL, err)
		}

		validators = append(validators, validator)
	}

	if len(validators) == 0 {
		return nil, nil, errors.New("no validators available in the committee")
	}

	firstValidatorAddress := validators[0].Address()
	proposerAddress := vp.proposerSigner.PublicAddress()
	if firstValidatorAddress != proposerAddress {
		return nil, nil, fmt.Errorf("expected proposer %s to be the first member of the validator committee, got %s",
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

			signature, err := vp.getSignatureFromValidator(ctx, validator, cert, lastL2BlockInCert)
			if err != nil {
				vp.log.Errorf("validator %s failed to validate the certificate: %v", validator.String(), err)
				resultsCh <- signResult{err: err, validator: validator}
				return
			}

			resultsCh <- signResult{signature: signature, validator: validator}
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
	threshold *big.Int,
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

			metrics.ValidatorError(res.validator.Address())

			continue
		}

		if !vp.isValidSignature(res.signature) {
			err := fmt.Errorf("validator %s returned an invalid signature with length %d",
				res.validator.String(), len(res.signature))
			errs = append(errs, err)

			vp.log.Error(err.Error())

			metrics.ValidatorInvalidSignature(res.validator.Address())

			continue
		}

		multisig.Signatures = append(multisig.Signatures, agglayertypes.ECDSAMultisigEntry{
			Index:     res.validator.Index(),
			Signature: res.signature,
		})

		if big.NewInt(int64(len(multisig.Signatures))).Cmp(threshold) >= 0 {
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
	threshold *big.Int,
	errs []error,
) (*agglayertypes.Multisig, error) {
	if big.NewInt(int64(len(multisig.Signatures))).Cmp(threshold) < 0 {
		metrics.MultiSigThresholdNotReached()
		return nil, fmt.Errorf("threshold not reached: %d/%d. Errors: %w",
			len(multisig.Signatures), threshold, errors.Join(errs...))
	}

	vp.log.Infof("certificate validation passed with %d/%d signatures: %s",
		len(multisig.Signatures), threshold, cert.Brief())

	return multisig, nil
}
