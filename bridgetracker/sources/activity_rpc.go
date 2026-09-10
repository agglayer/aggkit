package sources

import (
	"context"
	"fmt"
	"math/big"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/agglayer/aggkit/bridgeservice"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	aggkitcommon "github.com/agglayer/aggkit/common"
	aggkitlog "github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
)

// bridgeAddressResolver is the slice of NetworkLister activityRPCScanner actually needs: just
// the destination bridge contract address lookup (unlike ActivitySource itself, it never needs
// NetworkIDs/GetURL — its caller already knows which network to scan). Narrowing to this lets
// tests fake just this one method instead of the whole NetworkLister surface.
type bridgeAddressResolver interface {
	BridgeAddress(ctx context.Context, networkID uint32) (common.Address, error)
}

// activityRPCScanner implements the RPC-based fallback half of GET /activity/from/{from_address}
// (see agglayer/aggkit#1837): for one network, it scans that network's own bridge contract
// directly via RPC over a small, configurable, recent block window for BridgeEvent logs sent by
// a given address — a safety net for a bridge that network's own bridge-service instance has not
// indexed yet. ActivitySource.BridgesFrom runs this in parallel with, and merges its result into,
// the existing bridge-service-backed scan (see fetchNewBridgesFrom).
type activityRPCScanner struct {
	ethClients EthClientResolver
	finder     bridgeAddressResolver
	fromBlock  aggkittypes.BlockNumberFinality
	toBlock    aggkittypes.BlockNumberFinality
	// parser is the bridge contract binding used only for ABI log unpacking (no backend calls),
	// the same trick NewBridgeEventSource uses
	parser *agglayerbridge.Agglayerbridge
	logger aggkitcommon.Logger
}

// newActivityRPCScanner returns an activityRPCScanner resolving per-network JSON-RPC clients and
// bridge contract addresses through ethClients/finder, scanning [fromBlock, toBlock] (each
// resolved against the scanned network's own head) on every network it is asked about.
func newActivityRPCScanner(
	ethClients EthClientResolver, finder bridgeAddressResolver, fromBlock, toBlock aggkittypes.BlockNumberFinality,
	logger aggkitcommon.Logger,
) (*activityRPCScanner, error) {
	parser, err := agglayerbridge.NewAgglayerbridge(common.Address{}, nil)
	if err != nil {
		return nil, fmt.Errorf("creating bridge contract parser: %w", err)
	}
	return &activityRPCScanner{
		ethClients: ethClients, finder: finder, fromBlock: fromBlock, toBlock: toBlock,
		parser: parser, logger: logger,
	}, nil
}

// BridgesFrom scans networkID's own bridge contract via RPC for BridgeEvent logs sent by
// fromAddress within [fromBlock, toBlock], converting each match into a domain.ScannedBridge via
// the same bridgeservice.NewBridgeResponse converter the bridge-service instance itself uses, so
// an RPC-discovered bridge is byte-for-byte comparable (same GlobalIndex, same BridgeHash) to
// whatever that instance would eventually report for it. Returns an empty, nil-error result for
// an empty (fromBlock > toBlock) window — that is not itself a failure.
func (r *activityRPCScanner) BridgesFrom(
	ctx context.Context, networkID uint32, fromAddress common.Address,
) ([]*domain.ScannedBridge, error) {
	client, err := r.ethClients.RPCClientFor(ctx, networkID)
	if err != nil {
		return nil, fmt.Errorf("resolving JSON-RPC client for network %d: %w", networkID, err)
	}

	bridgeAddr, err := r.finder.BridgeAddress(ctx, networkID)
	if err != nil {
		return nil, fmt.Errorf("resolving bridge contract address for network %d: %w", networkID, err)
	}

	from, err := r.fromBlock.BlockNumber(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("resolving %s for network %d: %w", r.fromBlock.String(), networkID, err)
	}
	to, err := r.toBlock.BlockNumber(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("resolving %s for network %d: %w", r.toBlock.String(), networkID, err)
	}
	if from > to {
		return nil, nil
	}

	logs, err := client.FilterLogs(ctx, ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(from),
		ToBlock:   new(big.Int).SetUint64(to),
		Addresses: []common.Address{bridgeAddr},
		Topics:    [][]common.Hash{{bridgeEventSignature}},
	})
	if err != nil {
		return nil, fmt.Errorf("filtering BridgeEvent logs for network %d [%d, %d]: %w", networkID, from, to, err)
	}

	// ethClient/txLogger are only needed once there is at least one log to process — resolved
	// lazily so an empty window never fails on a client that happens not to support the extended
	// RPC calls (debug_traceTransaction) ExtractTxnAddresses may need.
	var ethClient aggkittypes.EthClienter
	var txLogger *aggkitlog.Logger

	out := make([]*domain.ScannedBridge, 0, len(logs))
	for _, l := range logs {
		if ethClient == nil {
			var ok bool
			ethClient, ok = client.(aggkittypes.EthClienter)
			if !ok {
				return nil, fmt.Errorf("JSON-RPC client for network %d does not support the extended RPC "+
					"calls (debug_traceTransaction) needed to resolve a bridge's sender", networkID)
			}
			if txLogger, ok = r.logger.(*aggkitlog.Logger); !ok {
				txLogger = aggkitlog.GetDefaultLogger()
			}
		}

		event, err := r.parser.ParseBridgeEvent(l)
		if err != nil {
			return nil, fmt.Errorf("parsing BridgeEvent log of %s on network %d: %w", l.TxHash, networkID, err)
		}

		// syncFromInBridges=true: the recent window this source scans is small, so paying for
		// debug_traceTransaction on the rare indirect Asset bridge is cheap here, unlike a full
		// historical sync (see ExtractTxnAddresses)
		txnSender, depositorAddr, toAddr, err := bridgesync.ExtractTxnAddresses(
			ctx, ethClient, bridgeAddr, l.TxHash, event, txLogger, true)
		if err != nil {
			return nil, fmt.Errorf("extracting sender of %s on network %d: %w", l.TxHash, networkID, err)
		}
		if depositorAddr == nil || *depositorAddr != fromAddress {
			continue // not a bridge sent by fromAddress
		}

		timestamp, err := blockTimestamp(ctx, client, l.BlockHash)
		if err != nil {
			return nil, fmt.Errorf("fetching timestamp of block %s on network %d: %w", l.BlockHash, networkID, err)
		}

		bridge := &bridgesync.Bridge{
			BlockNum:           l.BlockNumber,
			BlockPos:           uint64(l.Index),
			FromAddress:        depositorAddr,
			TxHash:             l.TxHash,
			BlockTimestamp:     timestamp,
			LeafType:           event.LeafType,
			OriginNetwork:      event.OriginNetwork,
			OriginAddress:      event.OriginAddress,
			DestinationNetwork: event.DestinationNetwork,
			DestinationAddress: event.DestinationAddress,
			Amount:             event.Amount,
			Metadata:           event.Metadata,
			DepositCount:       event.DepositCount,
			TxnSender:          txnSender,
			ToAddress:          toAddr,
		}
		// etrogL1UpgradeBlock=0: this source only ever scans a small, recent window, always well
		// past any pre-Etrog legacy encoding on any live network.
		out = append(out, &domain.ScannedBridge{
			Bridge:    bridgeservice.NewBridgeResponse(bridge, networkID, 0),
			NetworkID: networkID,
		})
	}
	return out, nil
}
