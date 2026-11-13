package chaingersender

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"testing"

	typesmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var (
	gerL2Addr      = common.HexToAddress("0x00000000000000000000000000000000000000a1")
	otherGERL2Addr = common.HexToAddress("0x00000000000000000000000000000000000000a2")
	committeeAddr  = common.HexToAddress("0x00000000000000000000000000000000000000c1")
	eoaAddr        = common.HexToAddress("0x00000000000000000000000000000000000000e1")
	proposerAddr   = common.HexToAddress("0x00000000000000000000000000000000000000b1")
)

var errTransport = errors.New("dial tcp 127.0.0.1:8545: connect: connection refused")

// rpcRevertError is the error returned by the nodes for a reverted call
type rpcRevertError struct{}

func (rpcRevertError) Error() string  { return "execution reverted" }
func (rpcRevertError) ErrorCode() int { return revertErrorCode }

type callKey struct {
	to       common.Address
	selector [4]byte
}

type callResponse struct {
	out []byte
	err error
}

// committeeFixture describes, as test data, what the contracts of the L2 answer. mock() turns it into
// expectations on the generated mock of the Ethereum client. A call with no answer reverts, as a contract
// that doesn't implement the function does, and an address with no code is an EOA.
type committeeFixture struct {
	t         *testing.T
	code      map[common.Address][]byte
	responses map[callKey]callResponse
	codeErr   error
}

func newFixture(t *testing.T) *committeeFixture {
	t.Helper()
	return &committeeFixture{
		t:         t,
		code:      map[common.Address][]byte{},
		responses: map[callKey]callResponse{},
	}
}

func selectorOf(signature string) [4]byte {
	var sel [4]byte
	copy(sel[:], crypto.Keccak256([]byte(signature))[:4])
	return sel
}

func (f *committeeFixture) on(to common.Address, signature string, out []byte) *committeeFixture {
	f.responses[callKey{to: to, selector: selectorOf(signature)}] = callResponse{out: out}
	return f
}

func (f *committeeFixture) onError(to common.Address, signature string, err error) *committeeFixture {
	f.responses[callKey{to: to, selector: selectorOf(signature)}] = callResponse{err: err}
	return f
}

// matchCall matches the eth_call to a contract function
func matchCall(key callKey) interface{} {
	return mock.MatchedBy(func(msg ethereum.CallMsg) bool {
		return msg.To != nil && *msg.To == key.to && len(msg.Data) >= len(key.selector) &&
			bytes.Equal(msg.Data[:len(key.selector)], key.selector[:])
	})
}

// mock builds the mocked Ethereum client that answers as described by the fixture.
// The expectations are optional (Maybe): a detection stops at the first transport error.
func (f *committeeFixture) mock() *typesmocks.BaseEthereumClienter {
	f.t.Helper()
	client := typesmocks.NewBaseEthereumClienter(f.t)

	for key, resp := range f.responses {
		client.EXPECT().CallContract(mock.Anything, matchCall(key), mock.Anything).
			Return(resp.out, resp.err).Maybe()
	}
	// Registered after the specific answers (the first matching expectation wins)
	client.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).
		Return(nil, rpcRevertError{}).Maybe()

	if f.codeErr != nil {
		client.EXPECT().CodeAt(mock.Anything, mock.Anything, mock.Anything).Return(nil, f.codeErr).Maybe()
		return client
	}
	for addr, code := range f.code {
		client.EXPECT().CodeAt(mock.Anything, addr, mock.Anything).Return(code, nil).Maybe()
	}
	client.EXPECT().CodeAt(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Maybe()

	return client
}

func addressWord(addr common.Address) []byte {
	return common.LeftPadBytes(addr.Bytes(), wordSize)
}

func uintWord(n uint64) []byte {
	return common.LeftPadBytes(new(big.Int).SetUint64(n).Bytes(), wordSize)
}

func stringReturn(t *testing.T, str string) []byte {
	t.Helper()
	stringType, err := abi.NewType("string", "", nil)
	require.NoError(t, err)
	out, err := abi.Arguments{{Type: stringType}}.Pack(str)
	require.NoError(t, err)
	return out
}

// withUpdater makes the L2 GER manager report updater as globalExitRootUpdater
func (f *committeeFixture) withUpdater(updater common.Address) *committeeFixture {
	return f.on(gerL2Addr, sigGlobalExitRootUpdater, addressWord(updater))
}

// asCommitteeV2 makes the contract at addr look like an AggOracleCommittee v2 of gerManager
func (f *committeeFixture) asCommitteeV2(t *testing.T, addr, gerManager common.Address, version string) *committeeFixture {
	t.Helper()
	f.code[addr] = []byte{0x60, 0x80}
	return f.on(addr, sigV2Version, stringReturn(t, version)).
		on(addr, sigV2AgglayerGERL2, addressWord(gerManager)).
		on(addr, sigV2Threshold, uintWord(2)).
		on(addr, sigV2Proposer, addressWord(proposerAddr))
}

// asCommitteeV1 makes the contract at addr look like an AggOracleCommittee v1 of gerManager
func (f *committeeFixture) asCommitteeV1(t *testing.T, addr, gerManager common.Address, version string) *committeeFixture {
	t.Helper()
	f.code[addr] = []byte{0x60, 0x80}
	return f.on(addr, sigV1Version, stringReturn(t, version)).
		on(addr, sigV1GERManager, addressWord(gerManager)).
		on(addr, sigV1Quorum, uintWord(2))
}

func TestDetectGERMode_Direct(t *testing.T) {
	t.Run("zero updater (block.coinbase)", func(t *testing.T) {
		caller := newFixture(t).withUpdater(common.Address{})
		got, err := DetectGERMode(context.Background(), caller.mock(), gerL2Addr, common.Address{})
		require.NoError(t, err)
		require.Equal(t, DirectInjectionMode, got.Mode)
		require.Equal(t, common.Address{}, got.Committee)
		require.NotEmpty(t, got.Reason)
	})

	t.Run("EOA updater", func(t *testing.T) {
		caller := newFixture(t).withUpdater(eoaAddr)
		got, err := DetectGERMode(context.Background(), caller.mock(), gerL2Addr, common.Address{})
		require.NoError(t, err)
		require.Equal(t, DirectInjectionMode, got.Mode)
		require.Equal(t, eoaAddr, got.Updater)
		require.Equal(t, common.Address{}, got.Committee)
	})

	t.Run("EOA delegated with EIP-7702", func(t *testing.T) {
		caller := newFixture(t).withUpdater(eoaAddr)
		caller.code[eoaAddr] = append([]byte{0xef, 0x01, 0x00}, committeeAddr.Bytes()...)
		got, err := DetectGERMode(context.Background(), caller.mock(), gerL2Addr, common.Address{})
		require.NoError(t, err)
		require.Equal(t, DirectInjectionMode, got.Mode)
		require.Contains(t, got.Reason, "EIP-7702")
	})
}

func TestDetectGERMode_Committees(t *testing.T) {
	t.Run("v2", func(t *testing.T) {
		caller := newFixture(t).withUpdater(committeeAddr).asCommitteeV2(t, committeeAddr, gerL2Addr, "v2.0.0")
		got, err := DetectGERMode(context.Background(), caller.mock(), gerL2Addr, common.Address{})
		require.NoError(t, err)
		require.Equal(t, AggOracleCommitteeV2Mode, got.Mode)
		require.Equal(t, committeeAddr, got.Committee)
		require.Equal(t, committeeAddr, got.Updater)
		require.Equal(t, "v2.0.0", got.Version)
	})

	t.Run("v2 minor version", func(t *testing.T) {
		caller := newFixture(t).withUpdater(committeeAddr).asCommitteeV2(t, committeeAddr, gerL2Addr, "v2.3.1")
		got, err := DetectGERMode(context.Background(), caller.mock(), gerL2Addr, common.Address{})
		require.NoError(t, err)
		require.Equal(t, AggOracleCommitteeV2Mode, got.Mode)
	})

	t.Run("v2 with the committee address configured", func(t *testing.T) {
		caller := newFixture(t).withUpdater(committeeAddr).asCommitteeV2(t, committeeAddr, gerL2Addr, "v2.0.0")
		got, err := DetectGERMode(context.Background(), caller.mock(), gerL2Addr, committeeAddr)
		require.NoError(t, err)
		require.Equal(t, AggOracleCommitteeV2Mode, got.Mode)
	})
}

func TestDetectGERMode_Errors(t *testing.T) {
	// The support of the AggOracleCommittee v1 was removed: it is detected to say so, and it is not reported as
	// an unknown contract
	t.Run("v1 committee is not supported anymore", func(t *testing.T) {
		caller := newFixture(t).withUpdater(committeeAddr).asCommitteeV1(t, committeeAddr, gerL2Addr, "v1.0.0")
		got, err := DetectGERMode(context.Background(), caller.mock(), gerL2Addr, common.Address{})
		require.ErrorIs(t, err, ErrLegacyCommitteeNotSupported)
		require.NotErrorIs(t, err, ErrUnknownGERUpdaterContract)
		require.Nil(t, got)
		// It says what the contract is and how to migrate
		require.ErrorContains(t, err, "v1.0.0")
		require.ErrorContains(t, err, `"committee"`)
		require.ErrorContains(t, err, `"direct"`)
	})

	t.Run("contract that is not a committee", func(t *testing.T) {
		caller := newFixture(t).withUpdater(committeeAddr)
		caller.code[committeeAddr] = []byte{0x60, 0x80} // e.g. a multisig: no probe answers
		_, err := DetectGERMode(context.Background(), caller.mock(), gerL2Addr, common.Address{})
		require.ErrorIs(t, err, ErrUnknownGERUpdaterContract)
		require.ErrorContains(t, err, "set AggOracle.Mode explicitly")
		require.ErrorContains(t, err, "v2 probe")
		require.ErrorContains(t, err, "v1 probe")
	})

	t.Run("contract that matches both versions", func(t *testing.T) {
		caller := newFixture(t).withUpdater(committeeAddr).
			asCommitteeV2(t, committeeAddr, gerL2Addr, "v2.0.0").
			asCommitteeV1(t, committeeAddr, gerL2Addr, "v1.0.0")
		_, err := DetectGERMode(context.Background(), caller.mock(), gerL2Addr, common.Address{})
		require.ErrorIs(t, err, ErrUnknownGERUpdaterContract)
	})

	t.Run("v2 committee of another L2 GER manager", func(t *testing.T) {
		caller := newFixture(t).withUpdater(committeeAddr).asCommitteeV2(t, committeeAddr, otherGERL2Addr, "v2.0.0")
		_, err := DetectGERMode(context.Background(), caller.mock(), gerL2Addr, common.Address{})
		require.ErrorIs(t, err, ErrUnknownGERUpdaterContract)
		require.ErrorContains(t, err, "agglayerGERL2() is not the L2 GER manager")
	})

	t.Run("committee of another major version", func(t *testing.T) {
		caller := newFixture(t).withUpdater(committeeAddr).asCommitteeV2(t, committeeAddr, gerL2Addr, "v3.0.0")
		_, err := DetectGERMode(context.Background(), caller.mock(), gerL2Addr, common.Address{})
		require.ErrorIs(t, err, ErrUnknownGERUpdaterContract)
		require.ErrorContains(t, err, "version() is not v2.x")
	})

	t.Run("v2 committee that lacks threshold", func(t *testing.T) {
		caller := newFixture(t).withUpdater(committeeAddr).asCommitteeV2(t, committeeAddr, gerL2Addr, "v2.0.0")
		delete(caller.responses, callKey{to: committeeAddr, selector: selectorOf(sigV2Threshold)})
		_, err := DetectGERMode(context.Background(), caller.mock(), gerL2Addr, common.Address{})
		require.ErrorIs(t, err, ErrUnknownGERUpdaterContract)
		require.ErrorContains(t, err, "threshold() not available")
	})

	t.Run("version that is not an ABI string", func(t *testing.T) {
		caller := newFixture(t).withUpdater(committeeAddr).asCommitteeV2(t, committeeAddr, gerL2Addr, "v2.0.0")
		caller.on(committeeAddr, sigV2Version, []byte{0x01})
		_, err := DetectGERMode(context.Background(), caller.mock(), gerL2Addr, common.Address{})
		require.ErrorIs(t, err, ErrUnknownGERUpdaterContract)
	})

	t.Run("configured committee is not the GER updater", func(t *testing.T) {
		caller := newFixture(t).withUpdater(eoaAddr)
		_, err := DetectGERMode(context.Background(), caller.mock(), gerL2Addr, committeeAddr)
		require.ErrorIs(t, err, ErrConfiguredCommitteeMismatch)
		require.NotContains(t, err.Error(), "acceptGlobalExitRootUpdater")
	})

	t.Run("configured committee is the pending GER updater", func(t *testing.T) {
		caller := newFixture(t).withUpdater(eoaAddr).
			on(gerL2Addr, sigPendingGlobalExitRootUpdater, addressWord(committeeAddr))
		_, err := DetectGERMode(context.Background(), caller.mock(), gerL2Addr, committeeAddr)
		require.ErrorIs(t, err, ErrConfiguredCommitteeMismatch)
		require.ErrorContains(t, err, "acceptGlobalExitRootUpdater")
	})

	t.Run("configured committee but the updater is zero", func(t *testing.T) {
		caller := newFixture(t).withUpdater(common.Address{})
		_, err := DetectGERMode(context.Background(), caller.mock(), gerL2Addr, committeeAddr)
		require.ErrorIs(t, err, ErrConfiguredCommitteeMismatch)
	})

	t.Run("globalExitRootUpdater reverts", func(t *testing.T) {
		caller := newFixture(t) // no response for the updater: reverts
		_, err := DetectGERMode(context.Background(), caller.mock(), gerL2Addr, common.Address{})
		require.ErrorContains(t, err, "failed to read globalExitRootUpdater")
		require.ErrorContains(t, err, "reverted")
	})

	t.Run("globalExitRootUpdater answers nothing (not a contract)", func(t *testing.T) {
		caller := newFixture(t).on(gerL2Addr, sigGlobalExitRootUpdater, nil)
		_, err := DetectGERMode(context.Background(), caller.mock(), gerL2Addr, common.Address{})
		require.ErrorContains(t, err, "is "+gerL2Addr.Hex()+" a contract?")
	})
}

// A failure of the transport must be an error, never "the contract is not a committee v2"
func TestDetectGERMode_TransportErrors(t *testing.T) {
	t.Run("reading the GER updater", func(t *testing.T) {
		caller := newFixture(t).onError(gerL2Addr, sigGlobalExitRootUpdater, errTransport)
		_, err := DetectGERMode(context.Background(), caller.mock(), gerL2Addr, common.Address{})
		require.ErrorIs(t, err, errTransport)
	})

	t.Run("reading the code", func(t *testing.T) {
		caller := newFixture(t).withUpdater(committeeAddr)
		caller.codeErr = errTransport
		_, err := DetectGERMode(context.Background(), caller.mock(), gerL2Addr, common.Address{})
		require.ErrorIs(t, err, errTransport)
	})

	for _, sig := range []string{sigV2Version, sigV2AgglayerGERL2, sigV2Threshold, sigV2Proposer} {
		t.Run("v2 probe "+sig, func(t *testing.T) {
			caller := newFixture(t).withUpdater(committeeAddr).asCommitteeV2(t, committeeAddr, gerL2Addr, "v2.0.0").
				onError(committeeAddr, sig, errTransport)
			_, err := DetectGERMode(context.Background(), caller.mock(), gerL2Addr, common.Address{})
			require.ErrorIs(t, err, errTransport)
			require.NotErrorIs(t, err, ErrUnknownGERUpdaterContract)
		})
	}

	for _, sig := range []string{sigV1Version, sigV1GERManager, sigV1Quorum} {
		t.Run("v1 probe "+sig, func(t *testing.T) {
			caller := newFixture(t).withUpdater(committeeAddr).asCommitteeV1(t, committeeAddr, gerL2Addr, "v1.0.0").
				onError(committeeAddr, sig, errTransport)
			_, err := DetectGERMode(context.Background(), caller.mock(), gerL2Addr, common.Address{})
			require.ErrorIs(t, err, errTransport)
			require.NotErrorIs(t, err, ErrUnknownGERUpdaterContract)
		})
	}
}

func TestResolveCommitteeAddress(t *testing.T) {
	t.Run("defaults to the GER updater", func(t *testing.T) {
		caller := newFixture(t).withUpdater(committeeAddr)
		committee, updater, err := ResolveCommitteeAddress(context.Background(), caller.mock(), gerL2Addr, common.Address{})
		require.NoError(t, err)
		require.Equal(t, committeeAddr, committee)
		require.Equal(t, committeeAddr, updater)
	})

	t.Run("configured address that is the updater", func(t *testing.T) {
		caller := newFixture(t).withUpdater(committeeAddr)
		committee, _, err := ResolveCommitteeAddress(context.Background(), caller.mock(), gerL2Addr, committeeAddr)
		require.NoError(t, err)
		require.Equal(t, committeeAddr, committee)
	})

	t.Run("configured address that is not the updater", func(t *testing.T) {
		caller := newFixture(t).withUpdater(eoaAddr)
		_, updater, err := ResolveCommitteeAddress(context.Background(), caller.mock(), gerL2Addr, committeeAddr)
		require.ErrorIs(t, err, ErrConfiguredCommitteeMismatch)
		require.Equal(t, eoaAddr, updater)
	})
}

func TestValidateCommitteeV2(t *testing.T) {
	t.Run("valid committee", func(t *testing.T) {
		caller := newFixture(t).asCommitteeV2(t, committeeAddr, gerL2Addr, "v2.0.0")
		version, err := ValidateCommitteeV2(context.Background(), caller.mock(), committeeAddr, gerL2Addr)
		require.NoError(t, err)
		require.Equal(t, "v2.0.0", version)
	})

	t.Run("v1 committee", func(t *testing.T) {
		caller := newFixture(t).asCommitteeV1(t, committeeAddr, gerL2Addr, "v1.0.0")
		_, err := ValidateCommitteeV2(context.Background(), caller.mock(), committeeAddr, gerL2Addr)
		require.ErrorIs(t, err, ErrUnknownGERUpdaterContract)
	})

	t.Run("transport error", func(t *testing.T) {
		caller := newFixture(t).asCommitteeV2(t, committeeAddr, gerL2Addr, "v2.0.0").
			onError(committeeAddr, sigV2Version, errTransport)
		_, err := ValidateCommitteeV2(context.Background(), caller.mock(), committeeAddr, gerL2Addr)
		require.ErrorIs(t, err, errTransport)
	})
}

func TestIsRevert(t *testing.T) {
	require.True(t, isRevert(rpcRevertError{}))
	require.True(t, isRevert(errors.New("execution reverted: some reason")))
	require.True(t, isRevert(errors.New("Execution Reverted")))
	require.False(t, isRevert(errTransport))
	require.False(t, isRevert(context.DeadlineExceeded))
	require.False(t, isRevert(context.Canceled))
}
