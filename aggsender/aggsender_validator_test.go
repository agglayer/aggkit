package aggsender

import (
	"context"
	"errors"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	aggsendertypes "github.com/agglayer/aggkit/aggsender/types"
	validatorcfg "github.com/agglayer/aggkit/aggsender/validator"
	aggkitgrpc "github.com/agglayer/aggkit/grpc"
	"github.com/agglayer/aggkit/log"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newTestAggsenderValidator(t *testing.T) *AggsenderValidator {
	t.Helper()
	grpcServer, err := aggkitgrpc.NewServer(aggkitgrpc.ServerConfig{
		Host: "127.0.0.1",
		Port: 0,
	})
	require.NoError(t, err)
	return &AggsenderValidator{
		log:              log.WithFields("module", "aggsender-validator-test"),
		validatorService: grpcServer,
		cfg:              validatorcfg.Config{},
	}
}

// TestAggsenderValidatorStart_SetClaimSyncerFails checks that Start panics (via Fatalf)
// when SetClaimSyncerNextRequiredBlock returns an error.
func TestAggsenderValidatorStart_SetClaimSyncerFails(t *testing.T) {
	sut := newTestAggsenderValidator(t)
	mockSetter := mocks.NewInitialBlockClaimSyncerSetter(t)
	mockSetter.EXPECT().
		SetClaimSyncerNextRequiredBlock(mock.Anything, mock.Anything, mock.Anything).
		Return(errors.New("setter error")).Once()
	sut.initialBlockClaimSyncerSetter = mockSetter

	require.Panics(t, func() {
		sut.Start(t.Context())
	})
}

// TestAggsenderValidatorStart_Success checks that Start succeeds when
// SetClaimSyncerNextRequiredBlock returns nil and the gRPC server starts normally.
func TestAggsenderValidatorStart_Success(t *testing.T) {
	sut := newTestAggsenderValidator(t)
	mockSetter := mocks.NewInitialBlockClaimSyncerSetter(t)
	mockSetter.EXPECT().
		SetClaimSyncerNextRequiredBlock(mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()
	sut.initialBlockClaimSyncerSetter = mockSetter

	// Cancel the context immediately so the gRPC server stops right after starting.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.NotPanics(t, func() {
		sut.Start(ctx)
	})
}

// TestAggsenderValidatorValidateCertificate_Success checks that ValidateCertificate
// delegates to the inner validator and returns nil on success.
func TestAggsenderValidatorValidateCertificate_Success(t *testing.T) {
	sut := newTestAggsenderValidator(t)
	mockValidator := mocks.NewCertificateValidator(t)
	params := aggsendertypes.VerifyIncomingRequest{
		Certificate: &agglayertypes.Certificate{},
	}
	mockValidator.EXPECT().ValidateCertificate(mock.Anything, params).Return(nil).Once()
	sut.validator = mockValidator

	err := sut.ValidateCertificate(t.Context(), params)
	require.NoError(t, err)
}

// TestAggsenderValidatorValidateCertificate_Error checks that ValidateCertificate
// propagates the error returned by the inner validator.
func TestAggsenderValidatorValidateCertificate_Error(t *testing.T) {
	sut := newTestAggsenderValidator(t)
	mockValidator := mocks.NewCertificateValidator(t)
	expectedErr := errors.New("validation failed")
	params := aggsendertypes.VerifyIncomingRequest{
		Certificate: &agglayertypes.Certificate{},
	}
	mockValidator.EXPECT().ValidateCertificate(mock.Anything, params).Return(expectedErr).Once()
	sut.validator = mockValidator

	err := sut.ValidateCertificate(t.Context(), params)
	require.ErrorIs(t, err, expectedErr)
}
