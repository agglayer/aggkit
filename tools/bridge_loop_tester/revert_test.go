package bridgelooptester

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/stretchr/testify/require"
)

// dataError is an rpc.DataError carrying a hex revert payload, as a node returns for a rejected
// eth_estimateGas or eth_call.
type dataError struct {
	message string
	data    any
}

func (e *dataError) Error() string  { return e.message }
func (e *dataError) ErrorCode() int { return 3 }
func (e *dataError) ErrorData() any { return e.data }

// encodeErrorString ABI-encodes a Solidity `revert("...")`, i.e. an Error(string) payload.
func encodeErrorString(t *testing.T, reason string) []byte {
	t.Helper()

	args := abi.Arguments{{Type: mustABIType(t, "string")}}
	encoded, err := args.Pack(reason)
	require.NoError(t, err)

	return append(hexutil.MustDecode(errorStringSelector), encoded...)
}

func mustABIType(t *testing.T, name string) abi.Type {
	t.Helper()

	abiType, err := abi.NewType(name, "", nil)
	require.NoError(t, err)

	return abiType
}

// TestBridgeErrorsBySelectorKnowsAlreadyClaimed pins the one selector the hop state machine
// branches on against the real bridge ABI, so an ABI change is caught here and not at 3am in a
// soak run.
func TestBridgeErrorsBySelectorKnowsAlreadyClaimed(t *testing.T) {
	t.Parallel()

	abiErr, ok := bridgeErrorsBySelector[AlreadyClaimedSelector]
	require.True(t, ok, "the agglayerbridgel2 ABI no longer declares an error with selector %s",
		AlreadyClaimedSelector)
	require.Equal(t, alreadyClaimedErrorName, abiErr.Name)
}

func TestDecodeRevertReason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
		want string
	}{
		{
			name: "no data",
			data: nil,
			want: noRevertDataReason,
		},
		{
			name: "truncated payload",
			data: []byte{0x01, 0x02},
			want: "reverted with a truncated payload 0x0102",
		},
		{
			name: "solidity error string",
			data: encodeErrorString(t, "ERC20: transfer amount exceeds balance"),
			want: "execution reverted: ERC20: transfer amount exceeds balance",
		},
		{
			name: "solidity panic",
			data: append(hexutil.MustDecode(panicSelector), common.BigToHash(big.NewInt(0x11)).Bytes()...),
			want: "solidity panic(0x11)",
		},
		{
			name: "known bridge custom error",
			data: hexutil.MustDecode(AlreadyClaimedSelector),
			want: "reverted with AlreadyClaimed() [" + AlreadyClaimedSelector + "]",
		},
		{
			name: "unknown custom error",
			data: hexutil.MustDecode("0xdeadbeef"),
			want: "reverted with unknown custom error 0xdeadbeef (data 0xdeadbeef)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, DecodeRevertReason(tt.data))
		})
	}
}

func TestDecodeRevertError(t *testing.T) {
	t.Parallel()

	t.Run("nil error", func(t *testing.T) {
		t.Parallel()
		reason, data := DecodeRevertError(nil)
		require.Empty(t, reason)
		require.Nil(t, data)
	})

	t.Run("plain error keeps its own message", func(t *testing.T) {
		t.Parallel()
		reason, data := DecodeRevertError(errors.New("connection refused"))
		require.Equal(t, "connection refused", reason)
		require.Nil(t, data)
	})

	t.Run("hex string data", func(t *testing.T) {
		t.Parallel()
		reason, data := DecodeRevertError(&dataError{
			message: "execution reverted",
			data:    AlreadyClaimedSelector,
		})
		require.Equal(t, "reverted with AlreadyClaimed() ["+AlreadyClaimedSelector+"]", reason)
		require.Equal(t, hexutil.MustDecode(AlreadyClaimedSelector), data)
	})

	t.Run("raw bytes data", func(t *testing.T) {
		t.Parallel()
		reason, data := DecodeRevertError(&dataError{
			message: "execution reverted",
			data:    hexutil.Bytes(hexutil.MustDecode(AlreadyClaimedSelector)),
		})
		require.Contains(t, reason, alreadyClaimedErrorName)
		require.Len(t, data, selectorLen)
	})

	t.Run("nested object data", func(t *testing.T) {
		t.Parallel()
		reason, _ := DecodeRevertError(&dataError{
			message: "execution reverted",
			data:    map[string]any{"data": AlreadyClaimedSelector},
		})
		require.Contains(t, reason, alreadyClaimedErrorName)
	})

	t.Run("unparseable data falls back to the message", func(t *testing.T) {
		t.Parallel()
		reason, data := DecodeRevertError(&dataError{message: "execution reverted", data: "not-hex"})
		require.Equal(t, "execution reverted", reason)
		require.Nil(t, data)
	})
}

func TestIsAlreadyClaimed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{
			name: "revert error with the selector",
			err:  &RevertError{Data: hexutil.MustDecode(AlreadyClaimedSelector)},
			want: true,
		},
		{
			name: "revert error with another selector",
			err:  &RevertError{Data: hexutil.MustDecode("0xdeadbeef"), Reason: "something else"},
			want: false,
		},
		{
			name: "wrapped revert error",
			err: errors.Join(errors.New("claim failed"),
				&RevertError{Data: hexutil.MustDecode(AlreadyClaimedSelector)}),
			want: true,
		},
		{
			name: "selector only in the message (anvil estimate-gas shape)",
			err:  errors.New("execution reverted, custom error " + AlreadyClaimedSelector),
			want: true,
		},
		{name: "unrelated error", err: errors.New("connection refused"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, IsAlreadyClaimed(tt.err))
		})
	}
}

func TestRevertErrorMessage(t *testing.T) {
	t.Parallel()

	t.Run("before submission", func(t *testing.T) {
		t.Parallel()
		err := &RevertError{Reason: "reverted with AlreadyClaimed() [" + AlreadyClaimedSelector + "]"}
		require.Equal(t,
			"transaction: call rejected before submission: reverted with AlreadyClaimed() ["+
				AlreadyClaimedSelector+"]",
			err.Error())
	})

	t.Run("mined and reverted", func(t *testing.T) {
		t.Parallel()
		underlying := errors.New("execution reverted")
		err := &RevertError{
			Label:       "claimAsset",
			Network:     "L2B",
			TxHash:      common.HexToHash("0xabc"),
			BlockNumber: big.NewInt(7),
			GasUsed:     123,
			Reason:      "reverted with AlreadyClaimed() [" + AlreadyClaimedSelector + "]",
			Data:        hexutil.MustDecode(AlreadyClaimedSelector),
			Err:         underlying,
		}
		require.Contains(t, err.Error(), "claimAsset on L2B: tx 0x")
		require.Contains(t, err.Error(), "reverted in block 7 (gas used 123)")
		require.Contains(t, err.Error(), "[revert data "+AlreadyClaimedSelector+"]")
		require.ErrorIs(t, err, underlying)
	})
}

func TestDecorateRevert(t *testing.T) {
	t.Parallel()

	plain := errors.New("connection refused")
	require.Equal(t, plain, decorateRevert(plain))

	decorated := decorateRevert(&dataError{message: "execution reverted", data: AlreadyClaimedSelector})
	require.True(t, IsAlreadyClaimed(decorated))
}
