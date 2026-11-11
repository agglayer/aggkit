package aggoracle

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/agglayer/aggkit/aggoracle/chaingersender"
	"github.com/agglayer/aggkit/aggoracle/mocks"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/log"
	typesmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var (
	factoryGERAddr   = common.HexToAddress("0x00000000000000000000000000000000000000a1")
	factoryCommittee = common.HexToAddress("0x00000000000000000000000000000000000000c1")
	factoryEOA       = common.HexToAddress("0x00000000000000000000000000000000000000e1")
	factoryProposer  = common.HexToAddress("0x00000000000000000000000000000000000000b1")
	errExecReverted  = errors.New("execution reverted")
)

// l2Fixture describes what the L2 contracts answer, as test data, and builds the generated mock of the L2 client
type l2Fixture struct {
	t         *testing.T
	code      map[common.Address][]byte
	responses map[factoryCallKey][]byte
}

type factoryCallKey struct {
	to       common.Address
	selector [4]byte
}

func newL2Fixture(t *testing.T) *l2Fixture {
	t.Helper()
	return &l2Fixture{t: t, code: map[common.Address][]byte{}, responses: map[factoryCallKey][]byte{}}
}

func (f *l2Fixture) on(to common.Address, signature string, out []byte) *l2Fixture {
	var selector [4]byte
	copy(selector[:], crypto.Keccak256([]byte(signature))[:4])
	f.responses[factoryCallKey{to: to, selector: selector}] = out
	return f
}

func word(addr common.Address) []byte { return common.LeftPadBytes(addr.Bytes(), 32) }

func (f *l2Fixture) stringOut(str string) []byte {
	f.t.Helper()
	stringType, err := abi.NewType("string", "", nil)
	require.NoError(f.t, err)
	out, err := abi.Arguments{{Type: stringType}}.Pack(str)
	require.NoError(f.t, err)
	return out
}

// withUpdater sets the GER updater of the L2 GER manager
func (f *l2Fixture) withUpdater(updater common.Address) *l2Fixture {
	return f.on(factoryGERAddr, "globalExitRootUpdater()", word(updater))
}

func (f *l2Fixture) asCommitteeV2(addr common.Address) *l2Fixture {
	f.code[addr] = []byte{0x60, 0x80}
	return f.on(addr, "version()", f.stringOut("v2.0.0")).
		on(addr, "agglayerGERL2()", word(factoryGERAddr)).
		on(addr, "threshold()", common.LeftPadBytes([]byte{2}, 32)).
		on(addr, "proposer()", word(factoryProposer))
}

func (f *l2Fixture) asCommitteeV1(addr common.Address) *l2Fixture {
	f.code[addr] = []byte{0x60, 0x80}
	return f.on(addr, "VERSION()", f.stringOut("v1.0.0")).
		on(addr, "globalExitRootManagerL2Sovereign()", word(factoryGERAddr)).
		on(addr, "quorum()", common.LeftPadBytes([]byte{2}, 32))
}

// mock builds the L2 client. Calls with no answer revert, and addresses with no code are EOAs
func (f *l2Fixture) mock() *typesmocks.BaseEthereumClienter {
	f.t.Helper()
	client := typesmocks.NewBaseEthereumClienter(f.t)
	for key, out := range f.responses {
		client.EXPECT().CallContract(mock.Anything, mock.MatchedBy(func(msg ethereum.CallMsg) bool {
			return msg.To != nil && *msg.To == key.to && len(msg.Data) >= 4 && bytes.Equal(msg.Data[:4], key.selector[:])
		}), mock.Anything).Return(out, nil).Maybe()
	}
	client.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).Return(nil, errExecReverted).Maybe()
	for addr, code := range f.code {
		client.EXPECT().CodeAt(mock.Anything, addr, mock.Anything).Return(code, nil).Maybe()
	}
	client.EXPECT().CodeAt(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	return client
}

func factoryConfig(mode Mode, committee common.Address) Config {
	return Config{
		Mode: mode,
		EVMSender: chaingersender.EVMConfig{
			GlobalExitRootL2Addr:   factoryGERAddr,
			AggOracleCommitteeAddr: committee,
			WaitPeriodMonitorTx:    cfgtypes.NewDuration(time.Millisecond),
		},
		CommitteeV2: CommitteeV2Config{
			RequestTimeout:       cfgtypes.NewDuration(time.Second),
			CollectRetryInterval: cfgtypes.NewDuration(time.Second),
			MaxInjectAttempts:    3,
		},
	}
}

func TestResolveSubmission_ExplicitModes(t *testing.T) {
	tests := []struct {
		name          string
		cfg           Config
		fixture       func(f *l2Fixture)
		expectedMode  chaingersender.GERMode
		expectedAddr  common.Address
		expectedError string
	}{
		{
			name:         "direct is used as configured, without reading the L2",
			cfg:          factoryConfig(ModeDirect, common.Address{}),
			fixture:      func(f *l2Fixture) {},
			expectedMode: chaingersender.DirectInjectionMode,
		},
		{
			name:         "legacy is used as configured, without reading the L2",
			cfg:          factoryConfig(ModeLegacy, factoryCommittee),
			fixture:      func(f *l2Fixture) {},
			expectedMode: chaingersender.AggOracleCommitteeMode,
			expectedAddr: factoryCommittee,
		},
		{
			name:          "legacy requires the address",
			cfg:           factoryConfig(ModeLegacy, common.Address{}),
			fixture:       func(f *l2Fixture) {},
			expectedError: "AggOracleCommitteeAddr must be set",
		},
		{
			name:         "the deprecated flag with the address selects legacy, without reading the L2",
			cfg:          func() Config { c := factoryConfig("", factoryCommittee); c.EnableAggOracleCommittee = true; return c }(),
			fixture:      func(f *l2Fixture) {},
			expectedMode: chaingersender.AggOracleCommitteeMode,
			expectedAddr: factoryCommittee,
		},
		{
			name:         "committee without address takes the GER updater",
			cfg:          factoryConfig(ModeCommittee, common.Address{}),
			fixture:      func(f *l2Fixture) { f.withUpdater(factoryCommittee) },
			expectedMode: chaingersender.AggOracleCommitteeV2Mode,
			expectedAddr: factoryCommittee,
		},
		{
			name:         "committee with the address of the GER updater",
			cfg:          factoryConfig(ModeCommittee, factoryCommittee),
			fixture:      func(f *l2Fixture) { f.withUpdater(factoryCommittee) },
			expectedMode: chaingersender.AggOracleCommitteeV2Mode,
			expectedAddr: factoryCommittee,
		},
		{
			name:          "committee with an address that is not the GER updater",
			cfg:           factoryConfig(ModeCommittee, factoryCommittee),
			fixture:       func(f *l2Fixture) { f.withUpdater(factoryEOA) },
			expectedError: "configured committee is " + factoryCommittee.Hex(),
		},
		{
			name:          "committee but the GER updater is the zero address",
			cfg:           factoryConfig(ModeCommittee, common.Address{}),
			fixture:       func(f *l2Fixture) { f.withUpdater(common.Address{}) },
			expectedError: "there is no committee",
		},
		{
			name:          "committee when the GER updater can't be read",
			cfg:           factoryConfig(ModeCommittee, common.Address{}),
			fixture:       func(f *l2Fixture) {},
			expectedError: "failed to read globalExitRootUpdater",
		},
		{
			name: "contradictory configuration",
			cfg: func() Config {
				c := factoryConfig(ModeDirect, factoryCommittee)
				c.EnableAggOracleCommittee = true
				return c
			}(),
			fixture:       func(f *l2Fixture) {},
			expectedError: "contradicts AggOracle.Mode",
		},
		{
			name:          "invalid mode",
			cfg:           factoryConfig("bogus", common.Address{}),
			fixture:       func(f *l2Fixture) {},
			expectedError: "invalid AggOracle.Mode",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newL2Fixture(t)
			tc.fixture(fixture)
			// The mock of the L2 client has no expectation for the calls that must not happen
			// when the mode is explicit: the catch-all makes them revert, and the tests assert the result
			submission, err := resolveSubmission(context.Background(), tc.cfg, fixture.mock())
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
				require.Nil(t, submission)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.expectedMode, submission.mode)
			require.Equal(t, tc.expectedAddr, submission.committee)
			require.NotEmpty(t, submission.reason)
		})
	}
}

func TestResolveSubmission_Auto(t *testing.T) {
	tests := []struct {
		name          string
		fixture       func(f *l2Fixture)
		committeeCfg  common.Address
		expectedMode  chaingersender.GERMode
		expectedAddr  common.Address
		expectedError string
	}{
		{
			name:         "EOA updater: direct",
			fixture:      func(f *l2Fixture) { f.withUpdater(factoryEOA) },
			expectedMode: chaingersender.DirectInjectionMode,
		},
		{
			name:         "zero updater: direct",
			fixture:      func(f *l2Fixture) { f.withUpdater(common.Address{}) },
			expectedMode: chaingersender.DirectInjectionMode,
		},
		{
			name:         "committee",
			fixture:      func(f *l2Fixture) { f.withUpdater(factoryCommittee).asCommitteeV2(factoryCommittee) },
			expectedMode: chaingersender.AggOracleCommitteeV2Mode,
			expectedAddr: factoryCommittee,
		},
		{
			name:         "legacy, with the committee address that was configured",
			fixture:      func(f *l2Fixture) { f.withUpdater(factoryCommittee).asCommitteeV1(factoryCommittee) },
			committeeCfg: factoryCommittee,
			expectedMode: chaingersender.AggOracleCommitteeMode,
			expectedAddr: factoryCommittee,
		},
		{
			name: "a contract that is not a committee",
			fixture: func(f *l2Fixture) {
				f.withUpdater(factoryCommittee)
				f.code[factoryCommittee] = []byte{0x60, 0x80}
			},
			expectedError: "the mode can't be detected",
		},
		{
			name:          "the configured committee is not the GER updater",
			fixture:       func(f *l2Fixture) { f.withUpdater(factoryEOA) },
			committeeCfg:  factoryCommittee,
			expectedError: "configured committee is",
		},
		{
			name:          "the L2 can't be read",
			fixture:       func(f *l2Fixture) {},
			expectedError: "the mode can't be detected",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newL2Fixture(t)
			tc.fixture(fixture)
			submission, err := resolveSubmission(context.Background(), factoryConfig(ModeAuto, tc.committeeCfg), fixture.mock())
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.expectedMode, submission.mode)
			require.Equal(t, tc.expectedAddr, submission.committee)
		})
	}
}

func TestNewEVMChainSender(t *testing.T) {
	t.Run("auto with an EOA creates the direct sender", func(t *testing.T) {
		client := newL2Fixture(t).withUpdater(factoryProposer).mock()
		gerManager := mocks.NewL2GERManagerContract(t)
		ethTxMan := mocks.NewEthTxManager(t)
		// The direct sender checks that the account of the EthTxManager is the GER updater
		ethTxMan.EXPECT().From().Return(factoryProposer)
		gerManager.EXPECT().GlobalExitRootUpdater(mock.Anything).Return(factoryProposer, nil)

		sender, err := NewEVMChainSender(context.Background(), log.GetDefaultLogger(),
			factoryConfig(ModeAuto, common.Address{}), client, gerManager, ethTxMan)
		require.NoError(t, err)
		require.IsType(t, &chaingersender.EVMChainGERSender{}, sender)

		// ChainSender: IsGERInjected goes to the L2 GER manager
		gerManager.EXPECT().GlobalExitRootMap(mock.Anything, [32]byte{1}).Return(big.NewInt(1), nil).Once()
		injected, err := sender.IsGERInjected(common.Hash{1})
		require.NoError(t, err)
		require.True(t, injected)
	})

	t.Run("direct mode", func(t *testing.T) {
		gerManager := mocks.NewL2GERManagerContract(t)
		ethTxMan := mocks.NewEthTxManager(t)
		ethTxMan.EXPECT().From().Return(factoryProposer)
		gerManager.EXPECT().GlobalExitRootUpdater(mock.Anything).Return(factoryProposer, nil)

		sender, err := NewEVMChainSender(context.Background(), log.GetDefaultLogger(),
			factoryConfig(ModeDirect, common.Address{}), newL2Fixture(t).mock(), gerManager, ethTxMan)
		require.NoError(t, err)
		require.IsType(t, &chaingersender.EVMChainGERSender{}, sender)
	})

	t.Run("direct mode with an account that is not the GER updater", func(t *testing.T) {
		gerManager := mocks.NewL2GERManagerContract(t)
		ethTxMan := mocks.NewEthTxManager(t)
		ethTxMan.EXPECT().From().Return(factoryProposer)
		gerManager.EXPECT().GlobalExitRootUpdater(mock.Anything).Return(factoryEOA, nil)

		sender, err := NewEVMChainSender(context.Background(), log.GetDefaultLogger(),
			factoryConfig(ModeDirect, common.Address{}), newL2Fixture(t).mock(), gerManager, ethTxMan)
		require.ErrorContains(t, err, "invalid GER sender")
		require.Nil(t, sender)
	})

	t.Run("an invalid configuration fails before reading anything", func(t *testing.T) {
		cfg := factoryConfig(ModeDirect, factoryCommittee)
		cfg.EnableAggOracleCommittee = true

		sender, err := NewEVMChainSender(context.Background(), log.GetDefaultLogger(), cfg,
			typesmocks.NewBaseEthereumClienter(t), mocks.NewL2GERManagerContract(t), mocks.NewEthTxManager(t))
		require.ErrorContains(t, err, "contradicts AggOracle.Mode")
		require.Nil(t, sender)
	})

	t.Run("committee with an invalid TLS configuration", func(t *testing.T) {
		cfg := factoryConfig(ModeCommittee, common.Address{})
		cfg.CommitteeV2.TLS.CACertFile = "/does/not/exist.pem"
		client := newL2Fixture(t).withUpdater(factoryCommittee).mock()

		sender, err := NewEVMChainSender(context.Background(), log.GetDefaultLogger(), cfg, client,
			mocks.NewL2GERManagerContract(t), mocks.NewEthTxManager(t))
		require.ErrorContains(t, err, "failed to create the client of the GER validators")
		require.Nil(t, sender)
	})

	t.Run("auto that can't detect the mode", func(t *testing.T) {
		fixture := newL2Fixture(t).withUpdater(factoryCommittee)
		fixture.code[factoryCommittee] = []byte{0x60, 0x80}

		sender, err := NewEVMChainSender(context.Background(), log.GetDefaultLogger(),
			factoryConfig(ModeAuto, common.Address{}), fixture.mock(), mocks.NewL2GERManagerContract(t), mocks.NewEthTxManager(t))
		require.ErrorIs(t, err, chaingersender.ErrUnknownGERUpdaterContract)
		require.Nil(t, sender)
	})
}

func TestDeprecatedModeWarning(t *testing.T) {
	// Only the legacy mode is deprecated, whatever the way it was selected (configured, by the deprecated flag
	// or found by "auto"): the warning depends on the mode that was resolved
	require.Empty(t, deprecatedModeWarning(chaingersender.DirectInjectionMode))
	require.Empty(t, deprecatedModeWarning(chaingersender.AggOracleCommitteeV2Mode))

	warning := deprecatedModeWarning(chaingersender.AggOracleCommitteeMode)
	require.Contains(t, warning, "LEGACY")
	require.Contains(t, warning, "DEPRECATED")
	require.Contains(t, warning, "removed in the next one")
	// It tells what to migrate to
	require.Contains(t, warning, `AggOracle.Mode = "committee"`)
}

func TestDeprecatedConfigWarning(t *testing.T) {
	require.Empty(t, deprecatedConfigWarning(Config{}))
	require.Empty(t, deprecatedConfigWarning(Config{Mode: ModeLegacy}), "the mode has its own warning")

	warning := deprecatedConfigWarning(Config{EnableAggOracleCommittee: true})
	require.Contains(t, warning, "EnableAggOracleCommittee is deprecated")
	require.Contains(t, warning, "removed in the next release")
	require.Contains(t, warning, "AggOracle.Mode")
}
