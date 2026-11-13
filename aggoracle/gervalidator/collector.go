package gervalidator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
)

// Result is the outcome of a successful collection of signatures
type Result struct {
	// Signatures are exactly threshold signatures of 65 bytes, concatenated and ordered by signer address in
	// strictly ascending order: the signatures argument of AggOracleCommittee.injectGER
	Signatures []byte
	// Signers are the addresses that signed, in the same order as the signatures
	Signers []common.Address
}

// Collector asks the GER validators, in parallel, to sign a GER, and stops as soon as it has enough
// valid signatures. It follows the same pattern as the validator poller of the AggSender.
type Collector struct {
	logger         *log.Logger
	client         Client
	requestTimeout time.Duration
}

// NewCollector creates a collector. requestTimeout limits each request to a validator (0 means no limit)
func NewCollector(logger *log.Logger, client Client, requestTimeout time.Duration) *Collector {
	return &Collector{logger: logger, client: client, requestTimeout: requestTimeout}
}

// outcome is the answer of a validator: a valid signature or the reason there isn't one
type outcome struct {
	validator Validator
	signed    *signedGER
	err       error
}

// Collect asks all the validators to sign the GER and returns exactly threshold valid signatures.
// A signature is valid if it is correctly formed (65 bytes, v 27/28, low s) and the signer recovered from it is
// the address of the validator that provided it. If the threshold can't be reached it returns a
// *ThresholdNotReachedError (errors.Is(err, ErrThresholdNotReached)) that tells if retrying can help.
func (c *Collector) Collect(
	ctx context.Context,
	ger common.Hash,
	validators []Validator,
	threshold int,
) (*Result, error) {
	if ger == (common.Hash{}) {
		return nil, errors.New("the GER to sign can't be zero")
	}
	if threshold < 1 {
		return nil, fmt.Errorf("invalid threshold %d: it must be at least 1", threshold)
	}
	unique := uniqueValidators(validators)
	if threshold > len(unique) {
		return nil, fmt.Errorf("threshold (%d) is greater than the number of validators (%d)", threshold, len(unique))
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // stops the requests that are still running once the threshold is reached

	start := time.Now()
	c.logger.Infof("requesting the signature of GER %s to %d validators (threshold %d): %s",
		ger, len(unique), threshold, validatorList(unique))
	answers := make(map[common.Address]string, len(unique))
	defer func() {
		c.logger.Infof("GER %s: answers of the validators in %s: %s",
			ger, time.Since(start).Round(time.Millisecond), describeAnswers(unique, answers))
	}()

	results := make(chan outcome, len(unique)) // buffered: the goroutines never block, even if we returned
	for _, validator := range unique {
		go c.askValidator(ctx, ger, validator, results)
	}

	var (
		valid    = make([]signedGER, 0, threshold)
		failures []ValidatorFailure
	)
	for range unique {
		select {
		case out := <-results:
			if out.err != nil {
				failure := c.logFailure(ger, out)
				failures = append(failures, failure)
				answers[out.validator.Address] = fmt.Sprintf("%s (%s)", failure.Kind, out.err)
				continue
			}
			valid = append(valid, *out.signed)
			answers[out.validator.Address] = "signed"
			c.logger.Debugf("validator %s signed GER %s: %d signatures of threshold %d",
				out.validator.Address, ger, len(valid), threshold)
			if len(valid) == threshold {
				signatures, signers, err := packSignatures(valid)
				if err != nil {
					return nil, fmt.Errorf("failed to pack the signatures of GER %s: %w", ger, err)
				}
				return &Result{Signatures: signatures, Signers: signers}, nil
			}
		case <-ctx.Done():
			return nil, fmt.Errorf("collecting the signatures of GER %s: %w", ger, ctx.Err())
		}
	}

	// If the caller cancelled, the failures are consequence of it: report the cancellation, not the threshold
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("collecting the signatures of GER %s: %w", ger, err)
	}

	sort.Slice(failures, func(i, j int) bool {
		return bytes.Compare(failures[i].Validator.Bytes(), failures[j].Validator.Bytes()) < 0
	})
	return nil, &ThresholdNotReachedError{GER: ger, Threshold: threshold, Valid: len(valid), Failures: failures}
}

// askValidator asks one validator for its signature and sends the outcome to results. A panic is
// converted into a failure of that validator, so it never affects the others.
func (c *Collector) askValidator(ctx context.Context, ger common.Hash, validator Validator, results chan<- outcome) {
	out := outcome{validator: validator}
	defer func() {
		if r := recover(); r != nil {
			out = outcome{validator: validator, err: fmt.Errorf("%w: panic: %v", ErrInvalidResponse, r)}
		}
		results <- out
	}()
	out = c.requestSignature(ctx, ger, validator)
}

func (c *Collector) requestSignature(ctx context.Context, ger common.Hash, validator Validator) outcome {
	if validator.URL == "" {
		return outcome{validator: validator, err: ErrNoURL}
	}

	if c.requestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.requestTimeout)
		defer cancel()
	}

	c.logger.Debugf("asking validator %s (%s) to sign GER %s", validator.Address, validator.URL, ger)
	resp, err := c.client.SignGER(ctx, validator.URL, ger)
	if err != nil {
		return outcome{validator: validator, err: err}
	}
	if resp == nil {
		return outcome{validator: validator, err: fmt.Errorf("%w: empty response", ErrInvalidResponse)}
	}
	if resp.GER != ger {
		return outcome{validator: validator, err: fmt.Errorf("%w: it signed GER %s instead of %s",
			ErrInvalidResponse, resp.GER, ger)}
	}

	signature, signer, err := RecoverSigner(ger, resp.Signature)
	if err != nil {
		return outcome{validator: validator, err: err}
	}
	if signer != validator.Address {
		return outcome{validator: validator, err: fmt.Errorf("%w: the signature is from %s, not from validator %s",
			ErrInvalidResponse, signer, validator.Address)}
	}
	return outcome{validator: validator, signed: &signedGER{signer: signer, signature: signature}}
}

// logFailure logs why a validator didn't provide a signature and returns it as a ValidatorFailure
func (c *Collector) logFailure(ger common.Hash, out outcome) ValidatorFailure {
	failure := ValidatorFailure{Validator: out.validator.Address, Kind: ClassifyFailure(out.err), Err: out.err}
	switch failure.Kind {
	case FailureInvalid:
		c.logger.Warnf("validator %s gave no valid signature for GER %s: %s", out.validator.Address, ger, out.err)
	case FailureGERRejected:
		c.logger.Infof("validator %s rejected GER %s: %s", out.validator.Address, ger, out.err)
	case FailureTransient:
		c.logger.Debugf("validator %s gave no signature for GER %s yet: %s", out.validator.Address, ger, out.err)
	}
	return failure
}

// validatorList describes the validators: address and URL
func validatorList(validators []Validator) string {
	items := make([]string, len(validators))
	for i, validator := range validators {
		url := validator.URL
		if url == "" {
			url = "no URL"
		}
		items[i] = fmt.Sprintf("%s (%s)", validator.Address, url)
	}
	return strings.Join(items, ", ")
}

// describeAnswers describes the answer of each validator, in the order they were asked. The ones without an
// answer were not waited for: the threshold was reached before (or the collection was cancelled)
func describeAnswers(validators []Validator, answers map[common.Address]string) string {
	items := make([]string, len(validators))
	for i, validator := range validators {
		answer, ok := answers[validator.Address]
		if !ok {
			answer = "not waited for"
		}
		items[i] = fmt.Sprintf("%s: %s", validator.Address, answer)
	}
	return strings.Join(items, "; ")
}

// uniqueValidators removes the validators that repeat an address, keeping the first one
func uniqueValidators(validators []Validator) []Validator {
	seen := make(map[common.Address]struct{}, len(validators))
	unique := make([]Validator, 0, len(validators))
	for _, validator := range validators {
		if _, ok := seen[validator.Address]; ok {
			continue
		}
		seen[validator.Address] = struct{}{}
		unique = append(unique, validator)
	}
	return unique
}
