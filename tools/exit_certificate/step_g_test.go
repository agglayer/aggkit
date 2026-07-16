package exit_certificate

import (
	"math/big"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestIsNativeBridgeExit(t *testing.T) {
	t.Parallel()

	gasTokenNetwork := uint32(0)
	gasTokenAddr := common.HexToAddress("0xGasToken")

	tests := []struct {
		name   string
		ti     *agglayertypes.TokenInfo
		native bool
	}{
		{
			name:   "nil TokenInfo is native",
			ti:     nil,
			native: true,
		},
		{
			name:   "zero origin address is native (ETH)",
			ti:     &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: common.Address{}},
			native: true,
		},
		{
			name:   "gas token address is native",
			ti:     &agglayertypes.TokenInfo{OriginNetwork: gasTokenNetwork, OriginTokenAddress: gasTokenAddr},
			native: true,
		},
		{
			name:   "non-native ERC-20",
			ti:     &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: common.HexToAddress("0x1111")},
			native: false,
		},
		{
			// Regression (AET-01): another network's native asset is a wrapped ERC-20 on this L2 with
			// OriginTokenAddress=0x0 but a non-gas-token OriginNetwork. It must NOT be treated as the
			// local gas token, otherwise Step G replays it natively and computes the LER from wrong leaves.
			name:   "external native token (zero addr, non-gas-token network) is not native",
			ti:     &agglayertypes.TokenInfo{OriginNetwork: 5, OriginTokenAddress: common.Address{}},
			native: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := isNativeBridgeExit(tc.ti, gasTokenNetwork, gasTokenAddr)
			require.Equal(t, tc.native, got)
		})
	}
}

func TestFindTokenAddress_Found(t *testing.T) {
	t.Parallel()

	originAddr := common.HexToAddress("0x1111111111111111111111111111111111111111")
	wrappedAddr := common.HexToAddress("0x2222222222222222222222222222222222222222")

	tokenMap := map[tokenOriginKey]common.Address{
		{0, originAddr}: wrappedAddr,
	}
	exit := &agglayertypes.BridgeExit{
		TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: originAddr},
	}

	addr, err := findTokenAddress(exit, tokenMap)
	require.NoError(t, err)
	require.Equal(t, wrappedAddr, addr)
}

func TestFindTokenAddress_NotFound(t *testing.T) {
	t.Parallel()

	originAddr := common.HexToAddress("0x1111111111111111111111111111111111111111")
	exit := &agglayertypes.BridgeExit{
		TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: originAddr},
	}

	_, err := findTokenAddress(exit, map[tokenOriginKey]common.Address{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found in token map")
}

func TestFindTokenAddress_NilTokenInfo(t *testing.T) {
	t.Parallel()

	exit := &agglayertypes.BridgeExit{TokenInfo: nil}
	_, err := findTokenAddress(exit, map[tokenOriginKey]common.Address{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil TokenInfo")
}

func TestBuildLBTTokenMap(t *testing.T) {
	t.Parallel()

	origin1 := common.HexToAddress("0x1111111111111111111111111111111111111111")
	wrapped1 := common.HexToAddress("0x2222222222222222222222222222222222222222")
	origin2 := common.HexToAddress("0x3333333333333333333333333333333333333333")
	wrapped2 := common.HexToAddress("0x4444444444444444444444444444444444444444")

	entries := []LBTEntry{
		{OriginNetwork: 0, OriginTokenAddress: origin1, WrappedTokenAddress: wrapped1},
		{OriginNetwork: 1, OriginTokenAddress: origin2, WrappedTokenAddress: wrapped2},
		// zero WrappedTokenAddress should be excluded (native entry)
		{OriginNetwork: 0, OriginTokenAddress: origin1, WrappedTokenAddress: common.Address{}},
	}

	m := buildLBTTokenMap(entries)
	require.Len(t, m, 2)
	require.Equal(t, wrapped1, m[tokenOriginKey{0, origin1}])
	require.Equal(t, wrapped2, m[tokenOriginKey{1, origin2}])
}

func TestBuildLBTTokenMap_Empty(t *testing.T) {
	t.Parallel()
	m := buildLBTTokenMap(nil)
	require.Empty(t, m)
}

func TestEncodeERC20ApproveCallRaw_Length(t *testing.T) {
	t.Parallel()

	spender := common.HexToAddress("0x1234567890123456789012345678901234567890")
	amount := big.NewInt(1000)

	data := encodeERC20ApproveCallRaw(spender, amount)
	// 4 bytes selector + 32 bytes spender + 32 bytes amount = 68
	require.Len(t, data, 68)
}

func TestEncodeERC20ApproveCallRaw_NilAmount(t *testing.T) {
	t.Parallel()

	spender := common.HexToAddress("0x1234567890123456789012345678901234567890")
	data := encodeERC20ApproveCallRaw(spender, nil)
	require.Len(t, data, 68)
}

func TestEncodeERC20ApproveCallRaw_Selector(t *testing.T) {
	t.Parallel()

	// keccak256("approve(address,uint256)")[:4] = 0x095ea7b3
	spender := common.HexToAddress("0x1234567890123456789012345678901234567890")
	data := encodeERC20ApproveCallRaw(spender, big.NewInt(1))
	require.Equal(t, []byte{0x09, 0x5e, 0xa7, 0xb3}, data[:4])
}

func TestEncodeBridgeAssetCallRaw_NonNil(t *testing.T) {
	t.Parallel()

	destAddr := common.HexToAddress("0xDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD")
	tokenAddr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")

	data := encodeBridgeAssetCallRaw(1, destAddr, big.NewInt(500), tokenAddr)
	require.NotEmpty(t, data)
	// ABI-encoded: 4 selector + 5 * 32 = 164 bytes minimum
	require.Greater(t, len(data), 4)
}

func TestEncodeBridgeAssetCallRaw_NilAmount(t *testing.T) {
	t.Parallel()

	destAddr := common.HexToAddress("0xDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD")
	tokenAddr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")

	require.NotPanics(t, func() {
		data := encodeBridgeAssetCallRaw(0, destAddr, nil, tokenAddr)
		require.NotEmpty(t, data)
	})
}
