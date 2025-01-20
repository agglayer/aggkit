package aggsender

import (
	"fmt"

	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/types"
)

type InitialStatus struct {
	AggLayerLastSettledCert *agglayer.CertificateHeader
	AggLayerLastPendingCert *agglayer.CertificateHeader
	LocalCert               *types.CertificateInfo
	log                     types.Logger
}

type InitialStatusAction int

const (
	InitialStatusActionNone InitialStatusAction = iota
	InitialStatusActionUpdateCurrentCert
	InitialStatusActionInsertNewCert
)

var (
	ErrorAgglayerInconsistence         = fmt.Errorf("recovery: agglayer incosistence")
	ErrorMismatchStateAgglayerAndLocal = fmt.Errorf("recovery: mismatch between local and agglayer certificates")
	ErrorUnknownCase                   = fmt.Errorf("recovery: unknown case")
)

type InitialStatusResult struct {
	Action  InitialStatusAction
	Message string
	Cert    *agglayer.CertificateHeader
}

// NewInitialStatus creates a new InitialStatus object, get the data from AggLayer and local storage
func NewInitialStatus(log types.Logger, networkID uint32, storage db.AggSenderStorage, aggLayerClient agglayer.AggLayerClientRecoveryQuerier) (*InitialStatus, error) {
	log.Infof("recovery: checking last certificate from AggLayer for network %d", networkID)
	aggLayerLastSettledCert, err := aggLayerClient.GetLatestSettledCertificateHeader(networkID)
	if err != nil {
		return nil, fmt.Errorf("recovery: error getting GetLatestSettledCertificateHeader from agglayer: %w", err)
	}

	aggLayerLastPendingCert, err := aggLayerClient.GetLatestPendingCertificateHeader(networkID)
	if err != nil {
		return nil, fmt.Errorf("recovery: error getting GetLatestPendingCertificateHeader from agglayer: %w", err)
	}

	localLastCert, err := storage.GetLastSentCertificate()
	if err != nil {
		return nil, fmt.Errorf("recovery: error getting last sent certificate from local storage: %w", err)
	}
	return &InitialStatus{
		AggLayerLastSettledCert: aggLayerLastSettledCert,
		AggLayerLastPendingCert: aggLayerLastPendingCert,
		LocalCert:               localLastCert,
		log:                     log,
	}, nil
}

// LogData logs the data from the InitialStatus object
func (i *InitialStatus) LogData() {
	i.log.Infof("recovery: last settled certificate from AggLayer: %s", i.AggLayerLastSettledCert.ID())
	i.log.Infof("recovery: last pending certificate from AggLayer: %s / status: %s", i.AggLayerLastPendingCert.ID(), i.AggLayerLastPendingCert.StatusString())
	i.log.Infof("recovery: last certificate from Local           : %s / status: %s", i.LocalCert.ID(), i.LocalCert.StatusString())
}

// Process checks the data from the InitialStatus object and returns the action to be taken or error
func (i *InitialStatus) Process() (*InitialStatusResult, error) {
	// CASE1: Same page
	if i.isLocalAndAggLayerEqual() {
		// same page, nothing to do
		return &InitialStatusResult{Action: InitialStatusActionNone, Message: "agglayer and aggsender same cert"}, nil
	}
	// Check that agglayer data is consistent.
	if err := i.checkAgglayerConsistenceCerts(); err != nil {
		return nil, err
	}
	if err := i.checkAgglayerAndAggsenderConsistenceCerts(); err != nil {
		return nil, err
	}
	// CASE 3.2: aggsender stopped between sending to agglayer and storing to the local storage
	if i.hasAggLayerNextCert() {
		cert := i.getLastAggLayerCert()
		return &InitialStatusResult{Action: InitialStatusActionInsertNewCert,
			Message: "aggsender stopped between sending to agglayer and storing to the local storage",
			Cert:    cert}, nil
	}

	// CASE 2: No certificates in local storage but agglayer has one (no InError)
	if i.isEmptyLocal() && i.thereAreNoInErrorAggLayerCert() {
		cert := i.getNoInErrorAggLayerCert()
		return &InitialStatusResult{Action: InitialStatusActionInsertNewCert,
			Message: "no certificates in local storage but agglayer have one (no InError)",
			Cert:    cert}, nil
	}
	// CASE 2.1: certificate in storage but not in agglayer
	// CASE 3.1: the certificate on the agglayer has less height than the one stored in the local storage
	if i.isLocalCertIsNewerThanAggLayer() {
		return nil, fmt.Errorf("recovery: certificate in storage is newer than agglayer. Inconsistency. Err:%w", ErrorMismatchStateAgglayerAndLocal)
	}

	// CASE 5: AggSender and AggLayer are at same page
	// just update status
	if i.isLocalAndAggLayerSameCert() {
		return &InitialStatusResult{Action: InitialStatusActionInsertNewCert,
			Message: "agglayer and aggsender have same cert, just update status",
			Cert:    i.getLastAggLayerCert()}, nil
	} else {
		// CASE 4: AggSender and AggLayer are not on the same page
		// It's not next cert because this case is already handled
		cert := i.getNoInErrorAggLayerCert()
		return nil,
			fmt.Errorf("recovery: Local certificate:\n %s \n is different from agglayer certificate:\n %s. Err:%w",
				i.LocalCert.String(), cert.String(), ErrorMismatchStateAgglayerAndLocal)
	}
	// case localEmpty && aggLayer InError
}

func (i *InitialStatus) checkAgglayerAndAggsenderConsistenceCerts() error {
	// If no local cert, nothing to check
	if i.isEmptyLocal() {
		return nil
	}
	// If we have a final cert state but agglayer is not in error, we have a problem
	if !i.LocalCert.Status.IsClosed() && i.isEmptyAggLayer() {
		return fmt.Errorf("no agglayer  cert, but therea are a local cert %s in state %s. Err: %w", i.LocalCert.ID(), i.LocalCert.Status.String(), ErrorMismatchStateAgglayerAndLocal)
	}
	// local cert heigth > agglayer cert height
	if i.isLocalCertIsNewerThanAggLayer() {
		return fmt.Errorf("certificate in storage is newer than agglayer. Inconsistency. Err:%w", ErrorMismatchStateAgglayerAndLocal)
	}
	return nil

}

func (i *InitialStatus) checkAgglayerConsistenceCerts() error {
	if i.isEmptyAggLayer() {
		return nil
	}
	if i.AggLayerLastPendingCert != nil || i.AggLayerLastSettledCert == nil {
		if !i.AggLayerLastPendingCert.Status.IsInError() && i.AggLayerLastPendingCert.Height != 0 {
			return fmt.Errorf("no settled cert, and pending one is height %d and not in error. Err: %w", i.AggLayerLastPendingCert.Height, ErrorAgglayerInconsistence)
		}
		return nil
	}
	if i.AggLayerLastSettledCert != nil || i.AggLayerLastPendingCert != nil {
		if i.AggLayerLastPendingCert.Height == i.AggLayerLastSettledCert.Height &&
			i.AggLayerLastSettledCert.CertificateID != i.AggLayerLastPendingCert.CertificateID &&
			!i.AggLayerLastSettledCert.Status.IsInError() {
			return fmt.Errorf("settled (%s) and pending (%s) certs are different for same height. Err: %w",
				i.AggLayerLastSettledCert.ID(), i.AggLayerLastPendingCert.ID(),
				ErrorAgglayerInconsistence)
		}
	}
	return nil
}

func (i *InitialStatus) isLocalAndAggLayerSameCert() bool {
	agglayerCert := i.getLastAggLayerCert()
	if agglayerCert == nil && i.LocalCert == nil {
		return true
	}
	if agglayerCert == nil || i.LocalCert == nil {
		return false
	}
	return agglayerCert.Height == i.LocalCert.Height && agglayerCert.CertificateID == i.LocalCert.CertificateID
}

// isLocalAndAggLayerEqual checks if the local and aggLayer are in the same page:
// - both empty
// - both have the same certificate / status
func (i *InitialStatus) isLocalAndAggLayerEqual() bool {
	if i.isEmptyAggLayer() && i.isEmptyLocal() {
		return true
	}
	agglayerCert := i.getLastAggLayerCert()
	return certEqual(agglayerCert, i.LocalCert, false)
}

func (i *InitialStatus) isEmptyAggLayer() bool {
	return i.AggLayerLastPendingCert == nil && i.AggLayerLastSettledCert == nil
}

func (i *InitialStatus) isEmptyLocal() bool {
	return i.LocalCert == nil
}

func (i *InitialStatus) isLocalAndAggLayerSameHeight() bool {
	agglayerCert := i.getLastAggLayerCert()
	return agglayerCert != nil && agglayerCert.Height == i.LocalCert.Height
}

func (i *InitialStatus) getLastAggLayerCert() *agglayer.CertificateHeader {
	if i.AggLayerLastPendingCert == nil {
		return i.AggLayerLastSettledCert
	}
	// If the lastCert is not inError we return it as last one
	if !i.AggLayerLastPendingCert.Status.IsInError() {
		return i.AggLayerLastPendingCert
	}
	if i.AggLayerLastPendingCert.Status.IsInError() && i.LocalCert != nil && i.LocalCert.CertificateID == i.AggLayerLastPendingCert.CertificateID {
		return i.AggLayerLastPendingCert
	}
	return i.AggLayerLastSettledCert
}

func (i *InitialStatus) thereAreNoInErrorAggLayerCert() bool {
	return i.getNoInErrorAggLayerCert() != nil
}

func (i *InitialStatus) getNoInErrorAggLayerCert() *agglayer.CertificateHeader {
	if i.AggLayerLastPendingCert != nil && !i.AggLayerLastPendingCert.Status.IsInError() {
		return i.AggLayerLastPendingCert
	}
	if i.AggLayerLastSettledCert != nil && !i.AggLayerLastSettledCert.Status.IsInError() {
		return i.AggLayerLastSettledCert
	}
	return nil
}

func (i *InitialStatus) isLocalCertIsNewerThanAggLayer() bool {
	aggLayerCert := i.getLastAggLayerCert()
	return aggLayerCert != nil && i.LocalCert.Height > aggLayerCert.Height
}

func (i *InitialStatus) hasAggLayerNextCert() bool {
	aggLayerCert := i.getLastAggLayerCert()
	nextHeight := uint64(0)
	if i.LocalCert != nil {
		nextHeight = i.LocalCert.Height + 1
	}
	return aggLayerCert != nil && i.getLastAggLayerCert().Height == nextHeight
}

func certEqual(agglayerCert *agglayer.CertificateHeader,
	LocalCert *types.CertificateInfo, ignoreStatus bool) bool {
	if agglayerCert == nil && LocalCert == nil {
		return true
	}
	if agglayerCert == nil || LocalCert == nil {
		return false
	}
	res := agglayerCert.Height == LocalCert.Height && agglayerCert.CertificateID == LocalCert.CertificateID
	if ignoreStatus {
		return res
	}
	return res && agglayerCert.Status == LocalCert.Status
}
