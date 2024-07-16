package localbridgesync

import (
	"context"
	"math/big"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/etrog/polygonzkevmbridge"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/etrog/polygonzkevmbridgev2"
	"github.com/0xPolygon/cdk/etherman"
	"github.com/0xPolygon/cdk/log"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	waitForNewBlocksPeriod = time.Millisecond * 100
)

var (
	bridgeEventSignature        = crypto.Keccak256Hash([]byte("BridgeEvent(uint8,uint32,address,uint32,address,uint256,bytes,uint32)"))
	claimEventSignature         = crypto.Keccak256Hash([]byte("ClaimEvent(uint256,uint32,address,address,uint256)"))
	claimEventSignaturePreEtrog = crypto.Keccak256Hash([]byte("ClaimEvent(uint32,uint32,address,address,uint256)"))
)

type EthClienter interface {
	ethereum.LogFilterer
	ethereum.BlockNumberReader
	ethereum.ChainReader
	bind.ContractBackend
}

type downloaderInterface interface {
	waitForNewBlocks(ctx context.Context, lastBlockSeen uint64) (newLastBlock uint64)
	getEventsByBlockRange(ctx context.Context, fromBlock, toBlock uint64) []block
	getLogs(ctx context.Context, fromBlock, toBlock uint64) []types.Log
	appendLog(b *block, l types.Log)
	getBlockHeader(ctx context.Context, blockNum uint64) blockHeader
}

type downloader struct {
	syncBlockChunkSize uint64
	downloaderInterface
}

func newDownloader(
	bridgeAddr common.Address,
	ethClient EthClienter,
	syncBlockChunkSize uint64,
	blockFinalityType etherman.BlockNumberFinality,
) (*downloader, error) {
	bridgeContractV1, err := polygonzkevmbridge.NewPolygonzkevmbridge(bridgeAddr, ethClient)
	if err != nil {
		return nil, err
	}
	bridgeContractV2, err := polygonzkevmbridgev2.NewPolygonzkevmbridgev2(bridgeAddr, ethClient)
	if err != nil {
		return nil, err
	}
	finality, err := blockFinalityType.ToBlockNum()
	if err != nil {
		return nil, err
	}
	return &downloader{
		syncBlockChunkSize: syncBlockChunkSize,
		downloaderInterface: &downloaderImplementation{
			bridgeAddr:       bridgeAddr,
			bridgeContractV1: bridgeContractV1,
			bridgeContractV2: bridgeContractV2,
			ethClient:        ethClient,
			blockFinality:    finality,
		},
	}, nil
}

func (d *downloader) download(ctx context.Context, fromBlock uint64, downloadedCh chan block) {
	lastBlock := d.waitForNewBlocks(ctx, 0)
	for {
		select {
		case <-ctx.Done():
			log.Debug("closing channel")
			close(downloadedCh)
			return
		default:
		}
		toBlock := fromBlock + d.syncBlockChunkSize
		if toBlock > lastBlock {
			toBlock = lastBlock
		}
		if fromBlock > toBlock {
			log.Debug("waiting for new blocks, last block ", toBlock)
			lastBlock = d.waitForNewBlocks(ctx, toBlock)
			continue
		}
		log.Debugf("getting events from blocks %d to  %d", fromBlock, toBlock)
		blocks := d.getEventsByBlockRange(ctx, fromBlock, toBlock)
		for _, b := range blocks {
			log.Debugf("sending block %d to the driver (with events)", b.Num)
			downloadedCh <- b
		}
		if len(blocks) == 0 || blocks[len(blocks)-1].Num < toBlock {
			// Indicate the last downloaded block if there are not events on it
			log.Debugf("sending block %d to the driver (without evvents)", toBlock)
			downloadedCh <- block{
				blockHeader: d.getBlockHeader(ctx, toBlock),
			}
		}
		fromBlock = toBlock + 1
	}
}

type downloaderImplementation struct {
	bridgeAddr       common.Address
	bridgeContractV1 *polygonzkevmbridge.Polygonzkevmbridge
	bridgeContractV2 *polygonzkevmbridgev2.Polygonzkevmbridgev2
	ethClient        EthClienter
	blockFinality    *big.Int
}

func (d *downloaderImplementation) waitForNewBlocks(ctx context.Context, lastBlockSeen uint64) (newLastBlock uint64) {
	attempts := 0
	for {
		header, err := d.ethClient.HeaderByNumber(ctx, d.blockFinality)
		if err != nil {
			attempts++
			log.Error("error geting last block num from eth client: ", err)
			retryHandler("waitForNewBlocks", attempts)
			continue
		}
		if header.Number.Uint64() > lastBlockSeen {
			return header.Number.Uint64()
		}
		time.Sleep(waitForNewBlocksPeriod)
	}
}

func (d *downloaderImplementation) getEventsByBlockRange(ctx context.Context, fromBlock, toBlock uint64) []block {
	blocks := []block{}
	logs := d.getLogs(ctx, fromBlock, toBlock)
	for _, l := range logs {
		if len(blocks) == 0 || blocks[len(blocks)-1].Num < l.BlockNumber {
			blocks = append(blocks, block{
				blockHeader: blockHeader{
					Num:  l.BlockNumber,
					Hash: l.BlockHash,
				},
				Events: []BridgeEvent{},
			})
		}
		d.appendLog(&blocks[len(blocks)-1], l)
	}

	return blocks
}

func (d *downloaderImplementation) getLogs(ctx context.Context, fromBlock, toBlock uint64) []types.Log {
	query := ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(fromBlock),
		Addresses: []common.Address{d.bridgeAddr},
		Topics: [][]common.Hash{
			{bridgeEventSignature},
			{claimEventSignature},
			{claimEventSignaturePreEtrog},
		},
		ToBlock: new(big.Int).SetUint64(toBlock),
	}
	attempts := 0
	for {
		logs, err := d.ethClient.FilterLogs(ctx, query)
		if err != nil {
			attempts++
			log.Error("error calling FilterLogs to eth client: ", err)
			retryHandler("getLogs", attempts)
			continue
		}
		return logs
	}
}

func (d *downloaderImplementation) appendLog(b *block, l types.Log) {
	switch l.Topics[0] {
	case bridgeEventSignature:
		bridge, err := d.bridgeContractV2.ParseBridgeEvent(l)
		if err != nil {
			log.Fatalf(
				"error parsing log %+v using d.bridgeContractV2.ParseBridgeEvent: %v",
				l, err,
			)
		}
		b.Events = append(b.Events, BridgeEvent{Bridge: &Bridge{
			LeafType:           bridge.LeafType,
			OriginNetwork:      bridge.OriginNetwork,
			OriginAddress:      bridge.OriginAddress,
			DestinationNetwork: bridge.DestinationNetwork,
			DestinationAddress: bridge.DestinationAddress,
			Amount:             bridge.Amount,
			Metadata:           bridge.Metadata,
			DepositCount:       bridge.DepositCount,
		}})
	case claimEventSignature:
		claim, err := d.bridgeContractV2.ParseClaimEvent(l)
		if err != nil {
			log.Fatalf(
				"error parsing log %+v using d.bridgeContractV2.ParseClaimEvent: %v",
				l, err,
			)
		}
		b.Events = append(b.Events, BridgeEvent{Claim: &Claim{
			GlobalIndex:        claim.GlobalIndex,
			OriginNetwork:      claim.OriginNetwork,
			OriginAddress:      claim.OriginAddress,
			DestinationAddress: claim.DestinationAddress,
			Amount:             claim.Amount,
		}})
	case claimEventSignaturePreEtrog:
		claim, err := d.bridgeContractV1.ParseClaimEvent(l)
		if err != nil {
			log.Fatalf(
				"error parsing log %+v using d.bridgeContractV1.ParseClaimEvent: %v",
				l, err,
			)
		}
		b.Events = append(b.Events, BridgeEvent{Claim: &Claim{
			GlobalIndex:        big.NewInt(int64(claim.Index)),
			OriginNetwork:      claim.OriginNetwork,
			OriginAddress:      claim.OriginAddress,
			DestinationAddress: claim.DestinationAddress,
			Amount:             claim.Amount,
		}})
	default:
		log.Fatalf("unexpected log %+v", l)
	}
}

func (d *downloaderImplementation) getBlockHeader(ctx context.Context, blockNum uint64) blockHeader {
	attempts := 0
	for {
		header, err := d.ethClient.HeaderByNumber(ctx, big.NewInt(int64(blockNum)))
		if err != nil {
			attempts++
			log.Errorf("error getting block header for block %d, err: %v", blockNum, err)
			retryHandler("getBlockHeader", attempts)
			continue
		}
		return blockHeader{
			Num:  header.Number.Uint64(),
			Hash: header.Hash(),
		}
	}
}
