package sender

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/0xPolygon/zkevm-ethtx-manager/ethtxmanager"
	ethtxtypes "github.com/0xPolygon/zkevm-ethtx-manager/types"
	aggoracletypes "github.com/agglayer/aggkit/aggoracle/types"
	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/test/contracts/claimmock"
	"github.com/ethereum/go-ethereum/common"
	coretypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

var fixedNow = time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)

func TestPackClaimCalldataForAsset(t *testing.T) {
	sender := newTestSender(
		t,
		newFakeStorage(makeRequest(bridgesynctypes.LeafTypeAsset)),
		&fakeEthTxManager{},
		&fakeClaimReader{},
	)
	request := makeRequest(bridgesynctypes.LeafTypeAsset)
	proof := makeProof()

	data, err := sender.packClaim(request, proof, claimGlobalIndex(request))
	require.NoError(t, err)

	bridgeABI, err := claimmock.ClaimmockMetaData.GetAbi()
	require.NoError(t, err)
	require.Equal(t, bridgeABI.Methods[claimAssetMethod].ID, data[:4])
	inputs, err := bridgeABI.Methods[claimAssetMethod].Inputs.Unpack(data[4:])
	require.NoError(t, err)
	requireClaimInputs(t, inputs, request, proof)
}

func TestPackClaimCalldataForMessage(t *testing.T) {
	sender := newTestSender(
		t,
		newFakeStorage(makeRequest(bridgesynctypes.LeafTypeMessage)),
		&fakeEthTxManager{},
		&fakeClaimReader{},
	)
	request := makeRequest(bridgesynctypes.LeafTypeMessage)
	proof := makeProof()

	data, err := sender.packClaim(request, proof, claimGlobalIndex(request))
	require.NoError(t, err)

	bridgeABI, err := claimmock.ClaimmockMetaData.GetAbi()
	require.NoError(t, err)
	require.Equal(t, bridgeABI.Methods[claimMessageMethod].ID, data[:4])
	inputs, err := bridgeABI.Methods[claimMessageMethod].Inputs.Unpack(data[4:])
	require.NoError(t, err)
	requireClaimInputs(t, inputs, request, proof)
}

func TestSubmitClaimAddsTransactionAndConfirmsMinedResult(t *testing.T) {
	request := makeRequest(bridgesynctypes.LeafTypeAsset)
	storage := newFakeStorage(request)
	txManagerID := common.HexToHash("0xabc1")
	claimTx := coretypes.NewTransaction(
		1,
		common.HexToAddress("0x5000000000000000000000000000000000000005"),
		common.Big0,
		1,
		common.Big1,
		[]byte{0xca, 0xfe},
	)
	ethTxManager := &fakeEthTxManager{
		addID: txManagerID,
		results: []ethtxtypes.MonitoredTxResult{{
			ID:     txManagerID,
			Status: ethtxtypes.MonitoredTxStatusMined,
			Txs: map[common.Hash]ethtxtypes.TxResult{
				claimTx.Hash(): {Tx: claimTx},
			},
		}},
	}
	sender := newTestSender(t, storage, ethTxManager, &fakeClaimReader{})

	attempt, err := sender.SubmitClaim(context.Background(), request, makeProof(), makeTarget(1))
	require.NoError(t, err)

	require.Equal(t, txManagerID, attempt.TxManagerID)
	require.Equal(t, claimTx.Hash(), attempt.ClaimTxHash)
	require.Equal(t, ethtxtypes.MonitoredTxStatusMined, attempt.Status)
	require.Equal(t, autoclaimtypes.RequestStatusConfirmed, storage.request.Status)
	require.Equal(t, makeTarget(1).BridgeAddr, *ethTxManager.addTo)
	require.Equal(t, common.Big0, ethTxManager.addValue)
	require.Equal(t, makeTarget(1).GasOffset, ethTxManager.addGasOffset)
	require.Nil(t, ethTxManager.addSidecar)
	require.NotEmpty(t, ethTxManager.addData)
	require.GreaterOrEqual(t, len(storage.attempts), 2)
	require.Equal(t, txManagerID, storage.attempts[0].TxManagerID)
	require.Equal(t, makeTarget(1).BridgeAddr, storage.attempts[0].TargetBridgeAddr)
	require.NotEmpty(t, storage.attempts[0].TransactionData)
}

func TestSubmitClaimTreatsErrAlreadyExistsAsIdempotent(t *testing.T) {
	request := makeRequest(bridgesynctypes.LeafTypeAsset)
	storage := newFakeStorage(request)
	txManagerID := common.HexToHash("0xabc2")
	ethTxManager := &fakeEthTxManager{
		addID:  txManagerID,
		addErr: ethtxmanager.ErrAlreadyExists,
		results: []ethtxtypes.MonitoredTxResult{{
			ID:     txManagerID,
			Status: ethtxtypes.MonitoredTxStatusFinalized,
		}},
	}
	sender := newTestSender(t, storage, ethTxManager, &fakeClaimReader{})

	attempt, err := sender.SubmitClaim(context.Background(), request, makeProof(), makeTarget(1))
	require.NoError(t, err)

	require.Equal(t, txManagerID, attempt.TxManagerID)
	require.Equal(t, ethtxtypes.MonitoredTxStatusFinalized, attempt.Status)
	require.Equal(t, autoclaimtypes.RequestStatusConfirmed, storage.request.Status)
	require.Equal(t, "transaction manager already has claim", storage.attempts[0].StatusReason)
}

func TestSubmitClaimPollsInflightStatusesUntilConfirmed(t *testing.T) {
	request := makeRequest(bridgesynctypes.LeafTypeAsset)
	storage := newFakeStorage(request)
	txManagerID := common.HexToHash("0xabc3")
	ethTxManager := &fakeEthTxManager{
		addID: txManagerID,
		results: []ethtxtypes.MonitoredTxResult{
			{ID: txManagerID, Status: ethtxtypes.MonitoredTxStatusCreated},
			{ID: txManagerID, Status: ethtxtypes.MonitoredTxStatusSent},
			{ID: txManagerID, Status: ethtxtypes.MonitoredTxStatusFinalized},
		},
	}
	sender := newTestSender(t, storage, ethTxManager, &fakeClaimReader{})

	attempt, err := sender.SubmitClaim(context.Background(), request, makeProof(), makeTarget(1))
	require.NoError(t, err)

	require.Equal(t, ethtxtypes.MonitoredTxStatusFinalized, attempt.Status)
	require.Equal(t, autoclaimtypes.RequestStatusConfirmed, storage.request.Status)
	require.Equal(t, 3, ethTxManager.resultCalls)
	require.Equal(t, []ethtxtypes.MonitoredTxStatus{
		ethtxtypes.MonitoredTxStatusCreated,
		ethtxtypes.MonitoredTxStatusCreated,
		ethtxtypes.MonitoredTxStatusSent,
		ethtxtypes.MonitoredTxStatusFinalized,
	}, storage.attemptStatuses())
}

func TestSubmitClaimHonorsContextCancellationWhilePolling(t *testing.T) {
	request := makeRequest(bridgesynctypes.LeafTypeAsset)
	storage := newFakeStorage(request)
	ctx, cancel := context.WithCancel(context.Background())
	txManagerID := common.HexToHash("0xabc4")
	ethTxManager := &fakeEthTxManager{
		addID: txManagerID,
		results: []ethtxtypes.MonitoredTxResult{{
			ID:     txManagerID,
			Status: ethtxtypes.MonitoredTxStatusCreated,
		}},
		onResult: cancel,
	}
	sender := newTestSender(t, storage, ethTxManager, &fakeClaimReader{})

	target := makeTarget(1)
	target.WaitPeriod = time.Hour
	attempt, err := sender.SubmitClaim(ctx, request, makeProof(), target)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, ethtxtypes.MonitoredTxStatusCreated, attempt.Status)
	require.Equal(t, autoclaimtypes.RequestStatusSent, storage.request.Status)
	require.Equal(t, 1, ethTxManager.resultCalls)
}

func TestSubmitClaimFailedStatusWithoutRetryMarksFailed(t *testing.T) {
	request := makeRequest(bridgesynctypes.LeafTypeAsset)
	storage := newFakeStorage(request)
	txManagerID := common.HexToHash("0xabc5")
	ethTxManager := &fakeEthTxManager{
		addID: txManagerID,
		results: []ethtxtypes.MonitoredTxResult{{
			ID:     txManagerID,
			Status: ethtxtypes.MonitoredTxStatusFailed,
		}},
	}
	sender := newTestSender(t, storage, ethTxManager, &fakeClaimReader{})

	attempt, err := sender.SubmitClaim(context.Background(), request, makeProof(), makeTarget(1))
	require.ErrorIs(t, err, ErrTerminalStatus)
	require.Equal(t, ethtxtypes.MonitoredTxStatusFailed, attempt.Status)
	require.Equal(t, autoclaimtypes.RequestStatusFailed, storage.request.Status)
	require.Equal(t, "claim transaction failed", storage.request.LastError)
}

func TestSubmitClaimEvictedStatusWithRetryRequeues(t *testing.T) {
	request := makeRequest(bridgesynctypes.LeafTypeAsset)
	storage := newFakeStorage(request)
	txManagerID := common.HexToHash("0xabc6")
	ethTxManager := &fakeEthTxManager{
		addID: txManagerID,
		results: []ethtxtypes.MonitoredTxResult{{
			ID:     txManagerID,
			Status: ethtxtypes.MonitoredTxStatusEvicted,
		}},
	}
	sender := newTestSender(t, storage, ethTxManager, &fakeClaimReader{})

	attempt, err := sender.SubmitClaim(context.Background(), request, makeProof(), makeTarget(2))
	require.ErrorIs(t, err, ErrRetryableStatus)
	require.Equal(t, ethtxtypes.MonitoredTxStatusEvicted, attempt.Status)
	require.Equal(t, autoclaimtypes.RequestStatusQueued, storage.request.Status)
	require.Equal(t, uint64(1), attempt.RetryCount)
	require.Equal(t, uint64(2), attempt.MaxRetries)
}

func TestSubmitClaimPreventsDuplicateConfirmedClaims(t *testing.T) {
	t.Run("stored confirmed", func(t *testing.T) {
		request := makeRequest(bridgesynctypes.LeafTypeAsset)
		request.Status = autoclaimtypes.RequestStatusConfirmed
		storage := newFakeStorage(request)
		ethTxManager := &fakeEthTxManager{}
		sender := newTestSender(t, storage, ethTxManager, &fakeClaimReader{})

		attempt, err := sender.SubmitClaim(context.Background(), request, makeProof(), makeTarget(1))
		require.NoError(t, err)
		require.Equal(t, ethtxtypes.MonitoredTxStatusFinalized, attempt.Status)
		require.Equal(t, 0, ethTxManager.addCalls)
		require.Empty(t, storage.attempts)
	})

	t.Run("target already claimed", func(t *testing.T) {
		request := makeRequest(bridgesynctypes.LeafTypeAsset)
		storage := newFakeStorage(request)
		ethTxManager := &fakeEthTxManager{}
		sender := newTestSender(t, storage, ethTxManager, &fakeClaimReader{claimed: true})

		attempt, err := sender.SubmitClaim(context.Background(), request, makeProof(), makeTarget(1))
		require.NoError(t, err)
		require.Equal(t, ethtxtypes.MonitoredTxStatusFinalized, attempt.Status)
		require.Equal(t, autoclaimtypes.RequestStatusConfirmed, storage.request.Status)
		require.Equal(t, 0, ethTxManager.addCalls)
		require.Empty(t, storage.attempts)
	})
}

func requireClaimInputs(
	t *testing.T,
	inputs []any,
	request autoclaimtypes.AutoClaimRequest,
	proof autoclaimtypes.ClaimProof,
) {
	t.Helper()

	require.Len(t, inputs, 11)
	require.Equal(t, [32][32]byte(proof.ABILocalExitRoot), inputs[0])
	require.Equal(t, [32][32]byte(proof.ABIRollupExitRoot), inputs[1])
	require.Zero(t, claimGlobalIndex(request).Cmp(inputs[2].(*big.Int)))
	require.Equal(t, [32]byte(proof.MainnetExitRoot), inputs[3])
	require.Equal(t, [32]byte(proof.RollupExitRoot), inputs[4])
	require.Equal(t, request.Bridge.OriginNetwork, inputs[5])
	require.Equal(t, request.Bridge.OriginAddress, inputs[6])
	require.Equal(t, request.Bridge.DestinationNetwork, inputs[7])
	require.Equal(t, request.Bridge.DestinationAddress, inputs[8])
	require.Zero(t, request.Bridge.Amount.Cmp(inputs[9].(*big.Int)))
	require.Equal(t, request.Bridge.Metadata, inputs[10])
}

func newTestSender(
	t *testing.T,
	storage *fakeStorage,
	ethTxManager aggoracletypes.EthTxManager,
	targetClaimReader autoclaimtypes.TargetClaimReader,
) *Sender {
	t.Helper()

	sender, err := New(
		storage,
		ethTxManager,
		targetClaimReader,
		WithNow(func() time.Time { return fixedNow }),
		WithPollPeriod(time.Nanosecond),
	)
	require.NoError(t, err)
	return sender
}

func makeRequest(leafType bridgesynctypes.LeafType) autoclaimtypes.AutoClaimRequest {
	bridge := autoclaimtypes.BridgeExit{
		BlockNum:           10,
		BlockPos:           2,
		TxHash:             common.HexToHash("0x1111"),
		LeafType:           leafType,
		OriginNetwork:      autoclaimtypes.L1OriginNetwork,
		OriginAddress:      common.HexToAddress("0x1000000000000000000000000000000000000001"),
		DestinationNetwork: 20,
		DestinationAddress: common.HexToAddress("0x2000000000000000000000000000000000000002"),
		Amount:             big.NewInt(12345),
		Metadata:           []byte{0xde, 0xad, 0xbe, 0xef},
		DepositCount:       7,
		GlobalIndex:        autoclaimtypes.DeriveGlobalIndex(autoclaimtypes.L1OriginNetwork, 7),
	}

	return autoclaimtypes.AutoClaimRequest{
		Key:         autoclaimtypes.DeriveRequestKey(bridge.OriginNetwork, bridge.DestinationNetwork, 7),
		Status:      autoclaimtypes.RequestStatusQueued,
		Bridge:      bridge,
		GlobalIndex: new(big.Int).Set(bridge.GlobalIndex),
		RetryCount:  0,
		MaxRetries:  1,
		CreatedAt:   fixedNow,
		UpdatedAt:   fixedNow,
	}
}

func makeProof() autoclaimtypes.ClaimProof {
	proof := autoclaimtypes.ClaimProof{
		MainnetExitRoot: common.HexToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		RollupExitRoot:  common.HexToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
		PreparedAt:      fixedNow,
	}
	proof.ABILocalExitRoot[0] = common.HexToHash("0x01")
	proof.ABILocalExitRoot[31] = common.HexToHash("0x02")
	proof.ABIRollupExitRoot[0] = common.HexToHash("0x03")
	proof.ABIRollupExitRoot[31] = common.HexToHash("0x04")
	return proof
}

func makeTarget(maxRetries uint64) autoclaimtypes.ClaimerTarget {
	return autoclaimtypes.ClaimerTarget{
		ID:                 "claimer-20",
		DestinationNetwork: 20,
		BridgeAddr:         common.HexToAddress("0x5000000000000000000000000000000000000005"),
		GasOffset:          123,
		WaitPeriod:         time.Nanosecond,
		MaxRetries:         maxRetries,
	}
}

type fakeStorage struct {
	request  autoclaimtypes.AutoClaimRequest
	attempts []autoclaimtypes.TransactionAttempt
}

func newFakeStorage(request autoclaimtypes.AutoClaimRequest) *fakeStorage {
	return &fakeStorage{request: request}
}

func (s *fakeStorage) EnqueueRequest(
	context.Context,
	autoclaimtypes.AutoClaimRequest,
) (*autoclaimtypes.AutoClaimRequest, bool, error) {
	return nil, false, errors.New("not implemented")
}

func (s *fakeStorage) GetRequest(
	context.Context,
	autoclaimtypes.RequestKey,
) (*autoclaimtypes.AutoClaimRequest, error) {
	request := s.request
	return &request, nil
}

func (s *fakeStorage) ListRequests(
	context.Context,
	autoclaimtypes.RequestFilter,
) (*autoclaimtypes.RequestPage, error) {
	return nil, errors.New("not implemented")
}

func (s *fakeStorage) ListRecoverableRequests(
	context.Context,
	autoclaimtypes.RecoveryFilter,
) (*autoclaimtypes.RequestPage, error) {
	return nil, errors.New("not implemented")
}

func (s *fakeStorage) RecordPolicyDecision(
	context.Context,
	autoclaimtypes.RequestKey,
	autoclaimtypes.PolicyDecision,
) error {
	return errors.New("not implemented")
}

func (s *fakeStorage) RecordManualDecision(
	context.Context,
	autoclaimtypes.RequestKey,
	autoclaimtypes.PolicyDecision,
) error {
	return errors.New("not implemented")
}

func (s *fakeStorage) SaveProof(context.Context, autoclaimtypes.RequestKey, autoclaimtypes.ClaimProof) error {
	return errors.New("not implemented")
}

func (s *fakeStorage) RecordTransactionAttempt(
	_ context.Context,
	_ autoclaimtypes.RequestKey,
	attempt autoclaimtypes.TransactionAttempt,
) error {
	s.attempts = append(s.attempts, attempt)
	s.request.RetryCount = attempt.RetryCount
	s.request.MaxRetries = attempt.MaxRetries
	s.request.TxManagerID = &attempt.TxManagerID
	if attempt.ClaimTxHash != (common.Hash{}) {
		s.request.ClaimTxHash = &attempt.ClaimTxHash
	}
	s.request.LastError = attempt.LastError
	return nil
}

func (s *fakeStorage) TransitionRequest(
	_ context.Context,
	_ autoclaimtypes.RequestKey,
	from autoclaimtypes.RequestStatus,
	to autoclaimtypes.RequestStatus,
	now time.Time,
) (*autoclaimtypes.AutoClaimRequest, error) {
	if s.request.Status != from {
		return nil, errors.New("precondition failed")
	}
	if !from.CanTransitionTo(to) {
		return nil, errors.New("invalid transition")
	}
	s.request.Status = to
	s.request.UpdatedAt = now
	request := s.request
	return &request, nil
}

func (s *fakeStorage) UpdateLastError(
	_ context.Context,
	_ autoclaimtypes.RequestKey,
	lastError string,
	now time.Time,
) error {
	s.request.LastError = lastError
	s.request.UpdatedAt = now
	return nil
}

func (s *fakeStorage) attemptStatuses() []ethtxtypes.MonitoredTxStatus {
	statuses := make([]ethtxtypes.MonitoredTxStatus, 0, len(s.attempts))
	for _, attempt := range s.attempts {
		statuses = append(statuses, attempt.Status)
	}
	return statuses
}

type fakeClaimReader struct {
	claimed bool
	err     error
}

func (r *fakeClaimReader) IsClaimed(context.Context, *big.Int) (bool, error) {
	return r.claimed, r.err
}

type fakeEthTxManager struct {
	addID        common.Hash
	addErr       error
	addCalls     int
	addTo        *common.Address
	addValue     *big.Int
	addData      []byte
	addGasOffset uint64
	addSidecar   *coretypes.BlobTxSidecar
	results      []ethtxtypes.MonitoredTxResult
	resultCalls  int
	onResult     func()
}

func (m *fakeEthTxManager) Remove(context.Context, common.Hash) error {
	return nil
}

func (m *fakeEthTxManager) ResultsByStatus(
	context.Context,
	[]ethtxtypes.MonitoredTxStatus,
) ([]ethtxtypes.MonitoredTxResult, error) {
	return nil, nil
}

func (m *fakeEthTxManager) Result(context.Context, common.Hash) (ethtxtypes.MonitoredTxResult, error) {
	m.resultCalls++
	if m.onResult != nil {
		m.onResult()
	}
	if len(m.results) == 0 {
		return ethtxtypes.MonitoredTxResult{}, errors.New("no result")
	}
	result := m.results[0]
	m.results = m.results[1:]
	return result, nil
}

func (m *fakeEthTxManager) Add(
	_ context.Context,
	to *common.Address,
	value *big.Int,
	data []byte,
	gasOffset uint64,
	sidecar *coretypes.BlobTxSidecar,
) (common.Hash, error) {
	m.addCalls++
	m.addTo = to
	m.addValue = value
	m.addData = append([]byte(nil), data...)
	m.addGasOffset = gasOffset
	m.addSidecar = sidecar
	return m.addID, m.addErr
}

func (m *fakeEthTxManager) From() common.Address {
	return common.HexToAddress("0x6000000000000000000000000000000000000006")
}
