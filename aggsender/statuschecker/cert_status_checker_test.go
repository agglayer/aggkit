package statuschecker

import (
	"context"
	"fmt"
	"math/big"
	"testing"

	"github.com/agglayer/aggkit/agglayer"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestCheckIfCertificatesAreSettled(t *testing.T) {
	tests := []struct {
		name                     string
		pendingCertificates      []*types.CertificateHeader
		certificateHeaders       map[common.Hash]*agglayertypes.CertificateHeader
		getFromDBError           error
		clientError              error
		updateDBError            error
		expectedErrorLogMessages []string
		expectedInfoMessages     []string
		expectedError            bool
	}{
		{
			name: "All certificates settled - update successful",
			pendingCertificates: []*types.CertificateHeader{
				{CertificateID: common.HexToHash("0x1"), Height: 1},
				{CertificateID: common.HexToHash("0x2"), Height: 2},
			},
			certificateHeaders: map[common.Hash]*agglayertypes.CertificateHeader{
				common.HexToHash("0x1"): {Status: agglayertypes.Settled},
				common.HexToHash("0x2"): {Status: agglayertypes.Settled},
			},
			expectedInfoMessages: []string{
				"certificate %s changed status to %s",
			},
		},
		{
			name: "Some certificates in error - update successful",
			pendingCertificates: []*types.CertificateHeader{
				{CertificateID: common.HexToHash("0x1"), Height: 1},
				{CertificateID: common.HexToHash("0x2"), Height: 2},
			},
			certificateHeaders: map[common.Hash]*agglayertypes.CertificateHeader{
				common.HexToHash("0x1"): {Status: agglayertypes.InError},
				common.HexToHash("0x2"): {Status: agglayertypes.Settled},
			},
			expectedInfoMessages: []string{
				"certificate %s changed status to %s",
			},
		},
		{
			name:           "Error getting pending certificates",
			getFromDBError: fmt.Errorf("storage error"),
			expectedErrorLogMessages: []string{
				"error getting pending certificates: %w",
			},
			expectedError: true,
		},
		{
			name: "Error getting certificate header",
			pendingCertificates: []*types.CertificateHeader{
				{CertificateID: common.HexToHash("0x1"), Height: 1},
			},
			certificateHeaders: map[common.Hash]*agglayertypes.CertificateHeader{
				common.HexToHash("0x1"): {Status: agglayertypes.InError},
			},
			clientError: fmt.Errorf("client error"),
			expectedErrorLogMessages: []string{
				"error getting header of certificate %s with height: %d from agglayer: %w",
			},
			expectedError: true,
		},
		{
			name: "Error updating certificate status",
			pendingCertificates: []*types.CertificateHeader{
				{CertificateID: common.HexToHash("0x1"), Height: 1},
			},
			certificateHeaders: map[common.Hash]*agglayertypes.CertificateHeader{
				common.HexToHash("0x1"): {Status: agglayertypes.Settled},
			},
			updateDBError: fmt.Errorf("update error"),
			expectedErrorLogMessages: []string{
				"error updating certificate status in storage: %w",
			},
			expectedInfoMessages: []string{
				"certificate %s changed status to %s",
			},
			expectedError: true,
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			mockStorage := mocks.NewAggSenderStorage(t)
			mockAggLayerClient := agglayer.NewAgglayerClientMock(t)
			mockLogger := log.WithFields("test", "unittest")

			mockStorage.EXPECT().GetCertificateHeadersByStatus(agglayertypes.NonSettledStatuses).Return(
				tt.pendingCertificates, tt.getFromDBError)
			for certID, header := range tt.certificateHeaders {
				mockAggLayerClient.EXPECT().GetCertificateHeader(mock.Anything, certID).Return(header, tt.clientError)
			}
			if tt.updateDBError != nil {
				mockStorage.EXPECT().UpdateCertificateStatus(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(tt.updateDBError)
			} else if tt.clientError == nil && tt.getFromDBError == nil {
				mockStorage.EXPECT().UpdateCertificateStatus(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
			}

			certStatusChecker := NewCertStatusChecker(mockLogger, mockStorage, mockAggLayerClient, 1)

			ctx := context.TODO()
			checkResult := certStatusChecker.CheckPendingCertificatesStatus(ctx)
			require.Equal(t, tt.expectedError, checkResult.ExistPendingCerts)
			mockAggLayerClient.AssertExpectations(t)
			mockStorage.AssertExpectations(t)
		})
	}
}

func TestNewCertificateInfoFromAgglayerCertHeader(t *testing.T) {
	t.Parallel()

	previousLER := common.HexToHash("0xdef")

	tests := []struct {
		name           string
		inputHeader    *agglayertypes.CertificateHeader
		expectedResult *types.Certificate
	}{
		{
			name:           "Nil input header",
			inputHeader:    nil,
			expectedResult: nil,
		},
		{
			name: "Valid input header with version >= 1",
			inputHeader: &agglayertypes.CertificateHeader{
				Height:                100,
				CertificateID:         common.HexToHash("0x1"),
				NewLocalExitRoot:      common.HexToHash("0xabc"),
				Metadata:              (&types.CertificateMetadata{FromBlock: 10, Offset: 5, CreatedAt: 1234567890, Version: 1}).ToHash(),
				Status:                agglayertypes.Settled,
				PreviousLocalExitRoot: &previousLER,
			},
			expectedResult: &types.Certificate{
				Header: &types.CertificateHeader{
					Height:                100,
					CertificateID:         common.HexToHash("0x1"),
					NewLocalExitRoot:      common.HexToHash("0xabc"),
					FromBlock:             10,
					ToBlock:               15,
					Status:                agglayertypes.Settled,
					CreatedAt:             1234567890,
					PreviousLocalExitRoot: &previousLER,
				},
				SignedCertificate: &naAgglayerHeader,
			},
		},
		{
			name: "Valid input header with version < 1",
			inputHeader: &agglayertypes.CertificateHeader{
				Height:           200,
				CertificateID:    common.HexToHash("0x2"),
				NewLocalExitRoot: common.HexToHash("0x123"),
				Metadata:         common.BigToHash(big.NewInt(25)),
				Status:           agglayertypes.InError,
			},
			expectedResult: &types.Certificate{
				Header: &types.CertificateHeader{
					Height:           200,
					CertificateID:    common.HexToHash("0x2"),
					NewLocalExitRoot: common.HexToHash("0x123"),
					FromBlock:        0,
					ToBlock:          25,
					Status:           agglayertypes.InError,
				},
				SignedCertificate: &naAgglayerHeader,
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := newCertificateInfoFromAgglayerCertHeader(tt.inputHeader)
			if tt.expectedResult == nil {
				require.Nil(t, result)
			} else {
				require.NotNil(t, result)
				require.Equal(t, tt.expectedResult.Header.Height, result.Header.Height)
				require.Equal(t, tt.expectedResult.Header.CertificateID, result.Header.CertificateID)
				require.Equal(t, tt.expectedResult.Header.NewLocalExitRoot, result.Header.NewLocalExitRoot)
				require.Equal(t, tt.expectedResult.Header.FromBlock, result.Header.FromBlock)
				require.Equal(t, tt.expectedResult.Header.ToBlock, result.Header.ToBlock)
				require.Equal(t, tt.expectedResult.Header.Status, result.Header.Status)
				require.Equal(t, tt.expectedResult.Header.PreviousLocalExitRoot, result.Header.PreviousLocalExitRoot)
				require.Equal(t, tt.expectedResult.SignedCertificate, result.SignedCertificate)
			}
		})
	}
}

func TestUpdateLocalStorageWithAggLayerCert(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		inputCert      *agglayertypes.CertificateHeader
		saveError      error
		expectedResult *types.Certificate
		expectedError  bool
	}{
		{
			name:      "Valid certificate header - save successful",
			inputCert: &agglayertypes.CertificateHeader{Height: 100, CertificateID: common.HexToHash("0x1"), NewLocalExitRoot: common.HexToHash("0xabc")},
			expectedResult: &types.Certificate{
				Header: &types.CertificateHeader{
					Height:           100,
					CertificateID:    common.HexToHash("0x1"),
					NewLocalExitRoot: common.HexToHash("0xabc"),
				},
				SignedCertificate: &naAgglayerHeader,
			},
			expectedError: false,
		},
		{
			name:           "Nil certificate header",
			inputCert:      nil,
			expectedResult: nil,
			expectedError:  false,
		},
		{
			name:      "Error saving certificate to storage",
			inputCert: &agglayertypes.CertificateHeader{Height: 200, CertificateID: common.HexToHash("0x2"), NewLocalExitRoot: common.HexToHash("0xdef")},
			saveError: fmt.Errorf("storage save error"),
			expectedResult: &types.Certificate{
				Header: &types.CertificateHeader{
					Height:           200,
					CertificateID:    common.HexToHash("0x2"),
					NewLocalExitRoot: common.HexToHash("0xdef"),
				},
				SignedCertificate: &naAgglayerHeader,
			},
			expectedError: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockStorage := mocks.NewAggSenderStorage(t)
			mockLogger := log.WithFields("test", "unittest")

			if tt.inputCert != nil {
				mockStorage.EXPECT().SaveLastSentCertificate(mock.Anything, mock.MatchedBy(func(cert types.Certificate) bool {
					return cert.Header.CertificateID == tt.expectedResult.Header.CertificateID
				})).Return(tt.saveError)
			}

			certStatusChecker := &certStatusChecker{
				log:             mockLogger,
				storage:         mockStorage,
				l2OriginNetwork: 1,
			}

			ctx := context.TODO()
			result, err := certStatusChecker.updateLocalStorageWithAggLayerCert(ctx, tt.inputCert)

			if tt.expectedError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tt.expectedResult == nil {
				require.Nil(t, result)
			} else {
				require.NotNil(t, result)
				require.Equal(t, tt.expectedResult.Header.CertificateID, result.Header.CertificateID)
				require.Equal(t, tt.expectedResult.Header.NewLocalExitRoot, result.Header.NewLocalExitRoot)
				require.Equal(t, tt.expectedResult.Header.Height, result.Header.Height)
			}

			mockStorage.AssertExpectations(t)
		})
	}
}

func TestExecuteInitialStatusAction(t *testing.T) {
	t.Parallel()

	ctx := context.TODO()

	tests := []struct {
		name          string
		action        *initialStatusResult
		localCert     *types.CertificateHeader
		mockFn        func(m *mocks.AggSenderStorage)
		expectedError string
	}{
		{
			name: "Action None",
			action: &initialStatusResult{
				action: InitialStatusActionNone,
			},
		},
		{
			name: "Action UpdateCurrentCert - success",
			action: &initialStatusResult{
				action: InitialStatusActionUpdateCurrentCert,
				cert:   &agglayertypes.CertificateHeader{CertificateID: common.HexToHash("0x1")},
			},
			localCert: &types.CertificateHeader{CertificateID: common.HexToHash("0x1")},
		},
		{
			name: "Action UpdateCurrentCert - error",
			action: &initialStatusResult{
				action: InitialStatusActionUpdateCurrentCert,
				cert:   &agglayertypes.CertificateHeader{CertificateID: common.HexToHash("0x1"), Status: agglayertypes.InError},
			},
			localCert: &types.CertificateHeader{CertificateID: common.HexToHash("0x1")},
			mockFn: func(m *mocks.AggSenderStorage) {
				m.EXPECT().UpdateCertificateStatus(ctx, common.HexToHash("0x1"), agglayertypes.InError, mock.Anything).Return(fmt.Errorf("update error"))
			},
			expectedError: "recovery: error updating local storage with agglayer certificate",
		},
		{
			name: "Action InsertNewCert - success",
			action: &initialStatusResult{
				action: InitialStatusActionInsertNewCert,
				cert:   &agglayertypes.CertificateHeader{CertificateID: common.HexToHash("0x2")},
			},
			mockFn: func(m *mocks.AggSenderStorage) {
				m.EXPECT().SaveLastSentCertificate(ctx, mock.Anything).Return(nil)
			},
		},
		{
			name: "Action InsertNewCert - error",
			action: &initialStatusResult{
				action: InitialStatusActionInsertNewCert,
				cert:   &agglayertypes.CertificateHeader{CertificateID: common.HexToHash("0x2")},
			},
			mockFn: func(m *mocks.AggSenderStorage) {
				m.EXPECT().SaveLastSentCertificate(ctx, mock.Anything).Return(fmt.Errorf("insert error"))
			},
			expectedError: "recovery: error new local storage with agglayer certificate",
		},
		{
			name: "Unknown Action",
			action: &initialStatusResult{
				action: initialStatusAction(-1111111),
			},
			expectedError: "recovery: unknown action",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockLogger := log.WithFields("test", "unittest")
			mockStorage := mocks.NewAggSenderStorage(t)

			if tt.mockFn != nil {
				tt.mockFn(mockStorage)
			}

			certStatusChecker := &certStatusChecker{
				log:     mockLogger,
				storage: mockStorage,
			}

			err := certStatusChecker.executeInitialStatusAction(ctx, tt.action, tt.localCert)

			if tt.expectedError != "" {
				require.ErrorContains(t, err, tt.expectedError)
			} else {
				require.NoError(t, err)
			}

			mockStorage.AssertExpectations(t)
		})
	}
}

func TestCheckLastCertificateFromAgglayer(t *testing.T) {
	ctx := context.TODO()

	tests := []struct {
		name          string
		newInitialErr error
		processErr    error
		action        *initialStatusResult
		localCert     *types.CertificateHeader
		agglayerCert  *agglayertypes.CertificateHeader
		mockFn        func(m *mocks.AggSenderStorage)
		expectedError string
	}{
		{
			name:          "Error retrieving initial status",
			newInitialErr: fmt.Errorf("initial status error"),
			expectedError: "recovery: error retrieving initial status",
		},
		{
			name: "Successful execution of action",
			action: &initialStatusResult{
				action: InitialStatusActionUpdateCurrentCert,
				cert:   &agglayertypes.CertificateHeader{CertificateID: common.HexToHash("0x1")},
			},
			localCert:    &types.CertificateHeader{CertificateID: common.HexToHash("0x1")},
			agglayerCert: &agglayertypes.CertificateHeader{CertificateID: common.HexToHash("0x1"), Status: agglayertypes.Settled},
			mockFn: func(m *mocks.AggSenderStorage) {
				m.EXPECT().UpdateCertificateStatus(ctx, common.HexToHash("0x1"), agglayertypes.Settled, mock.Anything).Return(nil)
			},
		},
		{
			name: "Error executing action",
			action: &initialStatusResult{
				action: InitialStatusActionUpdateCurrentCert,
				cert:   &agglayertypes.CertificateHeader{CertificateID: common.HexToHash("0x1")},
			},
			localCert:    &types.CertificateHeader{CertificateID: common.HexToHash("0x1")},
			agglayerCert: &agglayertypes.CertificateHeader{CertificateID: common.HexToHash("0x1"), Status: agglayertypes.InError},
			mockFn: func(m *mocks.AggSenderStorage) {
				m.EXPECT().UpdateCertificateStatus(ctx, common.HexToHash("0x1"), agglayertypes.InError, mock.Anything).Return(fmt.Errorf("update error"))
			},
			expectedError: "recovery: error updating local storage with agglayer certificate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockLogger := log.WithFields("test", "unittest")
			mockStorage := mocks.NewAggSenderStorage(t)
			mockAggLayerClient := agglayer.NewAgglayerClientMock(t)

			if tt.mockFn != nil {
				tt.mockFn(mockStorage)
			}

			mockInitialStatus := &initialStatus{
				log:         mockLogger,
				LocalCert:   tt.localCert,
				SettledCert: tt.agglayerCert,
			}

			newInitialStatusFn = func(_ context.Context,
				_ types.Logger, _ uint32,
				_ db.AggSenderStorage,
				_ agglayer.AggLayerClientRecoveryQuerier) (*initialStatus, error) {
				return mockInitialStatus, tt.newInitialErr
			}

			certStatusChecker := &certStatusChecker{
				log:            mockLogger,
				storage:        mockStorage,
				agglayerClient: mockAggLayerClient,
			}

			err := certStatusChecker.checkLastCertificateFromAgglayer(ctx)

			if tt.expectedError != "" {
				require.ErrorContains(t, err, tt.expectedError)
			} else {
				require.NoError(t, err)
			}

			mockStorage.AssertExpectations(t)
			mockAggLayerClient.AssertExpectations(t)
		})
	}
}
