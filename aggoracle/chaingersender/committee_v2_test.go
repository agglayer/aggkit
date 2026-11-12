package chaingersender

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/aggoraclecommittee"
	"github.com/0xPolygon/zkevm-ethtx-manager/ethtxmanager"
	ethtxtypes "github.com/0xPolygon/zkevm-ethtx-manager/types"
	"github.com/agglayer/aggkit/aggoracle/gervalidator"
	"github.com/agglayer/aggkit/aggoracle/mocks"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var (
	v2TestGER     = common.HexToHash("0x0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	validatorA    = common.HexToAddress("0x00000000000000000000000000000000000000a0")
	validatorB    = common.HexToAddress("0x00000000000000000000000000000000000000b0")
	validatorC    = common.HexToAddress("0x00000000000000000000000000000000000000c0")
	errRPC        = errors.New("rpc down")
	injectTxID    = common.HexToHash("0xabc1")
	injectTxID2   = common.HexToHash("0xabc2")
	v2RoundWait   = 10 * time.Millisecond
	v2TestTimeout = 5 * time.Second
)

// revertDataError is the error that a node returns for a reverted call: it carries the revert data
type revertDataError struct{ data string }

func (e revertDataError) Error() string { return "execution reverted" }
func (e revertDataError) ErrorData() any {
	return e.data
}

// revertWith returns the error of a call that reverts with the custom error of the committee
func revertWith(t *testing.T, errorName string) error {
	t.Helper()
	committeeABI, err := aggoraclecommittee.AggoraclecommitteeMetaData.GetAbi()
	require.NoError(t, err)
	abiErr, ok := committeeABI.Errors[errorName]
	require.True(t, ok, "error %s", errorName)
	return revertDataError{data: hexutil.Encode(abiErr.ID[:4])}
}

func validatorInfos(urls ...string) []aggoraclecommittee.IAggOracleCommitteeValidatorInfo {
	addrs := []common.Address{validatorA, validatorB, validatorC}
	infos := make([]aggoraclecommittee.IAggOracleCommitteeValidatorInfo, len(urls))
	for i, url := range urls {
		infos[i] = aggoraclecommittee.IAggOracleCommitteeValidatorInfo{Addr: addrs[i], Url: url}
	}
	return infos
}

type v2Fixture struct {
	t          *testing.T
	gerManager *mocks.L2GERManagerContract
	committee  *mocks.AggOracleCommitteeV2Contract
	collector  *mocks.GERSignatureCollector
	ethTxMan   *mocks.EthTxManager
}

func newV2Fixture(t *testing.T) *v2Fixture {
	t.Helper()
	return &v2Fixture{
		t:          t,
		gerManager: mocks.NewL2GERManagerContract(t),
		committee:  mocks.NewAggOracleCommitteeV2Contract(t),
		collector:  mocks.NewGERSignatureCollector(t),
		ethTxMan:   mocks.NewEthTxManager(t),
	}
}

func defaultV2Config() CommitteeV2Config {
	return CommitteeV2Config{CollectRetryInterval: v2RoundWait, MaxInjectAttempts: 3}
}

func evmTestConfig() EVMConfig {
	return EVMConfig{
		GlobalExitRootL2Addr: gerL2Addr,
		WaitPeriodMonitorTx:  cfgtypes.NewDuration(time.Millisecond),
	}
}

// expectCommitteeState expects the reading of the committee: threshold and validators
func (f *v2Fixture) expectCommitteeState(threshold int64, infos []aggoraclecommittee.IAggOracleCommitteeValidatorInfo) {
	f.t.Helper()
	f.committee.EXPECT().Threshold(mock.Anything).Return(big.NewInt(threshold), nil).Once()
	f.committee.EXPECT().GetValidatorsInfo(mock.Anything).Return(infos, nil).Once()
}

// expectChecks expects what the constructor reads, before the committee state, to validate the committee
func (f *v2Fixture) expectChecks() {
	f.t.Helper()
	f.ethTxMan.EXPECT().From().Return(proposerAddr).Once()
	f.committee.EXPECT().Version(mock.Anything).Return("v2.0.0", nil).Once()
	f.committee.EXPECT().AgglayerGERL2(mock.Anything).Return(gerL2Addr, nil).Once()
	f.gerManager.EXPECT().GlobalExitRootUpdater(mock.Anything).Return(committeeAddr, nil).Once()
	f.committee.EXPECT().Proposer(mock.Anything).Return(proposerAddr, nil).Once()
}

// expectValidation expects everything the constructor reads to validate the committee
func (f *v2Fixture) expectValidation(threshold int64, infos []aggoraclecommittee.IAggOracleCommitteeValidatorInfo) {
	f.t.Helper()
	f.expectChecks()
	f.expectCommitteeState(threshold, infos)
}

// expectInvalidThreshold expects the validation to stop at a threshold that is out of range,
// before reading the validators
func (f *v2Fixture) expectInvalidThreshold(threshold int64) {
	f.t.Helper()
	f.expectChecks()
	f.committee.EXPECT().Threshold(mock.Anything).Return(big.NewInt(threshold), nil).Once()
}

func (f *v2Fixture) newSender(cfg CommitteeV2Config) *CommitteeV2Sender {
	f.t.Helper()
	f.expectValidation(2, validatorInfos("http://a", "http://b", "http://c"))
	sender, err := NewCommitteeV2Sender(context.Background(), log.GetDefaultLogger(), evmTestConfig(), cfg,
		committeeAddr, f.gerManager, f.committee, f.collector, f.ethTxMan)
	require.NoError(f.t, err)
	return sender
}

// expectNotProcessed expects the check that the GER is not injected, nor processed by the committee
func (f *v2Fixture) expectNotProcessed(ger common.Hash) {
	f.t.Helper()
	f.gerManager.EXPECT().GlobalExitRootMap(mock.Anything, [32]byte(ger)).Return(big.NewInt(0), nil).Once()
	f.committee.EXPECT().InjectedGERs(mock.Anything, [32]byte(ger)).Return(false, nil).Once()
}

func twoSignatures() *gervalidator.Result {
	signatures := make([]byte, 2*gervalidator.SignatureLength)
	for i := range signatures {
		signatures[i] = byte(i + 1)
	}
	return &gervalidator.Result{Signatures: signatures, Signers: []common.Address{validatorA, validatorB}}
}

// expectInjectTx expects the injectGER transaction to the committee and that it is mined
func (f *v2Fixture) expectInjectTx(ger common.Hash, signatures []byte, id common.Hash, status ethtxtypes.MonitoredTxStatus) {
	f.t.Helper()
	committeeABI, err := aggoraclecommittee.AggoraclecommitteeMetaData.GetAbi()
	require.NoError(f.t, err)
	expectedData, err := committeeABI.Pack("injectGER", [32]byte(ger), signatures)
	require.NoError(f.t, err)

	f.ethTxMan.EXPECT().Add(mock.Anything, &committeeAddr, common.Big0, expectedData, uint64(0), mock.Anything).
		Return(id, nil).Once()
	f.ethTxMan.EXPECT().Result(mock.Anything, id).
		Return(ethtxtypes.MonitoredTxResult{ID: id, Status: status, MinedAtBlockNumber: big.NewInt(10)}, nil).Once()
}

func notReachedError(retryable bool) error {
	failureKind := gervalidator.FailureGERRejected
	if retryable {
		failureKind = gervalidator.FailureTransient
	}
	return &gervalidator.ThresholdNotReachedError{
		GER: v2TestGER, Threshold: 2, Valid: 0,
		Failures: []gervalidator.ValidatorFailure{{Kind: failureKind}, {Kind: failureKind}, {Kind: failureKind}},
	}
}

func TestNewCommitteeV2Sender_Validation(t *testing.T) {
	infos := validatorInfos("http://a", "http://b", "http://c")

	tests := []struct {
		name    string
		cfg     CommitteeV2Config
		mutate  func(f *v2Fixture) // sets the expectations, the last one is the failing one
		wantErr string
	}{
		{
			name: "valid", cfg: defaultV2Config(),
			mutate: func(f *v2Fixture) { f.expectValidation(2, infos) },
		},
		{
			name: "no inject attempts", cfg: CommitteeV2Config{CollectRetryInterval: v2RoundWait},
			mutate: func(*v2Fixture) {}, wantErr: "MaxInjectAttempts must be at least 1",
		},
		{
			name: "no retry interval", cfg: CommitteeV2Config{MaxInjectAttempts: 1},
			mutate: func(*v2Fixture) {}, wantErr: "CollectRetryInterval must be greater than 0",
		},
		{
			name: "version can't be read", cfg: defaultV2Config(),
			mutate: func(f *v2Fixture) {
				f.ethTxMan.EXPECT().From().Return(proposerAddr).Once()
				f.committee.EXPECT().Version(mock.Anything).Return("", errRPC).Once()
			},
			wantErr: "failed to read the version",
		},
		{
			name: "a v1 committee", cfg: defaultV2Config(),
			mutate: func(f *v2Fixture) {
				f.ethTxMan.EXPECT().From().Return(proposerAddr).Once()
				f.committee.EXPECT().Version(mock.Anything).Return("v1.0.0", nil).Once()
			},
			wantErr: `is not an AggOracleCommittee v2: its version is "v1.0.0"`,
		},
		{
			name: "committee of another L2 GER manager", cfg: defaultV2Config(),
			mutate: func(f *v2Fixture) {
				f.ethTxMan.EXPECT().From().Return(proposerAddr).Once()
				f.committee.EXPECT().Version(mock.Anything).Return("v2.0.0", nil).Once()
				f.committee.EXPECT().AgglayerGERL2(mock.Anything).Return(common.HexToAddress("0x99"), nil).Once()
			},
			wantErr: "works with the L2 GER manager",
		},
		{
			name: "the committee is not the GER updater", cfg: defaultV2Config(),
			mutate: func(f *v2Fixture) {
				f.ethTxMan.EXPECT().From().Return(proposerAddr).Once()
				f.committee.EXPECT().Version(mock.Anything).Return("v2.0.0", nil).Once()
				f.committee.EXPECT().AgglayerGERL2(mock.Anything).Return(gerL2Addr, nil).Once()
				f.gerManager.EXPECT().GlobalExitRootUpdater(mock.Anything).Return(eoaAddr, nil).Once()
			},
			wantErr: "is not the GER updater of the L2 GER manager",
		},
		{
			name: "the account is not the proposer", cfg: defaultV2Config(),
			mutate: func(f *v2Fixture) {
				f.ethTxMan.EXPECT().From().Return(eoaAddr).Once()
				f.committee.EXPECT().Version(mock.Anything).Return("v2.0.0", nil).Once()
				f.committee.EXPECT().AgglayerGERL2(mock.Anything).Return(gerL2Addr, nil).Once()
				f.gerManager.EXPECT().GlobalExitRootUpdater(mock.Anything).Return(committeeAddr, nil).Once()
				f.committee.EXPECT().Proposer(mock.Anything).Return(proposerAddr, nil).Once()
			},
			wantErr: "is not the proposer of the AggOracleCommittee",
		},
		{
			name: "threshold greater than the validators", cfg: defaultV2Config(),
			mutate: func(f *v2Fixture) { f.expectValidation(4, infos) }, wantErr: "greater than its validators",
		},
		{
			name: "threshold zero", cfg: defaultV2Config(),
			mutate: func(f *v2Fixture) { f.expectInvalidThreshold(0) }, wantErr: "unexpected threshold",
		},
		{
			name: "threshold above the maximum", cfg: defaultV2Config(),
			mutate: func(f *v2Fixture) { f.expectInvalidThreshold(256) }, wantErr: "unexpected threshold",
		},
		{
			name: "not enough validators with URL", cfg: defaultV2Config(),
			mutate:  func(f *v2Fixture) { f.expectValidation(2, validatorInfos("http://a", "", "")) },
			wantErr: "only 1 of its 3 validators have a URL",
		},
		{
			name: "the overrides provide the missing URLs",
			cfg: CommitteeV2Config{
				CollectRetryInterval: v2RoundWait, MaxInjectAttempts: 1,
				ValidatorURLOverrides: map[common.Address]string{validatorB: "http://override-b"},
			},
			mutate: func(f *v2Fixture) { f.expectValidation(2, validatorInfos("http://a", "", "")) },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newV2Fixture(t)
			tc.mutate(f)
			sender, err := NewCommitteeV2Sender(context.Background(), log.GetDefaultLogger(), evmTestConfig(), tc.cfg,
				committeeAddr, f.gerManager, f.committee, f.collector, f.ethTxMan)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				require.Nil(t, sender)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, sender)
		})
	}
}

func TestCommitteeV2Sender_IsGERInjected(t *testing.T) {
	f := newV2Fixture(t)
	sender := f.newSender(defaultV2Config())

	f.gerManager.EXPECT().GlobalExitRootMap(mock.Anything, [32]byte(v2TestGER)).Return(big.NewInt(0), nil).Once()
	injected, err := sender.IsGERInjected(v2TestGER)
	require.NoError(t, err)
	require.False(t, injected)

	f.gerManager.EXPECT().GlobalExitRootMap(mock.Anything, [32]byte(v2TestGER)).Return(big.NewInt(3), nil).Once()
	injected, err = sender.IsGERInjected(v2TestGER)
	require.NoError(t, err)
	require.True(t, injected)

	f.gerManager.EXPECT().GlobalExitRootMap(mock.Anything, [32]byte(v2TestGER)).Return(nil, errRPC).Once()
	_, err = sender.IsGERInjected(v2TestGER)
	require.ErrorIs(t, err, errRPC)
}

func TestCommitteeV2Sender_ProcessGER_AlreadyProcessed(t *testing.T) {
	t.Run("registered on the L2 GER manager", func(t *testing.T) {
		f := newV2Fixture(t)
		sender := f.newSender(defaultV2Config())
		f.gerManager.EXPECT().GlobalExitRootMap(mock.Anything, [32]byte(v2TestGER)).Return(big.NewInt(7), nil).Once()

		require.NoError(t, sender.ProcessGER(context.Background(), v2TestGER)) // no signatures are requested
	})

	t.Run("processed by the committee, and removed from the L2 GER manager afterwards", func(t *testing.T) {
		f := newV2Fixture(t)
		sender := f.newSender(defaultV2Config())
		f.gerManager.EXPECT().GlobalExitRootMap(mock.Anything, [32]byte(v2TestGER)).Return(big.NewInt(0), nil).Once()
		f.committee.EXPECT().InjectedGERs(mock.Anything, [32]byte(v2TestGER)).Return(true, nil).Once()

		require.NoError(t, sender.ProcessGER(context.Background(), v2TestGER))
	})

	t.Run("errors checking it", func(t *testing.T) {
		f := newV2Fixture(t)
		sender := f.newSender(defaultV2Config())
		f.gerManager.EXPECT().GlobalExitRootMap(mock.Anything, [32]byte(v2TestGER)).Return(nil, errRPC).Once()
		require.ErrorIs(t, sender.ProcessGER(context.Background(), v2TestGER), errRPC)

		f.gerManager.EXPECT().GlobalExitRootMap(mock.Anything, [32]byte(v2TestGER)).Return(big.NewInt(0), nil).Once()
		f.committee.EXPECT().InjectedGERs(mock.Anything, [32]byte(v2TestGER)).Return(false, errRPC).Once()
		require.ErrorIs(t, sender.ProcessGER(context.Background(), v2TestGER), errRPC)
	})
}

func TestCommitteeV2Sender_ProcessGER_Injects(t *testing.T) {
	f := newV2Fixture(t)
	sender := f.newSender(defaultV2Config())
	result := twoSignatures()
	validators := []gervalidator.Validator{
		{Address: validatorA, URL: "http://a"}, {Address: validatorB, URL: "http://b"}, {Address: validatorC, URL: "http://c"},
	}

	f.expectNotProcessed(v2TestGER)
	f.expectCommitteeState(2, validatorInfos("http://a", "http://b", "http://c"))
	f.collector.EXPECT().Collect(mock.Anything, v2TestGER, validators, 2).Return(result, nil).Once()
	f.committee.EXPECT().VerifyGER(mock.Anything, [32]byte(v2TestGER), result.Signatures).Return(true, nil).Once()
	f.expectInjectTx(v2TestGER, result.Signatures, injectTxID, ethtxtypes.MonitoredTxStatusMined)

	require.NoError(t, sender.ProcessGER(context.Background(), v2TestGER))
}

func TestCommitteeV2Sender_ProcessGER_AppliesURLOverrides(t *testing.T) {
	f := newV2Fixture(t)
	cfg := defaultV2Config()
	cfg.ValidatorURLOverrides = map[common.Address]string{
		validatorB: "https://override-b", // replaces the registered one
		validatorC: "https://override-c", // sets the missing one
	}
	f.expectValidation(2, validatorInfos("http://a", "http://b", ""))
	sender, err := NewCommitteeV2Sender(context.Background(), log.GetDefaultLogger(), evmTestConfig(), cfg,
		committeeAddr, f.gerManager, f.committee, f.collector, f.ethTxMan)
	require.NoError(t, err)

	result := twoSignatures()
	f.expectNotProcessed(v2TestGER)
	f.expectCommitteeState(2, validatorInfos("http://a", "http://b", ""))
	f.collector.EXPECT().Collect(mock.Anything, v2TestGER, []gervalidator.Validator{
		{Address: validatorA, URL: "http://a"},
		{Address: validatorB, URL: "https://override-b"},
		{Address: validatorC, URL: "https://override-c"},
	}, 2).Return(result, nil).Once()
	f.committee.EXPECT().VerifyGER(mock.Anything, [32]byte(v2TestGER), result.Signatures).Return(true, nil).Once()
	f.expectInjectTx(v2TestGER, result.Signatures, injectTxID, ethtxtypes.MonitoredTxStatusMined)

	require.NoError(t, sender.ProcessGER(context.Background(), v2TestGER))
}

func TestCommitteeV2Sender_ProcessGER_VerifyReverts(t *testing.T) {
	t.Run("already injected: nothing to send", func(t *testing.T) {
		f := newV2Fixture(t)
		sender := f.newSender(defaultV2Config())
		result := twoSignatures()
		f.expectNotProcessed(v2TestGER)
		f.expectCommitteeState(2, validatorInfos("http://a", "http://b", "http://c"))
		f.collector.EXPECT().Collect(mock.Anything, v2TestGER, mock.Anything, 2).Return(result, nil).Once()
		f.committee.EXPECT().VerifyGER(mock.Anything, [32]byte(v2TestGER), result.Signatures).
			Return(false, revertWith(t, "GERAlreadyInjected")).Once()

		require.NoError(t, sender.ProcessGER(context.Background(), v2TestGER)) // no transaction is added
	})

	for _, errorName := range []string{"InvalidSignaturesLength", "ValidatorDoesNotExist", "ValidatorsNotOrdered"} {
		t.Run("committee changed ("+errorName+"): the signatures are collected again", func(t *testing.T) {
			f := newV2Fixture(t)
			sender := f.newSender(defaultV2Config())
			result := twoSignatures()

			// First attempt: the threshold changed from 2 to 3 after it was read
			f.expectNotProcessed(v2TestGER)
			f.expectCommitteeState(2, validatorInfos("http://a", "http://b", "http://c"))
			f.collector.EXPECT().Collect(mock.Anything, v2TestGER, mock.Anything, 2).Return(result, nil).Once()
			f.committee.EXPECT().VerifyGER(mock.Anything, [32]byte(v2TestGER), result.Signatures).
				Return(false, revertWith(t, errorName)).Once()
			// Second attempt, with the new committee
			f.expectNotProcessed(v2TestGER)
			f.expectCommitteeState(3, validatorInfos("http://a", "http://b", "http://c"))
			threeSignatures := &gervalidator.Result{
				Signatures: append(append([]byte(nil), result.Signatures...), make([]byte, gervalidator.SignatureLength)...),
				Signers:    []common.Address{validatorA, validatorB, validatorC},
			}
			f.collector.EXPECT().Collect(mock.Anything, v2TestGER, mock.Anything, 3).Return(threeSignatures, nil).Once()
			f.committee.EXPECT().VerifyGER(mock.Anything, [32]byte(v2TestGER), threeSignatures.Signatures).Return(true, nil).Once()
			f.expectInjectTx(v2TestGER, threeSignatures.Signatures, injectTxID, ethtxtypes.MonitoredTxStatusMined)

			require.NoError(t, sender.ProcessGER(context.Background(), v2TestGER))
		})
	}

	t.Run("a committee that keeps changing exhausts the attempts", func(t *testing.T) {
		f := newV2Fixture(t)
		cfg := defaultV2Config()
		cfg.MaxInjectAttempts = 2
		sender := f.newSender(cfg)
		result := twoSignatures()
		for range 2 {
			f.expectNotProcessed(v2TestGER)
			f.expectCommitteeState(2, validatorInfos("http://a", "http://b", "http://c"))
			f.collector.EXPECT().Collect(mock.Anything, v2TestGER, mock.Anything, 2).Return(result, nil).Once()
			f.committee.EXPECT().VerifyGER(mock.Anything, [32]byte(v2TestGER), result.Signatures).
				Return(false, revertWith(t, "InvalidSignaturesLength")).Once()
		}

		err := sender.ProcessGER(context.Background(), v2TestGER)
		require.ErrorIs(t, err, ErrInjectAttemptsExhausted)
		require.ErrorContains(t, err, "2 attempts")
	})

	t.Run("another revert is not retried", func(t *testing.T) {
		f := newV2Fixture(t)
		sender := f.newSender(defaultV2Config())
		result := twoSignatures()
		f.expectNotProcessed(v2TestGER)
		f.expectCommitteeState(2, validatorInfos("http://a", "http://b", "http://c"))
		f.collector.EXPECT().Collect(mock.Anything, v2TestGER, mock.Anything, 2).Return(result, nil).Once()
		f.committee.EXPECT().VerifyGER(mock.Anything, [32]byte(v2TestGER), result.Signatures).
			Return(false, revertWith(t, "GERCannotBeZero")).Once()

		err := sender.ProcessGER(context.Background(), v2TestGER)
		require.ErrorContains(t, err, "verifyGER reverted")
		require.NotErrorIs(t, err, ErrInjectAttemptsExhausted)
	})

	t.Run("an error without revert data is not retried", func(t *testing.T) {
		f := newV2Fixture(t)
		sender := f.newSender(defaultV2Config())
		result := twoSignatures()
		f.expectNotProcessed(v2TestGER)
		f.expectCommitteeState(2, validatorInfos("http://a", "http://b", "http://c"))
		f.collector.EXPECT().Collect(mock.Anything, v2TestGER, mock.Anything, 2).Return(result, nil).Once()
		f.committee.EXPECT().VerifyGER(mock.Anything, [32]byte(v2TestGER), result.Signatures).Return(false, errRPC).Once()

		require.ErrorIs(t, sender.ProcessGER(context.Background(), v2TestGER), errRPC)
	})

	t.Run("the contract says the signatures are not valid", func(t *testing.T) {
		f := newV2Fixture(t)
		sender := f.newSender(defaultV2Config())
		result := twoSignatures()
		f.expectNotProcessed(v2TestGER)
		f.expectCommitteeState(2, validatorInfos("http://a", "http://b", "http://c"))
		f.collector.EXPECT().Collect(mock.Anything, v2TestGER, mock.Anything, 2).Return(result, nil).Once()
		f.committee.EXPECT().VerifyGER(mock.Anything, [32]byte(v2TestGER), result.Signatures).Return(false, nil).Once()

		require.ErrorContains(t, sender.ProcessGER(context.Background(), v2TestGER), "didn't validate the signatures")
	})
}

func TestCommitteeV2Sender_ProcessGER_CollectionErrors(t *testing.T) {
	t.Run("validators not ready: the GER stays pending and waiting is not an attempt", func(t *testing.T) {
		f := newV2Fixture(t)
		cfg := defaultV2Config()
		cfg.MaxInjectAttempts = 1 // only one failed attempt is allowed, but waiting doesn't count
		sender := f.newSender(cfg)
		result := twoSignatures()

		for range 3 {
			f.expectNotProcessed(v2TestGER)
			f.expectCommitteeState(2, validatorInfos("http://a", "http://b", "http://c"))
			f.collector.EXPECT().Collect(mock.Anything, v2TestGER, mock.Anything, 2).Return(nil, notReachedError(true)).Once()
		}
		f.expectNotProcessed(v2TestGER)
		f.expectCommitteeState(2, validatorInfos("http://a", "http://b", "http://c"))
		f.collector.EXPECT().Collect(mock.Anything, v2TestGER, mock.Anything, 2).Return(result, nil).Once()
		f.committee.EXPECT().VerifyGER(mock.Anything, [32]byte(v2TestGER), result.Signatures).Return(true, nil).Once()
		f.expectInjectTx(v2TestGER, result.Signatures, injectTxID, ethtxtypes.MonitoredTxStatusMined)

		start := time.Now()
		require.NoError(t, sender.ProcessGER(context.Background(), v2TestGER))
		require.GreaterOrEqual(t, time.Since(start), 3*v2RoundWait, "it waits CollectRetryInterval between rounds")
	})

	t.Run("the GER is injected by someone else while it is pending", func(t *testing.T) {
		f := newV2Fixture(t)
		sender := f.newSender(defaultV2Config())

		f.expectNotProcessed(v2TestGER)
		f.expectCommitteeState(2, validatorInfos("http://a", "http://b", "http://c"))
		f.collector.EXPECT().Collect(mock.Anything, v2TestGER, mock.Anything, 2).Return(nil, notReachedError(true)).Once()
		f.gerManager.EXPECT().GlobalExitRootMap(mock.Anything, [32]byte(v2TestGER)).Return(big.NewInt(5), nil).Once()

		require.NoError(t, sender.ProcessGER(context.Background(), v2TestGER))
	})

	t.Run("too many validators reject the GER: it is dropped", func(t *testing.T) {
		f := newV2Fixture(t)
		sender := f.newSender(defaultV2Config())
		f.expectNotProcessed(v2TestGER)
		f.expectCommitteeState(2, validatorInfos("http://a", "http://b", "http://c"))
		f.collector.EXPECT().Collect(mock.Anything, v2TestGER, mock.Anything, 2).Return(nil, notReachedError(false)).Once()

		err := sender.ProcessGER(context.Background(), v2TestGER)
		require.ErrorIs(t, err, gervalidator.ErrThresholdNotReached)
		require.ErrorContains(t, err, "dropping it")
	})

	t.Run("another collection error", func(t *testing.T) {
		f := newV2Fixture(t)
		sender := f.newSender(defaultV2Config())
		f.expectNotProcessed(v2TestGER)
		f.expectCommitteeState(2, validatorInfos("http://a", "http://b", "http://c"))
		f.collector.EXPECT().Collect(mock.Anything, v2TestGER, mock.Anything, 2).Return(nil, errRPC).Once()

		require.ErrorIs(t, sender.ProcessGER(context.Background(), v2TestGER), errRPC)
	})

	t.Run("the committee can't be read", func(t *testing.T) {
		f := newV2Fixture(t)
		sender := f.newSender(defaultV2Config())
		f.expectNotProcessed(v2TestGER)
		f.committee.EXPECT().Threshold(mock.Anything).Return(nil, errRPC).Once()
		require.ErrorIs(t, sender.ProcessGER(context.Background(), v2TestGER), errRPC)

		f.expectNotProcessed(v2TestGER)
		f.committee.EXPECT().Threshold(mock.Anything).Return(big.NewInt(2), nil).Once()
		f.committee.EXPECT().GetValidatorsInfo(mock.Anything).Return(nil, errRPC).Once()
		require.ErrorIs(t, sender.ProcessGER(context.Background(), v2TestGER), errRPC)
	})

	t.Run("a threshold that doesn't fit the validators after a change", func(t *testing.T) {
		f := newV2Fixture(t)
		sender := f.newSender(defaultV2Config())
		f.expectNotProcessed(v2TestGER)
		f.expectCommitteeState(5, validatorInfos("http://a", "http://b", "http://c"))

		require.ErrorContains(t, sender.ProcessGER(context.Background(), v2TestGER), "greater than its validators")
	})
}

func TestCommitteeV2Sender_ProcessGER_TransactionFailures(t *testing.T) {
	t.Run("the transaction fails: collects again and succeeds", func(t *testing.T) {
		f := newV2Fixture(t)
		sender := f.newSender(defaultV2Config())
		result := twoSignatures()

		f.expectNotProcessed(v2TestGER)
		f.expectCommitteeState(2, validatorInfos("http://a", "http://b", "http://c"))
		f.collector.EXPECT().Collect(mock.Anything, v2TestGER, mock.Anything, 2).Return(result, nil).Once()
		f.committee.EXPECT().VerifyGER(mock.Anything, [32]byte(v2TestGER), result.Signatures).Return(true, nil).Once()
		f.expectInjectTx(v2TestGER, result.Signatures, injectTxID, ethtxtypes.MonitoredTxStatusFailed)

		f.expectNotProcessed(v2TestGER)
		f.expectCommitteeState(2, validatorInfos("http://a", "http://b", "http://c"))
		f.collector.EXPECT().Collect(mock.Anything, v2TestGER, mock.Anything, 2).Return(result, nil).Once()
		f.committee.EXPECT().VerifyGER(mock.Anything, [32]byte(v2TestGER), result.Signatures).Return(true, nil).Once()
		f.expectInjectTx(v2TestGER, result.Signatures, injectTxID2, ethtxtypes.MonitoredTxStatusMined)

		require.NoError(t, sender.ProcessGER(context.Background(), v2TestGER))
	})

	t.Run("the transaction fails but the GER ended up injected", func(t *testing.T) {
		f := newV2Fixture(t)
		sender := f.newSender(defaultV2Config())
		result := twoSignatures()

		f.expectNotProcessed(v2TestGER)
		f.expectCommitteeState(2, validatorInfos("http://a", "http://b", "http://c"))
		f.collector.EXPECT().Collect(mock.Anything, v2TestGER, mock.Anything, 2).Return(result, nil).Once()
		f.committee.EXPECT().VerifyGER(mock.Anything, [32]byte(v2TestGER), result.Signatures).Return(true, nil).Once()
		f.expectInjectTx(v2TestGER, result.Signatures, injectTxID, ethtxtypes.MonitoredTxStatusFailed)
		// It was injected anyway (for example by a previous transaction that was resent)
		f.gerManager.EXPECT().GlobalExitRootMap(mock.Anything, [32]byte(v2TestGER)).Return(big.NewInt(9), nil).Once()

		require.NoError(t, sender.ProcessGER(context.Background(), v2TestGER))
	})

	t.Run("the transaction can't be added", func(t *testing.T) {
		f := newV2Fixture(t)
		cfg := defaultV2Config()
		cfg.MaxInjectAttempts = 1
		sender := f.newSender(cfg)
		result := twoSignatures()

		f.expectNotProcessed(v2TestGER)
		f.expectCommitteeState(2, validatorInfos("http://a", "http://b", "http://c"))
		f.collector.EXPECT().Collect(mock.Anything, v2TestGER, mock.Anything, 2).Return(result, nil).Once()
		f.committee.EXPECT().VerifyGER(mock.Anything, [32]byte(v2TestGER), result.Signatures).Return(true, nil).Once()
		f.ethTxMan.EXPECT().Add(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(common.Hash{}, errRPC).Once()

		err := sender.ProcessGER(context.Background(), v2TestGER)
		require.ErrorIs(t, err, ErrInjectAttemptsExhausted)
		require.ErrorIs(t, err, errRPC)
	})

	t.Run("the transaction already exists in the monitoring DB", func(t *testing.T) {
		f := newV2Fixture(t)
		sender := f.newSender(defaultV2Config())
		result := twoSignatures()

		f.expectNotProcessed(v2TestGER)
		f.expectCommitteeState(2, validatorInfos("http://a", "http://b", "http://c"))
		f.collector.EXPECT().Collect(mock.Anything, v2TestGER, mock.Anything, 2).Return(result, nil).Once()
		f.committee.EXPECT().VerifyGER(mock.Anything, [32]byte(v2TestGER), result.Signatures).Return(true, nil).Once()
		f.ethTxMan.EXPECT().Add(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(injectTxID, ethtxmanager.ErrAlreadyExists).Once()
		f.ethTxMan.EXPECT().Result(mock.Anything, injectTxID).
			Return(ethtxtypes.MonitoredTxResult{ID: injectTxID, Status: ethtxtypes.MonitoredTxStatusMined,
				MinedAtBlockNumber: big.NewInt(1)}, nil).Once()

		require.NoError(t, sender.ProcessGER(context.Background(), v2TestGER))
	})
}

func TestCommitteeV2Sender_ProcessGER_Cancellation(t *testing.T) {
	t.Run("cancelled while the GER is pending", func(t *testing.T) {
		f := newV2Fixture(t)
		cfg := defaultV2Config()
		cfg.CollectRetryInterval = time.Hour // it would wait forever
		sender := f.newSender(cfg)
		ctx, cancel := context.WithCancel(context.Background())

		f.expectNotProcessed(v2TestGER)
		f.expectCommitteeState(2, validatorInfos("http://a", "http://b", "http://c"))
		f.collector.EXPECT().Collect(mock.Anything, v2TestGER, mock.Anything, 2).
			RunAndReturn(func(context.Context, common.Hash, []gervalidator.Validator, int) (*gervalidator.Result, error) {
				cancel()
				return nil, notReachedError(true)
			}).Once()

		err := sender.ProcessGER(ctx, v2TestGER)
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("cancelled while the transaction is monitored: not reported as injected", func(t *testing.T) {
		f := newV2Fixture(t)
		sender := f.newSender(defaultV2Config())
		result := twoSignatures()
		ctx, cancel := context.WithCancel(context.Background())

		f.expectNotProcessed(v2TestGER)
		f.expectCommitteeState(2, validatorInfos("http://a", "http://b", "http://c"))
		f.collector.EXPECT().Collect(mock.Anything, v2TestGER, mock.Anything, 2).Return(result, nil).Once()
		f.committee.EXPECT().VerifyGER(mock.Anything, [32]byte(v2TestGER), result.Signatures).Return(true, nil).Once()
		f.ethTxMan.EXPECT().Add(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			RunAndReturn(func(context.Context, *common.Address, *big.Int, []byte, uint64, *gethtypes.BlobTxSidecar) (common.Hash, error) {
				cancel()
				return injectTxID, nil
			}).Once()

		require.ErrorIs(t, sender.ProcessGER(ctx, v2TestGER), context.Canceled)
	})
}

func TestCommitteeV2Sender_ProcessGER_PendingTimeout(t *testing.T) {
	f := newV2Fixture(t)
	cfg := defaultV2Config()
	cfg.PendingGERTimeout = 60 * time.Millisecond
	sender := f.newSender(cfg)

	// The validators are never ready
	f.gerManager.EXPECT().GlobalExitRootMap(mock.Anything, [32]byte(v2TestGER)).Return(big.NewInt(0), nil)
	f.committee.EXPECT().InjectedGERs(mock.Anything, [32]byte(v2TestGER)).Return(false, nil)
	f.committee.EXPECT().Threshold(mock.Anything).Return(big.NewInt(2), nil)
	f.committee.EXPECT().GetValidatorsInfo(mock.Anything).Return(validatorInfos("http://a", "http://b", "http://c"), nil)
	f.collector.EXPECT().Collect(mock.Anything, v2TestGER, mock.Anything, 2).Return(nil, notReachedError(true))

	start := time.Now()
	err := sender.ProcessGER(context.Background(), v2TestGER)
	require.ErrorIs(t, err, ErrGERPendingTimeout)
	require.Less(t, time.Since(start), v2TestTimeout)
}

// While a GER is being processed, the new ones are skipped
func TestCommitteeV2Sender_ProcessGER_OneAtATime(t *testing.T) {
	f := newV2Fixture(t)
	sender := f.newSender(defaultV2Config())
	result := twoSignatures()

	collecting := make(chan struct{})
	release := make(chan struct{})
	f.expectNotProcessed(v2TestGER)
	f.expectCommitteeState(2, validatorInfos("http://a", "http://b", "http://c"))
	f.collector.EXPECT().Collect(mock.Anything, v2TestGER, mock.Anything, 2).
		RunAndReturn(func(context.Context, common.Hash, []gervalidator.Validator, int) (*gervalidator.Result, error) {
			close(collecting)
			<-release
			return result, nil
		}).Once()
	f.committee.EXPECT().VerifyGER(mock.Anything, [32]byte(v2TestGER), result.Signatures).Return(true, nil).Once()
	f.expectInjectTx(v2TestGER, result.Signatures, injectTxID, ethtxtypes.MonitoredTxStatusMined)

	var wg sync.WaitGroup
	var firstErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		firstErr = sender.ProcessGER(context.Background(), v2TestGER)
	}()

	select {
	case <-collecting:
	case <-time.After(v2TestTimeout):
		require.Fail(t, "the first GER didn't start being processed")
	}

	// A newer GER arrives while the first one is pending: it is skipped, without touching the contracts
	// (the mocks have no expectation for it, so any call would fail the test)
	newerGER := common.HexToHash("0x99")
	for range 5 {
		require.NoError(t, sender.ProcessGER(context.Background(), newerGER))
	}

	close(release)
	wg.Wait()
	require.NoError(t, firstErr)

	// Once the first one is done, the next GER is processed
	f.gerManager.EXPECT().GlobalExitRootMap(mock.Anything, [32]byte(newerGER)).Return(big.NewInt(1), nil).Once()
	require.NoError(t, sender.ProcessGER(context.Background(), newerGER))
}

func TestRevertSelector(t *testing.T) {
	selector, ok := revertSelector(revertDataError{data: "0x7bde2647"})
	require.True(t, ok)
	require.Equal(t, [4]byte{0x7b, 0xde, 0x26, 0x47}, selector)

	// With arguments after the selector, and wrapped
	selector, ok = revertSelector(fmt.Errorf("call failed: %w", revertDataError{data: "0x7bde2647" + "00000000000000000000000000000000000000000000000000000000000000ff"}))
	require.True(t, ok)
	require.Equal(t, [4]byte{0x7b, 0xde, 0x26, 0x47}, selector)

	for name, err := range map[string]error{
		"no data":       errRPC,
		"short data":    revertDataError{data: "0x7bde26"},
		"not hex":       revertDataError{data: "0xzz"},
		"empty":         revertDataError{data: ""},
		"data not text": dataNotTextError{},
	} {
		_, ok := revertSelector(err)
		require.False(t, ok, name)
	}
}

type dataNotTextError struct{}

func (dataNotTextError) Error() string  { return "execution reverted" }
func (dataNotTextError) ErrorData() any { return 42 }
