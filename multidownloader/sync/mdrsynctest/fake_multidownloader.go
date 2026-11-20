// Package mdrsynctest provides an in-memory multidownloader to test the syncers that are synced
// through the multidownloader, without the RPC and the storage that a real multidownloader needs.
package mdrsynctest

import (
	"context"
	"fmt"
	"math/big"
	"sync"

	aggkitcommon "github.com/agglayer/aggkit/common"
	mdrsynctypes "github.com/agglayer/aggkit/multidownloader/sync/types"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

var (
	_ mdrsynctypes.MultidownloaderInterface = (*FakeMultidownloader)(nil)
	_ aggkittypes.TransactionProvider       = (*FakeMultidownloader)(nil)
)

type blockKey struct {
	number uint64
	hash   common.Hash
}

type block struct {
	header aggkittypes.BlockHeader
	logs   []mdrtypes.Log
}

// FakeMultidownloader is an in-memory multidownloader. Its chain is built by the test with
// AddBlock, and a reorg is simulated with Reorg: as the real one does, it removes the reorged blocks
// (that are no longer available until new ones are added) and it remembers them as reorged.
// It is also a transaction provider, that returns Tx for the logs of the canonical blocks.
type FakeMultidownloader struct {
	// Tx is the transaction that TransactionByLog returns for the logs of the canonical blocks
	Tx *aggkittypes.RPCTransaction
	// RegisterErr is the error that RegisterSyncer returns
	RegisterErr error

	mu             sync.Mutex
	firstBlock     uint64
	blocks         []block
	hashNonce      int64
	finalizedBlock uint64
	reorgs         []mdrtypes.ReorgData
	reorgedBlocks  map[blockKey]uint64
	registered     []aggkittypes.SyncerConfig
}

// NewFakeMultidownloader creates a FakeMultidownloader whose first block will be firstBlock
func NewFakeMultidownloader(firstBlock uint64) *FakeMultidownloader {
	return &FakeMultidownloader{
		firstBlock:    firstBlock,
		reorgedBlocks: make(map[blockKey]uint64),
	}
}

// AddBlock adds the next block to the chain, with the given logs, and returns its header
func (f *FakeMultidownloader) AddBlock(logs ...mdrtypes.Log) aggkittypes.BlockHeader {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hashNonce++
	number := f.firstBlock + uint64(len(f.blocks))
	header := aggkittypes.BlockHeader{
		Number: number,
		Hash:   common.BigToHash(big.NewInt(f.hashNonce)),
		Time:   number,
	}
	blockLogs := make([]mdrtypes.Log, 0, len(logs))
	for i, l := range logs {
		l.BlockNumber = number
		l.BlockTimestamp = header.Time
		l.Index = uint(i)
		blockLogs = append(blockLogs, l)
	}
	f.blocks = append(f.blocks, block{header: header, logs: blockLogs})
	return header
}

// AddEmptyBlocks adds count blocks without logs to the chain
func (f *FakeMultidownloader) AddEmptyBlocks(count int) {
	for range count {
		f.AddBlock()
	}
}

// SetFinalizedBlock sets the last finalized block
func (f *FakeMultidownloader) SetFinalizedBlock(number uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finalizedBlock = number
}

// Reorg simulates the detection of a reorg that affects the blocks >= fromBlock: they are removed
// from the chain and stored as reorged. It returns the ID of the reorg.
func (f *FakeMultidownloader) Reorg(fromBlock uint64) uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	lastBlock, ok := f.lastBlockNumber()
	if !ok || fromBlock < f.firstBlock || fromBlock > lastBlock {
		panic(fmt.Sprintf("mdrsynctest: can't reorg from block %d", fromBlock))
	}
	reorgID := uint64(len(f.reorgs)) + 1
	keep := fromBlock - f.firstBlock
	for _, b := range f.blocks[keep:] {
		f.reorgedBlocks[blockKey{number: b.header.Number, hash: b.header.Hash}] = reorgID
	}
	f.blocks = f.blocks[:keep]
	f.reorgs = append(f.reorgs, mdrtypes.ReorgData{
		ReorgID:                   reorgID,
		BlockRangeAffected:        aggkitcommon.NewBlockRange(fromBlock, lastBlock),
		DetectedAtBlock:           lastBlock,
		DetectedTimestamp:         lastBlock,
		NetworkLatestBlock:        lastBlock,
		NetworkFinalizedBlock:     f.finalizedBlock,
		NetworkFinalizedBlockName: aggkittypes.FinalizedBlock,
	})
	return reorgID
}

// RegisteredConfigs returns the configs of the syncers that have been registered
func (f *FakeMultidownloader) RegisteredConfigs() []aggkittypes.SyncerConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]aggkittypes.SyncerConfig(nil), f.registered...)
}

// RegisterSyncer stores the config of the syncer
func (f *FakeMultidownloader) RegisterSyncer(data aggkittypes.SyncerConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.RegisterErr != nil {
		return f.RegisterErr
	}
	f.registered = append(f.registered, data)
	return nil
}

// GetLastReorgData returns the last reorg, or nil if there is none
func (f *FakeMultidownloader) GetLastReorgData(context.Context) (*mdrtypes.ReorgData, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reorgs) == 0 {
		return nil, nil
	}
	reorgData := f.reorgs[len(f.reorgs)-1]
	return &reorgData, nil
}

// TransactionByLog returns Tx if the block of the log is in the chain, otherwise the log is stale
func (f *FakeMultidownloader) TransactionByLog(_ context.Context,
	log ethtypes.Log) (*aggkittypes.RPCTransaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.block(log.BlockNumber)
	if b == nil || b.header.Hash != log.BlockHash {
		return nil, fmt.Errorf("block %d (%s) is not in the chain: %w", log.BlockNumber, log.BlockHash.Hex(),
			aggkittypes.ErrTransactionInclusionMismatch)
	}
	return f.Tx, nil
}

// CheckValidBlock checks if the block is in the chain, if not it returns the ID of its reorg
func (f *FakeMultidownloader) CheckValidBlock(_ context.Context, blockNumber uint64,
	blockHash common.Hash) (bool, uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if b := f.block(blockNumber); b != nil && b.header.Hash == blockHash {
		return true, 0, nil
	}
	if reorgID, ok := f.reorgedBlocks[blockKey{number: blockNumber, hash: blockHash}]; ok {
		return false, reorgID, nil
	}
	return false, 0, fmt.Errorf("mdrsynctest: block %d (%s) is unknown", blockNumber, blockHash.Hex())
}

// GetReorgedDataByReorgID returns the reorg with the given ID, or nil if it doesn't exist
func (f *FakeMultidownloader) GetReorgedDataByReorgID(_ context.Context, reorgID uint64) (*mdrtypes.ReorgData, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if reorgID == 0 || reorgID > uint64(len(f.reorgs)) {
		return nil, nil
	}
	reorgData := f.reorgs[reorgID-1]
	return &reorgData, nil
}

// IsAvailable returns true if all the blocks of the query are in the chain
func (f *FakeMultidownloader) IsAvailable(query mdrtypes.LogQuery) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	lastBlock, ok := f.lastBlockNumber()
	return ok && query.BlockRange.FromBlock >= f.firstBlock && query.BlockRange.ToBlock <= lastBlock
}

// IsPartiallyAvailable returns true, and the available query, if the first blocks of the query are
// in the chain
func (f *FakeMultidownloader) IsPartiallyAvailable(query mdrtypes.LogQuery) (bool, *mdrtypes.LogQuery) {
	f.mu.Lock()
	defer f.mu.Unlock()
	lastBlock, ok := f.lastBlockNumber()
	if !ok || query.BlockRange.FromBlock < f.firstBlock || query.BlockRange.FromBlock > lastBlock {
		return false, nil
	}
	partialQuery := mdrtypes.NewLogQuery(query.BlockRange.FromBlock,
		min(query.BlockRange.ToBlock, lastBlock), query.Addrs)
	return true, &partialQuery
}

// LogQuery returns the blocks of the query that have logs
func (f *FakeMultidownloader) LogQuery(_ context.Context, query mdrtypes.LogQuery) (mdrtypes.LogQueryResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	response := mdrtypes.LogQueryResponse{
		ResponseRange: query.BlockRange,
		UnsafeRange:   aggkitcommon.BlockRangeZero,
	}
	if query.BlockRange.ToBlock > f.finalizedBlock {
		response.UnsafeRange = aggkitcommon.NewBlockRange(
			max(query.BlockRange.FromBlock, f.finalizedBlock+1), query.BlockRange.ToBlock)
	}
	for number := query.BlockRange.FromBlock; number <= query.BlockRange.ToBlock; number++ {
		b := f.block(number)
		if b == nil {
			return mdrtypes.LogQueryResponse{}, fmt.Errorf("mdrsynctest: block %d is not available", number)
		}
		if len(b.logs) == 0 {
			continue
		}
		response.Blocks = append(response.Blocks, mdrtypes.BlockWithLogs{
			Header:  b.header,
			IsFinal: number <= f.finalizedBlock,
			Logs:    append([]mdrtypes.Log(nil), b.logs...),
		})
	}
	return response, nil
}

// Finality is the block that the multidownloader considers final
func (f *FakeMultidownloader) Finality() aggkittypes.BlockNumberFinality {
	return aggkittypes.FinalizedBlock
}

// HeaderByNumber returns the header of the block: the finalized block, a block number or, for
// any other block tag, the last block of the chain
func (f *FakeMultidownloader) HeaderByNumber(_ context.Context,
	number *aggkittypes.BlockNumberFinality) (*aggkittypes.BlockHeader, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	blockNumber, ok := f.resolveBlockNumber(number)
	b := f.block(blockNumber)
	if !ok || b == nil {
		return nil, fmt.Errorf("mdrsynctest: block %s not found", number.String())
	}
	header := b.header
	return &header, nil
}

// StorageHeaderByNumber returns the header of the block and if it is finalized, or nil if the
// block is not in the chain
func (f *FakeMultidownloader) StorageHeaderByNumber(_ context.Context,
	number *aggkittypes.BlockNumberFinality) (*aggkittypes.BlockHeader, mdrtypes.FinalizedType, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	blockNumber, ok := f.resolveBlockNumber(number)
	b := f.block(blockNumber)
	if !ok || b == nil {
		return nil, false, nil
	}
	header := b.header
	return &header, blockNumber <= f.finalizedBlock, nil
}

// ChainID returns the chain ID
func (f *FakeMultidownloader) ChainID(context.Context) (uint64, error) {
	return 1, nil
}

func (f *FakeMultidownloader) resolveBlockNumber(number *aggkittypes.BlockNumberFinality) (uint64, bool) {
	switch {
	case number == nil:
		return f.lastBlockNumber()
	case number.IsConstant():
		return number.Specific, true
	case number.IsFinalized():
		return f.finalizedBlock, true
	default:
		return f.lastBlockNumber()
	}
}

func (f *FakeMultidownloader) lastBlockNumber() (uint64, bool) {
	if len(f.blocks) == 0 {
		return 0, false
	}
	return f.firstBlock + uint64(len(f.blocks)) - 1, true
}

func (f *FakeMultidownloader) block(number uint64) *block {
	if number < f.firstBlock || number-f.firstBlock >= uint64(len(f.blocks)) {
		return nil
	}
	return &f.blocks[number-f.firstBlock]
}
