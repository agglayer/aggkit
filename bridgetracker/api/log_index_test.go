package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// recordingSupervisedRegistry is a fakeSupervisedRegistry recording the ids it is asked to
// await/forget, answering GetAndAwait with a fresh (Registered) snapshot of the requested id
type recordingSupervisedRegistry struct {
	fakeSupervisedRegistry
	awaited   []domain.TrackingID
	forgotten []domain.TrackingID
}

func (f *recordingSupervisedRegistry) GetAndAwait(id domain.TrackingID, _ time.Duration) (*domain.TrackingData, error) {
	f.awaited = append(f.awaited, id)
	return domain.NewTrackingData(id, domain.TrackingBridgeTx{}, nil), nil
}

func (f *recordingSupervisedRegistry) Forget(id domain.TrackingID) {
	f.forgotten = append(f.forgotten, id)
}

// executeTxStatus runs getTxStatusCommand.Execute for network 1/testGetTxStatusHash with query
func executeTxStatus(
	t *testing.T, registry domain.SupervisedRegistry, query string,
) (int, any, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/"+query, nil)
	c.Params = gin.Params{
		{Key: networkIDParam, Value: "1"},
		{Key: txHashParam, Value: testGetTxStatusHash},
	}
	cmd := &getTxStatusCommand{supervised: registry}
	code, body, errData := cmd.Execute(c)
	if errData != nil {
		return errData.Code, nil, errData.Message
	}
	return code, body, ""
}

// TestGetTxStatusCommandLogIndex pins the log_index query parameter: absent,
// the request tracks the tx-only identity exactly as before and the response carries no
// log_index; present, it tracks (and flush_cache forgets) that one BridgeEvent, echoed back as
// log_index
func TestGetTxStatusCommandLogIndex(t *testing.T) {
	txHash := common.HexToHash(testGetTxStatusHash)

	t.Run("tx-only", func(t *testing.T) {
		registry := &recordingSupervisedRegistry{}
		code, body, _ := executeTxStatus(t, registry, "")
		require.Equal(t, http.StatusOK, code)
		require.Equal(t, []domain.TrackingID{{NetworkID: 1, TxHash: txHash}}, registry.awaited)

		raw, err := json.Marshal(body)
		require.NoError(t, err)
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &fields))
		require.NotContains(t, fields, "log_index", "a tx-only response keeps its pre-existing shape")
	})

	t.Run("selected log", func(t *testing.T) {
		registry := &recordingSupervisedRegistry{}
		code, body, _ := executeTxStatus(t, registry, "?log_index=9&flush_cache=true")
		require.Equal(t, http.StatusOK, code)
		want := domain.TrackingID{NetworkID: 1, TxHash: txHash, Log: domain.LogIndex(9)}
		require.Equal(t, []domain.TrackingID{want}, registry.forgotten)
		require.Equal(t, []domain.TrackingID{want}, registry.awaited)

		tracking, ok := body.(TrackingData)
		require.True(t, ok)
		require.NotNil(t, tracking.LogIndex)
		require.Equal(t, uint32(9), *tracking.LogIndex)
	})

	for _, invalid := range []string{"abc", "-1", "4294967296", ""} {
		t.Run("invalid "+invalid, func(t *testing.T) {
			registry := &recordingSupervisedRegistry{}
			code, _, message := executeTxStatus(t, registry, "?log_index="+invalid)
			require.Equal(t, http.StatusBadRequest, code)
			require.Contains(t, message, "invalid log_index parameter")
			require.Empty(t, registry.awaited)
		})
	}
}
