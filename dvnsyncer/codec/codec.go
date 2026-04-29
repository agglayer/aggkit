// Package codec provides encoding/decoding helpers for LayerZero packet data
// and AggLayer OFT payload types used by the DVN syncer.
package codec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	// packetHeaderLen is the fixed byte-length of a LayerZero PacketV1 header.
	packetHeaderLen = 81

	// packetGUIDLen is the fixed byte-length of the GUID field appended after the header.
	packetGUIDLen = 32

	// minPacketLen is the minimum valid length of an encodedPayload (header + guid).
	minPacketLen = packetHeaderLen + packetGUIDLen

	// oftMessageMinLen is the minimum valid length of an OFT message (sendTo + amountSD).
	oftMessageMinLen = 40

	// oftMessageComposeSenderLen is the length of the composeSender field in an OFT message with compose.
	oftMessageComposeSenderLen = 32
)

// AggLayerMagic is the magic bytes that identify an AggLayerOFTPayloadV1 encoded message.
var AggLayerMagic = [4]byte{'A', 'L', 'O', '1'}

// aggLayerOFTPayloadVersion is the expected version field for AggLayerOFTPayloadV1.
const aggLayerOFTPayloadVersion = uint16(1)

// ErrInvalidMagicOrVersion is returned when magic or version don't match expected values.
var ErrInvalidMagicOrVersion = errors.New("invalid AggLayerOFTPayloadV1 magic or version")

// PacketHeader holds the decoded fields from the 81-byte LayerZero packet header.
type PacketHeader struct {
	Version  uint8
	Nonce    uint64
	SrcEid   uint32
	Sender   [32]byte
	DstEid   uint32
	Receiver [32]byte
}

// DecodedPacket holds all fields decoded from an encodedPayload byte slice.
type DecodedPacket struct {
	Header      PacketHeader
	GUID        [32]byte
	Message     []byte
	PayloadHash [32]byte // keccak256(guid || message)
}

// AggLayerOFTPayloadV1 is the Go equivalent of the Solidity AggLayerOFTPayloadV1 struct.
type AggLayerOFTPayloadV1 struct {
	Magic       [4]byte
	Version     uint16
	OFTMessage  []byte
	GlobalIndex *big.Int
}

// OFTMessage holds the decoded fields from the inner OFT message bytes.
type OFTMessage struct {
	SendTo     [32]byte
	AmountSD   uint64
	ComposeMsg []byte // nil if no compose
}

// aggLayerOFTPayloadArgs holds the four ABI arguments as a flat tuple for Pack/Unpack.
// go-ethereum's Pack method accepts the argument value for a tuple type as a struct
// whose exported field names (case-insensitively) match the ABI field names.
var (
	aggLayerOFTPayloadArgs abi.Arguments

	// bytes4Type, uint16Type, etc. are pre-built for use in the ABI arguments.
	bytes4Type  abi.Type
	uint16Type  abi.Type
	bytesType   abi.Type
	uint256Type abi.Type
)

func init() {
	var err error

	// Build individual component types for use in flat Arguments (avoids tuple nesting issues).
	bytes4Type, err = abi.NewType("bytes4", "", nil)
	if err != nil {
		panic(fmt.Sprintf("dvnsyncer/codec: failed to build bytes4 type: %v", err))
	}
	uint16Type, err = abi.NewType("uint16", "", nil)
	if err != nil {
		panic(fmt.Sprintf("dvnsyncer/codec: failed to build uint16 type: %v", err))
	}
	bytesType, err = abi.NewType("bytes", "", nil)
	if err != nil {
		panic(fmt.Sprintf("dvnsyncer/codec: failed to build bytes type: %v", err))
	}
	uint256Type, err = abi.NewType("uint256", "", nil)
	if err != nil {
		panic(fmt.Sprintf("dvnsyncer/codec: failed to build uint256 type: %v", err))
	}

	// AggLayerOFTPayloadV1 is ABI-encoded as abi.encode(magic, version, oftMessage, globalIndex),
	// which is the flat encoding of its four scalar fields — not a tuple wrapper.
	aggLayerOFTPayloadArgs = abi.Arguments{
		{Name: "magic", Type: bytes4Type},
		{Name: "version", Type: uint16Type},
		{Name: "oftMessage", Type: bytesType},
		{Name: "globalIndex", Type: uint256Type},
	}
}

// EncodeAggLayerOFTPayloadV1 ABI-encodes an AggLayerOFTPayloadV1.
// The encoding mirrors the Solidity `abi.encode(magic, version, oftMessage, globalIndex)`.
func EncodeAggLayerOFTPayloadV1(p AggLayerOFTPayloadV1) ([]byte, error) {
	gi := p.GlobalIndex
	if gi == nil {
		gi = new(big.Int)
	}
	packed, err := aggLayerOFTPayloadArgs.Pack(p.Magic, p.Version, p.OFTMessage, gi)
	if err != nil {
		return nil, fmt.Errorf("EncodeAggLayerOFTPayloadV1: ABI pack failed: %w", err)
	}
	return packed, nil
}

// DecodeAggLayerOFTPayloadV1 ABI-decodes an AggLayerOFTPayloadV1 from its ABI-encoded form.
// It validates the magic bytes and version before returning.
func DecodeAggLayerOFTPayloadV1(data []byte) (AggLayerOFTPayloadV1, error) {
	vals, err := aggLayerOFTPayloadArgs.Unpack(data)
	if err != nil {
		return AggLayerOFTPayloadV1{}, fmt.Errorf("DecodeAggLayerOFTPayloadV1: ABI unpack failed: %w", err)
	}
	if len(vals) != 4 { //nolint:mnd
		return AggLayerOFTPayloadV1{}, fmt.Errorf("DecodeAggLayerOFTPayloadV1: expected 4 values, got %d", len(vals))
	}

	magic, ok := vals[0].([4]byte)
	if !ok {
		return AggLayerOFTPayloadV1{}, fmt.Errorf("DecodeAggLayerOFTPayloadV1: magic field type mismatch: %T", vals[0])
	}
	version, ok := vals[1].(uint16)
	if !ok {
		return AggLayerOFTPayloadV1{}, fmt.Errorf("DecodeAggLayerOFTPayloadV1: version field type mismatch: %T", vals[1])
	}
	oftMessage, ok := vals[2].([]byte)
	if !ok {
		return AggLayerOFTPayloadV1{}, fmt.Errorf("DecodeAggLayerOFTPayloadV1: oftMessage field type mismatch: %T", vals[2])
	}
	globalIndex, ok := vals[3].(*big.Int)
	if !ok {
		return AggLayerOFTPayloadV1{}, fmt.Errorf("DecodeAggLayerOFTPayloadV1: globalIndex field type mismatch: %T", vals[3])
	}

	if magic != AggLayerMagic || version != aggLayerOFTPayloadVersion {
		return AggLayerOFTPayloadV1{}, ErrInvalidMagicOrVersion
	}

	return AggLayerOFTPayloadV1{
		Magic:       magic,
		Version:     version,
		OFTMessage:  oftMessage,
		GlobalIndex: globalIndex,
	}, nil
}

// DecodePacketHeader decodes a 81-byte LayerZero packet header.
// Layout:
//
//	bytes[0:1]   = version (uint8)
//	bytes[1:9]   = nonce (uint64, big-endian)
//	bytes[9:13]  = srcEid (uint32, big-endian)
//	bytes[13:45] = sender (bytes32)
//	bytes[45:49] = dstEid (uint32, big-endian)
//	bytes[49:81] = receiver (bytes32)
func DecodePacketHeader(data []byte) (PacketHeader, error) {
	if len(data) < packetHeaderLen {
		return PacketHeader{}, fmt.Errorf("packet header too short: got %d bytes, need %d", len(data), packetHeaderLen)
	}

	var h PacketHeader
	h.Version = data[0]
	h.Nonce = binary.BigEndian.Uint64(data[1:9])
	h.SrcEid = binary.BigEndian.Uint32(data[9:13])
	copy(h.Sender[:], data[13:45])
	h.DstEid = binary.BigEndian.Uint32(data[45:49])
	copy(h.Receiver[:], data[49:81])
	return h, nil
}

// DecodePacket decodes a full LayerZero encodedPayload.
// encodedPayload = header(81) || guid(32) || message(variable)
// payloadHash = keccak256(guid || message)
func DecodePacket(encodedPayload []byte) (DecodedPacket, error) {
	if len(encodedPayload) < minPacketLen {
		return DecodedPacket{}, fmt.Errorf(
			"encodedPayload too short: got %d bytes, need at least %d", len(encodedPayload), minPacketLen)
	}

	header, err := DecodePacketHeader(encodedPayload[:packetHeaderLen])
	if err != nil {
		return DecodedPacket{}, fmt.Errorf("DecodePacket: %w", err)
	}

	var guid [32]byte
	copy(guid[:], encodedPayload[packetHeaderLen:packetHeaderLen+packetGUIDLen])

	message := encodedPayload[minPacketLen:]

	// payloadHash = keccak256(guid || message)
	payloadData := encodedPayload[packetHeaderLen:] // guid + message
	payloadHash := crypto.Keccak256Hash(payloadData)

	var phArr [32]byte
	copy(phArr[:], payloadHash.Bytes())

	return DecodedPacket{
		Header:      header,
		GUID:        guid,
		Message:     message,
		PayloadHash: phArr,
	}, nil
}

// DecodeOFTMessage decodes the inner OFT message bytes.
//
// Without compose (len == 40): sendTo = msg[0:32], amountSD = msg[32:40]
// With compose    (len >  40): sendTo, amountSD, composeSender(msg[40:72]) stripped, composeMsg = msg[72:]
func DecodeOFTMessage(msg []byte) (OFTMessage, error) {
	if len(msg) < oftMessageMinLen {
		return OFTMessage{}, fmt.Errorf(
			"OFT message too short: got %d bytes, need at least %d", len(msg), oftMessageMinLen)
	}

	var out OFTMessage
	copy(out.SendTo[:], msg[0:32])
	out.AmountSD = binary.BigEndian.Uint64(msg[32:40])

	if len(msg) > oftMessageMinLen {
		// composeMsg starts at byte 72 (skip 32-byte composeSender at [40:72])
		composeStart := oftMessageMinLen + oftMessageComposeSenderLen
		if len(msg) >= composeStart {
			raw := msg[composeStart:]
			if len(raw) > 0 {
				out.ComposeMsg = make([]byte, len(raw))
				copy(out.ComposeMsg, raw)
			}
		}
	}

	return out, nil
}

