package validator

import (
	"context"
	"errors"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
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
	metadataV2Block46 = &types.CertificateMetadata{
		FromBlock: 46,
		Offset:    40,
		CreatedAt: 0,
		CertType:  0,
		Version:   types.CertificateMetadataV2,
	}
	errGenericForTesting = errors.New("generic error for testing purposes")

	testTreeRootIndex9 = treetypes.Root{
		Hash:          common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"),
		Index:         9,
		BlockNum:      1234,
		BlockPosition: 2,
	}
)

func TestValidateCertificate(t *testing.T) {
	t.Run("metadata not latest", func(t *testing.T) {
		testData := newTestDataCertificateValidator(t)
		err := testData.sut.ValidateCertificate(testData.ctx, types.VerifyIncomingRequest{
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
		testData := newTestDataCertificateValidator(t)
		err := testData.sut.ValidateCertificate(testData.ctx, types.VerifyIncomingRequest{
			Certificate: &agglayertypes.Certificate{
				Height:   1,
				Metadata: metadataV2Block1.ToHash(),
			},
			PreviousCertificate: nil,
		})
		require.Error(t, err)
		require.ErrorContains(t, err, "first certificate must have height 0")
	})
	t.Run("prev cert bad status", func(t *testing.T) {
		testData := newTestDataCertificateValidator(t)
		err := testData.sut.ValidateCertificate(testData.ctx, types.VerifyIncomingRequest{
			Certificate: &agglayertypes.Certificate{
				Height:   1,
				Metadata: metadataV2Block46.ToHash(),
			},
			PreviousCertificate: &agglayertypes.CertificateHeader{
				Height:   0,
				Metadata: metadataV2Block1.ToHash(),
				Status:   agglayertypes.Pending,
			},
		})
		require.Error(t, err)
		require.ErrorContains(t, err, "is not settled")
	})
	t.Run("GetCertificatePreBuildParams error l1infotree", func(t *testing.T) {
		testData := newTestDataCertificateValidator(t)
		testData.mockL1InfoTreeQuerier.EXPECT().
			GetL1InfoRootByLeafIndex(testData.ctx, uint32(9)).Return(nil, errGenericForTesting)
		err := testData.sut.ValidateCertificate(testData.ctx, types.VerifyIncomingRequest{
			Certificate: &agglayertypes.Certificate{
				Height:              0,
				Metadata:            metadataV2Block1.ToHash(),
				L1InfoTreeLeafCount: 10,
			},
			PreviousCertificate: nil,
		})
		require.ErrorContains(t, err, "failed to get L1 Info tree root by leaf count 10")
	})
	t.Run("fails flowPP.GenerateBuildParams", func(t *testing.T) {
		testData := newTestDataCertificateValidator(t)
		testData.mockL1InfoTreeQuerier.EXPECT().
			GetL1InfoRootByLeafIndex(testData.ctx, uint32(9)).Return(&testTreeRootIndex9, nil).Maybe()
		testData.mockFlow.EXPECT().
			GenerateBuildParams(testData.ctx, mock.Anything).Return(nil, errGenericForTesting)
		err := testData.sut.ValidateCertificate(testData.ctx, types.VerifyIncomingRequest{
			Certificate: &agglayertypes.Certificate{
				Height:              0,
				Metadata:            metadataV2Block1.ToHash(),
				L1InfoTreeLeafCount: 10,
			},
			PreviousCertificate: nil,
		})
		require.ErrorContains(t, err, "failed flow.GenerateBuildParams")
	})

	t.Run("fails flowPP.BuildCertificate", func(t *testing.T) {
		testData := newTestDataCertificateValidator(t)
		testData.mockL1InfoTreeQuerier.EXPECT().
			GetL1InfoRootByLeafIndex(testData.ctx, uint32(9)).Return(&testTreeRootIndex9, nil).Maybe()
		testData.mockFlow.EXPECT().
			GenerateBuildParams(testData.ctx, mock.Anything).Return(&types.CertificateBuildParams{}, nil)
		testData.mockFlow.EXPECT().
			BuildCertificate(testData.ctx, mock.Anything).Return(nil, errGenericForTesting)
		err := testData.sut.ValidateCertificate(testData.ctx, types.VerifyIncomingRequest{
			Certificate: &agglayertypes.Certificate{
				Height:              0,
				Metadata:            metadataV2Block1.ToHash(),
				L1InfoTreeLeafCount: 10,
			},
			PreviousCertificate: nil,
		})
		require.ErrorContains(t, err, "failed flow.BuildCertificate")
	})

	t.Run("fails CompareCertificates", func(t *testing.T) {
		testData := newTestDataCertificateValidator(t)
		testData.mockL1InfoTreeQuerier.EXPECT().
			GetL1InfoRootByLeafIndex(testData.ctx, uint32(9)).Return(&testTreeRootIndex9, nil).Maybe()
		testData.mockFlow.EXPECT().
			GenerateBuildParams(testData.ctx, mock.Anything).Return(&types.CertificateBuildParams{}, nil)
		testData.mockFlow.EXPECT().
			BuildCertificate(testData.ctx, mock.Anything).Return(&agglayertypes.Certificate{
			Metadata: metadataV2Block1.ToHash(),
		}, nil)
		err := testData.sut.ValidateCertificate(testData.ctx, types.VerifyIncomingRequest{
			Certificate: &agglayertypes.Certificate{
				Height:              0,
				Metadata:            metadataV2Block1.ToHash(),
				L1InfoTreeLeafCount: 10,
			},
			PreviousCertificate: nil,
		})
		require.ErrorContains(t, err, "certificate not equal to expected")
	})
}

func TestCheckContigousCertificates(t *testing.T) {
	testData := newTestDataCertificateValidator(t)
	t.Run("Nil Certificates", func(t *testing.T) {
		err := testData.sut.checkContigousCertificates(types.VerifyIncomingRequest{})
		require.Error(t, err)
	})
	t.Run("Nil PreviousCertificate, cert no start 0", func(t *testing.T) {
		err := testData.sut.checkContigousCertificates(types.VerifyIncomingRequest{
			Certificate: &agglayertypes.Certificate{
				Height: 0,
			},
			PreviousCertificate: nil,
		})
		require.ErrorContains(t, err, "first certificate must start from block 1, but got: 0")
	})

	t.Run("Nil PreviousCertificate, cert height!= 0", func(t *testing.T) {
		err := testData.sut.checkContigousCertificates(types.VerifyIncomingRequest{
			Certificate: &agglayertypes.Certificate{
				Height:   1,
				Metadata: metadataV2Block1.ToHash(),
			},
			PreviousCertificate: nil,
		})
		require.ErrorContains(t, err, "first certificate must have height 0, but got: 1")
	})

	t.Run("Non Contiguous BlockRange Certificates", func(t *testing.T) {
		err := testData.sut.checkContigousCertificates(types.VerifyIncomingRequest{
			Certificate: &agglayertypes.Certificate{
				Height:   2,
				Metadata: metadataV2Block1.ToHash(),
			},
			PreviousCertificate: &agglayertypes.CertificateHeader{
				Height:   1,
				Metadata: metadataV2Block1.ToHash(),
			}})
		require.ErrorContains(t, err, "is not contiguous with previous certificate")
	})
	t.Run("Non-Contiguous Certificates", func(t *testing.T) {
		err := testData.sut.checkContigousCertificates(types.VerifyIncomingRequest{
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
		_, err := testData.sut.getCertificatePreBuildParams(testData.ctx, types.VerifyIncomingRequest{})
		require.ErrorIs(t, err, ErrNilCertificate)
	})

	t.Run("fails AgglayerCertificateHeaderToAggsender", func(t *testing.T) {
		_, err := testData.sut.getCertificatePreBuildParams(testData.ctx, types.VerifyIncomingRequest{
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
		_, err := testData.sut.getCertificatePreBuildParams(testData.ctx, types.VerifyIncomingRequest{
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

func TestCompareCertificates(t *testing.T) {
	t.Run("TestCompareCertificates not same CertificateID", func(t *testing.T) {
		cert1 := &agglayertypes.Certificate{
			Height: 1,
		}
		cert2 := &agglayertypes.Certificate{
			Height: 2,
		}
		sut := &CertificateValidator{}
		err := sut.compareCertificates(cert1, cert2)
		require.ErrorContains(t, err, "height mismatch")
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
