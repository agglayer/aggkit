package chaingersender

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/0xPolygon/zkevm-ethtx-manager/ethtxmanager"
	"github.com/0xPolygon/zkevm-ethtx-manager/types"
	"github.com/agglayer/aggkit/aggoracle/mocks"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewEVMChainGERSender(t *testing.T) {
	txManagerMock := mocks.NewEthTxManager(t)
	txManagerMock.EXPECT().From().Return(common.HexToAddress("0x123")).Once()

	l2GERManagerMock := mocks.NewL2GERManagerContract(t)
	l2GERManagerMock.EXPECT().GlobalExitRootUpdater(mock.Anything).Return(common.HexToAddress("0x123"), nil).Once()

	gerSender, err := NewEVMChainGERSender(log.GetDefaultLogger(), EVMConfig{}, l2GERManagerMock, txManagerMock)
	require.NoError(t, err)
	require.NotNil(t, gerSender)
}

func TestNewEVMChainGERSender_SenderIsNotTheGERUpdater(t *testing.T) {
	txManagerMock := mocks.NewEthTxManager(t)
	txManagerMock.EXPECT().From().Return(common.HexToAddress("0x123")).Once()

	l2GERManagerMock := mocks.NewL2GERManagerContract(t)
	l2GERManagerMock.EXPECT().GlobalExitRootUpdater(mock.Anything).Return(common.HexToAddress("0x456"), nil).Once()

	gerSender, err := NewEVMChainGERSender(log.GetDefaultLogger(), EVMConfig{}, l2GERManagerMock, txManagerMock)
	require.ErrorContains(t, err, "invalid GER sender provided")
	require.Nil(t, gerSender)
}

func TestEVMChainGERSender_InjectGER(t *testing.T) {
	insertGERFuncABI := `[{
		"inputs": [
			{
				"internalType": "bytes32",
				"name": "_newRoot",
				"type": "bytes32"
			}
		],
		"name": "insertGlobalExitRoot",
		"outputs": [],
		"stateMutability": "nonpayable",
		"type": "function"
	}]`
	insertGERABI, err := abi.JSON(strings.NewReader(insertGERFuncABI))
	require.NoError(t, err)

	l2GERManagerAddr := common.HexToAddress("0x123")
	txID := common.HexToHash("0x789")
	ger := common.HexToHash("0x456")

	tests := []struct {
		name            string
		addReturnTxID   common.Hash
		addReturnErr    error
		resultReturn    *types.MonitoredTxResult
		resultReturnErr error
		expectedErr     string
	}{
		{
			name:          "successful transaction",
			addReturnTxID: txID,
			resultReturn:  &types.MonitoredTxResult{Status: types.MonitoredTxStatusMined, MinedAtBlockNumber: big.NewInt(123)},
		},
		{
			name:         "transaction fails due to Add method error",
			addReturnErr: errors.New("add error"),
			expectedErr:  "add error",
		},
		{
			name:          "transaction fails due to transaction failure",
			addReturnTxID: txID,
			resultReturn:  &types.MonitoredTxResult{Status: types.MonitoredTxStatusFailed},
			expectedErr:   "inject GER tx",
		},
		{
			name:            "transaction fails due to Result method error",
			addReturnTxID:   txID,
			resultReturn:    &types.MonitoredTxResult{},
			resultReturnErr: errors.New("result error"),
			expectedErr:     "result error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancelFn := context.WithTimeout(context.Background(), time.Millisecond*500)
			defer cancelFn()

			ethTxMan := mocks.NewEthTxManager(t)
			ethTxMan.EXPECT().
				Add(ctx, &l2GERManagerAddr, common.Big0, mock.Anything, mock.Anything, mock.Anything).
				Return(tt.addReturnTxID, tt.addReturnErr)
			if tt.resultReturn != nil {
				ethTxMan.EXPECT().Result(ctx, tt.addReturnTxID).Return(*tt.resultReturn, tt.resultReturnErr)
			}

			// The GER is not injected yet
			l2GERManager := mocks.NewL2GERManagerContract(t)
			l2GERManager.EXPECT().GlobalExitRootMap(mock.Anything, mock.Anything).Return(big.NewInt(0), nil)

			sender := &EVMChainGERSender{
				logger:              log.GetDefaultLogger(),
				l2GERManager:        l2GERManager,
				l2GERManagerAddr:    l2GERManagerAddr,
				l2GERManagerAbi:     &insertGERABI,
				ethTxMan:            ethTxMan,
				waitPeriodMonitorTx: time.Millisecond * 10,
			}

			err := sender.InjectGER(ctx, ger)
			if tt.expectedErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tt.expectedErr)
			}
		})
	}
}

// A GER that is already injected is not sent again
func TestEVMChainGERSender_ProcessGER_AlreadyInjected(t *testing.T) {
	// No expectation on the transaction manager: it must not be used
	ethTxMan := mocks.NewEthTxManager(t)
	l2GERManager := mocks.NewL2GERManagerContract(t)
	l2GERManager.EXPECT().GlobalExitRootMap(mock.Anything, mock.Anything).Return(big.NewInt(1), nil).Once()

	sender := &EVMChainGERSender{
		logger:       log.GetDefaultLogger(),
		l2GERManager: l2GERManager,
		ethTxMan:     ethTxMan,
	}
	require.NoError(t, sender.ProcessGER(context.Background(), common.HexToHash("0x456")))
}

func TestEVMChainGERSender_SubmitTransaction(t *testing.T) {
	testFuncABI := `[{
		"inputs": [
			{
				"internalType": "bytes32",
				"name": "_data",
				"type": "bytes32"
			}
		],
		"name": "testFunction",
		"outputs": [],
		"stateMutability": "nonpayable",
		"type": "function"
	}]`

	targetAddr := common.HexToAddress("0x123")
	testAbi, err := abi.JSON(strings.NewReader(testFuncABI))
	require.NoError(t, err)

	ger := common.HexToHash("0x456")
	txID := common.HexToHash("0x789")

	tests := []struct {
		name            string
		funcName        string
		action          string
		addReturnTxID   common.Hash
		addReturnErr    error
		resultReturn    *types.MonitoredTxResult
		resultReturnErr error
		expectedErr     string
	}{
		{
			name:            "successful transaction",
			funcName:        "testFunction",
			action:          "test",
			addReturnTxID:   txID,
			addReturnErr:    nil,
			resultReturn:    &types.MonitoredTxResult{Status: types.MonitoredTxStatusMined, MinedAtBlockNumber: big.NewInt(123)},
			resultReturnErr: nil,
			expectedErr:     "",
		},
		{
			name:            "transaction failed",
			funcName:        "testFunction",
			action:          "test",
			addReturnTxID:   txID,
			addReturnErr:    nil,
			resultReturn:    &types.MonitoredTxResult{Status: types.MonitoredTxStatusFailed},
			resultReturnErr: nil,
			expectedErr:     "test GER tx",
		},
		{
			name:            "transaction safe",
			funcName:        "testFunction",
			action:          "test",
			addReturnTxID:   txID,
			addReturnErr:    nil,
			resultReturn:    &types.MonitoredTxResult{Status: types.MonitoredTxStatusSafe, MinedAtBlockNumber: big.NewInt(123)},
			resultReturnErr: nil,
			expectedErr:     "",
		},
		{
			name:            "transaction finalized",
			funcName:        "testFunction",
			action:          "test",
			addReturnTxID:   txID,
			addReturnErr:    nil,
			resultReturn:    &types.MonitoredTxResult{Status: types.MonitoredTxStatusFinalized, MinedAtBlockNumber: big.NewInt(123)},
			resultReturnErr: nil,
			expectedErr:     "",
		},
		{
			name:            "transaction created - continue monitoring",
			funcName:        "testFunction",
			action:          "test",
			addReturnTxID:   txID,
			addReturnErr:    nil,
			resultReturn:    &types.MonitoredTxResult{Status: types.MonitoredTxStatusCreated},
			resultReturnErr: nil,
			expectedErr:     "",
		},
		{
			name:            "transaction sent - continue monitoring",
			funcName:        "testFunction",
			action:          "test",
			addReturnTxID:   txID,
			addReturnErr:    nil,
			resultReturn:    &types.MonitoredTxResult{Status: types.MonitoredTxStatusSent},
			resultReturnErr: nil,
			expectedErr:     "",
		},
		{
			name:            "result error",
			funcName:        "testFunction",
			action:          "test",
			addReturnTxID:   txID,
			addReturnErr:    nil,
			resultReturn:    &types.MonitoredTxResult{},
			resultReturnErr: errors.New("result error"),
			expectedErr:     "result error",
		},
		{
			name:            "ErrAlreadyExists is not an error - monitoring continues until mined",
			funcName:        "testFunction",
			action:          "test",
			addReturnTxID:   txID,
			addReturnErr:    ethtxmanager.ErrAlreadyExists,
			resultReturn:    &types.MonitoredTxResult{Status: types.MonitoredTxStatusMined, MinedAtBlockNumber: big.NewInt(123)},
			resultReturnErr: nil,
			expectedErr:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancelFn := context.WithTimeout(context.Background(), time.Millisecond*500)
			defer cancelFn()

			ethTxMan := mocks.NewEthTxManager(t)
			ethTxMan.EXPECT().
				Add(ctx, &targetAddr, common.Big0, mock.Anything, mock.Anything, mock.Anything).
				Return(tt.addReturnTxID, tt.addReturnErr)

			if tt.resultReturn != nil || tt.resultReturnErr != nil {
				ethTxMan.EXPECT().
					Result(ctx, tt.addReturnTxID).
					Return(*tt.resultReturn, tt.resultReturnErr)
			}

			sender := &EVMChainGERSender{
				logger:              log.GetDefaultLogger(),
				ethTxMan:            ethTxMan,
				waitPeriodMonitorTx: time.Millisecond * 10,
			}

			err := sender.submitTransaction(ctx, &targetAddr, &testAbi, tt.funcName, ger, tt.action)
			if tt.expectedErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectedErr)
			}
		})
	}
}

func TestEVMChainGERSender_SubmitTransaction_ContextCancellation(t *testing.T) {
	testFuncABI := `[{
		"inputs": [
			{
				"internalType": "bytes32",
				"name": "_data",
				"type": "bytes32"
			}
		],
		"name": "testFunction",
		"outputs": [],
		"stateMutability": "nonpayable",
		"type": "function"
	}]`

	targetAddr := common.HexToAddress("0x123")
	testAbi, err := abi.JSON(strings.NewReader(testFuncABI))
	require.NoError(t, err)

	ger := common.HexToHash("0x456")
	txID := common.HexToHash("0x789")

	ethTxMan := mocks.NewEthTxManager(t)
	ethTxMan.EXPECT().
		Add(mock.Anything, &targetAddr, common.Big0, mock.Anything, mock.Anything, mock.Anything).
		Return(txID, nil)

	sender := &EVMChainGERSender{
		logger:              log.GetDefaultLogger(),
		ethTxMan:            ethTxMan,
		waitPeriodMonitorTx: time.Millisecond * 100, // Longer wait to ensure context cancellation
	}

	// Create a context that will be cancelled immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err = sender.submitTransaction(ctx, &targetAddr, &testAbi, "testFunction", ger, "test")
	require.NoError(t, err) // Should return nil when context is cancelled
}

func TestEVMChainGERSender_IsGERInjected(t *testing.T) {
	tests := []struct {
		name           string
		mockReturn     *big.Int
		mockError      error
		expectedResult bool
		expectedErrMsg string
	}{
		{
			name:           "GER is injected",
			mockReturn:     big.NewInt(1),
			mockError:      nil,
			expectedResult: true,
			expectedErrMsg: "",
		},
		{
			name:           "GER is not injected",
			mockReturn:     big.NewInt(0),
			mockError:      nil,
			expectedResult: false,
			expectedErrMsg: "",
		},
		{
			name:           "Error checking GER injection",
			mockReturn:     nil,
			mockError:      errors.New("some error"),
			expectedResult: false,
			expectedErrMsg: "failed to check if global exit root is injected",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockL2GERManager := mocks.NewL2GERManagerContract(t)
			mockL2GERManager.EXPECT().
				GlobalExitRootMap(mock.Anything, mock.Anything).
				Return(tt.mockReturn, tt.mockError)

			evmChainGERSender := &EVMChainGERSender{
				l2GERManager: mockL2GERManager,
			}

			ger := common.HexToHash("0x12345")
			result, err := evmChainGERSender.IsGERInjected(ger)
			if tt.expectedErrMsg != "" {
				require.ErrorContains(t, err, tt.expectedErrMsg)
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, tt.expectedResult, result)

			mockL2GERManager.AssertExpectations(t)
		})
	}
}

func TestValidateGERSender(t *testing.T) {
	zeroAddr := common.Address{}
	updaterAddr := common.HexToAddress("0x1111")
	otherAddr := common.HexToAddress("0x9999")

	tests := []struct {
		name         string
		gerSender    common.Address
		setupMock    func(*mocks.L2GERManagerContract)
		expectErrMsg string
	}{
		{
			name:      "valid sender - matches updater",
			gerSender: updaterAddr,
			setupMock: func(m *mocks.L2GERManagerContract) {
				m.EXPECT().GlobalExitRootUpdater(mock.Anything).Return(updaterAddr, nil)
			},
			expectErrMsg: "",
		},
		{
			name:      "valid sender - zero updater address (anyone can update)",
			gerSender: otherAddr,
			setupMock: func(m *mocks.L2GERManagerContract) {
				m.EXPECT().GlobalExitRootUpdater(mock.Anything).Return(zeroAddr, nil)
			},
			expectErrMsg: "",
		},
		{
			name:      "invalid updater sender",
			gerSender: otherAddr,
			setupMock: func(m *mocks.L2GERManagerContract) {
				m.EXPECT().GlobalExitRootUpdater(mock.Anything).Return(updaterAddr, nil)
			},
			expectErrMsg: "invalid GER sender provided (in the EthTxManager configuration), and it is not allowed to update GERs",
		},
		{
			name:      "contract returns error on updater",
			gerSender: updaterAddr,
			setupMock: func(m *mocks.L2GERManagerContract) {
				m.EXPECT().GlobalExitRootUpdater(mock.Anything).Return(zeroAddr, fmt.Errorf("updater error"))
			},
			expectErrMsg: "updater error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockGERManager := mocks.NewL2GERManagerContract(t)
			tt.setupMock(mockGERManager)
			err := validateGERSender(tt.gerSender, mockGERManager, otherAddr)
			if tt.expectErrMsg == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.ErrorContains(t, err, tt.expectErrMsg)
			}
		})
	}
}

func TestEVMChainGERSender_ValidateGERSender(t *testing.T) {
	tests := []struct {
		name             string
		gerUpdater       common.Address
		ethTxManagerFrom common.Address
		mockError        error
		expectedErrMsg   string
	}{
		{
			name:             "valid GER sender",
			gerUpdater:       common.HexToAddress("0x123"),
			ethTxManagerFrom: common.HexToAddress("0x123"),
			mockError:        nil,
			expectedErrMsg:   "",
		},
		{
			name:             "zero address updater",
			gerUpdater:       common.Address{},
			ethTxManagerFrom: common.HexToAddress("0x123"),
			mockError:        nil,
			expectedErrMsg:   "",
		},
		{
			name:             "invalid GER sender",
			gerUpdater:       common.HexToAddress("0x456"),
			ethTxManagerFrom: common.HexToAddress("0x123"),
			mockError:        nil,
			expectedErrMsg:   "invalid GER sender provided",
		},
		{
			name:             "contract error",
			gerUpdater:       common.Address{},
			ethTxManagerFrom: common.HexToAddress("0x123"),
			mockError:        errors.New("contract error"),
			expectedErrMsg:   "failed to retrieve GER updater address",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockL2GERManager := mocks.NewL2GERManagerContract(t)

			// Setup mock expectations
			mockL2GERManager.EXPECT().GlobalExitRootUpdater(mock.Anything).Return(tt.gerUpdater, tt.mockError)

			// Test the validation logic that we can properly mock
			err := validateGERSender(tt.ethTxManagerFrom, mockL2GERManager, common.Address{})
			if tt.expectedErrMsg != "" {
				require.ErrorContains(t, err, tt.expectedErrMsg)
			} else {
				require.NoError(t, err)
			}

			mockL2GERManager.AssertExpectations(t)
		})
	}
}
