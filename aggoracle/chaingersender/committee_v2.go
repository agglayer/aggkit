package chaingersender

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/agglayer/aggkit/aggoracle/contracts/aggoraclecommitteev2"
	"github.com/agglayer/aggkit/aggoracle/gervalidator"
	"github.com/agglayer/aggkit/aggoracle/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

const (
	injectGERFuncName = "injectGER"

	// maxValidators is the maximum number of validators of the contract (uint8 indexes)
	maxValidators = 255

	// selectorLength is the length of the selector of a custom error
	selectorLength = 4
)

var (
	// ErrGERPendingTimeout is returned when the GER is not injected before PendingGERTimeout
	ErrGERPendingTimeout = errors.New("the GER was pending for too long")
	// ErrInjectAttemptsExhausted is returned when the GER couldn't be injected in MaxInjectAttempts attempts
	ErrInjectAttemptsExhausted = errors.New("the GER couldn't be injected")
)

// CommitteeV2Config is the configuration of the CommitteeV2Sender
type CommitteeV2Config struct {
	// CollectRetryInterval is the time to wait before the next attempt: when the threshold of signatures was
	// not reached but it still can be, and after an attempt that failed
	CollectRetryInterval time.Duration
	// MaxInjectAttempts is how many attempts fail (the committee changed after collecting the signatures, or the
	// transaction failed) before giving up with the GER. Waiting for the validators is not an attempt
	MaxInjectAttempts int
	// PendingGERTimeout is how long a GER is kept pending: 0 means until it is injected
	PendingGERTimeout time.Duration
	// ValidatorURLOverrides replaces the URL registered on-chain of the validators (or sets it, if empty)
	ValidatorURLOverrides map[common.Address]string
}

// attemptOutcome is the result of an attempt to inject a GER
type attemptOutcome int

const (
	// outcomeDone: there is nothing left to do with the GER (injected, or it already was)
	outcomeDone attemptOutcome = iota
	// outcomePending: the validators are not ready to sign the GER, but they can be later. Not an error
	outcomePending
	// outcomeRetry: the attempt failed in a way that collecting the signatures again can solve: the committee
	// changed, or the transaction failed. It comes with the error
	outcomeRetry
	// outcomeFail: the GER can't be injected (or the processing was cancelled). It comes with the error
	outcomeFail
)

// verification is the result of verifying the signatures with the contract
type verification int

const (
	verificationValid verification = iota
	verificationAlreadyInjected
	verificationCommitteeChanged
	verificationFailed
)

// CommitteeV2Sender sends the GERs to the AggOracleCommittee v2: it collects the signatures of the
// GER validators and injects the GER with a single transaction, sent by the proposer
// (the account of the EthTxManager).
//
// A GER that is not final yet for the validators is kept pending (and the new GERs are skipped, as they
// would restart the collection) until it is injected, it can't be injected (the validators reject it) or
// PendingGERTimeout expires. Skipping the intermediate GERs is safe: a later GER includes the exits of the
// previous ones.
type CommitteeV2Sender struct {
	logger *log.Logger
	cfg    CommitteeV2Config

	l2GERManager types.L2GERManagerContract

	committee     types.AggOracleCommitteeV2Contract
	committeeAddr common.Address
	committeeABI  *abi.ABI

	collector types.GERSignatureCollector
	submitter *txSubmitter
	proposer  common.Address

	// processing makes only one GER to be processed at the same time
	processing sync.Mutex
}

// NewCommitteeV2Sender creates the sender and validates, at startup, that it can work with the committee:
//   - the contract is a v2 committee of the L2 GER manager, and it is its globalExitRootUpdater
//   - the account of the EthTxManager is the proposer of the committee
//   - the committee has, at least, threshold validators that can be reached (URL registered or overridden)
func NewCommitteeV2Sender(
	ctx context.Context,
	logger *log.Logger,
	evmCfg EVMConfig,
	cfg CommitteeV2Config,
	committeeAddr common.Address,
	l2GERManager types.L2GERManagerContract,
	committee types.AggOracleCommitteeV2Contract,
	collector types.GERSignatureCollector,
	ethTxMan types.EthTxManager,
) (*CommitteeV2Sender, error) {
	if cfg.MaxInjectAttempts < 1 {
		return nil, fmt.Errorf("MaxInjectAttempts must be at least 1, got %d", cfg.MaxInjectAttempts)
	}
	if cfg.CollectRetryInterval <= 0 {
		return nil, fmt.Errorf("CollectRetryInterval must be greater than 0, got %s", cfg.CollectRetryInterval)
	}

	committeeABI, err := aggoraclecommitteev2.AggOracleCommitteeV2MetaData.GetAbi()
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve the AggOracleCommittee v2 ABI: %w", err)
	}

	sender := &CommitteeV2Sender{
		logger:        logger,
		cfg:           cfg,
		l2GERManager:  l2GERManager,
		committee:     committee,
		committeeAddr: committeeAddr,
		committeeABI:  committeeABI,
		collector:     collector,
		proposer:      ethTxMan.From(),
		submitter: &txSubmitter{
			logger:              logger,
			ethTxMan:            ethTxMan,
			gasOffset:           evmCfg.GasOffset,
			waitPeriodMonitorTx: evmCfg.WaitPeriodMonitorTx.Duration,
		},
	}

	if err := sender.validate(ctx, evmCfg.GlobalExitRootL2Addr); err != nil {
		return nil, err
	}
	logger.Infof("CommitteeV2Sender initialized: committee %s, proposer %s", committeeAddr, sender.proposer)

	return sender, nil
}

// validate checks the committee can be used by this sender
func (s *CommitteeV2Sender) validate(ctx context.Context, l2GERManagerAddr common.Address) error {
	opts := &bind.CallOpts{Context: ctx}

	version, err := s.committee.Version(opts)
	if err != nil {
		return fmt.Errorf("failed to read the version of the AggOracleCommittee %s: %w", s.committeeAddr, err)
	}
	if !strings.HasPrefix(version, "v2.") {
		return fmt.Errorf("the contract %s is not an AggOracleCommittee v2: its version is %q", s.committeeAddr, version)
	}

	gerManager, err := s.committee.AgglayerGERL2(opts)
	if err != nil {
		return fmt.Errorf("failed to read agglayerGERL2 from the AggOracleCommittee %s: %w", s.committeeAddr, err)
	}
	if gerManager != l2GERManagerAddr {
		return fmt.Errorf("the AggOracleCommittee %s works with the L2 GER manager %s, but %s is configured",
			s.committeeAddr, gerManager, l2GERManagerAddr)
	}

	updater, err := s.l2GERManager.GlobalExitRootUpdater(opts)
	if err != nil {
		return fmt.Errorf("failed to read the GER updater from the L2 GER manager %s: %w", l2GERManagerAddr, err)
	}
	if updater != s.committeeAddr {
		return fmt.Errorf("the AggOracleCommittee %s is not the GER updater of the L2 GER manager %s (it is %s)",
			s.committeeAddr, l2GERManagerAddr, updater)
	}

	proposer, err := s.committee.Proposer(opts)
	if err != nil {
		return fmt.Errorf("failed to read the proposer of the AggOracleCommittee %s: %w", s.committeeAddr, err)
	}
	if proposer != s.proposer {
		return fmt.Errorf("the account of the EthTxManager (%s) is not the proposer of the AggOracleCommittee %s (%s): "+
			"only the proposer can inject GERs", s.proposer, s.committeeAddr, proposer)
	}

	validators, threshold, err := s.committeeState(ctx)
	if err != nil {
		return err
	}
	reachable := 0
	for _, validator := range validators {
		if validator.URL != "" {
			reachable++
		}
	}
	if reachable < threshold {
		return fmt.Errorf("the AggOracleCommittee requires %d signatures but only %d of its %d validators have a URL: "+
			"set the missing ones in AggOracle.CommitteeV2.ValidatorURLOverrides", threshold, reachable, len(validators))
	}
	return nil
}

// committeeState reads the validators (with the configured URL overrides applied) and the threshold
func (s *CommitteeV2Sender) committeeState(ctx context.Context) ([]gervalidator.Validator, int, error) {
	opts := &bind.CallOpts{Context: ctx}

	rawThreshold, err := s.committee.Threshold(opts)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to read the threshold of the AggOracleCommittee: %w", err)
	}
	if !rawThreshold.IsUint64() || rawThreshold.Uint64() == 0 || rawThreshold.Uint64() > maxValidators {
		return nil, 0, fmt.Errorf("unexpected threshold in the AggOracleCommittee: %s", rawThreshold)
	}
	threshold := int(rawThreshold.Uint64())

	infos, err := s.committee.GetValidatorsInfo(opts)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to read the validators of the AggOracleCommittee: %w", err)
	}
	if threshold > len(infos) {
		return nil, 0, fmt.Errorf("the AggOracleCommittee has a threshold (%d) greater than its validators (%d)",
			threshold, len(infos))
	}

	validators := make([]gervalidator.Validator, len(infos))
	for i, info := range infos {
		url := info.Url
		if override, ok := s.cfg.ValidatorURLOverrides[info.Addr]; ok {
			url = override
		}
		validators[i] = gervalidator.Validator{Address: info.Addr, URL: url}
	}
	return validators, threshold, nil
}

// IsGERInjected checks if the GER is registered on the L2 GER manager
func (s *CommitteeV2Sender) IsGERInjected(ger common.Hash) (bool, error) {
	gerIndex, err := s.l2GERManager.GlobalExitRootMap(&bind.CallOpts{Pending: false}, ger)
	if err != nil {
		return false, fmt.Errorf("failed to check if global exit root is injected %s: %w", ger, err)
	}
	return gerIndex.Sign() > 0, nil
}

// isProcessed returns true if there is nothing left to do with the GER: it is registered on the L2 GER manager,
// or the committee already processed it. The committee never processes a GER twice, not even
// if it was removed from the L2 GER manager afterwards, so retrying it would be useless.
func (s *CommitteeV2Sender) isProcessed(ctx context.Context, ger common.Hash) (bool, error) {
	injected, err := s.IsGERInjected(ger)
	if err != nil {
		return false, err
	}
	if injected {
		return true, nil
	}
	processed, err := s.committee.InjectedGERs(&bind.CallOpts{Context: ctx}, ger)
	if err != nil {
		return false, fmt.Errorf("failed to check if the GER %s was processed by the committee: %w", ger, err)
	}
	return processed, nil
}

// ProcessGER gets the GER injected: collects the signatures of the validators and sends injectGER.
// If another GER is being processed it does nothing: that one has to finish (or give up) first.
func (s *CommitteeV2Sender) ProcessGER(ctx context.Context, ger common.Hash) error {
	if !s.processing.TryLock() {
		s.logger.Debugf("GER %s: another GER is still being processed, skipping it", ger)
		return nil
	}
	defer s.processing.Unlock()

	if s.cfg.PendingGERTimeout <= 0 {
		return s.processGER(ctx, ger)
	}

	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, s.cfg.PendingGERTimeout)
	defer cancel()
	err := s.processGER(ctx, ger)
	if err != nil && parent.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: GER %s was not injected in %s: %w",
			ErrGERPendingTimeout, ger, s.cfg.PendingGERTimeout, err)
	}
	return err
}

// processGER makes attempts to inject the GER until it is done, it can't be injected or the
// context is done
func (s *CommitteeV2Sender) processGER(ctx context.Context, ger common.Hash) error {
	failedAttempts := 0
	for {
		processed, err := s.isProcessed(ctx, ger)
		if err != nil {
			return err
		}
		if processed {
			s.logger.Debugf("GER %s is already processed", ger)
			return nil
		}

		outcome, err := s.tryInject(ctx, ger)
		switch outcome {
		case outcomeDone:
			return nil
		case outcomeFail:
			return err
		case outcomeRetry:
			failedAttempts++
			s.logger.Warnf("GER %s: attempt %d of %d failed: %s", ger, failedAttempts, s.cfg.MaxInjectAttempts, err)
			if failedAttempts >= s.cfg.MaxInjectAttempts {
				return fmt.Errorf("%w: GER %s, %d attempts, last error: %w",
					ErrInjectAttemptsExhausted, ger, failedAttempts, err)
			}
		case outcomePending:
		}

		if err := sleepContext(ctx, s.cfg.CollectRetryInterval); err != nil {
			return fmt.Errorf("processing GER %s: %w", ger, err)
		}
	}
}

// tryInject makes one attempt to inject the GER
func (s *CommitteeV2Sender) tryInject(ctx context.Context, ger common.Hash) (attemptOutcome, error) {
	validators, threshold, err := s.committeeState(ctx)
	if err != nil {
		return outcomeFail, err
	}

	result, err := s.collector.Collect(ctx, ger, validators, threshold)
	if err != nil {
		return s.classifyCollectError(ger, err)
	}

	verified, err := s.verify(ctx, ger, result.Signatures)
	switch verified {
	case verificationAlreadyInjected:
		return outcomeDone, nil
	case verificationCommitteeChanged:
		return outcomeRetry, err
	case verificationFailed:
		return outcomeFail, err
	case verificationValid:
	}

	if err := s.submit(ctx, ger, result); err != nil {
		if ctx.Err() != nil {
			return outcomeFail, err
		}
		// The transaction failed: the GER may have been injected anyway (processGER checks it), if not it is
		// worth collecting the signatures again
		return outcomeRetry, err
	}
	return outcomeDone, nil
}

// classifyCollectError decides what to do after failing to collect the signatures
func (s *CommitteeV2Sender) classifyCollectError(ger common.Hash, err error) (attemptOutcome, error) {
	var notReached *gervalidator.ThresholdNotReachedError
	if !errors.As(err, &notReached) {
		return outcomeFail, err
	}
	if notReached.Retryable() {
		s.logger.Infof("GER %s is pending: %s", ger, err)
		return outcomePending, nil
	}
	return outcomeFail, fmt.Errorf("not enough validators can sign GER %s, dropping it: %w", ger, err)
}

// verify checks the signatures with the contract before spending gas. A reverted call is classified by its
// custom error: the GER is already injected, the committee changed since the validators and the
// threshold were read, or anything else.
func (s *CommitteeV2Sender) verify(ctx context.Context, ger common.Hash, signatures []byte) (verification, error) {
	valid, err := s.committee.VerifyGER(&bind.CallOpts{Context: ctx, From: s.proposer}, ger, signatures)
	if err == nil {
		if !valid {
			return verificationFailed, fmt.Errorf("the AggOracleCommittee didn't validate the signatures of GER %s", ger)
		}
		return verificationValid, nil
	}

	selector, hasSelector := revertSelector(err)
	if !hasSelector {
		return verificationFailed, fmt.Errorf("verifyGER failed for GER %s: %w", ger, err)
	}
	switch selector {
	case s.errorSelector("GERAlreadyInjected"):
		s.logger.Infof("GER %s was already injected into the committee", ger)
		return verificationAlreadyInjected, nil
	case s.errorSelector("InvalidSignaturesLength"),
		s.errorSelector("ValidatorDoesNotExist"),
		s.errorSelector("ValidatorsNotOrdered"):
		return verificationCommitteeChanged, fmt.Errorf("the committee changed while collecting the signatures: %w", err)
	default:
		return verificationFailed, fmt.Errorf("verifyGER reverted for GER %s (error selector 0x%x): %w", ger, selector, err)
	}
}

// submit sends injectGER and waits until it is mined
func (s *CommitteeV2Sender) submit(ctx context.Context, ger common.Hash, result *gervalidator.Result) error {
	txInput, err := s.committeeABI.Pack(injectGERFuncName, [common.HashLength]byte(ger), result.Signatures)
	if err != nil {
		return fmt.Errorf("failed to pack the %s call: %w", injectGERFuncName, err)
	}

	s.logger.Infof("injecting GER %s with the signatures of %d validators", ger, len(result.Signers))
	if err := s.submitter.submitAndWait(ctx, &s.committeeAddr, txInput, "inject", ger); err != nil {
		return err
	}
	// submitAndWait returns nil when the context is cancelled: don't report that as injected
	return ctx.Err()
}

// errorSelector returns the selector of a custom error of the committee
func (s *CommitteeV2Sender) errorSelector(name string) [selectorLength]byte {
	var selector [selectorLength]byte
	if abiErr, ok := s.committeeABI.Errors[name]; ok {
		copy(selector[:], abiErr.ID[:selectorLength])
	}
	return selector
}

// revertSelector returns the selector of the custom error that made a call revert, if the error carries it
func revertSelector(err error) ([selectorLength]byte, bool) {
	var selector [selectorLength]byte
	var dataErr interface{ ErrorData() any }
	if !errors.As(err, &dataErr) {
		return selector, false
	}
	hexData, ok := dataErr.ErrorData().(string)
	if !ok {
		return selector, false
	}
	data, decodeErr := hexutil.Decode(hexData)
	if decodeErr != nil || len(data) < selectorLength {
		return selector, false
	}
	copy(selector[:], data[:selectorLength])
	return selector, true
}

// sleepContext waits for the duration or until the context is done
func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
