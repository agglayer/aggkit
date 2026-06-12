package exit_certificate

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"github.com/agglayer/aggkit/agglayer"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	waitPollInterval = 5 * time.Second
	// verifyBatchesDataLen is the ABI-encoded data length of VerifyBatchesTrustedAggregator:
	// numBatch (uint64) + stateRoot (bytes32) + exitRoot (bytes32), each padded to 32 bytes.
	verifyBatchesDataLen = 96
	// rollupManagerSelector is keccak256("rollupManager()")[:4], the getter exposed by the
	// consensus contract (PolygonConsensusBase, i.e. sovereignRollupAddr) that returns the
	// address of the PolygonRollupManager it belongs to.
	rollupManagerSelector = "0x49b7b802"
	// updateL1InfoTreeMinTopics is the minimum number of topics an UpdateL1InfoTree log must carry:
	// topics[0] (event signature) + the indexed mainnetExitRoot and rollupExitRoot.
	updateL1InfoTreeMinTopics = 3
)

// verifyBatchesTrustedAggregatorTopic is keccak256 of the event signature. The RollupManager
// emits it on L1 when a rollup's batches are verified (the certificate is settled on L1):
// VerifyBatchesTrustedAggregator(uint32 indexed rollupID, uint64 numBatch, bytes32 stateRoot,
// bytes32 exitRoot, address indexed aggregator).
var verifyBatchesTrustedAggregatorTopic = crypto.Keccak256Hash(
	[]byte("VerifyBatchesTrustedAggregator(uint32,uint64,bytes32,bytes32,address)"),
)

// L1 GlobalExitRoot contract events emitted alongside the certificate's L1 settlement.
var (
	// UpdateL1InfoTree(bytes32 indexed mainnetExitRoot, bytes32 indexed rollupExitRoot).
	updateL1InfoTreeTopic = crypto.Keccak256Hash([]byte("UpdateL1InfoTree(bytes32,bytes32)"))
	// UpdateL1InfoTreeV2(bytes32 currentL1InfoRoot, uint32 indexed leafCount, uint256 blockhash,
	// uint64 minTimestamp). leafCount is indexed (topics[1]); the rest is in data.
	updateL1InfoTreeV2TopicWait = crypto.Keccak256Hash(
		[]byte("UpdateL1InfoTreeV2(bytes32,uint32,uint256,uint64)"))
)

// RunStepWait waits for the submitted certificate to reach a final state. It polls the
// agglayer for the certificate header by hash with GetCertificateHeader — which always
// returns the current status — until it is Settled (success) or InError (error).
//
// Requires options.agglayerClient.grpc.url.
func RunStepWait(ctx context.Context, cfg *Config, submitResult *StepSubmitResult) (*StepWaitResult, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP WAIT - Wait for certificate settlement")
	log.Info("═══════════════════════════════════════════")

	agglayerClientCfg := cfg.Options.AgglayerClient
	if agglayerClientCfg.GRPC == nil || agglayerClientCfg.GRPC.URL == "" {
		return nil, fmt.Errorf("agglayerClient.grpc.url is required for step wait")
	}

	client, err := agglayer.NewAgglayerClient(agglayerClientCfg, log.GetDefaultLogger())
	if err != nil {
		return nil, fmt.Errorf("create agglayer gRPC client: %w", err)
	}

	return runStepWait(ctx, cfg, client, submitResult)
}

// runStepWait is the client-injectable core of RunStepWait (tests pass an agglayer client mock in
// place of the real gRPC client). It polls the certificate until it is final, errors if it settled
// InError, and then confirms the settlement on L1.
func runStepWait(
	ctx context.Context, cfg *Config, client agglayer.AgglayerClientInterface, submitResult *StepSubmitResult,
) (*StepWaitResult, error) {
	certHash := submitResult.CertificateHash

	start := time.Now()
	result := &StepWaitResult{CertificateHash: certHash}

	// Poll the submitted certificate by hash until it reaches a final state.
	log.Infof("Polling submitted certificate %s every %s...", certHash.Hex(), waitPollInterval)
	finalHeader, err := waitUntilFinal(ctx, client, certHash)
	if err != nil {
		return nil, err
	}

	elapsed := time.Since(start)
	result.FinalStatus = finalHeader.Status
	result.SettlementTxHash = finalHeader.SettlementTxHash
	result.ElapsedSeconds = elapsed.Seconds()

	if !finalHeader.Status.IsSettled() {
		errMsg := ""
		if finalHeader.Error != nil {
			errMsg = finalHeader.Error.Error()
		}
		log.Errorf("Certificate entered InError after %s: %s", elapsed.Round(time.Second), errMsg)
		return nil, fmt.Errorf("certificate %s is in error after %s: %s",
			certHash.Hex(), elapsed.Round(time.Second), errMsg)
	}

	log.Infof("Certificate settled in %s", elapsed.Round(time.Second))
	if finalHeader.SettlementTxHash != nil {
		log.Infof("Settlement tx: %s", finalHeader.SettlementTxHash.Hex())
	}

	// Confirm the settlement on L1: the RollupManager must have emitted a
	// VerifyBatchesTrustedAggregator event for our rollupID with this certificate's exit root.
	if err := confirmVerifyBatchesOnL1(ctx, cfg, submitResult, finalHeader.NewLocalExitRoot, result); err != nil {
		return nil, err
	}

	log.Info("STEP WAIT complete")
	return result, nil
}

// waitUntilFinal polls GetCertificateHeader every waitPollInterval until the certificate
// reaches a closed state (Settled or InError) and returns the final header.
func waitUntilFinal(
	ctx context.Context, client agglayer.AgglayerClientInterface, certHash common.Hash,
) (*agglayertypes.CertificateHeader, error) {
	var lastStatus agglayertypes.CertificateStatus = -1
	start := time.Now()

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled after %s: %w", time.Since(start).Round(time.Second), ctx.Err())
		case <-time.After(waitPollInterval):
		}

		header, err := client.GetCertificateHeader(ctx, certHash)
		if err != nil {
			log.Warnf("GetCertificateHeader(%s) error (will retry): %v", certHash.Hex(), err)
			continue
		}

		if header.Status != lastStatus {
			log.Infof("[%s] status: %s (elapsed: %s)",
				certHash.Hex()[:10], header.Status, time.Since(start).Round(time.Second))
			lastStatus = header.Status
		}

		if header.Status.IsClosed() {
			return header, nil
		}
	}
}

// confirmVerifyBatchesOnL1 confirms the just-settled certificate also landed on L1: it scans the
// RollupManager for the VerifyBatchesTrustedAggregator event between the L1 block captured right
// before submission and the finalized block, matching the rollupID (cfg.L2NetworkID) and the
// certificate's NewLocalExitRoot. On success it records the L1 block and tx hash in result.
//
// The RollupManager address is taken from cfg.RollupManagerAddress when set, otherwise resolved
// on-chain from the consensus contract (cfg.SovereignRollupAddr.rollupManager()). It errors when
// l1RpcUrl is unset or neither the RollupManager nor the sovereign-rollup address is available.
func confirmVerifyBatchesOnL1(
	ctx context.Context, cfg *Config, submitResult *StepSubmitResult, exitRoot common.Hash, result *StepWaitResult,
) error {
	if cfg.L1RPCURL == "" {
		return fmt.Errorf("l1RpcUrl is required to confirm the certificate's L1 settlement " +
			"(VerifyBatchesTrustedAggregator)")
	}

	rollupManagerAddr, err := resolveRollupManagerAddress(ctx, cfg)
	if err != nil {
		return fmt.Errorf("resolve rollupManager address: %w", err)
	}
	if rollupManagerAddr == (common.Address{}) {
		return fmt.Errorf("cannot confirm the certificate's L1 settlement: set rollupManagerAddress, " +
			"or sovereignRollupAddr so it can be resolved on-chain")
	}

	fromBlock := submitResult.L1LatestBlockBeforeSubmittingCertificate
	rollupID := cfg.L2NetworkID
	log.Infof("Confirming L1 settlement: scanning RollupManager %s for VerifyBatchesTrustedAggregator "+
		"(rollupID=%d, exitRoot=%s) from L1 block %d to finalized...",
		rollupManagerAddr.Hex(), rollupID, exitRoot.Hex(), fromBlock)

	blockNumber, txHash, err := waitForVerifyBatchesOnL1(ctx, cfg, rollupManagerAddr, fromBlock, rollupID, exitRoot)
	if err != nil {
		return fmt.Errorf("confirm VerifyBatchesTrustedAggregator on L1: %w", err)
	}

	result.VerifyBatchesL1Block = blockNumber
	result.VerifyBatchesTxHash = &txHash
	log.Infof("✅ Certificate settled on L1: VerifyBatchesTrustedAggregator found at block %d (tx: %s)",
		blockNumber, txHash.Hex())

	// The same L1 block carries the GlobalExitRoot contract's L1 info tree updates.
	if err := fetchGERUpdatesInBlock(ctx, cfg, blockNumber, result); err != nil {
		return fmt.Errorf("fetch L1 info tree updates at block %d: %w", blockNumber, err)
	}
	return nil
}

// fetchGERUpdatesInBlock reads the L1 GlobalExitRoot contract's UpdateL1InfoTree and
// UpdateL1InfoTreeV2 events from the given L1 block (where VerifyBatchesTrustedAggregator landed)
// and stores the last occurrence of each on result. Both events are emitted when the global exit
// root is updated as part of the settlement, so each must be present — a missing event is an error.
func fetchGERUpdatesInBlock(ctx context.Context, cfg *Config, blockNumber uint64, result *StepWaitResult) error {
	if cfg.L1GlobalExitRootAddress == (common.Address{}) {
		return fmt.Errorf("l1GlobalExitRootAddress is required to read the L1 info tree updates")
	}

	v1, err := fetchLastUpdateL1InfoTree(ctx, cfg, blockNumber)
	if err != nil {
		return err
	}
	v2, err := fetchLastUpdateL1InfoTreeV2(ctx, cfg, blockNumber)
	if err != nil {
		return err
	}

	result.UpdateL1InfoTree = v1
	result.UpdateL1InfoTreeV2 = v2
	log.Infof("UpdateL1InfoTree at block %d (tx: %s): mainnetExitRoot=%s rollupExitRoot=%s",
		blockNumber, v1.TxHash.Hex(), v1.MainnetExitRoot.Hex(), v1.RollupExitRoot.Hex())
	log.Infof("UpdateL1InfoTreeV2 at block %d (tx: %s): leafCount=%d currentL1InfoRoot=%s minTimestamp=%d",
		blockNumber, v2.TxHash.Hex(), v2.LeafCount, v2.CurrentL1InfoRoot.Hex(), v2.MinTimestamp)
	return nil
}

// rawLog is the subset of an eth_getLogs entry we decode for the GlobalExitRoot events.
type rawLog struct {
	Topics []string `json:"topics"`
	Data   string   `json:"data"`
	TxHash string   `json:"transactionHash"`
}

// fetchLogsInBlock returns every log emitted by addr with the given topic[0] in a single L1 block.
func fetchLogsInBlock(
	ctx context.Context, rpcURL string, addr common.Address, topic common.Hash, blockNumber uint64,
) ([]rawLog, error) {
	tag := toBlockTag(blockNumber)
	result, err := singleRPC(ctx, rpcURL, "eth_getLogs", []any{
		map[string]any{
			"address":   addr.Hex(),
			"topics":    []string{topic.Hex()},
			"fromBlock": tag,
			"toBlock":   tag,
		},
	}, defaultRetries)
	if err != nil {
		return nil, err
	}
	var logs []rawLog
	if err := json.Unmarshal(result, &logs); err != nil {
		return nil, fmt.Errorf("unmarshal logs: %w", err)
	}
	return logs, nil
}

// fetchLastUpdateL1InfoTree returns the last UpdateL1InfoTree event in blockNumber.
func fetchLastUpdateL1InfoTree(ctx context.Context, cfg *Config, blockNumber uint64) (*L1InfoTreeUpdate, error) {
	logs, err := fetchLogsInBlock(ctx, cfg.L1RPCURL, cfg.L1GlobalExitRootAddress, updateL1InfoTreeTopic, blockNumber)
	if err != nil {
		return nil, fmt.Errorf("query UpdateL1InfoTree: %w", err)
	}
	if len(logs) == 0 {
		return nil, fmt.Errorf("no UpdateL1InfoTree event found in block %d", blockNumber)
	}

	// mainnetExitRoot and rollupExitRoot are both indexed (topics[1], topics[2]).
	last := logs[len(logs)-1]
	if len(last.Topics) < updateL1InfoTreeMinTopics {
		return nil, fmt.Errorf("UpdateL1InfoTree log has only %d topics", len(last.Topics))
	}
	return &L1InfoTreeUpdate{
		MainnetExitRoot: common.HexToHash(last.Topics[1]),
		RollupExitRoot:  common.HexToHash(last.Topics[2]),
		TxHash:          common.HexToHash(last.TxHash),
	}, nil
}

// fetchLastUpdateL1InfoTreeV2 returns the last UpdateL1InfoTreeV2 event in blockNumber.
func fetchLastUpdateL1InfoTreeV2(ctx context.Context, cfg *Config, blockNumber uint64) (*L1InfoTreeV2Update, error) {
	logs, err := fetchLogsInBlock(
		ctx, cfg.L1RPCURL, cfg.L1GlobalExitRootAddress, updateL1InfoTreeV2TopicWait, blockNumber)
	if err != nil {
		return nil, fmt.Errorf("query UpdateL1InfoTreeV2: %w", err)
	}
	if len(logs) == 0 {
		return nil, fmt.Errorf("no UpdateL1InfoTreeV2 event found in block %d", blockNumber)
	}

	last := logs[len(logs)-1]
	if len(last.Topics) < minTopicsForLeaf {
		return nil, fmt.Errorf("UpdateL1InfoTreeV2 log has only %d topics", len(last.Topics))
	}
	// leafCount is indexed (topics[1]); data = currentL1InfoRoot[0:32] ++ blockhash[32:64] ++
	// minTimestamp[64:96].
	leafCount, err := safeUint32(new(big.Int).SetBytes(common.FromHex(last.Topics[1])))
	if err != nil {
		return nil, fmt.Errorf("decode UpdateL1InfoTreeV2 leafCount: %w", err)
	}
	data := common.FromHex(last.Data)
	if len(data) < verifyBatchesDataLen {
		return nil, fmt.Errorf("UpdateL1InfoTreeV2 log has %d data bytes, expected %d",
			len(data), verifyBatchesDataLen)
	}
	return &L1InfoTreeV2Update{
		CurrentL1InfoRoot: common.BytesToHash(data[0:32]),
		LeafCount:         leafCount,
		Blockhash:         common.BytesToHash(data[32:64]),
		MinTimestamp:      new(big.Int).SetBytes(data[64:96]).Uint64(),
		TxHash:            common.HexToHash(last.TxHash),
	}, nil
}

// resolveRollupManagerAddress returns cfg.RollupManagerAddress when set, otherwise reads it from the
// consensus contract via cfg.SovereignRollupAddr.rollupManager() (PolygonConsensusBase) on L1.
// Returns the zero address (no error) when neither is available.
func resolveRollupManagerAddress(ctx context.Context, cfg *Config) (common.Address, error) {
	if cfg.RollupManagerAddress != (common.Address{}) {
		return cfg.RollupManagerAddress, nil
	}
	if cfg.SovereignRollupAddr == (common.Address{}) {
		return common.Address{}, nil
	}

	result, err := singleRPC(ctx, cfg.L1RPCURL, "eth_call", []any{
		map[string]string{"to": cfg.SovereignRollupAddr.Hex(), "data": rollupManagerSelector},
		"latest",
	}, defaultRetries)
	if err != nil {
		return common.Address{}, fmt.Errorf("call rollupManager() on %s: %w", cfg.SovereignRollupAddr.Hex(), err)
	}

	var hex string
	if err := json.Unmarshal(result, &hex); err != nil {
		return common.Address{}, fmt.Errorf("parse rollupManager() result: %w", err)
	}
	addr := common.HexToAddress(hex)
	if addr == (common.Address{}) {
		return common.Address{}, fmt.Errorf("rollupManager() on %s returned the zero address", cfg.SovereignRollupAddr.Hex())
	}
	log.Infof("Resolved rollupManager %s from sovereignRollupAddr %s", addr.Hex(), cfg.SovereignRollupAddr.Hex())
	return addr, nil
}

// waitForVerifyBatchesOnL1 polls L1 until the RollupManager's VerifyBatchesTrustedAggregator event
// for rollupID with the given exitRoot appears in [fromBlock, finalized]. The settlement tx may not
// be finalized yet when the certificate first reports Settled, so it re-resolves the finalized block
// and re-scans every waitPollInterval until the event is found or the context is cancelled.
func waitForVerifyBatchesOnL1(
	ctx context.Context, cfg *Config, rollupManagerAddr common.Address,
	fromBlock uint64, rollupID uint32, exitRoot common.Hash,
) (blockNumber uint64, txHash common.Hash, err error) {
	chunkSize := uint64(cfg.Options.BlockRange)
	if chunkSize == 0 {
		chunkSize = defaultBlockRange
	}
	start := time.Now()

	for {
		toBlock, ferr := resolveFinalizedBlock(ctx, cfg.L1RPCURL)
		if ferr != nil {
			log.Warnf("resolve finalized L1 block error (will retry): %v", ferr)
		} else if toBlock >= fromBlock {
			block, tx, found, serr := scanVerifyBatches(
				ctx, cfg.L1RPCURL, rollupManagerAddr, rollupID, exitRoot, fromBlock, toBlock, chunkSize)
			if serr != nil {
				log.Warnf("scan VerifyBatchesTrustedAggregator [%d-%d] error (will retry): %v", fromBlock, toBlock, serr)
			} else if found {
				return block, tx, nil
			} else {
				log.Infof("VerifyBatchesTrustedAggregator not found yet in [%d-%d] (elapsed: %s), waiting...",
					fromBlock, toBlock, time.Since(start).Round(time.Second))
			}
		}

		select {
		case <-ctx.Done():
			return 0, common.Hash{}, fmt.Errorf(
				"context cancelled after %s waiting for VerifyBatchesTrustedAggregator: %w",
				time.Since(start).Round(time.Second), ctx.Err())
		case <-time.After(waitPollInterval):
		}
	}
}

// scanVerifyBatches scans [fromBlock, toBlock] forward in chunkSize-sized ranges for the
// VerifyBatchesTrustedAggregator event filtered by rollupID, returning the first log whose exitRoot
// matches the given one.
func scanVerifyBatches(
	ctx context.Context, rpcURL string, contractAddr common.Address, rollupID uint32, exitRoot common.Hash,
	fromBlock, toBlock, chunkSize uint64,
) (blockNumber uint64, txHash common.Hash, found bool, err error) {
	for start := fromBlock; start <= toBlock; start += chunkSize {
		end := min(start+chunkSize-1, toBlock)

		block, tx, ok, qerr := queryVerifyBatches(ctx, rpcURL, contractAddr, rollupID, exitRoot, start, end)
		if qerr != nil {
			return 0, common.Hash{}, false, qerr
		}
		if ok {
			return block, tx, true, nil
		}
	}
	return 0, common.Hash{}, false, nil
}

// queryVerifyBatches fetches VerifyBatchesTrustedAggregator logs for the given rollupID in
// [fromBlock, toBlock] and returns the first one whose exitRoot (data[64:96]) matches exitRoot.
func queryVerifyBatches(
	ctx context.Context, rpcURL string, contractAddr common.Address, rollupID uint32, exitRoot common.Hash,
	fromBlock, toBlock uint64,
) (blockNumber uint64, txHash common.Hash, found bool, err error) {
	// topics[1] is the indexed rollupID, ABI-encoded as a 32-byte big-endian value.
	rollupIDTopic := common.BigToHash(new(big.Int).SetUint64(uint64(rollupID)))
	result, err := singleRPC(ctx, rpcURL, "eth_getLogs", []any{
		map[string]any{
			"address":   contractAddr.Hex(),
			"topics":    []string{verifyBatchesTrustedAggregatorTopic.Hex(), rollupIDTopic.Hex()},
			"fromBlock": toBlockTag(fromBlock),
			"toBlock":   toBlockTag(toBlock),
		},
	}, defaultRetries)
	if err != nil {
		return 0, common.Hash{}, false, err
	}

	var logs []struct {
		BlockNumber string `json:"blockNumber"`
		TxHash      string `json:"transactionHash"`
		Data        string `json:"data"`
	}
	if err := json.Unmarshal(result, &logs); err != nil {
		return 0, common.Hash{}, false, fmt.Errorf("unmarshal VerifyBatchesTrustedAggregator logs: %w", err)
	}

	for _, l := range logs {
		data := common.FromHex(l.Data)
		if len(data) < verifyBatchesDataLen {
			log.Warnf("VerifyBatchesTrustedAggregator log has %d data bytes, expected %d — skipping",
				len(data), verifyBatchesDataLen)
			continue
		}
		// data layout: [0:32] numBatch, [32:64] stateRoot, [64:96] exitRoot.
		if common.BytesToHash(data[64:96]) == exitRoot {
			return hexToUint64(l.BlockNumber), common.HexToHash(l.TxHash), true, nil
		}
	}
	return 0, common.Hash{}, false, nil
}

// resolveFinalizedBlock returns the number of the latest finalized L1 block.
func resolveFinalizedBlock(ctx context.Context, rpcURL string) (uint64, error) {
	result, err := singleRPC(ctx, rpcURL, "eth_getBlockByNumber", []any{"finalized", false}, defaultRetries)
	if err != nil {
		return 0, err
	}
	var block struct {
		Number string `json:"number"`
	}
	if err := json.Unmarshal(result, &block); err != nil {
		return 0, fmt.Errorf("parse finalized block: %w", err)
	}
	if block.Number == "" {
		return 0, fmt.Errorf("finalized block not available")
	}
	return hexToUint64(block.Number), nil
}
