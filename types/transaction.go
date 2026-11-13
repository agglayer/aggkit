package types

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

// hexBase is the radix of the JSON-RPC hex quantities and data handled by this
// file, matching the repo-wide hexBase constant used by other hex parsers.
const hexBase = 16

// ErrTransactionInclusionMismatch is a shared sentinel returned by transaction
// providers (and their storage consumers) when the requested canonical
// inclusion cannot be satisfied because the expected block/header is not (or is
// no longer) part of the canonical chain. This normally means the source log's
// block was reorged away.
//
// It is intentionally distinct from retryable network/transport errors:
//
//   - Retryable errors (timeouts, connection resets, transient RPC failures)
//     are returned as-is and the caller may retry the same operation.
//   - ErrTransactionInclusionMismatch must NOT be retried against the same
//     log. Callers (transaction providers, the sync log appender and the sync
//     driver) must treat it as a signal to refetch the whole affected range so
//     the stale log is dropped and re-resolved, instead of retrying the same
//     stale log forever.
//
// The absence of coverage (a block that has simply not been downloaded yet) is
// a different, benign condition. Because the caller explicitly requested a
// required transaction, the provider MUST NOT report it as a nil transaction
// with a nil error (that would be indistinguishable from "there is no
// transaction here"). It must instead return an explicit, distinct
// "unavailable" error (see ErrTransactionUnavailable) that the caller can
// recognise and treat as "retry later once coverage advances", never as this
// mismatch sentinel.
var ErrTransactionInclusionMismatch = errors.New(
	"transaction inclusion mismatch: expected canonical block/header is missing (stale or reorged)")

// ErrTransactionUnavailable is returned by a TransactionProvider when the
// requested log/transaction is not yet covered by the downloader (for example
// its block has not been downloaded yet). It is distinct from
// ErrTransactionInclusionMismatch (stale/reorged) and from retryable transport
// errors: the caller should treat it as "not available yet" and retry once
// coverage advances. A provider must never signal this condition by returning a
// nil transaction with a nil error.
var ErrTransactionUnavailable = errors.New(
	"transaction unavailable: log/transaction is not yet covered by the downloader")

// RPCTransaction is the reusable, shared representation of a transaction as
// returned by eth_getTransactionByHash and similar JSON-RPC endpoints.
//
// The DTO keeps the original JSON-RPC hex strings so no information is lost,
// and exposes typed accessors that decode JSON-RPC hex quantities and hex data.
// Nullable members (`to`, `blockHash`, `blockNumber`, `transactionIndex`) are
// pointers so that an explicit JSON `null`/absent member can be distinguished
// from the zero value: a pending transaction has `blockHash`/`blockNumber`/
// `transactionIndex` set to null, and a contract-creation transaction has `to`
// set to null.
//
// The full original payload is preserved in Raw so unknown ("extra") members
// survive a round-trip. The representation is deliberately compatible with the
// legacy bridgesync transaction DTO: From() and ToAddress() keep the same
// meaning and signature.
type RPCTransaction struct {
	// FromRaw is the raw hex sender address (`from`).
	FromRaw string `json:"from"`
	// To is the raw hex recipient address (`to`); nil for contract creation.
	To *string `json:"to"`
	// HashRaw is the raw hex transaction hash (`hash`).
	HashRaw string `json:"hash"`
	// ValueRaw is the raw hex wei value (`value`).
	ValueRaw string `json:"value,omitempty"`
	// GasRaw is the raw hex gas limit (`gas`).
	GasRaw string `json:"gas,omitempty"`
	// GasPriceRaw is the raw hex gas price (`gasPrice`).
	GasPriceRaw string `json:"gasPrice,omitempty"`
	// NonceRaw is the raw hex nonce (`nonce`).
	NonceRaw string `json:"nonce,omitempty"`
	// InputRaw is the raw hex input data (`input`).
	InputRaw string `json:"input,omitempty"`
	// BlockHashRaw is the raw hex block hash (`blockHash`); nil when pending.
	BlockHashRaw *string `json:"blockHash"`
	// BlockNumberRaw is the raw hex block number (`blockNumber`); nil when pending.
	BlockNumberRaw *string `json:"blockNumber"`
	// TransactionIndexRaw is the raw hex index in the block (`transactionIndex`);
	// nil when pending.
	TransactionIndexRaw *string `json:"transactionIndex"`

	// Raw preserves the original JSON payload verbatim, including unknown
	// members. It is not serialized back as a field.
	Raw json.RawMessage `json:"-"`
}

// UnmarshalJSON decodes the JSON-RPC representation while preserving the raw
// payload and keeping nullable members distinguishable from the zero value.
func (t *RPCTransaction) UnmarshalJSON(data []byte) error {
	type alias RPCTransaction
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return fmt.Errorf("RPCTransaction: cannot unmarshal transaction: %w", err)
	}
	*t = RPCTransaction(a)
	if len(data) > 0 {
		t.Raw = append(json.RawMessage(nil), data...)
	}
	return nil
}

// MarshalJSON returns the preserved raw payload when available, otherwise it
// serializes the current field values.
func (t *RPCTransaction) MarshalJSON() ([]byte, error) {
	if t != nil && len(t.Raw) > 0 {
		return t.Raw, nil
	}
	type alias RPCTransaction
	return json.Marshal((*alias)(t))
}

// From returns the sender address. It returns the zero address when the sender
// is not available, mirroring the legacy bridgesync behaviour.
func (t *RPCTransaction) From() common.Address {
	if t == nil {
		return common.Address{}
	}
	return common.HexToAddress(t.FromRaw)
}

// ToAddress returns the recipient address. It returns the zero address for a
// contract-creation (or pending) transaction whose `to` is null, mirroring the
// legacy bridgesync behaviour.
func (t *RPCTransaction) ToAddress() common.Address {
	if t == nil || t.To == nil || *t.To == "" {
		return common.Address{}
	}
	return common.HexToAddress(*t.To)
}

// ToAddressPtr returns the recipient address or nil when `to` is null. Use it
// when the distinction between a zero address and a missing recipient matters.
func (t *RPCTransaction) ToAddressPtr() *common.Address {
	if t == nil || t.To == nil || *t.To == "" {
		return nil
	}
	addr := common.HexToAddress(*t.To)
	return &addr
}

// Hash returns the transaction hash.
func (t *RPCTransaction) Hash() common.Hash {
	if t == nil {
		return common.Hash{}
	}
	return common.HexToHash(t.HashRaw)
}

// BlockHash returns the block hash and whether it is present. It returns
// (common.Hash{}, false) for pending transactions.
func (t *RPCTransaction) BlockHash() (common.Hash, bool) {
	if t == nil || t.BlockHashRaw == nil || *t.BlockHashRaw == "" {
		return common.Hash{}, false
	}
	return common.HexToHash(*t.BlockHashRaw), true
}

// BlockNumber returns the block number and whether it is present and well
// formed. It returns (0, false) for pending or malformed transactions.
func (t *RPCTransaction) BlockNumber() (uint64, bool) {
	if t == nil || t.BlockNumberRaw == nil {
		return 0, false
	}
	value, err := DecodeHexQuantityUint64(*t.BlockNumberRaw)
	if err != nil {
		return 0, false
	}
	return value, true
}

// TransactionIndex returns the transaction index within its block and whether
// it is present and well formed. It returns (0, false) for pending or malformed
// transactions.
func (t *RPCTransaction) TransactionIndex() (uint64, bool) {
	if t == nil || t.TransactionIndexRaw == nil {
		return 0, false
	}
	value, err := DecodeHexQuantityUint64(*t.TransactionIndexRaw)
	if err != nil {
		return 0, false
	}
	return value, true
}

// Nonce returns the transaction nonce and whether it is present and well formed.
func (t *RPCTransaction) Nonce() (uint64, bool) {
	if t == nil {
		return 0, false
	}
	value, err := DecodeHexQuantityUint64(t.NonceRaw)
	if err != nil {
		return 0, false
	}
	return value, true
}

// Gas returns the gas limit and whether it is present and well formed.
func (t *RPCTransaction) Gas() (uint64, bool) {
	if t == nil {
		return 0, false
	}
	value, err := DecodeHexQuantityUint64(t.GasRaw)
	if err != nil {
		return 0, false
	}
	return value, true
}

// Value returns the wei value. It returns nil when the value is absent or
// malformed.
func (t *RPCTransaction) Value() *big.Int {
	if t == nil {
		return nil
	}
	return decodeHexQuantityBig(t.ValueRaw)
}

// GasPrice returns the gas price. It returns nil when the gas price is absent or
// malformed.
func (t *RPCTransaction) GasPrice() *big.Int {
	if t == nil {
		return nil
	}
	return decodeHexQuantityBig(t.GasPriceRaw)
}

// Input returns the decoded input data. It returns nil when the input is absent
// or malformed.
func (t *RPCTransaction) Input() []byte {
	if t == nil {
		return nil
	}
	data, err := DecodeHexData(t.InputRaw)
	if err != nil {
		return nil
	}
	return data
}

// IsPending reports whether the transaction is not yet mined, i.e. it has no
// block hash.
func (t *RPCTransaction) IsPending() bool {
	if t == nil {
		return false
	}
	return t.BlockHashRaw == nil || *t.BlockHashRaw == ""
}

// Validate checks that the transaction carries the minimum data required for
// canonical inclusion: a well-formed 32-byte hash, a well-formed 20-byte sender
// address, a non-null 32-byte block hash, a well-formed block number and a
// well-formed transaction index. A nullable `to` is allowed (contract
// creation); when present it must be a well-formed address. Optional numeric
// fields (nonce/gas/gasPrice/value) are validated only when present, so real
// RPC payloads that omit them still validate. It does not require the
// transaction to be stored.
func (t *RPCTransaction) Validate() error {
	if t == nil {
		return errors.New("RPCTransaction: nil transaction")
	}
	if err := validateHexHash("hash", t.HashRaw); err != nil {
		return err
	}
	if err := validateHexAddress("from", t.FromRaw); err != nil {
		return err
	}
	// `to` is nullable (contract creation). An explicit empty string is treated
	// as absent; when present it must be a well-formed address.
	if t.To != nil && *t.To != "" {
		if err := validateHexAddress("to", *t.To); err != nil {
			return err
		}
	}
	if t.BlockHashRaw == nil || *t.BlockHashRaw == "" {
		return errors.New("RPCTransaction: transaction is pending (missing blockHash)")
	}
	if err := validateHexHash("blockHash", *t.BlockHashRaw); err != nil {
		return err
	}
	if t.BlockNumberRaw == nil {
		return errors.New("RPCTransaction: missing blockNumber")
	}
	if _, ok := t.BlockNumber(); !ok {
		return fmt.Errorf("RPCTransaction: malformed blockNumber %q", *t.BlockNumberRaw)
	}
	if t.TransactionIndexRaw == nil {
		return errors.New("RPCTransaction: missing transactionIndex")
	}
	if _, ok := t.TransactionIndex(); !ok {
		return fmt.Errorf("RPCTransaction: malformed transactionIndex %q", *t.TransactionIndexRaw)
	}
	if err := validateOptionalQuantity("nonce", t.NonceRaw); err != nil {
		return err
	}
	if err := validateOptionalQuantity("gas", t.GasRaw); err != nil {
		return err
	}
	if err := validateOptionalBigQuantity("gasPrice", t.GasPriceRaw); err != nil {
		return err
	}
	if err := validateOptionalBigQuantity("value", t.ValueRaw); err != nil {
		return err
	}
	if _, err := DecodeHexData(t.InputRaw); err != nil {
		return fmt.Errorf("RPCTransaction: malformed input: %w", err)
	}
	return nil
}

// validateHexHash returns an error when s is not a 0x-prefixed, 32-byte hex
// hash. It never accepts garbage that common.HexToHash would silently map to the
// zero hash.
func validateHexHash(name, s string) error {
	if s == "" {
		return fmt.Errorf("RPCTransaction: missing %s", name)
	}
	var h common.Hash
	if err := h.UnmarshalText([]byte(s)); err != nil {
		return fmt.Errorf("RPCTransaction: malformed %s %q: %w", name, s, err)
	}
	return nil
}

// validateHexAddress returns an error when s is not a 0x-prefixed, 20-byte hex
// address. It never accepts garbage that common.HexToAddress would silently map
// to the zero address.
func validateHexAddress(name, s string) error {
	if s == "" {
		return fmt.Errorf("RPCTransaction: missing %s", name)
	}
	var a common.Address
	if err := a.UnmarshalText([]byte(s)); err != nil {
		return fmt.Errorf("RPCTransaction: malformed %s %q: %w", name, s, err)
	}
	return nil
}

// validateOptionalQuantity validates a uint64 JSON-RPC hex quantity only when it
// is present. An absent (empty) value is accepted.
func validateOptionalQuantity(name, s string) error {
	if s == "" {
		return nil
	}
	if _, err := DecodeHexQuantityUint64(s); err != nil {
		return fmt.Errorf("RPCTransaction: malformed %s: %w", name, err)
	}
	return nil
}

// validateOptionalBigQuantity validates an unbounded (uint256) JSON-RPC hex
// quantity only when it is present, so large wei values do not overflow.
func validateOptionalBigQuantity(name, s string) error {
	if s == "" {
		return nil
	}
	if !strings.HasPrefix(s, "0x") && !strings.HasPrefix(s, "0X") {
		return fmt.Errorf("RPCTransaction: malformed %s %q: missing 0x prefix", name, s)
	}
	digits := s[2:]
	if digits == "" {
		return fmt.Errorf("RPCTransaction: malformed %s %q: no digits", name, s)
	}
	if _, ok := new(big.Int).SetString(digits, hexBase); !ok {
		return fmt.Errorf("RPCTransaction: malformed %s %q", name, s)
	}
	return nil
}

// DecodeHexQuantityUint64 decodes a JSON-RPC hex quantity (for example "0x1a")
// into a uint64. It rejects empty values and values without the 0x prefix.
func DecodeHexQuantityUint64(s string) (uint64, error) {
	if s == "" {
		return 0, errors.New("empty hex quantity")
	}
	if !strings.HasPrefix(s, "0x") && !strings.HasPrefix(s, "0X") {
		return 0, fmt.Errorf("hex quantity %q is missing the 0x prefix", s)
	}
	digits := s[2:]
	if digits == "" {
		return 0, fmt.Errorf("hex quantity %q has no digits", s)
	}
	value, err := strconv.ParseUint(digits, hexBase, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid hex quantity %q: %w", s, err)
	}
	return value, nil
}

// DecodeHexData decodes a JSON-RPC hex data string (for example "0xdeadbeef")
// into bytes. An empty string decodes to nil.
func DecodeHexData(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	if !strings.HasPrefix(s, "0x") && !strings.HasPrefix(s, "0X") {
		return nil, fmt.Errorf("hex data %q is missing the 0x prefix", s)
	}
	data, err := hex.DecodeString(s[2:])
	if err != nil {
		return nil, fmt.Errorf("invalid hex data %q: %w", s, err)
	}
	return data, nil
}

// decodeHexQuantityBig decodes a JSON-RPC hex quantity into a big.Int. It
// returns nil when the value is empty or malformed.
func decodeHexQuantityBig(s string) *big.Int {
	if s == "" {
		return nil
	}
	if !strings.HasPrefix(s, "0x") && !strings.HasPrefix(s, "0X") {
		return nil
	}
	digits := s[2:]
	if digits == "" {
		return big.NewInt(0)
	}
	value, ok := new(big.Int).SetString(digits, hexBase)
	if !ok {
		return nil
	}
	return value
}

// TransactionProvider is an OPTIONAL capability exposed by a MultiDownloader
// (and implemented by the transaction fetch worker) that resolves the full
// transaction which produced a given log.
//
// It is deliberately NOT part of the mandatory MultiDownloaderLegacy contract
// and must not be added to it. Callers must type-assert and handle a missing
// implementation explicitly, so that a disabled transaction path can never
// silently appear to work:
//
//	if tp, ok := md.(aggkittypes.TransactionProvider); ok {
//		tx, err := tp.TransactionByLog(ctx, log)
//	} else {
//		// clear error: transaction provider not available
//	}
//
// TransactionByLog returns aggkittypes.ErrTransactionInclusionMismatch when the
// log's canonical source block/header is not available (typically due to a
// reorg), so the caller can refetch the whole affected range. Retryable network
// errors are returned unchanged and must not be conflated with that sentinel.
// When the source block is simply not covered yet, the provider MUST return an
// explicit aggkittypes.ErrTransactionUnavailable (never a nil transaction with
// a nil error), because the caller requested a required transaction and a
// silent nil would be misread as "no transaction".
type TransactionProvider interface {
	TransactionByLog(ctx context.Context, log ethtypes.Log) (*RPCTransaction, error)
}
