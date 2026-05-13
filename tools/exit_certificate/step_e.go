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
	"github.com/ethereum/go-ethereum/crypto"
)

var bridgeEventTopic = crypto.Keccak256Hash([]byte("BridgeEvent(uint8,uint32,address,uint32,address,uint256,bytes,uint32)"))

// isClaimedSelector is the 4-byte ABI selector for isClaimed(uint32,uint32).
// keccak256("isClaimed(uint32,uint32)")[:4]
const isClaimedSelector = "0xcc461632"

// sourceBridgeNetworkMainnet is the sourceBridgeNetwork value for L1 (mainnet) deposits.
// isClaimed(leafIndex, sourceBridgeNetwork) uses 0 for mainnet.
const sourceBridgeNetworkMainnet = 0

// RunStepE finds unclaimed L1→L2 bridge deposits and adds them to the exit certificate.
//
// Approach:
//  1. Scan L1 bridge for BridgeEvent where destinationNetwork == L2 networkId
//  2. For each deposit, call isClaimed(depositCount, 0) on the L2 bridge contract
//  3. Unclaimed deposits become BridgeExit entries in the certificate
func RunStepE(
	ctx context.Context, cfg *Config,
	certificate *agglayertypes.Certificate,
) (*StepEResult, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP E — Unclaimed L1→L2 bridge deposits")
	log.Info("═══════════════════════════════════════════")

	l1LatestBlock, err := resolveL1LatestBlock(ctx, cfg)
	if err != nil {
		return nil, err
	}

	l1Deposits, err := fetchL1BridgeEvents(ctx, cfg, l1LatestBlock)
	if err != nil {
		return nil, err
	}
	log.Infof("L1→L2 deposits found: %d", len(l1Deposits))

	claimedSet, err := checkClaimedBatch(ctx, cfg, l1Deposits)
	if err != nil {
		return nil, fmt.Errorf("check isClaimed: %w", err)
	}
	log.Infof("Already claimed on L2: %d", len(claimedSet))

	unclaimed := filterUnclaimedDeposits(l1Deposits, claimedSet)
	log.Infof("Unclaimed L1→L2 deposits: %d", len(unclaimed))

	if cfg.Options.IgnoreUnclaimed {
		for _, dep := range unclaimed {
			log.Warnf("⚠️  Unclaimed deposit ignored (ignoreUnclaimed=true): depositCount=%d originNetwork=%d originAddr=%s amount=%s tx=%s",
				dep.DepositCount, dep.OriginNetwork, dep.OriginAddress.Hex(), dep.Amount, dep.TxHash.Hex())
		}
		if len(unclaimed) > 0 {
			log.Warnf("⚠️  %d unclaimed deposit(s) detected but NOT added to the certificate (ignoreUnclaimed=true)", len(unclaimed))
		}
		log.Info("STEP E complete (certificate unchanged)")
		return &StepEResult{
			UnclaimedBridges: unclaimed,
			FinalCertificate: certificate,
		}, nil
	}

	newExits := depositsToExits(unclaimed, cfg)
	log.Infof("Adding %d unclaimed-deposit exits to certificate", len(newExits))

	newImportedExits := depositsToImportedExits(unclaimed)
	log.Infof("Adding %d unclaimed-deposit imported exits to certificate", len(newImportedExits))

	finalCertificate := mergeCertificate(certificate, newExits, newImportedExits)
	log.Infof("STEP E complete: certificate has %d bridge exits, %d imported bridge exits",
		len(finalCertificate.BridgeExits), len(finalCertificate.ImportedBridgeExits))

	return &StepEResult{
		UnclaimedBridges: unclaimed,
		FinalCertificate: finalCertificate,
	}, nil
}

func resolveL1LatestBlock(ctx context.Context, cfg *Config) (uint64, error) {
	latestResult, err := singleRPC(ctx, cfg.L1RPCURL, "eth_blockNumber", nil, defaultRetries)
	if err != nil {
		return 0, fmt.Errorf("get L1 latest block: %w", err)
	}
	var latestHex string
	if err := json.Unmarshal(latestResult, &latestHex); err != nil {
		return 0, fmt.Errorf("parse L1 latest block: %w", err)
	}
	block := hexToUint64(latestHex)
	log.Infof("L1 latest block: %d, scanning from %d", block, cfg.Options.L1StartBlock)
	return block, nil
}

// checkClaimedBatch calls isClaimed(depositCount, 0) on the L2 bridge for each deposit.
//
// isClaimed inputs:
//   - leafIndex = depositCount from the BridgeEvent
//   - sourceBridgeNetwork = 0 (mainnet), because the deposit originates from L1
//
// The contract internally computes:
//
//	globalIndex = leafIndex + sourceBridgeNetwork * 2^32
//
// With sourceBridgeNetwork=0 this simplifies to globalIndex = leafIndex.
func checkClaimedBatch(
	ctx context.Context, cfg *Config, deposits []L1Deposit,
) (map[uint32]struct{}, error) {
	if len(deposits) == 0 {
		return nil, nil
	}

	calls := make([]RPCCall, len(deposits))
	for i, dep := range deposits {
		calls[i] = RPCCall{
			Method: "eth_call",
			Params: []any{
				map[string]string{
					"to":   cfg.L2BridgeAddress.Hex(),
					"data": encodeIsClaimed(dep.DepositCount, sourceBridgeNetworkMainnet),
				},
				"latest",
			},
		}
	}

	results, err := concurrentBatchRPC(
		ctx, cfg.L2RPCURL, calls, cfg.Options.RPCBatchSize, cfg.Options.ConcurrencyLimit,
		"L2 RPC/isClaimed",
	)
	if err != nil {
		return nil, fmt.Errorf("batch isClaimed: %w", err)
	}

	return parseClaimedResults(results, deposits), nil
}

// encodeIsClaimed ABI-encodes isClaimed(uint32 leafIndex, uint32 sourceBridgeNetwork).
func encodeIsClaimed(leafIndex, sourceBridgeNetwork uint32) string {
	data := make([]byte, 4+64) //nolint:mnd
	copy(data[0:4], common.FromHex(isClaimedSelector))
	new(big.Int).SetUint64(uint64(leafIndex)).FillBytes(data[4:36])
	new(big.Int).SetUint64(uint64(sourceBridgeNetwork)).FillBytes(data[36:68])
	return "0x" + common.Bytes2Hex(data)
}

func parseClaimedResults(results []json.RawMessage, deposits []L1Deposit) map[uint32]struct{} {
	claimed := make(map[uint32]struct{})
	for i, result := range results {
		if result == nil {
			continue
		}
		var hex string
		if json.Unmarshal(result, &hex) != nil {
			continue
		}
		val := hexToBigInt(hex)
		if val.Sign() > 0 {
			claimed[deposits[i].DepositCount] = struct{}{}
		}
	}
	return claimed
}

func filterUnclaimedDeposits(
	l1Deposits []L1Deposit, claimedSet map[uint32]struct{},
) []L1Deposit {
	var unclaimed []L1Deposit
	for _, dep := range l1Deposits {
		if _, ok := claimedSet[dep.DepositCount]; !ok {
			unclaimed = append(unclaimed, dep)
		}
	}
	return unclaimed
}

func depositsToImportedExits(unclaimed []L1Deposit) []*agglayertypes.ImportedBridgeExit {
	exits := make([]*agglayertypes.ImportedBridgeExit, 0, len(unclaimed))
	for _, dep := range unclaimed {
		if dep.Amount == nil || dep.Amount.Sign() == 0 {
			continue
		}
		exits = append(exits, &agglayertypes.ImportedBridgeExit{
			BridgeExit: &agglayertypes.BridgeExit{
				LeafType: bridgetypes.LeafType(dep.LeafType),
				TokenInfo: &agglayertypes.TokenInfo{
					OriginNetwork:      dep.OriginNetwork,
					OriginTokenAddress: dep.OriginAddress,
				},
				DestinationNetwork: dep.DestinationNetwork,
				DestinationAddress: dep.DestinationAddress,
				Amount:             dep.Amount,
				Metadata:           dep.Metadata,
			},
			GlobalIndex: &agglayertypes.GlobalIndex{
				MainnetFlag: true,
				RollupIndex: 0,
				LeafIndex:   dep.DepositCount,
			},
			// ClaimData is nil: Merkle proofs are not available via RPC
		})
	}
	return exits
}

func depositsToExits(
	unclaimed []L1Deposit, cfg *Config,
) []*agglayertypes.BridgeExit {
	exits := make([]*agglayertypes.BridgeExit, 0, len(unclaimed))
	for _, dep := range unclaimed {
		if dep.Amount == nil || dep.Amount.Sign() == 0 {
			continue
		}
		exits = append(exits, &agglayertypes.BridgeExit{
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
	return exits
}

func mergeCertificate(
	certificate *agglayertypes.Certificate,
	newExits []*agglayertypes.BridgeExit,
	newImportedExits []*agglayertypes.ImportedBridgeExit,
) *agglayertypes.Certificate {
	allExits := make([]*agglayertypes.BridgeExit, 0,
		len(certificate.BridgeExits)+len(newExits))
	allExits = append(allExits, certificate.BridgeExits...)
	allExits = append(allExits, newExits...)

	allImported := make([]*agglayertypes.ImportedBridgeExit, 0,
		len(certificate.ImportedBridgeExits)+len(newImportedExits))
	allImported = append(allImported, certificate.ImportedBridgeExits...)
	allImported = append(allImported, newImportedExits...)

	return &agglayertypes.Certificate{
		NetworkID:           certificate.NetworkID,
		Height:              certificate.Height,
		PrevLocalExitRoot:   certificate.PrevLocalExitRoot,
		NewLocalExitRoot:    certificate.NewLocalExitRoot,
		BridgeExits:         allExits,
		ImportedBridgeExits: allImported,
	}
}

// fetchL1BridgeEvents scans L1 for BridgeEvents using a worker pool.
func fetchL1BridgeEvents(
	ctx context.Context, cfg *Config, l1LatestBlock uint64,
) ([]L1Deposit, error) {
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
			return fetchBridgeEventsInRange(
				ctx, cfg.L1RPCURL, cfg.L1BridgeAddress, cfg.L2NetworkID, j.from, j.to,
			)
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

// decodeBridgeEvent decodes ABI-encoded BridgeEvent data.
// Layout: leafType | originNetwork | originAddress | destNetwork |
//
//	destAddress | amount | metadataOffset | depositCount | metadata...
func decodeBridgeEvent(
	dataHex, blockNumberHex, txHashHex string,
) (L1Deposit, error) {
	data := common.FromHex(dataHex)
	const minDataLen = 256
	if len(data) < minDataLen {
		return L1Deposit{}, fmt.Errorf("data too short: %d bytes", len(data))
	}

	metadataOffset := new(big.Int).SetBytes(data[192:224]).Uint64()
	metadata, err := extractMetadata(data, metadataOffset)
	if err != nil {
		return L1Deposit{}, err
	}

	return parseBridgeFields(data, metadata, blockNumberHex, txHashHex)
}

func parseBridgeFields(
	data, metadata []byte, blockNumberHex, txHashHex string,
) (L1Deposit, error) {
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

func extractMetadata(data []byte, metadataOffset uint64) ([]byte, error) {
	const abiWordSize = 32
	if metadataOffset+abiWordSize > uint64(len(data)) {
		return nil, nil
	}
	metadataLen := new(big.Int).SetBytes(
		data[metadataOffset : metadataOffset+abiWordSize],
	).Uint64()
	if metadataLen > maxMetadataSize {
		return nil, fmt.Errorf(
			"metadata too large: %d bytes (max %d)", metadataLen, maxMetadataSize,
		)
	}
	metadataStart := metadataOffset + abiWordSize
	if metadataStart+metadataLen > uint64(len(data)) {
		return nil, nil
	}
	metadata := make([]byte, metadataLen)
	copy(metadata, data[metadataStart:metadataStart+metadataLen])
	return metadata, nil
}
