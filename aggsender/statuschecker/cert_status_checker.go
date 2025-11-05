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
	certQuerier    types.CertificateQuerier

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
	certQuerier types.CertificateQuerier,
	l2OriginNetwork uint32,
) types.CertificateStatusChecker {
	return &certStatusChecker{
		log:             log,
		storage:         storage,
		certQuerier:     certQuerier,
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

// CheckPeriodicallyCertificateStatus checks the status of pending certificates
// and the last certificate from the aggregation layer.
// It returns the status of pending certificates and any error encountered
// while checking the last certificate from the aggregation layer.
func (c *certStatusChecker) CheckPeriodicallyCertificateStatus(
	ctx context.Context,
) (types.CertStatus, error) {
	// Don't need to check the returned value here
	c.CheckPendingCertificatesStatus(ctx)
	err := c.checkLastCertificateFromAgglayer(ctx)
	return c.CheckPendingCertificatesStatus(ctx), err
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
			certificateLocal.ElapsedTimeSinceCreationString())
		appearsNewInErrorCert = appearsNewInErrorCert ||
			(!certificateLocal.Status.IsInError() && certificateHeader.Status.IsInError())

		if err := c.updateCertificateStatus(ctx, certificateLocal, certificateHeader); err != nil {
			c.log.Errorf("error updating certificate %s status in storage: %w", certificateHeader.String(), err)
			return types.CertStatus{ExistPendingCerts: true, ExistNewInErrorCert: false}
		}

		if !certificateLocal.IsClosed() {
			c.log.Debugf("certificate %s is still pending, elapsed time:%s ",
				certificateHeader.ID(), certificateLocal.ElapsedTimeSinceCreationString())
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
		localCert.ID(), localCert.Status, agglayerCert.Status, localCert.ElapsedTimeSinceCreationString(),
		agglayerCert.String())

	switch agglayerCert.Status {
	case agglayertypes.Settled:
		metrics.Settled()
		t := localCert.ElapsedTimeSinceCreation()
		if t > 0 {
			// log certificate settlement time only if we have a set creation time
			// it can be 0 only if the certificate was synced from agglayer
			metrics.CertificateSettlementTime(t.Seconds())
		}
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
	actions, err := initialStatus.process()
	if err != nil {
		return fmt.Errorf("recovery: error processing initial status: %w", err)
	}

	for _, action := range actions {
		if err := c.executeInitialStatusAction(ctx, action, initialStatus.LocalLastCert); err != nil {
			return fmt.Errorf("recovery: error executing initial status action: %w", err)
		}
	}

	c.log.Info("recovery: initial status actions executed successfully")

	return nil
}

func (c *certStatusChecker) executeInitialStatusAction(ctx context.Context,
	action *initialStatusResult, localCert *types.CertificateHeader) error {
	c.log.Infof("recovery: action: %s", action.String())
	switch action.action {
	case InitialStatusActionNone:
		c.log.Info("recovery: no action needed")
	case InitialStatusActionUpdateCurrentCert:
		if err := c.updateCertificateStatus(ctx, localCert, action.cert); err != nil {
			return fmt.Errorf("recovery: error updating local storage with agglayer certificate: %w", err)
		}
	case InitialStatusActionInsertNewCert:
		if action.cert.Status.IsInError() {
			// we will not save the last certificate if it is in error on startup
			// it will be rebuilt by the aggsender and sent again
			// we only care about the settled certificates on startup, since we
			// can not deduce the block range easily from a non settled certificate
			// gotten from the agglayer
			return nil
		}

		if action.cert.Status.IsOpen() {
			// if the certificate is still pending, we need to wait for it to be settled
			// before we can save it to the local storage, so we return an error here, and it will be retried in the main loop
			// of the status checker in CheckInitialStatus function
			// we do this because, it is not easy to deduce the block range from a non settled certificate
			return fmt.Errorf("recovery: we have a non settled certificate %s on startup. Waiting for it to be settled",
				action.cert.ID())
		}

		if _, err := c.updateLocalStorageWithSettledAggLayerCert(ctx, action.cert); err != nil {
			return fmt.Errorf("recovery: error new local storage with agglayer certificate: %w", err)
		}
	default:
		return fmt.Errorf("recovery: unknown action: %s", action.action)
	}
	return nil
}

// updateLocalStorageWithSettledAggLayerCert updates the local storage with the
// settled certificate from the AggLayer
func (c *certStatusChecker) updateLocalStorageWithSettledAggLayerCert(ctx context.Context,
	aggLayerCert *agglayertypes.CertificateHeader) (*types.Certificate, error) {
	cert, err := c.newSettledCertificateInfoFromAgglayerCertHeader(ctx, aggLayerCert)
	if err != nil {
		return nil, fmt.Errorf("error creating certificate from AggLayer header: %w", err)
	}

	c.log.Infof("setting initial certificate from AggLayer: %s", cert.String())
	return cert, c.storage.SaveOrUpdateCertificate(ctx, *cert)
}

func (c *certStatusChecker) newSettledCertificateInfoFromAgglayerCertHeader(
	ctx context.Context,
	cert *agglayertypes.CertificateHeader) (*types.Certificate, error) {
	if cert == nil {
		return nil, nil
	}

	toBlock, err := c.certQuerier.GetLastSettledCertificateToBlock(ctx, cert)
	if err != nil {
		return nil, fmt.Errorf("error getting last settled certificate to block: %w", err)
	}

	res := &types.Certificate{
		Header: &types.CertificateHeader{
			Height:           cert.Height,
			CertificateID:    cert.CertificateID,
			NewLocalExitRoot: cert.NewLocalExitRoot,
			Status:           cert.Status,
			CertSource:       types.CertificateSourceAggLayer,
			CertType:         c.certQuerier.CalculateCertificateTypeFromToBlock(toBlock),
			ToBlock:          toBlock,
			FromBlock:        0, // We don't have block range in the header and we don't use the metadata anymore
			CreatedAt:        0, // We don't have creation time in the header and we don't use the metadata anymore
			UpdatedAt:        0, // We don't have creation time in the header and we don't use the metadata anymore
		},
		SignedCertificate: &naAgglayerHeader,
	}

	if cert.PreviousLocalExitRoot != nil {
		res.Header.PreviousLocalExitRoot = cert.PreviousLocalExitRoot
	}

	return res, nil
}
