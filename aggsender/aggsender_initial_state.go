package aggsender

import (
	"fmt"

	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/types"
)

type InitialStatus struct {
	SettledCert *agglayer.CertificateHeader
	PendingCert *agglayer.CertificateHeader
	LocalCert   *types.CertificateInfo
	log         types.Logger
}

type InitialStatusAction int

const (
	InitialStatusActionNone InitialStatusAction = iota
	InitialStatusActionUpdateCurrentCert
	InitialStatusActionInsertNewCert
)

// String representation of the enum
func (i InitialStatusAction) String() string {
	return [...]string{"None", "Update", "InsertNew"}[i]
}

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

func (i *InitialStatusResult) String() string {
	if i == nil {
		return "nil"
	}
	res := fmt.Sprintf("Action: %d, Message: %s", i.Action, i.Message)

	if i.Cert != nil {
		res += fmt.Sprintf(", Cert: %s", i.Cert.ID())
	} else {
		res += ", Cert: nil"
	}
	return res
}

// NewInitialStatus creates a new InitialStatus object, get the data from AggLayer and local storage
func NewInitialStatus(log types.Logger, networkID uint32, storage db.AggSenderStorage, aggLayerClient agglayer.AggLayerClientRecoveryQuerier) (*InitialStatus, error) {
	log.Infof("recovery: checking last settled certificate from AggLayer for network %d", networkID)
	aggLayerLastSettledCert, err := aggLayerClient.GetLatestSettledCertificateHeader(networkID)
	if err != nil {
		return nil, fmt.Errorf("recovery: error getting GetLatestSettledCertificateHeader from agglayer: %w", err)
	}

	log.Infof("recovery: checking last pending certificate from AggLayer for network %d", networkID)
	aggLayerLastPendingCert, err := aggLayerClient.GetLatestPendingCertificateHeader(networkID)
	if err != nil {
		return nil, fmt.Errorf("recovery: error getting GetLatestPendingCertificateHeader from agglayer: %w", err)
	}

	localLastCert, err := storage.GetLastSentCertificate()
	if err != nil {
		return nil, fmt.Errorf("recovery: error getting last sent certificate from local storage: %w", err)
	}
	return &InitialStatus{
		SettledCert: aggLayerLastSettledCert, // from Agglayer
		PendingCert: aggLayerLastPendingCert, // from Agglayer
		LocalCert:   localLastCert,
		log:         log,
	}, nil
}

// LogData logs the data from the InitialStatus object
func (i *InitialStatus) LogData() {
	i.log.Infof("recovery: last settled certificate from AggLayer: %s", i.SettledCert.ID())
	i.log.Infof("recovery: last pending certificate from AggLayer: %s / status: %s", i.PendingCert.ID(), i.PendingCert.StatusString())
	i.log.Infof("recovery: last certificate from Local           : %s / status: %s", i.LocalCert.ID(), i.LocalCert.StatusString())
}

// checkLastCertificateFromAgglayer checks the last certificate from agglayer
func (i *InitialStatus) Process2() (*InitialStatusResult, error) {
	// Check that agglayer data is consistent.
	if err := i.checkAgglayerConsistenceCerts(); err != nil {
		return nil, err
	}
	if i.LocalCert == nil && i.SettledCert == nil && i.PendingCert != nil {
		if i.PendingCert.Height == 0 {
			return &InitialStatusResult{Action: InitialStatusActionInsertNewCert,
				Message: "no settled cert yet, and the pending cert have the righ height(0) so we use it",
				Cert:    i.PendingCert}, nil
		}

		// We don't known if pendingCert is going to be settle or error.
		// We can't use it becasue maybe is error wrong height
		if !i.PendingCert.Status.IsInError() && i.PendingCert.Height > 0 {
			return nil, fmt.Errorf("recovery: pendingCert %s is in state %s but have a suspicious height, so we wait to finish",
				i.PendingCert.ID(), i.PendingCert.StatusString())
		}
		if i.PendingCert.Status.IsInError() && i.PendingCert.Height > 0 {
			return &InitialStatusResult{Action: InitialStatusActionNone,
				Message: "the pending cert have wrong height and it's on error. We ignore it",
				Cert:    nil}, nil
		}
	}
	aggLayerLastCert := i.getLastAggLayerCert()
	i.log.Infof("recovery: last certificate from AggLayer: %s", aggLayerLastCert.String())
	localLastCert := i.LocalCert

	// CASE 1: No certificates in local storage and agglayer
	if localLastCert == nil && aggLayerLastCert == nil {
		return &InitialStatusResult{Action: InitialStatusActionNone,
			Message: "no certificates in local storage and agglayer: initial state",
			Cert:    nil}, nil
	}
	// CASE 2: No certificates in local storage but agglayer has one
	if localLastCert == nil && aggLayerLastCert != nil {
		return &InitialStatusResult{Action: InitialStatusActionInsertNewCert,
			Message: "no certificates in local storage but agglayer have one (no InError)",
			Cert:    aggLayerLastCert}, nil
	}
	// CASE 2.1: certificate in storage but not in agglayer
	// this is a non-sense, so throw an error
	if localLastCert != nil && aggLayerLastCert == nil {
		return nil, fmt.Errorf("recovery: certificate exists in storage but not in agglayer. Inconsistency")
	}
	// CASE 3.1: the certificate on the agglayer has less height than the one stored in the local storage
	if aggLayerLastCert.Height < localLastCert.Height {
		return nil, fmt.Errorf("recovery: the last certificate in the agglayer has less height (%d) "+
			"than the one in the local storage (%d)", aggLayerLastCert.Height, localLastCert.Height)
	}
	// CASE 3.2: aggsender stopped between sending to agglayer and storing to the local storage
	if aggLayerLastCert.Height == localLastCert.Height+1 {
		// we need to store the certificate in the local storage.
		return &InitialStatusResult{Action: InitialStatusActionInsertNewCert,
			Message: fmt.Sprintf("aggsender stopped between sending to agglayer and storing to the local storage: storing cert: %s",
				aggLayerLastCert.ID()),
			Cert: aggLayerLastCert}, nil
	}
	// CASE 4: AggSender and AggLayer are not on the same page
	// note: we don't need to check individual fields of the certificate
	// because CertificateID is a hash of all the fields
	if localLastCert.CertificateID != aggLayerLastCert.CertificateID {
		return nil, fmt.Errorf("recovery: Local certificate:\n %s \n is different from agglayer certificate:\n %s",
			localLastCert.String(), aggLayerLastCert.String())
	}
	// CASE 5: AggSender and AggLayer are at same page
	// just update status
	return &InitialStatusResult{Action: InitialStatusActionUpdateCurrentCert,
		Message: fmt.Sprintf("aggsender same cert, updating state: %s",
			aggLayerLastCert.ID()),
		Cert: aggLayerLastCert}, nil
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
	if i.PendingCert != nil && i.SettledCert == nil {
		// If Height>0 and not inError, we have a problem. We should have a settled cert
		if !i.PendingCert.Status.IsInError() && i.PendingCert.Height != 0 {
			return fmt.Errorf("consistence: no settled cert, and pending one is height %d and not in error. Err: %w", i.PendingCert.Height, ErrorAgglayerInconsistence)
		}
		return nil
	}

	// If only settled cert, there no chance of inconsistency
	if i.PendingCert == nil && i.SettledCert != nil {
		return nil
	}

	// If both settled and pending cert, that is the potential inconsistency
	if i.SettledCert != nil && i.PendingCert != nil {
		// This is there is a settled cert for a height but also a pending cert for the same height
		if i.PendingCert.Height == i.SettledCert.Height &&
			i.SettledCert.CertificateID != i.PendingCert.CertificateID &&
			!i.SettledCert.Status.IsInError() {
			return fmt.Errorf("consistence: settled (%s) and pending (%s) certs are different for same height. Err: %w",
				i.SettledCert.ID(), i.PendingCert.ID(),
				ErrorAgglayerInconsistence)
		}
		//
		if i.SettledCert.Height > i.PendingCert.Height && !i.SettledCert.Status.IsInError() {
			return fmt.Errorf("settled cert height %s is higher than pending cert height %s that is inNoError. Err: %w",
				i.SettledCert.ID(), i.PendingCert.ID(),
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
	return i.PendingCert == nil && i.SettledCert == nil
}

func (i *InitialStatus) isEmptyLocal() bool {
	return i.LocalCert == nil
}

func (i *InitialStatus) isLocalAndAggLayerSameHeight() bool {
	agglayerCert := i.getLastAggLayerCert()
	return agglayerCert != nil && agglayerCert.Height == i.LocalCert.Height
}

func (i *InitialStatus) getLastAggLayerCert() *agglayer.CertificateHeader {
	if i.PendingCert == nil {
		return i.SettledCert
	}
	return i.PendingCert
	// // If the lastCert is not inError we return it as last one
	// if !i.PendingCert.Status.IsInError() {
	// 	return i.PendingCert
	// }
	// if i.PendingCert.Status.IsInError() && i.LocalCert != nil && i.LocalCert.CertificateID == i.PendingCert.CertificateID {
	// 	return i.PendingCert
	// }
	//return i.SettledCert
}

func (i *InitialStatus) thereAreNoInErrorAggLayerCert() bool {
	return i.getNoInErrorAggLayerCert() != nil
}

func (i *InitialStatus) getNoInErrorAggLayerCert() *agglayer.CertificateHeader {
	if i.PendingCert != nil && !i.PendingCert.Status.IsInError() {
		return i.PendingCert
	}
	if i.SettledCert != nil && !i.SettledCert.Status.IsInError() {
		return i.SettledCert
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
