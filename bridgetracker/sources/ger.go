package sources

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerger"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayergerl2"
	"github.com/agglayer/aggkit/bridgeservicefinder"
	"github.com/agglayer/aggkit/bridgetracker"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	trackertypes "github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

const MainnetNetworkID = 0

// updateHashChainValueSignature is the topic0 of AgglayerGERL2's UpdateHashChainValue event —
// event UpdateHashChainValue(bytes32 indexed newGlobalExitRoot, bytes32 indexed
// newHashChainValue) — the event findL2InjectionBlockBackwards scans for. Not referenced by the
// filtering itself (l2GERManager.FilterUpdateHashChainValue's ABI binding computes it), only
// documented here for the same reason l2gersync names it (insertGEREventSignature)
var updateHashChainValueSignature = crypto.Keccak256Hash([]byte("UpdateHashChainValue(bytes32,bytes32)"))

// errNotCoveredYet marks a bridge not covered by any L1 info tree leaf yet, as opposed to a
// transient failure (URL resolution, network). It never escapes this package
var errNotCoveredYet = errors.New("bridge not covered by any L1 info tree leaf yet")

// GERSource implements bridgetracker.GERSource over the destination network's aggkit bridge
// service: every aggkit bridge service syncs the L1 info tree, and the destination's one is
// the instance whose injected GERs matter for the claim
type GERSource struct {
	logger                        aggkitcommon.Logger
	services                      *bridgeServiceClients
	clients                       EthClientResolver
	ContractGlobalExitRootAddress common.Address
	L1BlockFinality               aggkittypes.BlockNumberFinality
	// l2GERAddrs is the static networkID -> GlobalExitRootManagerL2 contract address map used by
	// findL2InjectionBlockBackwards, the fallback that scans a destination network's own L2 for
	// the UpdateHashChainValue event when its bridge-service instance does not report the
	// injection block itself (see InjectedGERAtIndex). A network absent from this map (the
	// default, empty map) simply never gets that fallback attempted — L2InjectedGER just stays
	// nil for it, exactly as before this existed
	l2GERAddrs map[uint32]common.Address
	// l2InjectionLookbackBlocks bounds how many blocks findL2InjectionBlockBackwards scans
	// backwards from the destination network's head before giving up, instead of continuing all
	// the way back to genesis (see bridgetracker.Config.L2InjectionLookbackBlocks). A value <= 0
	// falls back to bridgetracker.DefaultL2InjectionLookbackBlocks
	l2InjectionLookbackBlocks uint64
}

// NewGERSource returns a GERSource resolving per-network bridge service clients through finder,
// and the L1 (network 0) JSON-RPC client used by FindFirstL1InfoTreeAfterBlock through clients.
// contractGlobalExitRootAddress is the L1 GlobalExitRoot contract FindFirstL1InfoTreeAfterBlock
// reads UpdateL1InfoTree/UpdateL1InfoTreeV2 logs and state from; l1Finality caps its search range.
// l2GERAddrs is the per-network GlobalExitRootManagerL2 contract address map backing
// findL2InjectionBlockBackwards (see GERSource.l2GERAddrs); nil/empty simply disables that
// fallback for every network. l2InjectionLookbackBlocks bounds how far back that same fallback
// scans (see GERSource.l2InjectionLookbackBlocks); a value <= 0 falls back to
// bridgetracker.DefaultL2InjectionLookbackBlocks
func NewGERSource(
	finder NetworkURLResolver, clients EthClientResolver, contractGlobalExitRootAddress common.Address,
	l1Finality aggkittypes.BlockNumberFinality, l2GERAddrs map[uint32]common.Address,
	l2InjectionLookbackBlocks uint64, logger aggkitcommon.Logger,
) *GERSource {
	return &GERSource{
		logger:                        logger,
		services:                      newBridgeServiceClients(finder),
		clients:                       clients,
		ContractGlobalExitRootAddress: contractGlobalExitRootAddress,
		L1BlockFinality:               l1Finality,
		l2GERAddrs:                    l2GERAddrs,
		l2InjectionLookbackBlocks:     l2InjectionLookbackBlocks,
	}
}

// FindFirstL1InfoTreeAfterBlock returns the L1 info tree leaf index of the earliest
// UpdateL1InfoTree/UpdateL1InfoTreeV2 event strictly after (blockNumber, logIndex) — i.e. in
// blockNumber with a log index greater than logIndex, or in any later block up to
// L1BlockFinality — or nil if none has reached that finality yet
func (s *GERSource) FindFirstL1InfoTreeAfterBlock(
	ctx context.Context, blockNumber uint64, logIndex uint32,
) (*domain.ResultFindFirstL1InfoTreeAfterBlock, error) {
	client, err := s.clients.RPCClientFor(ctx, MainnetNetworkID)
	if err != nil {
		return nil, err // transient: URL resolution failure, retried by the engine
	}

	finalized, err := client.CustomHeaderByNumber(ctx, &s.L1BlockFinality)
	if err != nil {
		return nil, fmt.Errorf("fetching %s header for network %d: %w",
			s.L1BlockFinality.String(), MainnetNetworkID, err)
	}
	if blockNumber > finalized.Number {
		return nil, nil // nothing has reached the required finality yet
	}

	logs, err := client.FilterLogs(ctx, ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(blockNumber),
		ToBlock:   new(big.Int).SetUint64(finalized.Number),
		Addresses: []common.Address{s.ContractGlobalExitRootAddress},
		Topics:    [][]common.Hash{{updateL1InfoTreeSignature, updateL1InfoTreeV2Signature}},
	})
	if err != nil {
		return nil, fmt.Errorf("fetching L1 info tree update logs from block %d to %d: %w",
			blockNumber, finalized.Number, err)
	}

	// FilterLogs includes blockNumber itself, but any log there at or before logIndex sits at
	// or before the reference point (blockNumber, logIndex), not after it, so drop those before
	// picking the earliest remaining block
	filtered := logs[:0]
	for _, l := range logs {
		if l.BlockNumber == blockNumber && l.Index <= uint(logIndex) {
			continue
		}
		filtered = append(filtered, l)
	}
	logs = filtered

	// FilterLogs returns logs in ascending block/log-index order, so logs[0] sits in the
	// earliest block with an update in range. UpdateL1InfoTree and UpdateL1InfoTreeV2 are not
	// mutually exclusive — they normally fire together for the same update — and a single block
	// can carry more than one update, so gather every log of that first block and let
	// resultAtLogs sort out which fields each signature contributes
	if len(logs) == 0 {
		return nil, nil // no L1 info tree update in range yet
	}
	firstBlock := logs[0].BlockNumber
	firstBlockLogs := make([]gethtypes.Log, 0, len(logs))
	for _, l := range logs {
		if l.BlockNumber != firstBlock {
			break
		}
		firstBlockLogs = append(firstBlockLogs, l)
	}

	return s.resultAtLogs(ctx, client, firstBlockLogs)
}

// resultAtLogs builds the result by parsing logs (every UpdateL1InfoTree/UpdateL1InfoTreeV2 log
// of a single block, in emission order — see FindFirstL1InfoTreeAfterBlock), the same way
// l1infotreesync/downloader.go parses them (ger.ParseUpdateL1InfoTree/ParseUpdateL1InfoTreeV2),
// instead of reading the GlobalExitRoot contract's state. UpdateL1InfoTree carries the
// mainnet/rollup exit roots (both indexed), UpdateL1InfoTreeV2 carries the leaf count the update
// landed at; the two are not mutually exclusive — they normally fire together for the same
// update — so the last log of each signature in logs wins, matching whatever the block's last
// actual update produced. The GER itself is not emitted by either event: it is derived the same
// way the contract computes it, hashing the two exit roots together
func (s *GERSource) resultAtLogs(
	ctx context.Context, client aggkittypes.BaseEthereumClienter, logs []gethtypes.Log,
) (*domain.ResultFindFirstL1InfoTreeAfterBlock, error) {
	ger, err := agglayerger.NewAgglayerger(s.ContractGlobalExitRootAddress, client)
	if err != nil {
		return nil, fmt.Errorf("binding GlobalExitRoot contract at %s: %w", s.ContractGlobalExitRootAddress, err)
	}

	result := &domain.ResultFindFirstL1InfoTreeAfterBlock{}
	for _, l := range logs {
		if len(l.Topics) == 0 {
			continue
		}
		switch l.Topics[0] {
		case updateL1InfoTreeSignature:
			update, err := ger.ParseUpdateL1InfoTree(l)
			if err != nil {
				return nil, fmt.Errorf("parsing UpdateL1InfoTree log at block %d: %w", l.BlockNumber, err)
			}
			result.MainnetExitRoot = common.Hash(update.MainnetExitRoot)
			result.RollupExitRoot = common.Hash(update.RollupExitRoot)
		case updateL1InfoTreeV2Signature:
			updateV2, err := ger.ParseUpdateL1InfoTreeV2(l)
			if err != nil {
				return nil, fmt.Errorf("parsing UpdateL1InfoTreeV2 log at block %d: %w", l.BlockNumber, err)
			}
			result.LeafCount = updateV2.LeafCount
		}
	}
	result.GER = crypto.Keccak256Hash(result.MainnetExitRoot[:], result.RollupExitRoot[:])

	last := logs[len(logs)-1]
	result.BlockNumber = last.BlockNumber
	result.LogIndex = last.Index

	timestamp, err := blockTimestamp(ctx, client, last.BlockHash)
	if err != nil {
		return nil, err
	}
	result.BlockTimestamp = timestamp

	return result, nil
}

// OriginGER implements bridgetracker.GERSource. Only called for L1-originated bridges: the
// bridge is covered by a GER update on L1 once some L1 info tree leaf includes it
// (`l1-info-tree-index` resolves for its origin network + deposit count). Returns nil while
// not covered.
//
// Once covered, the leaf itself is fetched with a direct index lookup (`network_id=0`, per
// REFERENCE_API.md) to populate the resulting GER and the block it was updated in
func (s *GERSource) OriginGER(
	ctx context.Context, bridge *bridgetracker.BridgeInfo,
) (*trackertypes.GERData, error) {
	svc, err := s.services.aggkitBridgeClientFor(bridge.DestinationNetwork)
	if err != nil {
		return nil, err // transient: URL resolution failure, retried by the engine
	}

	leafIndex, err := s.coveringLeafIndex(ctx, bridge)
	if errors.Is(err, errNotCoveredYet) {
		return nil, nil // not covered by any GER update yet
	}
	if err != nil {
		return nil, err
	}

	leaf, err := svc.GetInjectedL1InfoLeaf(ctx, 0, int(leafIndex))
	if err != nil {
		return nil, fmt.Errorf("fetching L1 info tree leaf %d: %w", leafIndex, err)
	}

	ger := common.HexToHash(string(leaf.GlobalExitRoot))
	blockNumber := leaf.BlockNumber
	return &trackertypes.GERData{
		NetworkID:   bridge.NetworkID,
		GER:         &ger,
		LERType:     trackertypes.LERTypeMainnet,
		BlockNumber: &blockNumber,
	}, nil
}

// InjectedGER implements bridgetracker.GERSource: a covering GER is injected on the
// destination network once `injected-l1-info-leaf` resolves for the covering leaf index.
// Returns nil while the covering leaf (or a later one) has not been injected
func (s *GERSource) InjectedGER(
	ctx context.Context, bridge *bridgetracker.BridgeInfo,
) (*trackertypes.GERData, error) {
	leafIndex, err := s.coveringLeafIndex(ctx, bridge)
	if errors.Is(err, errNotCoveredYet) {
		return nil, nil // not even covered on the origin yet
	}
	if err != nil {
		return nil, err
	}

	return s.InjectedGERAtIndex(ctx, bridge, leafIndex)
}

// L1InfoTreeIndexForGER implements bridgetracker.GERSource: ger is looked up through the
// destination network's own bridge service — every aggkit bridge service instance embeds the
// same L1 info tree sync regardless of which network it otherwise serves, the same assumption
// OriginGER relies on for its own `network_id=0` lookup. Returns nil while ger has not reached
// that instance's L1 info tree sync yet
func (s *GERSource) L1InfoTreeIndexForGER(
	ctx context.Context, bridge *bridgetracker.BridgeInfo, ger common.Hash,
) (*uint32, error) {
	svc, err := s.services.aggkitBridgeClientFor(bridge.DestinationNetwork)
	if err != nil {
		return nil, err // transient: URL resolution failure, retried by the engine
	}
	leaf, err := svc.GetL1InfoTreeLeafByGER(ctx, ger.Hex())
	if isNotFound(err) {
		// ger comes from an already-finalized L1 event, so this is the bridge service's own L1
		// info tree sync lagging behind, not a real absence — treat as "not resolved yet"
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fetching L1 info tree leaf for GER %s: %w", ger, err)
	}

	index := leaf.L1InfoTreeIndex
	return &index, nil
}

// InjectedGERAtIndex implements bridgetracker.GERSource: reports whether leafIndex (or a later
// leaf) has been injected on bridge's destination network. Shared by InjectedGER, which derives
// leafIndex itself from the origin's deposit count, and by StepWaitL1SettledGER's sibling
// resolver, which already knows leafIndex from the certificate's settlement
func (s *GERSource) InjectedGERAtIndex(
	ctx context.Context, bridge *bridgetracker.BridgeInfo, leafIndex uint32,
) (*trackertypes.GERData, error) {
	svc, err := s.services.aggkitBridgeClientFor(bridge.DestinationNetwork)
	if err != nil {
		return nil, err
	}
	leaf, err := svc.GetInjectedL1InfoLeaf(ctx, int(bridge.DestinationNetwork), int(leafIndex))
	if isNotFound(err) {
		return nil, nil // covering leaf not injected on the destination yet
	}
	if err != nil {
		return nil, fmt.Errorf("fetching injected L1 info leaf %d on network %d: %w",
			leafIndex, bridge.DestinationNetwork, err)
	}

	// bridgeservice types.Hash is a hex string
	ger := common.HexToHash(string(leaf.GlobalExitRoot))
	mer := common.HexToHash(string(leaf.MainnetExitRoot))
	rer := common.HexToHash(string(leaf.RollupExitRoot))

	// leaf.BlockNumber/Timestamp are always the L1 event that produced the leaf, even for this L2
	// lookup (see InjectedL1InfoLeafHandler) — they never describe the L2 injection block, and
	// must not be presented as if they did (that conflation was #1818). The actual L2 injection
	// block/timestamp, when the destination's bridge-service instance reports them, go on the
	// separate L2BlockNumber/L2BlockTimestamp fields instead — left nil otherwise, rather than
	// falling back to the L1 block, so WaitingGERInjectionResolver can honestly report
	// InjectedGERResult.L2InjectedGER as absent instead of a wrong value
	blockNumber := leaf.BlockNumber
	timestamp := leaf.Timestamp
	gerData := &trackertypes.GERData{
		NetworkID:      bridge.DestinationNetwork,
		GER:            &ger,
		MER:            &mer,
		RER:            &rer,
		LERType:        trackertypes.LERTypeNA,
		BlockNumber:    &blockNumber,
		BlockTimestamp: &timestamp,
	}
	if leaf.InjectedL2BlockNumber != nil {
		l2BlockNumber := *leaf.InjectedL2BlockNumber
		gerData.L2BlockNumber = &l2BlockNumber
		if leaf.InjectedL2BlockTimestamp != nil {
			l2Timestamp := *leaf.InjectedL2BlockTimestamp
			gerData.L2BlockTimestamp = &l2Timestamp
		}
		return gerData, nil
	}

	// The destination's bridge-service instance predates injected_l2_block_num (see #1818): fall
	// back to finding the injection ourselves, straight off the destination network's own L2, if
	// its GlobalExitRootManagerL2 address is configured (l2GERAddrs). Best effort — any failure
	// here just leaves L2BlockNumber/L2BlockTimestamp nil, same as when the fallback isn't
	// configured at all; it must never fail InjectedGERAtIndex itself over this
	l2BlockNumber, l2Timestamp, err := s.findL2InjectionBlockBackwards(ctx, bridge.DestinationNetwork, ger)
	if err != nil {
		s.logger.Warnf("finding L2 injection block for GER %s on network %d: %v",
			ger.Hex(), bridge.DestinationNetwork, err)
		return gerData, nil
	}
	gerData.L2BlockNumber = l2BlockNumber
	gerData.L2BlockTimestamp = l2Timestamp
	return gerData, nil
}

// findL2InjectionBlockBackwards scans backward, in fixed-size chunks (mirroring
// SettlementSource.findEventUpdateL1InfoTreeBackwards), for the UpdateHashChainValue event that
// injected ger into the GlobalExitRootManagerL2 contract on networkID — used only as a fallback
// when that network's bridge-service instance does not report the injection block itself (see
// InjectedGERAtIndex). Returns (nil, nil, nil) — not an error — when networkID has no configured
// l2GERAddrs entry (the fallback is simply not available for it) or the scan reaches genesis, or
// s.l2InjectionLookbackBlocks (see GERSource.l2InjectionLookbackBlocks), without finding the
// event; the timestamp alone can be (non-nil, nil) if resolving it off the found block's hash
// fails, since the block number is already a genuine, useful answer on its own
func (s *GERSource) findL2InjectionBlockBackwards(
	ctx context.Context, networkID uint32, ger common.Hash,
) (blockNumber, timestamp *uint64, err error) {
	addr, ok := s.l2GERAddrs[networkID]
	if !ok {
		return nil, nil, nil // fallback not configured for this network, not an error (see doc comment)
	}
	client, err := s.clients.RPCClientFor(ctx, networkID)
	if err != nil {
		return nil, nil, err
	}
	l2GERManager, err := agglayergerl2.NewAgglayergerl2(addr, client)
	if err != nil {
		return nil, nil, fmt.Errorf("binding GlobalExitRootManagerL2 contract at %s: %w", addr, err)
	}
	head, err := client.CustomHeaderByNumber(ctx, &aggkittypes.LatestBlock)
	if err != nil {
		return nil, nil, fmt.Errorf("fetching latest block of network %d: %w", networkID, err)
	}

	lookback := s.l2InjectionLookbackBlocks
	if lookback == 0 {
		lookback = bridgetracker.DefaultL2InjectionLookbackBlocks
	}
	// floor is the lowest block the scan will look at: head.Number - lookback, or genesis (0)
	// if the network's chain is shorter than that
	floor := uint64(0)
	if head.Number > lookback {
		floor = head.Number - lookback
	}

	toBlock := head.Number
	for {
		fromBlock := floor
		if toBlock-floor > bridgeservicefinder.DefaultBlockChunkSize {
			fromBlock = toBlock - bridgeservicefinder.DefaultBlockChunkSize
		}

		end := toBlock
		found, filterErr := s.filterUpdateHashChainValue(ctx, l2GERManager, fromBlock, end, ger)
		if filterErr != nil {
			return nil, nil, fmt.Errorf("filtering UpdateHashChainValue logs [%d,%d] on network %d: %w",
				fromBlock, end, networkID, filterErr)
		}
		if found != nil {
			block := found.BlockNumber
			ts, tsErr := blockTimestamp(ctx, client, found.BlockHash)
			if tsErr != nil {
				return &block, nil, fmt.Errorf("resolving timestamp of block %d: %w", block, tsErr)
			}
			return &block, &ts, nil
		}
		if fromBlock == floor {
			// scanned back to genesis or the configured lookback limit, the injection event
			// genuinely isn't there (within that window)
			return nil, nil, nil
		}
		toBlock = fromBlock - 1
	}
}

// filterUpdateHashChainValue returns the most recent UpdateHashChainValue log for ger within
// [fromBlock, toBlock] on l2GERManager, or nil if none. The event's first indexed topic is the
// GER itself, so the topic filter alone does the matching — no need to decode every log in range
func (s *GERSource) filterUpdateHashChainValue(
	ctx context.Context, l2GERManager *agglayergerl2.Agglayergerl2, fromBlock, toBlock uint64, ger common.Hash,
) (*gethtypes.Log, error) {
	iter, err := l2GERManager.FilterUpdateHashChainValue(
		&bind.FilterOpts{Start: fromBlock, End: &toBlock, Context: ctx}, [][32]byte{ger}, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	var found *gethtypes.Log
	for iter.Next() {
		raw := iter.Event.Raw
		found = &raw
	}
	return found, iter.Error()
}

// coveringLeafIndex resolves the L1 info tree index whose leaf covers the bridge, asking
// the destination network's bridge service (which syncs the L1 info tree). Only ever called
// with bridge.NetworkID == 0 (mainnet), since mainnet's bridgesync is embedded in every
// instance — see OriginGER, its only caller with a non-nil result in practice
func (s *GERSource) coveringLeafIndex(
	ctx context.Context, bridge *bridgetracker.BridgeInfo,
) (uint32, error) {
	svc, err := s.services.aggkitBridgeClientFor(bridge.DestinationNetwork)
	if err != nil {
		return 0, err // transient: URL resolution failure, retried by the engine
	}
	index, err := svc.GetL1InfoTreeIndex(ctx, int(bridge.NetworkID), int(bridge.DepositCount))
	if isNotFound(err) {
		return 0, errNotCoveredYet
	}
	if err != nil {
		return 0, fmt.Errorf("fetching L1 info tree index for network %d deposit %d: %w",
			bridge.NetworkID, bridge.DepositCount, err)
	}
	return index, nil
}

// L1InfoTreeIndexForBridge implements bridgetracker.GERSource: resolves the L1 info tree leaf
// index covering bridge's own origin deposit, per GET /bridge/v1/l1-info-tree-index queried
// against the bridge-service instance that will actually build the claim proof for it — the
// origin network's own instance (see docs/bridge_service.md's L2->L2 flow: this same endpoint,
// queried on the origin, is what bridge_getProof is later called against there too). The
// endpoint only ever accepts network_id == mainnet or network_id == the queried instance's own
// network (see l1-info-tree-index's own network_id validation), and mainnet has no
// bridge-service deployment of its own — bridgeservicefinder.GetURL(0) fails unless a network-0
// URL was explicitly configured (see bridgeservicefinder/doc.go). So when the origin is
// mainnet, the request is instead sent to the destination's own instance (a real rollup,
// resolvable), which still answers for network_id=0 since mainnet's own bridgesync is embedded
// in every instance — exactly what coveringLeafIndex/OriginGER already rely on for that case.
// Returns nil while the queried instance's own L1 info tree sync has not caught up to this
// deposit yet
func (s *GERSource) L1InfoTreeIndexForBridge(
	ctx context.Context, bridge *bridgetracker.BridgeInfo,
) (*uint32, error) {
	queryNetwork := bridge.NetworkID
	if queryNetwork == MainnetNetworkID {
		queryNetwork = bridge.DestinationNetwork
	}
	svc, err := s.services.aggkitBridgeClientFor(queryNetwork)
	if err != nil {
		return nil, err // transient: URL resolution failure, retried by the engine
	}
	index, err := svc.GetL1InfoTreeIndex(ctx, int(bridge.NetworkID), int(bridge.DepositCount))
	if isNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fetching L1 info tree index for network %d deposit %d: %w",
			bridge.NetworkID, bridge.DepositCount, err)
	}
	return &index, nil
}
