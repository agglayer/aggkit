package exit_certificate

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
)

// rpcEventLog is the JSON representation of a log entry in an eth_getLogs response. Unlike rpcLog
// it also carries blockNumber, used to record the bridge's shadow-fork block.
type rpcEventLog struct {
	Address     string   `json:"address"`
	Topics      []string `json:"topics"`
	Data        string   `json:"data"`
	BlockNumber string   `json:"blockNumber"`
}

// readShadowForkBridges recovers the replayed bridge exits by reading BridgeEvent logs directly
// from the shadow-fork, starting at fromBlock (the first block containing replayed exits). It only
// queries the fork's own blocks — it does not sync the full L2 history. The returned bridges are
// ordered by DepositCount, which is the canonical exit-tree order.
func readShadowForkBridges(
	ctx context.Context, anvilURL string, bridgeAddr common.Address, fromBlock uint64,
) ([]shadowForkBridge, error) {
	filter := map[string]any{
		"fromBlock": toBlockTag(fromBlock),
		"toBlock":   "latest",
		"address":   bridgeAddr.Hex(),
		"topics":    []any{bridgeEventTopicHash.Hex()},
	}
	raw, err := singleRPC(ctx, anvilURL, "eth_getLogs", []any{filter}, defaultRetries)
	if err != nil {
		return nil, fmt.Errorf("eth_getLogs for BridgeEvent: %w", err)
	}
	var logs []rpcEventLog
	if err := json.Unmarshal(raw, &logs); err != nil {
		return nil, fmt.Errorf("parse eth_getLogs result: %w", err)
	}

	bridges := make([]shadowForkBridge, 0, len(logs))
	for _, l := range logs {
		event, matched, err := parseBridgeEventLog(l.Topics, l.Data)
		if err != nil {
			return nil, err
		}
		if !matched {
			continue
		}
		bridges = append(bridges, shadowForkBridge{
			BlockNum:           hexToUint64(l.BlockNumber),
			OriginNetwork:      event.OriginNetwork,
			OriginAddress:      event.OriginAddress,
			DestinationNetwork: event.DestinationNetwork,
			DestinationAddress: event.DestinationAddress,
			Amount:             event.Amount,
			DepositCount:       event.DepositCount,
		})
	}
	sort.Slice(bridges, func(i, j int) bool { return bridges[i].DepositCount < bridges[j].DepositCount })
	log.Infof("Read %d BridgeEvent logs from shadow-fork (from block %d)", len(bridges), fromBlock)
	return bridges, nil
}
