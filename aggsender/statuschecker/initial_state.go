package statuschecker

import (
	"context"
	"errors"
	"fmt"

	"github.com/agglayer/aggkit/agglayer"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/common"
)

var newInitialStatusFn = newInitialStatus

const (
	InitialStatusActionNone initialStatusAction = iota
	InitialStatusActionUpdateCurrentCert
	InitialStatusActionInsertNewCert
)

var ErrAgglayerInconsistence = errors.New("recovery: agglayer inconsistence")

type initialStatus struct {
	SettledCert *agglayertypes.CertificateHeader
	PendingCert *agglayertypes.CertificateHeader
	LocalCert   *types.CertificateHeader
	log         common.Logger
}

type initialStatusAction int

// String representation of the enum
func (i initialStatusAction) String() string {
	return [...]string{"None", "Update", "InsertNew"}[i]
}

type initialStatusResult struct {
	action  initialStatusAction
	message string
	cert    *agglayertypes.CertificateHeader
}

func (i *initialStatusResult) String() string {
	if i == nil {
		return types.NilStr
	}
	res := fmt.Sprintf("Action: %d, Message: %s", i.action, i.message)

	if i.cert != nil {
		res += fmt.Sprintf(", Cert: %s", i.cert.ID())
	} else {
		res += ", Cert: " + types.NilStr
	}
	return res
}

// newInitialStatus creates a new initialStatus object, get the data from AggLayer and local storage
func newInitialStatus(ctx context.Context,
	log types.Logger, networkID uint32,
	storage db.AggSenderStorage,
	aggLayerClient agglayer.AggLayerClientRecoveryQuerier) (*initialStatus, error) {
	log.Infof("recovery: checking last settled certificate from AggLayer for network %d", networkID)
	aggLayerLastSettledCert, err := aggLayerClient.GetLatestSettledCertificateHeader(ctx, networkID)
	if err != nil {
		return nil, fmt.Errorf("recovery: error getting GetLatestSettledCertificateHeader from agglayer: %w", err)
	}

	log.Infof("recovery: checking last pending certificate from AggLayer for network %d", networkID)
	aggLayerLastPendingCert, err := aggLayerClient.GetLatestPendingCertificateHeader(ctx, networkID)
	if err != nil {
		return nil, fmt.Errorf("recovery: error getting GetLatestPendingCertificateHeader from agglayer: %w", err)
	}

	localLastCert, err := storage.GetLastSentCertificateHeader()
	if err != nil {
		return nil, fmt.Errorf("recovery: error getting last sent certificate from local storage: %w", err)
	}
	return &initialStatus{
		SettledCert: aggLayerLastSettledCert, // from Agglayer
		PendingCert: aggLayerLastPendingCert, // from Agglayer
		LocalCert:   localLastCert,
		log:         log,
	}, nil
}

// logData logs the data from the initialStatus object
func (i *initialStatus) logData() {
	i.log.Infof("recovery: settled certificate from AggLayer: %s", i.SettledCert.ID())
	i.log.Infof("recovery: pending certificate from AggLayer: %s / status: %s",
		i.PendingCert.ID(), i.PendingCert.StatusString())
	i.log.Infof("recovery: certificate from Local           : %s / status: %s",
		i.LocalCert.ID(), i.LocalCert.StatusString())
}

// process checks the last certificates from agglayer vs local certificates and returns the action to take
func (i *initialStatus) process() (*initialStatusResult, error) {
	// Check that agglayer data is consistent.
	i.logData()
	if err := i.checkAgglayerConsistenceCerts(); err != nil {
		return nil, err
	}
	if i.LocalCert == nil && i.SettledCert == nil && i.PendingCert != nil {
		if i.PendingCert.Height == 0 {
			return &initialStatusResult{action: InitialStatusActionInsertNewCert,
				message: "no settled cert yet, and the pending cert have the correct height (0) so we use it",
				cert:    i.PendingCert}, nil
		}

		// We don't known if pendingCert is going to be Settled or InError.
		// We can't use it because maybe is error wrong height
		if !i.PendingCert.Status.IsInError() && i.PendingCert.Height > 0 {
			return nil, fmt.Errorf("recovery: pendingCert %s is in state %s but have a suspicious height, so we wait to finish",
				i.PendingCert.ID(), i.PendingCert.StatusString())
		}
		if i.PendingCert.Status.IsInError() && i.PendingCert.Height > 0 {
			return &initialStatusResult{action: InitialStatusActionNone,
				message: "the pending cert have wrong height and it's InError. We ignore it",
				cert:    nil}, nil
		}
	}
	aggLayerLastCert := i.getLatestAggLayerCert()
	i.log.Infof("recovery: last certificate from AggLayer: %s", aggLayerLastCert.String())
	localLastCert := i.LocalCert

	// CASE 1: No certificates in local storage and agglayer
	if localLastCert == nil && aggLayerLastCert == nil {
		return &initialStatusResult{action: InitialStatusActionNone,
			message: "no certificates in local storage and agglayer: initial state",
			cert:    nil}, nil
	}
	// CASE 2: No certificates in local storage but agglayer has one
	if localLastCert == nil && aggLayerLastCert != nil {
		return &initialStatusResult{action: InitialStatusActionInsertNewCert,
			message: "no certificates in local storage but agglayer have one (no InError)",
			cert:    aggLayerLastCert}, nil
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
		return &initialStatusResult{action: InitialStatusActionInsertNewCert,
			message: fmt.Sprintf("agglayer have next cert, storing cert: %s",
				aggLayerLastCert.ID()),
			cert: aggLayerLastCert}, nil
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
	return &initialStatusResult{action: InitialStatusActionUpdateCurrentCert,
		message: fmt.Sprintf("aggsender same cert, updating state: %s",
			aggLayerLastCert.ID()),
		cert: aggLayerLastCert}, nil
}

func (i *initialStatus) checkAgglayerConsistenceCerts() error {
	if i.PendingCert == nil {
		return nil
	}

	if i.SettledCert == nil {
		// If Height>0 and not inError, we have a problem. We should have a settled cert
		if !i.PendingCert.Status.IsInError() && i.PendingCert.Height != 0 {
			return fmt.Errorf("consistence: no settled cert, and pending one is height %d and not in error. Err: %w",
				i.PendingCert.Height, ErrAgglayerInconsistence)
		}
		return nil
	}

	// Both settled and pending cert != nil, that is the potential inconsistency
	// This is there is a settled cert for a height but also a pending cert for the same height
	if i.PendingCert.Height == i.SettledCert.Height &&
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

	return nil
}

func (i *initialStatus) getLatestAggLayerCert() *agglayertypes.CertificateHeader {
	if i.PendingCert == nil {
		return i.SettledCert
	}
	return i.PendingCert
}
