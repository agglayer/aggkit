package claimer

import (
	"context"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	bridgetypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	exitcertificate "github.com/agglayer/aggkit/tools/exit_certificate"
	"github.com/agglayer/aggkit/tools/exit_certificate/bridgesyncerlite"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

// Deposit counts of the circleFixture tree.
const (
	circleDCHistoricalMessage uint32 = iota // already claimed; same content as circleDCDuplicateMessage
	circleDCPendingMessage                  // pre-certificate, unclaimed, for holder
	circleDCPendingTransfer                 // pre-certificate, unclaimed, for holder
	circleDCTransfer                        // certificate: ETH for holder
	circleDCMessage                         // certificate: USDC payout for holder
	circleDCDuplicateMessage                // certificate: USDC payout for holder2 (duplicate leaf hash)
	circleDCUnknownRoute                    // certificate: message of a route that is not configured
	circleDCOtherTransfer                   // certificate: ERC-20 for another address
	circleLeafCount
)

// circleFixture mirrors the Circle USDC recovery on zkEVM: a real lite local exit tree with
// pre-certificate leaves and a certificate that pays ETH (Transfer) and USDC (Message exits from
// ZkMinterBurner to L1Escrow, the holder being only in the metadata), plus a claimer over it with the
// Circle message route configured.
type circleFixture struct {
	claimer *Claimer
	l1      *fakeL1
	root    common.Hash
	leaves  []bridgesyncerlite.BridgeLeaf
	// holder has a Transfer and a Message in the certificate, and a pending Message and Transfer.
	holder common.Address
	// holder2 is paid by the certificate Message that duplicates the historical leaf.
	holder2 common.Address
	// unknownDest receives the certificate Message whose route is not configured.
	unknownDest common.Address
}

func usdc(units int64) *big.Int { return big.NewInt(units) }

// erc20Metadata is the metadata bridgeAsset attaches to an ERC-20: abi.encode(name, symbol, decimals).
func erc20Metadata(t *testing.T, name, symbol string, decimals uint8) []byte {
	t.Helper()
	stringType, err := abi.NewType("string", "", nil)
	require.NoError(t, err)
	uint8Type, err := abi.NewType("uint8", "", nil)
	require.NoError(t, err)
	packed, err := abi.Arguments{{Type: stringType}, {Type: stringType}, {Type: uint8Type}}.Pack(name, symbol, decimals)
	require.NoError(t, err)
	return packed
}

func newCircleFixture(t *testing.T) *circleFixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "step-g-l2bridgesyncerlite.sqlite")

	f := &circleFixture{
		holder:      common.HexToAddress("0xA11CE00000000000000000000000000000000001"),
		holder2:     common.HexToAddress("0x3Dc400000000000000000000000000000000ca17"),
		unknownDest: common.HexToAddress("0xC0FFEE0000000000000000000000000000000003"),
	}
	other := common.HexToAddress("0xB0B0000000000000000000000000000000000002")
	unknownSender := common.HexToAddress("0x5E4DE40000000000000000000000000000000004")

	message := func(beneficiary common.Address, amount *big.Int) bridgesyncerlite.BridgeLeaf {
		return bridgesyncerlite.BridgeLeaf{
			LeafType: leafTypeMessage, OriginNetwork: circleNetworkID, OriginAddress: circleZkMinterBurner,
			DestinationNetwork: 0, DestinationAddress: circleL1Escrow, Amount: big.NewInt(0),
			Metadata: encodeAddressUint256(beneficiary, amount),
		}
	}
	transfer := func(token, dest common.Address, amount *big.Int, metadata []byte) bridgesyncerlite.BridgeLeaf {
		return bridgesyncerlite.BridgeLeaf{
			LeafType: leafTypeAsset, OriginNetwork: 0, OriginAddress: token,
			DestinationNetwork: 0, DestinationAddress: dest, Amount: amount, Metadata: metadata,
		}
	}
	oneEth := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)

	f.leaves = make([]bridgesyncerlite.BridgeLeaf, circleLeafCount)
	f.leaves[circleDCHistoricalMessage] = message(f.holder2, usdc(3_000_000_000))
	f.leaves[circleDCPendingMessage] = message(f.holder, usdc(250_500_000))
	f.leaves[circleDCPendingTransfer] = transfer(common.Address{}, f.holder, big.NewInt(100_000_000_000_000_000), nil)
	f.leaves[circleDCTransfer] = transfer(common.Address{}, f.holder, new(big.Int).Mul(oneEth, big.NewInt(2)), nil)
	f.leaves[circleDCMessage] = message(f.holder, usdc(1_234_560_000))
	f.leaves[circleDCDuplicateMessage] = message(f.holder2, usdc(3_000_000_000))
	f.leaves[circleDCUnknownRoute] = bridgesyncerlite.BridgeLeaf{
		LeafType: leafTypeMessage, OriginNetwork: circleNetworkID, OriginAddress: unknownSender,
		DestinationNetwork: 0, DestinationAddress: f.unknownDest, Amount: big.NewInt(0),
		// Shaped like (address, uint256), but its route is not configured: never decoded.
		Metadata: encodeAddressUint256(f.holder, usdc(999)),
	}
	f.leaves[circleDCOtherTransfer] = transfer(circleL1USDC, other, usdc(5_000_000), erc20Metadata(t, "USD Coin", "USDC", 6))
	for i := range f.leaves {
		f.leaves[i].DepositCount = uint32(i)
		f.leaves[i].BlockNum = 1000 + uint64(i)
	}
	require.Equal(t, f.leaves[circleDCHistoricalMessage].Hash(), f.leaves[circleDCDuplicateMessage].Hash(),
		"the fixture needs a certificate exit with the hash of an older leaf")

	syncer, err := bridgesyncerlite.New(ctx, bridgesyncerlite.Config{DBPath: dbPath}, nil)
	require.NoError(t, err)
	require.NoError(t, syncer.StoreBridges(ctx, append([]bridgesyncerlite.BridgeLeaf(nil), f.leaves...)))
	f.root, err = syncer.BuildTree(ctx)
	require.NoError(t, err)
	require.NoError(t, syncer.Close())

	// The certificate carries the leaves from circleDCTransfer on, metadata hashed.
	exits := make([]*agglayertypes.BridgeExit, 0, len(f.leaves))
	for _, l := range f.leaves[circleDCTransfer:] {
		exits = append(exits, &agglayertypes.BridgeExit{
			LeafType:           bridgetypes.LeafType(l.LeafType),
			TokenInfo:          &agglayertypes.TokenInfo{OriginNetwork: l.OriginNetwork, OriginTokenAddress: l.OriginAddress},
			DestinationNetwork: l.DestinationNetwork,
			DestinationAddress: l.DestinationAddress,
			Amount:             l.Amount,
			Metadata:           crypto.Keccak256(l.Metadata),
		})
	}
	raw, err := json.Marshal(&agglayertypes.Certificate{
		NetworkID: circleNetworkID, Height: 1, NewLocalExitRoot: f.root, BridgeExits: exits,
	})
	require.NoError(t, err)
	certPath := filepath.Join(dir, "exit-certificate-signed.json")
	require.NoError(t, os.WriteFile(certPath, raw, 0o600))
	cert, err := LoadCertificate(certPath)
	require.NoError(t, err)

	lt, err := OpenLocalExitTree(ctx, dbPath, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lt.Close()) })

	var rollupProof treetypes.Proof
	for i := range rollupProof {
		rollupProof[i] = crypto.Keccak256Hash([]byte{byte(i)})
	}
	f.l1 = &fakeL1{
		leaf: &l1infotreesync.L1InfoTreeLeaf{
			L1InfoTreeIndex: 123456,
			MainnetExitRoot: crypto.Keccak256Hash([]byte("mainnet exit root")),
			RollupExitRoot:  crypto.Keccak256Hash([]byte("rollup exit root")),
		},
		localRoot:   f.root,
		rollupProof: rollupProof,
	}
	waitResult := &exitcertificate.StepWaitResult{
		UpdateL1InfoTree: &exitcertificate.L1InfoTreeUpdate{
			MainnetExitRoot: f.l1.leaf.MainnetExitRoot, RollupExitRoot: f.l1.leaf.RollupExitRoot,
		},
	}

	pending := make([]exitcertificate.PendingBridge, 0, 2)
	for _, dc := range []uint32{circleDCPendingMessage, circleDCPendingTransfer} {
		l := f.leaves[dc]
		pending = append(pending, exitcertificate.PendingBridge{
			DepositCount: l.DepositCount, BlockNumber: l.BlockNum, LeafType: l.LeafType,
			OriginNetwork: l.OriginNetwork, OriginAddress: l.OriginAddress,
			DestinationNetwork: l.DestinationNetwork, DestinationAddress: l.DestinationAddress,
			Amount: l.Amount, Metadata: l.Metadata,
		})
	}

	f.claimer, err = NewClaimer(log.GetDefaultLogger(), cert, lt, f.l1, 0, waitResult).
		WithPendingBridges(pending).
		WithMessageRoutes([]MessageRoute{circleRoute()})
	require.NoError(t, err)
	return f
}
