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
	// DepositCount is the deposit count the BridgeEvent got on the kit's fork, which replays every exit
	// certified so far before move.sh: the exit must get this same deposit count in the delta (anti-replay).
	DepositCount *uint32 `json:"deposit_count"`
}

// RecoveryOriginSCToken is one tokens[] entry of an origin_SC_funds contract: a token (ETH is 0 / 0x0…0, also
// when a Message carries it) and the total the contract bridged out of it.
type RecoveryOriginSCToken struct {
	TokenInfo *RecoveryTokenInfo `json:"token_info"`
	Amount    *string            `json:"amount"`
}

// RecoveryOriginSCFunds is one origin_SC_funds entry: a contract that held the funds before move.sh and what
// it paid for.
type RecoveryOriginSCFunds struct {
	Address *string                  `json:"address"`
	Tokens  *[]RecoveryOriginSCToken `json:"tokens"`
}

// RecoveryFileV1 is a version-1 recovery.json.
type RecoveryFileV1 struct {
	Version       int                      `json:"version"`
	Outputs       []RecoveryOutput         `json:"outputs"`
	OriginSCFunds *[]RecoveryOriginSCFunds `json:"origin_SC_funds"`
}

// recoveryExit is a validated version-1 output.
type recoveryExit struct {
	LeafType           bridgetypes.LeafType
	TokenInfo          tokenKey
	DestinationNetwork uint32
	DestinationAddress common.Address
	Amount             *big.Int
	Metadata           []byte
	DepositCount       uint32
}

// recoverySCToken is a validated origin_SC_funds token: the origin token and the amount the contract paid.
type recoverySCToken struct {
	Token  tokenKey
	Amount *big.Int
}

// recoverySCFunds is a validated origin_SC_funds entry.
type recoverySCFunds struct {
	Contract common.Address
	Tokens   []recoverySCToken
}

// recoveryV1 is a validated version-1 recovery.json.
type recoveryV1 struct {
	Exits         []recoveryExit
	OriginSCFunds []recoverySCFunds
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
// to network 0, and origin_SC_funds lists each contract once, each token once per contract, positive amounts,
// and per token totals what the outputs bridge — plus what a certificate exit needs: a real destination
// address, a Transfer of a positive amount, a Message with a non-empty metadata and a non-zero sender, and the
// deposit_count of every output (required here; the kit's fork assigns it). What depends on the exiting
// network or on our state (origin/destination networks, the LBT, the balances, the deposit counts the delta
// assigns) is checked later (applyRecoveries).
func parseRecoveryFileV1(data []byte) (*recoveryV1, error) {
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
	out := &recoveryV1{Exits: make([]recoveryExit, 0, len(rec.Outputs))}
	var errs []error
	for i, o := range rec.Outputs {
		e, err := parseRecoveryOutput(fmt.Sprintf("outputs[%d]", i), o)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out.Exits = append(out.Exits, *e)
	}
	funds, err := parseOriginSCFunds(rec.OriginSCFunds)
	if err != nil {
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	if err := checkOriginSCFundsTotals(funds, out.Exits); err != nil {
		return nil, err
	}
	out.OriginSCFunds = funds
	return out, nil
}

// recoveryETH is the token a Message's value is charged to: ETH on L1 (0 / 0x0…0), as the agglayer does
// (amountTokenKey).
var recoveryETH = tokenKey{}

// recoveryOutputToken is the token an output's amount is charged to: a Transfer's token_info, ETH for a
// Message (bridgeMessage carries msg.value).
func recoveryOutputToken(e recoveryExit) tokenKey {
	if e.LeafType == bridgetypes.LeafTypeMessage {
		return recoveryETH
	}
	return e.TokenInfo
}

// parseOriginSCFunds validates the origin_SC_funds section as the kit does: required list; each entry
// {address, tokens}, the address once; tokens a non-empty list of {token_info, amount}, each token once per
// contract, amounts positive base-10 strings.
func parseOriginSCFunds(raw *[]RecoveryOriginSCFunds) ([]recoverySCFunds, error) {
	if raw == nil {
		return nil, errors.New("missing field: origin_SC_funds")
	}
	var errs []error
	funds := make([]recoverySCFunds, 0, len(*raw))
	seen := map[common.Address]int{}
	for i, e := range *raw {
		w := fmt.Sprintf("origin_SC_funds[%d]", i)
		var f recoverySCFunds
		if e.Address == nil {
			errs = append(errs, fmt.Errorf("%s: missing field: address", w))
		} else if a, err := recoveryV1Address(w+".address", *e.Address); err != nil {
			errs = append(errs, err)
		} else {
			if prev, dup := seen[a]; dup {
				errs = append(errs, fmt.Errorf("origin_SC_funds: %s is listed more than once (entries %d and %d)",
					a.Hex(), prev, i))
			}
			seen[a] = i
			f.Contract = a
		}
		if e.Tokens == nil || len(*e.Tokens) == 0 {
			errs = append(errs, fmt.Errorf("%s.tokens: must be a non-empty list", w))
			continue
		}
		tokens := map[tokenKey]bool{}
		for j, t := range *e.Tokens {
			u := fmt.Sprintf("%s.tokens[%d]", w, j)
			tok, err := parseOriginSCToken(u, t)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if tokens[tok.Token] {
				errs = append(errs, fmt.Errorf("%s.tokens: token %d/%s is listed more than once", w,
					tok.Token.OriginNetwork, tok.Token.OriginTokenAddress.Hex()))
				continue
			}
			tokens[tok.Token] = true
			f.Tokens = append(f.Tokens, tok)
		}
		funds = append(funds, f)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return funds, nil
}

func parseOriginSCToken(u string, t RecoveryOriginSCToken) (recoverySCToken, error) {
	var tok recoverySCToken
	var errs []error
	if t.TokenInfo == nil || t.TokenInfo.OriginNetwork == nil || t.TokenInfo.OriginTokenAddress == nil {
		errs = append(errs, fmt.Errorf("%s.token_info: must be {origin_network: <uint32>, origin_token_address: "+
			"<address>}", u))
	} else if a, err := recoveryV1Address(u+".token_info.origin_token_address",
		*t.TokenInfo.OriginTokenAddress); err != nil {
		errs = append(errs, err)
	} else {
		tok.Token = tokenKey{*t.TokenInfo.OriginNetwork, a}
	}
	if t.Amount == nil || !recoveryAmountRe.MatchString(*t.Amount) {
		got := "missing"
		if t.Amount != nil {
			got = fmt.Sprintf("%q", *t.Amount)
		}
		errs = append(errs, fmt.Errorf("%s.amount: must be a positive integer in base units, written as a string, "+
			"got %s", u, got))
	} else {
		tok.Amount, _ = new(big.Int).SetString(*t.Amount, decimalBase)
	}
	return tok, errors.Join(errs...)
}

// checkOriginSCFundsTotals requires, per token, that the origin_SC_funds amounts over every contract add up to
// what the outputs bridge (recoveryOutputToken; outputs of amount 0 pay for nothing).
func checkOriginSCFundsTotals(funds []recoverySCFunds, exits []recoveryExit) error {
	want := map[tokenKey]*big.Int{}
	got := map[tokenKey]*big.Int{}
	add := func(m map[tokenKey]*big.Int, k tokenKey, v *big.Int) {
		if m[k] == nil {
			m[k] = new(big.Int)
		}
		m[k].Add(m[k], v)
	}
	for _, e := range exits {
		if e.Amount.Sign() > 0 {
			add(want, recoveryOutputToken(e), e.Amount)
		}
	}
	for _, f := range funds {
		for _, t := range f.Tokens {
			add(got, t.Token, t.Amount)
		}
	}
	keys := map[tokenKey]*big.Int{}
	for k := range want {
		keys[k] = nil
	}
	for k := range got {
		keys[k] = nil
	}
	var errs []error
	for _, k := range sortedTokenKeys(keys) {
		w, g := want[k], got[k]
		if w == nil {
			w = new(big.Int)
		}
		if g == nil {
			g = new(big.Int)
		}
		if w.Cmp(g) != 0 {
			errs = append(errs, fmt.Errorf("origin_SC_funds: token %d/%s totals %s over the contracts, but the "+
				"outputs bridge %s", k.OriginNetwork, k.OriginTokenAddress.Hex(), g, w))
		}
	}
	return errors.Join(errs...)
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

	if o.DepositCount == nil {
		missing("deposit_count")
	} else {
		e.DepositCount = *o.DepositCount
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
	// before anything is written. It may carry ETH (bridgeMessage is payable): its amount is charged to ETH.
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
