package remove_ger

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/tree"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/urfave/cli/v2"
)

// DefaultDepositCount is the default deposit count for generated invalid GER scenarios.
// Uses a high value (42069) to avoid collisions with real deposits.
const DefaultDepositCount = uint32(42069)

// GenerateParams holds the input parameters for generating an invalid GER scenario.
type GenerateParams struct {
	DestinationNetwork uint32
	DestinationAddress common.Address
	OriginNetwork      uint32
	OriginAddress      common.Address
	Amount             *big.Int
	DepositCount       uint32
	LeafType           uint8
}

// GeneratedInvalidGER holds all computed values for the generated invalid GER scenario.
type GeneratedInvalidGER struct {
	GER             common.Hash
	MainnetExitRoot common.Hash
	RollupExitRoot  common.Hash
	ProofLocal      [32][32]byte
	ProofRollup     [32][32]byte
	GlobalIndex     *big.Int
	Params          GenerateParams
}

// GenerateInvalidGER computes a deterministic invalid GER from the given parameters.
// It builds a bridge leaf, places it in a single-leaf merkle tree (zero-hash siblings),
// and derives the GER from the resulting mainnet exit root.
func GenerateInvalidGER(params GenerateParams) *GeneratedInvalidGER {
	b := &bridgesync.Bridge{
		LeafType:           params.LeafType,
		OriginNetwork:      params.OriginNetwork,
		OriginAddress:      params.OriginAddress,
		DestinationNetwork: params.DestinationNetwork,
		DestinationAddress: params.DestinationAddress,
		Amount:             params.Amount,
		Metadata:           []byte{},
	}
	leafHash := b.Hash()

	zeroHashes := generateZeroHashes(treetypes.DefaultHeight)
	var proof treetypes.Proof
	for h := range treetypes.DefaultHeight {
		proof[h] = zeroHashes[h]
	}

	mainnetExitRoot := tree.CalculateRoot(leafHash, proof, params.DepositCount)
	rollupExitRoot := common.Hash{}
	ger := l1infotreesync.CalculateGER(mainnetExitRoot, rollupExitRoot)

	var proofLocal, proofRollup [32][32]byte
	for i := range 32 {
		proofLocal[i] = proof[i]
		proofRollup[i] = zeroHashes[i]
	}

	globalIndex := bridgesync.GenerateGlobalIndexForNetworkID(params.OriginNetwork, params.DepositCount)

	return &GeneratedInvalidGER{
		GER:             ger,
		MainnetExitRoot: mainnetExitRoot,
		RollupExitRoot:  rollupExitRoot,
		ProofLocal:      proofLocal,
		ProofRollup:     proofRollup,
		GlobalIndex:     globalIndex,
		Params:          params,
	}
}

// generateZeroHashes builds zero hashes for a merkle tree of the given height.
// Index 0 = empty leaf (zero hash), index i = keccak256(zeroHashes[i-1] || zeroHashes[i-1]).
func generateZeroHashes(height uint8) []common.Hash {
	zeroHashes := []common.Hash{{}}
	for i := 1; i <= int(height); i++ {
		next := crypto.Keccak256Hash(zeroHashes[i-1][:], zeroHashes[i-1][:])
		zeroHashes = append(zeroHashes, next)
	}
	return zeroHashes
}

// formatBytes32ArrayForCast formats a [32][32]byte array as a cast-compatible bytes32[32] literal.
// Output: [0x0000...,0x62c6...,...]
func formatBytes32ArrayForCast(arr [32][32]byte) string {
	parts := make([]string, treetypes.DefaultHeight)
	for i := range treetypes.DefaultHeight {
		parts[i] = common.Hash(arr[i]).Hex()
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// RunGenerate is the CLI action for the "generate" subcommand.
func RunGenerate(c *cli.Context) error {
	networkID := c.Uint("network-id")
	if networkID == 0 {
		return fmt.Errorf("--network-id is required and must be > 0")
	}

	params := GenerateParams{
		DestinationNetwork: uint32(networkID),
		DestinationAddress: common.HexToAddress(c.String("dest-addr")),
		OriginNetwork:      uint32(c.Uint("origin-network")),
		OriginAddress:      common.HexToAddress(c.String("origin-addr")),
		Amount:             new(big.Int).SetUint64(c.Uint64("amount")),
		DepositCount:       uint32(c.Uint("deposit-count")),
		LeafType:           uint8(c.Uint("leaf-type")),
	}

	cfg, err := LoadConfig(c)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	l2RPCURL := cfg.Common.L2RPC.URL
	bridgeAddr := cfg.BridgeL2Sync.BridgeAddr.Hex()
	gerAddr := cfg.L2GERSync.GlobalExitRootL2Addr.Hex()

	if l2RPCURL == "" {
		return fmt.Errorf("config missing Common.L2RPC.URL (L2 RPC URL)")
	}
	if bridgeAddr == (common.Address{}).Hex() {
		return fmt.Errorf("config missing BridgeL2Sync.BridgeAddr")
	}
	if gerAddr == (common.Address{}).Hex() {
		return fmt.Errorf("config missing L2GERSync.GlobalExitRootL2Addr")
	}

	result := GenerateInvalidGER(params)

	printGenerateOutput(result, l2RPCURL, bridgeAddr, gerAddr)
	return nil
}

func printGenerateOutput(r *GeneratedInvalidGER, l2RPCURL, bridgeAddr, gerAddr string) {
	fmt.Println("# Generated Invalid GER Data")
	fmt.Printf("# GER: %s\n", r.GER.Hex())
	fmt.Printf("# Deposit Count: %d\n", r.Params.DepositCount)
	fmt.Printf("# Global Index: %s\n", r.GlobalIndex.String())
	fmt.Printf("# Mainnet Exit Root: %s\n", r.MainnetExitRoot.Hex())
	fmt.Printf("# Rollup Exit Root: %s\n", r.RollupExitRoot.Hex())
	fmt.Println()

	fmt.Println("# Step 1: Inject fake GER (requires aggoracle private key)")
	fmt.Println("# Stop aggkit before running this to avoid nonce conflicts")
	fmt.Printf("cast send --legacy --private-key $AGGORACLE_PRIVATE_KEY "+
		"--rpc-url %s %s "+
		"\"insertGlobalExitRoot(bytes32)\" %s\n",
		l2RPCURL, gerAddr, r.GER.Hex())
	fmt.Println()

	fmt.Println("# Step 2: Claim with fake proof (requires any funded L2 private key)")
	fmt.Printf("cast send --legacy --private-key $CLAIM_PRIVATE_KEY "+
		"--rpc-url %s %s "+
		"\"claimAsset(bytes32[32],bytes32[32],uint256,bytes32,bytes32,uint32,address,uint32,address,uint256,bytes)\" "+
		"%s %s %s %s %s %d %s %d %s %s 0x\n",
		l2RPCURL, bridgeAddr,
		formatBytes32ArrayForCast(r.ProofLocal),
		formatBytes32ArrayForCast(r.ProofRollup),
		r.GlobalIndex.String(),
		r.MainnetExitRoot.Hex(),
		r.RollupExitRoot.Hex(),
		r.Params.OriginNetwork,
		r.Params.OriginAddress.Hex(),
		r.Params.DestinationNetwork,
		r.Params.DestinationAddress.Hex(),
		r.Params.Amount.String(),
	)
}
