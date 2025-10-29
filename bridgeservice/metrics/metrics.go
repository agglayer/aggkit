package metrics

import (
	"github.com/agglayer/aggkit/prometheus"
	prometheusclient "github.com/prometheus/client_golang/prometheus"
)

type PrometheusClienter interface {
	CounterInc(name string)
	RegisterCounters(opts ...prometheusclient.CounterOpts)
}

var promClient PrometheusClienter = prometheusWrapper{}

type prometheusWrapper struct{}

func (p prometheusWrapper) CounterInc(name string) {
	prometheus.CounterInc(name)
}

func (p prometheusWrapper) RegisterCounters(opts ...prometheusclient.CounterOpts) {
	prometheus.RegisterCounters(opts...)
}

const (
	prefix                     = "bridge_"
	L1InfoTreeIndexReqs        = prefix + "l1_info_tree_index_for_bridge"
	InjectedInfoAfterIndexReqs = prefix + "injected_info_after_index"
	ClaimProofReqs             = prefix + "claim_proof"
	LastReorgEventReqs         = prefix + "last_reorg_event"
	SyncStatusReqs             = prefix + "get_sync_status"
	BridgesReqs                = prefix + "get_bridges"
	ClaimsReqs                 = prefix + "get_claims"
	TokenMappingsReqs          = prefix + "get_token_mappings"
	LegacyTokenMigrationReqs   = prefix + "get_legacy_token_migrations"
)

var (
	incrementCountersHandlerMap = map[string]func(){
		L1InfoTreeIndexReqs:        IncL1InfoTreeIndexReqs,
		InjectedInfoAfterIndexReqs: IncInjectedInfoAfterIndexReqs,
		ClaimProofReqs:             IncClaimProofReqs,
		LastReorgEventReqs:         IncLastReorgEventsReqs,
		SyncStatusReqs:             IncSyncStatusReqs,
		BridgesReqs:                IncBridgesReqs,
		ClaimsReqs:                 IncClaimsReqs,
		TokenMappingsReqs:          IncTokenMappingReqs,
		LegacyTokenMigrationReqs:   IncLegacyTokenMigrationReqs,
	}
)

func Register() {
	counters := []prometheusclient.CounterOpts{
		{
			Name: L1InfoTreeIndexReqs,
			Help: "[BRIDGE] number of l1 info tree index for bridge requests",
		},
		{
			Name: InjectedInfoAfterIndexReqs,
			Help: "[BRIDGE] number of injected info after index requests",
		},
		{
			Name: ClaimProofReqs,
			Help: "[BRIDGE] number of claim proof requests",
		},
		{
			Name: LastReorgEventReqs,
			Help: "[BRIDGE] number of last reorg event requests",
		},
		{
			Name: SyncStatusReqs,
			Help: "[BRIDGE] number of get sync status requests",
		},
		{
			Name: BridgesReqs,
			Help: "[BRIDGE] number of get bridge requests",
		},
		{
			Name: ClaimsReqs,
			Help: "[BRIDGE] number of get claims requests",
		},
		{
			Name: TokenMappingsReqs,
			Help: "[BRIDGE] number of get token mappings requests",
		},
		{
			Name: LegacyTokenMigrationReqs,
			Help: "[BRIDGE] number of get legacy token migrations requests",
		},
	}
	promClient.RegisterCounters(counters...)
}

func IncrementCounter(counterName string) {
	if handler, exists := incrementCountersHandlerMap[counterName]; exists {
		handler()
	}
}

func IncL1InfoTreeIndexReqs() {
	promClient.CounterInc(L1InfoTreeIndexReqs)
}

func IncInjectedInfoAfterIndexReqs() {
	promClient.CounterInc(InjectedInfoAfterIndexReqs)
}

func IncClaimProofReqs() {
	promClient.CounterInc(ClaimProofReqs)
}

func IncLastReorgEventsReqs() {
	promClient.CounterInc(LastReorgEventReqs)
}

func IncSyncStatusReqs() {
	promClient.CounterInc(SyncStatusReqs)
}

func IncBridgesReqs() {
	promClient.CounterInc(BridgesReqs)
}

func IncClaimsReqs() {
	promClient.CounterInc(ClaimsReqs)
}

func IncTokenMappingReqs() {
	promClient.CounterInc(TokenMappingsReqs)
}

func IncLegacyTokenMigrationReqs() {
	promClient.CounterInc(LegacyTokenMigrationReqs)
}
