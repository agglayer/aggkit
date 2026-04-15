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

// L2ClaimEvent represents a ClaimEvent emitted on the L2 bridge contract.
// Used to identify L1 deposits that have already been claimed on L2,
// so Step E can exclude them from the exit certificate (avoiding double-counting
// with the EOA/SC balances discovered in steps A–D).
type L2ClaimEvent struct {
	GlobalIndex        *big.Int       `json:"globalIndex"`
	OriginNetwork      uint32         `json:"originNetwork"`
	OriginAddress      common.Address `json:"originAddress"`
	DestinationAddress common.Address `json:"destinationAddress"`
	Amount             *big.Int       `json:"amount"`
}

const (
	// globalIndexMainnetBit is the bit position of the mainnet flag in the globalIndex.
	globalIndexMainnetBit = 64
	// globalIndexLeafMask extracts the 32-bit leaf index from a globalIndex.
	globalIndexLeafMask = 0xFFFFFFFF
)

// mainnetFlag is the bit set in globalIndex for L1 (mainnet) deposits.
// GlobalIndex: | 191 bits (zero) | 1 bit mainnetFlag | 32 bits rollupIndex | 32 bits leafIndex |
var mainnetFlag = new(big.Int).Lsh(big.NewInt(1), globalIndexMainnetBit)

// bridgeEventTopic is keccak256("BridgeEvent(uint8,uint32,address,uint32,address,uint256,bytes,uint32)").
var bridgeEventTopic = common.HexToHash("0x501781209a1f8899323b96b4ef08b168df93e0a90c673d1e4cce39f97571d4d7")

// claimEventTopic is keccak256("ClaimEvent(uint256,uint32,address,address,uint256)").
var claimEventTopic = common.HexToHash("0x25308c93ceeed162da955b3f7ce3e3f93606579e40fb92029faa9efe27545983")

// RunStepE finds unclaimed L1→L2 bridge deposits and adds them to the exit certificate.
// If l2ClaimEvents is nil, it scans the L2 bridge for ClaimEvent logs to discover
// which L1 deposits have already been claimed (avoiding double-counting).
func RunStepE(
	ctx context.Context, cfg *Config,
	l2ClaimEvents []L2ClaimEvent,
	certificate *agglayertypes.Certificate,
) (*StepEResult, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP E — Unclaimed L1→L2 bridge deposits")
	log.Info("═══════════════════════════════════════════")

	// Fetch L2 ClaimEvents if not provided
	if l2ClaimEvents == nil {
		log.Info("No L2 claim events provided — scanning L2 bridge for ClaimEvent logs...")
		var err error
		l2ClaimEvents, err = fetchL2ClaimEvents(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("fetch L2 claim events: %w", err)
		}
	}

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
		return nil, err
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
	newExits := make([]*agglayertypes.BridgeExit, 0, len(unclaimed))
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
		L2ClaimEvents:    l2ClaimEvents,
		UnclaimedBridges: unclaimed,
		FinalCertificate: finalCertificate,
	}, nil
}

func buildClaimedSet(claims []L2ClaimEvent) map[uint32]struct{} {
	leafIndexMask := new(big.Int).SetUint64(globalIndexLeafMask)
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
		return nil, fmt.Errorf("L1 BridgeEvent scan: %w", err)
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

// fetchL2ClaimEvents scans L2 for ClaimEvent logs using a worker pool.
func fetchL2ClaimEvents(ctx context.Context, cfg *Config) ([]L2ClaimEvent, error) {
	toBlock := cfg.ResolvedTargetBlock
	blockRange := cfg.Options.BlockRange
	concurrency := cfg.Options.ConcurrencyLimit

	if toBlock == 0 {
		return nil, nil
	}

	type blockRangeJob struct{ from, to uint64 }
	var jobs []blockRangeJob
	for start := uint64(0); start <= toBlock; start += uint64(blockRange) {
		end := min(start+uint64(blockRange)-1, toBlock)
		jobs = append(jobs, blockRangeJob{from: start, to: end})
	}

	log.Infof("Fetching L2 ClaimEvents: blocks 0→%d, %d ranges, concurrency=%d",
		toBlock, len(jobs), concurrency)

	var allClaims []L2ClaimEvent

	err := runWorkerPool(
		jobs, concurrency,
		func(j blockRangeJob) ([]L2ClaimEvent, error) {
			return fetchClaimEventsInRange(ctx, cfg.L2RPCURL, cfg.L2BridgeAddress, j.from, j.to)
		},
		func(claims []L2ClaimEvent) {
			allClaims = append(allClaims, claims...)
		},
		"L2 ClaimEvent",
	)
	if err != nil {
		return nil, fmt.Errorf("L2 ClaimEvent scan: %w", err)
	}

	log.Infof("L2 ClaimEvent: %d events found", len(allClaims))
	return allClaims, nil
}

// fetchClaimEventsInRange fetches ClaimEvent logs in a single block range.
func fetchClaimEventsInRange(
	ctx context.Context, rpcURL string, bridgeAddress common.Address,
	fromBlock, toBlock uint64,
) ([]L2ClaimEvent, error) {
	result, err := singleRPC(ctx, rpcURL, "eth_getLogs", []any{
		map[string]any{
			"address":   bridgeAddress.Hex(),
			"topics":    []string{claimEventTopic.Hex()},
			"fromBlock": toBlockTag(fromBlock),
			"toBlock":   toBlockTag(toBlock),
		},
	}, defaultRetries)
	if err != nil {
		return nil, err
	}

	var logs []struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(result, &logs); err != nil {
		return nil, fmt.Errorf("unmarshal logs: %w", err)
	}

	claims := make([]L2ClaimEvent, 0, len(logs))
	for _, lg := range logs {
		claim, err := decodeClaimEvent(lg.Data)
		if err != nil {
			continue
		}
		claims = append(claims, claim)
	}
	return claims, nil
}

// decodeClaimEvent decodes ABI-encoded ClaimEvent data.
// Layout: globalIndex(256) | originNetwork(32) | originAddress(address) | destinationAddress(address) | amount(256)
func decodeClaimEvent(dataHex string) (L2ClaimEvent, error) {
	data := common.FromHex(dataHex)
	const minClaimDataLen = 160 // 5 * 32 bytes
	if len(data) < minClaimDataLen {
		return L2ClaimEvent{}, fmt.Errorf("claim data too short: %d bytes", len(data))
	}

	globalIndex := new(big.Int).SetBytes(data[0:32])
	originNetwork, err := safeUint32(new(big.Int).SetBytes(data[32:64]))
	if err != nil {
		return L2ClaimEvent{}, fmt.Errorf("originNetwork: %w", err)
	}

	return L2ClaimEvent{
		GlobalIndex:        globalIndex,
		OriginNetwork:      originNetwork,
		OriginAddress:      common.BytesToAddress(data[64:96]),
		DestinationAddress: common.BytesToAddress(data[96:128]),
		Amount:             new(big.Int).SetBytes(data[128:160]),
	}, nil
}

// decodeBridgeEvent decodes ABI-encoded BridgeEvent data.
// Layout: leafType | originNetwork | originAddress | destNetwork |
//
//	destAddress | amount | metadataOffset | depositCount | metadata...
func decodeBridgeEvent(
	dataHex, blockNumberHex, txHashHex string,
) (L1Deposit, error) {
	data := common.FromHex(dataHex)
	const (
		minDataLen  = 256
		abiWordSize = 32
	)
	if len(data) < minDataLen {
		return L1Deposit{}, fmt.Errorf("data too short: %d bytes", len(data))
	}

	metadataOffset := new(big.Int).SetBytes(data[192:224]).Uint64()
	var metadata []byte
	if metadataOffset+abiWordSize <= uint64(len(data)) {
		metadataLen := new(big.Int).SetBytes(
			data[metadataOffset : metadataOffset+abiWordSize],
		).Uint64()
		if metadataLen > maxMetadataSize {
			return L1Deposit{}, fmt.Errorf(
				"metadata too large: %d bytes (max %d)", metadataLen, maxMetadataSize,
			)
		}
		metadataStart := metadataOffset + abiWordSize
		if metadataStart+metadataLen <= uint64(len(data)) {
			metadata = make([]byte, metadataLen)
			copy(metadata, data[metadataStart:metadataStart+metadataLen])
		}
	}

	leafType, err := safeUint8(new(big.Int).SetBytes(data[0:32]))
	if err != nil {
		return L1Deposit{}, fmt.Errorf("leafType: %w", err)
	}
	originNetwork, err := safeUint32(new(big.Int).SetBytes(data[32:64]))
	if err != nil {
		return L1Deposit{}, fmt.Errorf("originNetwork: %w", err)
	}
	destNetwork, err := safeUint32(new(big.Int).SetBytes(data[96:128]))
	if err != nil {
		return L1Deposit{}, fmt.Errorf("destNetwork: %w", err)
	}
	depositCount, err := safeUint32(new(big.Int).SetBytes(data[224:256]))
	if err != nil {
		return L1Deposit{}, fmt.Errorf("depositCount: %w", err)
	}

	return L1Deposit{
		LeafType:           leafType,
		OriginNetwork:      originNetwork,
		OriginAddress:      common.BytesToAddress(data[64:96]),
		DestinationNetwork: destNetwork,
		DestinationAddress: common.BytesToAddress(data[128:160]),
		Amount:             new(big.Int).SetBytes(data[160:192]),
		Metadata:           metadata,
		DepositCount:       depositCount,
		BlockNumber:        hexToUint64(blockNumberHex),
		TxHash:             common.HexToHash(txHashHex),
	}, nil
}
