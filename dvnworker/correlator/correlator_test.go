package correlator_test

import (
	"context"
	"encoding/binary"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"

	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/dvnsyncer/codec"
	"github.com/agglayer/aggkit/dvnsyncer/db"
	"github.com/agglayer/aggkit/dvnworker/correlator"
	"github.com/agglayer/aggkit/log"
)

// ─── Test fixtures ─────────────────────────────────────────────────────────

var (
	testSourceOFT      = common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	testDestOFT        = common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
	testSourceToken    = common.HexToAddress("0xCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC")
	testDestToken      = common.HexToAddress("0xDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD")
	testRecipient      = common.HexToAddress("0xEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE")

	testSourceNetwork uint32 = 2 // rollup index 1 → network 2
	testDestNetwork   uint32 = 3
	testDepositCount  uint32 = 7

	// amountSD in shared decimals (6), amountLD in local decimals (18): rate = 10^12
	testAmountSD  uint64   = 1_000_000 // 1 USDT in shared decimals
	testRate             = new(big.Int).Exp(big.NewInt(10), big.NewInt(12), nil)
	testAmountLD         = new(big.Int).Mul(new(big.Int).SetUint64(testAmountSD), testRate)

	testRouteConfig = correlator.RouteConfig{
		SourceBridgeNetwork:      testSourceNetwork,
		DestinationBridgeNetwork: testDestNetwork,
		SourceOFT:                testSourceOFT,
		DestinationOFT:           testDestOFT,
		SourceToken:              testSourceToken,
		DestinationToken:         testDestToken,
		DecimalConversionRate:    testRate,
	}
)

// ─── Helpers ───────────────────────────────────────────────────────────────

// buildGlobalIndex builds a globalIndex for a rollup network.
// rollupIndex = sourceBridgeNetwork - 1; depositCount = local leaf index.
// Layout: bits[63:32] = rollupIndex, bits[31:0] = depositCount, bit64 = 0 (not mainnet).
func buildGlobalIndex(sourceBridgeNetwork, depositCount uint32) *big.Int {
	rollupIndex := uint64(sourceBridgeNetwork - 1)
	gi := (rollupIndex << 32) | uint64(depositCount)
	return new(big.Int).SetUint64(gi)
}

// buildOFTMessage builds a minimal OFT message (40 bytes: sendTo + amountSD).
func buildOFTMessage(sendTo common.Address, amountSD uint64) []byte {
	msg := make([]byte, 40)
	// sendTo right-padded as bytes32: address sits in the right-most 20 bytes.
	copy(msg[12:32], sendTo.Bytes())
	binary.BigEndian.PutUint64(msg[32:40], amountSD)
	return msg
}

// buildPacketMessage encodes an AggLayerOFTPayloadV1 as the LZ packet message.
func buildPacketMessage(t *testing.T, globalIndex *big.Int, sendTo common.Address, amountSD uint64) []byte {
	t.Helper()
	oftMsg := buildOFTMessage(sendTo, amountSD)
	payload, err := codec.EncodeAggLayerOFTPayloadV1(codec.AggLayerOFTPayloadV1{
		Magic:      codec.AggLayerMagic,
		Version:    1,
		OFTMessage: oftMsg,
		GlobalIndex: globalIndex,
	})
	require.NoError(t, err)
	return payload
}

// buildPacketRecord builds a valid PacketRecord for the test fixture.
func buildPacketRecord(t *testing.T, senderBytes32, receiverBytes32 string, globalIndex *big.Int, message []byte) db.PacketRecord {
	t.Helper()
	// GUID is arbitrary for tests; payloadHash = keccak256(guid || message).
	var guid [32]byte
	guid[0] = 0xDE
	guid[1] = 0xAD

	payloadHash := crypto.Keccak256Hash(append(guid[:], message...))

	return db.PacketRecord{
		ChainID:     1,
		BlockNum:    100,
		TxHash:      "0xabc",
		LogIndex:    0,
		SrcEid:      101,
		Sender:      senderBytes32,
		DstEid:      202,
		Receiver:    receiverBytes32,
		Nonce:       1,
		GUID:        "0x" + common.Bytes2Hex(guid[:]),
		Message:     message,
		PayloadHash: payloadHash.Hex(),
		GlobalIndex: globalIndex,
		OFTSendTo:   testRecipient.Hex(),
		OFTAmountSD: testAmountSD,
	}
}

// buildJobRecord builds a valid JobAssignedRecord whose PayloadHash matches a PacketRecord.
func buildJobRecord(payloadHash string) db.JobAssignedRecord {
	return db.JobAssignedRecord{
		ChainID:       1,
		BlockNum:      100,
		TxHash:        "0xabc",
		LogIndex:      0,
		PayloadHash:   payloadHash,
		DstEid:        202,
		Sender:        addrToBytes32Hex(testSourceOFT),
		Fee:           big.NewInt(1000),
		Confirmations: 10,
	}
}

// buildBridge builds a valid bridgesync.Bridge for the test fixture.
func buildBridge() *bridgesync.Bridge {
	return &bridgesync.Bridge{
		BlockNum:           100,
		LeafType:           0,
		OriginNetwork:      testSourceNetwork,
		OriginAddress:      testSourceToken,
		DestinationNetwork: testDestNetwork,
		DestinationAddress: testDestOFT,
		Amount:             new(big.Int).Set(testAmountLD),
		DepositCount:       testDepositCount,
	}
}

// addrToBytes32Hex converts an Ethereum address to the "0x"+bytes32 hex string
// (address right-padded to 32 bytes, address in the last 20 bytes).
func addrToBytes32Hex(addr common.Address) string {
	var b [32]byte
	copy(b[12:], addr.Bytes())
	return "0x" + common.Bytes2Hex(b[:])
}

// defaultFixtures returns valid job, packet, bridge, and lookup for a passing scenario.
func defaultFixtures(t *testing.T) (db.JobAssignedRecord, db.PacketRecord, *bridgesync.Bridge, func(context.Context, uint32) (*bridgesync.Bridge, error)) {
	t.Helper()
	globalIndex := buildGlobalIndex(testSourceNetwork, testDepositCount)
	message := buildPacketMessage(t, globalIndex, testRecipient, testAmountSD)
	packet := buildPacketRecord(t, addrToBytes32Hex(testSourceOFT), addrToBytes32Hex(testDestOFT), globalIndex, message)
	job := buildJobRecord(packet.PayloadHash)
	bridge := buildBridge()
	lookup := func(_ context.Context, depositCount uint32) (*bridgesync.Bridge, error) {
		if depositCount == testDepositCount {
			return bridge, nil
		}
		return nil, nil
	}
	return job, packet, bridge, lookup
}

func testLogger(_ *testing.T) *log.Logger {
	return log.GetDefaultLogger()
}

// ─── Tests ─────────────────────────────────────────────────────────────────

// TestValidate_AllChecksPass is the happy-path test: all 9 checks should pass.
func TestValidate_AllChecksPass(t *testing.T) {
	t.Parallel()
	job, packet, _, lookup := defaultFixtures(t)
	c := correlator.New(testRouteConfig, testLogger(t))

	result := c.Validate(context.Background(), job, packet, lookup)
	require.True(t, result.Accepted, "expected Accepted=true, got rejectCode=%s", result.RejectCode)
}

// TestValidate_C1_SenderNotAuthorized mutates packet.Sender to a wrong address.
func TestValidate_C1_SenderNotAuthorized(t *testing.T) {
	t.Parallel()
	job, packet, _, lookup := defaultFixtures(t)
	wrongSender := common.HexToAddress("0x1111111111111111111111111111111111111111")
	packet.Sender = addrToBytes32Hex(wrongSender)

	c := correlator.New(testRouteConfig, testLogger(t))
	result := c.Validate(context.Background(), job, packet, lookup)
	require.False(t, result.Accepted)
	require.Contains(t, result.RejectCode, "C1")
}

// TestValidate_C2_ReceiverNotDestinationOFT mutates packet.Receiver to a wrong address.
func TestValidate_C2_ReceiverNotDestinationOFT(t *testing.T) {
	t.Parallel()
	job, packet, _, lookup := defaultFixtures(t)
	wrongReceiver := common.HexToAddress("0x2222222222222222222222222222222222222222")
	packet.Receiver = addrToBytes32Hex(wrongReceiver)

	c := correlator.New(testRouteConfig, testLogger(t))
	result := c.Validate(context.Background(), job, packet, lookup)
	require.False(t, result.Accepted)
	require.Contains(t, result.RejectCode, "C2")
}

// TestValidate_C3_PayloadHashMismatch mutates job.PayloadHash to a wrong hash.
func TestValidate_C3_PayloadHashMismatch(t *testing.T) {
	t.Parallel()
	job, packet, _, lookup := defaultFixtures(t)
	job.PayloadHash = "0x0000000000000000000000000000000000000000000000000000000000000000"

	c := correlator.New(testRouteConfig, testLogger(t))
	result := c.Validate(context.Background(), job, packet, lookup)
	require.False(t, result.Accepted)
	require.Contains(t, result.RejectCode, "C3")
}

// TestValidate_C4_GlobalIndexNil sets packet.GlobalIndex to nil.
func TestValidate_C4_GlobalIndexNil(t *testing.T) {
	t.Parallel()
	job, packet, _, lookup := defaultFixtures(t)
	packet.GlobalIndex = nil

	c := correlator.New(testRouteConfig, testLogger(t))
	result := c.Validate(context.Background(), job, packet, lookup)
	require.False(t, result.Accepted)
	require.Contains(t, result.RejectCode, "C4")
}

// TestValidate_C5_DepositCountMismatch: bridge lookup returns a bridge with a different depositCount.
func TestValidate_C5_DepositCountMismatch(t *testing.T) {
	t.Parallel()
	job, packet, bridge, _ := defaultFixtures(t)

	// Tamper: the bridge returned has a different depositCount than what globalIndex decodes to.
	bridge.DepositCount = testDepositCount + 1
	lookup := func(_ context.Context, _ uint32) (*bridgesync.Bridge, error) {
		return bridge, nil
	}

	c := correlator.New(testRouteConfig, testLogger(t))
	result := c.Validate(context.Background(), job, packet, lookup)
	require.False(t, result.Accepted)
	require.Contains(t, result.RejectCode, "C5")
}

// TestValidate_C6_SourceNetworkMismatch: use a globalIndex that encodes a different network.
func TestValidate_C6_SourceNetworkMismatch(t *testing.T) {
	t.Parallel()
	// Build globalIndex for a different source network (network 99).
	wrongNetwork := uint32(99)
	globalIndex := buildGlobalIndex(wrongNetwork, testDepositCount)
	message := buildPacketMessage(t, globalIndex, testRecipient, testAmountSD)
	packet := buildPacketRecord(t, addrToBytes32Hex(testSourceOFT), addrToBytes32Hex(testDestOFT), globalIndex, message)
	job := buildJobRecord(packet.PayloadHash)
	bridge := buildBridge()
	bridge.DepositCount = testDepositCount
	lookup := func(_ context.Context, depositCount uint32) (*bridgesync.Bridge, error) {
		if depositCount == testDepositCount {
			return bridge, nil
		}
		return nil, nil
	}

	c := correlator.New(testRouteConfig, testLogger(t))
	result := c.Validate(context.Background(), job, packet, lookup)
	require.False(t, result.Accepted)
	require.Contains(t, result.RejectCode, "C6")
}

// TestValidate_C7_AmountMismatch: bridge.Amount doesn't match amountSD * rate.
func TestValidate_C7_AmountMismatch(t *testing.T) {
	t.Parallel()
	job, packet, bridge, lookup := defaultFixtures(t)
	// Change bridge.Amount to something wrong.
	bridge.Amount = new(big.Int).Add(testAmountLD, big.NewInt(1))
	_ = lookup // reuse lookup but with tampered bridge in closure
	wrongLookup := func(_ context.Context, depositCount uint32) (*bridgesync.Bridge, error) {
		if depositCount == testDepositCount {
			return bridge, nil
		}
		return nil, nil
	}

	c := correlator.New(testRouteConfig, testLogger(t))
	result := c.Validate(context.Background(), job, packet, wrongLookup)
	require.False(t, result.Accepted)
	require.Contains(t, result.RejectCode, "C7")
}

// TestValidate_C8_RecipientIsDestinationAddress: bridge.DestinationAddress == LZ recipient.
func TestValidate_C8_RecipientIsDestinationAddress(t *testing.T) {
	t.Parallel()
	// Build a message where sendTo == testRecipient, but set bridge.DestinationAddress = testRecipient
	// (instead of testDestOFT) so C8 fires. We also need C9 to pass, but since we're testing C8,
	// the bridge should otherwise be valid except for this field.
	// Note: C8 fires before C9, so we only need bridge.DestinationAddress = recipient.
	job, packet, bridge, _ := defaultFixtures(t)
	// Set bridge.DestinationAddress to the LZ recipient (testRecipient).
	bridge.DestinationAddress = testRecipient
	lookup := func(_ context.Context, depositCount uint32) (*bridgesync.Bridge, error) {
		if depositCount == testDepositCount {
			return bridge, nil
		}
		return nil, nil
	}

	c := correlator.New(testRouteConfig, testLogger(t))
	result := c.Validate(context.Background(), job, packet, lookup)
	require.False(t, result.Accepted)
	require.Contains(t, result.RejectCode, "C8")
}

// TestValidate_C9_OriginNetworkMismatch: bridge.OriginNetwork doesn't match config.
func TestValidate_C9_OriginNetworkMismatch(t *testing.T) {
	t.Parallel()
	job, packet, bridge, _ := defaultFixtures(t)
	bridge.OriginNetwork = testSourceNetwork + 100
	lookup := func(_ context.Context, depositCount uint32) (*bridgesync.Bridge, error) {
		if depositCount == testDepositCount {
			return bridge, nil
		}
		return nil, nil
	}

	c := correlator.New(testRouteConfig, testLogger(t))
	result := c.Validate(context.Background(), job, packet, lookup)
	require.False(t, result.Accepted)
	require.Contains(t, result.RejectCode, "C9")
}
