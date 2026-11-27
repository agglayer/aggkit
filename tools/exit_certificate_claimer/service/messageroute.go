package claimer

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

const (
	// MetadataLayoutAddressUint256 is the metadata layout abi.encode(address beneficiary, uint256 amount):
	// two 32-byte words, the first one an address left-padded with zeros. It is the only layout supported.
	MetadataLayoutAddressUint256 = "address,uint256"

	// maxDisplayTokenDecimals bounds displayToken.decimals: a uint256 amount has at most 78 digits.
	maxDisplayTokenDecimals = 77

	abiWordSize         = 32
	abiAddressPadding   = abiWordSize - common.AddressLength
	addressUint256Bytes = 2 * abiWordSize
)

// MessageRoute tells the claimer how to read the metadata of the Message exits (bridgeMessage) sent
// by one contract to another: who the beneficiary is, which amount it is paid, and in which token the
// destination contract pays it. With it, API v2 finds a Message exit by its beneficiary (the
// destination address of a message is the contract that receives it, not the holder).
type MessageRoute struct {
	// OriginNetwork and OriginAddress identify the contract that called bridgeMessage.
	OriginNetwork uint32 `json:"originNetwork"`
	OriginAddress string `json:"originAddress"`
	// DestinationNetwork and DestinationAddress identify the contract that receives the message.
	DestinationNetwork uint32 `json:"destinationNetwork"`
	DestinationAddress string `json:"destinationAddress"`
	// MetadataLayout is the ABI layout of the message metadata. Only "address,uint256" is supported.
	MetadataLayout string `json:"metadataLayout"`
	// DisplayToken is the token the destination contract pays the beneficiary with. It is only
	// returned to the API client, to render the decoded amount.
	DisplayToken DisplayToken `json:"displayToken"`
}

// DisplayToken describes the token a message route pays out. It is the config form and the JSON form
// returned by API v2 (display_token).
type DisplayToken struct {
	Address  string `json:"address"`
	Symbol   string `json:"symbol"`
	Decimals int    `json:"decimals"`
}

// messageRouteKey identifies a message route by its two ends.
type messageRouteKey struct {
	originNetwork      uint32
	originAddress      common.Address
	destinationNetwork uint32
	destinationAddress common.Address
}

// messageRoutes is the validated, indexed form of the configured message routes.
type messageRoutes map[messageRouteKey]DisplayToken

// decodedMessage is the content of a Message exit decoded through its route.
type decodedMessage struct {
	beneficiary common.Address
	amount      *big.Int
	token       DisplayToken
}

// validateMessageRoutes checks every route (addresses, layout, display token) and rejects two routes
// with the same ends.
func validateMessageRoutes(routes []MessageRoute) error {
	_, err := indexMessageRoutes(routes)
	return err
}

// indexMessageRoutes validates the routes and indexes them by their ends. The display token address is
// normalized to its checksummed form.
func indexMessageRoutes(routes []MessageRoute) (messageRoutes, error) {
	indexed := make(messageRoutes, len(routes))
	for i := range routes {
		r := routes[i]
		originAddress, err := parseRouteAddress(r.OriginAddress)
		if err != nil {
			return nil, fmt.Errorf("messageRoutes[%d]: originAddress: %w", i, err)
		}
		destinationAddress, err := parseRouteAddress(r.DestinationAddress)
		if err != nil {
			return nil, fmt.Errorf("messageRoutes[%d]: destinationAddress: %w", i, err)
		}
		if r.MetadataLayout != MetadataLayoutAddressUint256 {
			return nil, fmt.Errorf("messageRoutes[%d]: unsupported metadataLayout %q (supported: %q)",
				i, r.MetadataLayout, MetadataLayoutAddressUint256)
		}
		tokenAddress, err := parseRouteAddress(r.DisplayToken.Address)
		if err != nil {
			return nil, fmt.Errorf("messageRoutes[%d]: displayToken.address: %w", i, err)
		}
		if strings.TrimSpace(r.DisplayToken.Symbol) == "" {
			return nil, fmt.Errorf("messageRoutes[%d]: displayToken.symbol is required", i)
		}
		if r.DisplayToken.Decimals < 0 || r.DisplayToken.Decimals > maxDisplayTokenDecimals {
			return nil, fmt.Errorf("messageRoutes[%d]: displayToken.decimals %d out of range [0, %d]",
				i, r.DisplayToken.Decimals, maxDisplayTokenDecimals)
		}

		key := messageRouteKey{
			originNetwork:      r.OriginNetwork,
			originAddress:      originAddress,
			destinationNetwork: r.DestinationNetwork,
			destinationAddress: destinationAddress,
		}
		if _, dup := indexed[key]; dup {
			return nil, fmt.Errorf("messageRoutes[%d]: duplicate route (%d, %s) -> (%d, %s)", i,
				r.OriginNetwork, originAddress.Hex(), r.DestinationNetwork, destinationAddress.Hex())
		}
		indexed[key] = DisplayToken{
			Address:  tokenAddress.Hex(),
			Symbol:   r.DisplayToken.Symbol,
			Decimals: r.DisplayToken.Decimals,
		}
	}
	return indexed, nil
}

// parseRouteAddress parses a non-zero 0x-prefixed address of a message route.
func parseRouteAddress(s string) (common.Address, error) {
	if !strings.HasPrefix(s, "0x") || !common.IsHexAddress(s) {
		return common.Address{}, fmt.Errorf("invalid address %q", s)
	}
	addr := common.HexToAddress(s)
	if addr == (common.Address{}) {
		return common.Address{}, errors.New("the zero address is not allowed")
	}
	return addr, nil
}

// decode returns the beneficiary and amount of a Message exit when its route is configured and its
// metadata follows the route's layout. ok is false for an exit that is not a message, an unknown
// route, or metadata that does not decode: such an exit is then only matched by destination address.
func (m messageRoutes) decode(
	leafType uint8, originNetwork uint32, originAddress common.Address,
	destinationNetwork uint32, destinationAddress common.Address, metadata []byte,
) (*decodedMessage, bool) {
	if leafType != leafTypeMessage || len(m) == 0 {
		return nil, false
	}
	token, known := m[messageRouteKey{
		originNetwork:      originNetwork,
		originAddress:      originAddress,
		destinationNetwork: destinationNetwork,
		destinationAddress: destinationAddress,
	}]
	if !known {
		return nil, false
	}
	beneficiary, amount, err := decodeAddressUint256(metadata)
	if err != nil {
		return nil, false
	}
	return &decodedMessage{beneficiary: beneficiary, amount: amount, token: token}, true
}

// decodeAddressUint256 decodes abi.encode(address, uint256). It is strict: exactly two words, and the
// 12 bytes that pad the address must be zero (as Solidity's abi.decode requires).
func decodeAddressUint256(metadata []byte) (common.Address, *big.Int, error) {
	if len(metadata) != addressUint256Bytes {
		return common.Address{}, nil, fmt.Errorf("metadata is %d bytes, want %d", len(metadata), addressUint256Bytes)
	}
	for _, b := range metadata[:abiAddressPadding] {
		if b != 0 {
			return common.Address{}, nil, errors.New("the address word has non-zero padding")
		}
	}
	beneficiary := common.BytesToAddress(metadata[abiAddressPadding:abiWordSize])
	amount := new(big.Int).SetBytes(metadata[abiWordSize:])
	return beneficiary, amount, nil
}

// messageRoutesFile is the content of a --message-routes file.
type messageRoutesFile struct {
	MessageRoutes []MessageRoute `json:"messageRoutes"`
}

// LoadMessageRoutesFile reads and validates a message routes file: a JSON or TOML document (selected by
// the .toml extension, as for the config) with a messageRoutes list.
func LoadMessageRoutesFile(path string) ([]MessageRoute, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading message routes %q: %w", path, err)
	}
	if strings.EqualFold(filepath.Ext(path), ".toml") {
		raw, err = tomlToJSON(raw)
		if err != nil {
			return nil, fmt.Errorf("parsing TOML message routes %q: %w", path, err)
		}
	}
	var file messageRoutesFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parsing message routes %q: %w", path, err)
	}
	if len(file.MessageRoutes) == 0 {
		return nil, fmt.Errorf("message routes %q: no messageRoutes defined", path)
	}
	if err := validateMessageRoutes(file.MessageRoutes); err != nil {
		return nil, fmt.Errorf("message routes %q: %w", path, err)
	}
	return file.MessageRoutes, nil
}
