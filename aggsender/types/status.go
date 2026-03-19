package types

import (
	"time"

	"github.com/agglayer/aggkit"
	aggkitcommon "github.com/agglayer/aggkit/common"
)

type AggsenderStatusType string

const (
	StatusNone                     AggsenderStatusType = "none"
	StatusCheckingDBCompatibility  AggsenderStatusType = "checking_db_compatibility"
	StatusCheckingInitialStage     AggsenderStatusType = "checking_initial_stage"
	StartingClaimSyncerStage       AggsenderStatusType = "starting_claim_syncer_stage"
	StatusFlowCheckingInitialStage AggsenderStatusType = "checking_flow_initial_stage"
	StatusCertificateStage         AggsenderStatusType = "certificate_stage"
)

type AggsenderStatus struct {
	Running   bool                `json:"running"`
	StartTime time.Time           `json:"start_time"`
	Status    AggsenderStatusType `json:"status"`
	LastError string              `json:"last_error"`
}

type AggsenderInfo struct {
	AggsenderStatus AggsenderStatus `json:"aggsender_status"`
	Version         aggkit.FullVersion
	TriggerStatus   string        `json:"trigger_status"`
	NetworkID       uint32        `json:"network_id"`
	Mode            AggsenderMode `json:"mode"`
}

func (a *AggsenderStatus) Start(startTime time.Time) {
	a.Running = true
	a.StartTime = startTime
}

func (a *AggsenderStatus) SetStatus(status AggsenderStatusType, logger aggkitcommon.Logger) {
	a.Status = status
	if logger != nil {
		logger.Infof("Aggsender status changed to: %s", status)
	}
}

func (a *AggsenderStatus) GetStatus() AggsenderStatusType {
	return a.Status
}

func (a *AggsenderStatus) SetLastError(err error) {
	if err == nil {
		a.LastError = ""
	} else {
		a.LastError = err.Error()
	}
}
