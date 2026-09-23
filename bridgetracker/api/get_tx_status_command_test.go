package api

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// fakeSupervisedRegistry is a minimal domain.SupervisedRegistry double, shared by the
// getTxStatusCommand and wsHandler tests in this package: each test only sets the field(s) its
// scenario needs (getAndAwaitErr, subscribeErr, ...), the rest are left as zero-value stubs
type fakeSupervisedRegistry struct {
	getAndAwaitErr error
	subscribeErr   error
}

func (f *fakeSupervisedRegistry) Get(_ domain.TrackingID, _ bool) (*domain.TrackingData, error) {
	return nil, nil
}

func (f *fakeSupervisedRegistry) GetAndAwait(_ domain.TrackingID, _ time.Duration) (*domain.TrackingData, error) {
	return nil, f.getAndAwaitErr
}

func (f *fakeSupervisedRegistry) UpdateTrackingBridgeTx(_ domain.TrackingID, _ domain.TrackingBridgeTx) error {
	return nil
}

func (f *fakeSupervisedRegistry) UpdateTrackingStep(_ domain.TrackingID, _ uint, _ domain.BridgeStepPath) error {
	return nil
}

func (f *fakeSupervisedRegistry) GetTrackerActives(_ *uint32) ([]*domain.TrackingData, error) {
	return nil, nil
}

func (f *fakeSupervisedRegistry) GetNetworks(_ *types.TrackingStatus) ([]uint32, error) {
	return nil, nil
}

func (f *fakeSupervisedRegistry) GetNumTracker() int {
	return 0
}

func (f *fakeSupervisedRegistry) PruneTerminal(_ time.Time) (int, error) {
	return 0, nil
}

func (f *fakeSupervisedRegistry) PruneIdle(_ time.Time) (int, error) {
	return 0, nil
}

func (f *fakeSupervisedRegistry) Forget(_ domain.TrackingID) {}

func (f *fakeSupervisedRegistry) Subscribe(_ domain.TrackingID) (<-chan *domain.TrackingData, func(), error) {
	return nil, nil, f.subscribeErr
}

const testGetTxStatusHash = "0x1234567890123456789012345678901234567890123456789012345678901234"

// TestGetTxStatusCommandExecuteErrorRedacted pins that getTxStatusCommand.Execute never lets a
// backend URL reach the client: GetAndAwait's error is redacted before it lands in
// types.ErrorData.Message, at the construction site (bridgetracker/api/get_tx_status_command.go)
func TestGetTxStatusCommandExecuteErrorRedacted(t *testing.T) {
	t.Parallel()

	rawErr := `claim status: fetching claims of global index 123 on network 2: do request: ` +
		`Get "http://10.0.0.5:5577/bridge/v1/claims?network_id=2&global_index=123": ` +
		`dial tcp 10.0.0.5:5577: connect: connection refused`

	cmd := &getTxStatusCommand{
		supervised:     &fakeSupervisedRegistry{getAndAwaitErr: errors.New(rawErr)},
		resolveTimeout: time.Second,
	}

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Params = gin.Params{
		{Key: networkIDParam, Value: "1"},
		{Key: txHashParam, Value: testGetTxStatusHash},
	}

	_, _, errData := cmd.Execute(c)

	require.NotNil(t, errData)
	data, err := json.Marshal(errData)
	require.NoError(t, err)
	require.NotContains(t, string(data), "://")
	require.NotContains(t, string(data), "10.0.0.5")
}
