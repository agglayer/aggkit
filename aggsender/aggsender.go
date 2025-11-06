package aggsender

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	storage                      db.AggSenderStorage
	aggLayerClient               agglayer.AgglayerClientInterface
	compatibilityStoragedChecker compatibility.CompatibilityChecker
	certStatusChecker            types.CertificateStatusChecker
	certQuerier                  types.CertificateQuerier
	rollupDataQuerier            types.RollupDataQuerier
	validatorPoller              types.ValidatorPoller
	localValidator               types.CertificateValidateAndSigner

	l1Client         aggkittypes.BaseEthereumClienter
	l1InfoTreeSyncer types.L1InfoTreeSyncer

	certificateSendTrigger types.CertificateSendTrigger

	cfg config.Config

	status *types.AggsenderStatus
	flow   types.AggsenderBuilderFlow

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
	l1Client aggkittypes.BaseEthereumClienter,
	l2Client aggkittypes.BaseEthereumClienter,
	rollupDataQuerier types.RollupDataQuerier,
	committeeQuerier types.MultisigQuerier,
) (*AggSender, error) {
	mode, err := committeeQuerier.ResolveAutoMode(cfg.Mode)
	if err != nil {
		return nil, err
	}
	// Override configuration with the resolved mode
	if cfg.Mode != mode {
		log.Infof("aggsender mode from %s to %s", cfg.Mode, mode)
		cfg.Mode = mode
	}

	certificateSendTrigger, err := NewCertificateSendTrigger(
		ctx,
		cfg,
		logger,
		l1Client,
		l2Syncer,
		aggLayerClient,
	)
	if err != nil {
		return nil, fmt.Errorf("error creating runner: %w", err)
	}

	return newAggsender(
		ctx,
		logger,
		cfg,
		aggLayerClient,
		l1InfoTreeSyncer,
		l2Syncer,
		l1Client,
		l2Client,
		rollupDataQuerier,
		committeeQuerier,
		certificateSendTrigger,
	)
}

// newAggsender returns a new AggSender instance
func newAggsender(
	ctx context.Context,
	logger *log.Logger,
	cfg config.Config,
	aggLayerClient agglayer.AgglayerClientInterface,
	l1InfoTreeSyncer types.L1InfoTreeSyncer,
	l2Syncer types.L2BridgeSyncer,
	l1Client aggkittypes.BaseEthereumClienter,
	l2Client aggkittypes.BaseEthereumClienter,
	rollupDataQuerier types.RollupDataQuerier,
	committeeQuerier types.MultisigQuerier,
	certificateSendTrigger types.CertificateSendTrigger,
) (*AggSender, error) {
	storageConfig := db.AggSenderSQLStorageConfig{
		DBPath:                   cfg.StoragePath,
		CertificatesDir:          cfg.CertificatesDir,
		RetainCertificatesPolicy: cfg.StorageRetainCertificatesPolicy,
	}
	storage, err := db.NewAggSenderSQLStorage(logger, storageConfig)
	if err != nil {
		return nil, err
	}

	flowManager, err := flows.NewBuilderFlow(
		ctx,
		cfg,
		logger,
		storage,
		l1Client,
		l2Client,
		l1InfoTreeSyncer,
		l2Syncer,
		rollupDataQuerier,
		committeeQuerier,
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

	aggchainFEPCaller, err := query.NewAggchainFEPQuerier(logger, cfg.Mode,
		cfg.SovereignRollupAddr, l1Client)
	if err != nil {
		return nil, fmt.Errorf("error creating aggchain FEP caller: %w", err)
	}

	certQuerier := query.NewCertificateQuerier(
		l2Syncer,
		aggchainFEPCaller,
		aggLayerClient,
	)

	verifierFlow, err := flows.NewLocalVerifier(
		ctx,
		cfg,
		l1Client,
		flowManager,
	)
	if err != nil {
		return nil, fmt.Errorf("error creating verifier flow: %w", err)
	}

	localValidator := validator.NewLocalValidator(
		logger,
		storage,
		validator.NewAggsenderValidator(
			logger,
			verifierFlow,
			query.NewL1InfoTreeDataQuerier(l1Client, l1InfoTreeSyncer),
			certQuerier,
			query.NewLERDataQuerier(cfg.RollupCreationBlockL1, rollupDataQuerier),
		),
	)

	return &AggSender{
		cfg:                          cfg,
		log:                          logger,
		storage:                      storage,
		aggLayerClient:               aggLayerClient,
		status:                       &types.AggsenderStatus{Status: types.StatusNone},
		flow:                         flowManager,
		compatibilityStoragedChecker: compatibilityStoragedChecker,
		l2OriginNetwork:              l2OriginNetwork,
		certQuerier:                  certQuerier,
		rollupDataQuerier:            rollupDataQuerier,
		localValidator:               localValidator,
		validatorPoller: NewValidatorPoller(
			logger,
			storage,
			flowManager.Signer(),
			committeeQuerier,
			cfg.ValidatorClient,
		),
		certStatusChecker: statuschecker.NewCertStatusChecker(
			logger, storage, aggLayerClient, certQuerier, l2OriginNetwork),
		l1Client:               l1Client,
		l1InfoTreeSyncer:       l1InfoTreeSyncer,
		certificateSendTrigger: certificateSendTrigger,
	}, nil
}

func (a *AggSender) Info() types.AggsenderInfo {
	res := types.AggsenderInfo{
		AggsenderStatus: *a.status,
		Version:         aggkit.GetVersion(),
		TriggerStatus:   a.certificateSendTrigger.Status(),
		NetworkID:       a.l2OriginNetwork,
		Mode:            a.cfg.Mode,
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

	a.certificateSendTrigger.Setup(ctx)
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

// sendCertificates sends certificates to the aggLayer based on epoch notifications
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

	sendTriggerCh := a.certificateSendTrigger.TriggerCh(ctx)
	a.status.Status = types.StatusCertificateStage
	iteration := 0
	for {
		select {
		case <-checkCertChannel:
			iteration++
			a.log.Debugf("Checking perodical certificates status (%s)",
				a.cfg.CheckCertConfigBriefString())

			checkResult, err := a.certStatusChecker.CheckPeriodicallyStatus(ctx)
			if err != nil {
				a.status.SetLastError(err)
				a.log.Errorf("error checking last certificate from agglayer: %v", err)
			} else if !checkResult.ExistPendingCerts && checkResult.ExistNewInErrorCert {
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
		case triggerEvent := <-sendTriggerCh:
			iteration++
			a.log.Infof("Certificate send trigger event received: %s", triggerEvent.String())
			checkResult, err := a.certStatusChecker.CheckPeriodicallyStatus(ctx)
			if err != nil {
				a.log.Errorf("Certificate send trigger: error checking certificate status: %v", err)
				a.status.SetLastError(err)
			} else {
				if !checkResult.ExistPendingCerts {
					_, err := a.sendCertificateWithRetries(ctx)
					if err != nil {
						a.log.Errorf("Certificate send trigger: error sending certificate: %v", err)
						a.status.SetLastError(err)
					}
				} else {
					a.log.Info("Skipping certificate sending because there are pending certificates")
				}
			}

			if returnAfterNIterations > 0 && iteration >= returnAfterNIterations {
				a.log.Warnf("Certificate send trigger: reached number of iterations, so we are going to return")
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
	startRunnerStatus := a.certificateSendTrigger.Status()
	a.log.Infof("trying to send a new certificate... %s", startRunnerStatus)

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

	if _, err := a.localValidator.ValidateAndSignCertificate(ctx, certificate, certificateParams.ToBlock); err != nil {
		a.log.Warnf("error validating certificate locally: %w", err)
	}

	multisig, err := a.validatorPoller.PollValidators(ctx, &types.ValidationRequest{
		Certificate:       certificate,
		LastL2BlockInCert: certificateParams.ToBlock,
	})
	if err != nil {
		return nil, fmt.Errorf("error polling validator committee: %w", err)
	}

	if err := a.flow.UpdateAggchainData(certificate, multisig); err != nil {
		return nil, fmt.Errorf("error updating agchain data with multisig: %w", err)
	}

	a.log.Infof("certificate ready to be sent to AggLayer: %s start: %s, end: %s",
		certificate.Brief(), startRunnerStatus, a.certificateSendTrigger.Status())
	metrics.CertificateBuildTime(time.Since(start).Seconds())

	if a.cfg.DryRun {
		a.log.Warn("dry run mode enabled, skipping sending certificate")
		return certificate, nil
	}

	certificateHash, err := a.aggLayerClient.SendCertificate(ctx, certificate)
	if err != nil {
		a.saveNonAcceptedCert(ctx, certificate, certificateParams.CreatedAt, err)

		return nil, fmt.Errorf("error sending certificate: %w", err)
	}

	metrics.CertificateSent()
	a.log.Infof("certificate send: Height: %d blockRange: [%d - %d] cert: %s", certificate.Height,
		certificateParams.FromBlock, certificateParams.ToBlock, certificate.Brief())

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
