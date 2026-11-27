package exit_certificate

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/polygonzkevmbridge"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	bridgetypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient/simulated"
	"github.com/stretchr/testify/require"
)

var (
	recSender   = common.HexToAddress("0x00000000000000000000000000000000000000c1") // L2 contract sending messages
	recL1Target = "0x5555555555555555555555555555555555555555"
	// recSCContract is the contract the funds of the test version-1 cases come from (origin_SC_funds).
	recSCContract = common.HexToAddress("0x00000000000000000000000000000000000000c5")
)

// v1Output renders one version-1 output as the kit writes it.
func v1Output(leafType string, originNetwork uint32, origin common.Address, destNetwork uint32, dest, amount,
	metadata string) string {
	b, err := json.Marshal(map[string]any{
		"leaf_type": leafType,
		"token_info": map[string]any{
			"origin_network": originNetwork, "origin_token_address": origin.Hex(),
		},
		"destination_network": destNetwork, "destination_address": dest, "amount": amount, "metadata": metadata,
	})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// v1Doc renders a version-1 document whose outputs get deposit counts 0, 1, ... (v1DocAt).
func v1Doc(outputs ...string) string { return v1DocAt(0, outputs...) }

// v1DocAt renders a version-1 document as the kit writes it: each output without a deposit_count gets first +
// its index, and origin_SC_funds is one contract (recSCContract) paying, per token, what the outputs bridge (a
// Transfer its token_info, a Message ETH; outputs of amount 0 or unparsable are skipped).
func v1DocAt(first uint32, outputs ...string) string {
	objs := make([]json.RawMessage, len(outputs))
	totals := map[tokenKey]*big.Int{}
	var order []tokenKey
	for i, o := range outputs {
		var m map[string]any
		dec := json.NewDecoder(strings.NewReader(o))
		dec.UseNumber()
		if err := dec.Decode(&m); err != nil {
			objs[i] = json.RawMessage(o)
			continue
		}
		if _, ok := m["deposit_count"]; !ok {
			m["deposit_count"] = first + uint32(i)
		}
		b, err := json.Marshal(m)
		if err != nil {
			panic(err)
		}
		objs[i] = b
		s, _ := m["amount"].(string)
		amount, ok := new(big.Int).SetString(s, 10)
		if !ok || amount.Sign() <= 0 {
			continue
		}
		k := recoveryETH
		if m["leaf_type"] == "Transfer" {
			ti, _ := m["token_info"].(map[string]any)
			addr, _ := ti["origin_token_address"].(string)
			net, _ := ti["origin_network"].(json.Number)
			n, _ := net.Int64()
			k = tokenKey{uint32(n), common.HexToAddress(addr)}
		}
		if totals[k] == nil {
			totals[k] = new(big.Int)
			order = append(order, k)
		}
		totals[k].Add(totals[k], amount)
	}
	funds := []any{}
	if len(order) > 0 {
		tokens := make([]any, 0, len(order))
		for _, k := range order {
			tokens = append(tokens, map[string]any{
				"token_info": map[string]any{"origin_network": k.OriginNetwork,
					"origin_token_address": k.OriginTokenAddress.Hex()},
				"amount": totals[k].String(),
			})
		}
		funds = append(funds, map[string]any{"address": recSCContract.Hex(), "tokens": tokens})
	}
	b, err := json.Marshal(map[string]any{"version": 1, "outputs": objs, "origin_SC_funds": funds})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// testRecEnv is the environment of the unit tests: deposit counts start at 0 and every contract holds plenty.
func testRecEnv() *recoveriesEnv {
	return &recoveriesEnv{
		L2NetworkID: recL2Network,
		SCBalance: func(common.Address, common.Address) (*big.Int, error) {
			return new(big.Int).Lsh(big.NewInt(1), 128), nil
		},
	}
}

// v1Case parses a version-1 document into a case, as loadRecoveryCase does.
func v1Case(t *testing.T, source, hash, doc string) recoveryCase {
	t.Helper()
	rec, err := parseRecoveryFileV1([]byte(doc))
	require.NoError(t, err)
	return recoveryCase{Source: source, CaseHash: hash, RecoveryHash: hexSHA256([]byte(doc)), Exits: rec.Exits,
		OriginSCFunds: rec.OriginSCFunds}
}

func TestRecoveryFileVersion(t *testing.T) {
	v, err := recoveryFileVersion([]byte(recDoc))
	require.NoError(t, err)
	require.Equal(t, recoveryVersionLegacy, v)

	v, err = recoveryFileVersion([]byte(`{"version":1,"outputs":[]}`))
	require.NoError(t, err)
	require.Equal(t, recoveryVersionV1, v)

	for name, doc := range map[string]string{
		"future version":   `{"version":2,"outputs":[]}`,
		"version string":   `{"version":"1","outputs":[]}`,
		"version null":     `{"version":null,"outputs":[]}`,
		"not an object":    `[1]`,
		"top level null":   `null`,
		"not json":         `{`,
		"version fraction": `{"version":1.5}`,
	} {
		_, err := recoveryFileVersion([]byte(doc))
		require.Error(t, err, name)
	}
}

func TestParseRecoveryFileV1(t *testing.T) {
	transfer := v1Output("Transfer", 0, recOrigin, 0, recDest1, "50", "0x")
	message := v1Output("Message", recL2Network, recSender, 0, recL1Target, "0", "0xbeef")
	rec, err := parseRecoveryFileV1([]byte(v1DocAt(7, transfer, message)))
	require.NoError(t, err)
	exits := rec.Exits
	require.Equal(t, uint32(7), exits[0].DepositCount)
	require.Equal(t, uint32(8), exits[1].DepositCount)
	require.Equal(t, []recoverySCFunds{{Contract: recSCContract,
		Tokens: []recoverySCToken{{Token: tokenKey{0, recOrigin}, Amount: big.NewInt(50)}}}}, rec.OriginSCFunds)
	require.Len(t, exits, 2)
	require.Equal(t, bridgetypes.LeafTypeAsset, exits[0].LeafType)
	require.Equal(t, tokenKey{0, recOrigin}, exits[0].TokenInfo)
	require.Equal(t, common.HexToAddress(recDest1), exits[0].DestinationAddress)
	require.Equal(t, "50", exits[0].Amount.String())
	require.Empty(t, exits[0].Metadata)
	require.Equal(t, bridgetypes.LeafTypeMessage, exits[1].LeafType)
	require.Equal(t, tokenKey{recL2Network, recSender}, exits[1].TokenInfo)
	require.Equal(t, []byte{0xbe, 0xef}, exits[1].Metadata)
	require.Equal(t, 0, exits[1].Amount.Sign())

	// ETH's origin_token_address is the zero address: valid in a version-1 Transfer.
	_, err = parseRecoveryFileV1([]byte(v1Doc(v1Output("Transfer", 0, common.Address{}, 0, recDest1, "1", "0x"))))
	require.NoError(t, err)

	tests := map[string]struct{ doc, want string }{
		"no outputs":             {`{"version":1,"outputs":[]}`, "non-empty list"},
		"unknown top":            {`{"version":1,"outputs":[` + transfer + `],"extra":1}`, "unknown field"},
		"missing deposit_count":  {strings.Replace(v1Doc(transfer), `"deposit_count":0,`, ``, 1), "missing field: deposit_count"},
		"negative deposit_count": {v1Doc(strings.Replace(transfer, `"amount"`, `"deposit_count":-1,"amount"`, 1)), "deposit_count"},
		"string deposit_count":   {v1Doc(strings.Replace(transfer, `"amount"`, `"deposit_count":"1","amount"`, 1)), "deposit_count"},
		"deposit_count above uint32": {
			v1Doc(strings.Replace(transfer, `"amount"`, `"deposit_count":4294967296,"amount"`, 1)), "deposit_count"},
		"unknown token_info field": {
			v1Doc(strings.Replace(transfer, `"origin_network"`, `"x":1,"origin_network"`, 1)), "unknown field"},
		"missing leaf_type":   {v1Doc(strings.Replace(transfer, `"leaf_type":"Transfer",`, ``, 1)), "missing field: leaf_type"},
		"missing token_info":  {v1Doc(v1Without(t, transfer, "token_info")), "missing field: token_info"},
		"missing origin":      {v1Doc(strings.Replace(transfer, `"origin_network":0,`, ``, 1)), "token_info.origin_network"},
		"missing token":       {v1Doc(v1Without(t, transfer, "token_info.origin_token_address")), "origin_token_address"},
		"missing network":     {v1Doc(v1Without(t, transfer, "destination_network")), "missing field: destination_network"},
		"missing destination": {v1Doc(v1Without(t, transfer, "destination_address")), "missing field: destination_address"},
		"missing amount":      {v1Doc(v1Without(t, transfer, "amount")), "missing field: amount"},
		"missing metadata":    {v1Doc(v1Without(t, transfer, "metadata")), "missing field: metadata"},
		// the names of a certificate's bridge exit (and of the first sketch of the format) are not the kit's.
		"dest_network instead of destination_network": {
			v1Doc(strings.Replace(transfer, `"destination_network"`, `"dest_network"`, 1)), "unknown field"},
		"numeric leaf_type":   {v1Doc(strings.Replace(transfer, `"leaf_type":"Transfer"`, `"leaf_type":0`, 1)), "leaf_type"},
		"unknown leaf_type":   {v1Doc(v1Output("Asset", 0, recOrigin, 0, recDest1, "1", "0x")), "leaf_type: must be"},
		"bad destination":     {v1Doc(v1Output("Transfer", 0, recOrigin, 0, "0x1234", "1", "0x")), "must be an address"},
		"destination no 0x":   {v1Doc(v1Output("Transfer", 0, recOrigin, 0, recDest1[2:], "1", "0x")), "must be an address"},
		"zero destination":    {v1Doc(v1Output("Transfer", 0, recOrigin, 0, common.Address{}.Hex(), "1", "0x")), "placeholder"},
		"bad origin address":  {v1Doc(strings.Replace(transfer, recOrigin.Hex(), "0xzz", 1)), "must be an address"},
		"numeric amount":      {v1Doc(strings.Replace(transfer, `"amount":"50"`, `"amount":50`, 1)), "amount"},
		"negative amount":     {v1Doc(v1Output("Transfer", 0, recOrigin, 0, recDest1, "-1", "0x")), "amount: must be an integer"},
		"hex amount":          {v1Doc(v1Output("Transfer", 0, recOrigin, 0, recDest1, "0x10", "0x")), "amount: must be an integer"},
		"leading zero amount": {v1Doc(v1Output("Transfer", 0, recOrigin, 0, recDest1, "01", "0x")), "amount: must be an integer"},
		"metadata without 0x": {
			v1Doc(v1Output("Message", recL2Network, recSender, 0, recL1Target, "0", "beef")), "metadata: must be hex bytes"},
		"odd metadata": {
			v1Doc(v1Output("Message", recL2Network, recSender, 0, recL1Target, "0", "0xbee")), "metadata: must be hex bytes"},
		"non-hex metadata": {
			v1Doc(v1Output("Message", recL2Network, recSender, 0, recL1Target, "0", "0xzz")), "metadata: must be hex bytes"},
		"negative network": {
			v1Doc(strings.Replace(transfer, `"destination_network":0`, `"destination_network":-1`, 1)), "destination_network"},
		"network above uint32": {
			v1Doc(strings.Replace(transfer, `"destination_network":0`, `"destination_network":4294967296`, 1)),
			"destination_network"},
		"transfer to another network": {v1Doc(v1Output("Transfer", 0, recOrigin, 5, recDest1, "1", "0x")), "must be 0"},
		"transfer of zero":            {v1Doc(v1Output("Transfer", 0, recOrigin, 0, recDest1, "0", "0x")), "Transfer of 0"},
		"message with empty metadata": {
			v1Doc(v1Output("Message", recL2Network, recSender, 0, recL1Target, "0", "0x")), "non-empty metadata"},
		"message from the zero address": {
			v1Doc(v1Output("Message", recL2Network, common.Address{}, 0, recL1Target, "0", "0xbeef")), "zero address"},
		"version 2 in v1 file": {`{"version":2,"outputs":[` + transfer + `]}`, "version must be 1"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := parseRecoveryFileV1([]byte(tc.doc))
			require.ErrorContains(t, err, tc.want)
		})
	}

	// every invalid output is reported, with its index.
	_, err = parseRecoveryFileV1([]byte(v1Doc(transfer,
		v1Output("Transfer", 0, recOrigin, 0, recDest1, "0", "0x"),
		v1Output("Message", recL2Network, recSender, 0, recL1Target, "1", "0x"))))
	require.ErrorContains(t, err, "outputs[1].amount")
	require.ErrorContains(t, err, "outputs[2].metadata")
}

// v1Without removes a field (or a token_info sub-field, "token_info.<name>") from a rendered output.
func v1Without(t *testing.T, output, field string) string {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(output), &m))
	if sub, ok := strings.CutPrefix(field, "token_info."); ok {
		tokenInfo, isMap := m["token_info"].(map[string]any)
		require.True(t, isMap)
		delete(tokenInfo, sub)
	} else {
		delete(m, field)
	}
	b, err := json.Marshal(m)
	require.NoError(t, err)
	return string(b)
}

// The outputs of a version-1 case become the delta exits field by field and in the same order (the order of
// the kit's BridgeEvents): nothing is merged, sorted or derived — even two Transfers to the same wallet.
func TestApplyRecoveries_V1OutputsBecomeExitsInOrder(t *testing.T) {
	type output struct {
		leafType      string
		originNetwork uint32
		origin        common.Address
		dest          string
		amount        string
		metadata      string
	}
	outputs := []output{
		{"Message", recL2Network, recSender, recL1Target, "0", "0x02"},
		{"Transfer", 0, recOrigin, recDest2, "40", "0xaabb"},
		{"Message", recL2Network, recSender, recL1Target, "0", "0x01"},
		{"Transfer", 0, common.Address{}, recDest1, "10", "0x"},
		{"Transfer", 0, recOrigin, recDest2, "5", "0xaabb"},
	}
	rendered := make([]string, len(outputs))
	for i, o := range outputs {
		rendered[i] = v1Output(o.leafType, o.originNetwork, o.origin, 0, o.dest, o.amount, o.metadata)
	}
	st := newRecoveriesState()
	out, err := applyRecoveries(st, testRecEnv(), []recoveryCase{v1Case(t, "v1.tgz", "h-v1", v1Doc(rendered...))})
	require.NoError(t, err)

	require.Len(t, out.Exits, len(outputs))
	for i, o := range outputs {
		e := out.Exits[i]
		require.Equal(t, o.leafType, e.LeafType.String(), i)
		require.Equal(t, tokenKey{o.originNetwork, o.origin},
			tokenKey{e.TokenInfo.OriginNetwork, e.TokenInfo.OriginTokenAddress}, i)
		require.Equal(t, uint32(0), e.DestinationNetwork, i)
		require.Equal(t, common.HexToAddress(o.dest), e.DestinationAddress, i)
		require.Equal(t, o.amount, e.Amount.String(), i)
		require.Equal(t, common.Bytes2Hex(common.FromHex(o.metadata)), common.Bytes2Hex(e.Metadata), i)
	}

	// only the Transfers move balances: native 10, token 40 + 5.
	require.Equal(t, "990", st.SC[0].PendingSCLockedBalance)
	require.Equal(t, "455", st.SC[1].PendingSCLockedBalance)
	require.Len(t, out.Report, 2)

	// the file the delta mode loads keeps them the same.
	file := filepath.Join(t.TempDir(), fileRecoveriesDelta)
	b, err := json.Marshal(DeltaExitsFile{BridgeExits: out.Exits})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(file, b, 0o600))
	loaded, err := LoadDeltaExits(file, recL2Network)
	require.NoError(t, err)
	require.Len(t, loaded, len(outputs))
	for i := range loaded {
		require.Equal(t, out.Exits[i].Hash(), loaded[i].Hash(), i)
	}
}

func TestApplyRecoveries_V1MixedWithLegacy(t *testing.T) {
	st := newRecoveriesState()
	v1 := v1DocAt(2, // after the 2 merged legacy exits

		v1Output("Message", recL2Network, recSender, 0, recL1Target, "0", "0x01"),
		v1Output("Transfer", 0, recOrigin, 0, recDest1, "40", "0x"),
		v1Output("Transfer", 0, common.Address{}, 0, recDest2, "10", "0x"),
	)
	out, err := applyRecoveries(st, testRecEnv(), []recoveryCase{
		v1Case(t, "v1.tgz", "h-v1", v1),
		recCase("legacy.tgz", "h-legacy", recLocked1, recDest1,
			RecoveryFund{Token: "native", Amount: "100"}, RecoveryFund{Token: recWrapped.Hex(), Amount: "10"}),
	})
	require.NoError(t, err)

	// native: 100 (legacy) + 10 (v1 transfer); token: 10 (legacy) + 40 (v1). The Message moves nothing.
	require.Equal(t, "890", st.SC[0].PendingSCLockedBalance)
	require.Equal(t, "110", st.SC[0].RecoveredSCLockedBalance)
	require.Equal(t, "450", st.SC[1].PendingSCLockedBalance)
	require.Equal(t, "4110", st.Checks[0].CertificateAmount)
	require.Equal(t, "890", st.Checks[0].SkippedSCLockedAmount)

	// ledger, in the order of the cases: 3 v1 entries (one per output, no locked contract) + 2 legacy entries.
	require.Len(t, st.Ledger, 5)
	for _, l := range st.Ledger[:3] {
		require.Equal(t, recoveryVersionV1, l.Version)
		require.Nil(t, l.LockedContract)
		require.Equal(t, hexSHA256([]byte(v1)), l.RecoveryHash)
		require.Equal(t, "h-v1", l.CaseHash)
	}
	require.Equal(t, recoveryLeafMessage, st.Ledger[0].LeafType)
	require.Equal(t, "0x01", st.Ledger[0].Metadata)
	require.Equal(t, recoveryLeafTransfer, st.Ledger[1].LeafType)
	require.Equal(t, recWrapped, st.Ledger[1].Token, "a v1 Transfer records the L2 token it consumes")
	for _, l := range st.Ledger[3:] {
		require.Equal(t, recoveryVersionLegacy, l.Version)
		require.NotNil(t, l.LockedContract)
		require.Empty(t, l.LeafType)
	}

	// exits: the legacy ones first (merged per destination+token and sorted, as always), then the v1 outputs
	// as they are — a legacy and a v1 Transfer to the same wallet stay two exits.
	require.Len(t, out.Exits, 5)
	require.Equal(t, "100", out.Exits[0].Amount.String())
	require.Equal(t, common.Address{}, out.Exits[0].TokenInfo.OriginTokenAddress)
	require.Equal(t, "10", out.Exits[1].Amount.String())
	require.Equal(t, recOrigin, out.Exits[1].TokenInfo.OriginTokenAddress)
	require.Equal(t, bridgetypes.LeafTypeMessage, out.Exits[2].LeafType)
	require.Equal(t, []byte{0x01}, out.Exits[2].Metadata)
	require.Equal(t, "40", out.Exits[3].Amount.String())
	require.Equal(t, common.HexToAddress(recDest1), out.Exits[3].DestinationAddress)
	require.Equal(t, "10", out.Exits[4].Amount.String())
	require.Equal(t, common.HexToAddress(recDest2), out.Exits[4].DestinationAddress)
}

func TestApplyRecoveries_V1Errors_LeaveStateUntouched(t *testing.T) {
	tests := map[string]struct {
		doc     string
		wantErr string
	}{
		"transfer of an L2-native token": {
			v1Doc(v1Output("Transfer", recL2Network, recSender, 0, recDest1, "1", "0x")), "native to the L2"},
		"transfer of a token not in the LBT": {
			v1Doc(v1Output("Transfer", 0, recSender, 0, recDest1, "1", "0x")), "not in the LBT"},
		"message not from the L2": {
			v1Doc(v1Output("Message", 0, recSender, 0, recL1Target, "0", "0x01")), "network that sent it"},
		"message to the L2 itself": {
			v1Doc(v1Output("Message", recL2Network, recSender, recL2Network, recL1Target, "0", "0x01")),
			"exiting network itself"},
		"transfer above pending": {
			v1Doc(v1Output("Transfer", 0, recOrigin, 0, recDest1, "501", "0x")), "exceeds pendingSCLockedBalance"},
		"transfers that together exceed pending": {
			v1Doc(v1Output("Transfer", 0, recOrigin, 0, recDest1, "300", "0x"),
				v1Output("Transfer", 0, recOrigin, 0, recDest2, "201", "0x")), "exceeds pendingSCLockedBalance"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			st := newRecoveriesState()
			before := newRecoveriesState()
			_, err := applyRecoveries(st, testRecEnv(), []recoveryCase{
				recCase("ok", "h-ok", recLocked1, recDest1, RecoveryFund{Token: "native", Amount: "1"}),
				v1Case(t, "bad", "h-bad", tc.doc),
			})
			require.ErrorContains(t, err, tc.wantErr)
			require.Equal(t, before, st)
		})
	}
}

// A Message moves no balance: it does not depend on the LBT nor on the pending balances.
func TestApplyRecoveries_V1MessageOnly(t *testing.T) {
	st := &recoveriesState{}
	out, err := applyRecoveries(st, testRecEnv(), []recoveryCase{v1Case(t, "m", "h",
		v1Doc(v1Output("Message", recL2Network, recSender, 0, recL1Target, "0", "0xbeef")))})
	require.NoError(t, err)
	require.Empty(t, out.Report)
	require.Len(t, out.Exits, 1)
	require.Len(t, st.Ledger, 1)
	require.Equal(t, common.Address{}, st.Ledger[0].Token)

	// the same case again is skipped, as a legacy one.
	out, err = applyRecoveries(st, testRecEnv(), []recoveryCase{v1Case(t, "m-again", "h",
		v1Doc(v1Output("Message", recL2Network, recSender, 0, recL1Target, "0", "0xbeef")))})
	require.NoError(t, err)
	require.Equal(t, []string{"m-again"}, out.Skipped)
	require.Empty(t, out.Exits)
}

// The case hash is the ledger key: it must stay what the kit's ownership-proof/case-hash.sh prints, for both
// formats. The expected values were computed with sha256sum, as the script does:
//
//	(sha256sum recovery.json move.sh | sha256sum)   — and `sha256sum recovery.json` without a move.sh.
func TestRecoveryCaseHash_Golden(t *testing.T) {
	const legacy = `{"locked_contract":"0x1111111111111111111111111111111111111111",` +
		`"destination":"0x2222222222222222222222222222222222222222","funds":[{"token":"native","amount":"100"}]}`
	require.Equal(t, "14d1401bbedee2d4617268dd787f4a51f617ed6459aa8149b43ac22d0c8f501b", hexSHA256([]byte(legacy)))
	require.Equal(t, "451b5cc40050141199a84f9760bec7fa732adcad6feb4ba05c83f6632c0df882",
		recoveryCaseHash([]byte(legacy), []byte("#!/bin/bash\n")))
	require.Equal(t, "14d1401bbedee2d4617268dd787f4a51f617ed6459aa8149b43ac22d0c8f501b",
		recoveryCaseHash([]byte(legacy), nil))

	// through the loader, for a legacy and a version-1 package: the format does not change how it is hashed.
	dir := t.TempDir()
	v1 := v1Doc(v1Output("Message", recL2Network, recSender, 0, recL1Target, "0", "0xbeef"))
	writeKitTgz(t, dir, "legacy.tgz", map[string]string{"recovery.json": legacy, "move.sh": "#!/bin/bash\n"})
	writeKitTgz(t, dir, "v1.tgz", map[string]string{"recovery.json": v1, "move.sh": "#!/bin/bash\n"})
	cases, err := loadRecoveryCases(dir)
	require.NoError(t, err)
	require.Len(t, cases, 2)
	byVersion := map[int]recoveryCase{}
	for _, c := range cases {
		byVersion[c.version()] = c
	}
	require.Equal(t, "451b5cc40050141199a84f9760bec7fa732adcad6feb4ba05c83f6632c0df882",
		byVersion[recoveryVersionLegacy].CaseHash)
	require.NotNil(t, byVersion[recoveryVersionLegacy].Recovery)
	require.Equal(t, recoveryCaseHash([]byte(v1), []byte("#!/bin/bash\n")), byVersion[recoveryVersionV1].CaseHash)
	require.Len(t, byVersion[recoveryVersionV1].Exits, 1)

	// an unsupported version is refused when loading, naming the file.
	bad := writeKitTgz(t, t.TempDir(), "v2.tgz", map[string]string{"recovery.json": `{"version":2,"outputs":[]}`})
	_, err = loadRecoveryCases(bad)
	require.ErrorContains(t, err, "unsupported version 2")
	require.ErrorContains(t, err, "v2.tgz")
}

// Backward compatibility: a ledger written before version 1 (no recoveryHash/version/leafType) still makes its
// legacy cases be skipped by case hash, and its entries keep their JSON shape when written back.
func TestApplyRecoveries_LegacyLedgerStillRecognised(t *testing.T) {
	legacyLedger := `[{"caseHash":"h-old","source":"old.tgz","lockedContract":"` + recLocked1 + `","destination":"` +
		recDest1 + `","token":"0x0000000000000000000000000000000000000000","originNetwork":0,` +
		`"originTokenAddress":"0x0000000000000000000000000000000000000000","amount":"100"}]`
	st := newRecoveriesState()
	require.NoError(t, json.Unmarshal([]byte(legacyLedger), &st.Ledger))

	out, err := applyRecoveries(st, testRecEnv(), []recoveryCase{
		recCase("old-again.tgz", "h-old", recLocked1, recDest1, RecoveryFund{Token: "native", Amount: "100"}),
		v1Case(t, "new.tgz", "h-new", v1Doc(v1Output("Transfer", 0, recOrigin, 0, recDest1, "5", "0x"))),
	})
	require.NoError(t, err)
	require.Equal(t, []string{"old-again.tgz"}, out.Skipped)
	require.Len(t, out.Applied, 1)
	require.Equal(t, recNativePending, st.SC[0].PendingSCLockedBalance, "the skipped legacy case moves nothing")

	// the legacy contract+token+destination guard still reads the old entries.
	_, err = applyRecoveries(newLedgerState(t, legacyLedger), testRecEnv(), []recoveryCase{
		recCase("repack.tgz", "h-repack", recLocked1, recDest1, RecoveryFund{Token: "native", Amount: "1"})})
	require.ErrorContains(t, err, "already recovered")

	b, err := json.Marshal(st.Ledger[:1])
	require.NoError(t, err)
	require.JSONEq(t, legacyLedger, string(b))
}

func newLedgerState(t *testing.T, ledger string) *recoveriesState {
	t.Helper()
	st := newRecoveriesState()
	require.NoError(t, json.Unmarshal([]byte(ledger), &st.Ledger))
	return st
}

// The same recovery.json re-packaged with another move.sh has another case hash: it is not applied again.
func TestApplyRecoveries_V1RepackagedCaseRefused(t *testing.T) {
	doc := v1Doc(v1Output("Transfer", 0, recOrigin, 0, recDest1, "5", "0x"))
	st := newRecoveriesState()
	_, err := applyRecoveries(st, testRecEnv(), []recoveryCase{v1Case(t, "a.tgz", "h-a", doc)})
	require.NoError(t, err)
	_, err = applyRecoveries(st, testRecEnv(), []recoveryCase{v1Case(t, "b.tgz", "h-b", doc)})
	require.ErrorContains(t, err, "already in the recoveries ledger under case hash h-a")
}

// A legacy-only, a version-1-only and a mixed batch through the step, chained: the ledger carries over, so the
// cases of an earlier run — of either format — are skipped in the next one.
func TestRunRecoveries_V1EndToEndAndChaining(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "output-first")
	st := newRecoveriesState()
	writeJSONFile(t, first, fileStep0LBT, st.LBT)
	writeJSONFile(t, first, fileStepCSCLockedValues, st.SC)
	writeJSONFile(t, first, fileStepFChecks, st.Checks)
	writeJSONFile(t, first, fileStep0TargetBlock, uint64(1234))
	// each previous run holds its exit tree: 5 leaves after the first run, + 1 legacy exit (delta 1), + 2 (delta 2).
	writeLiteTree(t, filepath.Join(first, fileStepGLiteDB), baseLeaves(5))
	rpc := balanceRPCStub(t, 1234)

	v1 := v1DocAt(6,
		v1Output("Message", recL2Network, recSender, 0, recL1Target, "0", "0xabcd"),
		v1Output("Transfer", 0, recOrigin, 0, recDest2, "20", "0x"),
	)
	v1b := v1DocAt(8, v1Output("Message", recL2Network, recSender, 0, recL1Target, "7", "0xabce"))
	run := func(name, previous string, packages map[string]map[string]string) (string, error) {
		casesDir := filepath.Join(root, "cases-"+name)
		require.NoError(t, os.MkdirAll(casesDir, 0o755))
		for file, content := range packages {
			writeKitTgz(t, casesDir, file, content)
		}
		cfg := &Config{L2NetworkID: recL2Network, L2RPCURL: rpc, Options: Options{
			DeltaMode: true, PreviousRun: previous, OutputDir: filepath.Join(root, "output-"+name),
			RecoveriesPath: casesDir,
		}}
		return filepath.Join(cfg.Options.OutputDir, dirRecoveries), runRecoveries(t.Context(), cfg)
	}
	legacyPkg := map[string]string{"recovery.json": recDoc, "move.sh": "m1"}
	v1Pkg := map[string]string{"recovery.json": v1, "move.sh": "m2", "case-hash.txt": "x", "signatures.json": "[]"}

	// delta 1: legacy only.
	out1, err := run("delta-1", first, map[string]map[string]string{"legacy.tgz": legacyPkg})
	require.NoError(t, err)
	exits, err := LoadDeltaExits(filepath.Join(out1, fileRecoveriesDelta), recL2Network)
	require.NoError(t, err)
	require.Len(t, exits, 1)
	require.FileExists(t, filepath.Join(out1, fileStep0TargetBlock), "the snapshot block is carried to the next delta")
	writeLiteTree(t, filepath.Join(filepath.Dir(out1), fileStepGLiteDB), baseLeaves(6))

	// delta 2: the legacy case again (skipped) + a version-1 case.
	out2, err := run("delta-2", filepath.Dir(out1), map[string]map[string]string{"legacy.tgz": legacyPkg, "v1.tgz": v1Pkg})
	require.NoError(t, err)
	exits, err = LoadDeltaExits(filepath.Join(out2, fileRecoveriesDelta), recL2Network)
	require.NoError(t, err)
	require.Len(t, exits, 2, "only the version-1 outputs: the legacy case is already in the ledger")
	require.Equal(t, bridgetypes.LeafTypeMessage, exits[0].LeafType)
	require.Equal(t, []byte{0xab, 0xcd}, exits[0].Metadata)
	require.Equal(t, 0, exits[0].Amount.Sign())
	require.Equal(t, bridgetypes.LeafTypeAsset, exits[1].LeafType)
	require.Equal(t, "20", exits[1].Amount.String())

	var ledger []RecoveryLedgerEntry
	require.NoError(t, loadJSON(out2, fileRecoveriesLedger, &ledger))
	require.Len(t, ledger, 3)
	require.Equal(t, recoveryCaseHash([]byte(recDoc), []byte("m1")), ledger[0].CaseHash)
	require.Equal(t, recoveryCaseHash([]byte(v1), []byte("m2")), ledger[1].CaseHash)
	var sc []SCLockedValue
	require.NoError(t, loadJSON(out2, fileStepCSCLockedValues, &sc))
	require.Equal(t, "900", sc[0].PendingSCLockedBalance, "the legacy 100 is not taken twice")
	require.Equal(t, "480", sc[1].PendingSCLockedBalance)

	report, err := os.ReadFile(filepath.Join(out2, fileRecoveriesReport))
	require.NoError(t, err)
	require.Contains(t, string(report), "Transfer: 1, Message: 1")
	require.Contains(t, string(report), "move only the ETH they carry")
	require.Contains(t, string(report), recSCContract.Hex())
	require.Equal(t, uint32(6), *ledger[1].DepositCount)
	require.Equal(t, uint32(7), *ledger[2].DepositCount)
	var scFunds []RecoveredSCFunds
	require.NoError(t, loadJSON(out2, fileRecoveriesSCFunds, &scFunds))
	require.Equal(t, []RecoveredSCFunds{{Contract: recSCContract, OriginNetwork: 0, OriginTokenAddress: recOrigin,
		L2Token: recWrapped, SnapshotBalance: "1000", Recovered: "20"}}, scFunds)
	writeLiteTree(t, filepath.Join(filepath.Dir(out2), fileStepGLiteDB), baseLeaves(8))

	// a case built on an older tree (its deposit counts are taken) is refused, and nothing is written.
	_, err = run("stale", filepath.Dir(out2), map[string]map[string]string{"v1c.tgz": {"recovery.json": v1DocAt(6,
		v1Output("Message", recL2Network, recSender, 0, recL1Target, "0", "0xabcf")), "move.sh": "m4"}})
	require.ErrorContains(t, err, "deposit_count 6, but this exit gets deposit count 8")
	require.NoDirExists(t, filepath.Join(root, "output-stale", dirRecoveries))

	// delta 3: both again (skipped) + another version-1 case, Message only.
	out3, err := run("delta-3", filepath.Dir(out2), map[string]map[string]string{
		"legacy.tgz": legacyPkg, "v1.tgz": v1Pkg, "v1b.tgz": {"recovery.json": v1b, "move.sh": "m3"},
	})
	require.NoError(t, err)
	exits, err = LoadDeltaExits(filepath.Join(out3, fileRecoveriesDelta), recL2Network)
	require.NoError(t, err)
	require.Len(t, exits, 1)
	require.Equal(t, []byte{0xab, 0xce}, exits[0].Metadata)
	require.NoError(t, loadJSON(out3, fileRecoveriesLedger, &ledger))
	require.Len(t, ledger, 4)
	// the Message carries 7 wei of ETH: charged to ETH (pending 900 → 893) and to the contract (ETH: 7 of 1000).
	require.NoError(t, loadJSON(out3, fileStepCSCLockedValues, &sc))
	require.Equal(t, "893", sc[0].PendingSCLockedBalance)
	require.NoError(t, loadJSON(out3, fileRecoveriesSCFunds, &scFunds))
	require.Len(t, scFunds, 2, "the token entry is carried, the ETH one added")
	require.Equal(t, "20", scFunds[0].Recovered)
	require.Equal(t, RecoveredSCFunds{Contract: recSCContract, SnapshotBalance: "1000", Recovered: "7"}, scFunds[1])
}

// Step F: a Message without value is in no token group, so it never enters the per-token budget check; were
// one to carry value, it is charged to L1 ETH, as the agglayer does (never to its sender's "token").
func TestGroupBridgeExitsByToken_Messages(t *testing.T) {
	sender := &agglayertypes.TokenInfo{OriginNetwork: recL2Network, OriginTokenAddress: recSender}
	token := &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: recOrigin}
	valueless := &agglayertypes.BridgeExit{LeafType: bridgetypes.LeafTypeMessage, TokenInfo: sender, Amount: big.NewInt(0)}
	transfer := &agglayertypes.BridgeExit{LeafType: bridgetypes.LeafTypeAsset, TokenInfo: token, Amount: big.NewInt(3)}
	withValue := &agglayertypes.BridgeExit{LeafType: bridgetypes.LeafTypeMessage, TokenInfo: sender, Amount: big.NewInt(7)}
	exits := []*agglayertypes.BridgeExit{valueless, transfer}
	groups := groupBridgeExitsByToken(&agglayertypes.Certificate{BridgeExits: exits})
	require.Len(t, groups, 1)
	require.Len(t, groups[tokenKey{0, recOrigin}], 1)

	// the delta check then sees the Transfer only: no entry (and no failure) for the sender's pseudo-token.
	checks, err := compareDeltaBalances(groups, []agglayerTokenEntry{{OriginTokenAddress: recOrigin, Amount: "3"}})
	require.NoError(t, err)
	require.Len(t, checks, 1)
	require.True(t, checks[0].Match)
	require.Equal(t, recOrigin.Hex(), checks[0].OriginTokenAddress)

	// a certificate of Messages only has nothing to check.
	require.Empty(t, groupBridgeExitsByToken(&agglayertypes.Certificate{BridgeExits: exits[:1]}))

	groups = groupBridgeExitsByToken(&agglayertypes.Certificate{
		BridgeExits: []*agglayertypes.BridgeExit{valueless, transfer, withValue}})
	require.Len(t, groups, 2)
	require.Len(t, groups[tokenKey{}], 1)
	require.Equal(t, int64(7), groups[tokenKey{}][0].Amount.Int64())
}

// A message's metadata is never generated from the L2 (it is the message); only Transfers without it are.
func TestDeltaRawMetadata_MessagesAreNotGenerated(t *testing.T) {
	sender := &agglayertypes.TokenInfo{OriginNetwork: recL2Network, OriginTokenAddress: recSender}
	raw, err := deltaRawMetadata(t.Context(), &Config{}, []*agglayertypes.BridgeExit{
		{LeafType: bridgetypes.LeafTypeMessage, TokenInfo: sender, Amount: big.NewInt(0)},
		{LeafType: bridgetypes.LeafTypeMessage, TokenInfo: sender, Amount: big.NewInt(0), Metadata: []byte{1}},
	})
	require.NoError(t, err) // no L2 RPC configured: it would fail if it tried to generate anything
	require.Empty(t, raw[0])
	require.Equal(t, []byte{1}, raw[1])
}

// The leaf the delta mode appends for a Message exit must be the leaf the bridge contract computes for the same
// bridgeMessage: the hash is asked to the contract itself (getLeafValue, the function bridgeMessage and
// claimMessage use), deployed on a simulated chain. The exit is shaped like the Circle USDC payouts: sender
// (1, ZkMinterBurner), destination (0, L1Escrow), amount 0, metadata abi.encode(holder, amount).
func TestDeltaMessageLeafHash_MatchesBridgeContract(t *testing.T) {
	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	auth, err := bind.NewKeyedTransactorWithChainID(key, big.NewInt(1337))
	require.NoError(t, err)
	backend := simulated.NewBackend(types.GenesisAlloc{auth.From: {Balance: big.NewInt(1e18)}},
		simulated.WithBlockGasLimit(30_000_000))
	defer backend.Close()
	_, _, bridge, err := polygonzkevmbridge.DeployPolygonzkevmbridge(auth, backend.Client())
	require.NoError(t, err)
	backend.Commit()

	const l2Network = uint32(1)
	sender := common.HexToAddress("0xBDa0B27f93B2FD3f076725b89cf02e48609bC189")
	l1Escrow := common.HexToAddress("0x70E70e58ed7B1Cec0D8ef7464072ED8A52d755eB")
	metadata := common.FromHex("0x00000000000000000000000000000000b0e79c68cafc54802726c6f102cca300" +
		"00000000000000000000000000000000000000000000000000000000000f4240")
	doc := v1Doc(v1Output("Message", l2Network, sender, 0, l1Escrow.Hex(), "0", "0x"+common.Bytes2Hex(metadata)))

	// kit output → recoveries → delta exits file → delta leaves, as the tool does.
	out, err := applyRecoveries(&recoveriesState{}, &recoveriesEnv{L2NetworkID: l2Network}, []recoveryCase{v1Case(t, "circle", "h", doc)})
	require.NoError(t, err)
	file := filepath.Join(t.TempDir(), fileRecoveriesDelta)
	b, err := json.Marshal(DeltaExitsFile{BridgeExits: out.Exits})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(file, b, 0o600))
	exits, err := LoadDeltaExits(file, l2Network)
	require.NoError(t, err)
	raw, err := deltaRawMetadata(t.Context(), &Config{}, exits)
	require.NoError(t, err)
	// gas token ETH (0, 0x0), as on zkEVM mainnet.
	leaves := liteLeavesFromExits(exits, 10, 3, 0, common.Address{}, raw)
	require.Len(t, leaves, 1)

	want, err := bridge.GetLeafValue(&bind.CallOpts{}, bridgetypes.LeafTypeMessage.Uint8(), l2Network, sender, 0,
		l1Escrow, big.NewInt(0), crypto.Keccak256Hash(metadata))
	require.NoError(t, err)
	require.Equal(t, common.Hash(want), leaves[0].Hash(), "the delta leaf is not the contract's leaf")

	// and the certificate's bridge exit (metadata hashed, as Step G2 leaves it) hashes to the same leaf.
	exits[0].Metadata = crypto.Keccak256(raw[0])
	require.Equal(t, common.Hash(want), exits[0].Hash())

	// a Transfer leaf of the same fields is another leaf: the leaf type is part of the hash.
	asTransfer, err := bridge.GetLeafValue(&bind.CallOpts{}, bridgetypes.LeafTypeAsset.Uint8(), l2Network, sender, 0,
		l1Escrow, big.NewInt(0), crypto.Keccak256Hash(metadata))
	require.NoError(t, err)
	require.NotEqual(t, common.Hash(asTransfer), leaves[0].Hash())

	// a Message carrying ETH (bridgeMessage is payable): the amount is part of the leaf, as the contract hashes it.
	withValue := *exits[0]
	withValue.Amount = big.NewInt(5)
	valueLeaves := liteLeavesFromExits([]*agglayertypes.BridgeExit{&withValue}, 10, 3, 0, common.Address{}, raw)
	wantValue, err := bridge.GetLeafValue(&bind.CallOpts{}, bridgetypes.LeafTypeMessage.Uint8(), l2Network, sender, 0,
		l1Escrow, big.NewInt(5), crypto.Keccak256Hash(metadata))
	require.NoError(t, err)
	require.Equal(t, common.Hash(wantValue), valueLeaves[0].Hash())
	require.NotEqual(t, common.Hash(want), valueLeaves[0].Hash())
}

// Two exits of one delta that would be the same leaf are refused by the delta mode (rejectDuplicateLeaves), so
// applyRecoveries refuses them first, naming the case and output, and leaves the state untouched: no ledger
// entry may record a recovery whose delta cannot be certified.
func TestApplyRecoveries_DuplicateExitsRefused(t *testing.T) {
	transfer := v1Output("Transfer", 0, recOrigin, 0, recDest1, "40", "0xaabb")
	message := v1Output("Message", recL2Network, recSender, 0, recL1Target, "0", "0xbeef")
	tests := map[string]struct {
		cases func(t *testing.T) []recoveryCase
		want  []string
	}{
		"identical transfers in one case": {
			func(t *testing.T) []recoveryCase {
				t.Helper()
				return []recoveryCase{v1Case(t, "a.tgz", "h-a", v1Doc(message, transfer, transfer))}
			},
			[]string{"a.tgz: outputs[1] and a.tgz: outputs[2] are the same Transfer exit"},
		},
		"identical messages in one case": {
			func(t *testing.T) []recoveryCase {
				t.Helper()
				return []recoveryCase{v1Case(t, "a.tgz", "h-a", v1Doc(message, message))}
			},
			[]string{"a.tgz: outputs[0] and a.tgz: outputs[1] are the same Message exit"},
		},
		"identical outputs across two cases": {
			func(t *testing.T) []recoveryCase {
				t.Helper()
				return []recoveryCase{
					v1Case(t, "a.tgz", "h-a", v1Doc(transfer)),
					v1Case(t, "b.tgz", "h-b", v1Doc(message, transfer)),
				}
			},
			[]string{"a.tgz: outputs[0] and b.tgz: outputs[1] are the same Transfer exit"},
		},
		"transfer without metadata gets the token's: same leaf as one with it": {
			func(t *testing.T) []recoveryCase {
				t.Helper()
				return []recoveryCase{v1Case(t, "a.tgz", "h-a", v1Doc(transfer,
					v1Output("Transfer", 0, recOrigin, 0, recDest1, "40", "0x")))}
			},
			[]string{"a.tgz: outputs[0] and a.tgz: outputs[1]"},
		},
		"v1 transfer identical to a legacy exit": {
			func(t *testing.T) []recoveryCase {
				t.Helper()
				return []recoveryCase{
					recCase("legacy.tgz", "h-l", recLocked1, recDest1, RecoveryFund{Token: recWrapped.Hex(), Amount: "40"}),
					v1Case(t, "a.tgz", "h-a", v1Doc(v1Output("Transfer", 0, recOrigin, 0, recDest1, "40", "0x"))),
				}
			},
			[]string{"the legacy exit of legacy.tgz", "and a.tgz: outputs[0] are the same Transfer exit"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			st := newRecoveriesState()
			_, err := applyRecoveries(st, testRecEnv(), tc.cases(t))
			for _, w := range tc.want {
				require.ErrorContains(t, err, w)
			}
			require.Equal(t, newRecoveriesState(), st)
		})
	}

	// Exits that differ in anything that goes into the leaf are fine: amount, destination, or — when both
	// carry one — a Message's or Transfer's metadata.
	st := newRecoveriesState()
	out, err := applyRecoveries(st, testRecEnv(), []recoveryCase{
		v1Case(t, "a.tgz", "h-a", v1DocAt(1, transfer, message,
			v1Output("Transfer", 0, recOrigin, 0, recDest1, "40", "0xaabbcc"),
			v1Output("Transfer", 0, recOrigin, 0, recDest2, "40", "0xaabb"),
			v1Output("Message", recL2Network, recSender, 0, recL1Target, "0", "0xbeee"))),
		recCase("legacy.tgz", "h-l", recLocked1, recDest1, RecoveryFund{Token: recWrapped.Hex(), Amount: "41"}),
	})
	require.NoError(t, err)
	require.Len(t, out.Exits, 6)
}

// balanceRPCStub serves eth_getBalance and eth_call (balanceOf) at the given block: every contract holds 1000
// of every token. Any other block is an error.
func balanceRPCStub(t *testing.T, block uint64) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params []any           `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		switch {
		case len(req.Params) != 2 || req.Params[1] != toBlockTag(block):
			resp["error"] = map[string]any{"code": -32000, "message": fmt.Sprintf("unexpected params %v", req.Params)}
		case req.Method == "eth_getBalance":
			resp["result"] = "0x3e8"
		case req.Method == "eth_call":
			resp["result"] = "0x" + common.Bytes2Hex(common.LeftPadBytes(big.NewInt(1000).Bytes(), 32))
		default:
			resp["error"] = map[string]any{"code": -32601, "message": "method not found"}
		}
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}
