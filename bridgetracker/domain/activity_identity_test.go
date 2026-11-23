package domain

import (
	"testing"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/stretchr/testify/require"
)

func identityTestBridge() *bridgeservicetypes.BridgeResponse {
	return &bridgeservicetypes.BridgeResponse{
		TxHash: "0xAAAA", BlockNum: 100, BlockTimestamp: 1_700_000_000, BlockPos: 3, DepositCount: 7,
		GlobalIndex: "7", BridgeHash: "0xBBBB", Amount: "1000", DestinationNetwork: 2, DestinationAddress: "0xCCCC",
	}
}

// TestBridgeKey verifies the key of a scanned bridge and of an activity entry is the reporting
// network plus the deposit count — never the global index — and how keys order and print
func TestBridgeKey(t *testing.T) {
	b := identityTestBridge()
	scanned := &ScannedBridge{Bridge: b, NetworkID: 1}
	entry := &ActivityEntry{Bridge: b, BridgeNetworkID: 1}
	want := BridgeKey{NetworkID: 1, DepositCount: 7}
	require.Equal(t, want, scanned.Key())
	require.Equal(t, want, entry.Key())
	require.Equal(t, "1:7", want.String())

	// the legacy pre-Etrog L1 bridge to network 1 with the same deposit count encodes to the
	// same global index, but is a different key
	legacy := &ScannedBridge{Bridge: identityTestBridge(), NetworkID: 0}
	require.Equal(t, scanned.Bridge.GlobalIndex, legacy.Bridge.GlobalIndex)
	require.NotEqual(t, scanned.Key(), legacy.Key())

	require.Equal(t, -1, BridgeKey{NetworkID: 0, DepositCount: 9}.Compare(BridgeKey{NetworkID: 1, DepositCount: 0}))
	require.Equal(t, 1, BridgeKey{NetworkID: 1, DepositCount: 2}.Compare(BridgeKey{NetworkID: 1, DepositCount: 1}))
	require.Equal(t, 0, want.Compare(BridgeKey{NetworkID: 1, DepositCount: 7}))
}

// TestSameBridge verifies two observations of one bridge key are the same bridge event only when
// location, block position and contents all match
func TestSameBridge(t *testing.T) {
	tests := map[string]struct {
		change func(b *bridgeservicetypes.BridgeResponse)
		same   bool
	}{
		"identical":                          {change: func(*bridgeservicetypes.BridgeResponse) {}, same: true},
		"hashes and addresses in other case": {change: lowerCaseHashes, same: true},
		"bridge_hash not reported":           {change: func(b *bridgeservicetypes.BridgeResponse) { b.BridgeHash = "" }, same: true},
		"block_timestamp not populated yet":  {change: func(b *bridgeservicetypes.BridgeResponse) { b.BlockTimestamp = 0 }, same: true},
		"another tx":                         {change: func(b *bridgeservicetypes.BridgeResponse) { b.TxHash = "0xdddd" }},
		"another block":                      {change: func(b *bridgeservicetypes.BridgeResponse) { b.BlockNum++ }},
		"another block_pos":                  {change: func(b *bridgeservicetypes.BridgeResponse) { b.BlockPos++ }},
		"another bridge_hash":                {change: func(b *bridgeservicetypes.BridgeResponse) { b.BridgeHash = "0xeeee" }},
		"another amount":                     {change: func(b *bridgeservicetypes.BridgeResponse) { b.Amount = "999" }},
		"another destination network":        {change: func(b *bridgeservicetypes.BridgeResponse) { b.DestinationNetwork = 9 }},
		"another destination address":        {change: func(b *bridgeservicetypes.BridgeResponse) { b.DestinationAddress = "0xffff" }},
		"another global_index":               {change: func(b *bridgeservicetypes.BridgeResponse) { b.GlobalIndex = "4294967303" }},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cached, observed := identityTestBridge(), identityTestBridge()
			tc.change(observed)
			require.Equal(t, tc.same, SameBridge(cached, observed))
			require.Equal(t, tc.same, SameBridge(observed, cached), "symmetric")

			known := KnownBridge{
				TxHash: cached.TxHash, BlockNum: cached.BlockNum, BlockTimestamp: cached.BlockTimestamp,
				BlockPos: cached.BlockPos, DepositCount: cached.DepositCount, GlobalIndex: cached.GlobalIndex,
				BridgeHash: cached.BridgeHash, Amount: cached.Amount, DestinationNetwork: cached.DestinationNetwork,
				DestinationAddress: cached.DestinationAddress, NetworkID: 1,
			}
			require.Equal(t, tc.same, known.SameBridge(observed), "KnownBridge agrees with SameBridge")
			require.Equal(t, BridgeKey{NetworkID: 1, DepositCount: 7}, known.Key())
		})
	}
	require.True(t, SameBridge(nil, nil))
	require.False(t, SameBridge(identityTestBridge(), nil))
	require.False(t, KnownBridge{}.SameBridge(nil))
}

func lowerCaseHashes(b *bridgeservicetypes.BridgeResponse) {
	b.TxHash = "0xaaaa"
	b.BridgeHash = "0xbbbb"
	b.DestinationAddress = "0xcccc"
}
