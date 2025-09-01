package validator

import (
	"context"
	"errors"
	"testing"

	"github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestValidateAndSignCertificate_Success(t *testing.T) {
	logger := log.WithFields("module", "aggsender.validator.local")

	storage := mocks.NewAggSenderStorage(t)
	storage.EXPECT().GetCertificateHeaderByHeight(mock.Anything).Return(nil, nil)

	validator := mocks.NewCertificateValidator(t)
	validator.EXPECT().ValidateCertificate(mock.Anything, mock.Anything).Return(nil)

	localValidator := NewLocalValidator(logger, storage, validator)

	certificate := &types.Certificate{
		Height:    1,
		NetworkID: 1,
	}

	signature, err := localValidator.ValidateAndSignCertificate(context.Background(), certificate, 0)
	require.NoError(t, err)
	require.Equal(t, signature, aggkitcommon.EmptySignature)
	require.NotNil(t, localValidator.String())

	storage.AssertExpectations(t)
	validator.AssertExpectations(t)
}

func TestValidateAndSignCertificate_PreviousCertificateError(t *testing.T) {
	logger := log.WithFields("module", "aggsender.validator.local")

	storage := mocks.NewAggSenderStorage(t)
	storage.On("GetCertificateHeaderByHeight", mock.Anything).Return(nil, errors.New("storage error"))

	localValidator := &LocalValidator{
		log:     logger,
		storage: storage,
	}

	certificate := &types.Certificate{
		Height: 1,
	}

	signature, err := localValidator.ValidateAndSignCertificate(context.Background(), certificate, 0)
	require.Error(t, err)
	require.Nil(t, signature)

	storage.AssertExpectations(t)
}

func TestValidateAndSignCertificate_ValidationError(t *testing.T) {
	logger := log.WithFields("module", "aggsender.validator.local")

	storage := mocks.NewAggSenderStorage(t)
	storage.On("GetCertificateHeaderByHeight", mock.Anything).Return(nil, nil)

	validator := mocks.NewCertificateValidator(t)
	validator.On("ValidateCertificate", mock.Anything, mock.Anything).Return(errors.New("validation error"))

	localValidator := &LocalValidator{
		log:       logger,
		storage:   storage,
		validator: validator,
	}

	certificate := &types.Certificate{
		Height: 1,
	}

	signature, err := localValidator.ValidateAndSignCertificate(context.Background(), certificate, 0)
	require.Error(t, err)
	require.Nil(t, signature)

	storage.AssertExpectations(t)
	validator.AssertExpectations(t)
}

func TestValidateAndSignCertificate_HealthCheck(t *testing.T) {
	logger := log.WithFields("module", "aggsender.validator.local")

	localValidator := &LocalValidator{
		log: logger,
	}

	response, err := localValidator.HealthCheck(context.Background())
	require.NoError(t, err)
	require.NotNil(t, response)
	require.Equal(t, "OK", response.Status)
	require.Equal(t, "local", response.Version)
}
