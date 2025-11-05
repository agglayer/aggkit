package chaingersender

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/0xPolygon/zkevm-ethtx-manager/types"
	"github.com/agglayer/aggkit/aggoracle/mocks"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNEWEVMChainGERSender_DirectMode(t *testing.T) {
	txManagerMock := mocks.NewEthTxManager(t)
	txManagerMock.EXPECT().From().Return(common.HexToAddress("0x123")).Once()

	l2GERManagerMock := mocks.NewL2GERManagerContract(t)
	l2GERManagerMock.EXPECT().GlobalExitRootUpdater(mock.Anything).Return(common.HexToAddress("0x123"), nil).Once()

	gerSender, err := NewEVMChainGERSender(log.GetDefaultLogger(), EVMConfig{}, nil, l2GERManagerMock, txManagerMock, false)
	require.NoError(t, err)
	require.NotNil(t, gerSender)
	require.Equal(t, DirectInjectionMode, gerSender.mode)
}

func TestEVMChainGERSender_InitializeAndValidateMode(t *testing.T) {
	tests := []struct {
		name        string
		mode        GERMode
		setupSender func() *EVMChainGERSender
		expectedErr string
	}{
		{
			name: "direct injection mode",
			mode: DirectInjectionMode,
			setupSender: func() *EVMChainGERSender {
				mockL2GERManager := mocks.NewL2GERManagerContract(t)
				ethTxMan := mocks.NewEthTxManager(t)

				ethTxMan.EXPECT().From().Return(common.HexToAddress("0x789"))
				mockL2GERManager.EXPECT().GlobalExitRootUpdater(mock.Anything).Return(common.HexToAddress("0x789"), nil)

				return &EVMChainGERSender{
					logger:       log.GetDefaultLogger(),
					l2GERManager: mockL2GERManager,
					ethTxMan:     ethTxMan,
				}
			},
			expectedErr: "",
		},
		{
			name: "unknown mode",
			mode: GERMode("unknown"),
			setupSender: func() *EVMChainGERSender {
				return &EVMChainGERSender{
					logger: log.GetDefaultLogger(),
				}
			},
			expectedErr: "unknown GER mode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender := tt.setupSender()
			sender.mode = tt.mode
			err := sender.initializeAndValidateMode()

			if tt.expectedErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectedErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// testCase represents a test case for transaction submission
type testCase struct {
	name            string
	mode            GERMode
	addReturnTxID   common.Hash
	addReturnErr    error
	resultReturn    *types.MonitoredTxResult
	resultReturnErr error
	expectedErr     string
}

// testConfig holds configuration for running transaction tests
type testConfig struct {
	funcABI      string
	targetAddr   common.Address
	funcName     string
	action       string
	expectedMode GERMode
}

// runTransactionTest is a helper function to run transaction submission tests
func runTransactionTest(t *testing.T, config testConfig, tests []testCase) {
	t.Helper()
	abi, err := abi.JSON(strings.NewReader(config.funcABI))
	require.NoError(t, err)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancelFn := context.WithTimeout(context.Background(), time.Millisecond*500)
			defer cancelFn()

			ethTxMan := mocks.NewEthTxManager(t)

			// Add From() expectation for ProposeGER tests since it calls IsGERProposed internally
			if config.funcName == proposeGERFuncName && tt.mode == config.expectedMode {
				ethTxMan.EXPECT().From().Return(common.HexToAddress("0x123"))
			}

			if tt.mode == config.expectedMode && (tt.addReturnTxID != (common.Hash{}) || tt.addReturnErr != nil) {
				ethTxMan.EXPECT().
					Add(ctx, &config.targetAddr, common.Big0, mock.Anything, mock.Anything, mock.Anything).
					Return(tt.addReturnTxID, tt.addReturnErr)
				if tt.resultReturn != nil || tt.resultReturnErr != nil {
					ethTxMan.EXPECT().
						Result(ctx, tt.addReturnTxID).
						Return(*tt.resultReturn, tt.resultReturnErr)
				}
			}

			sender := &EVMChainGERSender{
				logger:              log.GetDefaultLogger(),
				mode:                tt.mode,
				ethTxMan:            ethTxMan,
				waitPeriodMonitorTx: time.Millisecond * 10,
			}

			// Set the appropriate fields based on the mode
			if config.expectedMode == AggOracleCommitteeMode {
				sender.aggOracleCommitteeAddr = config.targetAddr
				sender.aggOracleCommitteeAbi = &abi

				// Add mock for aggOracleCommittee when testing ProposeGER
				if config.funcName == proposeGERFuncName && tt.mode == config.expectedMode {
					mockAggOracleCommittee := mocks.NewAggOracleCommitteeContract(t)
					expectedAddress := common.HexToAddress("0x123")
					// Mock IsGERProposed to return false (not already proposed)
					mockAggOracleCommittee.EXPECT().
						AddressToLastProposedGER(mock.Anything, expectedAddress).
						Return([common.HashLength]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, nil)
					sender.aggOracleCommittee = mockAggOracleCommittee
				}
			} else {
				sender.l2GERManagerAddr = config.targetAddr
				sender.l2GERManagerAbi = &abi
			}

			// Set up l2GERManager mock for tests that need IsGERInjected
			if (config.funcName == proposeGERFuncName && tt.mode == config.expectedMode) ||
				(config.funcName == "insertGlobalExitRoot" && tt.mode == config.expectedMode) {
				mockL2GERManager := mocks.NewL2GERManagerContract(t)
				// Mock IsGERInjected to return false (not already injected)
				mockL2GERManager.EXPECT().
					GlobalExitRootMap(mock.Anything, mock.Anything).
					Return(big.NewInt(0), nil)
				sender.l2GERManager = mockL2GERManager
			}

			var err error
			ger := common.HexToHash("0x456")
			if config.funcName == proposeGERFuncName {
				err = sender.ProposeGER(ctx, ger)
			} else {
				err = sender.InjectGER(ctx, ger)
			}

			if tt.expectedErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectedErr)
			}
		})
	}
}

// createTestCases creates test cases for transaction submission
func createTestCases(mode GERMode, txID common.Hash, expectedErrPrefix string) []testCase {
	return []testCase{
		{
			name:            "successful transaction",
			mode:            mode,
			addReturnTxID:   txID,
			addReturnErr:    nil,
			resultReturn:    &types.MonitoredTxResult{Status: types.MonitoredTxStatusMined, MinedAtBlockNumber: big.NewInt(123)},
			resultReturnErr: nil,
			expectedErr:     "",
		},
		{
			name:            "transaction fails due to Add method error",
			mode:            mode,
			addReturnTxID:   common.Hash{},
			addReturnErr:    errors.New("add error"),
			resultReturn:    nil,
			resultReturnErr: nil,
			expectedErr:     "add error",
		},
		{
			name:            "transaction fails due to transaction failure",
			mode:            mode,
			addReturnTxID:   txID,
			addReturnErr:    nil,
			resultReturn:    &types.MonitoredTxResult{Status: types.MonitoredTxStatusFailed},
			resultReturnErr: nil,
			expectedErr:     expectedErrPrefix + " tx",
		},
		{
			name:            "transaction fails due to Result method error",
			mode:            mode,
			addReturnTxID:   txID,
			addReturnErr:    nil,
			resultReturn:    &types.MonitoredTxResult{},
			resultReturnErr: errors.New("result error"),
			expectedErr:     "result error",
		},
	}
}

func TestEVMChainGERSender_ProposeGER(t *testing.T) {
	proposeGERFuncABI := `[{
		"inputs": [
			{
				"internalType": "bytes32",
				"name": "_proposedGlobalExitRoot",
				"type": "bytes32"
			}
		],
		"name": "proposeGlobalExitRoot",
		"outputs": [],
		"stateMutability": "nonpayable",
		"type": "function"
	}]`
	aggOracleCommitteeAddr := common.HexToAddress("0x456")
	txID := common.HexToHash("0x789")

	config := testConfig{
		funcABI:      proposeGERFuncABI,
		targetAddr:   aggOracleCommitteeAddr,
		funcName:     proposeGERFuncName,
		action:       "propose",
		expectedMode: AggOracleCommitteeMode,
	}

	tests := createTestCases(AggOracleCommitteeMode, txID, "propose GER")

	runTransactionTest(t, config, tests)
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
	l2GERManagerAddr := common.HexToAddress("0x123")
	txID := common.HexToHash("0x789")

	config := testConfig{
		funcABI:      insertGERFuncABI,
		targetAddr:   l2GERManagerAddr,
		funcName:     "insertGlobalExitRoot",
		action:       "inject",
		expectedMode: DirectInjectionMode,
	}

	tests := createTestCases(DirectInjectionMode, txID, "inject GER")

	runTransactionTest(t, config, tests)
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

func TestValidateGERProposer(t *testing.T) {
	validProposer := common.HexToAddress("0x789")
	invalidProposer := common.HexToAddress("0x999")

	tests := []struct {
		name        string
		gerProposer common.Address
		setupMock   func(*mocks.AggOracleCommitteeContract)
		expectedErr string
	}{
		{
			name:        "valid proposer",
			gerProposer: validProposer,
			setupMock: func(m *mocks.AggOracleCommitteeContract) {
				m.EXPECT().GetAggOracleMemberIndex(mock.Anything, validProposer).Return(big.NewInt(1), nil)
			},
			expectedErr: "",
		},
		{
			name:        "invalid proposer - not oracle member",
			gerProposer: invalidProposer,
			setupMock: func(m *mocks.AggOracleCommitteeContract) {
				m.EXPECT().GetAggOracleMemberIndex(mock.Anything, invalidProposer).Return(nil, errors.New("OracleMemberNotFound"))
			},
			expectedErr: "invalid GER proposer provided",
		},
		{
			name:        "contract error",
			gerProposer: validProposer,
			setupMock: func(m *mocks.AggOracleCommitteeContract) {
				m.EXPECT().GetAggOracleMemberIndex(mock.Anything, validProposer).Return(nil, errors.New("contract error"))
			},
			expectedErr: "invalid GER proposer provided",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockAggOracleCommittee := mocks.NewAggOracleCommitteeContract(t)
			tt.setupMock(mockAggOracleCommittee)

			err := validateGERProposer(tt.gerProposer, mockAggOracleCommittee)

			if tt.expectedErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectedErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
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

func TestEVMChainGERSender_IsGERProposed(t *testing.T) {
	tests := []struct {
		name           string
		mode           GERMode
		ger            common.Hash
		mockReturn     [common.HashLength]byte
		mockError      error
		expectedResult bool
		expectedErrMsg string
	}{
		{
			name:           "GER is proposed - committee mode",
			mode:           AggOracleCommitteeMode,
			ger:            common.HexToHash("0x1234567890abcdef"),
			mockReturn:     common.HexToHash("0x1234567890abcdef"),
			mockError:      nil,
			expectedResult: true,
			expectedErrMsg: "",
		},
		{
			name:           "GER is not proposed - committee mode",
			mode:           AggOracleCommitteeMode,
			ger:            common.HexToHash("0x1234567890abcdef"),
			mockReturn:     [common.HashLength]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
			mockError:      nil,
			expectedResult: false,
			expectedErrMsg: "",
		},
		{
			name:           "Contract error - committee mode",
			mode:           AggOracleCommitteeMode,
			ger:            common.HexToHash("0x1234567890abcdef"),
			mockReturn:     [common.HashLength]byte{},
			mockError:      errors.New("contract error"),
			expectedResult: false,
			expectedErrMsg: "failed to check last proposed GER for oracle committee member",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockAggOracleCommittee := mocks.NewAggOracleCommitteeContract(t)
			mockEthTxManager := mocks.NewEthTxManager(t)

			// Setup mock expectations only for committee mode
			if tt.mode == AggOracleCommitteeMode {
				expectedAddress := common.HexToAddress("0x123")
				mockEthTxManager.EXPECT().From().Return(expectedAddress)
				mockAggOracleCommittee.EXPECT().
					AddressToLastProposedGER(mock.Anything, expectedAddress).
					Return(tt.mockReturn, tt.mockError)
			}

			evmChainGERSender := &EVMChainGERSender{
				mode:               tt.mode,
				aggOracleCommittee: mockAggOracleCommittee,
				ethTxMan:           mockEthTxManager,
			}

			result, err := evmChainGERSender.IsGERProposed(tt.ger)
			if tt.expectedErrMsg != "" {
				require.ErrorContains(t, err, tt.expectedErrMsg)
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, tt.expectedResult, result)

			mockAggOracleCommittee.AssertExpectations(t)
			mockEthTxManager.AssertExpectations(t)
		})
	}
}

func TestEVMChainGERSender_initializeAggOracleCommitteeMode(t *testing.T) {
	tests := []struct {
		name                   string
		aggOracleCommitteeAddr common.Address
		mockValidationError    error
		expectedErrMsg         string
	}{
		{
			name:                   "successful validation",
			aggOracleCommitteeAddr: common.HexToAddress("0x456"),
			mockValidationError:    nil,
			expectedErrMsg:         "",
		},
		{
			name:                   "validation error",
			aggOracleCommitteeAddr: common.HexToAddress("0x456"),
			mockValidationError:    errors.New("validation failed"),
			expectedErrMsg:         "validation failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockAggOracleCommittee := mocks.NewAggOracleCommitteeContract(t)

			// Setup mock expectations for validation
			expectedAddress := common.HexToAddress("0x123")

			if tt.mockValidationError == nil {
				mockAggOracleCommittee.EXPECT().
					GetAggOracleMemberIndex(mock.Anything, expectedAddress).
					Return(big.NewInt(1), nil)
			} else {
				mockAggOracleCommittee.EXPECT().
					GetAggOracleMemberIndex(mock.Anything, expectedAddress).
					Return(nil, tt.mockValidationError)
			}

			// Test the validation logic that we can properly mock
			err := validateGERProposer(expectedAddress, mockAggOracleCommittee)
			if tt.mockValidationError != nil {
				require.ErrorContains(t, err, tt.expectedErrMsg)
			} else {
				require.NoError(t, err)
			}

			mockAggOracleCommittee.AssertExpectations(t)
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
