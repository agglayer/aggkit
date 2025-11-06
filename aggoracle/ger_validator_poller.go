package aggoracle

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/aggsender/validator"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/grpc"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// gerValidatorPoller handles GER validation by a committee of validators
type gerValidatorPoller struct {
	log aggkitcommon.Logger

	multisigQuerier    types.MultisigQuerier
	validatorClientCfg grpc.ClientConfig
}

// NewGERValidatorPoller creates a new GERValidatorPoller instance
func NewGERValidatorPoller(
	log aggkitcommon.Logger,
	multisigQuerier types.MultisigQuerier,
	validatorClientCfg grpc.ClientConfig,
) GERValidatorPoller {
	return &gerValidatorPoller{
		log:                log,
		multisigQuerier:    multisigQuerier,
		validatorClientCfg: validatorClientCfg,
	}
}

// gerSignResult represents the result from a single validator
type gerSignResult struct {
	signature []byte
	err       error
	elapsed   time.Duration
	validator *GERRemoteValidator
}

func (s *gerSignResult) String() string {
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

// PollValidators orchestrates the GER validation process across all committee members
func (vp *gerValidatorPoller) PollValidators(
	ctx context.Context, ger common.Hash) (*agglayertypes.Multisig, error) {
	if ger == (common.Hash{}) {
		return nil, errors.New("GER cannot be zero")
	}

	vp.log.Infof("delegating GER validation: %s", ger.Hex())

	validators, threshold, err := vp.getValidators(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get validators: %w", err)
	}

	return vp.executeRequest(ctx, ger, threshold, validators)
}

// executeRequest runs the validation and processes the results
func (vp *gerValidatorPoller) executeRequest(
	ctx context.Context,
	ger common.Hash,
	threshold uint64,
	validators []*GERRemoteValidator,
) (*agglayertypes.Multisig, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	resultsCh := vp.executeValidation(ctx, validators, ger)
	return vp.processResults(resultsCh, threshold, ger, cancel)
}

// getValidators retrieves the actual multisig committee and creates a set of the validators
// that are going to validate the provided GER
func (vp *gerValidatorPoller) getValidators(ctx context.Context) ([]*GERRemoteValidator, uint64, error) {
	committee, err := vp.multisigQuerier.GetMultisigCommittee(ctx, big.NewInt(int64(aggkittypes.Latest)))
	if err != nil {
		return nil, 0, fmt.Errorf("failed to retrieve the latest multisig committee: %w", err)
	}

	validators := make([]*GERRemoteValidator, 0, committee.Size())
	for i, signer := range committee.Signers() {
		clientCfg := vp.validatorClientCfg.WithURL(signer.URL)
		validator, err := NewGERRemoteValidator(&clientCfg, signer.Address, uint32(i))
		if err != nil {
			return nil, 0, fmt.Errorf("failed to create a remote validator for committee signer (Address=%s, URL=%s): %w",
				signer.Address, signer.URL, err)
		}

		validators = append(validators, validator)
	}

	if len(validators) == 0 {
		return nil, 0, errors.New("no validators available in the committee")
	}

	return validators, committee.Threshold(), nil
}

// executeValidation runs validation across all validators concurrently
func (vp *gerValidatorPoller) executeValidation(
	ctx context.Context,
	validators []*GERRemoteValidator,
	ger common.Hash,
) <-chan gerSignResult {
	resultsCh := make(chan gerSignResult, len(validators))
	var wg sync.WaitGroup

	start := time.Now()

	for _, validator := range validators {
		wg.Add(1)
		go func(validator *GERRemoteValidator) {
			defer wg.Done()

			select {
			case <-ctx.Done():
				return
			default:
			}
			timeStart := time.Now()
			signature, err := validator.ValidateGER(ctx, ger)
			resultsCh <- gerSignResult{signature: signature, validator: validator, err: err, elapsed: time.Since(timeStart)}
		}(validator)
	}

	go func() {
		wg.Wait()
		close(resultsCh)
		vp.log.Debugf("GER validation completed in %s", time.Since(start).String())
	}()

	return resultsCh
}

// processResults collects and validates all results from validators
func (vp *gerValidatorPoller) processResults(
	resultsCh <-chan gerSignResult,
	threshold uint64,
	ger common.Hash,
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
			continue
		}

		if !vp.isValidSignature(res.signature) {
			err := fmt.Errorf("validatorRequest returned an invalid signature: %s",
				res.String())
			errs = append(errs, err)
			vp.log.Error(err.Error())
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

	return vp.isThresholdReached(multisig, ger, threshold, errs)
}

// isValidSignature checks if the signature has the correct length
func (vp *gerValidatorPoller) isValidSignature(signature []byte) bool {
	return len(signature) == aggkitcommon.SignatureSize
}

// isThresholdReached checks if enough signatures were collected
func (vp *gerValidatorPoller) isThresholdReached(
	multisig *agglayertypes.Multisig,
	ger common.Hash,
	threshold uint64,
	errs []error,
) (*agglayertypes.Multisig, error) {
	if uint64(len(multisig.Signatures)) < threshold {
		return nil, fmt.Errorf("GERValidatorPoller threshold not reached: %d/%d. Errors: %w",
			len(multisig.Signatures), threshold, errors.Join(errs...))
	}

	vp.log.Infof("GERValidatorPoller GER validation passed with %d/%d signatures: %s",
		len(multisig.Signatures), threshold, ger.Hex())

	return multisig, nil
}

// GERRemoteValidator encapsulates the gRPC client and configuration
// required to interact with the AggsenderValidator service for GER validation.
type GERRemoteValidator struct {
	url     string
	address common.Address
	client  *validator.ValidatorClient
	index   uint32
}

// NewGERRemoteValidator initializes a new GERRemoteValidator with the provided gRPC client configuration.
// It returns an error if the gRPC client cannot be created.
func NewGERRemoteValidator(
	cfg *grpc.ClientConfig,
	address common.Address,
	index uint32,
) (*GERRemoteValidator, error) {
	client, err := validator.NewValidatorClient(cfg)
	if err != nil {
		return nil, err
	}

	return &GERRemoteValidator{
		url:     cfg.URL,
		client:  client,
		address: address,
		index:   index,
	}, nil
}

// String returns a string representation of the GERRemoteValidator.
func (v *GERRemoteValidator) String() string {
	return fmt.Sprintf("GERRemoteValidator (URL=%s, Address=%s)", v.url, v.address.String())
}

// Address returns the Ethereum address of the remote validator
func (v *GERRemoteValidator) Address() common.Address {
	return v.address
}

// Index is the index of the signer in the signers list on the Multisig contract
func (v *GERRemoteValidator) Index() uint32 {
	return v.index
}

// ValidateGER sends a GER to the AggsenderValidator service for validation.
func (v *GERRemoteValidator) ValidateGER(
	ctx context.Context,
	ger common.Hash,
) ([]byte, error) {
	signature, err := v.client.ValidateGER(ctx, ger)
	if err != nil {
		return nil, fmt.Errorf("error validating GER on aggsender validator service: %w", err)
	}

	// Validate received signature
	// We do not support ethereum legacy v+27 signatures
	// GER is already a hash, so we use it directly for signature verification
	recoveredPublicKey, err := crypto.SigToPub(ger[:], signature)
	if err != nil {
		return nil, fmt.Errorf("error validating remote validator signature: %w", err)
	}

	recoveredAddress := crypto.PubkeyToAddress(*recoveredPublicKey)
	if v.address != recoveredAddress {
		return nil, fmt.Errorf("error validating remote validator signature, mismatch expected:%v current:%v",
			v.address, recoveredAddress)
	}

	return signature, nil
}
