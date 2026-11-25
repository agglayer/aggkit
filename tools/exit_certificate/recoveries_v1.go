package exit_certificate

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"

	bridgetypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/ethereum/go-ethereum/common"
)

// recovery.json formats.
//
// The legacy format (no `version` field) names a locked contract, a destination and a list of funds
// (RecoveryFile). Version 1 lists the bridge exits themselves (RecoveryFileV1): one output per BridgeEvent
// that the case's move.sh emitted on the kit's shadow-fork, built by the kit's recovery-tx/build-recovery.sh
// and validated by its recovery-tx/validate-recovery.sh. Both formats can be mixed in the same run.

const (
	// recoveryVersionLegacy is the version of a recovery.json without `version` field.
	recoveryVersionLegacy = 0
	// recoveryVersionV1 is the `outputs[]` format.
	recoveryVersionV1 = 1

	recoveryLeafTransfer = "Transfer" // bridgeAsset
	recoveryLeafMessage  = "Message"  // bridgeMessage
)

var (
	recoveryAmountV1Re   = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
	recoveryMetadataV1Re = regexp.MustCompile(`^0x([0-9a-fA-F]{2})*$`)
	recoveryAddressV1Re  = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
)

// RecoveryTokenInfo is the token_info of a version-1 output: the token of a Transfer (ETH is 0 / 0x0…0) or the
// sender of a Message (the L2 network and the contract that called bridgeMessage).
type RecoveryTokenInfo struct {
	OriginNetwork      *uint32 `json:"origin_network"`
	OriginTokenAddress *string `json:"origin_token_address"`
}

// RecoveryOutput is one outputs[] entry of a version-1 recovery.json: a BridgeEvent of the simulated move,
// with the names aggkit's bridgesync uses. Pointers tell a missing field from a zero value: every field is
// required.
type RecoveryOutput struct {
	LeafType           *string            `json:"leaf_type"`
	TokenInfo          *RecoveryTokenInfo `json:"token_info"`
	DestinationNetwork *uint32            `json:"destination_network"`
	DestinationAddress *string            `json:"destination_address"`
	Amount             *string            `json:"amount"`
	Metadata           *string            `json:"metadata"`
}

// RecoveryFileV1 is a version-1 recovery.json.
type RecoveryFileV1 struct {
	Version int              `json:"version"`
	Outputs []RecoveryOutput `json:"outputs"`
}

// recoveryExit is a validated version-1 output.
type recoveryExit struct {
	LeafType           bridgetypes.LeafType
	TokenInfo          tokenKey
	DestinationNetwork uint32
	DestinationAddress common.Address
	Amount             *big.Int
	Metadata           []byte
}

// recoveryFileVersion returns the version of a recovery.json: recoveryVersionLegacy when it has no `version`
// field, else that field, which must be a supported version.
func recoveryFileVersion(data []byte) (int, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return 0, fmt.Errorf("invalid %s: %w", fileRecoveryJSON, err)
	}
	if top == nil {
		return 0, fmt.Errorf("invalid %s: the top level must be a JSON object", fileRecoveryJSON)
	}
	raw, ok := top["version"]
	if !ok {
		return recoveryVersionLegacy, nil
	}
	var v int
	if err := json.Unmarshal(raw, &v); err != nil || v != recoveryVersionV1 {
		return 0, fmt.Errorf("invalid %s: unsupported version %s (supported: %d, or no version field for the "+
			"legacy format)", fileRecoveryJSON, string(raw), recoveryVersionV1)
	}
	return v, nil
}

// parseRecoveryFileV1 parses and validates a version-1 recovery.json with the rules of the kit's
// recovery-tx/validate-recovery.sh — no unknown fields, every field present, leaf_type Transfer/Message,
// addresses 0x + 40 hex, uint32 networks, amounts as base-10 strings, metadata as 0x hex bytes, a Transfer goes
// to network 0 — plus what a certificate exit needs: a real destination address, a Transfer of a positive
// amount, and a Message with amount 0, a non-empty metadata and a non-zero sender. What depends on the exiting
// network or on our state (origin/destination networks, the LBT, the pending balances) is checked later
// (applyRecoveries).
func parseRecoveryFileV1(data []byte) ([]recoveryExit, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var rec RecoveryFileV1
	if err := dec.Decode(&rec); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", fileRecoveryJSON, err)
	}
	if rec.Version != recoveryVersionV1 {
		return nil, fmt.Errorf("invalid %s: version must be %d", fileRecoveryJSON, recoveryVersionV1)
	}
	if len(rec.Outputs) == 0 {
		return nil, errors.New("outputs must be a non-empty list")
	}
	exits := make([]recoveryExit, 0, len(rec.Outputs))
	var errs []error
	for i, o := range rec.Outputs {
		e, err := parseRecoveryOutput(fmt.Sprintf("outputs[%d]", i), o)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		exits = append(exits, *e)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return exits, nil
}

func parseRecoveryOutput(w string, o RecoveryOutput) (*recoveryExit, error) {
	var errs []error
	missing := func(field string) { errs = append(errs, fmt.Errorf("%s: missing field: %s", w, field)) }
	e := &recoveryExit{}

	switch {
	case o.LeafType == nil:
		missing("leaf_type")
	case *o.LeafType == recoveryLeafTransfer:
		e.LeafType = bridgetypes.LeafTypeAsset
	case *o.LeafType == recoveryLeafMessage:
		e.LeafType = bridgetypes.LeafTypeMessage
	default:
		errs = append(errs, fmt.Errorf("%s.leaf_type: must be %q (bridgeAsset) or %q (bridgeMessage), got %q",
			w, recoveryLeafTransfer, recoveryLeafMessage, *o.LeafType))
	}

	if o.TokenInfo == nil {
		missing("token_info")
	} else {
		if o.TokenInfo.OriginNetwork == nil {
			missing("token_info.origin_network")
		} else {
			e.TokenInfo.OriginNetwork = *o.TokenInfo.OriginNetwork
		}
		if o.TokenInfo.OriginTokenAddress == nil {
			missing("token_info.origin_token_address")
		} else if a, err := recoveryV1Address(w+".token_info.origin_token_address",
			*o.TokenInfo.OriginTokenAddress); err != nil {
			errs = append(errs, err)
		} else {
			e.TokenInfo.OriginTokenAddress = a
		}
	}

	if o.DestinationNetwork == nil {
		missing("destination_network")
	} else {
		e.DestinationNetwork = *o.DestinationNetwork
	}
	if o.DestinationAddress == nil {
		missing("destination_address")
	} else if a, err := recoveryV1Address(w+".destination_address", *o.DestinationAddress); err != nil {
		errs = append(errs, err)
	} else if isRecoveryPlaceholder(a) {
		errs = append(errs, fmt.Errorf("%s.destination_address is a placeholder (%s): the funds would be lost",
			w, a.Hex()))
	} else {
		e.DestinationAddress = a
	}

	if o.Amount == nil {
		missing("amount")
	} else if !recoveryAmountV1Re.MatchString(*o.Amount) {
		errs = append(errs, fmt.Errorf("%s.amount: must be an integer in base units, written as a string, got %q",
			w, *o.Amount))
	} else {
		e.Amount, _ = new(big.Int).SetString(*o.Amount, decimalBase)
	}

	if o.Metadata == nil {
		missing("metadata")
	} else if !recoveryMetadataV1Re.MatchString(*o.Metadata) {
		errs = append(errs, fmt.Errorf("%s.metadata: must be hex bytes (0x...), got %q", w, *o.Metadata))
	} else {
		e.Metadata, _ = hex.DecodeString(strings.TrimPrefix(*o.Metadata, "0x"))
	}

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	if e.LeafType == bridgetypes.LeafTypeAsset {
		if e.DestinationNetwork != 0 {
			errs = append(errs, fmt.Errorf("%s.destination_network: must be 0 (Ethereum) for a Transfer, got %d",
				w, e.DestinationNetwork))
		}
		if e.Amount.Sign() == 0 {
			errs = append(errs, fmt.Errorf("%s.amount: a Transfer of 0 cannot be a certificate exit", w))
		}
		return e, errors.Join(errs...)
	}
	// Message: the same rules as the delta mode (validateDeltaMessageExit), checked here so a case is refused
	// before anything is written.
	if e.Amount.Sign() != 0 {
		errs = append(errs, fmt.Errorf("%s.amount: a Message must have amount 0, got %s (messages carrying value "+
			"are not supported)", w, e.Amount))
	}
	if len(e.Metadata) == 0 {
		errs = append(errs, fmt.Errorf("%s.metadata: a Message needs a non-empty metadata (the message itself)", w))
	}
	if e.TokenInfo.OriginTokenAddress == (common.Address{}) {
		errs = append(errs, fmt.Errorf("%s.token_info.origin_token_address: a Message's sender cannot be the zero "+
			"address", w))
	}
	return e, errors.Join(errs...)
}

// recoveryV1Address parses an address written as the kit writes it: 0x + 40 hex chars (any case). Unlike the
// legacy format, the zero address is valid here (it is ETH's origin_token_address).
func recoveryV1Address(field, v string) (common.Address, error) {
	if !recoveryAddressV1Re.MatchString(v) {
		return common.Address{}, fmt.Errorf("%s: must be an address (0x + 40 hex chars), got %q", field, v)
	}
	return common.HexToAddress(v), nil
}

func isRecoveryPlaceholder(a common.Address) bool {
	for _, p := range kitPlaceholders {
		if a == p {
			return true
		}
	}
	return false
}
