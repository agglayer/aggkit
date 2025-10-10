package validator

import (
	"context"
	"errors"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var (
	errGenericForTesting = errors.New("generic error for testing purposes")

	testTreeRootIndex9 = treetypes.Root{
		Hash:          common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"),
		Index:         9,
		BlockNum:      1234,
		BlockPosition: 2,
	}
)

func TestValidateCertificate(t *testing.T) {
	t.Run("invalid LastL2BlockInCert - ToBlock in cert larger", func(t *testing.T) {
		testData := newTestDataCertificateValidator(t)
		testData.mockCertQuerier.EXPECT().GetNewCertificateToBlock(testData.ctx, mock.Anything).Return(uint64(10), nil)
		err := testData.sut.ValidateCertificate(testData.ctx, types.VerifyIncomingRequest{
			Certificate: &agglayertypes.Certificate{
				Height: 1,
			},
			PreviousCertificate: nil,
			LastL2BlockInCert:   5,
		})
		require.Error(t, err)
		require.ErrorContains(t, err, "new certificate to block 10 must be less than or equal to last L2 block provided by the proposer 5")
	})

	t.Run("invalid LastL2BlockInCert - smaller than previous settled block", func(t *testing.T) {
		testData := newTestDataCertificateValidator(t)
		testData.mockCertQuerier.EXPECT().GetLastSettledCertificateToBlock(testData.ctx, mock.Anything).Return(uint64(20), nil)
		err := testData.sut.ValidateCertificate(testData.ctx, types.VerifyIncomingRequest{
			Certificate: &agglayertypes.Certificate{
				Height: 1,
			},
			PreviousCertificate: &agglayertypes.CertificateHeader{
				Height:           0,
				Metadata:         aggkitcommon.ZeroHash,
				Status:           agglayertypes.Pending,
				NewLocalExitRoot: common.HexToHash("0x1"),
			},
			LastL2BlockInCert: 10,
		})
		require.Error(t, err)
		require.ErrorContains(t, err, "the last L2 block in the certificate (10) must be greater than the last settled block (20)")
	})

	t.Run("first cert bad height", func(t *testing.T) {
		testData := newTestDataCertificateValidator(t)
		testData.mockCertQuerier.EXPECT().GetNewCertificateToBlock(testData.ctx, mock.Anything).Return(uint64(10), nil)
		err := testData.sut.ValidateCertificate(testData.ctx, types.VerifyIncomingRequest{
			Certificate: &agglayertypes.Certificate{
				Height: 1,
			},
			PreviousCertificate: nil,
			LastL2BlockInCert:   10,
		})
		require.Error(t, err)
		require.ErrorContains(t, err, "first certificate must have height 0")
	})

	t.Run("first cert bad previous LER", func(t *testing.T) {
		testData := newTestDataCertificateValidator(t)
		testData.mockLERQuerier.EXPECT().GetLastLocalExitRoot().Return(types.EmptyLER, nil)
		testData.mockCertQuerier.EXPECT().GetNewCertificateToBlock(testData.ctx, mock.Anything).Return(uint64(10), nil)
		err := testData.sut.ValidateCertificate(testData.ctx, types.VerifyIncomingRequest{
			Certificate: &agglayertypes.Certificate{
				Height:            0,
				PrevLocalExitRoot: common.HexToHash("0x1"),
			},
			PreviousCertificate: nil,
			LastL2BlockInCert:   10,
		})
		require.Error(t, err)
		require.ErrorContains(t, err, "first certificate must have correct starting PrevLocalExitRoot")
	})

	t.Run("prev cert bad status", func(t *testing.T) {
		testData := newTestDataCertificateValidator(t)
		testData.mockCertQuerier.EXPECT().GetLastSettledCertificateToBlock(testData.ctx, mock.Anything).Return(uint64(10), nil)
		testData.mockCertQuerier.EXPECT().GetNewCertificateToBlock(testData.ctx, mock.Anything).Return(uint64(20), nil)
		err := testData.sut.ValidateCertificate(testData.ctx, types.VerifyIncomingRequest{
			Certificate: &agglayertypes.Certificate{
				Height:            1,
				PrevLocalExitRoot: common.HexToHash("0x1"),
			},
			PreviousCertificate: &agglayertypes.CertificateHeader{
				Height:           0,
				Metadata:         aggkitcommon.ZeroHash,
				Status:           agglayertypes.Pending,
				NewLocalExitRoot: common.HexToHash("0x1"),
			},
			LastL2BlockInCert: 20,
		})
		require.Error(t, err)
		require.ErrorContains(t, err, "is not settled")
	})

	t.Run("GetCertificatePreBuildParams error l1infotree", func(t *testing.T) {
		testData := newTestDataCertificateValidator(t)
		testData.mockCertQuerier.EXPECT().GetNewCertificateToBlock(testData.ctx, mock.Anything).Return(uint64(10), nil)
		testData.mockLERQuerier.EXPECT().GetLastLocalExitRoot().Return(types.EmptyLER, nil)
		testData.mockCertQuerier.EXPECT().CalculateCertificateType(mock.Anything, uint64(10)).Return(types.CertificateTypePP)
		testData.mockL1InfoTreeQuerier.EXPECT().
			GetL1InfoRootByLeafIndex(testData.ctx, uint32(9)).Return(nil, errGenericForTesting)
		err := testData.sut.ValidateCertificate(testData.ctx, types.VerifyIncomingRequest{
			Certificate: &agglayertypes.Certificate{
				Height:              0,
				L1InfoTreeLeafCount: 10,
				PrevLocalExitRoot:   types.EmptyLER,
			},
			PreviousCertificate: nil,
			LastL2BlockInCert:   10,
		})
		require.ErrorContains(t, err, "failed to get L1 Info tree root by leaf count 10")
	})

	t.Run("fails flowPP.GenerateBuildParams", func(t *testing.T) {
		testData := newTestDataCertificateValidator(t)
		testData.mockCertQuerier.EXPECT().GetNewCertificateToBlock(testData.ctx, mock.Anything).Return(uint64(10), nil)
		testData.mockLERQuerier.EXPECT().GetLastLocalExitRoot().Return(types.EmptyLER, nil)
		testData.mockCertQuerier.EXPECT().CalculateCertificateType(mock.Anything, uint64(10)).Return(types.CertificateTypePP)
		testData.mockL1InfoTreeQuerier.EXPECT().
			GetL1InfoRootByLeafIndex(testData.ctx, uint32(9)).Return(&testTreeRootIndex9, nil).Maybe()
		testData.mockFlow.EXPECT().
			GenerateBuildParams(testData.ctx, mock.Anything).Return(nil, errGenericForTesting)
		err := testData.sut.ValidateCertificate(testData.ctx, types.VerifyIncomingRequest{
			Certificate: &agglayertypes.Certificate{
				Height:              0,
				L1InfoTreeLeafCount: 10,
				PrevLocalExitRoot:   types.EmptyLER,
			},
			PreviousCertificate: nil,
			LastL2BlockInCert:   10,
		})
		require.ErrorContains(t, err, "failed flow.GenerateBuildParams")
	})

	t.Run("fails flowPP.BuildCertificate", func(t *testing.T) {
		testData := newTestDataCertificateValidator(t)
		testData.mockCertQuerier.EXPECT().GetNewCertificateToBlock(testData.ctx, mock.Anything).Return(uint64(10), nil)
		testData.mockLERQuerier.EXPECT().GetLastLocalExitRoot().Return(types.EmptyLER, nil)
		testData.mockCertQuerier.EXPECT().CalculateCertificateType(mock.Anything, uint64(10)).Return(types.CertificateTypePP)
		testData.mockL1InfoTreeQuerier.EXPECT().
			GetL1InfoRootByLeafIndex(testData.ctx, uint32(9)).Return(&testTreeRootIndex9, nil).Maybe()
		testData.mockFlow.EXPECT().
			GenerateBuildParams(testData.ctx, mock.Anything).Return(&types.CertificateBuildParams{}, nil)
		testData.mockFlow.EXPECT().
			BuildCertificate(testData.ctx, mock.Anything).Return(nil, errGenericForTesting)
		err := testData.sut.ValidateCertificate(testData.ctx, types.VerifyIncomingRequest{
			Certificate: &agglayertypes.Certificate{
				Height:              0,
				L1InfoTreeLeafCount: 10,
				PrevLocalExitRoot:   types.EmptyLER,
			},
			PreviousCertificate: nil,
			LastL2BlockInCert:   10,
		})
		require.ErrorContains(t, err, "failed flow.BuildCertificate")
	})

	t.Run("fails CompareCertificates", func(t *testing.T) {
		testData := newTestDataCertificateValidator(t)
		testData.mockCertQuerier.EXPECT().GetNewCertificateToBlock(testData.ctx, mock.Anything).Return(uint64(10), nil)
		testData.mockLERQuerier.EXPECT().GetLastLocalExitRoot().Return(types.EmptyLER, nil)
		testData.mockCertQuerier.EXPECT().CalculateCertificateType(mock.Anything, uint64(10)).Return(types.CertificateTypePP)
		testData.mockL1InfoTreeQuerier.EXPECT().
			GetL1InfoRootByLeafIndex(testData.ctx, uint32(9)).Return(&testTreeRootIndex9, nil).Maybe()
		testData.mockFlow.EXPECT().
			GenerateBuildParams(testData.ctx, mock.Anything).Return(&types.CertificateBuildParams{}, nil)
		testData.mockFlow.EXPECT().
			BuildCertificate(testData.ctx, mock.Anything).Return(&agglayertypes.Certificate{}, nil)
		err := testData.sut.ValidateCertificate(testData.ctx, types.VerifyIncomingRequest{
			Certificate: &agglayertypes.Certificate{
				Height:              0,
				L1InfoTreeLeafCount: 10,
				PrevLocalExitRoot:   types.EmptyLER,
			},
			PreviousCertificate: nil,
			LastL2BlockInCert:   10,
		})
		require.ErrorContains(t, err, "certificate not equal to expected")
	})

	t.Run("fails VerifyCertificate in flow", func(t *testing.T) {
		testData := newTestDataCertificateValidator(t)
		certificate := &agglayertypes.Certificate{
			Height:              0,
			L1InfoTreeLeafCount: 10,
			PrevLocalExitRoot:   types.EmptyLER,
		}

		testData.mockCertQuerier.EXPECT().GetNewCertificateToBlock(testData.ctx, mock.Anything).Return(uint64(10), nil)
		testData.mockLERQuerier.EXPECT().GetLastLocalExitRoot().Return(types.EmptyLER, nil)
		testData.mockCertQuerier.EXPECT().CalculateCertificateType(mock.Anything, uint64(10)).Return(types.CertificateTypePP)
		testData.mockL1InfoTreeQuerier.EXPECT().
			GetL1InfoRootByLeafIndex(testData.ctx, uint32(9)).Return(&testTreeRootIndex9, nil).Maybe()
		testData.mockFlow.EXPECT().
			GenerateBuildParams(testData.ctx, mock.Anything).Return(&types.CertificateBuildParams{
			L1InfoTreeRootFromWhichToProve: common.HexToHash("0x123"),
		}, nil)
		testData.mockFlow.EXPECT().
			BuildCertificate(testData.ctx, mock.Anything).Return(certificate, nil)
		testData.mockFlow.EXPECT().
			VerifyCertificate(testData.ctx, certificate, uint64(10), uint64(0)).Return(errGenericForTesting)

		err := testData.sut.ValidateCertificate(testData.ctx, types.VerifyIncomingRequest{
			Certificate:         certificate,
			PreviousCertificate: nil,
			LastL2BlockInCert:   10,
		})
		require.ErrorContains(t, err, "failed to verify certificate in flow")
	})
}

func TestCheckContigousCertificates(t *testing.T) {
	t.Run("Nil PreviousCertificate, err getting start LER", func(t *testing.T) {
		testData := newTestDataCertificateValidator(t)
		testData.mockLERQuerier.EXPECT().GetLastLocalExitRoot().Return(types.EmptyLER, errors.New("some error"))
		err := testData.sut.checkContigousCertificates(types.VerifyIncomingRequest{
			Certificate: &agglayertypes.Certificate{
				Height: 0,
			},
			PreviousCertificate: nil,
		})
		require.ErrorContains(t, err, "failed to get start LER: some error")
	})

	t.Run("Nil PreviousCertificate, cert height!= 0", func(t *testing.T) {
		testData := newTestDataCertificateValidator(t)
		err := testData.sut.checkContigousCertificates(types.VerifyIncomingRequest{
			Certificate: &agglayertypes.Certificate{
				Height: 1,
			},
			PreviousCertificate: nil,
		})
		require.ErrorContains(t, err, "first certificate must have height 0, but got: 1")
	})

	t.Run("Nil PreviousCertificate, cert height == 0, non expected LER", func(t *testing.T) {
		testData := newTestDataCertificateValidator(t)
		testData.mockLERQuerier.EXPECT().GetLastLocalExitRoot().Return(types.EmptyLER, nil)
		err := testData.sut.checkContigousCertificates(types.VerifyIncomingRequest{
			Certificate: &agglayertypes.Certificate{
				Height:            0,
				PrevLocalExitRoot: common.HexToHash("0x1"),
			},
			PreviousCertificate: nil,
		})
		require.ErrorContains(t, err, "first certificate must have correct starting PrevLocalExitRoot")
	})

	t.Run("Non Contiguous LER in Certificates", func(t *testing.T) {
		testData := newTestDataCertificateValidator(t)
		err := testData.sut.checkContigousCertificates(types.VerifyIncomingRequest{
			Certificate: &agglayertypes.Certificate{
				Height:            2,
				PrevLocalExitRoot: common.HexToHash("0x2"),
			},
			PreviousCertificate: &agglayertypes.CertificateHeader{
				Height:           1,
				NewLocalExitRoot: common.HexToHash("0x1"),
			}})
		require.ErrorContains(t, err, "not equal to previous certificate NewLocalExitRoot")
	})

	t.Run("Non-Contiguous Certificates", func(t *testing.T) {
		testData := newTestDataCertificateValidator(t)
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
	t.Run("Nil Certificates", func(t *testing.T) {
		testData := newTestDataCertificateValidator(t)
		_, err := testData.sut.getCertificatePreBuildParams(testData.ctx, types.VerifyIncomingRequest{}, 0)
		require.ErrorIs(t, err, ErrNilCertificate)
	})

	t.Run("fails GetL1InfoRootByLeafIndex", func(t *testing.T) {
		testData := newTestDataCertificateValidator(t)
		testData.mockCertQuerier.EXPECT().CalculateCertificateType(mock.Anything, uint64(20)).Return(types.CertificateTypePP)
		testData.mockL1InfoTreeQuerier.EXPECT().
			GetL1InfoRootByLeafIndex(testData.ctx, uint32(9)).Return(nil, errGenericForTesting)
		_, err := testData.sut.getCertificatePreBuildParams(testData.ctx, types.VerifyIncomingRequest{
			Certificate: &agglayertypes.Certificate{
				Height:              2,
				L1InfoTreeLeafCount: 10,
			},
			PreviousCertificate: &agglayertypes.CertificateHeader{
				Height:   13,
				Metadata: aggkitcommon.ZeroHash,
			},
			LastL2BlockInCert: 20,
		}, 10)
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
	mockFlow              *mocks.AggsenderVerifierFlow
	mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier
	mockCertQuerier       *mocks.CertificateQuerier
	mockLERQuerier        *mocks.LERQuerier
	sut                   *CertificateValidator
}

func newTestDataCertificateValidator(t *testing.T) testDataCertificateValidator {
	t.Helper()
	mockLogger := log.WithFields("test", "TestValidateCertificate")
	mockFlow := mocks.NewAggsenderVerifierFlow(t)
	mockL1InfoTreeQuerier := mocks.NewL1InfoTreeDataQuerier(t)
	mockCertQuerier := mocks.NewCertificateQuerier(t)
	lerQuerier := mocks.NewLERQuerier(t)

	return testDataCertificateValidator{
		ctx:                   context.TODO(),
		logger:                mockLogger,
		mockFlow:              mockFlow,
		mockL1InfoTreeQuerier: mockL1InfoTreeQuerier,
		mockCertQuerier:       mockCertQuerier,
		mockLERQuerier:        lerQuerier,
		sut:                   NewAggsenderValidator(mockLogger, mockFlow, mockL1InfoTreeQuerier, mockCertQuerier, lerQuerier),
	}
}
