package chaingersender

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"
)

const (
	// wordSize is the size of an ABI encoded word
	wordSize = 32
	// revertErrorCode is the JSON-RPC error code used by nodes for "execution reverted"
	revertErrorCode = 3

	// AgglayerGERL2 (L2 GER manager)
	sigGlobalExitRootUpdater        = "globalExitRootUpdater()"
	sigPendingGlobalExitRootUpdater = "pendingGlobalExitRootUpdater()"

	// AggOracleCommittee v2
	sigV2Version       = "version()"
	sigV2AgglayerGERL2 = "agglayerGERL2()"
	sigV2Threshold     = "threshold()"
	sigV2Proposer      = "proposer()"
	committeeV2Prefix  = "v2."

	// AggOracleCommittee v1
	sigV1Version      = "VERSION()"
	sigV1GERManager   = "globalExitRootManagerL2Sovereign()"
	sigV1Quorum       = "quorum()"
	committeeV1Prefix = "v1."

	// eip7702DelegateLen is the length of the code of an EOA delegated with EIP-7702: prefix + address
	eip7702DelegateLen = 23
)

// eip7702DelegationPrefix is the prefix of the code of an EOA that delegated to a contract (EIP-7702).
var eip7702DelegationPrefix = []byte{0xef, 0x01, 0x00}

var (
	// ErrConfiguredCommitteeMismatch is returned when the configured committee address
	// is not the globalExitRootUpdater of the L2 GER manager.
	ErrConfiguredCommitteeMismatch = errors.New("configured AggOracle committee is not the GER updater")
	// ErrUnknownGERUpdaterContract is returned when the GER updater is a contract that can't be
	// identified as an AggOracleCommittee v2, or that looks like both a v1 and a v2.
	ErrUnknownGERUpdaterContract = errors.New("GER updater is a contract that is not a known AggOracleCommittee")
	// ErrLegacyCommitteeNotSupported is returned when the GER updater is an AggOracleCommittee v1. Its support was
	// removed: the contract is detected only to say so, instead of reporting an unknown contract.
	ErrLegacyCommitteeNotSupported = errors.New("the GER updater is an AggOracleCommittee v1, no longer supported")
)

// Detection is the outcome of inspecting the globalExitRootUpdater of the L2 GER manager.
type Detection struct {
	// Mode is the GER submission mode that matches the contract found on the L2
	Mode GERMode
	// Updater is the globalExitRootUpdater of the L2 GER manager (zero if it uses block.coinbase)
	Updater common.Address
	// Committee is the AggOracleCommittee v2 contract. Zero in direct mode
	Committee common.Address
	// Version is the version reported by the committee contract. Empty in direct mode
	Version string
	// Reason explains why Mode was selected, to be logged
	Reason string
}

// ResolveCommitteeAddress returns the AggOracleCommittee address to use, and the GER updater found on the L2.
// If configuredCommittee is zero the committee is the GER updater, otherwise it must be the GER updater.
func ResolveCommitteeAddress(
	ctx context.Context,
	caller bind.ContractCaller,
	l2GERManager common.Address,
	configuredCommittee common.Address,
) (committee common.Address, updater common.Address, err error) {
	updater, err = callAddress(ctx, caller, l2GERManager, sigGlobalExitRootUpdater)
	if err != nil {
		return common.Address{}, common.Address{},
			fmt.Errorf("failed to read globalExitRootUpdater from the L2 GER manager %s: %w", l2GERManager, err)
	}

	if configuredCommittee == aggkitcommon.ZeroAddress {
		return updater, updater, nil
	}
	if configuredCommittee == updater {
		return configuredCommittee, updater, nil
	}

	err = fmt.Errorf("%w: configured committee is %s but the globalExitRootUpdater of the L2 GER manager %s is %s",
		ErrConfiguredCommitteeMismatch, configuredCommittee, l2GERManager, updater)
	// A committee that is only the pending updater needs one more step: give a hint about it
	pending, pendingErr := callAddress(ctx, caller, l2GERManager, sigPendingGlobalExitRootUpdater)
	if pendingErr == nil && pending == configuredCommittee {
		err = fmt.Errorf("%w: the committee is the pending GER updater, "+
			"acceptGlobalExitRootUpdater() must be called on it first", err)
	}
	return common.Address{}, updater, err
}

// DetectGERMode inspects the globalExitRootUpdater of the L2 GER manager and selects the GER submission mode:
//   - zero address, or an address without code (an EOA, or an EOA delegated with EIP-7702): direct injection
//   - an AggOracleCommittee v2 contract (version "v2.x" pointing to the same L2 GER manager): the committee mode
//   - an AggOracleCommittee v1 contract (VERSION "v1.x" pointing to the same L2 GER manager): error
//     (ErrLegacyCommitteeNotSupported), because its support was removed
//   - anything else (or a contract matching both): error, the mode must be configured explicitly.
//
// A failure of the transport (RPC unreachable, timeout...) is returned as an error: it is never
// interpreted as "the contract doesn't implement the function".
func DetectGERMode(
	ctx context.Context,
	caller bind.ContractCaller,
	l2GERManager common.Address,
	configuredCommittee common.Address,
) (*Detection, error) {
	committee, updater, err := ResolveCommitteeAddress(ctx, caller, l2GERManager, configuredCommittee)
	if err != nil {
		return nil, err
	}

	if updater == aggkitcommon.ZeroAddress {
		return &Detection{
			Mode:   DirectInjectionMode,
			Reason: "the GER updater is the zero address (the L2 GER manager accepts block.coinbase)",
		}, nil
	}

	code, err := caller.CodeAt(ctx, updater, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to read the code of the GER updater %s: %w", updater, err)
	}
	if len(code) == 0 {
		return &Detection{
			Mode:    DirectInjectionMode,
			Updater: updater,
			Reason:  fmt.Sprintf("the GER updater %s is an EOA", updater),
		}, nil
	}
	if len(code) == eip7702DelegateLen && bytes.HasPrefix(code, eip7702DelegationPrefix) {
		return &Detection{
			Mode:    DirectInjectionMode,
			Updater: updater,
			Reason:  fmt.Sprintf("the GER updater %s is an EOA delegated with EIP-7702", updater),
		}, nil
	}

	v2, err := probeCommitteeV2(ctx, caller, committee, l2GERManager)
	if err != nil {
		return nil, err
	}
	v1, err := probeCommitteeV1(ctx, caller, committee, l2GERManager)
	if err != nil {
		return nil, err
	}

	switch {
	case v2.matches && !v1.matches:
		return &Detection{
			Mode:      AggOracleCommitteeV2Mode,
			Updater:   updater,
			Committee: committee,
			Version:   v2.version,
			Reason:    fmt.Sprintf("the GER updater %s is an AggOracleCommittee v2 (version %s)", updater, v2.version),
		}, nil
	case v1.matches && !v2.matches:
		return nil, fmt.Errorf("%w: the GER updater %s is an AggOracleCommittee v1 (version %s). "+
			"Migrate the chain to an AggOracleCommittee v2 (make it the GER updater of the L2 GER manager and set "+
			"AggOracle.Mode to \"committee\"), or to a GER updater that is an EOA (AggOracle.Mode \"direct\")",
			ErrLegacyCommitteeNotSupported, updater, v1.version)
	default:
		return nil, fmt.Errorf("%w: GER updater %s (v2 probe: %s; v1 probe: %s): "+
			"set AggOracle.Mode explicitly", ErrUnknownGERUpdaterContract, updater, v2.describe(), v1.describe())
	}
}

// ValidateCommitteeV2 checks that the contract at committee is an AggOracleCommittee v2
// that points to the L2 GER manager. It returns the version reported by the contract.
func ValidateCommitteeV2(
	ctx context.Context,
	caller bind.ContractCaller,
	committee common.Address,
	l2GERManager common.Address,
) (string, error) {
	report, err := probeCommitteeV2(ctx, caller, committee, l2GERManager)
	if err != nil {
		return "", err
	}
	if !report.matches {
		return "", fmt.Errorf("%w: contract %s is not an AggOracleCommittee v2 (%s)",
			ErrUnknownGERUpdaterContract, committee, report.describe())
	}
	return report.version, nil
}

// probeReport is the outcome of probing a contract looking for a given AggOracleCommittee version
type probeReport struct {
	matches  bool
	version  string
	failures []string
}

func (p *probeReport) describe() string {
	if p.matches {
		return "matches"
	}
	return "failed: " + strings.Join(p.failures, ", ")
}

func (p *probeReport) fail(reason string) {
	p.failures = append(p.failures, reason)
}

// probeCommitteeV2 checks the contract at addr looks like an AggOracleCommittee v2 of the L2 GER manager
func probeCommitteeV2(ctx context.Context, caller bind.ContractCaller,
	addr, l2GERManager common.Address) (*probeReport, error) {
	report := &probeReport{}

	version, ok, err := probeVersion(ctx, caller, addr, sigV2Version, committeeV2Prefix)
	if err != nil {
		return nil, err
	}
	if !ok {
		report.fail(sigV2Version + " is not " + committeeV2Prefix + "x")
	}
	report.version = version

	pointsToGER, err := probePointsTo(ctx, caller, addr, sigV2AgglayerGERL2, l2GERManager)
	if err != nil {
		return nil, err
	}
	if !pointsToGER {
		report.fail(sigV2AgglayerGERL2 + " is not the L2 GER manager")
	}

	for _, sig := range []string{sigV2Threshold, sigV2Proposer} {
		responds, err := probeResponds(ctx, caller, addr, sig)
		if err != nil {
			return nil, err
		}
		if !responds {
			report.fail(sig + " not available")
		}
	}

	report.matches = len(report.failures) == 0
	return report, nil
}

// probeCommitteeV1 checks the contract at addr looks like an AggOracleCommittee v1 of the L2 GER manager
func probeCommitteeV1(ctx context.Context, caller bind.ContractCaller,
	addr, l2GERManager common.Address) (*probeReport, error) {
	report := &probeReport{}

	version, ok, err := probeVersion(ctx, caller, addr, sigV1Version, committeeV1Prefix)
	if err != nil {
		return nil, err
	}
	if !ok {
		report.fail(sigV1Version + " is not " + committeeV1Prefix + "x")
	}
	report.version = version

	pointsToGER, err := probePointsTo(ctx, caller, addr, sigV1GERManager, l2GERManager)
	if err != nil {
		return nil, err
	}
	if !pointsToGER {
		report.fail(sigV1GERManager + " is not the L2 GER manager")
	}

	responds, err := probeResponds(ctx, caller, addr, sigV1Quorum)
	if err != nil {
		return nil, err
	}
	if !responds {
		report.fail(sigV1Quorum + " not available")
	}

	report.matches = len(report.failures) == 0
	return report, nil
}

// probeVersion calls a version getter returning a string and checks its prefix.
// It returns the version (empty if the call failed) and whether it has the expected prefix.
func probeVersion(ctx context.Context, caller bind.ContractCaller,
	addr common.Address, signature, prefix string) (string, bool, error) {
	out, rejected, err := callView(ctx, caller, addr, signature)
	if err != nil {
		return "", false, err
	}
	if rejected || len(out) == 0 {
		return "", false, nil
	}
	version, err := decodeString(out)
	if err != nil {
		// not an ABI encoded string: it is not the contract we are looking for
		return "", false, nil //nolint:nilerr
	}
	return version, strings.HasPrefix(version, prefix), nil
}

// probePointsTo calls a getter returning an address and checks that it is the expected one
func probePointsTo(ctx context.Context, caller bind.ContractCaller,
	addr common.Address, signature string, expected common.Address) (bool, error) {
	out, rejected, err := callView(ctx, caller, addr, signature)
	if err != nil {
		return false, err
	}
	if rejected || len(out) < wordSize {
		return false, nil
	}
	return common.BytesToAddress(out[:wordSize]) == expected, nil
}

// probeResponds calls a getter and checks that it returns at least one ABI word
func probeResponds(ctx context.Context, caller bind.ContractCaller,
	addr common.Address, signature string) (bool, error) {
	out, rejected, err := callView(ctx, caller, addr, signature)
	if err != nil {
		return false, err
	}
	return !rejected && len(out) >= wordSize, nil
}

// callAddress calls a getter without arguments that returns an address.
// A call rejected by the node (revert) is an error here: the contract must implement the function.
func callAddress(ctx context.Context, caller bind.ContractCaller,
	to common.Address, signature string) (common.Address, error) {
	out, rejected, err := callView(ctx, caller, to, signature)
	if err != nil {
		return common.Address{}, err
	}
	if rejected {
		return common.Address{}, fmt.Errorf("%s reverted", signature)
	}
	if len(out) < wordSize {
		return common.Address{}, fmt.Errorf("%s returned %d bytes, expected an address (is %s a contract?)",
			signature, len(out), to)
	}
	return common.BytesToAddress(out[:wordSize]), nil
}

// callView executes an eth_call to a function without arguments.
// rejected is true when the node says the call reverted (the function is probably not implemented).
// Any other error (transport, timeout...) is returned as err.
func callView(ctx context.Context, caller bind.ContractCaller,
	to common.Address, signature string) (out []byte, rejected bool, err error) {
	selector := crypto.Keccak256([]byte(signature))[:4]
	out, err = caller.CallContract(ctx, ethereum.CallMsg{To: &to, Data: selector}, nil)
	if err != nil {
		if isRevert(err) {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("eth_call %s on %s failed: %w", signature, to, err)
	}
	return out, false, nil
}

// isRevert returns true if the error is an "execution reverted" answer from the node
func isRevert(err error) bool {
	var rpcErr rpc.Error
	if errors.As(err, &rpcErr) && rpcErr.ErrorCode() == revertErrorCode {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "execution reverted")
}

// decodeString decodes an ABI encoded string
func decodeString(data []byte) (string, error) {
	stringType, err := abi.NewType("string", "", nil)
	if err != nil {
		return "", err
	}
	values, err := abi.Arguments{{Type: stringType}}.Unpack(data)
	if err != nil {
		return "", err
	}
	if len(values) != 1 {
		return "", fmt.Errorf("unexpected number of values decoding a string: %d", len(values))
	}
	str, ok := values[0].(string)
	if !ok {
		return "", fmt.Errorf("unexpected type decoding a string: %T", values[0])
	}
	return str, nil
}
