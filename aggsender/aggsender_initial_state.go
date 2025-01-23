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

	nilStr = "nil"
)

// String representation of the enum
func (i InitialStatusAction) String() string {
	return [...]string{"None", "Update", "InsertNew"}[i]
}

var (
	ErrAgglayerInconsistence         = fmt.Errorf("recovery: agglayer inconsistence")
	ErrMismatchStateAgglayerAndLocal = fmt.Errorf("recovery: mismatch between local and agglayer certificates")
	ErrUnknownCase                   = fmt.Errorf("recovery: unknown case")
)

type InitialStatusResult struct {
	Action  InitialStatusAction
	Message string
	Cert    *agglayer.CertificateHeader
}

func (i *InitialStatusResult) String() string {
	if i == nil {
		return nilStr
	}
	res := fmt.Sprintf("Action: %d, Message: %s", i.Action, i.Message)

	if i.Cert != nil {
		res += fmt.Sprintf(", Cert: %s", i.Cert.ID())
	} else {
		res += ", Cert: " + nilStr
	}
	return res
}

// NewInitialStatus creates a new InitialStatus object, get the data from AggLayer and local storage
func NewInitialStatus(log types.Logger, networkID uint32,
	storage db.AggSenderStorage,
	aggLayerClient agglayer.AggLayerClientRecoveryQuerier) (*InitialStatus, error) {
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
	i.log.Infof("recovery: settled certificate from AggLayer: %s", i.SettledCert.ID())
	i.log.Infof("recovery: pending certificate from AggLayer: %s / st: %s",
		i.PendingCert.ID(), i.PendingCert.StatusString())
	i.log.Infof("recovery: certificate from Local           : %s / status: %s",
		i.LocalCert.ID(), i.LocalCert.StatusString())
}

// Process checks the last certificates from agglayer vs local certificates and returns the action to take
func (i *InitialStatus) Process() (*InitialStatusResult, error) {
	// Check that agglayer data is consistent.
	if err := i.checkAgglayerConsistenceCerts(); err != nil {
		return nil, err
	}
	if i.LocalCert == nil && i.SettledCert == nil && i.PendingCert != nil {
		if i.PendingCert.Height == 0 {
			return &InitialStatusResult{Action: InitialStatusActionInsertNewCert,
				Message: "no settled cert yet, and the pending cert have the correct height (0) so we use it",
				Cert:    i.PendingCert}, nil
		}

		// We don't known if pendingCert is going to be Settled or InError.
		// We can't use it because maybe is error wrong height
		if !i.PendingCert.Status.IsInError() && i.PendingCert.Height > 0 {
			return nil, fmt.Errorf("recovery: pendingCert %s is in state %s but have a suspicious height, so we wait to finish",
				i.PendingCert.ID(), i.PendingCert.StatusString())
		}
		if i.PendingCert.Status.IsInError() && i.PendingCert.Height > 0 {
			return &InitialStatusResult{Action: InitialStatusActionNone,
				Message: "the pending cert have wrong height and it's InError. We ignore it",
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
			Message: fmt.Sprintf("agglayer have next cert, storing cert: %s",
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

func (i *InitialStatus) checkAgglayerConsistenceCerts() error {
	if i.PendingCert == nil && i.SettledCert == nil {
		return nil
	}
	if i.PendingCert != nil && i.SettledCert == nil {
		// If Height>0 and not inError, we have a problem. We should have a settled cert
		if !i.PendingCert.Status.IsInError() && i.PendingCert.Height != 0 {
			return fmt.Errorf("consistence: no settled cert, and pending one is height %d and not in error. Err: %w",
				i.PendingCert.Height, ErrAgglayerInconsistence)
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
				ErrAgglayerInconsistence)
		}
		//
		if i.SettledCert.Height > i.PendingCert.Height && !i.SettledCert.Status.IsInError() {
			return fmt.Errorf("settled cert height %s is higher than pending cert height %s that is inNoError. Err: %w",
				i.SettledCert.ID(), i.PendingCert.ID(),
				ErrAgglayerInconsistence)
		}
	}
	return nil
}

func (i *InitialStatus) getLastAggLayerCert() *agglayer.CertificateHeader {
	if i.PendingCert == nil {
		return i.SettledCert
	}
	return i.PendingCert
}
