package backward_forward_let

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	agglayermocks "github.com/agglayer/aggkit/agglayer/mocks"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	gethTypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// stubL2Bridge is a minimal stub implementing l2BridgeContract for testing.
type stubL2Bridge struct {
	depositCount    *big.Int
	depositCountErr error
	root            [32]byte
	rootErr         error
	emergency       bool
	emergencyErr    error
	activateErr     error
	deactivateErr   error
	backwardLETErr  error
	forwardLETErr   error
	// tx is returned for all transact methods.
	tx *gethTypes.Transaction
}

func (s *stubL2Bridge) DepositCount(_ *bind.CallOpts) (*big.Int, error) {
	if s.depositCountErr != nil {
		return nil, s.depositCountErr
	}
	if s.depositCount == nil {
		return big.NewInt(0), nil
	}
	return s.depositCount, nil
}

func (s *stubL2Bridge) GetRoot(_ *bind.CallOpts) ([32]byte, error) {
	return s.root, s.rootErr
}

func (s *stubL2Bridge) IsEmergencyState(_ *bind.CallOpts) (bool, error) {
	return s.emergency, s.emergencyErr
}

func (s *stubL2Bridge) ActivateEmergencyState(_ *bind.TransactOpts) (*gethTypes.Transaction, error) {
	return s.tx, s.activateErr
}

func (s *stubL2Bridge) DeactivateEmergencyState(_ *bind.TransactOpts) (*gethTypes.Transaction, error) {
	return s.tx, s.deactivateErr
}

func (s *stubL2Bridge) BackwardLET(_ *bind.TransactOpts, _ *big.Int, _ [32][32]byte, _ [32]byte, _ [32][32]byte) (*gethTypes.Transaction, error) {
	return s.tx, s.backwardLETErr
}

func (s *stubL2Bridge) ForwardLET(_ *bind.TransactOpts, _ []agglayerbridgel2.AgglayerBridgeL2LeafData, _ [32]byte) (*gethTypes.Transaction, error) {
	return s.tx, s.forwardLETErr
}

// successReceipt returns a receipt with Status=1 (success).
func successReceipt() *gethTypes.Receipt { return &gethTypes.Receipt{Status: 1} }

// failedReceipt returns a receipt with Status=0 (failed tx).
func failedReceipt() *gethTypes.Receipt { return &gethTypes.Receipt{Status: 0} }

// noopAuth returns a TransactOpts with a no-op signer function.
func noopAuth() *bind.TransactOpts {
	return &bind.TransactOpts{
		From: common.HexToAddress("0xdeadbeef"),
		Signer: func(_ common.Address, tx *gethTypes.Transaction) (*gethTypes.Transaction, error) {
			return tx, nil
		},
	}
}

// buildTestEnv builds a minimal Env with stub L2Bridge, injectable auth builder,
// injectable receipt waiter, and an injectable chainID getter.
func buildTestEnv(t *testing.T,
	bridge l2BridgeContract,
	chainID *big.Int,
	chainIDErr error,
	authErr error,
	receipt *gethTypes.Receipt,
	receiptErr error,
) *Env {
	t.Helper()

	env := &Env{
		L2Bridge: bridge,
		Config:   &Config{},
		chainIDFn: func(_ context.Context) (*big.Int, error) {
			return chainID, chainIDErr
		},
		buildAuthFn: func(_ context.Context, _ signertypes.SignerConfig, _ *big.Int, _ string) (*bind.TransactOpts, error) {
			if authErr != nil {
				return nil, authErr
			}
			return noopAuth(), nil
		},
		waitReceiptFn: func(_ context.Context, _ *gethTypes.Transaction) (*gethTypes.Receipt, error) {
			return receipt, receiptErr
		},
	}
	return env
}

// newAgglayerMockWithSettledHeight creates an agglayer mock that returns a valid NetworkInfo.
func newAgglayerMockWithSettledHeight(
	t *testing.T,
	networkID uint32,
	settledDC uint32,
	settledLER [32]byte,
) *agglayermocks.AgglayerClientMock {
	t.Helper()
	settledHeight := uint64(0)
	settledLeafCount := uint64(settledDC)
	ler := common.Hash(settledLER)
	certID := common.Hash{}
	m := agglayermocks.NewAgglayerClientMock(t)
	m.EXPECT().GetNetworkInfo(mock.Anything, networkID).Return(agglayertypes.NetworkInfo{
		SettledHeight:        &settledHeight,
		SettledLETLeafCount:  &settledLeafCount,
		SettledLER:           &ler,
		SettledCertificateID: &certID,
	}, nil)
	return m
}

// --- Diagnose Step 2 error tests ---

// TestDiagnose_DepositCountError verifies Diagnose returns an error when DepositCount fails.
func TestDiagnose_DepositCountError(t *testing.T) {
	t.Parallel()

	agglayerMock := newAgglayerMockWithSettledHeight(t, 3, 1, [32]byte{0x01})
	bridge := &stubL2Bridge{depositCountErr: errors.New("rpc error")}

	env := &Env{
		AgglayerClient: agglayerMock,
		L2Bridge:       bridge,
		L2NetworkID:    3,
	}

	_, err := Diagnose(context.Background(), env)
	require.Error(t, err)
	require.Contains(t, err.Error(), "get L2 deposit count")
}

// TestDiagnose_GetRootError verifies Diagnose returns an error when GetRoot fails.
func TestDiagnose_GetRootError(t *testing.T) {
	t.Parallel()

	agglayerMock := newAgglayerMockWithSettledHeight(t, 4, 2, [32]byte{0x02})
	bridge := &stubL2Bridge{rootErr: errors.New("root unavailable")}

	env := &Env{
		AgglayerClient: agglayerMock,
		L2Bridge:       bridge,
		L2NetworkID:    4,
	}

	_, err := Diagnose(context.Background(), env)
	require.Error(t, err)
	require.Contains(t, err.Error(), "get L2 bridge root")
}

// TestDiagnose_IsEmergencyStateError verifies Diagnose returns an error when IsEmergencyState fails.
func TestDiagnose_IsEmergencyStateError(t *testing.T) {
	t.Parallel()

	agglayerMock := newAgglayerMockWithSettledHeight(t, 5, 3, [32]byte{0x03})
	bridge := &stubL2Bridge{emergencyErr: errors.New("bridge paused query failed")}

	env := &Env{
		AgglayerClient: agglayerMock,
		L2Bridge:       bridge,
		L2NetworkID:    5,
	}

	_, err := Diagnose(context.Background(), env)
	require.Error(t, err)
	require.Contains(t, err.Error(), "check L2 emergency state")
}

// TestDiagnose_NoDivergence_LERAndDCMatch verifies that when L2 LER and DC match L1,
// Diagnose returns NoDivergence.
func TestDiagnose_NoDivergence_LERAndDCMatch(t *testing.T) {
	t.Parallel()

	settledLER := [32]byte{0xAB}
	settledDC := uint32(7)

	agglayerMock := newAgglayerMockWithSettledHeight(t, 6, settledDC, settledLER)

	// L2 bridge returns matching values.
	bridge := &stubL2Bridge{
		depositCount: big.NewInt(int64(settledDC)),
		root:         settledLER,
		emergency:    false,
	}

	env := &Env{
		AgglayerClient: agglayerMock,
		L2Bridge:       bridge,
		L2NetworkID:    6,
	}

	result, err := Diagnose(context.Background(), env)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, NoDivergence, result.Case)
}

// TestDiagnose_Case1_SingleDivergentLeaf verifies that when there's one divergent leaf
// and no extra L2 bridges, Diagnose returns Case1 and populates DivergentLeaves.
func TestDiagnose_Case1_SingleDivergentLeaf(t *testing.T) {
	t.Parallel()

	// L1 settled: height=0, DC=1 (one leaf settled).
	settledLER := [32]byte{0xBB}
	agglayerMock := newAgglayerMockWithSettledHeight(t, 7, 1, settledLER)

	// L2 has DC=0 (no leaves); L1 settled has 1 divergent leaf.
	// hasExtraL2 = l2CurrentDC(0) > divergencePoint(0) = false → Case1.
	bridge := &stubL2Bridge{
		depositCount: big.NewInt(0),
		root:         [32]byte{0xCC},
		emergency:    false,
	}

	// AggsenderRPC: height 0 returns one mismatched exit.
	mismatchedExit := &agglayertypes.BridgeExit{
		DestinationNetwork: 99,
		Amount:             big.NewInt(9999),
	}

	// BridgeService at DC=0: returns a different bridge (so they won't match).
	diffBR := &bridgeservicetypes.BridgeResponse{
		LeafType:           0,
		OriginNetwork:      1,
		OriginAddress:      bridgeservicetypes.Address("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"),
		DestinationNetwork: 2,
		DestinationAddress: bridgeservicetypes.Address("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"),
		Amount:             bridgeservicetypes.BigIntString("1000"),
	}

	env := &Env{
		AgglayerClient: agglayerMock,
		L2Bridge:       bridge,
		L2NetworkID:    7,
		AggsenderRPC: &stubAggsenderRPC{
			exitsByHeight: map[uint64][]*agglayertypes.BridgeExit{
				0: {mismatchedExit},
			},
		},
		BridgeService: &stubBridgeService{
			bridges: map[uint32]*bridgeservicetypes.BridgeResponse{0: diffBR},
		},
	}

	result, err := Diagnose(context.Background(), env)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, Case1, result.Case)
	require.Len(t, result.DivergentLeaves, 1)
	require.Equal(t, mismatchedExit, result.DivergentLeaves[0])
	require.Equal(t, uint32(0), result.DivergencePoint)
	require.False(t, result.IsEmergencyState)
}

// TestDiagnose_Case2_WithExtraL2Bridges verifies Case2 when L2 has extra bridges.
func TestDiagnose_Case2_WithExtraL2Bridges(t *testing.T) {
	t.Parallel()

	settledLER := [32]byte{0xDD}
	agglayerMock := newAgglayerMockWithSettledHeight(t, 8, 1, settledLER)

	bridge := &stubL2Bridge{
		depositCount: big.NewInt(2), // one extra beyond the divergent L1 leaf
		root:         [32]byte{0xEE},
		emergency:    false,
	}

	mismatchedExit := &agglayertypes.BridgeExit{
		DestinationNetwork: 77,
		Amount:             big.NewInt(7777),
	}

	diffBR := &bridgeservicetypes.BridgeResponse{
		OriginNetwork:      0,
		DestinationNetwork: 1,
		Amount:             bridgeservicetypes.BigIntString("1"),
	}
	extraBR := &bridgeservicetypes.BridgeResponse{
		OriginNetwork:      0,
		DestinationNetwork: 2,
		Amount:             bridgeservicetypes.BigIntString("999"),
	}

	env := &Env{
		AgglayerClient: agglayerMock,
		L2Bridge:       bridge,
		L2NetworkID:    8,
		AggsenderRPC: &stubAggsenderRPC{
			exitsByHeight: map[uint64][]*agglayertypes.BridgeExit{
				0: {mismatchedExit},
			},
		},
		BridgeService: &stubBridgeService{
			bridges: map[uint32]*bridgeservicetypes.BridgeResponse{
				0: diffBR,
				1: extraBR,
			},
		},
	}

	result, err := Diagnose(context.Background(), env)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, Case2, result.Case)
	require.Len(t, result.DivergentLeaves, 1)
	// collectExtraL2Bridges(startDC=0, endDC=2) fetches DC 0 and DC 1 → 2 extra bridges.
	require.Len(t, result.ExtraL2Bridges, 2)
	require.NotNil(t, result.Undercollateralization)
}

// TestDiagnose_EmergencyStateTrue verifies that IsEmergencyState=true is captured.
func TestDiagnose_EmergencyStateTrue(t *testing.T) {
	t.Parallel()

	settledLER := [32]byte{0xFE}
	agglayerMock := newAgglayerMockWithSettledHeight(t, 9, 1, settledLER)

	mismatchedExit := &agglayertypes.BridgeExit{DestinationNetwork: 5}
	diffBR := &bridgeservicetypes.BridgeResponse{DestinationNetwork: 6}

	bridge := &stubL2Bridge{
		depositCount: big.NewInt(1),
		root:         [32]byte{0xFF},
		emergency:    true,
	}

	env := &Env{
		AgglayerClient: agglayerMock,
		L2Bridge:       bridge,
		L2NetworkID:    9,
		AggsenderRPC: &stubAggsenderRPC{
			exitsByHeight: map[uint64][]*agglayertypes.BridgeExit{
				0: {mismatchedExit},
			},
		},
		BridgeService: &stubBridgeService{
			bridges: map[uint32]*bridgeservicetypes.BridgeResponse{0: diffBR},
		},
	}

	result, err := Diagnose(context.Background(), env)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsEmergencyState)
}

// TestDiagnose_CollectExtraL2Bridges_Error verifies that an error from collectExtraL2Bridges
// causes Diagnose to return an error.
func TestDiagnose_CollectExtraL2Bridges_Error(t *testing.T) {
	t.Parallel()

	settledLER := [32]byte{0xAA}
	agglayerMock := newAgglayerMockWithSettledHeight(t, 10, 1, settledLER)

	bridge := &stubL2Bridge{
		depositCount: big.NewInt(2),
		root:         [32]byte{0xBB},
	}

	mismatchedExit := &agglayertypes.BridgeExit{DestinationNetwork: 3}
	diffBR := &bridgeservicetypes.BridgeResponse{DestinationNetwork: 4}

	env := &Env{
		AgglayerClient: agglayerMock,
		L2Bridge:       bridge,
		L2NetworkID:    10,
		AggsenderRPC: &stubAggsenderRPC{
			exitsByHeight: map[uint64][]*agglayertypes.BridgeExit{
				0: {mismatchedExit},
			},
		},
		BridgeService: &stubBridgeService{
			bridges: map[uint32]*bridgeservicetypes.BridgeResponse{
				0: diffBR,
			},
			errAtDC: map[uint32]error{1: errors.New("DB failure")},
		},
	}

	_, err := Diagnose(context.Background(), env)
	require.Error(t, err)
	require.Contains(t, err.Error(), "collect extra L2 bridges")
}

// --- ExecuteRecovery unit tests ---

// TestExecuteRecovery_ChainIDError verifies that an error from chainIDFn is propagated.
func TestExecuteRecovery_ChainIDError(t *testing.T) {
	t.Parallel()

	env := buildTestEnv(t, &stubL2Bridge{}, nil, errors.New("chain ID unavailable"), nil, nil, nil)
	diagnosis := &DiagnosisResult{Case: Case1, DivergentLeaves: []*agglayertypes.BridgeExit{{}}}

	err := ExecuteRecovery(context.Background(), env, diagnosis)
	require.Error(t, err)
	require.Contains(t, err.Error(), "get L2 chain ID")
}

// TestExecuteRecovery_BuildAuthError verifies that an error from buildAuthFn is propagated.
func TestExecuteRecovery_BuildAuthError(t *testing.T) {
	t.Parallel()

	env := buildTestEnv(t, &stubL2Bridge{}, big.NewInt(1), nil, errors.New("key not found"), nil, nil)
	diagnosis := &DiagnosisResult{Case: Case1, DivergentLeaves: []*agglayertypes.BridgeExit{{}}}

	err := ExecuteRecovery(context.Background(), env, diagnosis)
	require.Error(t, err)
	require.Contains(t, err.Error(), "build admin transact opts")
}

// TestExecuteRecovery_ActivateEmergencyError verifies activate emergency state error propagation.
func TestExecuteRecovery_ActivateEmergencyError(t *testing.T) {
	t.Parallel()

	bridge := &stubL2Bridge{
		activateErr: errors.New("tx reverted"),
	}

	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, successReceipt(), nil)
	diagnosis := &DiagnosisResult{
		Case:             Case1,
		IsEmergencyState: false,
		DivergentLeaves:  []*agglayertypes.BridgeExit{{}},
	}

	err := ExecuteRecovery(context.Background(), env, diagnosis)
	require.Error(t, err)
	require.Contains(t, err.Error(), "activate emergency state")
}

// TestExecuteRecovery_Case2_BackwardLETError verifies that a BackwardLET error is propagated.
func TestExecuteRecovery_Case2_BackwardLETError(t *testing.T) {
	t.Parallel()

	bridge := &stubL2Bridge{
		tx:             &gethTypes.Transaction{},
		emergency:      true,
		backwardLETErr: errors.New("backward LET failed"),
	}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, successReceipt(), nil)
	env.BridgeService = &stubBridgeService{
		bridges: map[uint32]*bridgeservicetypes.BridgeResponse{},
	}

	diagnosis := &DiagnosisResult{
		Case:                  Case2,
		IsEmergencyState:      true,
		DivergencePoint:       0,
		L2CurrentDepositCount: 1,
		DivergentLeaves:       []*agglayertypes.BridgeExit{{}},
	}

	err := ExecuteRecovery(context.Background(), env, diagnosis)
	require.Error(t, err)
	require.Contains(t, err.Error(), "backward LET")
}

// --- stepActivateEmergency unit tests ---

// TestStepActivateEmergency_ActivateTxError verifies the tx send error path.
func TestStepActivateEmergency_ActivateTxError(t *testing.T) {
	t.Parallel()

	bridge := &stubL2Bridge{activateErr: errors.New("nonce too low")}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, successReceipt(), nil)
	auth := noopAuth()

	err := stepActivateEmergency(context.Background(), env, auth, &bind.CallOpts{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "send ActivateEmergencyState tx")
}

// TestStepActivateEmergency_ReceiptError verifies receipt wait error path.
func TestStepActivateEmergency_ReceiptError(t *testing.T) {
	t.Parallel()

	bridge := &stubL2Bridge{tx: &gethTypes.Transaction{}}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, nil, errors.New("timeout"))
	auth := noopAuth()

	err := stepActivateEmergency(context.Background(), env, auth, &bind.CallOpts{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "wait for ActivateEmergencyState receipt")
}

// TestStepActivateEmergency_TxStatusFailed verifies the status=0 path.
func TestStepActivateEmergency_TxStatusFailed(t *testing.T) {
	t.Parallel()

	bridge := &stubL2Bridge{tx: &gethTypes.Transaction{}}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, failedReceipt(), nil)
	auth := noopAuth()

	err := stepActivateEmergency(context.Background(), env, auth, &bind.CallOpts{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "ActivateEmergencyState tx failed")
}

// TestStepActivateEmergency_IsEmergencyCheckFails verifies the IsEmergencyState error after activation.
func TestStepActivateEmergency_IsEmergencyCheckFails(t *testing.T) {
	t.Parallel()

	bridge := &stubL2Bridge{
		tx:           &gethTypes.Transaction{},
		emergencyErr: errors.New("contract call failed"),
	}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, successReceipt(), nil)
	auth := noopAuth()

	err := stepActivateEmergency(context.Background(), env, auth, &bind.CallOpts{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "verify emergency state after activation")
}

// TestStepActivateEmergency_NotActiveAfterTx verifies the "not active" path.
func TestStepActivateEmergency_NotActiveAfterTx(t *testing.T) {
	t.Parallel()

	bridge := &stubL2Bridge{
		tx:        &gethTypes.Transaction{},
		emergency: false,
	}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, successReceipt(), nil)
	auth := noopAuth()

	err := stepActivateEmergency(context.Background(), env, auth, &bind.CallOpts{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "emergency state not active after ActivateEmergencyState")
}

// TestStepActivateEmergency_Success verifies the happy path.
func TestStepActivateEmergency_Success(t *testing.T) {
	t.Parallel()

	bridge := &stubL2Bridge{
		tx:        &gethTypes.Transaction{},
		emergency: true,
	}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, successReceipt(), nil)
	auth := noopAuth()

	err := stepActivateEmergency(context.Background(), env, auth, &bind.CallOpts{})
	require.NoError(t, err)
}

// --- stepDeactivateEmergency unit tests ---

// TestStepDeactivateEmergency_TxError verifies the tx send error.
func TestStepDeactivateEmergency_TxError(t *testing.T) {
	t.Parallel()

	bridge := &stubL2Bridge{deactivateErr: errors.New("gas too low")}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, successReceipt(), nil)

	err := stepDeactivateEmergency(context.Background(), env, noopAuth(), &bind.CallOpts{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "send DeactivateEmergencyState tx")
}

// TestStepDeactivateEmergency_ReceiptError verifies the receipt wait error.
func TestStepDeactivateEmergency_ReceiptError(t *testing.T) {
	t.Parallel()

	bridge := &stubL2Bridge{tx: &gethTypes.Transaction{}}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, nil, errors.New("ctx cancelled"))

	err := stepDeactivateEmergency(context.Background(), env, noopAuth(), &bind.CallOpts{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "wait for DeactivateEmergencyState receipt")
}

// TestStepDeactivateEmergency_TxFailed verifies the status=0 path.
func TestStepDeactivateEmergency_TxFailed(t *testing.T) {
	t.Parallel()

	bridge := &stubL2Bridge{tx: &gethTypes.Transaction{}}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, failedReceipt(), nil)

	err := stepDeactivateEmergency(context.Background(), env, noopAuth(), &bind.CallOpts{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "DeactivateEmergencyState tx failed")
}

// TestStepDeactivateEmergency_IsEmergencyCheckFails verifies IsEmergencyState error after deactivate.
func TestStepDeactivateEmergency_IsEmergencyCheckFails(t *testing.T) {
	t.Parallel()

	bridge := &stubL2Bridge{
		tx:           &gethTypes.Transaction{},
		emergencyErr: errors.New("rpc down"),
	}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, successReceipt(), nil)

	err := stepDeactivateEmergency(context.Background(), env, noopAuth(), &bind.CallOpts{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "verify emergency state after deactivation")
}

// TestStepDeactivateEmergency_StillActiveAfterTx verifies the "still active" path.
func TestStepDeactivateEmergency_StillActiveAfterTx(t *testing.T) {
	t.Parallel()

	bridge := &stubL2Bridge{
		tx:        &gethTypes.Transaction{},
		emergency: true,
	}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, successReceipt(), nil)

	err := stepDeactivateEmergency(context.Background(), env, noopAuth(), &bind.CallOpts{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "emergency state still active after DeactivateEmergencyState")
}

// TestStepDeactivateEmergency_Success verifies the happy path.
func TestStepDeactivateEmergency_Success(t *testing.T) {
	t.Parallel()

	bridge := &stubL2Bridge{
		tx:        &gethTypes.Transaction{},
		emergency: false,
	}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, successReceipt(), nil)

	err := stepDeactivateEmergency(context.Background(), env, noopAuth(), &bind.CallOpts{})
	require.NoError(t, err)
}

// --- stepBackwardLET unit tests ---

// TestStepBackwardLET_FetchL2LeafHashesError verifies the fetch error path.
func TestStepBackwardLET_FetchL2LeafHashesError(t *testing.T) {
	t.Parallel()

	bridge := &stubL2Bridge{tx: &gethTypes.Transaction{}}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, successReceipt(), nil)
	env.BridgeService = &stubBridgeService{
		errAtDC: map[uint32]error{0: errors.New("bridge service down")},
	}

	diagnosis := &DiagnosisResult{
		DivergencePoint:       1,
		L2CurrentDepositCount: 2,
	}

	err := stepBackwardLET(context.Background(), env, noopAuth(), &bind.CallOpts{}, diagnosis)
	require.Error(t, err)
	require.Contains(t, err.Error(), "fetch L2 leaf hashes")
}

// TestStepBackwardLET_BackwardLETTxError verifies the BackwardLET tx send error.
func TestStepBackwardLET_BackwardLETTxError(t *testing.T) {
	t.Parallel()

	br0 := &bridgeservicetypes.BridgeResponse{Amount: bridgeservicetypes.BigIntString("1")}
	br1 := &bridgeservicetypes.BridgeResponse{Amount: bridgeservicetypes.BigIntString("2")}

	bridge := &stubL2Bridge{backwardLETErr: errors.New("contract reverted")}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, successReceipt(), nil)
	env.BridgeService = &stubBridgeService{
		bridges: map[uint32]*bridgeservicetypes.BridgeResponse{0: br0, 1: br1},
	}

	diagnosis := &DiagnosisResult{
		DivergencePoint:       1,
		L2CurrentDepositCount: 2,
	}

	err := stepBackwardLET(context.Background(), env, noopAuth(), &bind.CallOpts{}, diagnosis)
	require.Error(t, err)
	require.Contains(t, err.Error(), "send BackwardLET tx")
}

// TestStepBackwardLET_ReceiptError verifies the receipt wait error path for BackwardLET.
func TestStepBackwardLET_ReceiptError(t *testing.T) {
	t.Parallel()

	br0 := &bridgeservicetypes.BridgeResponse{Amount: bridgeservicetypes.BigIntString("1")}
	br1 := &bridgeservicetypes.BridgeResponse{Amount: bridgeservicetypes.BigIntString("2")}

	bridge := &stubL2Bridge{tx: &gethTypes.Transaction{}}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, nil, errors.New("node unreachable"))
	env.BridgeService = &stubBridgeService{
		bridges: map[uint32]*bridgeservicetypes.BridgeResponse{0: br0, 1: br1},
	}

	diagnosis := &DiagnosisResult{
		DivergencePoint:       1,
		L2CurrentDepositCount: 2,
	}

	err := stepBackwardLET(context.Background(), env, noopAuth(), &bind.CallOpts{}, diagnosis)
	require.Error(t, err)
	require.Contains(t, err.Error(), "wait for BackwardLET receipt")
}

// TestStepBackwardLET_TxFailed verifies the tx status=0 path.
func TestStepBackwardLET_TxFailed(t *testing.T) {
	t.Parallel()

	br0 := &bridgeservicetypes.BridgeResponse{Amount: bridgeservicetypes.BigIntString("1")}
	br1 := &bridgeservicetypes.BridgeResponse{Amount: bridgeservicetypes.BigIntString("2")}

	bridge := &stubL2Bridge{tx: &gethTypes.Transaction{}}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, failedReceipt(), nil)
	env.BridgeService = &stubBridgeService{
		bridges: map[uint32]*bridgeservicetypes.BridgeResponse{0: br0, 1: br1},
	}

	diagnosis := &DiagnosisResult{
		DivergencePoint:       1,
		L2CurrentDepositCount: 2,
	}

	err := stepBackwardLET(context.Background(), env, noopAuth(), &bind.CallOpts{}, diagnosis)
	require.Error(t, err)
	require.Contains(t, err.Error(), "BackwardLET tx failed")
}

// TestStepBackwardLET_DepositCountMismatch verifies the DC verification mismatch path.
func TestStepBackwardLET_DepositCountMismatch(t *testing.T) {
	t.Parallel()

	br0 := &bridgeservicetypes.BridgeResponse{Amount: bridgeservicetypes.BigIntString("1")}
	br1 := &bridgeservicetypes.BridgeResponse{Amount: bridgeservicetypes.BigIntString("2")}

	bridge := &stubL2Bridge{
		tx:           &gethTypes.Transaction{},
		depositCount: big.NewInt(99),
	}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, successReceipt(), nil)
	env.BridgeService = &stubBridgeService{
		bridges: map[uint32]*bridgeservicetypes.BridgeResponse{0: br0, 1: br1},
	}

	diagnosis := &DiagnosisResult{
		DivergencePoint:       1,
		L2CurrentDepositCount: 2,
	}

	err := stepBackwardLET(context.Background(), env, noopAuth(), &bind.CallOpts{}, diagnosis)
	require.Error(t, err)
	require.Contains(t, err.Error(), "deposit count mismatch after BackwardLET")
}

// TestStepBackwardLET_Success verifies the happy path.
func TestStepBackwardLET_Success(t *testing.T) {
	t.Parallel()

	br0 := &bridgeservicetypes.BridgeResponse{Amount: bridgeservicetypes.BigIntString("1")}
	br1 := &bridgeservicetypes.BridgeResponse{Amount: bridgeservicetypes.BigIntString("2")}

	bridge := &stubL2Bridge{
		tx:           &gethTypes.Transaction{},
		depositCount: big.NewInt(1),
	}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, successReceipt(), nil)
	env.BridgeService = &stubBridgeService{
		bridges: map[uint32]*bridgeservicetypes.BridgeResponse{0: br0, 1: br1},
	}

	diagnosis := &DiagnosisResult{
		DivergencePoint:       1,
		L2CurrentDepositCount: 2,
	}

	err := stepBackwardLET(context.Background(), env, noopAuth(), &bind.CallOpts{}, diagnosis)
	require.NoError(t, err)
}

// --- stepForwardLETDivergentLeaves unit tests ---

// TestStepForwardLETDivergentLeaves_ForwardLETTxError verifies the tx error path.
func TestStepForwardLETDivergentLeaves_ForwardLETTxError(t *testing.T) {
	t.Parallel()

	leaf := &agglayertypes.BridgeExit{DestinationNetwork: 1, Amount: big.NewInt(1)}
	bridge := &stubL2Bridge{forwardLETErr: errors.New("contract error")}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, successReceipt(), nil)

	diagnosis := &DiagnosisResult{
		DivergencePoint: 0,
		DivergentLeaves: []*agglayertypes.BridgeExit{leaf},
	}

	err := stepForwardLETDivergentLeaves(context.Background(), env, noopAuth(), &bind.CallOpts{}, diagnosis)
	require.Error(t, err)
	require.Contains(t, err.Error(), "send ForwardLET (divergent leaves) tx")
}

// TestStepForwardLETDivergentLeaves_ReceiptError verifies the receipt error path.
func TestStepForwardLETDivergentLeaves_ReceiptError(t *testing.T) {
	t.Parallel()

	leaf := &agglayertypes.BridgeExit{DestinationNetwork: 1, Amount: big.NewInt(5)}
	bridge := &stubL2Bridge{tx: &gethTypes.Transaction{}}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, nil, errors.New("receipt error"))

	diagnosis := &DiagnosisResult{
		DivergencePoint: 0,
		DivergentLeaves: []*agglayertypes.BridgeExit{leaf},
	}

	err := stepForwardLETDivergentLeaves(context.Background(), env, noopAuth(), &bind.CallOpts{}, diagnosis)
	require.Error(t, err)
	require.Contains(t, err.Error(), "wait for ForwardLET (divergent leaves) receipt")
}

// TestStepForwardLETDivergentLeaves_TxFailed verifies the tx status=0 path.
func TestStepForwardLETDivergentLeaves_TxFailed(t *testing.T) {
	t.Parallel()

	leaf := &agglayertypes.BridgeExit{DestinationNetwork: 1, Amount: big.NewInt(5)}
	bridge := &stubL2Bridge{tx: &gethTypes.Transaction{}}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, failedReceipt(), nil)

	diagnosis := &DiagnosisResult{
		DivergencePoint: 0,
		DivergentLeaves: []*agglayertypes.BridgeExit{leaf},
	}

	err := stepForwardLETDivergentLeaves(context.Background(), env, noopAuth(), &bind.CallOpts{}, diagnosis)
	require.Error(t, err)
	require.Contains(t, err.Error(), "ForwardLET (divergent leaves) tx failed")
}

// TestStepForwardLETDivergentLeaves_DepositCountMismatch verifies DC mismatch after ForwardLET.
func TestStepForwardLETDivergentLeaves_DepositCountMismatch(t *testing.T) {
	t.Parallel()

	leaf := &agglayertypes.BridgeExit{DestinationNetwork: 1, Amount: big.NewInt(5)}
	bridge := &stubL2Bridge{
		tx:           &gethTypes.Transaction{},
		depositCount: big.NewInt(99),
	}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, successReceipt(), nil)

	diagnosis := &DiagnosisResult{
		DivergencePoint: 0,
		DivergentLeaves: []*agglayertypes.BridgeExit{leaf},
	}

	err := stepForwardLETDivergentLeaves(context.Background(), env, noopAuth(), &bind.CallOpts{}, diagnosis)
	require.Error(t, err)
	require.Contains(t, err.Error(), "deposit count mismatch after ForwardLET (divergent leaves)")
}

// TestStepForwardLETDivergentLeaves_GetRootError verifies GetRoot error after ForwardLET.
func TestStepForwardLETDivergentLeaves_GetRootError(t *testing.T) {
	t.Parallel()

	leaf := &agglayertypes.BridgeExit{DestinationNetwork: 1, Amount: big.NewInt(5)}
	bridge := &stubL2Bridge{
		tx:           &gethTypes.Transaction{},
		depositCount: big.NewInt(1),
		rootErr:      errors.New("root fetch failed"),
	}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, successReceipt(), nil)

	diagnosis := &DiagnosisResult{
		DivergencePoint: 0,
		DivergentLeaves: []*agglayertypes.BridgeExit{leaf},
	}

	err := stepForwardLETDivergentLeaves(context.Background(), env, noopAuth(), &bind.CallOpts{}, diagnosis)
	require.Error(t, err)
	require.Contains(t, err.Error(), "get root after ForwardLET (divergent leaves)")
}

// TestStepForwardLETDivergentLeaves_LERMismatch verifies the LER mismatch path.
func TestStepForwardLETDivergentLeaves_LERMismatch(t *testing.T) {
	t.Parallel()

	leaf := &agglayertypes.BridgeExit{DestinationNetwork: 1, Amount: big.NewInt(5)}
	bridge := &stubL2Bridge{
		tx:           &gethTypes.Transaction{},
		depositCount: big.NewInt(1),
		root:         [32]byte{0xFF},
	}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, successReceipt(), nil)

	diagnosis := &DiagnosisResult{
		DivergencePoint: 0,
		DivergentLeaves: []*agglayertypes.BridgeExit{leaf},
	}

	err := stepForwardLETDivergentLeaves(context.Background(), env, noopAuth(), &bind.CallOpts{}, diagnosis)
	require.Error(t, err)
	require.Contains(t, err.Error(), "LER mismatch after ForwardLET (divergent leaves)")
}

// TestStepForwardLETDivergentLeaves_Success verifies the happy path (DivergencePoint=0, one leaf).
func TestStepForwardLETDivergentLeaves_Success(t *testing.T) {
	t.Parallel()

	leaf := &agglayertypes.BridgeExit{DestinationNetwork: 1, Amount: big.NewInt(5)}
	leafHash := BridgeExitLeafHash(leaf)
	expectedLER, err := computeRootFromFrontier([32]common.Hash{}, 0, []common.Hash{leafHash})
	require.NoError(t, err)

	bridge := &stubL2Bridge{
		tx:           &gethTypes.Transaction{},
		depositCount: big.NewInt(1),
		root:         [32]byte(expectedLER),
	}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, successReceipt(), nil)

	diagnosis := &DiagnosisResult{
		DivergencePoint: 0,
		DivergentLeaves: []*agglayertypes.BridgeExit{leaf},
	}

	err = stepForwardLETDivergentLeaves(context.Background(), env, noopAuth(), &bind.CallOpts{}, diagnosis)
	require.NoError(t, err)
}

// TestStepForwardLETDivergentLeaves_WithDivergencePoint verifies non-zero DivergencePoint path.
func TestStepForwardLETDivergentLeaves_WithDivergencePoint(t *testing.T) {
	t.Parallel()

	existingBR := &bridgeservicetypes.BridgeResponse{
		OriginNetwork:      0,
		DestinationNetwork: 1,
		Amount:             bridgeservicetypes.BigIntString("100"),
	}

	leaf := &agglayertypes.BridgeExit{DestinationNetwork: 2, Amount: big.NewInt(200)}

	existingHash := BridgeResponseLeafHash(existingBR)
	leafHash := BridgeExitLeafHash(leaf)
	expectedLER, err := ComputeLERForNewLeaves([]common.Hash{existingHash}, []common.Hash{leafHash})
	require.NoError(t, err)

	bridge := &stubL2Bridge{
		tx:           &gethTypes.Transaction{},
		depositCount: big.NewInt(2),
		root:         [32]byte(expectedLER),
	}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, successReceipt(), nil)
	env.BridgeService = &stubBridgeService{
		bridges: map[uint32]*bridgeservicetypes.BridgeResponse{0: existingBR},
	}

	diagnosis := &DiagnosisResult{
		DivergencePoint: 1,
		DivergentLeaves: []*agglayertypes.BridgeExit{leaf},
	}

	err = stepForwardLETDivergentLeaves(context.Background(), env, noopAuth(), &bind.CallOpts{}, diagnosis)
	require.NoError(t, err)
}

// --- stepForwardLETExtraL2Bridges unit tests ---

// TestStepForwardLETExtraL2Bridges_ForwardLETTxError verifies the tx error path.
func TestStepForwardLETExtraL2Bridges_ForwardLETTxError(t *testing.T) {
	t.Parallel()

	bridge := &stubL2Bridge{forwardLETErr: errors.New("reverted")}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, successReceipt(), nil)

	leaf := &agglayertypes.BridgeExit{DestinationNetwork: 2, Amount: big.NewInt(200)}
	diagnosis := &DiagnosisResult{
		DivergencePoint: 0,
		DivergentLeaves: []*agglayertypes.BridgeExit{leaf},
		ExtraL2Bridges:  []bridgesync.LeafData{{DestinationNetwork: 3, Amount: big.NewInt(300)}},
	}

	err := stepForwardLETExtraL2Bridges(context.Background(), env, noopAuth(), &bind.CallOpts{}, diagnosis)
	require.Error(t, err)
	require.Contains(t, err.Error(), "send ForwardLET (extra L2 bridges) tx")
}

// TestStepForwardLETExtraL2Bridges_ReceiptError verifies the receipt error path.
func TestStepForwardLETExtraL2Bridges_ReceiptError(t *testing.T) {
	t.Parallel()

	bridge := &stubL2Bridge{tx: &gethTypes.Transaction{}}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, nil, errors.New("receipt unavailable"))

	leaf := &agglayertypes.BridgeExit{DestinationNetwork: 2, Amount: big.NewInt(200)}
	diagnosis := &DiagnosisResult{
		DivergencePoint: 0,
		DivergentLeaves: []*agglayertypes.BridgeExit{leaf},
		ExtraL2Bridges:  []bridgesync.LeafData{{DestinationNetwork: 3, Amount: big.NewInt(300)}},
	}

	err := stepForwardLETExtraL2Bridges(context.Background(), env, noopAuth(), &bind.CallOpts{}, diagnosis)
	require.Error(t, err)
	require.Contains(t, err.Error(), "wait for ForwardLET (extra L2 bridges) receipt")
}

// TestStepForwardLETExtraL2Bridges_TxFailed verifies the status=0 path.
func TestStepForwardLETExtraL2Bridges_TxFailed(t *testing.T) {
	t.Parallel()

	bridge := &stubL2Bridge{tx: &gethTypes.Transaction{}}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, failedReceipt(), nil)

	leaf := &agglayertypes.BridgeExit{DestinationNetwork: 2, Amount: big.NewInt(200)}
	diagnosis := &DiagnosisResult{
		DivergencePoint: 0,
		DivergentLeaves: []*agglayertypes.BridgeExit{leaf},
		ExtraL2Bridges:  []bridgesync.LeafData{{DestinationNetwork: 3, Amount: big.NewInt(300)}},
	}

	err := stepForwardLETExtraL2Bridges(context.Background(), env, noopAuth(), &bind.CallOpts{}, diagnosis)
	require.Error(t, err)
	require.Contains(t, err.Error(), "ForwardLET (extra L2 bridges) tx failed")
}

// TestStepForwardLETExtraL2Bridges_DepositCountMismatch verifies DC mismatch.
func TestStepForwardLETExtraL2Bridges_DepositCountMismatch(t *testing.T) {
	t.Parallel()

	leaf := &agglayertypes.BridgeExit{DestinationNetwork: 2, Amount: big.NewInt(200)}
	bridge := &stubL2Bridge{
		tx:           &gethTypes.Transaction{},
		depositCount: big.NewInt(99),
	}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, successReceipt(), nil)

	diagnosis := &DiagnosisResult{
		DivergencePoint: 0,
		DivergentLeaves: []*agglayertypes.BridgeExit{leaf},
		ExtraL2Bridges:  []bridgesync.LeafData{{DestinationNetwork: 3, Amount: big.NewInt(300)}},
	}

	err := stepForwardLETExtraL2Bridges(context.Background(), env, noopAuth(), &bind.CallOpts{}, diagnosis)
	require.Error(t, err)
	require.Contains(t, err.Error(), "deposit count mismatch after ForwardLET (extra L2 bridges)")
}

// TestStepForwardLETExtraL2Bridges_LERMismatch verifies LER mismatch after extra bridges ForwardLET.
func TestStepForwardLETExtraL2Bridges_LERMismatch(t *testing.T) {
	t.Parallel()

	leaf := &agglayertypes.BridgeExit{DestinationNetwork: 2, Amount: big.NewInt(200)}
	extraLeaf := bridgesync.LeafData{DestinationNetwork: 3, Amount: big.NewInt(300)}

	bridge := &stubL2Bridge{
		tx:           &gethTypes.Transaction{},
		depositCount: big.NewInt(2),
		root:         [32]byte{0xFF},
	}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, successReceipt(), nil)

	diagnosis := &DiagnosisResult{
		DivergencePoint: 0,
		DivergentLeaves: []*agglayertypes.BridgeExit{leaf},
		ExtraL2Bridges:  []bridgesync.LeafData{extraLeaf},
	}

	err := stepForwardLETExtraL2Bridges(context.Background(), env, noopAuth(), &bind.CallOpts{}, diagnosis)
	require.Error(t, err)
	require.Contains(t, err.Error(), "LER mismatch after ForwardLET (extra L2 bridges)")
}

// TestStepForwardLETExtraL2Bridges_Success verifies the happy path.
func TestStepForwardLETExtraL2Bridges_Success(t *testing.T) {
	t.Parallel()

	leaf := &agglayertypes.BridgeExit{DestinationNetwork: 2, Amount: big.NewInt(200)}
	extraLeaf := bridgesync.LeafData{DestinationNetwork: 3, Amount: big.NewInt(300)}

	leafHash := BridgeExitLeafHash(leaf)
	extraHash := leafDataLeafHash(extraLeaf)
	expectedLER, err := computeRootFromFrontier([32]common.Hash{}, 0, []common.Hash{leafHash, extraHash})
	require.NoError(t, err)

	bridge := &stubL2Bridge{
		tx:           &gethTypes.Transaction{},
		depositCount: big.NewInt(2),
		root:         [32]byte(expectedLER),
	}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, successReceipt(), nil)

	diagnosis := &DiagnosisResult{
		DivergencePoint: 0,
		DivergentLeaves: []*agglayertypes.BridgeExit{leaf},
		ExtraL2Bridges:  []bridgesync.LeafData{extraLeaf},
	}

	err = stepForwardLETExtraL2Bridges(context.Background(), env, noopAuth(), &bind.CallOpts{}, diagnosis)
	require.NoError(t, err)
}

// TestStepForwardLETExtraL2Bridges_WithDivergencePoint verifies non-zero DivergencePoint path.
func TestStepForwardLETExtraL2Bridges_WithDivergencePoint(t *testing.T) {
	t.Parallel()

	// DC=0 is the existing L2 leaf (fetched via BridgeService).
	existingBR := &bridgeservicetypes.BridgeResponse{
		OriginNetwork:      0,
		DestinationNetwork: 1,
		Amount:             bridgeservicetypes.BigIntString("50"),
	}

	// Divergent leaf at DC=1.
	divergentLeaf := &agglayertypes.BridgeExit{DestinationNetwork: 2, Amount: big.NewInt(100)}

	// Extra L2 leaf inserted at DC=2.
	extraLeaf := bridgesync.LeafData{DestinationNetwork: 3, Amount: big.NewInt(200)}

	// Compute expected LER: existing(DC=0) + divergent(DC=1) + extra(DC=2).
	existingHash := BridgeResponseLeafHash(existingBR)
	divHash := BridgeExitLeafHash(divergentLeaf)
	extraHash := leafDataLeafHash(extraLeaf)
	expectedLER, err := computeRootFromFrontier([32]common.Hash{}, 0, []common.Hash{existingHash, divHash, extraHash})
	require.NoError(t, err)

	bridge := &stubL2Bridge{
		tx:           &gethTypes.Transaction{},
		depositCount: big.NewInt(3),
		root:         [32]byte(expectedLER),
	}
	env := buildTestEnv(t, bridge, big.NewInt(1), nil, nil, successReceipt(), nil)
	env.BridgeService = &stubBridgeService{
		bridges: map[uint32]*bridgeservicetypes.BridgeResponse{0: existingBR},
	}

	diagnosis := &DiagnosisResult{
		DivergencePoint: 1,
		DivergentLeaves: []*agglayertypes.BridgeExit{divergentLeaf},
		ExtraL2Bridges:  []bridgesync.LeafData{extraLeaf},
	}

	err = stepForwardLETExtraL2Bridges(context.Background(), env, noopAuth(), &bind.CallOpts{}, diagnosis)
	require.NoError(t, err)
}
