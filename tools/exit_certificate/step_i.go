package exit_certificate

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
)

var (
	// keccak256("UpdateL1InfoTreeV2(bytes32,uint32,uint256,uint64)")
	// leafCount is indexed (topics[1]); currentL1InfoRoot, blockhash, minTimestamp are in data.
	updateL1InfoTreeV2Topic = common.HexToHash("0xaf6c6cd7790e0180a4d22eb8ed846e55846f54ed10e5946db19972b5a0813a59")
)

// RunStepI assembles the final certificate by applying the NewLocalExitRoot from Step G,
// the PreviousLocalExitRoot from Step H, and the L1InfoTreeLeafCount from L1.
func RunStepI(ctx context.Context, cfg *Config, certificate *agglayertypes.Certificate, gResult *StepGResult, hResult *StepHResult) error {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP I - Assemble final certificate")
	log.Info("═══════════════════════════════════════════")

	if certificate == nil {
		return fmt.Errorf("certificate is nil")
	}
	if gResult == nil {
		return fmt.Errorf("step G result is nil")
	}

	certificate.NewLocalExitRoot = gResult.NewLocalExitRoot
	log.Infof("NewLocalExitRoot:      %s", certificate.NewLocalExitRoot.Hex())

	if len(gResult.BridgeExitMetadata) > 0 {
		if len(gResult.BridgeExitMetadata) != len(certificate.BridgeExits) {
			return fmt.Errorf("step G metadata count (%d) does not match bridge exits count (%d)",
				len(gResult.BridgeExitMetadata), len(certificate.BridgeExits))
		}
		for i, meta := range gResult.BridgeExitMetadata {
			certificate.BridgeExits[i].Metadata = meta
		}
		log.Infof("Applied bridge exit metadata from Step G (%d entries)", len(gResult.BridgeExitMetadata))
	}

	if hResult != nil {
		certificate.PrevLocalExitRoot = hResult.PreviousLocalExitRoot
		certificate.Height = hResult.Height
		log.Infof("PreviousLocalExitRoot: %s", certificate.PrevLocalExitRoot.Hex())
		log.Infof("Height:                %d", certificate.Height)
	}

	leafCount, err := fetchL1InfoTreeLeafCount(ctx, cfg)
	if err != nil {
		return fmt.Errorf("Could not fetch L1InfoTreeLeafCount: %v", err)
	} else {
		certificate.L1InfoTreeLeafCount = leafCount
		log.Infof("L1InfoTreeLeafCount:   %d", leafCount)
	}

	log.Info("STEP I complete")
	return nil
}

// fetchL1InfoTreeLeafCount scans L1 backwards from the latest L1 block looking for the
// most recent UpdateL1InfoTreeV2 event emitted by cfg.L1GlobalExitRootAddress and returns
// its indexed leafCount field.
func fetchL1InfoTreeLeafCount(ctx context.Context, cfg *Config) (uint32, error) {
	if cfg.L1RPCURL == "" {
		return 0, fmt.Errorf("l1RpcUrl not configured")
	}
	if cfg.L1GlobalExitRootAddress == (common.Address{}) {
		return 0, fmt.Errorf("l1GlobalExitRootAddress not configured")
	}

	toBlock, err := resolveLatestBlock(ctx, cfg.L1RPCURL)
	if err != nil {
		return 0, fmt.Errorf("resolve latest L1 block: %w", err)
	}
	chunkSize := uint64(cfg.Options.BlockRange)
	if chunkSize == 0 {
		chunkSize = defaultBlockRange
	}

	log.Infof("Scanning L1 backwards for UpdateL1InfoTreeV2 (contract=%s, from block %d)",
		cfg.L1GlobalExitRootAddress.Hex(), toBlock)

	// Scan backwards in chunks until we find an event.
	for end := toBlock; ; {
		var start uint64
		if end >= chunkSize {
			start = end - chunkSize + 1
		}

		leafCount, found, err := queryUpdateL1InfoTreeV2(ctx, cfg.L1RPCURL, cfg.L1GlobalExitRootAddress, start, end)
		if err != nil {
			log.Warnf("eth_getLogs [%d-%d] error: %v", start, end, err)
		} else if found {
			log.Infof("Found UpdateL1InfoTreeV2 at block range [%d-%d]: leafCount=%d", start, end, leafCount)
			return leafCount, nil
		}

		if start == 0 {
			break
		}
		end = start - 1
	}

	return 0, fmt.Errorf("no UpdateL1InfoTreeV2 event found between block 0 and %d", toBlock)
}

// queryUpdateL1InfoTreeV2 fetches UpdateL1InfoTreeV2 logs in [fromBlock, toBlock] and returns
// the leafCount from the LAST (most recent) log found, or (0, false, nil) if none.
func queryUpdateL1InfoTreeV2(
	ctx context.Context, rpcURL string, contractAddr common.Address,
	fromBlock, toBlock uint64,
) (leafCount uint32, found bool, err error) {
	result, err := singleRPC(ctx, rpcURL, "eth_getLogs", []any{
		map[string]any{
			"address":   contractAddr.Hex(),
			"topics":    []string{updateL1InfoTreeV2Topic.Hex()},
			"fromBlock": toBlockTag(fromBlock),
			"toBlock":   toBlockTag(toBlock),
		},
	}, defaultRetries)
	if err != nil {
		return 0, false, err
	}

	var logs []struct {
		Topics []string `json:"topics"`
	}
	if err := json.Unmarshal(result, &logs); err != nil {
		return 0, false, fmt.Errorf("unmarshal UpdateL1InfoTreeV2 logs: %w", err)
	}
	if len(logs) == 0 {
		return 0, false, nil
	}

	// Take the LAST log (highest block number) in this range.
	last := logs[len(logs)-1]
	if len(last.Topics) < 2 {
		return 0, false, fmt.Errorf("UpdateL1InfoTreeV2 log has only %d topics", len(last.Topics))
	}

	// topics[1] is the indexed leafCount (uint32), ABI-encoded as a 32-byte big-endian value.
	topicBytes := common.FromHex(last.Topics[1])
	lc, err := safeUint32(new(big.Int).SetBytes(topicBytes))
	if err != nil {
		return 0, false, fmt.Errorf("decode leafCount from topics[1]: %w", err)
	}
	return lc, true, nil
}
