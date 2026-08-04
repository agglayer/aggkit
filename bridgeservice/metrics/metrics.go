package metrics

import (
	"time"

	"github.com/agglayer/aggkit/prometheus"
	prometheusclient "github.com/prometheus/client_golang/prometheus"
)

const (
	namespace      = "bridge"
	totalRequests  = "total_requests"
	requestLatency = "request_latency_seconds"
	handlerID      = "handler_id"
	statusCode     = "status_code"

	GetBridgesReq                = "get_bridges"
	GetClaimsReq                 = "get_claims"
	GetUnsetClaimsReq            = "get_unset_claims"
	GetSetClaimsReq              = "get_set_claims"
	GetTokenMappingsReq          = "get_token_mappings"
	GetLegacyTokenMigrationsReq  = "get_legacy_token_migrations"
	GetL1InfoTreeIndexReq        = "l1_info_tree_index_for_bridge"
	GetInjectedInfoAfterIndexReq = "injected_info_after_index"
	GetClaimProofReq             = "claim_proof"
	GetLastReorgEventReq         = "last_reorg_event"
	GetSyncStatusReq             = "get_sync_status"
	GetRemoveGEREventsReq        = "get_remove_ger_events"
	GetHealthCheckReq            = "health_check"
	GetClaimsByGERReq            = "get_claims_by_ger"
	GetBridgeByDepositCountReq   = "get_bridge_by_deposit_count"
	GetBridgesByContentReq       = "get_bridges_by_content"
	GetClaimCandidatesReq        = "get_claim_candidates"
	GetRootByLERReq              = "get_root_by_ler"
	GetL1InfoTreeLeafByGERReq    = "get_l1_info_tree_leaf_by_ger"
)

func Register() {
	counterVecs := []prometheus.CounterVecOpts{
		{
			CounterOpts: prometheusclient.CounterOpts{
				Namespace: namespace,
				Name:      totalRequests,
				Help:      "Total number of requests per handler id and http response code",
			},
			Labels: []string{handlerID, statusCode},
		},
	}

	prometheus.RegisterCounterVecs(counterVecs...)

	histogramVecs := []prometheus.HistogramVecOpts{
		{
			HistogramOpts: prometheusclient.HistogramOpts{
				Namespace: namespace,
				Name:      requestLatency,
				Help:      "Request latencies per handler id",
			},
			Labels: []string{handlerID},
		},
	}
	prometheus.RegisterHistogramVecs(histogramVecs...)
}

// IncTotalRequestCounter increments counter for given handler id and status code
func IncTotalRequestCounter(handlerID string, status string) {
	prometheus.CounterVecInc(totalRequests, handlerID, status)
}

// ObserveRequestLatencyHistogram reports latency in seconds for given handler id
func ObserveRequestLatencyHistogram(handlerID string, startTime time.Time) {
	prometheus.HistogramVecObserve(requestLatency, handlerID, time.Since(startTime).Seconds())
}
