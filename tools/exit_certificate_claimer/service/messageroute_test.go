package claimer

import (
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// Circle USDC recovery route (zkEVM mainnet): ZkMinterBurner on the L2 sends a message to L1Escrow,
// which pays the beneficiary in L1 USDC.
var (
	circleZkMinterBurner = common.HexToAddress("0xBDa0B27f93B2FD3f076725b89cf02e48609bC189")
	circleL1Escrow       = common.HexToAddress("0x70E70e58ed7B1Cec0D8ef7464072ED8A52d755eB")
	circleL1USDC         = common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")
)

const circleNetworkID uint32 = 1

func circleRoute() MessageRoute {
	return MessageRoute{
		OriginNetwork:      circleNetworkID,
		OriginAddress:      circleZkMinterBurner.Hex(),
		DestinationNetwork: 0,
		DestinationAddress: circleL1Escrow.Hex(),
		MetadataLayout:     MetadataLayoutAddressUint256,
		DisplayToken:       DisplayToken{Address: circleL1USDC.Hex(), Symbol: "USDC", Decimals: 6},
	}
}

// encodeAddressUint256 is abi.encode(address, uint256).
func encodeAddressUint256(addr common.Address, amount *big.Int) []byte {
	out := make([]byte, 0, addressUint256Bytes)
	out = append(out, common.LeftPadBytes(addr.Bytes(), abiWordSize)...)
	return append(out, common.LeftPadBytes(amount.Bytes(), abiWordSize)...)
}

func TestDecodeAddressUint256(t *testing.T) {
	t.Parallel()
	holder := common.HexToAddress("0x3Dc400000000000000000000000000000000ca17")
	amount := big.NewInt(3_000_000_000)
	valid := encodeAddressUint256(holder, amount)

	gotHolder, gotAmount, err := decodeAddressUint256(valid)
	require.NoError(t, err)
	require.Equal(t, holder, gotHolder)
	require.Equal(t, amount, gotAmount)

	// The amount takes the whole word.
	maxAmount := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	_, gotAmount, err = decodeAddressUint256(encodeAddressUint256(holder, maxAmount))
	require.NoError(t, err)
	require.Equal(t, maxAmount, gotAmount)

	for name, metadata := range map[string][]byte{
		"empty":     nil,
		"one word":  valid[:abiWordSize],
		"too short": valid[:addressUint256Bytes-1],
		"too long":  append(append([]byte(nil), valid...), 0x00),
	} {
		_, _, err := decodeAddressUint256(metadata)
		require.ErrorContains(t, err, "bytes, want 64", name)
	}

	// abi.decode rejects an address word with dirty upper bytes; so does the claimer.
	for _, dirty := range []int{0, abiAddressPadding - 1} {
		bad := append([]byte(nil), valid...)
		bad[dirty] = 0x01
		_, _, err := decodeAddressUint256(bad)
		require.ErrorContains(t, err, "non-zero padding")
	}
}

func TestMessageRoutesDecode(t *testing.T) {
	t.Parallel()
	routes, err := indexMessageRoutes([]MessageRoute{circleRoute()})
	require.NoError(t, err)

	holder := common.HexToAddress("0xA11CE00000000000000000000000000000000001")
	metadata := encodeAddressUint256(holder, big.NewInt(42))

	decoded, ok := routes.decode(leafTypeMessage, circleNetworkID, circleZkMinterBurner, 0, circleL1Escrow, metadata)
	require.True(t, ok)
	require.Equal(t, holder, decoded.beneficiary)
	require.Equal(t, big.NewInt(42), decoded.amount)
	require.Equal(t, DisplayToken{Address: circleL1USDC.Hex(), Symbol: "USDC", Decimals: 6}, decoded.token)

	other := common.HexToAddress("0xdead")
	for name, c := range map[string]struct {
		leafType                 uint8
		originNet, destNet       uint32
		originAddr, destAddr     common.Address
		metadata                 []byte
		routes                   messageRoutes
		wantDecodedForRouteCheck bool
	}{
		"not a message":           {leafTypeAsset, circleNetworkID, 0, circleZkMinterBurner, circleL1Escrow, metadata, routes, false},
		"unknown origin address":  {leafTypeMessage, circleNetworkID, 0, other, circleL1Escrow, metadata, routes, false},
		"unknown origin network":  {leafTypeMessage, 2, 0, circleZkMinterBurner, circleL1Escrow, metadata, routes, false},
		"unknown dest address":    {leafTypeMessage, circleNetworkID, 0, circleZkMinterBurner, other, metadata, routes, false},
		"unknown dest network":    {leafTypeMessage, circleNetworkID, 3, circleZkMinterBurner, circleL1Escrow, metadata, routes, false},
		"metadata of other shape": {leafTypeMessage, circleNetworkID, 0, circleZkMinterBurner, circleL1Escrow, metadata[:40], routes, false},
		"no routes configured":    {leafTypeMessage, circleNetworkID, 0, circleZkMinterBurner, circleL1Escrow, metadata, nil, false},
	} {
		decoded, ok := c.routes.decode(c.leafType, c.originNet, c.originAddr, c.destNet, c.destAddr, c.metadata)
		require.Equal(t, c.wantDecodedForRouteCheck, ok, name)
		require.Nil(t, decoded, name)
	}
}

func TestValidateMessageRoutes(t *testing.T) {
	t.Parallel()
	require.NoError(t, validateMessageRoutes(nil))
	require.NoError(t, validateMessageRoutes([]MessageRoute{circleRoute()}))

	// Same contracts on another origin network are a different route.
	otherNetwork := circleRoute()
	otherNetwork.OriginNetwork = 2
	require.NoError(t, validateMessageRoutes([]MessageRoute{circleRoute(), otherNetwork}))

	for name, c := range map[string]struct {
		mutate func(r *MessageRoute)
		want   string
	}{
		"origin address not hex":  {func(r *MessageRoute) { r.OriginAddress = "ZkMinterBurner" }, "originAddress: invalid address"},
		"origin address no 0x":    {func(r *MessageRoute) { r.OriginAddress = r.OriginAddress[2:] }, "originAddress: invalid address"},
		"origin address empty":    {func(r *MessageRoute) { r.OriginAddress = "" }, "originAddress: invalid address"},
		"origin address zero":     {func(r *MessageRoute) { r.OriginAddress = common.Address{}.Hex() }, "originAddress: the zero address"},
		"destination address bad": {func(r *MessageRoute) { r.DestinationAddress = "0x1234" }, "destinationAddress: invalid address"},
		"layout missing":          {func(r *MessageRoute) { r.MetadataLayout = "" }, `unsupported metadataLayout ""`},
		"layout unknown":          {func(r *MessageRoute) { r.MetadataLayout = "uint256,address" }, "unsupported metadataLayout"},
		"token address bad":       {func(r *MessageRoute) { r.DisplayToken.Address = "0xzz" }, "displayToken.address: invalid address"},
		"token missing":           {func(r *MessageRoute) { r.DisplayToken = DisplayToken{} }, "displayToken.address"},
		"token symbol empty":      {func(r *MessageRoute) { r.DisplayToken.Symbol = " " }, "displayToken.symbol is required"},
		"token decimals negative": {func(r *MessageRoute) { r.DisplayToken.Decimals = -1 }, "displayToken.decimals -1 out of range"},
		"token decimals too big":  {func(r *MessageRoute) { r.DisplayToken.Decimals = 78 }, "displayToken.decimals 78 out of range"},
	} {
		r := circleRoute()
		c.mutate(&r)
		err := validateMessageRoutes([]MessageRoute{circleRoute(), r})
		require.ErrorContains(t, err, "messageRoutes[1]", name)
		require.ErrorContains(t, err, c.want, name)
	}

	// The same ends twice (whatever the address casing) are ambiguous.
	dup := circleRoute()
	dup.OriginAddress = "0xbda0b27f93b2fd3f076725b89cf02e48609bc189"
	require.ErrorContains(t, validateMessageRoutes([]MessageRoute{circleRoute(), dup}), "duplicate route")
}

const circleRoutesTOML = `
[[messageRoutes]]
originNetwork = 1
originAddress = "0xBDa0B27f93B2FD3f076725b89cf02e48609bC189"
destinationNetwork = 0
destinationAddress = "0x70E70e58ed7B1Cec0D8ef7464072ED8A52d755eB"
metadataLayout = "address,uint256"
displayToken = { address = "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", symbol = "USDC", decimals = 6 }
`

const circleRoutesJSON = `{"messageRoutes": [{
  "originNetwork": 1,
  "originAddress": "0xBDa0B27f93B2FD3f076725b89cf02e48609bC189",
  "destinationNetwork": 0,
  "destinationAddress": "0x70E70e58ed7B1Cec0D8ef7464072ED8A52d755eB",
  "metadataLayout": "address,uint256",
  "displayToken": {"address": "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", "symbol": "USDC", "decimals": 6}
}]}`

func TestLoadMessageRoutesFile(t *testing.T) {
	t.Parallel()
	write := func(name, content string) string {
		path := filepath.Join(t.TempDir(), name)
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
		return path
	}

	for name, content := range map[string]string{"routes.toml": circleRoutesTOML, "routes.json": circleRoutesJSON} {
		routes, err := LoadMessageRoutesFile(write(name, content))
		require.NoError(t, err, name)
		require.Equal(t, []MessageRoute{circleRoute()}, routes, name)
	}

	_, err := LoadMessageRoutesFile(filepath.Join(t.TempDir(), "missing.toml"))
	require.ErrorContains(t, err, "reading message routes")
	_, err = LoadMessageRoutesFile(write("bad.toml", "[[messageRoutes"))
	require.ErrorContains(t, err, "parsing TOML message routes")
	_, err = LoadMessageRoutesFile(write("bad.json", "{"))
	require.ErrorContains(t, err, "parsing message routes")
	_, err = LoadMessageRoutesFile(write("empty.json", "{}"))
	require.ErrorContains(t, err, "no messageRoutes defined")
	_, err = LoadMessageRoutesFile(write("invalid.json", `{"messageRoutes": [{"originAddress": "0x01"}]}`))
	require.ErrorContains(t, err, "messageRoutes[0]: originAddress")
}
