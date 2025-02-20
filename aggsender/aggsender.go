package aggsender

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	jRPC "github.com/0xPolygon/cdk-rpc/rpc"
	zkevm "github.com/agglayer/aggkit"
	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/grpc"
	aggsenderrpc "github.com/agglayer/aggkit/aggsender/rpc"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const signatureSize = 65

var (
	errNoBridgesAndClaims   = errors.New("no bridges and claims to build certificate")
	errInvalidSignatureSize = errors.New("invalid signature size")

	zeroLER = common.HexToHash("0x27ae5ba08d7291c96c8cbddcc148bf48a6d68c7974b94356f53754ef6171d757")
)

type RateLimiter interface {
	Call(msg string, allowToSleep bool) *time.Duration
	String() string
}

// AggSender is a component that will send certificates to the aggLayer
type AggSender struct {
	log types.Logger

	l2Syncer         types.L2BridgeSyncer
	l1infoTreeSyncer types.L1InfoTreeSyncer
	epochNotifier    types.EpochNotifier

	storage        db.AggSenderStorage
	aggLayerClient agglayer.AgglayerClientInterface

	cfg Config

	aggsenderKey *ecdsa.PrivateKey

	status      types.AggsenderStatus
	rateLimiter RateLimiter
	flow        types.AggsenderFlow
}

// New returns a new AggSender instance
func New(
	ctx context.Context,
	logger *log.Logger,
	cfg Config,
	aggLayerClient agglayer.AgglayerClientInterface,
	l1InfoTreeSyncer *l1infotreesync.L1InfoTreeSync,
	l2Syncer types.L2BridgeSyncer,
	epochNotifier types.EpochNotifier,
	l1Client types.EthClient,
	l2Client types.EthClient) (*AggSender, error) {
	storageConfig := db.AggSenderSQLStorageConfig{
		DBPath:                  cfg.StoragePath,
		KeepCertificatesHistory: cfg.KeepCertificatesHistory,
	}
	storage, err := db.NewAggSenderSQLStorage(logger, storageConfig)
	if err != nil {
		return nil, err
	}

	aggsenderPrivateKey, err := aggkitcommon.NewKeyFromKeystore(cfg.AggsenderPrivateKey)
	if err != nil {
		return nil, err
	}
	rateLimit := aggkitcommon.NewRateLimit(cfg.MaxSubmitCertificateRate)

	var (
		aggchainProofClient grpc.AggchainProofClientInterface
		flowManager         types.AggsenderFlow
	)

	if types.AggsenderMode(cfg.Mode) == types.AggchainProofMode {
		if cfg.AggchainProofURL == "" {
			return nil, fmt.Errorf("aggchain prover mode requires AggchainProofURL")
		}

		aggchainProofClient, err = grpc.NewAggchainProofClient(cfg.AggchainProofURL)
		if err != nil {
			return nil, fmt.Errorf("error creating aggkit prover client: %w", err)
		}

		flowManager, err = newAggchainProverFlow(logger, cfg, aggchainProofClient, storage,
			l1InfoTreeSyncer, l2Syncer, l1Client, l2Client)
		if err != nil {
			return nil, fmt.Errorf("error creating aggchain prover flow: %w", err)
		}
	} else {
		flowManager = newPPFlow(logger, cfg, storage, l1InfoTreeSyncer, l2Syncer)
	}

	logger.Infof("Aggsender Config: %s.", cfg.String())

	return &AggSender{
		cfg:              cfg,
		log:              logger,
		storage:          storage,
		l2Syncer:         l2Syncer,
		aggLayerClient:   aggLayerClient,
		l1infoTreeSyncer: l1InfoTreeSyncer,
		aggsenderKey:     aggsenderPrivateKey,
		epochNotifier:    epochNotifier,
		status:           types.AggsenderStatus{Status: types.StatusNone},
		flow:             flowManager,
		rateLimiter:      rateLimit,
	}, nil
}

func (a *AggSender) Info() types.AggsenderInfo {
	res := types.AggsenderInfo{
		AggsenderStatus:          a.status,
		Version:                  zkevm.GetVersion(),
		EpochNotifierDescription: a.epochNotifier.String(),
		NetworkID:                a.l2Syncer.OriginNetwork(),
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
	a.status.Start(time.Now().UTC())
	a.checkInitialStatus(ctx)
	a.sendCertificates(ctx, 0)
}

// checkInitialStatus check local status vs agglayer status
func (a *AggSender) checkInitialStatus(ctx context.Context) {
	ticker := time.NewTicker(a.cfg.DelayBeetweenRetries.Duration)
	defer ticker.Stop()
	a.status.Status = types.StatusCheckingInitialStage
	for {
		a.checkPendingCertificatesStatus(ctx)
		err := a.checkLastCertificateFromAgglayer(ctx)
		a.status.SetLastError(err)
		if err != nil {
			a.log.Errorf("error checking initial status: %w, retrying in %s", err, a.cfg.DelayBeetweenRetries.String())
		} else {
			a.log.Info("Initial status checked successfully")
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
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
			checkResult := a.checkPendingCertificatesStatus(ctx)
			if !checkResult.existPendingCerts && checkResult.existNewInErrorCert {
				if a.cfg.RetryCertAfterInError {
					a.log.Infof("An InError cert exists. Sending a new one (%s)", a.cfg.CheckCertConfigBriefString())
					_, err := a.sendCertificate(ctx)
					a.status.SetLastError(err)
					if err != nil {
						a.log.Error(err)
					}
				} else {
					a.log.Infof("An InError cert exists but skipping send cert because RetryCertInmediatlyAfterInError is false")
				}
			}
			if returnAfterNIterations > 0 && iteration >= returnAfterNIterations {
				a.log.Warnf("reached number of iterations, so we are going to return")
				return
			}
		case epoch := <-chEpoch:
			iteration++
			a.log.Infof("Epoch received: %s", epoch.String())
			checkResult := a.checkPendingCertificatesStatus(ctx)
			if !checkResult.existPendingCerts {
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
func (a *AggSender) sendCertificate(ctx context.Context) (*agglayer.SignedCertificate, error) {
	a.log.Infof("trying to send a new certificate...")

	certificateParams, err := a.flow.GetCertificateBuildParams(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting certificate build params: %w", err)
	}

	if certificateParams == nil || len(certificateParams.Bridges) == 0 {
		return nil, nil
	}

	certificate, err := a.flow.BuildCertificate(ctx, certificateParams)
	if err != nil {
		return nil, fmt.Errorf("error building certificate: %w", err)
	}

	signedCertificate, err := a.signCertificate(certificate)
	if err != nil {
		return nil, fmt.Errorf("error signing certificate: %w", err)
	}

	if rateLimitSleepTime := a.rateLimiter.Call("sendCertificate", false); rateLimitSleepTime != nil {
		a.log.Warnf("rate limit reached , next cert %s can be submitted after %s so sleeping. Rate:%s",
			certificate.ID(),
			rateLimitSleepTime.String(), a.rateLimiter.String())
		time.Sleep(*rateLimitSleepTime)
	}
	if !a.isAllowedSendCertificateEpochPercent() {
		return nil, fmt.Errorf("forbidden to send certificate due epoch percentage")
	}

	a.log.Infof("certificate ready to be send to AggLayer: %s", signedCertificate.Brief())
	if a.cfg.DryRun {
		a.log.Warn("dry run mode enabled, skipping sending certificate")
		return signedCertificate, nil
	}
	certificateHash, err := a.aggLayerClient.SendCertificate(signedCertificate)
	if err != nil {
		return nil, fmt.Errorf("error sending certificate: %w", err)
	}

	a.log.Debugf("certificate sent: Height: %d cert: %s", signedCertificate.Height, signedCertificate.Brief())

	raw, err := json.Marshal(signedCertificate)
	if err != nil {
		return nil, fmt.Errorf("error marshalling signed certificate. Cert:%s. Err: %w", signedCertificate.Brief(), err)
	}

	prevLER := common.BytesToHash(certificate.PrevLocalExitRoot[:])
	var finalizedL1InfoTreeRoot *common.Hash
	if certificateParams.L1InfoTreeRootFromWhichToProve != nil {
		finalizedL1InfoTreeRoot = &certificateParams.L1InfoTreeRootFromWhichToProve.Hash
	}

	certInfo := types.CertificateInfo{
		Height:                  certificate.Height,
		RetryCount:              certificateParams.RetryCount,
		CertificateID:           certificateHash,
		NewLocalExitRoot:        certificate.NewLocalExitRoot,
		PreviousLocalExitRoot:   &prevLER,
		FromBlock:               certificateParams.FromBlock,
		ToBlock:                 certificateParams.ToBlock,
		CreatedAt:               certificateParams.CreatedAt,
		UpdatedAt:               certificateParams.CreatedAt,
		AggchainProof:           certificateParams.AggchainProof,
		FinalizedL1InfoTreeRoot: finalizedL1InfoTreeRoot,
		SignedCertificate:       string(raw),
	}
	// TODO: Improve this case, if a cert is not save in the storage, we are going to settle a unknown certificate
	err = a.saveCertificateToStorage(ctx, certInfo, a.cfg.MaxRetriesStoreCertificate)
	if err != nil {
		a.log.Errorf("error saving certificate  to storage. Cert:%s Err: %w", certInfo.String(), err)
		return nil, fmt.Errorf("error saving last sent certificate %s in db: %w", certInfo.String(), err)
	}

	a.log.Infof("certificate: %s sent successfully for range of l2 blocks (from block: %d, to block: %d) cert:%s",
		certInfo.ID(), certificateParams.FromBlock, certificateParams.ToBlock, signedCertificate.Brief())

	return signedCertificate, nil
}
func (a *AggSender) isAllowedSendCertificateEpochPercent() bool {
	if a.cfg.MaxEpochPercentageAllowedToSendCertificate == 0 ||
		a.cfg.MaxEpochPercentageAllowedToSendCertificate >= maxPercent {
		return true
	}
	status := a.epochNotifier.GetEpochStatus()
	if status.PercentEpoch >= float64(a.cfg.MaxEpochPercentageAllowedToSendCertificate)/100.0 {
		a.log.Warnf("forbidden to send certificate after epoch percentage: %f", status.PercentEpoch)
		return false
	}
	return true
}

// saveCertificateToStorage saves the certificate to the storage
// it retries if it fails. if param retries == 0 it retries indefinitely
func (a *AggSender) saveCertificateToStorage(ctx context.Context, cert types.CertificateInfo, maxRetries int) error {
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

// signCertificate signs a certificate with the sequencer key
func (a *AggSender) signCertificate(certificate *agglayer.Certificate) (*agglayer.SignedCertificate, error) {
	hashToSign := certificate.HashToSign()

	sig, err := crypto.Sign(hashToSign.Bytes(), a.aggsenderKey)
	if err != nil {
		return nil, err
	}

	a.log.Infof("Signed certificate. sequencer address: %s. New local exit root: %s Hash signed: %s",
		crypto.PubkeyToAddress(a.aggsenderKey.PublicKey).String(),
		common.BytesToHash(certificate.NewLocalExitRoot[:]).String(),
		hashToSign.String(),
	)

	r, s, isOddParity, err := extractSignatureData(sig)
	if err != nil {
		return nil, err
	}

	return &agglayer.SignedCertificate{
		Certificate: certificate,
		Signature: &agglayer.Signature{
			R:         r,
			S:         s,
			OddParity: isOddParity,
		},
	}, nil
}

type checkCertResult struct {
	// existPendingCerts means that there are still pending certificates
	existPendingCerts bool
	// existNewInErrorCert means than in this run a cert pass from xxx to InError
	existNewInErrorCert bool
}

// checkPendingCertificatesStatus checks the status of pending certificates
// and updates in the storage if it changed on agglayer
// It returns:
// bool -> if there are pending certificates
func (a *AggSender) checkPendingCertificatesStatus(ctx context.Context) checkCertResult {
	pendingCertificates, err := a.storage.GetCertificatesByStatus(agglayer.NonSettledStatuses)
	if err != nil {
		a.log.Errorf("error getting pending certificates: %w", err)
		return checkCertResult{existPendingCerts: true, existNewInErrorCert: false}
	}

	a.log.Debugf("checkPendingCertificatesStatus num of pendingCertificates: %d", len(pendingCertificates))
	thereArePendingCerts := false
	appearsNewInErrorCert := false
	for _, certificateLocal := range pendingCertificates {
		certificateHeader, err := a.aggLayerClient.GetCertificateHeader(certificateLocal.CertificateID)
		if err != nil {
			a.log.Errorf("error getting certificate header of %s from agglayer: %w",
				certificateLocal.ID(), err)
			return checkCertResult{existPendingCerts: true, existNewInErrorCert: false}
		}

		a.log.Debugf("aggLayerClient.GetCertificateHeader status [%s] of certificate %s  elapsed time:%s",
			certificateHeader.Status,
			certificateHeader.ID(),
			certificateLocal.ElapsedTimeSinceCreation())
		appearsNewInErrorCert = appearsNewInErrorCert ||
			(!certificateLocal.Status.IsInError() && certificateHeader.Status.IsInError())

		if err := a.updateCertificateStatus(ctx, certificateLocal, certificateHeader); err != nil {
			a.log.Errorf("error updating certificate %s status in storage: %w", certificateHeader.String(), err)
			return checkCertResult{existPendingCerts: true, existNewInErrorCert: false}
		}

		if !certificateLocal.IsClosed() {
			a.log.Infof("certificate %s is still pending, elapsed time:%s ",
				certificateHeader.ID(), certificateLocal.ElapsedTimeSinceCreation())
			thereArePendingCerts = true
		}
	}
	return checkCertResult{existPendingCerts: thereArePendingCerts, existNewInErrorCert: appearsNewInErrorCert}
}

// updateCertificate updates the certificate status in the storage
func (a *AggSender) updateCertificateStatus(ctx context.Context,
	localCert *types.CertificateInfo,
	agglayerCert *agglayer.CertificateHeader) error {
	if localCert.Status == agglayerCert.Status {
		return nil
	}
	a.log.Infof("certificate %s changed status from [%s] to [%s] elapsed time: %s full_cert (agglayer): %s",
		localCert.ID(), localCert.Status, agglayerCert.Status, localCert.ElapsedTimeSinceCreation(),
		agglayerCert.String())

	// That is a strange situation
	if agglayerCert.Status.IsOpen() && localCert.Status.IsClosed() {
		a.log.Warnf("certificate %s is reopened! from [%s] to [%s]",
			localCert.ID(), localCert.Status, agglayerCert.Status)
	}

	localCert.Status = agglayerCert.Status
	localCert.UpdatedAt = uint32(time.Now().UTC().Unix())
	if err := a.storage.UpdateCertificate(ctx, *localCert); err != nil {
		a.log.Errorf("error updating certificate %s status in storage: %w", agglayerCert.ID(), err)
		return fmt.Errorf("error updating certificate. Err: %w", err)
	}
	return nil
}

// checkLastCertificateFromAgglayer checks the last certificate from agglayer
func (a *AggSender) checkLastCertificateFromAgglayer(ctx context.Context) error {
	networkID := a.l2Syncer.OriginNetwork()
	initialStatus, err := NewInitialStatus(a.log, networkID, a.storage, a.aggLayerClient)
	if err != nil {
		return fmt.Errorf("recovery: error retrieving initial status: %w", err)
	}
	initialStatus.LogData()
	action, err := initialStatus.Process()
	if err != nil {
		return fmt.Errorf("recovery: error processing initial status: %w", err)
	}
	return a.executeInitialStatusAction(ctx, action, initialStatus.LocalCert)
}

func (a *AggSender) executeInitialStatusAction(ctx context.Context,
	action *InitialStatusResult, localCert *types.CertificateInfo) error {
	a.log.Infof("recovery: action: %s", action.String())
	switch action.Action {
	case InitialStatusActionNone:
		a.log.Info("recovery: No certificates in local storage and agglayer: initial state")
	case InitialStatusActionUpdateCurrentCert:
		if err := a.updateCertificateStatus(ctx, localCert, action.Cert); err != nil {
			return fmt.Errorf("recovery: error updating local storage with agglayer certificate: %w", err)
		}
	case InitialStatusActionInsertNewCert:
		if _, err := a.updateLocalStorageWithAggLayerCert(ctx, action.Cert); err != nil {
			return fmt.Errorf("recovery: error new local storage with agglayer certificate: %w", err)
		}
	default:
		return fmt.Errorf("recovery: unknown action: %s", action.Action)
	}
	return nil
}

// updateLocalStorageWithAggLayerCert updates the local storage with the certificate from the AggLayer
func (a *AggSender) updateLocalStorageWithAggLayerCert(ctx context.Context,
	aggLayerCert *agglayer.CertificateHeader) (*types.CertificateInfo, error) {
	certInfo := NewCertificateInfoFromAgglayerCertHeader(aggLayerCert)
	a.log.Infof("setting initial certificate from AggLayer: %s", certInfo.String())
	return certInfo, a.storage.SaveLastSentCertificate(ctx, *certInfo)
}

// extractSignatureData extracts the R, S, and V from a 65-byte signature
func extractSignatureData(signature []byte) (r, s common.Hash, isOddParity bool, err error) {
	if len(signature) != signatureSize {
		err = errInvalidSignatureSize
		return
	}

	r = common.BytesToHash(signature[:32])   // First 32 bytes are R
	s = common.BytesToHash(signature[32:64]) // Next 32 bytes are S
	isOddParity = signature[64]%2 == 1       //nolint:mnd // Last byte is V

	return
}

func NewCertificateInfoFromAgglayerCertHeader(c *agglayer.CertificateHeader) *types.CertificateInfo {
	if c == nil {
		return nil
	}
	now := uint32(time.Now().UTC().Unix())
	meta := types.NewCertificateMetadataFromHash(c.Metadata)
	toBlock := meta.FromBlock + uint64(meta.Offset)
	createdAt := meta.CreatedAt

	if meta.Version < 1 {
		toBlock = meta.ToBlock
		createdAt = now
	}

	res := &types.CertificateInfo{
		Height:            c.Height,
		CertificateID:     c.CertificateID,
		NewLocalExitRoot:  c.NewLocalExitRoot,
		FromBlock:         meta.FromBlock,
		ToBlock:           toBlock,
		Status:            c.Status,
		CreatedAt:         createdAt,
		UpdatedAt:         now,
		SignedCertificate: "na/agglayer header",
	}
	if c.PreviousLocalExitRoot != nil {
		res.PreviousLocalExitRoot = c.PreviousLocalExitRoot
	}
	return res
}
