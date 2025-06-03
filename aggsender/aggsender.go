package aggsender

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	jRPC "github.com/0xPolygon/cdk-rpc/rpc"
	zkevm "github.com/agglayer/aggkit"
	"github.com/agglayer/aggkit/agglayer"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/config"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/flows"
	"github.com/agglayer/aggkit/aggsender/metrics"
	aggsenderrpc "github.com/agglayer/aggkit/aggsender/rpc"
	"github.com/agglayer/aggkit/aggsender/statuschecker"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db/compatibility"
	"github.com/agglayer/aggkit/l1infotreesync"
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
	l1InfoTreeSyncer *l1infotreesync.L1InfoTreeSync,
	l2Syncer types.L2BridgeSyncer,
	epochNotifier types.EpochNotifier,
	l1Client aggkittypes.BaseEthereumClienter,
	l2Client aggkittypes.BaseEthereumClienter) (*AggSender, error) {
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
		certStatusChecker:            statuschecker.NewCertStatusChecker(logger, storage, aggLayerClient, l2OriginNetwork),
	}, nil
}

func (a *AggSender) Info() types.AggsenderInfo {
	res := types.AggsenderInfo{
		AggsenderStatus:          *a.status,
		Version:                  zkevm.GetVersion(),
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

	logger := log.WithFields("aggsender-rpc", aggkitcommon.BRIDGE)
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
	a.certStatusChecker.CheckInitialStatus(ctx, a.cfg.DelayBeetweenRetries.Duration, a.status)
	if err := a.flow.CheckInitialStatus(ctx); err != nil {
		a.log.Panicf("error checking flow Initial Status: %v", err)
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
				_, err := a.sendCertificate(ctx)
				a.status.SetLastError(err)
				if err != nil {
					a.log.Error(err)
				}
			} else {
				log.Infof("Skipping epoch %s because there are pending certificates",
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

	if rateLimitSleepTime := a.rateLimiter.Call("sendCertificate", false); rateLimitSleepTime != nil {
		a.log.Warnf("rate limit reached , next cert %s can be submitted after %s so sleeping. Rate:%s",
			certificate.ID(),
			rateLimitSleepTime.String(), a.rateLimiter.String())
		time.Sleep(*rateLimitSleepTime)
	}
	a.log.Infof("certificate ready to be sent to AggLayer: %s start: %s , end: %s",
		certificate.Brief(), startEpochStatus.String(), a.epochNotifier.GetEpochStatus().String())
	metrics.CertificateBuildTime(time.Since(start).Seconds())

	if a.cfg.DryRun {
		a.log.Warn("dry run mode enabled, skipping sending certificate")
		return certificate, nil
	}
	certificateHash, err := a.aggLayerClient.SendCertificate(ctx, certificate)
	if err != nil {
		if err := a.storage.SaveNonAcceptedCertificate(ctx, certificate, certificateParams.CreatedAt); err != nil {
			a.log.Errorf("error saving non accepted certificate %s in db: %w", certificate.Brief(), err)
		}

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
				time.Sleep(a.cfg.DelayBeetweenRetries.Duration)
			}
		}
	}
	return nil
}
