package bridgesyncerlite

import (
	"encoding/binary"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	// defaultBlockChunkSize is the number of blocks each parallel eth_getLogs query spans.
	defaultBlockChunkSize = uint64(10000)
	// defaultConcurrency is the number of eth_getLogs queries dispatched in parallel.
	defaultConcurrency = 10
)

// Config configures a BridgeSyncerLite instance.
type Config struct {
	// RPCURL is the JSON-RPC endpoint of the chain to read BridgeEvent logs from.
	RPCURL string
	// BridgeAddr is the address of the bridge contract whose logs are scanned.
	BridgeAddr common.Address
	// DBPath is the sqlite file storing bridge leaves and the exit tree.
	DBPath string
	// BlockChunkSize is the block span of each parallel eth_getLogs query (0 → defaultBlockChunkSize).
	BlockChunkSize uint64
	// Concurrency is the number of eth_getLogs queries run in parallel (0 → defaultConcurrency).
	Concurrency int
	// IgnoreUnsupportedL2Events downgrades the abort-on-forbidden-event behaviour to a warning: events
	// that would invalidate a BridgeEvent-only reconstruction (SetSovereignTokenAddress,
	// MigrateLegacyToken, RemoveLegacySovereignTokenAddress, BackwardLET, ForwardLET) are logged and
	// skipped instead of aborting the sync. The reconstructed tree / local exit root may then be
	// incorrect, so enable this only knowingly (e.g. to inspect a chain that emitted such an event).
	IgnoreUnsupportedL2Events bool
}

// BridgeLeaf is a single BridgeEvent log persisted by the lite syncer. It carries only the data
// available in the event itself — no calldata, no tx sender, no from-address tracing.
type BridgeLeaf struct {
	BlockNum           uint64         `meddler:"block_num"`
	BlockPos           uint64         `meddler:"block_pos"`
	LeafType           uint8          `meddler:"leaf_type"`
	OriginNetwork      uint32         `meddler:"origin_network"`
	OriginAddress      common.Address `meddler:"origin_address,address"`
	DestinationNetwork uint32         `meddler:"destination_network"`
	DestinationAddress common.Address `meddler:"destination_address,address"`
	Amount             *big.Int       `meddler:"amount,bigint"`
	Metadata           []byte         `meddler:"metadata"`
	DepositCount       uint32         `meddler:"deposit_count"`
	TxHash             common.Hash    `meddler:"tx_hash,hash"`
}

// Hash returns the exit-tree leaf hash of the bridge event. It is byte-for-byte identical to
// bridgesync.Bridge.Hash so the tree this syncer builds matches the canonical bridge exit tree.
func (b *BridgeLeaf) Hash() common.Hash {
	const (
		uint32ByteSize = 4
		bigIntSize     = 32
	)
	origNet := make([]byte, uint32ByteSize)
	binary.BigEndian.PutUint32(origNet, b.OriginNetwork)
	destNet := make([]byte, uint32ByteSize)
	binary.BigEndian.PutUint32(destNet, b.DestinationNetwork)

	metaHash := crypto.Keccak256(b.Metadata)
	var buf [bigIntSize]byte
	amount := b.Amount
	if amount == nil {
		amount = new(big.Int)
	}

	return crypto.Keccak256Hash(
		[]byte{b.LeafType},
		origNet,
		b.OriginAddress[:],
		destNet,
		b.DestinationAddress[:],
		amount.FillBytes(buf[:]),
		metaHash,
	)
}

func (b *BridgeLeaf) String() string {
	amountStr := "nil"
	if b.Amount != nil {
		amountStr = b.Amount.String()
	}
	return fmt.Sprintf("BridgeLeaf{BlockNum: %d, BlockPos: %d, LeafType: %d, OriginNetwork: %d, "+
		"OriginAddress: %s, DestinationNetwork: %d, DestinationAddress: %s, Amount: %s, "+
		"DepositCount: %d, TxHash: %s}",
		b.BlockNum, b.BlockPos, b.LeafType, b.OriginNetwork, b.OriginAddress.Hex(),
		b.DestinationNetwork, b.DestinationAddress.Hex(), amountStr, b.DepositCount, b.TxHash.Hex())
}
