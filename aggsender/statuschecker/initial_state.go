package statuschecker

import (
	"context"
	"errors"
	"fmt"

	"github.com/agglayer/aggkit/agglayer"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitdb "github.com/agglayer/aggkit/db"
)

const (
	InitialStatusActionNone initialStatusAction = iota
	InitialStatusActionUpdateCurrentCert
	InitialStatusActionInsertNewCert
	InitialStatusActionDeleteLocalCert

	conflictingStatusResultsCapacity = 2
)

var (
	newInitialStatusFn = newInitialStatus

	resultTypes                  = []agglayertypes.CertificateStatus{agglayertypes.Pending, agglayertypes.Settled}
	initialStatusResultsCapacity = len(resultTypes)

	ErrAgglayerInconsistence = errors.New("recovery: agglayer inconsistence")
)

type initialStatus struct {
	AgglayerLastSettledCert *agglayertypes.CertificateHeader
	AgglayerLastPendingCert *agglayertypes.CertificateHeader
	LocalLastCert           *types.CertificateHeader
	LocalLastSettledCert    *types.CertificateHeader
}

type initialStatusAction int

// String representation of the enum
func (i initialStatusAction) String() string {
	return [...]string{"None", "Update", "InsertNew", "DeleteLocal"}[i]
}

type initialStatusResult struct {
	action  initialStatusAction
	message string
	cert    *agglayertypes.CertificateHeader
	height  uint64
	warning string
}

func newInitialStatusResult(
	action initialStatusAction,
	message string,
	cert *agglayertypes.CertificateHeader) *initialStatusResult {
	return &initialStatusResult{
		action:  action,
		message: message,
		cert:    cert,
	}
}

func newInitialStatusDeleteResult(height uint64, message string) *initialStatusResult {
	return &initialStatusResult{
		action:  InitialStatusActionDeleteLocalCert,
		message: message,
		height:  height,
	}
}

func (i *initialStatusResult) withWarning(warning string) *initialStatusResult {
	i.warning = warning
	return i
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
	if i.action == InitialStatusActionDeleteLocalCert {
		res += fmt.Sprintf(", Height: %d", i.height)
	}
	if i.warning != "" {
		res += fmt.Sprintf(", Warning: %s", i.warning)
	}
	return res
}

// newInitialStatus creates a new initialStatus object, get the data from AggLayer and local storage
func newInitialStatus(ctx context.Context,
	logFn types.EmitLogFunc, networkID uint32,
	storage db.AggSenderStorage,
	aggLayerClient agglayer.AggLayerClientRecoveryQuerier) (*initialStatus, error) {
	logFn("recovery: checking last settled certificate from AggLayer for network %d", networkID)
	aggLayerLastSettledCert, err := aggLayerClient.GetLatestSettledCertificateHeader(ctx, networkID)
	if err != nil {
		return nil, fmt.Errorf("recovery: error getting GetLatestSettledCertificateHeader from agglayer: %w", err)
	}

	logFn("recovery: checking last pending certificate from AggLayer for network %d", networkID)
	aggLayerLastPendingCert, err := aggLayerClient.GetLatestPendingCertificateHeader(ctx, networkID)
	if err != nil {
		return nil, fmt.Errorf("recovery: error getting GetLatestPendingCertificateHeader from agglayer: %w", err)
	}

	localLastCert, err := storage.GetLastSentCertificateHeader()
	if err != nil {
		return nil, fmt.Errorf("recovery: error getting last sent certificate from local storage: %w", err)
	}

	localSettledCert, err := storage.GetLastSettledCertificate()
	if err != nil && !errors.Is(err, aggkitdb.ErrNotFound) {
		return nil, fmt.Errorf("recovery: error getting last settled certificate from local storage: %w", err)
	}

	return &initialStatus{
		AgglayerLastSettledCert: aggLayerLastSettledCert, // from Agglayer
		AgglayerLastPendingCert: aggLayerLastPendingCert, // from Agglayer
		LocalLastSettledCert:    localSettledCert,        // from local storage
		LocalLastCert:           localLastCert,           // from local storage
	}, nil
}

// logData logs the data from the initialStatus object
func (i *initialStatus) logData(logFn types.EmitLogFunc) {
	logFn("recovery: settled certificate from AggLayer: %s", i.AgglayerLastSettledCert.ID())
	logFn("recovery: pending certificate from AggLayer: %s / status: %s",
		i.AgglayerLastPendingCert.ID(), i.AgglayerLastPendingCert.StatusString())
	logFn("recovery: certificate from Local           : %s / status: %s",
		i.LocalLastCert.ID(), i.LocalLastCert.StatusString())
}

// process processes the initial status and returns the actions to take
// It checks the consistency of the agglayer data, processes the pending certificate,
// and processes the settled certificate. It returns a slice of initialStatusResult
// which contains the action to take, a message, and the certificate if applicable.
func (i *initialStatus) process() ([]*initialStatusResult, error) {
	// Check that agglayer data is consistent.
	if err := i.checkAgglayerConsistenceCerts(); err != nil {
		return nil, err
	}

	results := make([]*initialStatusResult, 0, initialStatusResultsCapacity)

	pendingCertActions, err := i.processLastLocalCert()
	if err != nil {
		return nil, fmt.Errorf("recovery: failed processing pending certificate: %w", err)
	}

	if pendingCertActions != nil {
		results = append(results, pendingCertActions...)
	}

	settledCertActions := i.processLastSettledCert()
	if settledCertActions != nil {
		results = append(results, settledCertActions...)
	}

	return results, nil
}

// processLastLocalCert checks the last certificates from agglayer vs local certificates and returns the action to take
func (i *initialStatus) processLastLocalCert() ([]*initialStatusResult, error) {
	if i.LocalLastCert == nil && i.AgglayerLastSettledCert == nil && i.AgglayerLastPendingCert != nil {
		if i.AgglayerLastPendingCert.Height == 0 {
			return []*initialStatusResult{newInitialStatusResult(
				InitialStatusActionInsertNewCert,
				"no settled cert yet, and the pending cert have the correct height (0) so we use it",
				i.AgglayerLastPendingCert,
			)}, nil
		}

		// We don't known if pendingCert is going to be Settled or InError.
		// We can't use it because maybe is error wrong height
		if !i.AgglayerLastPendingCert.Status.IsInError() && i.AgglayerLastPendingCert.Height > 0 {
			return nil, fmt.Errorf("recovery: pendingCert %s is in state %s but have a suspicious height, so we wait to finish",
				i.AgglayerLastPendingCert.ID(), i.AgglayerLastPendingCert.StatusString())
		}
		if i.AgglayerLastPendingCert.Status.IsInError() && i.AgglayerLastPendingCert.Height > 0 {
			return []*initialStatusResult{newInitialStatusResult(
				InitialStatusActionNone,
				"the pending cert have wrong height and it's InError. We ignore it",
				nil,
			)}, nil
		}
	}
	aggLayerLastCert := i.getLatestAggLayerCert()
	localLastCert := i.LocalLastCert

	// CASE 1: No certificates in local storage and agglayer
	if localLastCert == nil && aggLayerLastCert == nil {
		return []*initialStatusResult{newInitialStatusResult(
			InitialStatusActionNone,
			"no certificates in local storage and agglayer: initial state",
			nil,
		)}, nil
	}
	// CASE 2: No certificates in local storage but agglayer has one
	if localLastCert == nil && aggLayerLastCert != nil {
		return []*initialStatusResult{newInitialStatusResult(
			InitialStatusActionInsertNewCert,
			"no certificates in local storage but agglayer have one (no InError)",
			aggLayerLastCert,
		)}, nil
	}

	// CASE 2.1: certificate in storage but not in agglayer.
	// AggLayer is the source of truth, so drop the stale local cert and continue.
	if localLastCert != nil && aggLayerLastCert == nil {
		return []*initialStatusResult{
			newInitialStatusDeleteResult(
				localLastCert.Height,
				fmt.Sprintf(
					"agglayer has no certificate for local height %d, deleting stale local certificate",
					localLastCert.Height,
				),
			).withWarning(fmt.Sprintf(
				"local latest certificate %s is not present in agglayer; agglayer is the source of truth",
				localLastCert.ID(),
			)),
		}, nil
	}
	// CASE 3.1: the certificate on the agglayer has less height than the one stored in the local storage.
	// Delete the stale local tip and let the next recovery iteration continue reconciling if needed.
	if aggLayerLastCert.Height < localLastCert.Height {
		return []*initialStatusResult{
			newInitialStatusDeleteResult(
				localLastCert.Height,
				fmt.Sprintf("agglayer latest certificate is height %d, deleting stale local height %d",
					aggLayerLastCert.Height, localLastCert.Height),
			).withWarning(fmt.Sprintf(
				"local latest certificate %s is ahead of agglayer certificate %s; agglayer is the source of truth",
				localLastCert.ID(), aggLayerLastCert.ID(),
			)),
		}, nil
	}
	// CASE 3.2: aggsender stopped between sending to agglayer and storing to the local storage
	if aggLayerLastCert.Height == localLastCert.Height+1 {
		// we need to store the certificate in the local storage.
		return []*initialStatusResult{newInitialStatusResult(
			InitialStatusActionInsertNewCert,
			fmt.Sprintf("agglayer have next cert, storing cert: %s",
				aggLayerLastCert.ID()),
			aggLayerLastCert,
		)}, nil
	}
	// CASE 4: AggSender and AggLayer are not on the same page.
	// AggLayer is authoritative, so reconcile local state to the AggLayer view.
	if localLastCert.CertificateID != aggLayerLastCert.CertificateID {
		results := make([]*initialStatusResult, 0, conflictingStatusResultsCapacity)
		warning := fmt.Sprintf(
			"local latest certificate %s does not match agglayer certificate %s at height %d; agglayer is the source of truth",
			localLastCert.ID(), aggLayerLastCert.ID(), aggLayerLastCert.Height,
		)
		if localLastCert.Height == aggLayerLastCert.Height {
			results = append(results, newInitialStatusDeleteResult(
				localLastCert.Height,
				fmt.Sprintf("replacing conflicting local certificate at height %d with agglayer certificate %s",
					localLastCert.Height, aggLayerLastCert.ID()),
			).withWarning(warning))
		}
		results = append(results, newInitialStatusResult(
			InitialStatusActionInsertNewCert,
			fmt.Sprintf("syncing local certificate state to agglayer certificate %s", aggLayerLastCert.ID()),
			aggLayerLastCert,
		).withWarning(warning))
		return results, nil
	}
	// CASE 5: AggSender and AggLayer are at same page
	// just update status
	return []*initialStatusResult{newInitialStatusResult(
		InitialStatusActionUpdateCurrentCert,
		fmt.Sprintf("aggsender same cert, updating state: %s",
			aggLayerLastCert.ID()),
		aggLayerLastCert,
	)}, nil
}

func (i *initialStatus) checkAgglayerConsistenceCerts() error {
	if i.AgglayerLastPendingCert == nil {
		return nil
	}

	if i.AgglayerLastSettledCert == nil {
		// If Height>0 and not inError, we have a problem. We should have a settled cert
		if !i.AgglayerLastPendingCert.Status.IsInError() && i.AgglayerLastPendingCert.Height != 0 {
			return fmt.Errorf("consistence: no settled cert, and pending one is height %d and not in error. Err: %w",
				i.AgglayerLastPendingCert.Height, ErrAgglayerInconsistence)
		}
		return nil
	}

	// Both settled and pending cert != nil, that is the potential inconsistency
	// This is there is a settled cert for a height but also a pending cert for the same height
	if i.AgglayerLastPendingCert.Height == i.AgglayerLastSettledCert.Height &&
		!i.AgglayerLastSettledCert.Status.IsInError() {
		return fmt.Errorf("consistence: settled (%s) and pending (%s) certs are different for same height. Err: %w",
			i.AgglayerLastSettledCert.ID(), i.AgglayerLastPendingCert.ID(),
			ErrAgglayerInconsistence)
	}

	// Settled certificate has higher height than pending certificate and it is not InError. This should not happen
	if i.AgglayerLastSettledCert.Height > i.AgglayerLastPendingCert.Height &&
		!i.AgglayerLastSettledCert.Status.IsInError() {
		return fmt.Errorf("settled cert height %s is higher than pending cert height %s that is inNoError. Err: %w",
			i.AgglayerLastSettledCert.ID(), i.AgglayerLastPendingCert.ID(),
			ErrAgglayerInconsistence)
	}

	return nil
}

func (i *initialStatus) getLatestAggLayerCert() *agglayertypes.CertificateHeader {
	if i.AgglayerLastPendingCert == nil {
		return i.AgglayerLastSettledCert
	}
	return i.AgglayerLastPendingCert
}

// processLastSettledCert checks the last settled certificate from agglayer vs local storage
func (i *initialStatus) processLastSettledCert() []*initialStatusResult {
	if i.AgglayerLastPendingCert == nil {
		// if pending cert is nil, this will be processed in the processLastLocal function
		return nil
	}

	if i.AgglayerLastSettledCert == nil {
		// CASE 1: Local storage have settled certificate, but agglayer doesn't have one
		// AggLayer is authoritative, so delete the stale local settled cert.
		if i.LocalLastSettledCert != nil {
			return []*initialStatusResult{
				newInitialStatusDeleteResult(
					i.LocalLastSettledCert.Height,
					fmt.Sprintf("agglayer has no settled certificate, deleting stale local settled certificate at height %d",
						i.LocalLastSettledCert.Height),
				).withWarning(fmt.Sprintf(
					"local settled certificate %s is not present in agglayer; agglayer is the source of truth",
					i.LocalLastSettledCert.ID(),
				)),
			}
		}

		// CASE 2: Both local and agglayer have no settled certificate
		return []*initialStatusResult{newInitialStatusResult(
			InitialStatusActionNone,
			"agglayer and local storage have no settled certificate",
			i.AgglayerLastSettledCert,
		)}
	}

	if i.LocalLastSettledCert == nil {
		// CASE 3: We have no settled certificate in local storage
		return []*initialStatusResult{newInitialStatusResult(
			InitialStatusActionInsertNewCert,
			"no local settled certificate,inserting agglayer settled certificate into local storage",
			i.AgglayerLastSettledCert,
		)}
	}

	// CASE 4: We have a settled certificate in local storage
	// but its height is higher than the one in the agglayer
	if i.LocalLastSettledCert.Height > i.AgglayerLastSettledCert.Height {
		return []*initialStatusResult{
			newInitialStatusDeleteResult(
				i.LocalLastSettledCert.Height,
				fmt.Sprintf("agglayer settled height is %d, deleting stale local settled certificate at height %d",
					i.AgglayerLastSettledCert.Height, i.LocalLastSettledCert.Height),
			).withWarning(fmt.Sprintf(
				"local settled certificate %s is ahead of agglayer settled certificate %s; agglayer is the source of truth",
				i.LocalLastSettledCert.ID(), i.AgglayerLastSettledCert.ID(),
			)),
		}
	}

	// CASE 5: We have a settled certificate in local storage with same height
	if i.LocalLastSettledCert.Height == i.AgglayerLastSettledCert.Height {
		// CASE 5.1: We have a settled certificate in local storage
		// the height is the same but the certificate ID is different
		// than the one in the agglayer for the same height. Replace it with the agglayer value.
		if i.LocalLastSettledCert.CertificateID != i.AgglayerLastSettledCert.CertificateID {
			warning := fmt.Sprintf(
				"local settled certificate %s does not match agglayer settled certificate %s at height %d; "+
					"agglayer is the source of truth",
				i.LocalLastSettledCert.ID(), i.AgglayerLastSettledCert.ID(), i.AgglayerLastSettledCert.Height,
			)
			return []*initialStatusResult{
				newInitialStatusDeleteResult(
					i.LocalLastSettledCert.Height,
					fmt.Sprintf("replacing conflicting local settled certificate at height %d with agglayer settled certificate %s",
						i.LocalLastSettledCert.Height, i.AgglayerLastSettledCert.ID()),
				).withWarning(warning),
				newInitialStatusResult(
					InitialStatusActionInsertNewCert,
					"updating local storage with agglayer settled certificate after deleting conflicting local entry",
					i.AgglayerLastSettledCert,
				).withWarning(warning),
			}
		}

		// CASE 5.2: the local settled certificate matches the agglayer settled certificate
		return []*initialStatusResult{newInitialStatusResult(
			InitialStatusActionNone,
			"last settled certificate already in local storage with same height and ID",
			i.AgglayerLastSettledCert,
		)}
	}

	// CASE 6: We have a settled certificate in local storage that is lower than the one in the agglayer
	// this means that we need to update the local storage with the agglayer settled
	return []*initialStatusResult{newInitialStatusResult(
		InitialStatusActionInsertNewCert,
		"updating local storage with agglayer settled certificate",
		i.AgglayerLastSettledCert,
	)}
}
