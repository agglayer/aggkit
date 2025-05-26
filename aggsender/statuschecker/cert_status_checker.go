package statuschecker

import (
	"context"
	"fmt"
	"time"

	"github.com/agglayer/aggkit/agglayer"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/metrics"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/log"
)

var (
	naAgglayerHeader                                = "na/agglayer header"
	_                types.CertificateStatusChecker = (*certStatusChecker)(nil)
)

// certStatusChecker is a struct responsible for checking the status of certificates.
// It provides functionality to interact with the storage layer, communicate with the
// aggregation layer client, and log relevant information.
type certStatusChecker struct {
	log            *log.Logger
	storage        db.AggSenderStorage
	agglayerClient agglayer.AgglayerClientInterface

	l2OriginNetwork uint32
}

// NewCertStatusChecker creates a new instance of a CertificateStatusChecker.
// It initializes the checker with the provided logger, storage, Agglayer client,
// and the L2 origin network identifier.
//
// Parameters:
//   - log: Logger instance for logging messages.
//   - storage: Interface for accessing the AggSender storage.
//   - agglayerClient: Client interface for interacting with the Agglayer.
//   - l2OriginNetwork: Identifier for the L2 origin network.
//
// Returns:
//
//	A types.CertificateStatusChecker instance configured with the provided parameters.
func NewCertStatusChecker(
	log *log.Logger,
	storage db.AggSenderStorage,
	agglayerClient agglayer.AgglayerClientInterface,
	l2OriginNetwork uint32,
) types.CertificateStatusChecker {
	return &certStatusChecker{
		log:             log,
		storage:         storage,
		agglayerClient:  agglayerClient,
		l2OriginNetwork: l2OriginNetwork,
	}
}

// CheckInitialStatus checks the initial status of pending certificates and the last certificate
// from the aggregation layer. It retries the status check at regular intervals specified by
// delayBetweenRetries until it succeeds or the context is canceled. If an error occurs during
// the status check, it logs the error and retries after the specified delay.
//
// Parameters:
//   - ctx: The context used to manage the lifecycle of the status check operation.
//   - delayBetweenRetries: The duration to wait between retry attempts.
//   - aggsenderStatus: A pointer to an AggsenderStatus object where the last error encountered
//     during the status check will be recorded.
//
// Behavior:
//   - Continuously checks the status of pending certificates and the last certificate from the
//     aggregation layer.
//   - Logs errors and retries the operation if an error occurs.
//   - Stops retrying and exits if the context is canceled or the status check succeeds.
func (c *certStatusChecker) CheckInitialStatus(
	ctx context.Context,
	delayBetweenRetries time.Duration,
	aggsenderStatus *types.AggsenderStatus) {
	ticker := time.NewTicker(delayBetweenRetries)
	defer ticker.Stop()

	for {
		c.CheckPendingCertificatesStatus(ctx)
		err := c.checkLastCertificateFromAgglayer(ctx)
		aggsenderStatus.SetLastError(err)
		if err != nil {
			c.log.Errorf("error checking initial status: %w, retrying in %s", err, delayBetweenRetries.String())
		} else {
			c.log.Info("Initial status checked successfully")
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// CheckPendingCertificatesStatus checks the status of pending certificates
// and updates in the storage if it changed on agglayer
// It returns:
// bool -> if there are pending certificates
func (c *certStatusChecker) CheckPendingCertificatesStatus(ctx context.Context) types.CertStatus {
	pendingCertificates, err := c.storage.GetCertificateHeadersByStatus(agglayertypes.NonSettledStatuses)
	if err != nil {
		c.log.Errorf("error getting pending certificates: %w", err)
		return types.CertStatus{ExistPendingCerts: true, ExistNewInErrorCert: false}
	}

	c.log.Debugf("checkPendingCertificatesStatus num of pendingCertificates: %d", len(pendingCertificates))
	thereArePendingCerts := false
	appearsNewInErrorCert := false
	for _, certificateLocal := range pendingCertificates {
		certificateHeader, err := c.agglayerClient.GetCertificateHeader(ctx, certificateLocal.CertificateID)
		if err != nil {
			c.log.Errorf("error getting certificate header of %s from agglayer: %w",
				certificateLocal.ID(), err)
			return types.CertStatus{ExistPendingCerts: true, ExistNewInErrorCert: false}
		}

		c.log.Debugf("agglayerClient.GetCertificateHeader status [%s] of certificate %s  elapsed time:%s",
			certificateHeader.Status,
			certificateHeader.ID(),
			certificateLocal.ElapsedTimeSinceCreation())
		appearsNewInErrorCert = appearsNewInErrorCert ||
			(!certificateLocal.Status.IsInError() && certificateHeader.Status.IsInError())

		if err := c.updateCertificateStatus(ctx, certificateLocal, certificateHeader); err != nil {
			c.log.Errorf("error updating certificate %s status in storage: %w", certificateHeader.String(), err)
			return types.CertStatus{ExistPendingCerts: true, ExistNewInErrorCert: false}
		}

		if !certificateLocal.IsClosed() {
			c.log.Infof("certificate %s is still pending, elapsed time:%s ",
				certificateHeader.ID(), certificateLocal.ElapsedTimeSinceCreation())
			thereArePendingCerts = true
		}
	}
	return types.CertStatus{
		ExistPendingCerts:   thereArePendingCerts,
		ExistNewInErrorCert: appearsNewInErrorCert,
	}
}

// updateCertificate updates the certificate status in the storage
func (c *certStatusChecker) updateCertificateStatus(ctx context.Context,
	localCert *types.CertificateHeader,
	agglayerCert *agglayertypes.CertificateHeader) error {
	if localCert.Status == agglayerCert.Status {
		return nil
	}
	c.log.Infof("certificate %s changed status from [%s] to [%s] elapsed time: %s full_cert (agglayer): %s",
		localCert.ID(), localCert.Status, agglayerCert.Status, localCert.ElapsedTimeSinceCreation(),
		agglayerCert.String())

	switch agglayerCert.Status {
	case agglayertypes.Settled:
		metrics.Settled()
	case agglayertypes.InError:
		metrics.InError()
	}

	// That is a strange situation
	if agglayerCert.Status.IsOpen() && localCert.Status.IsClosed() {
		c.log.Warnf("certificate %s is reopened! from [%s] to [%s]",
			localCert.ID(), localCert.Status, agglayerCert.Status)
	}

	localCert.Status = agglayerCert.Status
	localCert.UpdatedAt = uint32(time.Now().UTC().Unix())
	if err := c.storage.UpdateCertificateStatus(
		ctx,
		localCert.CertificateID,
		localCert.Status,
		localCert.UpdatedAt); err != nil {
		c.log.Errorf("error updating certificate %s status in storage: %w", agglayerCert.ID(), err)
		return fmt.Errorf("error updating certificate. Err: %w", err)
	}
	return nil
}

// checkLastCertificateFromAgglayer checks the last certificate from agglayer
func (c *certStatusChecker) checkLastCertificateFromAgglayer(ctx context.Context) error {
	initialStatus, err := newInitialStatusFn(ctx, c.log, c.l2OriginNetwork, c.storage, c.agglayerClient)
	if err != nil {
		return fmt.Errorf("recovery: error retrieving initial status: %w", err)
	}
	initialStatus.logData()
	action, err := initialStatus.process()
	if err != nil {
		return fmt.Errorf("recovery: error processing initial status: %w", err)
	}
	return c.executeInitialStatusAction(ctx, action, initialStatus.LocalCert)
}

func (c *certStatusChecker) executeInitialStatusAction(ctx context.Context,
	action *initialStatusResult, localCert *types.CertificateHeader) error {
	c.log.Infof("recovery: action: %s", action.String())
	switch action.action {
	case InitialStatusActionNone:
		c.log.Info("recovery: No certificates in local storage and agglayer: initial state")
	case InitialStatusActionUpdateCurrentCert:
		if err := c.updateCertificateStatus(ctx, localCert, action.cert); err != nil {
			return fmt.Errorf("recovery: error updating local storage with agglayer certificate: %w", err)
		}
	case InitialStatusActionInsertNewCert:
		if _, err := c.updateLocalStorageWithAggLayerCert(ctx, action.cert); err != nil {
			return fmt.Errorf("recovery: error new local storage with agglayer certificate: %w", err)
		}
	default:
		return fmt.Errorf("recovery: unknown action: %s", action.action)
	}
	return nil
}

// updateLocalStorageWithAggLayerCert updates the local storage with the certificate from the AggLayer
func (c *certStatusChecker) updateLocalStorageWithAggLayerCert(ctx context.Context,
	aggLayerCert *agglayertypes.CertificateHeader) (*types.Certificate, error) {
	cert, err := newCertificateInfoFromAgglayerCertHeader(aggLayerCert)
	if err != nil {
		return nil, fmt.Errorf("error creating certificate from AggLayer header: %w", err)
	}
	if cert == nil {
		return nil, nil
	}

	c.log.Infof("setting initial certificate from AggLayer: %s", cert.String())
	return cert, c.storage.SaveLastSentCertificate(ctx, *cert)
}

func newCertificateInfoFromAgglayerCertHeader(c *agglayertypes.CertificateHeader) (*types.Certificate, error) {
	if c == nil {
		return nil, nil
	}
	now := uint32(time.Now().UTC().Unix())
	meta := types.NewCertificateMetadataFromHash(c.Metadata)
	var (
		toBlock   uint64
		createdAt uint32
		certType  types.CertificateType
	)

	switch meta.Version {
	case types.CertificateMetadataV0:
		toBlock = meta.ToBlock
		createdAt = now
		certType = types.CertificateTypeUnknown
	case types.CertificateMetadataV1:
		toBlock = meta.FromBlock + uint64(meta.Offset)
		createdAt = meta.CreatedAt
		certType = types.CertificateTypeUnknown
	case types.CertificateMetadataV2:
		toBlock = meta.FromBlock + uint64(meta.Offset)
		createdAt = meta.CreatedAt
		certType = types.NewCertificateTypeFromInt(meta.CertType)
	default:
		return nil, fmt.Errorf("newCertificateInfoFromAgglayerCertHeader."+
			" Unsupported certificate metadata version: %d", meta.Version)
	}

	res := &types.Certificate{
		Header: &types.CertificateHeader{
			Height:           c.Height,
			CertificateID:    c.CertificateID,
			NewLocalExitRoot: c.NewLocalExitRoot,
			FromBlock:        meta.FromBlock,
			ToBlock:          toBlock,
			Status:           c.Status,
			CreatedAt:        createdAt,
			UpdatedAt:        now,
			CertType:         certType,
			CertSource:       types.CertificateSourceAggLayer,
		},
		SignedCertificate: &naAgglayerHeader,
	}

	if c.PreviousLocalExitRoot != nil {
		res.Header.PreviousLocalExitRoot = c.PreviousLocalExitRoot
	}

	return res, nil
}
