package metrics

import (
	"github.com/agglayer/aggkit/prometheus"
	prometheusclient "github.com/prometheus/client_golang/prometheus"
)

const (
	namespace     = "bridge"
	totalRequests = "total_requests"
	handlerName   = "handler_name"
	statusCode    = "status_code"

	GetBridgesReq               = "get_bridges"
	GetClaimsReq                = "get_claims"
	GetTokenMappingsReq         = "get_token_mappings"
	GetLegacyTokenMigrationsReq = "get_legacy_token_migrations"
	GetL1InfoTreeIndexReq       = "l1_info_tree_index_for_bridge"
	GetIjectedInfoAfterIndexReq = "injected_info_after_index"
	GetClaimProofReq            = "claim_proof"
	GetLastReorgEventReq        = "last_reorg_event"
	GetSyncStatusReq            = "get_sync_status"
	GetHealthCheckReq           = "health_check"
)

func Register() {
	counterVecs := []prometheus.CounterVecOpts{
		{
			CounterOpts: prometheusclient.CounterOpts{
				Namespace: namespace,
				Name:      totalRequests,
				Help:      "Total number of requests per handler name and http response code",
			},
			Labels: []string{handlerName, statusCode},
		},
	}

	prometheus.RegisterCounterVecs(counterVecs...)
}

// IncTotalRequestCounter increments counter for given handler name and status code
func IncTotalRequestCounter(handler string, status string) {
	prometheus.CounterVecInc(totalRequests, handler, status)
}
