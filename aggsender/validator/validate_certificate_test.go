package validator

import (
	"context"
	"errors"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/log"
	"github.com/stretchr/testify/require"
)

var (
	metadataV1Block1 = &types.CertificateMetadata{
		FromBlock: 1,
		Offset:    44,
		CreatedAt: 0,
		CertType:  0,
		Version:   types.CertificateMetadataV1,
	}

	metadataV2Block1 = &types.CertificateMetadata{
		FromBlock: 1,
		Offset:    44,
		CreatedAt: 0,
		CertType:  0,
		Version:   types.CertificateMetadataV2,
	}
	errGenericForTesting = errors.New("generic error for testing purposes")
)

func TestValidateCertificate(t *testing.T) {
	mockLogger := log.WithFields("test", "TestValidateCertificate")
	mockFlow := mocks.NewAggsenderFlow(t)
	mockL1InfoTreeQuerier := mocks.NewL1InfoTreeDataQuerier(t)

	validator := NewAggsenderValidator(mockLogger, mockFlow, mockL1InfoTreeQuerier)

	ctx := context.Background()

	t.Run("metadata not latest", func(t *testing.T) {
		err := validator.ValidateCertificate(ctx, types.VerifyIncommingRequests{
			Certificate: &agglayertypes.Certificate{
				Height:   0,
				Metadata: metadataV1Block1.ToHash(),
			},
			PreviousCertificate: nil,
		})
		require.Error(t, err)
		require.ErrorContains(t, err, "certificate metadata version is not latest")
	})

	t.Run("first cert bad height", func(t *testing.T) {
		err := validator.ValidateCertificate(ctx, types.VerifyIncommingRequests{
			Certificate: &agglayertypes.Certificate{
				Height:   1,
				Metadata: metadataV2Block1.ToHash(),
			},
			PreviousCertificate: nil,
		})
		require.Error(t, err)
		require.ErrorContains(t, err, "first certificate must have height 0")
	})

	t.Run("first cert bad height", func(t *testing.T) {
		err := validator.ValidateCertificate(ctx, types.VerifyIncommingRequests{
			Certificate: &agglayertypes.Certificate{
				Height:   0,
				Metadata: metadataV2Block1.ToHash(),
			},
			PreviousCertificate: nil,
		})
		require.Error(t, err)
		require.ErrorContains(t, err, "first certificate must have height 0")
	})
}

func TestCheckContigousCertificates(t *testing.T) {
	testData := newTestDataCertificateValidator(t)
	t.Run("Nil Certificates", func(t *testing.T) {
		err := testData.sut.CheckContigousCertificates(types.VerifyIncommingRequests{})
		require.Error(t, err)
	})
	t.Run("Nil PreviousCertificate, cert no start 0", func(t *testing.T) {
		err := testData.sut.CheckContigousCertificates(types.VerifyIncommingRequests{
			Certificate: &agglayertypes.Certificate{
				Height: 0,
			},
			PreviousCertificate: nil,
		})
		require.ErrorContains(t, err, "first certificate must start from block 1, but got: 0")
	})

	t.Run("Nil PreviousCertificate, cert height!= 0", func(t *testing.T) {
		err := testData.sut.CheckContigousCertificates(types.VerifyIncommingRequests{
			Certificate: &agglayertypes.Certificate{
				Height:   1,
				Metadata: metadataV2Block1.ToHash(),
			},
			PreviousCertificate: nil,
		})
		require.ErrorContains(t, err, "first certificate must have height 0, but got: 1")
	})

	t.Run("Contiguous Certificates", func(t *testing.T) {
		err := testData.sut.CheckContigousCertificates(types.VerifyIncommingRequests{
			Certificate: &agglayertypes.Certificate{
				Height: 2,
			},
			PreviousCertificate: &agglayertypes.CertificateHeader{
				Height: 1,
			}})
		require.NoError(t, err)
	})
	t.Run("Non-Contiguous Certificates", func(t *testing.T) {
		err := testData.sut.CheckContigousCertificates(types.VerifyIncommingRequests{
			Certificate: &agglayertypes.Certificate{
				Height: 2,
			},
			PreviousCertificate: &agglayertypes.CertificateHeader{
				Height: 13,
			}})
		require.Error(t, err)
		require.Contains(t, err.Error(), "certificate height not contigous")
	})
}

func TestGetCertificatePreBuildParams(t *testing.T) {
	testData := newTestDataCertificateValidator(t)
	t.Run("Nil Certificates", func(t *testing.T) {
		_, err := testData.sut.GetCertificatePreBuildParams(testData.ctx, types.VerifyIncommingRequests{})
		require.ErrorIs(t, err, ErrNilCertificate)
	})

	t.Run("fails AgglayerCertificateHeaderToAggsender", func(t *testing.T) {
		_, err := testData.sut.GetCertificatePreBuildParams(testData.ctx, types.VerifyIncommingRequests{
			Certificate: &agglayertypes.Certificate{
				Height: 2,
			},
			PreviousCertificate: &agglayertypes.CertificateHeader{
				Height: 13,
			}})
		require.ErrorContains(t, err, "cant get blockRange from certificate metadata")
	})

	t.Run("fails GetL1InfoRootByLeafIndex", func(t *testing.T) {
		testData.mockL1InfoTreeQuerier.EXPECT().
			GetL1InfoRootByLeafIndex(testData.ctx, uint32(9)).Return(nil, errGenericForTesting)
		_, err := testData.sut.GetCertificatePreBuildParams(testData.ctx, types.VerifyIncommingRequests{
			Certificate: &agglayertypes.Certificate{
				Height:              2,
				Metadata:            metadataV2Block1.ToHash(),
				L1InfoTreeLeafCount: 10,
			},
			PreviousCertificate: &agglayertypes.CertificateHeader{
				Height:   13,
				Metadata: metadataV2Block1.ToHash(),
			}})
		require.ErrorContains(t, err, "failed to get L1 Info tree root")
	})
}

type testDataCertificateValidator struct {
	ctx                   context.Context
	logger                *log.Logger
	mockFlow              *mocks.AggsenderFlow
	mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier
	sut                   *CertificateValidator
}

func newTestDataCertificateValidator(t *testing.T) testDataCertificateValidator {
	t.Helper()
	mockLogger := log.WithFields("test", "TestValidateCertificate")
	mockFlow := mocks.NewAggsenderFlow(t)
	mockL1InfoTreeQuerier := mocks.NewL1InfoTreeDataQuerier(t)

	return testDataCertificateValidator{
		ctx:                   context.TODO(),
		logger:                mockLogger,
		mockFlow:              mockFlow,
		mockL1InfoTreeQuerier: mockL1InfoTreeQuerier,
		sut:                   NewAggsenderValidator(mockLogger, mockFlow, mockL1InfoTreeQuerier),
	}
}
