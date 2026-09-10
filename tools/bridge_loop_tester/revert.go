package bridgelooptester

import (
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
)

// AlreadyClaimedSelector is the 4-byte selector of the bridge contract's AlreadyClaimed() custom
// error, the one revert a claim submission is allowed to lose to (DESIGN.md §4, state S5submit).
// It matches test/e2e/bridge_utils.go's own alreadyClaimedErrorSelector.
const AlreadyClaimedSelector = "0x646cf558"

// alreadyClaimedErrorName is the bridge ABI's name for the AlreadyClaimedSelector error.
const alreadyClaimedErrorName = "AlreadyClaimed"

const (
	// selectorLen is the length in bytes of an ABI error/function selector.
	selectorLen = 4
	// errorStringSelector is the selector of Solidity's built-in Error(string) revert.
	errorStringSelector = "0x08c379a0"
	// panicSelector is the selector of Solidity's built-in Panic(uint256) revert.
	panicSelector = "0x4e487b71"
	// noRevertDataReason is the reason reported when a revert carried no decodable payload.
	noRevertDataReason = "reverted without revert data"
)

// bridgeErrorsBySelector maps a 4-byte selector to the bridge contract's custom error of that
// selector, so a claim/bridge revert can be reported by name (e.g. "AlreadyClaimed()") instead of
// as an opaque hex blob. Built once from the agglayerbridgel2 ABI; empty if that ABI cannot be
// parsed, which only degrades the message to the raw-selector form.
var bridgeErrorsBySelector = buildBridgeErrorsBySelector()

// buildBridgeErrorsBySelector indexes the agglayerbridgel2 ABI's declared custom errors by selector.
func buildBridgeErrorsBySelector() map[string]abi.Error {
	parsed, err := agglayerbridgel2.Agglayerbridgel2MetaData.GetAbi()
	if err != nil || parsed == nil {
		return map[string]abi.Error{}
	}

	bySelector := make(map[string]abi.Error, len(parsed.Errors))
	for _, abiErr := range parsed.Errors {
		bySelector[hexutil.Encode(abiErr.ID.Bytes()[:selectorLen])] = abiErr
	}

	return bySelector
}

// RevertError reports a contract revert the tool decoded into something a human can act on. It is
// produced both before submission (eth_estimateGas or eth_call rejecting the call) and after it
// (a transaction mined with a failed receipt status, replayed with eth_call to recover the payload).
type RevertError struct {
	// Label is the TxRequest label of the failing call ("claimAsset", "bridgeAsset", ...).
	Label string
	// Network is the human-readable name of the network the call was made on.
	Network string
	// TxHash is the failing transaction's hash, or the zero hash when the revert was detected
	// before anything was submitted.
	TxHash common.Hash
	// BlockNumber is the block the failing transaction was mined in, or nil when the revert was
	// detected before submission.
	BlockNumber *big.Int
	// GasUsed is the failing transaction's gas usage, or 0 when it was never mined.
	GasUsed uint64
	// Reason is the decoded, human-readable revert reason.
	Reason string
	// Data is the raw ABI-encoded revert payload, or nil when the node returned none.
	Data []byte
	// Err is the underlying JSON-RPC error, when there was one.
	Err error
}

// Error renders the revert with everything needed to act on it: which call, on which network,
// which transaction, and the decoded reason.
func (e *RevertError) Error() string {
	var b strings.Builder
	if e.Label != "" {
		b.WriteString(e.Label)
	} else {
		b.WriteString("transaction")
	}
	if e.Network != "" {
		fmt.Fprintf(&b, " on %s", e.Network)
	}
	if e.TxHash == (common.Hash{}) {
		b.WriteString(": call rejected before submission")
	} else {
		fmt.Fprintf(&b, ": tx %s reverted", e.TxHash)
		if e.BlockNumber != nil {
			fmt.Fprintf(&b, " in block %s", e.BlockNumber)
		}
		fmt.Fprintf(&b, " (gas used %d)", e.GasUsed)
	}
	fmt.Fprintf(&b, ": %s", e.Reason)
	if len(e.Data) > 0 {
		fmt.Fprintf(&b, " [revert data %s]", hexutil.Encode(e.Data))
	}

	return b.String()
}

// Unwrap returns the underlying JSON-RPC error, so errors.Is/As still reach it.
func (e *RevertError) Unwrap() error { return e.Err }

// IsAlreadyClaimed reports whether err is (or wraps) the bridge contract's AlreadyClaimed() revert.
// A claim submission that fails this way lost a race to another claimer, which the hop state
// machine treats as success after re-checking Bridge.IsClaimed (DESIGN.md §4/§8) - so this is the
// one revert callers are expected to branch on.
func IsAlreadyClaimed(err error) bool {
	if err == nil {
		return false
	}

	var revertErr *RevertError
	if errors.As(err, &revertErr) && len(revertErr.Data) >= selectorLen {
		return hexutil.Encode(revertErr.Data[:selectorLen]) == AlreadyClaimedSelector
	}

	// Some nodes (anvil among them) only report the selector inside the error string of a failing
	// eth_estimateGas, with no structured error data to decode - the same fallback
	// test/e2e/bridge_utils.go relies on.
	message := err.Error()

	return strings.Contains(message, AlreadyClaimedSelector) ||
		strings.Contains(message, alreadyClaimedErrorName)
}

// DecodeRevertError extracts the revert payload a JSON-RPC error carries and decodes it. It returns
// the decoded human-readable reason and the raw payload; when err carries no payload the reason is
// err's own message and the payload is nil.
func DecodeRevertError(err error) (string, []byte) {
	if err == nil {
		return "", nil
	}

	var dataErr rpc.DataError
	if errors.As(err, &dataErr) {
		if raw := revertPayload(dataErr.ErrorData()); len(raw) > 0 {
			return DecodeRevertReason(raw), raw
		}
	}

	return err.Error(), nil
}

// revertPayload normalises the shapes a node may use for a JSON-RPC error's "data" member into raw
// bytes: a hex string, raw bytes, or an object with a hex-string "data" member.
func revertPayload(data any) []byte {
	switch typed := data.(type) {
	case string:
		decoded, err := hexutil.Decode(typed)
		if err != nil {
			return nil
		}
		return decoded
	case hexutil.Bytes:
		return typed
	case []byte:
		return typed
	case map[string]any:
		if nested, ok := typed["data"]; ok {
			return revertPayload(nested)
		}
	}

	return nil
}

// DecodeRevertReason renders a raw ABI-encoded revert payload as a human-readable string: a
// Solidity Error(string)/Panic(uint256) revert, a named custom error from the bridge ABI (with its
// arguments, when they decode), or - failing all of those - the selector and raw payload.
func DecodeRevertReason(data []byte) string {
	if len(data) == 0 {
		return noRevertDataReason
	}
	if len(data) < selectorLen {
		return fmt.Sprintf("reverted with a truncated payload %s", hexutil.Encode(data))
	}

	selector := hexutil.Encode(data[:selectorLen])
	args := data[selectorLen:]

	switch selector {
	case errorStringSelector:
		if reason, err := abi.UnpackRevert(data); err == nil {
			return fmt.Sprintf("execution reverted: %s", reason)
		}
	case panicSelector:
		if len(args) > 0 {
			return fmt.Sprintf("solidity panic(%s)", hexutil.EncodeBig(new(big.Int).SetBytes(args)))
		}
	}

	abiErr, known := bridgeErrorsBySelector[selector]
	if !known {
		return fmt.Sprintf("reverted with unknown custom error %s (data %s)", selector, hexutil.Encode(data))
	}

	values, err := abiErr.Inputs.Unpack(args)
	if err != nil || len(values) == 0 {
		return fmt.Sprintf("reverted with %s [%s]", abiErr.Sig, selector)
	}

	return fmt.Sprintf("reverted with %s [%s]: %v", abiErr.Sig, selector, values)
}

// decorateRevert returns err with its decoded revert reason attached, when it carries one, and err
// unchanged otherwise. Used on eth_estimateGas / eth_sendRawTransaction errors, where the node
// rejects a call before it is ever mined.
func decorateRevert(err error) error {
	reason, data := DecodeRevertError(err)
	if len(data) == 0 {
		return err
	}

	return &RevertError{Reason: reason, Data: data, Err: err}
}
