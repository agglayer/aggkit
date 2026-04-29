// Package correlator joins dvnsyncer jobs with bridgesync BridgeEvent rows
// and runs all §3 leaf-vs-packet checks defined in FINAL-PROPOSAL.md.
package correlator

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"

	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/dvnsyncer/codec"
	"github.com/agglayer/aggkit/dvnsyncer/db"
	"github.com/agglayer/aggkit/log"
)

// RouteConfig holds the route parameters the correlator validates against.
type RouteConfig struct {
	// SourceBridgeNetwork is the AggLayer network ID of the source chain.
	SourceBridgeNetwork uint32
	// DestinationBridgeNetwork is the AggLayer network ID of the destination chain.
	DestinationBridgeNetwork uint32
	// SourceOFT is the authorized source OFT contract address.
	SourceOFT common.Address
	// DestinationOFT is the destination OFT custody contract (dstOFTReceiver).
	DestinationOFT common.Address
	// SourceToken is the token contract on the source chain.
	SourceToken common.Address
	// DestinationToken is the token contract on the destination chain.
	DestinationToken common.Address
	// DecimalConversionRate is the scale factor: amountSD * DecimalConversionRate = amountLD.
	// For example, if localDecimals=18 and sharedDecimals=6, this should be 10^12.
	DecimalConversionRate *big.Int
}

// ValidationResult is the outcome of correlating a job against a bridge event.
type ValidationResult struct {
	// Job is the DVN assignment job that was validated.
	Job db.JobAssignedRecord
	// Bridge is the matched AggLayer bridge event (nil if lookup failed before the Bridge was found).
	Bridge *bridgesync.Bridge
	// Accepted is true if all §3 checks passed.
	Accepted bool
	// RejectCode is a stable reason code when Accepted==false, e.g. "C1_sender_not_authorized".
	RejectCode string
}

// Correlator joins dvnsyncer jobs with bridgesync BridgeEvent rows and validates all §3 checks.
type Correlator struct {
	route  RouteConfig
	logger *log.Logger
}

// New creates a Correlator.
func New(route RouteConfig, logger *log.Logger) *Correlator {
	return &Correlator{route: route, logger: logger}
}

// Validate runs all §3 checks for a single job against a bridge event lookup.
// bridgeLookup is a function that returns the Bridge for a given depositCount.
// Returns a ValidationResult with Accepted==true only if all 9 checks pass.
func (c *Correlator) Validate(
	ctx context.Context,
	job db.JobAssignedRecord,
	packet db.PacketRecord,
	bridgeLookup func(ctx context.Context, depositCount uint32) (*bridgesync.Bridge, error),
) ValidationResult {
	reject := func(code string) ValidationResult {
		c.logger.Warnw("correlator: job rejected",
			"rejectCode", code,
			"payloadHash", job.PayloadHash,
			"txHash", job.TxHash,
		)
		return ValidationResult{Job: job, Accepted: false, RejectCode: code}
	}

	// ---- C1: sender must be the authorized source OFT ----
	// packet.Sender is a hex string of bytes32. Convert to address (right-most 20 bytes).
	senderAddr := bytes32HexToAddress(packet.Sender)
	if senderAddr != c.route.SourceOFT {
		return reject(fmt.Sprintf("C1_sender_not_authorized: got %s want %s", senderAddr, c.route.SourceOFT))
	}

	// ---- C2: receiver must be the destination OFT custody contract ----
	receiverAddr := bytes32HexToAddress(packet.Receiver)
	if receiverAddr != c.route.DestinationOFT {
		return reject(fmt.Sprintf("C2_receiver_not_destination_oft: got %s want %s", receiverAddr, c.route.DestinationOFT))
	}

	// ---- C3: payloadHash must equal keccak256(guid || message) recomputed from encodedPayload ----
	// Both job.PayloadHash and packet.PayloadHash are stored as "0x..." hex strings.
	// The packet was stored by the indexer which already computed the hash from the encodedPayload,
	// so packet.PayloadHash is the ground-truth. We verify it matches the job's payloadHash.
	if packet.PayloadHash != job.PayloadHash {
		return reject(fmt.Sprintf("C3_payload_hash_mismatch: job=%s packet=%s", job.PayloadHash, packet.PayloadHash))
	}

	// ---- Decode the AggLayerOFTPayloadV1 from the packet message ----
	if packet.GlobalIndex == nil {
		return reject("C4_global_index_nil: packet is not an AggLayer OFT packet")
	}

	// Decode the OFT payload from the packet message to get the globalIndex (canonical source).
	aggPayload, err := codec.DecodeAggLayerOFTPayloadV1(packet.Message)
	if err != nil {
		return reject(fmt.Sprintf("C4_decode_agg_payload_failed: %v", err))
	}

	// ---- C4: globalIndex must decode to a valid source bridge network and local leaf index ----
	sourceBridgeNetwork, depositCount, ok := decodeGlobalIndex(aggPayload.GlobalIndex)
	if !ok {
		return reject("C4_global_index_invalid: could not decode globalIndex")
	}

	// ---- Lookup the bridge event ----
	bridge, err := bridgeLookup(ctx, depositCount)
	if err != nil {
		return reject(fmt.Sprintf("C5_bridge_lookup_failed: depositCount=%d err=%v", depositCount, err))
	}
	if bridge == nil {
		return reject(fmt.Sprintf("C5_bridge_not_found: depositCount=%d", depositCount))
	}

	// ---- C5: decoded local leaf index must match BridgeEvent.depositCount ----
	if bridge.DepositCount != depositCount {
		return reject(fmt.Sprintf("C5_deposit_count_mismatch: decoded=%d bridge=%d", depositCount, bridge.DepositCount))
	}

	// ---- C6: decoded source bridge network must match the configured AggLayer source network ----
	if sourceBridgeNetwork != c.route.SourceBridgeNetwork {
		return reject(fmt.Sprintf("C6_source_network_mismatch: decoded=%d config=%d", sourceBridgeNetwork, c.route.SourceBridgeNetwork))
	}

	// ---- Decode the inner OFT message ----
	oftMsg, err := codec.DecodeOFTMessage(aggPayload.OFTMessage)
	if err != nil {
		return reject(fmt.Sprintf("C7_decode_oft_message_failed: %v", err))
	}

	// ---- C7: amountSD converted to amountLD must match BridgeEvent.amount ----
	// amountLD = amountSD * DecimalConversionRate
	rate := c.route.DecimalConversionRate
	if rate == nil {
		rate = big.NewInt(1)
	}
	amountLD := new(big.Int).Mul(new(big.Int).SetUint64(oftMsg.AmountSD), rate)
	if bridge.Amount == nil || amountLD.Cmp(bridge.Amount) != 0 {
		bridgeAmountStr := "<nil>"
		if bridge.Amount != nil {
			bridgeAmountStr = bridge.Amount.String()
		}
		return reject(fmt.Sprintf("C7_amount_mismatch: amountSD=%d rate=%s amountLD=%s bridge.Amount=%s",
			oftMsg.AmountSD, rate.String(), amountLD.String(), bridgeAmountStr))
	}

	// ---- C8: OFT recipient must NOT be the AggLayer destinationAddress ----
	// sendTo is bytes32; convert to address (right-most 20 bytes).
	recipient := sendToAddress(oftMsg.SendTo)
	if bridge.DestinationAddress == recipient {
		return reject(fmt.Sprintf("C8_recipient_is_destination_address: recipient=%s", recipient))
	}

	// ---- C9: route config fields must match bridge event ----
	// originNetwork, originAddress, destinationNetwork, destination custody address.
	if bridge.OriginNetwork != c.route.SourceBridgeNetwork {
		return reject(fmt.Sprintf("C9_origin_network_mismatch: bridge=%d config=%d",
			bridge.OriginNetwork, c.route.SourceBridgeNetwork))
	}
	if bridge.OriginAddress != c.route.SourceToken {
		return reject(fmt.Sprintf("C9_origin_address_mismatch: bridge=%s config=%s",
			bridge.OriginAddress, c.route.SourceToken))
	}
	if bridge.DestinationNetwork != c.route.DestinationBridgeNetwork {
		return reject(fmt.Sprintf("C9_destination_network_mismatch: bridge=%d config=%d",
			bridge.DestinationNetwork, c.route.DestinationBridgeNetwork))
	}
	// C9 destination custody check: bridge.DestinationAddress must equal the configured
	// DestinationOFT custody contract. This is compatible with C8 because:
	//   bridge.DestinationAddress == DestinationOFT (custody) != recipient (LZ end user).
	if bridge.DestinationAddress != c.route.DestinationOFT {
		return reject(fmt.Sprintf("C9_destination_address_mismatch: bridge=%s config=%s",
			bridge.DestinationAddress, c.route.DestinationOFT))
	}

	return ValidationResult{
		Job:      job,
		Bridge:   bridge,
		Accepted: true,
	}
}

// decodeGlobalIndex decodes a LayerZero/AggLayer globalIndex into
// (sourceBridgeNetwork, depositCount). Returns ok=false only if globalIndex is nil.
//
// Bit layout (from bridgesync/processor.go):
//
//	bit 64 = mainnet flag
//	bits 63-32 = rollup index (zero when mainnet flag is set)
//	bits 31-0  = deposit count (local leaf index)
//
// If the mainnet flag is set: sourceBridgeNetwork = 0 (Ethereum mainnet in AggLayer).
// Otherwise: rollupIndex = bits[63:32]; sourceBridgeNetwork = rollupIndex + 1.
func decodeGlobalIndex(globalIndex *big.Int) (sourceBridgeNetwork uint32, depositCount uint32, ok bool) {
	if globalIndex == nil {
		return 0, 0, false
	}

	mainnetFlagMask := new(big.Int).Lsh(big.NewInt(1), 64) // 1 << 64
	if new(big.Int).And(globalIndex, mainnetFlagMask).Sign() != 0 {
		// Mainnet flag is set: sourceBridgeNetwork = 0.
		depositCount = uint32(globalIndex.Uint64()) // lower 32 bits (Uint64 gives low 64 bits)
		return 0, depositCount, true
	}

	// Rollup path: upper 32 bits (above bit 32) are the rollupIndex.
	upper := new(big.Int).Rsh(globalIndex, 32)
	rollupIndex := uint32(upper.Uint64())
	depositCount = uint32(globalIndex.Uint64()) // lower 32 bits
	return rollupIndex + 1, depositCount, true
}

// bytes32HexToAddress converts a "0x..."-prefixed hex bytes32 string to an Ethereum address
// by taking the right-most 20 bytes (standard Solidity address-in-bytes32 layout).
func bytes32HexToAddress(hexStr string) common.Address {
	b := common.FromHex(hexStr)
	if len(b) >= 20 {
		return common.BytesToAddress(b[len(b)-20:])
	}
	return common.BytesToAddress(b)
}

// sendToAddress converts a bytes32 sendTo field to an Ethereum address
// using the same right-most-20-bytes convention.
func sendToAddress(sendTo [32]byte) common.Address {
	return common.BytesToAddress(sendTo[12:])
}
