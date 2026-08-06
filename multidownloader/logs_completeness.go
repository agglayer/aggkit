package multidownloader

import (
	"context"
	"fmt"

	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// maxArbitrationAttempts bounds the number of by-hash re-queries used to arbitrate a suspicious
// block. It must stay small: arbitration runs synchronously inside a syncer step.
const maxArbitrationAttempts = 2

// findSuspiciousBlockNumbers scans headers for eth_getLogs completeness suspicion: a header whose
// logs bloom is positive for the addresses queried for that block, but for which logs contains no
// entry with that block number. Blooms have no false negatives, so a bloom-positive header with no
// matching log is a signal of a possible silent eth_getLogs omission. Bloom false positives are
// also possible and are deterministic per (block, address), so this function only flags suspicion
// -- callers MUST arbitrate (see arbitrateSuspiciousBlock) before ever treating it as an error.
// Headers with LogsBloom == nil are always skipped: the bloom is unavailable, so there is nothing
// to verify for that block (graceful degradation for RPCs/mocks that don't provide blooms).
func findSuspiciousBlockNumbers(headers []*aggkittypes.BlockHeader, logs []types.Log,
	addrsForBlock func(blockNumber uint64) []common.Address) []uint64 {
	blockNumbersWithLogs := make(map[uint64]struct{}, len(logs))
	for _, lg := range logs {
		blockNumbersWithLogs[lg.BlockNumber] = struct{}{}
	}
	suspicious := make([]uint64, 0, len(headers))
	for _, h := range headers {
		if h == nil || h.LogsBloom == nil {
			continue
		}
		if !h.BloomMightContainAddresses(addrsForBlock(h.Number)) {
			continue
		}
		if _, hasLog := blockNumbersWithLogs[h.Number]; hasLog {
			continue
		}
		suspicious = append(suspicious, h.Number)
	}
	return suspicious
}

// uniformAddrsForBlock adapts a single, range-wide address list (as used by the safe-step LogQuery)
// to the per-block addrsForBlock signature required by findSuspiciousBlockNumbers/verifyLogsCompleteness.
func uniformAddrsForBlock(addrs []common.Address) func(uint64) []common.Address {
	return func(uint64) []common.Address { return addrs }
}

// getAddrsForBlockNumbers returns, for each given block number, the addresses currently pending to
// sync for that block. It mirrors the mutex pattern used by getUnsafeLogQueries since it reads
// dh.state.
func (dh *EVMMultidownloader) getAddrsForBlockNumbers(blockNumbers []uint64) map[uint64][]common.Address {
	dh.mutex.Lock()
	defer dh.mutex.Unlock()
	result := make(map[uint64][]common.Address, len(blockNumbers))
	for _, bn := range blockNumbers {
		result[bn] = dh.state.GetAddressesToSyncForBlockNumber(bn)
	}
	return result
}

// verifyLogsCompleteness runs the eth_getLogs completeness check over headers and arbitrates every
// suspicious block it finds (see findSuspiciousBlockNumbers and arbitrateSuspiciousBlock). It
// returns an error as soon as arbitration confirms a genuine eth_getLogs omission.
func (dh *EVMMultidownloader) verifyLogsCompleteness(ctx context.Context, headers []*aggkittypes.BlockHeader,
	logs []types.Log, addrsForBlock func(blockNumber uint64) []common.Address) error {
	suspiciousNumbers := findSuspiciousBlockNumbers(headers, logs, addrsForBlock)
	if len(suspiciousNumbers) == 0 {
		return nil
	}
	headersByNumber := make(map[uint64]*aggkittypes.BlockHeader, len(headers))
	for _, h := range headers {
		if h != nil {
			headersByNumber[h.Number] = h
		}
	}
	for _, bn := range suspiciousNumbers {
		if err := dh.arbitrateSuspiciousBlock(ctx, headersByNumber[bn], addrsForBlock(bn)); err != nil {
			return err
		}
	}
	return nil
}

// arbitrateSuspiciousBlock decides whether a bloom-suspicious block is a genuine eth_getLogs
// omission or a bloom false positive. Bloom false positives are deterministic per (block, address):
// a naive "suspicious -> error" would wedge the syncer in an infinite retry loop on one, so every
// suspicion must be arbitrated here before it is ever allowed to produce an error.
//
// It re-queries logs for the single block by block hash, up to maxArbitrationAttempts times (a
// retry may land on a healthy node behind a load balancer/proxy). Only logs whose BlockHash matches
// hdr.Hash are counted, guarding against a misbehaving RPC returning logs for the wrong block.
//
//   - If any attempt returns >=1 matching log, this is a genuine omission: it logs at Warn (block
//     number, addresses, how many logs the original response missed) and returns an error so the
//     whole step retries -- a healthy/round-robin RPC heals it, while a persistently-broken RPC
//     produces a loud, bounded retry loop instead of silently corrupting the DB.
//   - If every attempt succeeds and returns no matching logs, this is treated as a bloom false
//     positive (or an omission pinned to this specific block hash, which is indistinguishable from
//     here): it logs at Debug and the block is accepted as empty.
//   - If every attempt fails to execute (RPC/transport error), no verdict can be reached; this is
//     treated conservatively as an error rather than silently accepting the block, since silently
//     accepting an unverifiable suspicious block is exactly the failure mode this check exists to
//     prevent.
func (dh *EVMMultidownloader) arbitrateSuspiciousBlock(ctx context.Context, hdr *aggkittypes.BlockHeader,
	addrs []common.Address) error {
	reachedVerdict := false
	for attempt := 1; attempt <= maxArbitrationAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		query := ethereum.FilterQuery{BlockHash: &hdr.Hash, Addresses: addrs}
		logs, err := dh.ethClient.FilterLogs(ctx, query)
		if err != nil {
			dh.log.Warnf("arbitrateSuspiciousBlock: attempt %d/%d: failed to re-query logs for block %d (hash %s): %s",
				attempt, maxArbitrationAttempts, hdr.Number, hdr.Hash.String(), err.Error())
			continue
		}
		matching := 0
		for _, lg := range logs {
			if lg.BlockHash == hdr.Hash {
				matching++
			}
		}
		if matching > 0 {
			dh.log.Warnf("arbitrateSuspiciousBlock: genuine eth_getLogs omission detected at block %d (hash %s) "+
				"for addrs %v: original response missed %d log(s)",
				hdr.Number, hdr.Hash.String(), addrs, matching)
			return fmt.Errorf("arbitrateSuspiciousBlock: eth_getLogs omitted %d log(s) for block %d (hash %s)",
				matching, hdr.Number, hdr.Hash.String())
		}
		reachedVerdict = true
	}
	if !reachedVerdict {
		return fmt.Errorf("arbitrateSuspiciousBlock: could not arbitrate suspicious block %d (hash %s): "+
			"all %d re-query attempts failed", hdr.Number, hdr.Hash.String(), maxArbitrationAttempts)
	}
	dh.log.Debugf("arbitrateSuspiciousBlock: block %d (hash %s) bloom-positive for addrs %v but no logs found "+
		"after %d arbitration attempt(s); treating as bloom false positive",
		hdr.Number, hdr.Hash.String(), addrs, maxArbitrationAttempts)
	return nil
}
