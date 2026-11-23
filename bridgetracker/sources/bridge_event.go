package sources

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/agglayer/aggkit/bridgetracker"
	trackertypes "github.com/agglayer/aggkit/bridgetracker/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// bridgeEventSignature is the topic0 of the bridge contract's BridgeEvent (same event on L1
// and L2 bridges)
var bridgeEventSignature = crypto.Keccak256Hash([]byte(
	"BridgeEvent(uint8,uint32,address,uint32,address,uint256,bytes,uint32)",
))

// BridgeEventSource implements bridgetracker.BridgeEventSource over the origin network's
// JSON-RPC endpoint: it fetches the tx receipt and parses the BridgeEvent log
type BridgeEventSource struct {
	clients EthClientResolver
	// parser is the bridge contract binding used only for ABI log unpacking (no backend calls)
	parser *agglayerbridge.Agglayerbridge
	// l1Finality is the block finality an L1 (network 0) receipt must reach before it is
	// accepted (see FindBridge): a resolved bridge is never re-checked (TrackingBridgeTx.IsDone),
	// so accepting a receipt that later gets reorged out would otherwise be permanent
	l1Finality aggkittypes.BlockNumberFinality
	// l2Finality is the block finality an L2 (non-zero network) receipt must reach before it
	// is accepted; see l1Finality for the reasoning
	l2Finality aggkittypes.BlockNumberFinality
	// bridgeAddrs is the static networkID -> canonical bridge contract address override,
	// consulted before resolver; see FindBridge.
	bridgeAddrs map[uint32]common.Address
	// resolver resolves the canonical bridge contract address for a network not present in
	// bridgeAddrs. Required (fail closed): FindBridge never accepts a BridgeEvent log without
	// either an override or a successful resolver call.
	resolver bridgeAddressResolver
}

// NewBridgeEventSource returns a BridgeEventSource resolving per-network JSON-RPC clients
// through the given resolver, accepting a tx's receipt only once it reaches l1Finality (for
// network 0) or l2Finality (for any other network). bridgeAddrs is the static
// networkID -> canonical bridge contract address override used to reject a BridgeEvent log
// emitted by an unrelated or malicious contract; a network absent from it falls back to
// resolver, which is required (fail closed — there is no permissive fallback if neither knows
// the network's bridge address).
func NewBridgeEventSource(
	clients EthClientResolver, l1Finality, l2Finality aggkittypes.BlockNumberFinality,
	bridgeAddrs map[uint32]common.Address, resolver bridgeAddressResolver,
) (*BridgeEventSource, error) {
	if resolver == nil {
		return nil, fmt.Errorf("bridge address resolver is required")
	}
	parser, err := agglayerbridge.NewAgglayerbridge(common.Address{}, nil)
	if err != nil {
		return nil, fmt.Errorf("creating bridge contract parser: %w", err)
	}
	return &BridgeEventSource{
		clients: clients, parser: parser, l1Finality: l1Finality, l2Finality: l2Finality,
		bridgeAddrs: bridgeAddrs, resolver: resolver,
	}, nil
}

// finalityFor returns the block finality a receipt on networkID must reach before it is
// accepted: l1Finality for L1 (network 0), l2Finality for any other network.
func (s *BridgeEventSource) finalityFor(networkID uint32) aggkittypes.BlockNumberFinality {
	if networkID == 0 {
		return s.l1Finality
	}
	return s.l2Finality
}

// FindBridge implements bridgetracker.BridgeEventSource: it resolves the receipt of the tx
// on its origin network and extracts the BridgeEvent facts. A missing tx, or one whose receipt
// has not reached finality yet, is ErrBridgeTxNotFound (the engine retries — the tx may simply
// not be mined, or not final, yet); a reverted tx or a mined receipt without BridgeEvent logs
// are both ErrBridgeTxNotABridge, since neither can ever change on retry (the engine fails the
// bridge immediately for those). A tx with several canonical BridgeEvents is tracked one event at
// a time: id.Log selects which, and a tx-only id is ErrAmbiguousBridgeTx for it rather than
// silently resolving to the first one (see selectBridgeEventLog). Finality is checked before
// either of those outcomes is decided: a reverted or eventless receipt can be reorged away just as
// much as a successful one, and a resolved bridge is never re-checked afterward (see
// TrackingBridgeTx.IsDone), so accepting any outcome from a non-final receipt could leave the
// tracker permanently wrong.
//
// The BridgeEvent logs are only accepted when the upstream describes one coherent block: every
// canonical BridgeEvent log must be the receipt's own (same block hash and number, same tx hash)
// and the header of the block at the receipt's number must be the receipt's block (see
// checkBridgeLogBlock and checkReceiptHeader). Any contradiction is ErrInvalidUpstreamData with no
// BridgeInfo — transient, since a defective endpoint may recover — and nothing in the answer is
// trusted partially. The header is also the source of the timestamp, so BridgeInfo never mixes
// facts of two blocks
func (s *BridgeEventSource) FindBridge(
	ctx context.Context, id bridgetracker.TrackingID,
) (*bridgetracker.BridgeInfo, error) {
	client, err := s.clients.RPCClientFor(ctx, id.NetworkID)
	if err != nil {
		return nil, fmt.Errorf("resolving JSON-RPC client for network %d: %w", id.NetworkID, err)
	}

	receipt, err := client.TransactionReceipt(ctx, id.TxHash)
	if errors.Is(err, ethereum.NotFound) {
		return nil, fmt.Errorf("%s not found: %w", id, bridgetracker.ErrBridgeTxNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("fetching receipt of %s: %w", id, err)
	}

	finality := s.finalityFor(id.NetworkID)
	finalized, err := client.CustomHeaderByNumber(ctx, &finality)
	if err != nil {
		return nil, fmt.Errorf("fetching %s header for network %d: %w", finality.String(), id.NetworkID, err)
	}
	if receipt.BlockNumber == nil || receipt.BlockNumber.Uint64() > finalized.Number {
		return nil, fmt.Errorf("%s not yet %s: %w", id, finality.String(), bridgetracker.ErrBridgeTxNotFound)
	}

	if receipt.Status != gethtypes.ReceiptStatusSuccessful {
		return nil, fmt.Errorf("%s reverted: %w", id, bridgetracker.ErrBridgeTxNotABridge)
	}

	wantAddr, ok := s.bridgeAddrs[id.NetworkID]
	if !ok {
		var err error
		wantAddr, err = s.resolver.BridgeAddress(ctx, id.NetworkID)
		if err != nil {
			return nil, fmt.Errorf("resolving canonical bridge address for network %d: %w", id.NetworkID, err)
		}
	}

	// every canonical BridgeEvent of the receipt is checked, not only the selected one: the
	// candidates also decide ErrAmbiguousBridgeTx/ErrBridgeLogNotFound and the log indexes listed
	for _, l := range receipt.Logs {
		if isBridgeEventLog(l, wantAddr) {
			if err := checkBridgeLogBlock(id, receipt, l); err != nil {
				return nil, err
			}
		}
	}
	l, err := selectBridgeEventLog(id, receipt.Logs, wantAddr)
	if err != nil {
		return nil, err
	}
	event, err := s.parser.ParseBridgeEvent(*l)
	if err != nil {
		return nil, fmt.Errorf("parsing BridgeEvent log of %s: %w", id, err)
	}
	// Fetched by number, like the finality gate and the engine's origin re-check
	// (CheckOrigin), not by hash: CustomHeaderByNumber reports the block hash as the RPC
	// sent it when the client has HashFromJSON, whereas HeaderByHash returns a geth header
	// whose Hash() is recomputed from its fields and so differs from the RPC-reported one
	// on chains with non-standard headers. It also proves the receipt's block is the
	// canonical one at its height, not a stale or forked block
	header, err := client.CustomHeaderByNumber(ctx, aggkittypes.NewBlockNumber(receipt.BlockNumber.Uint64()))
	if err != nil {
		return nil, fmt.Errorf("fetching header of block %d of %s: %w", receipt.BlockNumber.Uint64(), id, err)
	}
	if err := checkReceiptHeader(id, receipt, header); err != nil {
		return nil, err
	}
	return &bridgetracker.BridgeInfo{
		NetworkID:          id.NetworkID,
		LeafType:           trackertypes.BridgeLeafType(event.LeafType),
		DestinationNetwork: event.DestinationNetwork,
		DepositCount:       event.DepositCount,
		BlockNumber:        receipt.BlockNumber.Uint64(),
		BlockHash:          receipt.BlockHash,
		LogIndex:           uint32(l.Index), // checkBridgeLogBlock bounds it
		OriginNetwork:      event.OriginNetwork,
		OriginAddress:      event.OriginAddress,
		DestinationAddress: event.DestinationAddress,
		Amount:             event.Amount,
		BlockTimestamp:     header.Time,
	}, nil
}

// isBridgeEventLog reports whether l is a BridgeEvent emitted by the canonical bridge contract
// bridgeAddr
func isBridgeEventLog(l *gethtypes.Log, bridgeAddr common.Address) bool {
	return l != nil && len(l.Topics) > 0 && l.Topics[0] == bridgeEventSignature && l.Address == bridgeAddr
}

// selectBridgeEventLog picks, among logs, the BridgeEvent emitted by the canonical bridge
// contract (bridgeAddr) that id identifies: the one at id.Log.Index if id selects a log
// (ErrBridgeLogNotFound if none is there), else the only one (ErrAmbiguousBridgeTx if there are
// several). Selection is by log index, never by position in logs. A tx with none at all is
// ErrBridgeTxNotABridge either way. Both errors list the candidates' log indexes, so the caller
// can pick one
func selectBridgeEventLog(
	id bridgetracker.TrackingID, logs []*gethtypes.Log, bridgeAddr common.Address,
) (*gethtypes.Log, error) {
	var candidates []*gethtypes.Log
	for _, l := range logs {
		if !isBridgeEventLog(l, bridgeAddr) {
			continue
		}
		if id.Log.Set && l.Index == uint(id.Log.Index) {
			return l, nil
		}
		candidates = append(candidates, l)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%s emitted no BridgeEvent: %w", id, bridgetracker.ErrBridgeTxNotABridge)
	}
	if id.Log.Set {
		return nil, fmt.Errorf("%s emitted BridgeEvents only at log_index %s: %w",
			id, logIndexes(candidates), bridgetracker.ErrBridgeLogNotFound)
	}
	if len(candidates) > 1 {
		return nil, fmt.Errorf("%s emitted BridgeEvents at log_index %s: %w",
			id, logIndexes(candidates), bridgetracker.ErrAmbiguousBridgeTx)
	}
	return candidates[0], nil
}

// logIndexes renders the log indexes of logs, sorted, as e.g. "[3 9]"
func logIndexes(logs []*gethtypes.Log) string {
	indexes := make([]uint, 0, len(logs))
	for _, l := range logs {
		indexes = append(indexes, l.Index)
	}
	slices.Sort(indexes)
	return fmt.Sprint(indexes)
}

// checkBridgeLogBlock fails closed with ErrInvalidUpstreamData unless the BridgeEvent log l is
// really one of receipt's logs: mined in the receipt's block (hash and number) by the tx the
// receipt is for, at a log index that fits BridgeInfo.LogIndex. receipt.BlockNumber must be
// set (FindBridge's finality gate checks it)
func checkBridgeLogBlock(id bridgetracker.TrackingID, receipt *gethtypes.Receipt, l *gethtypes.Log) error {
	switch {
	case !receipt.BlockNumber.IsUint64():
		return fmt.Errorf("%s: receipt block number %s is not a uint64: %w",
			id, receipt.BlockNumber, bridgetracker.ErrInvalidUpstreamData)
	case l.BlockHash != receipt.BlockHash:
		return fmt.Errorf("%s: BridgeEvent log block hash %s differs from its receipt's %s: %w",
			id, l.BlockHash, receipt.BlockHash, bridgetracker.ErrInvalidUpstreamData)
	case l.BlockNumber != receipt.BlockNumber.Uint64():
		return fmt.Errorf("%s: BridgeEvent log block number %d differs from its receipt's %d: %w",
			id, l.BlockNumber, receipt.BlockNumber.Uint64(), bridgetracker.ErrInvalidUpstreamData)
	case l.TxHash != id.TxHash:
		return fmt.Errorf("%s: BridgeEvent log tx hash %s is not the tracked tx: %w",
			id, l.TxHash, bridgetracker.ErrInvalidUpstreamData)
	case l.Index > math.MaxUint32:
		return fmt.Errorf("%s: BridgeEvent log index %d does not fit a uint32: %w",
			id, l.Index, bridgetracker.ErrInvalidUpstreamData)
	}
	return nil
}

// checkReceiptHeader fails closed with ErrInvalidUpstreamData unless header, fetched by the
// receipt's block number, is the receipt's block: same number and same hash. header.Hash is the
// hash the client reports (the RPC's own when HashFromJSON is set, see FindBridge)
func checkReceiptHeader(
	id bridgetracker.TrackingID, receipt *gethtypes.Receipt, header *aggkittypes.BlockHeader,
) error {
	switch {
	case header == nil:
		return fmt.Errorf("%s: no header for block %d: %w",
			id, receipt.BlockNumber.Uint64(), bridgetracker.ErrInvalidUpstreamData)
	case header.Number != receipt.BlockNumber.Uint64():
		return fmt.Errorf("%s: header number %d differs from its receipt's block %d: %w",
			id, header.Number, receipt.BlockNumber.Uint64(), bridgetracker.ErrInvalidUpstreamData)
	case header.Hash != receipt.BlockHash:
		return fmt.Errorf("%s: block %d has hash %s, not its receipt's %s (receipt block is not the canonical one): %w",
			id, header.Number, header.Hash, receipt.BlockHash, bridgetracker.ErrInvalidUpstreamData)
	}
	return nil
}
