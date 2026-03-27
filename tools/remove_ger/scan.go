package remove_ger

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerger"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayergerl2"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/polygonzkevmbridge"
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/urfave/cli/v2"
)

const defaultScanChunkSize uint64 = 5000

var (
	scanClaimEventSignature         = crypto.Keccak256Hash([]byte("ClaimEvent(uint256,uint32,address,address,uint256)"))
	scanDetailedClaimEventSignature = crypto.Keccak256Hash([]byte(
		"DetailedClaimEvent(bytes32[32],bytes32[32],uint256,bytes32,bytes32,uint8,uint32,address,uint32,address,uint256,bytes)",
	))
)

type l1GERLookup interface {
	GlobalExitRootMap(opts *bind.CallOpts, gerHash [32]byte) (*big.Int, error)
}

type scanClaimRecord struct {
	BlockNum  uint64
	LogIndex  uint64
	TxHash    common.Hash
	GlobalGER common.Hash
	ClaimType claimsynctypes.ClaimType
	GlobalIdx *big.Int
}

type invalidGERUsage struct {
	GER        common.Hash
	ClaimCount int
	FirstBlock uint64
	LastBlock  uint64
	TxHashes   []common.Hash
}

// ScanInvalidClaimsParams defines the block range and chunk size for scan-invalid-claims.
type ScanInvalidClaimsParams struct {
	FromBlock uint64
	ToBlock   uint64
	ChunkSize uint64
}

// RunScanInvalidClaims scans L2 claim logs, validates the GER used by each claim on L1,
// and prints the invalid GERs that were used in claims.
func RunScanInvalidClaims(c *cli.Context) error {
	cfg, err := LoadConfig(c)
	if err != nil {
		return err
	}

	if !c.IsSet("from-block") {
		return fmt.Errorf("--from-block is required")
	}
	fromBlock := c.Uint64("from-block")

	chunkSize := c.Uint64("chunk-size")
	if chunkSize == 0 {
		chunkSize = defaultScanChunkSize
	}

	dialCtx, dialCancel := context.WithTimeout(c.Context, dialTimeout)
	env, err := SetupScanEnv(dialCtx, cfg)
	dialCancel()
	if err != nil {
		return err
	}
	defer env.Close()

	usages, scannedClaims, resolvedToBlock, err := ScanInvalidClaims(c.Context, env, ScanInvalidClaimsParams{
		FromBlock: fromBlock,
		ToBlock:   c.Uint64("to-block"),
		ChunkSize: chunkSize,
	})
	if err != nil {
		return err
	}

	printInvalidClaimScan(usages, fromBlock, resolvedToBlock, scannedClaims)
	return nil
}

// SetupScanEnv dials L1/L2 and initializes the contract bindings needed for scan-invalid-claims.
func SetupScanEnv(ctx context.Context, cfg *Config) (*Env, error) {
	l1Client, err := ethclient.DialContext(ctx, cfg.L1NetworkConfig.RPC.URL)
	if err != nil {
		return nil, fmt.Errorf("connect to L1: %w", err)
	}

	l2Client, err := ethclient.DialContext(ctx, cfg.Common.L2RPC.URL)
	if err != nil {
		l1Client.Close()
		return nil, fmt.Errorf("connect to L2: %w", err)
	}

	l2Bridge, err := agglayerbridgel2.NewAgglayerbridgel2(cfg.BridgeL2Sync.BridgeAddr, l2Client)
	if err != nil {
		l1Client.Close()
		l2Client.Close()
		return nil, fmt.Errorf("initialize L2 bridge binding: %w", err)
	}

	l2GER, err := agglayergerl2.NewAgglayergerl2(cfg.L2GERSync.GlobalExitRootL2Addr, l2Client)
	if err != nil {
		l1Client.Close()
		l2Client.Close()
		return nil, fmt.Errorf("initialize L2 GER manager binding: %w", err)
	}

	l1GER, err := agglayerger.NewAgglayerger(cfg.L2GERSync.GlobalExitRootL1Addr, l1Client)
	if err != nil {
		l1Client.Close()
		l2Client.Close()
		return nil, fmt.Errorf("initialize L1 GER manager binding: %w", err)
	}

	return &Env{
		L1:           l1Client,
		L2:           l2Client,
		L2NetworkID:  cfg.RemoveGER.L2NetworkID,
		L1GERManager: l1GER,
		L2Bridge:     l2Bridge,
		L2GERManager: l2GER,
		L2BridgeAddr: cfg.BridgeL2Sync.BridgeAddr,
	}, nil
}

// ScanInvalidClaims scans the L2 bridge logs for claims in the given block range,
// validates their GER on L1, and returns the invalid GERs used by those claims.
func ScanInvalidClaims(
	ctx context.Context,
	env *Env,
	params ScanInvalidClaimsParams,
) ([]invalidGERUsage, int, uint64, error) {
	if params.ChunkSize == 0 {
		params.ChunkSize = defaultScanChunkSize
	}

	toBlock := params.ToBlock
	if toBlock == 0 {
		var err error
		toBlock, err = env.L2.BlockNumber(ctx)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("get latest L2 block: %w", err)
		}
	}
	if params.FromBlock > toBlock {
		return nil, 0, 0, fmt.Errorf("invalid block range: from-block %d > to-block %d", params.FromBlock, toBlock)
	}

	claims, err := collectClaimsFromL2(ctx, env, params.FromBlock, toBlock, params.ChunkSize)
	if err != nil {
		return nil, 0, 0, err
	}

	usages, err := findInvalidGERUsages(ctx, env.L1GERManager, claims)
	if err != nil {
		return nil, 0, 0, err
	}

	return usages, len(claims), toBlock, nil
}

func collectClaimsFromL2(
	ctx context.Context,
	env *Env,
	fromBlock, toBlock, chunkSize uint64,
) ([]scanClaimRecord, error) {
	claimsByTx := make(map[common.Hash]scanClaimRecord)

	for start := fromBlock; start <= toBlock; {
		end := minUint64(start+chunkSize-1, toBlock)
		logs, err := env.L2.FilterLogs(ctx, ethereumFilterQuery(env.L2BridgeAddr, start, end))
		if err != nil {
			return nil, fmt.Errorf("filter claim logs [%d,%d]: %w", start, end, err)
		}

		for _, lg := range logs {
			if len(lg.Topics) == 0 {
				continue
			}

			switch lg.Topics[0] {
			case scanDetailedClaimEventSignature:
				record, err := parseDetailedClaimLog(env, lg)
				if err != nil {
					return nil, err
				}
				claimsByTx[lg.TxHash] = record
			case scanClaimEventSignature:
				if existing, ok := claimsByTx[lg.TxHash]; ok && existing.ClaimType == claimsynctypes.DetailedClaimEvent {
					continue
				}
				record, err := parseClaimLog(ctx, env, lg)
				if err != nil {
					return nil, err
				}
				claimsByTx[lg.TxHash] = record
			}
		}

		if end == toBlock {
			break
		}
		start = end + 1
	}

	claims := make([]scanClaimRecord, 0, len(claimsByTx))
	for _, claim := range claimsByTx {
		claims = append(claims, claim)
	}
	sort.Slice(claims, func(i, j int) bool {
		if claims[i].BlockNum != claims[j].BlockNum {
			return claims[i].BlockNum < claims[j].BlockNum
		}
		return claims[i].LogIndex < claims[j].LogIndex
	})

	return claims, nil
}

func parseDetailedClaimLog(env *Env, lg types.Log) (scanClaimRecord, error) {
	ev, err := env.L2Bridge.ParseDetailedClaimEvent(lg)
	if err != nil {
		return scanClaimRecord{}, fmt.Errorf("parse DetailedClaimEvent tx %s: %w", lg.TxHash.Hex(), err)
	}

	return scanClaimRecord{
		BlockNum:  lg.BlockNumber,
		LogIndex:  uint64(lg.Index),
		TxHash:    lg.TxHash,
		GlobalGER: crypto.Keccak256Hash(ev.MainnetExitRoot[:], ev.RollupExitRoot[:]),
		ClaimType: claimsynctypes.DetailedClaimEvent,
		GlobalIdx: ev.GlobalIndex,
	}, nil
}

func parseClaimLog(ctx context.Context, env *Env, lg types.Log) (scanClaimRecord, error) {
	ev, err := env.L2Bridge.ParseClaimEvent(lg)
	if err != nil {
		return scanClaimRecord{}, fmt.Errorf("parse ClaimEvent tx %s: %w", lg.TxHash.Hex(), err)
	}

	tx, _, err := env.L2.TransactionByHash(ctx, lg.TxHash)
	if err != nil {
		return scanClaimRecord{}, fmt.Errorf("load tx calldata for claim tx %s: %w", lg.TxHash.Hex(), err)
	}

	ger, err := decodeClaimGERFromTxData(tx.Data(), ev.GlobalIndex)
	if err != nil {
		return scanClaimRecord{}, fmt.Errorf("decode GER from claim tx %s: %w", lg.TxHash.Hex(), err)
	}

	return scanClaimRecord{
		BlockNum:  lg.BlockNumber,
		LogIndex:  uint64(lg.Index),
		TxHash:    lg.TxHash,
		GlobalGER: ger,
		ClaimType: claimsynctypes.ClaimEvent,
		GlobalIdx: ev.GlobalIndex,
	}, nil
}

func findInvalidGERUsages(
	ctx context.Context,
	l1GER l1GERLookup,
	claims []scanClaimRecord,
) ([]invalidGERUsage, error) {
	validityCache := make(map[common.Hash]bool, len(claims))
	aggregated := make(map[common.Hash]*invalidGERUsage)

	for _, claim := range claims {
		valid, ok := validityCache[claim.GlobalGER]
		if !ok {
			ts, err := l1GER.GlobalExitRootMap(&bind.CallOpts{Context: ctx}, claim.GlobalGER)
			if err != nil {
				return nil, fmt.Errorf("query L1 GER %s: %w", claim.GlobalGER.Hex(), err)
			}
			valid = ts != nil && ts.Sign() > 0
			validityCache[claim.GlobalGER] = valid
		}
		if valid {
			continue
		}

		usage, ok := aggregated[claim.GlobalGER]
		if !ok {
			usage = &invalidGERUsage{
				GER:        claim.GlobalGER,
				FirstBlock: claim.BlockNum,
				LastBlock:  claim.BlockNum,
			}
			aggregated[claim.GlobalGER] = usage
		}

		usage.ClaimCount++
		if claim.BlockNum < usage.FirstBlock {
			usage.FirstBlock = claim.BlockNum
		}
		if claim.BlockNum > usage.LastBlock {
			usage.LastBlock = claim.BlockNum
		}
		usage.TxHashes = append(usage.TxHashes, claim.TxHash)
	}

	result := make([]invalidGERUsage, 0, len(aggregated))
	for _, usage := range aggregated {
		result = append(result, *usage)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].FirstBlock != result[j].FirstBlock {
			return result[i].FirstBlock < result[j].FirstBlock
		}
		return strings.Compare(result[i].GER.Hex(), result[j].GER.Hex()) < 0
	})

	return result, nil
}

func decodeClaimGERFromTxData(txData []byte, globalIndex *big.Int) (common.Hash, error) {
	if len(txData) < 4 {
		return common.Hash{}, fmt.Errorf("tx input too short")
	}

	method, unpacked, err := unpackClaimMethod(txData)
	if err != nil {
		return common.Hash{}, err
	}

	claim := &claimsynctypes.Claim{GlobalIndex: globalIndex}
	switch len(unpacked) {
	case 11:
		found, err := claim.DecodeEtrogCalldata(unpacked)
		if err != nil {
			return common.Hash{}, fmt.Errorf("decode %s calldata: %w", method.Name, err)
		}
		if !found {
			return common.Hash{}, fmt.Errorf("decoded %s calldata did not match global index %s",
				method.Name, globalIndex.String())
		}
	case 10:
		found, err := claim.DecodePreEtrogCalldata(unpacked)
		if err != nil {
			return common.Hash{}, fmt.Errorf("decode %s calldata: %w", method.Name, err)
		}
		if !found {
			return common.Hash{}, fmt.Errorf("decoded %s calldata did not match global index %s",
				method.Name, globalIndex.String())
		}
	default:
		return common.Hash{}, fmt.Errorf("unsupported %s input arity: %d", method.Name, len(unpacked))
	}

	return claim.GlobalExitRoot, nil
}

func unpackClaimMethod(txData []byte) (*abi.Method, []any, error) {
	abis := []*bind.MetaData{
		agglayerbridgel2.Agglayerbridgel2MetaData,
		agglayerbridge.AgglayerbridgeMetaData,
		polygonzkevmbridge.PolygonzkevmbridgeMetaData,
	}

	for _, meta := range abis {
		contractABI, err := meta.GetAbi()
		if err != nil {
			return nil, nil, fmt.Errorf("load contract ABI: %w", err)
		}
		method, err := contractABI.MethodById(txData[:4])
		if err != nil {
			continue
		}
		if method.Name != "claimAsset" && method.Name != "claimMessage" {
			return nil, nil, fmt.Errorf("unexpected method %q", method.Name)
		}
		unpacked, err := method.Inputs.Unpack(txData[4:])
		if err != nil {
			return nil, nil, fmt.Errorf("unpack %s calldata: %w", method.Name, err)
		}
		return method, unpacked, nil
	}

	return nil, nil, fmt.Errorf("tx input does not match claimAsset/claimMessage for known bridge ABIs")
}

func printInvalidClaimScan(usages []invalidGERUsage, fromBlock, toBlock uint64, scannedClaims int) {
	fmt.Println("=== Scan Invalid Claims ===")
	fmt.Printf("Scanned blocks: %d -> %d\n", fromBlock, toBlock)
	fmt.Printf("Claims scanned: %d\n", scannedClaims)
	fmt.Printf("Invalid GERs found: %d\n", len(usages))
	fmt.Println()

	if len(usages) == 0 {
		fmt.Println("No invalid GERs were found in the scanned claim range.")
		return
	}

	for i, usage := range usages {
		fmt.Printf("%d. GER: %s\n", i+1, usage.GER.Hex())
		fmt.Printf("   Claims: %d\n", usage.ClaimCount)
		fmt.Printf("   Blocks: %d -> %d\n", usage.FirstBlock, usage.LastBlock)
		fmt.Printf("   Txs: %s\n", joinHashes(usage.TxHashes))
	}
}

func joinHashes(hashes []common.Hash) string {
	parts := make([]string, 0, len(hashes))
	for _, h := range hashes {
		parts = append(parts, h.Hex())
	}
	return strings.Join(parts, ", ")
}

func minUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

func ethereumFilterQuery(addr common.Address, fromBlock, toBlock uint64) ethereum.FilterQuery {
	return ethereum.FilterQuery{
		FromBlock: big.NewInt(0).SetUint64(fromBlock),
		ToBlock:   big.NewInt(0).SetUint64(toBlock),
		Addresses: []common.Address{addr},
		Topics:    [][]common.Hash{{scanClaimEventSignature, scanDetailedClaimEventSignature}},
	}
}
