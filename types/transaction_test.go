package types

import (
	"encoding/json"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestErrTransactionInclusionMismatch_IsDistinctSentinel(t *testing.T) {
	require.Error(t, ErrTransactionInclusionMismatch)
	require.True(t, errors.Is(ErrTransactionInclusionMismatch, ErrTransactionInclusionMismatch))
	// Must not be confused with arbitrary transport errors.
	require.False(t, errors.Is(errors.New("network timeout"), ErrTransactionInclusionMismatch))
}

func TestRPCTransaction_UnmarshalMinedTransaction(t *testing.T) {
	payload := `{
		"from": "0x1111111111111111111111111111111111111111",
		"to": "0x2222222222222222222222222222222222222222",
		"hash": "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"value": "0xde0b6b3a7640000",
		"gas": "0x5208",
		"gasPrice": "0x3b9aca00",
		"nonce": "0x1a",
		"input": "0xdeadbeef",
		"blockHash": "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"blockNumber": "0x10",
		"transactionIndex": "0x2",
		"unexpected": {"kept": true}
	}`
	var tx RPCTransaction
	require.NoError(t, json.Unmarshal([]byte(payload), &tx))

	require.Equal(t, common.HexToAddress("0x1111111111111111111111111111111111111111"), tx.From())
	require.Equal(t, common.HexToAddress("0x2222222222222222222222222222222222222222"), tx.ToAddress())
	require.NotNil(t, tx.ToAddressPtr())
	require.Equal(t, common.HexToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), tx.Hash())
	require.False(t, tx.IsPending())

	bn, ok := tx.BlockNumber()
	require.True(t, ok)
	require.Equal(t, uint64(0x10), bn)
	bh, ok := tx.BlockHash()
	require.True(t, ok)
	require.Equal(t, common.HexToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"), bh)
	ti, ok := tx.TransactionIndex()
	require.True(t, ok)
	require.Equal(t, uint64(2), ti)
	nonce, ok := tx.Nonce()
	require.True(t, ok)
	require.Equal(t, uint64(0x1a), nonce)
	gas, ok := tx.Gas()
	require.True(t, ok)
	require.Equal(t, uint64(0x5208), gas)
	require.Equal(t, big.NewInt(1_000_000_000_000_000_000), tx.Value())
	require.Equal(t, big.NewInt(1_000_000_000), tx.GasPrice())
	require.Equal(t, []byte{0xde, 0xad, 0xbe, 0xef}, tx.Input())

	// Extra/unknown members are preserved verbatim in Raw.
	require.JSONEq(t, payload, string(tx.Raw))
}

func TestRPCTransaction_UnmarshalPendingTransaction(t *testing.T) {
	payload := `{
		"from": "0x1111111111111111111111111111111111111111",
		"to": null,
		"hash": "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"input": "0x",
		"blockHash": null,
		"blockNumber": null,
		"transactionIndex": null
	}`
	var tx RPCTransaction
	require.NoError(t, json.Unmarshal([]byte(payload), &tx))

	require.True(t, tx.IsPending())
	require.Nil(t, tx.BlockHashRaw)
	require.Nil(t, tx.BlockNumberRaw)
	require.Nil(t, tx.TransactionIndexRaw)

	// Nullable `to` is nil and maps to the zero address (contract creation).
	require.Nil(t, tx.To)
	require.Nil(t, tx.ToAddressPtr())
	require.Equal(t, common.Address{}, tx.ToAddress())

	_, ok := tx.BlockNumber()
	require.False(t, ok)
	_, ok = tx.BlockHash()
	require.False(t, ok)

	// A pending transaction cannot be canonically included.
	require.Error(t, tx.Validate())
}

func TestRPCTransaction_UnmarshalMalformedNullableTo(t *testing.T) {
	// `to` is a JSON number instead of a hex string.
	payload := `{"from": "0x1111111111111111111111111111111111111111",
		"to": 123, "hash": "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"blockHash": "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"blockNumber": "0x10", "transactionIndex": "0x0"}`
	var tx RPCTransaction
	err := json.Unmarshal([]byte(payload), &tx)
	require.Error(t, err)
}

func TestRPCTransaction_MalformedHexQuantities(t *testing.T) {
	bad := "0xZZ"
	tx := RPCTransaction{BlockNumberRaw: &bad}
	_, ok := tx.BlockNumber()
	require.False(t, ok)

	_, err := DecodeHexQuantityUint64("0xZZ")
	require.Error(t, err)
	_, err = DecodeHexQuantityUint64("10")
	require.Error(t, err)
	_, err = DecodeHexQuantityUint64("0x")
	require.Error(t, err)
	_, err = DecodeHexQuantityUint64("")
	require.Error(t, err)

	value, err := DecodeHexQuantityUint64("0x0")
	require.NoError(t, err)
	require.Equal(t, uint64(0), value)

	require.Nil(t, tx.Value())
	require.Nil(t, tx.GasPrice())
	require.Nil(t, tx.Input())

	_, err = DecodeHexData("0xnothex")
	require.Error(t, err)
	data, err := DecodeHexData("")
	require.NoError(t, err)
	require.Nil(t, data)
}

func TestRPCTransaction_MarshalRoundTripPreservesRaw(t *testing.T) {
	payload := `{"from":"0x1111111111111111111111111111111111111111","hash":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","to":null,"blockHash":null,"blockNumber":null,"transactionIndex":null,"custom":7}`
	var tx RPCTransaction
	require.NoError(t, json.Unmarshal([]byte(payload), &tx))

	out, err := json.Marshal(&tx)
	require.NoError(t, err)
	require.JSONEq(t, payload, string(out))
}

func TestRPCTransaction_Validate(t *testing.T) {
	bn := "0x1"
	bh := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	idx := "0x0"

	valid := RPCTransaction{
		FromRaw:             "0x1111111111111111111111111111111111111111",
		HashRaw:             "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BlockHashRaw:        &bh,
		BlockNumberRaw:      &bn,
		TransactionIndexRaw: &idx,
		InputRaw:            "0x",
	}
	require.NoError(t, valid.Validate())

	missingFrom := valid
	missingFrom.FromRaw = ""
	require.Error(t, missingFrom.Validate())

	missingHash := valid
	missingHash.HashRaw = ""
	require.Error(t, missingHash.Validate())

	badInput := valid
	badInput.InputRaw = "0xnothex"
	require.Error(t, badInput.Validate())
}

func TestRPCTransaction_ValidateStrict(t *testing.T) {
	hash := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	from := "0x1111111111111111111111111111111111111111"
	to := "0x2222222222222222222222222222222222222222"
	blockHash := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	blockNumber := "0x10"
	txIndex := "0x2"

	base := func() *RPCTransaction {
		return &RPCTransaction{
			FromRaw:             from,
			To:                  &to,
			HashRaw:             hash,
			BlockHashRaw:        &blockHash,
			BlockNumberRaw:      &blockNumber,
			TransactionIndexRaw: &txIndex,
			InputRaw:            "0x",
		}
	}

	require.NoError(t, base().Validate())

	// Garbage hex must be rejected instead of silently converting to zero.
	garbageHash := base()
	garbageHash.HashRaw = "garbage"
	require.Error(t, garbageHash.Validate())

	garbageFrom := base()
	garbageFrom.FromRaw = "garbage"
	require.Error(t, garbageFrom.Validate())

	garbageBlockHash := base()
	garbageBH := "garbage"
	garbageBlockHash.BlockHashRaw = &garbageBH
	require.Error(t, garbageBlockHash.Validate())

	// Wrong-size but valid hex must be rejected too.
	shortHash := base()
	shortHash.HashRaw = "0xdeadbeef"
	require.Error(t, shortHash.Validate())

	shortAddr := base()
	shortAddr.FromRaw = "0xdeadbeef"
	require.Error(t, shortAddr.Validate())

	// Malformed nullable `to` (present but not an address).
	garbageTo := base()
	garbageToStr := "garbage"
	garbageTo.To = &garbageToStr
	require.Error(t, garbageTo.Validate())

	// Null `to` (contract creation) is allowed.
	contractCreation := base()
	contractCreation.To = nil
	require.NoError(t, contractCreation.Validate())

	// JSON null for a required field must never validate.
	nullFrom := base()
	nullFrom.FromRaw = ""
	require.Error(t, nullFrom.Validate())

	nullHash := base()
	nullHash.HashRaw = ""
	require.Error(t, nullHash.Validate())

	nullBlockHash := base()
	nullBlockHash.BlockHashRaw = nil
	require.Error(t, nullBlockHash.Validate())

	nullBlockNumber := base()
	nullBlockNumber.BlockNumberRaw = nil
	require.Error(t, nullBlockNumber.Validate())

	nullTxIndex := base()
	nullTxIndex.TransactionIndexRaw = nil
	require.Error(t, nullTxIndex.Validate())

	// Malformed quantities (overflow / bad hex) must be rejected.
	overflow := base()
	overflowBN := "0x10000000000000000" // > uint64
	overflow.BlockNumberRaw = &overflowBN
	require.Error(t, overflow.Validate())

	badIndex := base()
	badIndexStr := "0xZZ"
	badIndex.TransactionIndexRaw = &badIndexStr
	require.Error(t, badIndex.Validate())

	// Optional numeric fields validate only when present.
	optionalAbsent := base()
	require.NoError(t, optionalAbsent.Validate())

	optionalMalformed := base()
	optionalMalformed.GasRaw = "0xZZ"
	require.Error(t, optionalMalformed.Validate())

	optionalMalformedNonce := base()
	optionalMalformedNonce.NonceRaw = "nothex"
	require.Error(t, optionalMalformedNonce.Validate())

	optionalMalformedValue := base()
	optionalMalformedValue.ValueRaw = "0xZZ"
	require.Error(t, optionalMalformedValue.Validate())

	// A very large value is valid (uint256, no uint64 overflow).
	largeValue := base()
	largeValue.ValueRaw = "0xffffffffffffffffffffffffffffffff"
	require.NoError(t, largeValue.Validate())

	// Real RPC extra fields still round-trip and validate.
	payload := `{"from":"` + from + `","to":"` + to + `","hash":"` + hash + `",` +
		`"value":"0x0","gas":"0x5208","gasPrice":"0x1","nonce":"0x1","input":"0x",` +
		`"blockHash":"` + blockHash + `","blockNumber":"` + blockNumber + `",` +
		`"transactionIndex":"` + txIndex + `","type":"0x2","chainId":"0x1"}`
	var rpc RPCTransaction
	require.NoError(t, json.Unmarshal([]byte(payload), &rpc))
	require.NoError(t, rpc.Validate())
	require.JSONEq(t, payload, string(rpc.Raw))
}

func TestErrTransactionUnavailable_IsDistinct(t *testing.T) {
	require.Error(t, ErrTransactionUnavailable)
	require.True(t, errors.Is(ErrTransactionUnavailable, ErrTransactionUnavailable))
	require.False(t, errors.Is(ErrTransactionUnavailable, ErrTransactionInclusionMismatch))
	require.False(t, errors.Is(ErrTransactionInclusionMismatch, ErrTransactionUnavailable))
}
