package metrics

import (
	"testing"

	"github.com/agglayer/aggkit/bridgeservice/metrics/mocks"
	"github.com/stretchr/testify/mock"
)

func TestIncrementCounter(t *testing.T) {
	origProm := promClient

	mockProm := mocks.NewPrometheusClienter(t)
	promClient = mockProm
	defer func() { promClient = origProm }()

	for name := range incrementCountersHandlerMap {
		mockProm.EXPECT().CounterInc(name).Once()
		IncrementCounter(name)
	}

	mockProm.AssertExpectations(t)
}

func TestRegisterCallsPrometheus(t *testing.T) {
	mockProm := mocks.NewPrometheusClienter(t)
	promClient = mockProm

	mockProm.EXPECT().RegisterCounters(
		mock.Anything, mock.Anything, mock.Anything,
		mock.Anything, mock.Anything, mock.Anything,
		mock.Anything, mock.Anything, mock.Anything).Once()

	Register()

	mockProm.AssertExpectations(t)
}
