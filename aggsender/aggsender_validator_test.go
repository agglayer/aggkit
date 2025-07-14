package aggsender

import (
	"testing"

	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/aggsender/validator"
	validatormocks "github.com/agglayer/aggkit/aggsender/validator/mocks"
	"github.com/agglayer/aggkit/log"
	"github.com/stretchr/testify/require"
)

func TestNewAggsenderValidator(t *testing.T) {
	ctx := t.Context()

	mockLogger := log.WithFields("test", "TestNewAggsenderValidator")
	mockFlowPP := mocks.NewAggsenderFlow(t)
	mockL1InfoTreeDataQuerier := mocks.NewL1InfoTreeDataQuerier(t)
	mockAggLayerClient := validatormocks.NewAgglayerClientInterface(t)
	cfg := validator.Config{}
	// Call the function
	validator, err := NewAggsenderValidator(ctx, mockLogger, cfg, mockFlowPP, mockL1InfoTreeDataQuerier,
		mockAggLayerClient, nil)

	// Assertions
	require.NoError(t, err, "Expected no error when creating AggsenderValidator")
	require.NotNil(t, validator, "Expected AggsenderValidator to be non-nil")
	require.Equal(t, mockLogger, validator.log, "Expected logger to be set correctly")
	require.NotNil(t, validator.validator, "Expected validator to be initialized")

	require.Equal(t, 1, len(validator.GetRPCServices()), "Expected one RPC service to be registered")

	require.Error(t, validator.ValidateCertificate(ctx, types.VerifyIncomingRequest{}))
}
