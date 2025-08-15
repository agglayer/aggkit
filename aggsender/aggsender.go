package aggsender

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	jRPC "github.com/0xPolygon/cdk-rpc/rpc"
	aggkit "github.com/agglayer/aggkit"
	"github.com/agglayer/aggkit/agglayer"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/config"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/flows"
	"github.com/agglayer/aggkit/aggsender/metrics"
	"github.com/agglayer/aggkit/aggsender/query"
	aggsenderrpc "github.com/agglayer/aggkit/aggsender/rpc"
	"github.com/agglayer/aggkit/aggsender/statuschecker"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/aggsender/validator"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db/compatibility"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

type RateLimiter interface {
	Call(msg string, allowToSleep bool) *time.Duration
	String() string
}

// AggSender is a component that will send certificates to the aggLayer
type AggSender struct {
	log aggkitcommon.Logger

	epochNotifier types.EpochNotifier

	storage                      db.AggSenderStorage
	aggLayerClient               agglayer.AgglayerClientInterface
	compatibilityStoragedChecker compatibility.CompatibilityChecker
	certStatusChecker            types.CertificateStatusChecker
	certQuerier                  types.CertificateQuerier
	rollupDataQuerier            types.RollupDataQuerier
	committeeQuerier             types.MultisigQuerier

	l1Client         aggkittypes.BaseEthereumClienter
	l1InfoTreeSyncer types.L1InfoTreeSyncer
	localValidator   types.CertificateValidateAndSigner

	cfg config.Config

	status      *types.AggsenderStatus
	rateLimiter RateLimiter
	flow        types.AggsenderFlow

	l2OriginNetwork uint32
}

// New returns a new AggSender instance
func New(
	ctx context.Context,
	logger *log.Logger,
	cfg config.Config,
	aggLayerClient agglayer.AgglayerClientInterface,
	l1InfoTreeSyncer types.L1InfoTreeSyncer,
	l2Syncer types.L2BridgeSyncer,
	epochNotifier types.EpochNotifier,
	l1Client aggkittypes.BaseEthereumClienter,
	l2Client aggkittypes.BaseEthereumClienter,
	rollupDataQuerier types.RollupDataQuerier,
	committeeQuerier types.MultisigQuerier,
) (*AggSender, error) {
	storageConfig := db.AggSenderSQLStorageConfig{
		DBPath:                  cfg.StoragePath,
		KeepCertificatesHistory: cfg.KeepCertificatesHistory,
	}
	storage, err := db.NewAggSenderSQLStorage(logger, storageConfig)
	if err != nil {
		return nil, err
	}

	rateLimit := aggkitcommon.NewRateLimit(cfg.MaxSubmitCertificateRate)

	flowManager, err := flows.NewFlow(
		ctx,
		cfg,
		logger,
		storage,
		l1Client,
		l2Client,
		l1InfoTreeSyncer,
		l2Syncer,
		rollupDataQuerier,
	)
	if err != nil {
		return nil, fmt.Errorf("error creating flow manager: %w", err)
	}

	logger.Infof("Aggsender Config: %s.", cfg.String())
	l2OriginNetwork := l2Syncer.OriginNetwork()

	compatibilityStoragedChecker := compatibility.NewCompatibilityCheck(
		cfg.RequireStorageContentCompatibility,
		func(ctx context.Context) (db.RuntimeData, error) {
			return db.RuntimeData{NetworkID: l2OriginNetwork}, nil
		},
		compatibility.NewKeyValueToCompatibilityStorage[db.RuntimeData](storage, aggkitcommon.AGGSENDER),
	)

	aggchainFEPCaller, err := query.NewAggchainFEPQuerier(logger, types.AggsenderMode(cfg.Mode),
		cfg.SovereignRollupAddr, l1Client)
	if err != nil {
		return nil, fmt.Errorf("error creating aggchain FEP caller: %w", err)
	}

	certQuerier := query.NewCertificateQuerier(
		l2Syncer,
		aggchainFEPCaller,
		aggLayerClient,
	)

	return &AggSender{
		cfg:                          cfg,
		log:                          logger,
		storage:                      storage,
		aggLayerClient:               aggLayerClient,
		epochNotifier:                epochNotifier,
		status:                       &types.AggsenderStatus{Status: types.StatusNone},
		flow:                         flowManager,
		rateLimiter:                  rateLimit,
		compatibilityStoragedChecker: compatibilityStoragedChecker,
		l2OriginNetwork:              l2OriginNetwork,
		certQuerier:                  certQuerier,
		rollupDataQuerier:            rollupDataQuerier,
		committeeQuerier:             committeeQuerier,
		certStatusChecker: statuschecker.NewCertStatusChecker(
			logger, storage, aggLayerClient, certQuerier, l2OriginNetwork),
		l1Client:         l1Client,
		l1InfoTreeSyncer: l1InfoTreeSyncer,
	}, nil
}

func (a *AggSender) GetLERQuerier() types.LERQuerier {
	return query.NewLERDataQuerier(a.cfg.RollupCreationBlockL1, a.rollupDataQuerier)
}

func (a *AggSender) Info() types.AggsenderInfo {
	res := types.AggsenderInfo{
		AggsenderStatus:          *a.status,
		Version:                  aggkit.GetVersion(),
		EpochNotifierDescription: a.epochNotifier.String(),
		NetworkID:                a.l2OriginNetwork,
	}
	return res
}

// GetRPCServices returns the list of services that the RPC provider exposes
func (a *AggSender) GetRPCServices() []jRPC.Service {
	if !a.cfg.EnableRPC {
		return []jRPC.Service{}
	}

	logger := log.WithFields("module", "aggsender-rpc")
	return []jRPC.Service{
		{
			Name:    "aggsender",
			Service: aggsenderrpc.NewAggsenderRPC(logger, a.storage, a),
		},
	}
}

// Start starts the AggSender
func (a *AggSender) Start(ctx context.Context) {
	a.log.Info("AggSender started")
	metrics.Register()
	a.status.Start(time.Now().UTC())

	a.checkDBCompatibility(ctx)
	a.certStatusChecker.CheckInitialStatus(ctx, a.cfg.DelayBetweenRetries.Duration, a.status)
	if err := a.flow.CheckInitialStatus(ctx); err != nil {
		a.log.Panicf("error checking flow Initial Status: %v", err)
	}

	if !a.cfg.RequireValidatorCall {
		a.localValidator = validator.NewLocalValidator(
			a.log,
			a.storage,
			validator.NewAggsenderValidator(
				a.log,
				a.flow,
				query.NewL1InfoTreeDataQuerier(a.l1Client, a.l1InfoTreeSyncer),
				a.certQuerier,
				a.GetLERQuerier(),
			),
		)
	}

	a.sendCertificates(ctx, 0)
}

func (a *AggSender) checkDBCompatibility(ctx context.Context) {
	if a.compatibilityStoragedChecker == nil {
		a.log.Warnf("compatibilityStoragedChecker is nil, so we are not going to check the compatibility")
		return
	}
	if err := a.compatibilityStoragedChecker.Check(ctx, nil); err != nil {
		a.log.Panicf("error checking compatibility data in DB, you can bypass this check using config file. Err: %w", err)
	}
}
func (a *AggSender) checkSendCertificateStopCondition(err error) {
	if errors.Is(err, flows.ErrComplete) {
		a.log.Infof("AggSender reached the end of the certificates to send")
		if a.cfg.StopOnFinishedSendingAllCertificates {
			// That is the fastest way to stop the process. Currently there are no way of gracefully stopping the AggSender
			// because the run.go launch the components with a goroutine but doesn't check any return value
			a.log.Panicf("Stopping AggSender because StopOnFinishedSendingAllCertificates is true")
		}
	}
}

// sendCertificates sends certificates to the aggLayer
func (a *AggSender) sendCertificates(ctx context.Context, returnAfterNIterations int) {
	var checkCertChannel <-chan time.Time
	if a.cfg.CheckStatusCertificateInterval.Duration > 0 {
		checkCertTicker := time.NewTicker(a.cfg.CheckStatusCertificateInterval.Duration)
		defer checkCertTicker.Stop()
		checkCertChannel = checkCertTicker.C
	} else {
		a.log.Infof("CheckStatusCertificateInterval is 0, so we are not going to check the certificate status")
		checkCertChannel = make(chan time.Time)
	}

	chEpoch := a.epochNotifier.Subscribe("aggsender")
	a.status.Status = types.StatusCertificateStage
	iteration := 0
	for {
		select {
		case <-checkCertChannel:
			iteration++
			a.log.Debugf("Checking perodical certificates status (%s)",
				a.cfg.CheckCertConfigBriefString())
			checkResult := a.certStatusChecker.CheckPendingCertificatesStatus(ctx)
			if !checkResult.ExistPendingCerts && checkResult.ExistNewInErrorCert {
				if a.cfg.RetryCertAfterInError {
					a.log.Infof("An InError cert exists. Sending a new one (%s)", a.cfg.CheckCertConfigBriefString())
					_, err := a.sendCertificate(ctx)
					a.status.SetLastError(err)
					if err != nil {
						a.log.Error(err)
					}
					a.checkSendCertificateStopCondition(err)
				} else {
					a.log.Infof("An InError cert exists but skipping send cert because RetryCertAfterInError is false")
				}
			}
			if returnAfterNIterations > 0 && iteration >= returnAfterNIterations {
				a.log.Warnf("reached number of iterations, so we are going to return")
				return
			}
		case epoch := <-chEpoch:
			iteration++
			a.log.Infof("Epoch received: %s", epoch.String())
			checkResult := a.certStatusChecker.CheckPendingCertificatesStatus(ctx)
			if !checkResult.ExistPendingCerts {
				_, err := a.sendCertificateWithRetries(ctx)
				if err != nil {
					a.log.Errorf("error sending certificate: %v", err)
					a.status.SetLastError(err)
				}
			} else {
				a.log.Infof("Skipping epoch %s because there are pending certificates",
					epoch.String())
			}

			if returnAfterNIterations > 0 && iteration >= returnAfterNIterations {
				a.log.Warnf("reached number of iterations, so we are going to return")
				return
			}
		case <-ctx.Done():
			a.log.Info("AggSender stopped")
			return
		}
	}
}

func (a *AggSender) sendCertificateWithRetries(ctx context.Context) (*agglayertypes.Certificate, error) {
	retryHandler, err := a.cfg.RetriesToBuildAndSendCertificate.NewRetryHandler()
	if err != nil {
		return nil, fmt.Errorf("error creating retry config: %w", err)
	}
	cert, err := aggkitcommon.Execute(retryHandler,
		ctx,
		a.log.Infof,
		"sendCertificateWithRetries",
		func() (*agglayertypes.Certificate, error) {
			cert, err := a.sendCertificate(ctx)
			a.status.SetLastError(err)
			if err != nil {
				a.log.Error(err)
			}
			// If ErrComplete, don't need to retry
			if errors.Is(err, flows.ErrComplete) {
				err = fmt.Errorf("%w. %w", err, aggkitcommon.ErrAbort)
			}
			return cert, err
		})
	// Check error to stop aggsender if needed
	a.checkSendCertificateStopCondition(err)
	return cert, err
}

// sendCertificate sends certificate for a network
func (a *AggSender) sendCertificate(ctx context.Context) (*agglayertypes.Certificate, error) {
	startEpochStatus := a.epochNotifier.GetEpochStatus()
	a.log.Infof("trying to send a new certificate... %s", startEpochStatus.String())

	start := time.Now()

	certificateParams, err := a.flow.GetCertificateBuildParams(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting certificate build params: %w", err)
	}

	if certificateParams == nil {
		return nil, nil
	}

	certificate, err := a.flow.BuildCertificate(ctx, certificateParams)
	if err != nil {
		return nil, fmt.Errorf("error building certificate: %w", err)
	}

	var (
		validators          []types.CertificateValidateAndSigner
		signaturesThreshold uint32
	)

	if a.localValidator != nil {
		validators = append(validators, a.localValidator)
		signaturesThreshold = 1
	} else {
		validators, signaturesThreshold, err = a.getValidators(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get validators: %w", err)
		}
	}

	multisig, err := a.pollValidators(ctx,
		validators, signaturesThreshold,
		certificate, certificateParams.ToBlock)
	if err != nil {
		// TODO - agglayer has not yet implemented the endpoints needed to validate a certificate
		// so lets just log the error and continue. This will be changed when the agglayer is ready
		// a.saveNonAcceptedCert(ctx, certificate, certificateParams.CreatedAt, err)
		// return nil, fmt.Errorf("certificate validation failed: %w", err)
		a.log.Warnf("certificate validation failed: %w. Cert: %s", err, certificate.Brief())
	}
	a.log.Infof("certificate ready to be sent to AggLayer: %s start: %s, end: %s",
		certificate.Brief(), startEpochStatus.String(), a.epochNotifier.GetEpochStatus().String())
	metrics.CertificateBuildTime(time.Since(start).Seconds())

	if a.cfg.DryRun {
		a.log.Warn("dry run mode enabled, skipping sending certificate")
		return certificate, nil
	}
	if rateLimitSleepTime := a.rateLimiter.Call("sendCertificate", false); rateLimitSleepTime != nil {
		a.log.Warnf("rate limit reached , next cert %s can be submitted after %s so sleeping. Rate:%s",
			certificate.ID(),
			rateLimitSleepTime.String(), a.rateLimiter.String())
		time.Sleep(*rateLimitSleepTime)
	}

	// TODO: Update once agglayer endpoint changes to accept the multisig
	var signature []byte
	if len(multisig) > 0 {
		signature = multisig[0]
	}
	certificateHash, err := a.aggLayerClient.SendCertificate(ctx, certificate, signature)
	if err != nil {
		a.saveNonAcceptedCert(ctx, certificate, certificateParams.CreatedAt, err)

		return nil, fmt.Errorf("error sending certificate: %w", err)
	}

	metrics.CertificateSent()
	a.log.Debugf("certificate send: Height: %d cert: %s", certificate.Height, certificate.Brief())

	raw, err := json.Marshal(certificate)
	if err != nil {
		return nil, fmt.Errorf("error marshalling signed certificate. Cert:%s. Err: %w", certificate.Brief(), err)
	}

	jsonCert := string(raw)
	prevLER := common.BytesToHash(certificate.PrevLocalExitRoot[:])

	certInfo := types.Certificate{
		Header: &types.CertificateHeader{
			Height:                  certificate.Height,
			RetryCount:              certificateParams.RetryCount,
			CertificateID:           certificateHash,
			NewLocalExitRoot:        certificate.NewLocalExitRoot,
			PreviousLocalExitRoot:   &prevLER,
			FromBlock:               certificateParams.FromBlock,
			ToBlock:                 certificateParams.ToBlock,
			CreatedAt:               certificateParams.CreatedAt,
			UpdatedAt:               certificateParams.CreatedAt,
			FinalizedL1InfoTreeRoot: &certificateParams.L1InfoTreeRootFromWhichToProve,
			L1InfoTreeLeafCount:     certificateParams.L1InfoTreeLeafCount,
			CertType:                certificateParams.CertificateType,
			CertSource:              types.CertificateSourceLocal,
		},
		SignedCertificate: &jsonCert,
		AggchainProof:     certificateParams.AggchainProof,
		ExtraData:         certificateParams.ExtraData,
	}
	// TODO: Improve this case, if a cert is not save in the storage, we are going to settle a unknown certificate
	err = a.saveCertificateToStorage(ctx, certInfo, a.cfg.MaxRetriesStoreCertificate)
	if err != nil {
		a.log.Errorf("error saving certificate  to storage. Cert:%s Err: %w", certInfo.String(), err)
		return nil, fmt.Errorf("error saving last sent certificate %s in db: %w", certInfo.String(), err)
	}

	a.log.Infof("certificate: %s sent successfully for range of l2 blocks (from block: %d, to block: %d) cert:%s",
		certInfo.Header.ID(), certificateParams.FromBlock, certificateParams.ToBlock, certificate.Brief())

	return certificate, nil
}

// pollValidators committee signers to validate the certificate and gather signatures
func (a *AggSender) pollValidators(
	ctx context.Context,
	validators []types.CertificateValidateAndSigner,
	signaturesThreshold uint32,
	certificate *agglayertypes.Certificate,
	lastL2BlockInCert uint64,
) ([][]byte, error) {
	if len(validators) == 0 {
		a.log.Warnf("skipping certificate validation, because there are no validators configured")
		return nil, nil
	}

	a.log.Infof("delegating certificate validation: %s", certificate.Brief())

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type signResult struct {
		signature []byte
		err       error
	}

	results := make(chan signResult, len(validators))
	var wg sync.WaitGroup

	for _, v := range validators {
		wg.Add(1)
		go func(v types.CertificateValidateAndSigner) {
			defer wg.Done()

			select {
			case <-ctx.Done():
				return
			default:
			}

			status, err := v.HealthCheck(ctx)
			if err != nil {
				a.log.Warnf("error checking validator health (URL=%s): %v", v.URL(), err)
				results <- signResult{err: err}
				return
			}

			if !status.IsHealthy() {
				a.log.Warnf("validator (URL=%s) is not healthy: %s, skipping it...", v.URL(), status.String())
				return // skip unhealthy validator
			}

			a.log.Infof("validator health check (URL=%s): %s", v.URL(), status.String())

			sig, err := v.ValidateAndSignCertificate(ctx, certificate, lastL2BlockInCert)
			if err != nil {
				a.log.Errorf("validator %v failed to validate the certificate: %v", v, err)
				results <- signResult{err: err}
				return
			}

			results <- signResult{signature: sig}
		}(v)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	signatures := make([][]byte, 0, len(validators))
	var errs []error

	for res := range results {
		if res.err != nil {
			errs = append(errs, res.err)
			continue
		}

		signatures = append(signatures, res.signature)
		if uint32(len(signatures)) >= signaturesThreshold {
			cancel() // signal other goroutines to stop early
		}
	}

	if uint32(len(signatures)) < signaturesThreshold {
		if len(errs) > 0 {
			return signatures, errors.Join(errs...)
		}
		return signatures, fmt.Errorf("threshold not reached: %d/%d", len(signatures), signaturesThreshold)
	}

	a.log.Infof("certificate validation passed with %d/%d signatures: %s",
		len(signatures), signaturesThreshold, certificate.Brief())

	return signatures, nil
}

// getValidators retrieves the actual multisig committee and creates a set of the validators
// that are going to validate the provided certificate
func (a *AggSender) getValidators(ctx context.Context) ([]types.CertificateValidateAndSigner, uint32, error) {
	committee, err := a.committeeQuerier.GetMultisigCommittee(ctx, aggkittypes.LatestBlockNum)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to retrieve the latest multisig committee: %w", err)
	}

	validators := make([]types.CertificateValidateAndSigner, 0, committee.Size())
	for _, signer := range committee.Signers() {
		clientCfg := a.cfg.ValidatorClient.WithURL(signer.URL)
		validator, err := validator.NewRemoteValidator(&clientCfg, a.storage)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to create a remote validator for committee signer (Address=%s, URL=%s): %w",
				signer.Address, signer.URL, err)
		}

		validators = append(validators, validator)
	}

	return validators, committee.Threshold(), nil
}

// saveCertificateToStorage saves the certificate to the storage
// it retries if it fails. if param retries == 0 it retries indefinitely
func (a *AggSender) saveCertificateToStorage(ctx context.Context, cert types.Certificate, maxRetries int) error {
	retries := 1
	err := fmt.Errorf("initial_error")
	for err != nil {
		if err = a.storage.SaveLastSentCertificate(ctx, cert); err != nil {
			// If this happens we can't work as normal, because local DB is outdated, we have to retry
			a.log.Errorf("error saving last sent certificate %s in db: %w", cert.String(), err)
			if retries == maxRetries {
				return fmt.Errorf("error saving last sent certificate %s in db: %w", cert.String(), err)
			} else {
				retries++
				time.Sleep(a.cfg.DelayBetweenRetries.Duration)
			}
		}
	}
	return nil
}

// saveNonAcceptedCert saves a certificate that was not accepted by the aggLayer in db
func (a *AggSender) saveNonAcceptedCert(
	ctx context.Context,
	cert *agglayertypes.Certificate,
	createdAt uint32,
	certError error) {
	nonAcceptedCert, err := db.NewNonAcceptedCertificate(cert, createdAt, certError.Error())
	if err != nil {
		a.log.Errorf("error creating non accepted certificate: %s. Err: %v", cert.Brief(), err)
		return
	}

	if err := a.storage.SaveNonAcceptedCertificate(ctx, nonAcceptedCert); err != nil {
		a.log.Errorf("error saving non accepted certificate: %s. Err: %v", cert.Brief(), err)
	}
}
