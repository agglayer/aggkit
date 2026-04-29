package codec_test

import (
	"encoding/binary"
	"math/big"
	"testing"

	"github.com/agglayer/aggkit/dvnsyncer/codec"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

// makeEncodedPayload builds a synthetic encodedPayload from its parts.
func makeEncodedPayload(version uint8, nonce uint64, srcEid uint32, sender [32]byte,
	dstEid uint32, receiver [32]byte, guid [32]byte, message []byte) []byte {
	buf := make([]byte, 81+32+len(message))
	buf[0] = version
	binary.BigEndian.PutUint64(buf[1:9], nonce)
	binary.BigEndian.PutUint32(buf[9:13], srcEid)
	copy(buf[13:45], sender[:])
	binary.BigEndian.PutUint32(buf[45:49], dstEid)
	copy(buf[49:81], receiver[:])
	copy(buf[81:113], guid[:])
	copy(buf[113:], message)
	return buf
}

// TestDecodePacketHeader checks that DecodePacketHeader correctly pulls all fields.
func TestDecodePacketHeader(t *testing.T) {
	t.Parallel()

	var sender, receiver [32]byte
	sender[31] = 0xAA
	receiver[31] = 0xBB

	payload := makeEncodedPayload(1, 42, 101, sender, 202, receiver, [32]byte{}, nil)
	hdr, err := codec.DecodePacketHeader(payload[:81])
	require.NoError(t, err)
	require.Equal(t, uint8(1), hdr.Version)
	require.Equal(t, uint64(42), hdr.Nonce)
	require.Equal(t, uint32(101), hdr.SrcEid)
	require.Equal(t, sender, hdr.Sender)
	require.Equal(t, uint32(202), hdr.DstEid)
	require.Equal(t, receiver, hdr.Receiver)
}

// TestDecodePacketHeaderTooShort checks that a slice shorter than 81 bytes returns an error.
func TestDecodePacketHeaderTooShort(t *testing.T) {
	t.Parallel()
	_, err := codec.DecodePacketHeader(make([]byte, 10))
	require.Error(t, err)
}

// TestDecodePacket checks payload hash computation and message extraction.
func TestDecodePacket(t *testing.T) {
	t.Parallel()

	var sender, receiver, guid [32]byte
	guid[0] = 0xFF
	message := []byte("hello layerzero")

	payload := makeEncodedPayload(1, 7, 10, sender, 20, receiver, guid, message)

	dp, err := codec.DecodePacket(payload)
	require.NoError(t, err)
	require.Equal(t, guid, dp.GUID)
	require.Equal(t, message, dp.Message)

	// payloadHash = keccak256(guid || message)
	expectedHash := crypto.Keccak256Hash(append(guid[:], message...))
	require.Equal(t, [32]byte(expectedHash), dp.PayloadHash)
}

// TestDecodePacketNoMessage checks a packet with an empty message (just guid).
func TestDecodePacketNoMessage(t *testing.T) {
	t.Parallel()

	var sender, receiver, guid [32]byte
	payload := makeEncodedPayload(1, 1, 1, sender, 2, receiver, guid, nil)

	dp, err := codec.DecodePacket(payload)
	require.NoError(t, err)
	require.Empty(t, dp.Message)

	expectedHash := crypto.Keccak256Hash(guid[:])
	require.Equal(t, [32]byte(expectedHash), dp.PayloadHash)
}

// TestAggLayerOFTPayloadRoundTrip encodes then decodes an AggLayerOFTPayloadV1 and checks all fields.
func TestAggLayerOFTPayloadRoundTrip(t *testing.T) {
	t.Parallel()

	original := codec.AggLayerOFTPayloadV1{
		Magic:       codec.AggLayerMagic,
		Version:     1,
		OFTMessage:  []byte{0x01, 0x02, 0x03},
		GlobalIndex: big.NewInt(12345),
	}

	encoded, err := codec.EncodeAggLayerOFTPayloadV1(original)
	require.NoError(t, err)
	require.NotEmpty(t, encoded)

	decoded, err := codec.DecodeAggLayerOFTPayloadV1(encoded)
	require.NoError(t, err)
	require.Equal(t, original.Magic, decoded.Magic)
	require.Equal(t, original.Version, decoded.Version)
	require.Equal(t, original.OFTMessage, decoded.OFTMessage)
	require.Equal(t, 0, original.GlobalIndex.Cmp(decoded.GlobalIndex))
}

// TestAggLayerOFTPayloadBadMagic checks that an incorrect magic returns ErrInvalidMagicOrVersion.
func TestAggLayerOFTPayloadBadMagic(t *testing.T) {
	t.Parallel()

	bad := codec.AggLayerOFTPayloadV1{
		Magic:       [4]byte{'X', 'X', 'X', 'X'},
		Version:     1,
		OFTMessage:  []byte{0x01},
		GlobalIndex: big.NewInt(1),
	}

	encoded, err := codec.EncodeAggLayerOFTPayloadV1(bad)
	require.NoError(t, err)

	_, err = codec.DecodeAggLayerOFTPayloadV1(encoded)
	require.ErrorIs(t, err, codec.ErrInvalidMagicOrVersion)
}

// TestDecodeOFTMessageWithoutCompose checks the 40-byte (no compose) path.
func TestDecodeOFTMessageWithoutCompose(t *testing.T) {
	t.Parallel()

	var sendTo [32]byte
	sendTo[31] = 0x42
	amountSD := uint64(9999)

	msg := make([]byte, 40)
	copy(msg[0:32], sendTo[:])
	binary.BigEndian.PutUint64(msg[32:40], amountSD)

	out, err := codec.DecodeOFTMessage(msg)
	require.NoError(t, err)
	require.Equal(t, sendTo, out.SendTo)
	require.Equal(t, amountSD, out.AmountSD)
	require.Nil(t, out.ComposeMsg)
}

// TestDecodeOFTMessageWithCompose checks the >40-byte (with compose) path.
func TestDecodeOFTMessageWithCompose(t *testing.T) {
	t.Parallel()

	var sendTo [32]byte
	sendTo[31] = 0x42
	amountSD := uint64(7777)
	composePayload := []byte("compose data here")

	// 32 (sendTo) + 8 (amountSD) + 32 (composeSender) + len(composePayload)
	msg := make([]byte, 72+len(composePayload))
	copy(msg[0:32], sendTo[:])
	binary.BigEndian.PutUint64(msg[32:40], amountSD)
	// msg[40:72] = composeSender (zeroes in this test)
	copy(msg[72:], composePayload)

	out, err := codec.DecodeOFTMessage(msg)
	require.NoError(t, err)
	require.Equal(t, sendTo, out.SendTo)
	require.Equal(t, amountSD, out.AmountSD)
	require.Equal(t, composePayload, out.ComposeMsg)
}

// TestAggLayerPayloadInPacket verifies the full end-to-end flow:
// encode an AggLayerOFTPayloadV1 as the packet message, decode the packet, then decode the payload.
func TestAggLayerPayloadInPacket(t *testing.T) {
	t.Parallel()

	original := codec.AggLayerOFTPayloadV1{
		Magic:       codec.AggLayerMagic,
		Version:     1,
		OFTMessage:  []byte{0xDE, 0xAD, 0xBE, 0xEF},
		GlobalIndex: big.NewInt(999),
	}

	encodedPayload, err := codec.EncodeAggLayerOFTPayloadV1(original)
	require.NoError(t, err)

	var sender, receiver, guid [32]byte
	guid[0] = 0x11

	fullPayload := makeEncodedPayload(1, 100, 111, sender, 222, receiver, guid, encodedPayload)

	dp, err := codec.DecodePacket(fullPayload)
	require.NoError(t, err)
	require.Equal(t, guid, dp.GUID)

	recovered, err := codec.DecodeAggLayerOFTPayloadV1(dp.Message)
	require.NoError(t, err)
	require.Equal(t, original.Magic, recovered.Magic)
	require.Equal(t, original.Version, recovered.Version)
	require.Equal(t, original.OFTMessage, recovered.OFTMessage)
	require.Equal(t, 0, original.GlobalIndex.Cmp(recovered.GlobalIndex))
}
