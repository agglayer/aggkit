package aggsender

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	jRPC "github.com/0xPolygon/cdk-rpc/rpc"
	zkevm "github.com/agglayer/aggkit"
	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/metrics"
	aggsenderrpc "github.com/agglayer/aggkit/aggsender/rpc"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db/compatibility"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/signer"
	"github.com/agglayer/aggkit/tree"
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
	log aggkitcommon.Logger

	l2Syncer         types.L2BridgeSyncer
	l1infoTreeSyncer types.L1InfoTreeSyncer
	epochNotifier    types.EpochNotifier

	storage                      db.AggSenderStorage
	aggLayerClient               agglayer.AgglayerClientInterface
	compatibilityStoragedChecker compatibility.CompatibilityChecker

	cfg Config

	signer signer.Signer

	status      types.AggsenderStatus
	rateLimiter RateLimiter
}

// New returns a new AggSender instance
func New(
	ctx context.Context,
	logger *log.Logger,
	cfg Config,
	aggLayerClient agglayer.AgglayerClientInterface,
	l1InfoTreeSyncer *l1infotreesync.L1InfoTreeSync,
	l2Syncer types.L2BridgeSyncer,
	epochNotifier types.EpochNotifier) (*AggSender, error) {
	storageConfig := db.AggSenderSQLStorageConfig{
		DBPath:                  cfg.StoragePath,
		KeepCertificatesHistory: cfg.KeepCertificatesHistory,
	}
	storage, err := db.NewAggSenderSQLStorage(logger, storageConfig)
	if err != nil {
		return nil, err
	}
	signer, err := signer.NewSigner(aggkitcommon.AGGSENDER, logger, ctx, cfg.AggsenderPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("error NewSigner. Err: %w", err)
	}
	err = signer.Initialize(ctx)
	if err != nil {
		return nil, fmt.Errorf("error signer.Initialize. Err: %w", err)
	}
	rateLimit := aggkitcommon.NewRateLimit(cfg.MaxSubmitCertificateRate)

	logger.Infof("Aggsender Config: %s.", cfg.String())

	compatibilityStoragedChecker := compatibility.NewCompatibilityCheck(
		cfg.RequireStorageContentCompatibility,
		func(ctx context.Context) (db.RuntimeData, error) {
			return db.RuntimeData{NetworkID: l2Syncer.OriginNetwork()}, nil
		},
		compatibility.NewKeyValueToCompatibilityStorage[db.RuntimeData](storage, aggkitcommon.AGGSENDER),
	)

	return &AggSender{
		cfg:                          cfg,
		log:                          logger,
		storage:                      storage,
		l2Syncer:                     l2Syncer,
		aggLayerClient:               aggLayerClient,
		l1infoTreeSyncer:             l1InfoTreeSyncer,
		signer:                       signer,
		epochNotifier:                epochNotifier,
		status:                       types.AggsenderStatus{Status: types.StatusNone},
		rateLimiter:                  rateLimit,
		compatibilityStoragedChecker: compatibilityStoragedChecker,
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
	metrics.Register()
	a.status.Start(time.Now().UTC())

	a.checkDBCompatibility(ctx)
	a.checkInitialStatus(ctx)
	a.sendCertificates(ctx, 0)
}
func (a *AggSender) checkDBCompatibility(ctx context.Context) {
	if a.compatibilityStoragedChecker == nil {
		a.log.Warnf("compatibilityStoragedChecker is nil, so we are not going to check the compatibility")
		return
	}
	if err := a.compatibilityStoragedChecker.Check(ctx, nil); err != nil {
		a.log.Fatalf("error checking compatibility data in DB, you can bypass this check using config file. Err: %w", err)
	}
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

	shouldSend, err := a.shouldSendCertificate()
	if err != nil {
		return nil, err
	}

	if !shouldSend {
		a.log.Infof("waiting for pending certificates to be settled")
		return nil, nil
	}

	start := time.Now()
	lastL2BlockSynced, err := a.l2Syncer.GetLastProcessedBlock(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting last processed block from l2: %w", err)
	}

	lastSentCertificateInfo, err := a.storage.GetLastSentCertificate()
	if err != nil {
		return nil, err
	}

	previousToBlock, retryCount := getLastSentBlockAndRetryCount(lastSentCertificateInfo)

	if previousToBlock >= lastL2BlockSynced {
		a.log.Infof("no new blocks to send a certificate, last certificate block: %d, last L2 block: %d",
			previousToBlock, lastL2BlockSynced)
		return nil, nil
	}

	fromBlock := previousToBlock + 1
	toBlock := lastL2BlockSynced

	bridges, err := a.l2Syncer.GetBridgesPublished(ctx, fromBlock, toBlock)
	if err != nil {
		return nil, fmt.Errorf("error getting bridges: %w", err)
	}

	if len(bridges) == 0 {
		a.log.Infof("no bridges consumed, no need to send a certificate from block: %d to block: %d", fromBlock, toBlock)
		return nil, nil
	}

	claims, err := a.l2Syncer.GetClaims(ctx, fromBlock, toBlock)
	if err != nil {
		return nil, fmt.Errorf("error getting claims: %w", err)
	}
	certificateParams := &types.CertificateBuildParams{
		FromBlock: fromBlock,
		ToBlock:   toBlock,
		Bridges:   bridges,
		Claims:    claims,
		CreatedAt: uint32(time.Now().UTC().Unix()),
	}

	certificateParams, err = a.limitCertSize(certificateParams)
	if err != nil {
		return nil, fmt.Errorf("error limitCertSize: %w", err)
	}
	a.log.Infof("building certificate for %s estimatedSize=%d",
		certificateParams.String(), certificateParams.EstimatedSize())

	certificate, err := a.buildCertificate(ctx, certificateParams, lastSentCertificateInfo)
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

	metrics.CertificateBuildTime(time.Since(start).Seconds())

	a.saveCertificateToFile(signedCertificate)
	a.log.Infof("certificate ready to be sent to AggLayer: %s", signedCertificate.Brief())
	if a.cfg.DryRun {
		a.log.Warn("dry run mode enabled, skipping sending certificate")
		return signedCertificate, nil
	}
	certificateHash, err := a.aggLayerClient.SendCertificate(signedCertificate)
	if err != nil {
		return nil, fmt.Errorf("error sending certificate: %w", err)
	}

	metrics.CertificateSent()
	a.log.Debugf("certificate send: Height: %d cert: %s", signedCertificate.Height, signedCertificate.Brief())

	raw, err := json.Marshal(signedCertificate)
	if err != nil {
		return nil, fmt.Errorf("error marshalling signed certificate. Cert:%s. Err: %w", signedCertificate.Brief(), err)
	}

	prevLER := common.BytesToHash(certificate.PrevLocalExitRoot[:])
	certInfo := types.CertificateInfo{
		Height:                certificate.Height,
		RetryCount:            retryCount,
		CertificateID:         certificateHash,
		NewLocalExitRoot:      certificate.NewLocalExitRoot,
		PreviousLocalExitRoot: &prevLER,
		FromBlock:             fromBlock,
		ToBlock:               toBlock,
		CreatedAt:             certificateParams.CreatedAt,
		UpdatedAt:             certificateParams.CreatedAt,
		SignedCertificate:     string(raw),
	}
	// TODO: Improve this case, if a cert is not save in the storage, we are going to settle a unknown certificate
	err = a.saveCertificateToStorage(ctx, certInfo, a.cfg.MaxRetriesStoreCertificate)
	if err != nil {
		a.log.Errorf("error saving certificate  to storage. Cert:%s Err: %w", certInfo.String(), err)
		return nil, fmt.Errorf("error saving last sent certificate %s in db: %w", certInfo.String(), err)
	}

	a.log.Infof("certificate: %s sent successfully for range of l2 blocks (from block: %d, to block: %d) cert:%s",
		certInfo.ID(), fromBlock, toBlock, signedCertificate.Brief())

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

func (a *AggSender) limitCertSize(fullCert *types.CertificateBuildParams) (*types.CertificateBuildParams, error) {
	currentCert := fullCert
	var previousCert *types.CertificateBuildParams
	var err error
	for {
		if currentCert.NumberOfBridges() == 0 {
			// We can't reduce more the certificate, so this is the minium size
			a.log.Warnf("We reach the minium size of bridge.Certificate size: %d >max size: %d",
				previousCert.EstimatedSize(), a.cfg.MaxCertSize)
			return previousCert, nil
		}

		if a.cfg.MaxCertSize == 0 || currentCert.EstimatedSize() < a.cfg.MaxCertSize {
			return currentCert, nil
		}

		// Minimum size of the certificate
		if currentCert.NumberOfBlocks() <= 1 {
			a.log.Warnf("reach the minium num blocks [%d to %d].Certificate size: %d >max size: %d",
				currentCert.FromBlock, currentCert.ToBlock, currentCert.EstimatedSize(), a.cfg.MaxCertSize)
			return currentCert, nil
		}
		previousCert = currentCert
		currentCert, err = currentCert.Range(currentCert.FromBlock, currentCert.ToBlock-1)
		if err != nil {
			return nil, fmt.Errorf("error reducing certificate: %w", err)
		}
	}
}

// saveCertificate saves the certificate to a tmp file
func (a *AggSender) saveCertificateToFile(signedCertificate *agglayer.SignedCertificate) {
	if signedCertificate == nil || a.cfg.SaveCertificatesToFilesPath == "" {
		return
	}
	fn := fmt.Sprintf("%s/certificate_%04d-%07d.json",
		a.cfg.SaveCertificatesToFilesPath, signedCertificate.Height, time.Now().Unix())
	a.log.Infof("saving certificate to file: %s", fn)
	jsonData, err := json.MarshalIndent(signedCertificate, "", "  ")
	if err != nil {
		a.log.Errorf("error marshalling certificate: %w", err)
	}

	if err = os.WriteFile(fn, jsonData, 0644); err != nil { //nolint:gosec,mnd // we are writing to a tmp file
		a.log.Errorf("error writing certificate to file: %w", err)
	}
}

// getNextHeightAndPreviousLER returns the height and previous LER for the new certificate
func (a *AggSender) getNextHeightAndPreviousLER(
	lastSentCertificateInfo *types.CertificateInfo) (uint64, common.Hash, error) {
	if lastSentCertificateInfo == nil {
		return 0, zeroLER, nil
	}
	if !lastSentCertificateInfo.Status.IsClosed() {
		return 0, zeroLER, fmt.Errorf("last certificate %s is not closed (status: %s)",
			lastSentCertificateInfo.ID(), lastSentCertificateInfo.Status.String())
	}
	if lastSentCertificateInfo.Status.IsSettled() {
		return lastSentCertificateInfo.Height + 1, lastSentCertificateInfo.NewLocalExitRoot, nil
	}

	if lastSentCertificateInfo.Status.IsInError() {
		// We can reuse last one of lastCert?
		if lastSentCertificateInfo.PreviousLocalExitRoot != nil {
			return lastSentCertificateInfo.Height, *lastSentCertificateInfo.PreviousLocalExitRoot, nil
		}
		// Is the first one, so we can set the zeroLER
		if lastSentCertificateInfo.Height == 0 {
			return 0, zeroLER, nil
		}
		// We get previous certificate that must be settled
		a.log.Debugf("last certificate %s is in error, getting previous settled certificate height:%d",
			lastSentCertificateInfo.Height-1)
		lastSettleCert, err := a.storage.GetCertificateByHeight(lastSentCertificateInfo.Height - 1)
		if err != nil {
			return 0, common.Hash{}, fmt.Errorf("error getting last settled certificate: %w", err)
		}
		if lastSettleCert == nil {
			return 0, common.Hash{}, fmt.Errorf("none settled certificate: %w", err)
		}
		if !lastSettleCert.Status.IsSettled() {
			return 0, common.Hash{}, fmt.Errorf("last settled certificate %s is not settled (status: %s)",
				lastSettleCert.ID(), lastSettleCert.Status.String())
		}

		return lastSentCertificateInfo.Height, lastSettleCert.NewLocalExitRoot, nil
	}
	return 0, zeroLER, fmt.Errorf("last certificate %s has an unknown status: %s",
		lastSentCertificateInfo.ID(), lastSentCertificateInfo.Status.String())
}

// buildCertificate builds a certificate from the bridge events
func (a *AggSender) buildCertificate(ctx context.Context,
	certParams *types.CertificateBuildParams,
	lastSentCertificateInfo *types.CertificateInfo) (*agglayer.Certificate, error) {
	if certParams.IsEmpty() {
		return nil, errNoBridgesAndClaims
	}

	bridgeExits := a.getBridgeExits(certParams.Bridges)
	importedBridgeExits, err := a.getImportedBridgeExits(ctx, certParams.Claims)
	if err != nil {
		return nil, fmt.Errorf("error getting imported bridge exits: %w", err)
	}

	depositCount := certParams.MaxDepositCount()

	exitRoot, err := a.l2Syncer.GetExitRootByIndex(ctx, depositCount)
	if err != nil {
		return nil, fmt.Errorf("error getting exit root by index: %d. Error: %w", depositCount, err)
	}

	height, previousLER, err := a.getNextHeightAndPreviousLER(lastSentCertificateInfo)
	if err != nil {
		return nil, fmt.Errorf("error getting next height and previous LER: %w", err)
	}

	meta := types.NewCertificateMetadata(
		certParams.FromBlock,
		uint32(certParams.ToBlock-certParams.FromBlock),
		certParams.CreatedAt,
	)

	return &agglayer.Certificate{
		NetworkID:           a.l2Syncer.OriginNetwork(),
		PrevLocalExitRoot:   previousLER,
		NewLocalExitRoot:    exitRoot.Hash,
		BridgeExits:         bridgeExits,
		ImportedBridgeExits: importedBridgeExits,
		Height:              height,
		Metadata:            meta.ToHash(),
	}, nil
}

// createCertificateMetadata creates the metadata for the certificate
// it returns: newMetadata + bool if the metadata is hashed or not
func convertBridgeMetadata(metadata []byte, importedBridgeMetadataAsHash bool) ([]byte, bool) {
	var metaData []byte
	var isMetadataHashed bool
	if importedBridgeMetadataAsHash && len(metadata) > 0 {
		metaData = crypto.Keccak256(metadata)
		isMetadataHashed = true
	} else {
		metaData = metadata
		isMetadataHashed = false
	}
	return metaData, isMetadataHashed
}

// convertClaimToImportedBridgeExit converts a claim to an ImportedBridgeExit object
func (a *AggSender) convertClaimToImportedBridgeExit(claim bridgesync.Claim) (*agglayer.ImportedBridgeExit, error) {
	leafType := agglayer.LeafTypeAsset
	if claim.IsMessage {
		leafType = agglayer.LeafTypeMessage
	}
	metaData, isMetadataIsHashed := convertBridgeMetadata(claim.Metadata, a.cfg.BridgeMetadataAsHash)

	bridgeExit := &agglayer.BridgeExit{
		LeafType: leafType,
		TokenInfo: &agglayer.TokenInfo{
			OriginNetwork:      claim.OriginNetwork,
			OriginTokenAddress: claim.OriginAddress,
		},
		DestinationNetwork: claim.DestinationNetwork,
		DestinationAddress: claim.DestinationAddress,
		Amount:             claim.Amount,
		IsMetadataHashed:   isMetadataIsHashed,
		Metadata:           metaData,
	}

	mainnetFlag, rollupIndex, leafIndex, err := bridgesync.DecodeGlobalIndex(claim.GlobalIndex)
	if err != nil {
		return nil, fmt.Errorf("error decoding global index: %w", err)
	}

	return &agglayer.ImportedBridgeExit{
		BridgeExit: bridgeExit,
		GlobalIndex: &agglayer.GlobalIndex{
			MainnetFlag: mainnetFlag,
			RollupIndex: rollupIndex,
			LeafIndex:   leafIndex,
		},
	}, nil
}

// getBridgeExits converts bridges to agglayer.BridgeExit objects
func (a *AggSender) getBridgeExits(bridges []bridgesync.Bridge) []*agglayer.BridgeExit {
	bridgeExits := make([]*agglayer.BridgeExit, 0, len(bridges))

	for _, bridge := range bridges {
		metaData, isMetadataHashed := convertBridgeMetadata(bridge.Metadata, a.cfg.BridgeMetadataAsHash)
		bridgeExits = append(bridgeExits, &agglayer.BridgeExit{
			LeafType: agglayer.LeafType(bridge.LeafType),
			TokenInfo: &agglayer.TokenInfo{
				OriginNetwork:      bridge.OriginNetwork,
				OriginTokenAddress: bridge.OriginAddress,
			},
			DestinationNetwork: bridge.DestinationNetwork,
			DestinationAddress: bridge.DestinationAddress,
			Amount:             bridge.Amount,
			IsMetadataHashed:   isMetadataHashed,
			Metadata:           metaData,
		})
	}

	return bridgeExits
}

// getImportedBridgeExits converts claims to agglayer.ImportedBridgeExit objects and calculates necessary proofs
func (a *AggSender) getImportedBridgeExits(
	ctx context.Context, claims []bridgesync.Claim,
) ([]*agglayer.ImportedBridgeExit, error) {
	if len(claims) == 0 {
		// no claims to convert
		return []*agglayer.ImportedBridgeExit{}, nil
	}

	var (
		greatestL1InfoTreeIndexUsed uint32
		importedBridgeExits         = make([]*agglayer.ImportedBridgeExit, 0, len(claims))
		claimL1Info                 = make([]*l1infotreesync.L1InfoTreeLeaf, 0, len(claims))
	)

	for _, claim := range claims {
		info, err := a.l1infoTreeSyncer.GetInfoByGlobalExitRoot(claim.GlobalExitRoot)
		if err != nil {
			return nil, fmt.Errorf("error getting info by global exit root: %w", err)
		}

		claimL1Info = append(claimL1Info, info)

		if info.L1InfoTreeIndex > greatestL1InfoTreeIndexUsed {
			greatestL1InfoTreeIndexUsed = info.L1InfoTreeIndex
		}
	}

	rootToProve, err := a.l1infoTreeSyncer.GetL1InfoTreeRootByIndex(ctx, greatestL1InfoTreeIndexUsed)
	if err != nil {
		return nil, fmt.Errorf("error getting L1 Info tree root by index: %d. Error: %w", greatestL1InfoTreeIndexUsed, err)
	}

	for i, claim := range claims {
		l1Info := claimL1Info[i]

		a.log.Debugf("claim[%d]: destAddr: %s GER: %s Block: %d Pos: %d GlobalIndex: 0x%x",
			i, claim.DestinationAddress.String(), claim.GlobalExitRoot.String(),
			claim.BlockNum, claim.BlockPos, claim.GlobalIndex)
		ibe, err := a.convertClaimToImportedBridgeExit(claim)
		if err != nil {
			return nil, fmt.Errorf("error converting claim to imported bridge exit: %w", err)
		}

		importedBridgeExits = append(importedBridgeExits, ibe)

		gerToL1Proof, err := a.l1infoTreeSyncer.GetL1InfoTreeMerkleProofFromIndexToRoot(
			ctx, l1Info.L1InfoTreeIndex, rootToProve.Hash,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"error getting L1 Info tree merkle proof for leaf index: %d and root: %s. Error: %w",
				l1Info.L1InfoTreeIndex, rootToProve.Hash, err,
			)
		}

		claim := claims[i]
		if ibe.GlobalIndex.MainnetFlag {
			ibe.ClaimData = &agglayer.ClaimFromMainnnet{
				L1Leaf: &agglayer.L1InfoTreeLeaf{
					L1InfoTreeIndex: l1Info.L1InfoTreeIndex,
					RollupExitRoot:  claim.RollupExitRoot,
					MainnetExitRoot: claim.MainnetExitRoot,
					Inner: &agglayer.L1InfoTreeLeafInner{
						GlobalExitRoot: l1Info.GlobalExitRoot,
						Timestamp:      l1Info.Timestamp,
						BlockHash:      l1Info.PreviousBlockHash,
					},
				},
				ProofLeafMER: &agglayer.MerkleProof{
					Root:  claim.MainnetExitRoot,
					Proof: claim.ProofLocalExitRoot,
				},
				ProofGERToL1Root: &agglayer.MerkleProof{
					Root:  rootToProve.Hash,
					Proof: gerToL1Proof,
				},
			}
		} else {
			ibe.ClaimData = &agglayer.ClaimFromRollup{
				L1Leaf: &agglayer.L1InfoTreeLeaf{
					L1InfoTreeIndex: l1Info.L1InfoTreeIndex,
					RollupExitRoot:  claim.RollupExitRoot,
					MainnetExitRoot: claim.MainnetExitRoot,
					Inner: &agglayer.L1InfoTreeLeafInner{
						GlobalExitRoot: l1Info.GlobalExitRoot,
						Timestamp:      l1Info.Timestamp,
						BlockHash:      l1Info.PreviousBlockHash,
					},
				},
				ProofLeafLER: &agglayer.MerkleProof{
					Root: tree.CalculateRoot(ibe.BridgeExit.Hash(),
						claim.ProofLocalExitRoot, ibe.GlobalIndex.LeafIndex),
					Proof: claim.ProofLocalExitRoot,
				},
				ProofLERToRER: &agglayer.MerkleProof{
					Root:  claim.RollupExitRoot,
					Proof: claim.ProofRollupExitRoot,
				},
				ProofGERToL1Root: &agglayer.MerkleProof{
					Root:  rootToProve.Hash,
					Proof: gerToL1Proof,
				},
			}
		}
	}

	return importedBridgeExits, nil
}

// signCertificate signs a certificate with the sequencer key
func (a *AggSender) signCertificate(certificate *agglayer.Certificate) (*agglayer.SignedCertificate, error) {
	hashToSign := certificate.HashToSign()
	sig, err := a.signer.SignHash(context.Background(), hashToSign)
	if err != nil {
		return nil, err
	}

	a.log.Infof("Signed certificate. sequencer address: %s. New local exit root: %s Hash signed: %s",
		a.signer.PublicAddress().String(),
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

	switch agglayerCert.Status {
	case agglayer.Settled:
		metrics.Settled()
	case agglayer.InError:
		metrics.InError()
	}

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

// shouldSendCertificate checks if a certificate should be sent at given time
// if we have pending certificates, then we wait until they are settled
func (a *AggSender) shouldSendCertificate() (bool, error) {
	pendingCertificates, err := a.storage.GetCertificatesByStatus(agglayer.NonSettledStatuses)
	if err != nil {
		return false, fmt.Errorf("error getting pending certificates: %w", err)
	}

	return len(pendingCertificates) == 0, nil
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

// getLastSentBlockAndRetryCount returns the last sent block of the last sent certificate
// if there is no previosly sent certificate, it returns 0 and 0
func getLastSentBlockAndRetryCount(lastSentCertificateInfo *types.CertificateInfo) (uint64, int) {
	if lastSentCertificateInfo == nil {
		return 0, 0
	}

	retryCount := 0
	lastSentBlock := lastSentCertificateInfo.ToBlock

	if lastSentCertificateInfo.Status == agglayer.InError {
		// if the last certificate was in error, we need to resend it
		// from the block before the error
		if lastSentCertificateInfo.FromBlock > 0 {
			lastSentBlock = lastSentCertificateInfo.FromBlock - 1
		}

		retryCount = lastSentCertificateInfo.RetryCount + 1
		metrics.SendingRetry()
	}

	return lastSentBlock, retryCount
}
