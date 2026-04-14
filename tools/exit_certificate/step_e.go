package exit_certificate

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	bridgetypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
)

// L2ClaimEvent represents a ClaimEvent emitted on the L2 bridge.
// Reserved for future use: Step A would scan L2 bridge ClaimEvent logs
// to identify already-claimed L1 deposits.
type L2ClaimEvent struct {
	GlobalIndex        *big.Int       `json:"globalIndex"`
	OriginNetwork      uint32         `json:"originNetwork"`
	OriginAddress      common.Address `json:"originAddress"`
	DestinationAddress common.Address `json:"destinationAddress"`
	Amount             *big.Int       `json:"amount"`
}

// mainnetFlag is the bit set in globalIndex for L1 (mainnet) deposits.
// GlobalIndex: | 191 bits (zero) | 1 bit mainnetFlag | 32 bits rollupIndex | 32 bits leafIndex |
var mainnetFlag = new(big.Int).Lsh(big.NewInt(1), 64) //nolint:mnd

// bridgeEventTopic is keccak256("BridgeEvent(uint8,uint32,address,uint32,address,uint256,bytes,uint32)").
var bridgeEventTopic = common.HexToHash("0x501781209a1f8899323b96b4ef08b168df93e0a90c673d1e4cce39f97571d4d7")

// RunStepE finds unclaimed L1→L2 bridge deposits and adds them to the exit certificate.
func RunStepE(ctx context.Context, cfg *Config, l2ClaimEvents []L2ClaimEvent, certificate *agglayertypes.Certificate) (*StepEResult, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP E — Unclaimed L1→L2 bridge deposits")
	log.Info("═══════════════════════════════════════════")

	// Resolve L1 latest block
	latestResult, err := singleRPC(ctx, cfg.L1RPCURL, "eth_blockNumber", nil, defaultRetries)
	if err != nil {
		return nil, fmt.Errorf("get L1 latest block: %w", err)
	}
	var latestHex string
	if err := json.Unmarshal(latestResult, &latestHex); err != nil {
		return nil, fmt.Errorf("parse L1 latest block: %w", err)
	}
	l1LatestBlock := hexToUint64(latestHex)
	log.Infof("L1 latest block: %d, scanning from %d", l1LatestBlock, cfg.Options.L1StartBlock)

	// Fetch L1 BridgeEvent events targeting our L2
	l1Deposits, err := fetchL1BridgeEvents(ctx, cfg, l1LatestBlock)
	if err != nil {
		return nil, fmt.Errorf("fetch L1 bridge events: %w", err)
	}
	log.Infof("L1→L2 deposits found: %d", len(l1Deposits))

	// Build claimed set from L2 ClaimEvents
	claimedCounts := buildClaimedSet(l2ClaimEvents)
	log.Infof("L2 claims of L1 deposits: %d", len(claimedCounts))

	// Find unclaimed deposits
	var unclaimed []L1Deposit
	for _, dep := range l1Deposits {
		if _, ok := claimedCounts[dep.DepositCount]; !ok {
			unclaimed = append(unclaimed, dep)
		}
	}
	log.Infof("Unclaimed L1→L2 deposits: %d", len(unclaimed))

	// Convert to BridgeExits
	var newExits []*agglayertypes.BridgeExit
	for _, dep := range unclaimed {
		if dep.Amount == nil || dep.Amount.Sign() == 0 {
			continue
		}
		newExits = append(newExits, &agglayertypes.BridgeExit{
			LeafType: bridgetypes.LeafType(dep.LeafType),
			TokenInfo: &agglayertypes.TokenInfo{
				OriginNetwork:      dep.OriginNetwork,
				OriginTokenAddress: dep.OriginAddress,
			},
			DestinationNetwork: cfg.DestinationNetwork,
			DestinationAddress: dep.DestinationAddress,
			Amount:             dep.Amount,
			Metadata:           dep.Metadata,
		})
	}
	log.Infof("Adding %d unclaimed-deposit exits to certificate", len(newExits))

	// Merge into existing certificate
	allExits := make([]*agglayertypes.BridgeExit, 0, len(certificate.BridgeExits)+len(newExits))
	allExits = append(allExits, certificate.BridgeExits...)
	allExits = append(allExits, newExits...)

	finalCertificate := &agglayertypes.Certificate{
		NetworkID:           certificate.NetworkID,
		Height:              certificate.Height,
		PrevLocalExitRoot:   certificate.PrevLocalExitRoot,
		NewLocalExitRoot:    certificate.NewLocalExitRoot,
		BridgeExits:         allExits,
		ImportedBridgeExits: certificate.ImportedBridgeExits,
	}

	log.Infof("STEP E complete: final certificate has %d total bridge exits", len(allExits))

	return &StepEResult{
		UnclaimedBridges: unclaimed,
		FinalCertificate: finalCertificate,
	}, nil
}

func buildClaimedSet(claims []L2ClaimEvent) map[uint32]struct{} {
	leafIndexMask := new(big.Int).SetUint64(0xFFFFFFFF) //nolint:mnd
	claimed := make(map[uint32]struct{})
	for _, c := range claims {
		if c.GlobalIndex == nil {
			continue
		}
		if new(big.Int).And(c.GlobalIndex, mainnetFlag).Sign() > 0 {
			leafIndex := uint32(new(big.Int).And(c.GlobalIndex, leafIndexMask).Uint64())
			claimed[leafIndex] = struct{}{}
		}
	}
	return claimed
}

// fetchL1BridgeEvents scans L1 for BridgeEvents using a worker pool.
func fetchL1BridgeEvents(ctx context.Context, cfg *Config, l1LatestBlock uint64) ([]L1Deposit, error) {
	fromBlock := cfg.Options.L1StartBlock
	blockRange := cfg.Options.BlockRange
	concurrency := cfg.Options.ConcurrencyLimit

	if l1LatestBlock < fromBlock {
		return nil, nil
	}

	type blockRangeJob struct{ from, to uint64 }
	var jobs []blockRangeJob
	for start := fromBlock; start <= l1LatestBlock; start += uint64(blockRange) {
		end := min(start+uint64(blockRange)-1, l1LatestBlock)
		jobs = append(jobs, blockRangeJob{from: start, to: end})
	}

	log.Infof("Fetching L1 BridgeEvents: blocks %d→%d, %d ranges, concurrency=%d",
		fromBlock, l1LatestBlock, len(jobs), concurrency)

	var allDeposits []L1Deposit

	err := runWorkerPool(
		jobs, concurrency,
		func(j blockRangeJob) ([]L1Deposit, error) {
			return fetchBridgeEventsInRange(ctx, cfg.L1RPCURL, cfg.L1BridgeAddress, cfg.L2NetworkID, j.from, j.to)
		},
		func(deposits []L1Deposit) {
			allDeposits = append(allDeposits, deposits...)
		},
		"L1 BridgeEvent",
	)
	if err != nil {
		log.Warnf("Some L1 BridgeEvent queries failed: %v", err)
	}

	log.Infof("L1 BridgeEvent: %d events found", len(allDeposits))
	return allDeposits, nil
}

// fetchBridgeEventsInRange fetches BridgeEvent logs in a single block range.
func fetchBridgeEventsInRange(
	ctx context.Context, rpcURL string, bridgeAddress common.Address,
	l2NetworkID uint32, fromBlock, toBlock uint64,
) ([]L1Deposit, error) {
	result, err := singleRPC(ctx, rpcURL, "eth_getLogs", []any{
		map[string]any{
			"address":   bridgeAddress.Hex(),
			"topics":    []string{bridgeEventTopic.Hex()},
			"fromBlock": toBlockTag(fromBlock),
			"toBlock":   toBlockTag(toBlock),
		},
	}, defaultRetries)
	if err != nil {
		return nil, err
	}

	var logs []struct {
		Data            string `json:"data"`
		BlockNumber     string `json:"blockNumber"`
		TransactionHash string `json:"transactionHash"`
	}
	if err := json.Unmarshal(result, &logs); err != nil {
		return nil, fmt.Errorf("unmarshal logs: %w", err)
	}

	var deposits []L1Deposit
	for _, lg := range logs {
		dep, err := decodeBridgeEvent(lg.Data, lg.BlockNumber, lg.TransactionHash)
		if err != nil {
			continue
		}
		if dep.DestinationNetwork == l2NetworkID {
			deposits = append(deposits, dep)
		}
	}
	return deposits, nil
}

// decodeBridgeEvent decodes ABI-encoded BridgeEvent data.
// Layout: leafType | originNetwork | originAddress | destNetwork | destAddress | amount | metadataOffset | depositCount | metadata...
func decodeBridgeEvent(dataHex, blockNumberHex, txHashHex string) (L1Deposit, error) {
	data := common.FromHex(dataHex)
	const minDataLen = 256
	if len(data) < minDataLen {
		return L1Deposit{}, fmt.Errorf("data too short: %d bytes", len(data))
	}

	// Dynamic metadata: offset at [192:224], then length + bytes
	metadataOffset := new(big.Int).SetBytes(data[192:224]).Uint64()
	var metadata []byte
	if metadataOffset+32 <= uint64(len(data)) {
		metadataLen := new(big.Int).SetBytes(data[metadataOffset : metadataOffset+32]).Uint64()
		metadataStart := metadataOffset + 32
		if metadataStart+metadataLen <= uint64(len(data)) {
			metadata = make([]byte, metadataLen)
			copy(metadata, data[metadataStart:metadataStart+metadataLen])
		}
	}

	return L1Deposit{
		LeafType:           uint8(new(big.Int).SetBytes(data[0:32]).Uint64()),
		OriginNetwork:      uint32(new(big.Int).SetBytes(data[32:64]).Uint64()),
		OriginAddress:      common.BytesToAddress(data[64:96]),
		DestinationNetwork: uint32(new(big.Int).SetBytes(data[96:128]).Uint64()),
		DestinationAddress: common.BytesToAddress(data[128:160]),
		Amount:             new(big.Int).SetBytes(data[160:192]),
		Metadata:           metadata,
		DepositCount:       uint32(new(big.Int).SetBytes(data[224:256]).Uint64()),
		BlockNumber:        hexToUint64(blockNumberHex),
		TxHash:             common.HexToHash(txHashHex),
	}, nil
}
