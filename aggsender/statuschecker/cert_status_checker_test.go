package statuschecker

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/agglayer/aggkit/agglayer"
	agglayermocks "github.com/agglayer/aggkit/agglayer/mocks"
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
			mockAggLayerClient := agglayermocks.NewAgglayerClientMock(t)
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

			sut := NewCertStatusChecker(mockLogger, mockStorage, mockAggLayerClient, nil, 1)
			certStatusChecker, ok := sut.(*certStatusChecker)
			require.True(t, ok)
			ctx := context.TODO()
			checkResult := certStatusChecker.checkPendingCertificatesStatus(ctx)
			require.Equal(t, tt.expectedError, checkResult.ExistPendingCerts)
			mockAggLayerClient.AssertExpectations(t)
			mockStorage.AssertExpectations(t)
		})
	}
}

func TestNewSettledCertificateInfoFromAgglayerCertHeader(t *testing.T) {
	t.Parallel()

	previousLER := common.HexToHash("0xdef")

	tests := []struct {
		name           string
		inputHeader    *agglayertypes.CertificateHeader
		mockFn         func(*mocks.CertificateQuerier)
		expectedResult *types.Certificate
		expectedError  string
	}{
		{
			name:           "Nil input header",
			inputHeader:    nil,
			expectedResult: nil,
		},
		{
			name: "cert querier error",
			inputHeader: &agglayertypes.CertificateHeader{
				Height: 100,
				Status: agglayertypes.Settled,
			},
			mockFn: func(m *mocks.CertificateQuerier) {
				m.EXPECT().GetLastSettledCertificateToBlock(t.Context(), &agglayertypes.CertificateHeader{
					Height: 100,
					Status: agglayertypes.Settled,
				}).Return(uint64(0), fmt.Errorf("querier error"))
			},
			expectedError: "error getting last settled certificate to block: querier error",
		},
		{
			name: "Valid settled certificate header",
			inputHeader: &agglayertypes.CertificateHeader{
				Height:                100,
				CertificateID:         common.HexToHash("0x1"),
				PreviousLocalExitRoot: &previousLER,
				NewLocalExitRoot:      common.HexToHash("0xabc"),
				Status:                agglayertypes.Settled,
			},
			mockFn: func(m *mocks.CertificateQuerier) {
				m.EXPECT().GetLastSettledCertificateToBlock(t.Context(), &agglayertypes.CertificateHeader{
					Height:                100,
					CertificateID:         common.HexToHash("0x1"),
					PreviousLocalExitRoot: &previousLER,
					NewLocalExitRoot:      common.HexToHash("0xabc"),
					Status:                agglayertypes.Settled,
				}).Return(uint64(200), nil)
				m.EXPECT().CalculateCertificateTypeFromToBlock(uint64(200)).Return(types.CertificateTypePP)
			},
			expectedResult: &types.Certificate{
				Header: &types.CertificateHeader{
					Height:                100,
					CertificateID:         common.HexToHash("0x1"),
					PreviousLocalExitRoot: &previousLER,
					NewLocalExitRoot:      common.HexToHash("0xabc"),
					Status:                agglayertypes.Settled,
					CertSource:            types.CertificateSourceAggLayer,
					CertType:              types.CertificateTypePP,
					ToBlock:               200,
					FromBlock:             0,
					CreatedAt:             0,
					UpdatedAt:             0,
				},
				SignedCertificate: &naAgglayerHeader,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockCertQuerier := mocks.NewCertificateQuerier(t)
			if tt.mockFn != nil {
				tt.mockFn(mockCertQuerier)
			}

			certStatusChecker := &certStatusChecker{
				certQuerier: mockCertQuerier,
			}

			result, err := certStatusChecker.newSettledCertificateInfoFromAgglayerCertHeader(t.Context(), tt.inputHeader)
			if tt.expectedError != "" {
				require.ErrorContains(t, err, tt.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedResult, result)
			}

			mockCertQuerier.AssertExpectations(t)
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
			mockCertQuerier := mocks.NewCertificateQuerier(t)

			if tt.inputCert != nil {
				mockCertQuerier.EXPECT().GetLastSettledCertificateToBlock(mock.Anything, tt.inputCert).Return(uint64(100), nil)
				mockCertQuerier.EXPECT().CalculateCertificateTypeFromToBlock(uint64(100)).Return(types.CertificateTypePP)
				mockStorage.EXPECT().SaveOrUpdateCertificate(mock.Anything, mock.MatchedBy(func(cert types.Certificate) bool {
					return cert.Header.CertificateID == tt.expectedResult.Header.CertificateID
				})).Return(tt.saveError)
			}

			certStatusChecker := &certStatusChecker{
				log:             mockLogger,
				storage:         mockStorage,
				certQuerier:     mockCertQuerier,
				l2OriginNetwork: 1,
			}

			ctx := context.TODO()
			result, err := certStatusChecker.updateLocalStorageWithSettledAggLayerCert(ctx, tt.inputCert)

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
			mockCertQuerier.AssertExpectations(t)
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
		mockFn        func(*mocks.AggSenderStorage, *mocks.CertificateQuerier)
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
			mockFn: func(mockStorage *mocks.AggSenderStorage, mockCertQuerier *mocks.CertificateQuerier) {
				mockStorage.EXPECT().UpdateCertificateStatus(ctx, common.HexToHash("0x1"), agglayertypes.InError, mock.Anything).Return(fmt.Errorf("update error"))
			},
			expectedError: "recovery: error updating local storage with agglayer certificate",
		},
		{
			name: "Action InsertNewCert - success",
			action: &initialStatusResult{
				action: InitialStatusActionInsertNewCert,
				cert:   &agglayertypes.CertificateHeader{CertificateID: common.HexToHash("0x2"), Status: agglayertypes.Settled},
			},
			mockFn: func(mockStorage *mocks.AggSenderStorage, mockCertQuerier *mocks.CertificateQuerier) {
				mockCertQuerier.EXPECT().GetLastSettledCertificateToBlock(ctx, mock.Anything).Return(uint64(10), nil)
				mockCertQuerier.EXPECT().CalculateCertificateTypeFromToBlock(uint64(10)).Return(types.CertificateTypePP)
				mockStorage.EXPECT().SaveOrUpdateCertificate(ctx, mock.Anything).Return(nil)
			},
		},
		{
			name: "Action InsertNewCert - error",
			action: &initialStatusResult{
				action: InitialStatusActionInsertNewCert,
				cert:   &agglayertypes.CertificateHeader{CertificateID: common.HexToHash("0x2"), Status: agglayertypes.Settled},
			},
			mockFn: func(mockStorage *mocks.AggSenderStorage, mockCertQuerier *mocks.CertificateQuerier) {
				mockCertQuerier.EXPECT().GetLastSettledCertificateToBlock(ctx, mock.Anything).Return(uint64(10), nil)
				mockCertQuerier.EXPECT().CalculateCertificateTypeFromToBlock(uint64(10)).Return(types.CertificateTypePP)
				mockStorage.EXPECT().SaveOrUpdateCertificate(ctx, mock.Anything).Return(fmt.Errorf("insert error"))
			},
			expectedError: "recovery: error new local storage with agglayer certificate",
		},
		{
			name: "Action InsertNewCert - InError status",
			action: &initialStatusResult{
				action: InitialStatusActionInsertNewCert,
				cert:   &agglayertypes.CertificateHeader{CertificateID: common.HexToHash("0x3"), Status: agglayertypes.InError},
			},
		},
		{
			name: "Action InsertNewCert - Pending status",
			action: &initialStatusResult{
				action: InitialStatusActionInsertNewCert,
				cert:   &agglayertypes.CertificateHeader{CertificateID: common.HexToHash("0x4"), Status: agglayertypes.Pending},
			},
			expectedError: "Waiting for it to be settled",
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
			mockCertQuerier := mocks.NewCertificateQuerier(t)

			if tt.mockFn != nil {
				tt.mockFn(mockStorage, mockCertQuerier)
			}

			certStatusChecker := &certStatusChecker{
				log:         mockLogger,
				storage:     mockStorage,
				certQuerier: mockCertQuerier,
			}

			err := certStatusChecker.executeInitialStatusAction(ctx, tt.action, tt.localCert)

			if tt.expectedError != "" {
				require.ErrorContains(t, err, tt.expectedError)
			} else {
				require.NoError(t, err)
			}

			mockStorage.AssertExpectations(t)
			mockCertQuerier.AssertExpectations(t)
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
				m.EXPECT().UpdateCertificateStatus(ctx, common.HexToHash("0x1"), agglayertypes.Settled, mock.Anything).Return(nil).Once()
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
				m.EXPECT().UpdateCertificateStatus(ctx, common.HexToHash("0x1"), agglayertypes.InError, mock.Anything).Return(fmt.Errorf("update error")).Once()
			},
			expectedError: "recovery: error updating local storage with agglayer certificate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockLogger := log.WithFields("test", "unittest")
			mockStorage := mocks.NewAggSenderStorage(t)
			mockAggLayerClient := agglayermocks.NewAgglayerClientMock(t)

			if tt.mockFn != nil {
				tt.mockFn(mockStorage)
			}

			mockInitialStatus := &initialStatus{
				log:                     mockLogger,
				LocalLastCert:           tt.localCert,
				AgglayerLastSettledCert: tt.agglayerCert,
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

func TestCheckPeriodicallyStatus(t *testing.T) {
	tests := []struct {
		name           string
		newInitialErr  error
		processErr     error
		action         *initialStatusResult
		localCert      *types.CertificateHeader
		agglayerCert   *agglayertypes.CertificateHeader
		mockFn         func(m *mocks.AggSenderStorage)
		expectedError  string
		expectedStatus types.CertStatus
	}{

		{
			name: "cert local InError, agglayer Settled",
			localCert: &types.CertificateHeader{
				CertificateID: common.HexToHash("0x1"),
				Status:        agglayertypes.InError,
			},
			agglayerCert: &agglayertypes.CertificateHeader{
				CertificateID: common.HexToHash("0x1"),
				Status:        agglayertypes.Settled,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.TODO()
			mockLogger := log.WithFields("test", "unittest")
			mockStorage := mocks.NewAggSenderStorage(t)
			mockAggLayerClient := agglayermocks.NewAgglayerClientMock(t)
			mockInitialStatus := &initialStatus{
				log:                     mockLogger,
				LocalLastCert:           tt.localCert,
				AgglayerLastSettledCert: tt.agglayerCert,
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
			mockStorage.EXPECT().GetCertificateHeadersByStatus(mock.Anything).Return(
				[]*types.CertificateHeader{tt.localCert}, nil)
			mockAggLayerClient.EXPECT().GetCertificateHeader(mock.Anything,
				mock.Anything).Return(tt.agglayerCert, nil)
			mockStorage.EXPECT().UpdateCertificateStatus(mock.Anything,
				tt.localCert.CertificateID,
				tt.agglayerCert.Status,
				mock.Anything).Return(nil)
			status, err := certStatusChecker.CheckPeriodicallyStatus(ctx)
			if tt.expectedError != "" {
				require.ErrorContains(t, err, tt.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedStatus, status)
			}

			mockStorage.AssertExpectations(t)
			mockAggLayerClient.AssertExpectations(t)
		})
	}
}

func TestCheckInitialStatus(t *testing.T) {
	ctx := t.Context()
	mockLogger := log.WithFields("test", "unittest")
	mockStorage := mocks.NewAggSenderStorage(t)
	mockAggLayerClient := agglayermocks.NewAgglayerClientMock(t)

	newInitialStatusFn = func(_ context.Context,
		_ types.Logger, _ uint32,
		_ db.AggSenderStorage,
		_ agglayer.AggLayerClientRecoveryQuerier) (*initialStatus, error) {
		return nil, fmt.Errorf("error")
	}

	certStatusChecker := &certStatusChecker{
		log:            mockLogger,
		storage:        mockStorage,
		agglayerClient: mockAggLayerClient,
	}
	mockStorage.EXPECT().GetCertificateHeadersByStatus(mock.Anything).Return(
		nil, fmt.Errorf("error"))
	aggsenderStatus := &types.AggsenderStatus{}
	ctx, cancel := context.WithTimeout(ctx, time.Millisecond*10)
	defer cancel()
	certStatusChecker.CheckInitialStatus(ctx, time.Millisecond, aggsenderStatus)
	require.Equal(t, "recovery: error retrieving initial status: error", aggsenderStatus.LastError)
}
