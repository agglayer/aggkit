package bridgelooptester

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

// Bridge contract method names, packed through the agglayerbridgel2 ABI rather than through the
// binding's own transactors: the binding would build and submit its own transaction, bypassing
// NetworkClient's serialized sender (and therefore its nonce guarantee).
const (
	methodBridgeAsset  = "bridgeAsset"
	methodClaimAsset   = "claimAsset"
	methodClaimMessage = "claimMessage"
)

// ErrNativeAssetUnsupported is returned by Bridge.BridgeAssetNative on a network whose gas token is
// not ether. Bridging ETH there requires the network's own WETH representation (bridgeAsset on
// WETHToken(), or bridgeMessageWETH) which this tool deliberately does not implement - see
// DESIGN.md §6. Callers are expected to refuse such a configuration up front rather than fail
// mid-run.
var ErrNativeAssetUnsupported = errors.New(
	"bridging the native asset is only supported on networks whose gas token is ether")

// ErrBridgeEventNotFound is returned by Bridge.BridgeEventFromReceipt when a receipt carries no
// decodable BridgeEvent log, which means the transaction was not a bridge deposit at all.
var ErrBridgeEventNotFound = errors.New("no BridgeEvent log found in the transaction receipt")

// BridgeEvent is the decoded BridgeEvent log a bridge deposit emits. DepositCount is the value the
// whole readiness/claim sequence keys off, and it comes from this log rather than from any REST
// list endpoint (DESIGN.md §5).
type BridgeEvent struct {
	// LeafType is 0 for an asset deposit and 1 for a message deposit.
	LeafType uint8
	// OriginNetwork is the network the *token* originates from, not the hop's source network.
	OriginNetwork uint32
	// OriginAddress is the token address on OriginNetwork (the zero address for the native asset).
	OriginAddress common.Address
	// DestinationNetwork is the network the deposit is claimable on.
	DestinationNetwork uint32
	// DestinationAddress is the address the deposit is claimable by.
	DestinationAddress common.Address
	// Amount is the deposited amount.
	Amount *big.Int
	// Metadata is the deposit metadata, needed verbatim to build the claim.
	Metadata []byte
	// DepositCount is the deposit's index in the source network's local exit tree.
	DepositCount uint32
	// TxHash is the hash of the transaction that emitted the event.
	TxHash common.Hash
	// BlockNumber is the block the event was emitted in.
	BlockNumber uint64
}

// BridgeAssetRequest describes one bridgeAsset call. The token is not part of it: the native and
// ERC20 variants take it (or omit it) separately, because they differ in more than one argument.
type BridgeAssetRequest struct {
	// DestinationNetwork is the network the deposit should be claimable on.
	DestinationNetwork uint32
	// DestinationAddress is the address the deposit should be claimable by.
	DestinationAddress common.Address
	// Amount is how much to bridge.
	Amount *big.Int
	// ForceUpdateGlobalExitRoot asks the bridge to update the global exit root in the same
	// transaction. The loop tester should normally set it, so a deposit does not wait for an
	// unrelated bridge to move the tree forward.
	ForceUpdateGlobalExitRoot bool
	// PermitData is optional ERC20 permit calldata; nil for the common Approve-based flow.
	PermitData []byte
	// GasLimit, when non-zero, overrides gas estimation for this deposit (see TxRequest.GasLimit).
	GasLimit uint64
}

// BridgeResult is the outcome of a successful bridgeAsset submission.
type BridgeResult struct {
	// TxHash is the bridge transaction's hash. Together with the source network it is all the hop
	// state machine needs to re-derive its state after a restart (DESIGN.md §4).
	TxHash common.Hash
	// Receipt is the bridge transaction's receipt.
	Receipt *ethtypes.Receipt
	// Event is the BridgeEvent decoded from Receipt.
	Event BridgeEvent
}

// ClaimRequest carries every argument claimAsset/claimMessage need. The proofs, exit roots and
// leaf come from GET /bridge/v1/claim-proof; the origin/destination/amount/metadata fields come
// from the BridgeEvent of the deposit being claimed; GlobalIndex comes from GlobalIndex().
type ClaimRequest struct {
	// ProofLocalExitRoot is the 32-level Merkle proof of the deposit in its local exit tree.
	ProofLocalExitRoot [32][32]byte
	// ProofRollupExitRoot is the 32-level Merkle proof of the local exit root in the rollup tree.
	ProofRollupExitRoot [32][32]byte
	// GlobalIndex encodes (mainnet flag, rollup index, deposit count) - build it with GlobalIndex.
	GlobalIndex *big.Int
	// MainnetExitRoot is the L1 info tree leaf's mainnet exit root.
	MainnetExitRoot common.Hash
	// RollupExitRoot is the L1 info tree leaf's rollup exit root.
	RollupExitRoot common.Hash
	// OriginNetwork is the token's origin network, from the deposit's BridgeEvent.
	OriginNetwork uint32
	// OriginAddress is the token (claimAsset) or sender (claimMessage) address on OriginNetwork.
	OriginAddress common.Address
	// DestinationNetwork is the deposit's destination network.
	DestinationNetwork uint32
	// DestinationAddress is the deposit's destination address.
	DestinationAddress common.Address
	// Amount is the deposited amount.
	Amount *big.Int
	// Metadata is the deposit metadata, verbatim from the BridgeEvent.
	Metadata []byte
	// GasLimit, when non-zero, overrides gas estimation for this claim (see TxRequest.GasLimit).
	GasLimit uint64
}

// Bridge is the subset of PolygonZkEVMBridgeV2 behaviour (via the agglayerbridgel2 binding) the
// loop tester needs on one network. Every method is safe for concurrent use; the write methods go
// through NetworkClient.SendTx and inherit its nonce serialization.
type Bridge interface {
	// Address returns the bridge contract's address on this network.
	Address() common.Address
	// NetworkID returns the aggkit network ID the bridge contract reports for itself.
	NetworkID(ctx context.Context) (uint32, error)
	// GasTokenAddress returns this network's gas token; the zero address means the gas token is
	// ether. The result is read from the contract once and then cached, since it is immutable.
	GasTokenAddress(ctx context.Context) (common.Address, error)
	// WETHToken returns this network's WETH representation, used only to explain why a native
	// hop is refused on a non-ether gas token network.
	WETHToken(ctx context.Context) (common.Address, error)
	// GetTokenWrappedAddress returns the address the wrapped representation of
	// (originNetwork, originToken) has, or would have, on this network.
	GetTokenWrappedAddress(
		ctx context.Context, originNetwork uint32, originToken common.Address,
	) (common.Address, error)
	// IsClaimed reports whether the deposit identified by (depositCount, sourceNetwork) has
	// already been claimed on this network. sourceNetwork is the hop's origin network - the
	// network the BridgeEvent fired on - not the token's origin network (DESIGN.md §8). This is
	// the query that makes the hop engine resumable and idempotent.
	IsClaimed(ctx context.Context, depositCount, sourceNetwork uint32) (bool, error)
	// BridgeAssetNative bridges this network's native currency, and returns
	// ErrNativeAssetUnsupported without submitting anything when the gas token is not ether.
	BridgeAssetNative(ctx context.Context, req BridgeAssetRequest) (*BridgeResult, error)
	// BridgeAssetERC20 bridges an ERC20 token, which must already be approved for the bridge.
	BridgeAssetERC20(ctx context.Context, token common.Address, req BridgeAssetRequest) (*BridgeResult, error)
	// ClaimAsset submits claimAsset for an asset deposit (BridgeEvent.LeafType == 0).
	ClaimAsset(ctx context.Context, req ClaimRequest) (*ethtypes.Receipt, error)
	// ClaimMessage submits claimMessage for a message deposit (BridgeEvent.LeafType == 1).
	ClaimMessage(ctx context.Context, req ClaimRequest) (*ethtypes.Receipt, error)
	// BridgeEventFromReceipt decodes the BridgeEvent log out of a bridge transaction's receipt.
	// It returns ErrBridgeEventNotFound when there is none.
	BridgeEventFromReceipt(receipt *ethtypes.Receipt) (*BridgeEvent, error)
}

// GlobalIndex returns the bridge global index of a deposit, delegating the mainnet-flag/rollup-index
// encoding to bridgesync.GenerateGlobalIndexForNetworkID - the same helper autoclaim uses, and the
// single source of truth for that encoding (DESIGN.md §7). sourceNetwork is the hop's origin
// network, not the token's origin network.
func GlobalIndex(sourceNetwork, depositCount uint32) *big.Int {
	return bridgesync.GenerateGlobalIndexForNetworkID(sourceNetwork, depositCount)
}

// bridgeContract is the Bridge implementation.
type bridgeContract struct {
	client   NetworkClient
	address  common.Address
	abi      *abi.ABI
	contract *agglayerbridgel2.Agglayerbridgel2

	// gasTokenMu guards the one-shot cache of the immutable gasTokenAddress() result.
	gasTokenMu     sync.Mutex
	gasToken       common.Address
	gasTokenCached bool
}

var _ Bridge = (*bridgeContract)(nil)

// NewBridge binds the bridge contract at address on client's network.
func NewBridge(client NetworkClient, address common.Address) (Bridge, error) {
	if client == nil {
		return nil, fmt.Errorf("new bridge: network client is required")
	}
	if address == (common.Address{}) {
		return nil, fmt.Errorf("new bridge on %s: bridge address must not be the zero address", client.Name())
	}

	parsed, err := agglayerbridgel2.Agglayerbridgel2MetaData.GetAbi()
	if err != nil {
		return nil, fmt.Errorf("new bridge on %s: parse AgglayerBridgeL2 ABI: %w", client.Name(), err)
	}

	contract, err := agglayerbridgel2.NewAgglayerbridgel2(address, client.Backend())
	if err != nil {
		return nil, fmt.Errorf("new bridge on %s: bind AgglayerBridgeL2 at %s: %w", client.Name(), address, err)
	}

	return &bridgeContract{client: client, address: address, abi: parsed, contract: contract}, nil
}

// Address returns the bridge contract's address on this network.
func (b *bridgeContract) Address() common.Address { return b.address }

// NetworkID returns the aggkit network ID the bridge contract reports for itself.
func (b *bridgeContract) NetworkID(ctx context.Context) (uint32, error) {
	networkID, err := b.contract.NetworkID(&bind.CallOpts{Context: ctx})
	if err != nil {
		return 0, fmt.Errorf("read networkID() from the bridge on %s: %w", b.client.Name(), err)
	}

	return networkID, nil
}

// GasTokenAddress returns this network's gas token, reading it from the contract once and caching
// the result. The zero address means the gas token is ether.
func (b *bridgeContract) GasTokenAddress(ctx context.Context) (common.Address, error) {
	b.gasTokenMu.Lock()
	defer b.gasTokenMu.Unlock()

	if b.gasTokenCached {
		return b.gasToken, nil
	}

	gasToken, err := b.contract.GasTokenAddress(&bind.CallOpts{Context: ctx})
	if err != nil {
		return common.Address{}, fmt.Errorf("read gasTokenAddress() from the bridge on %s: %w",
			b.client.Name(), err)
	}
	b.gasToken, b.gasTokenCached = gasToken, true

	return gasToken, nil
}

// WETHToken returns this network's WETH representation.
func (b *bridgeContract) WETHToken(ctx context.Context) (common.Address, error) {
	weth, err := b.contract.WETHToken(&bind.CallOpts{Context: ctx})
	if err != nil {
		return common.Address{}, fmt.Errorf("read WETHToken() from the bridge on %s: %w", b.client.Name(), err)
	}

	return weth, nil
}

// GetTokenWrappedAddress returns the wrapped representation of (originNetwork, originToken) on this
// network, via the bridge's computeTokenProxyAddress().
func (b *bridgeContract) GetTokenWrappedAddress(
	ctx context.Context, originNetwork uint32, originToken common.Address,
) (common.Address, error) {
	wrapped, err := b.contract.ComputeTokenProxyAddress(&bind.CallOpts{Context: ctx}, originNetwork, originToken)
	if err != nil {
		return common.Address{}, fmt.Errorf(
			"compute the wrapped address of token %s (origin network %d) on %s: %w",
			originToken, originNetwork, b.client.Name(), err)
	}

	return wrapped, nil
}

// IsClaimed reports whether (depositCount, sourceNetwork) has already been claimed here.
func (b *bridgeContract) IsClaimed(ctx context.Context, depositCount, sourceNetwork uint32) (bool, error) {
	claimed, err := b.contract.IsClaimed(&bind.CallOpts{Context: ctx}, depositCount, sourceNetwork)
	if err != nil {
		return false, fmt.Errorf("read isClaimed(%d, %d) from the bridge on %s: %w",
			depositCount, sourceNetwork, b.client.Name(), err)
	}

	return claimed, nil
}

// BridgeAssetNative bridges the native currency with token=0x0 and msg.value=Amount, the only
// native path DESIGN.md §6 requires. On a network whose gas token is not ether it returns
// ErrNativeAssetUnsupported without submitting anything.
func (b *bridgeContract) BridgeAssetNative(ctx context.Context, req BridgeAssetRequest) (*BridgeResult, error) {
	gasToken, err := b.GasTokenAddress(ctx)
	if err != nil {
		return nil, err
	}
	if gasToken != (common.Address{}) {
		weth := "unknown"
		if wethAddr, wethErr := b.WETHToken(ctx); wethErr == nil {
			weth = wethAddr.String()
		}

		return nil, fmt.Errorf("%w: %s reports gasTokenAddress()=%s (WETHToken()=%s)",
			ErrNativeAssetUnsupported, b.client.Name(), gasToken, weth)
	}

	return b.bridgeAsset(ctx, "bridgeAsset(native)", common.Address{}, req, amountOf(req.Amount))
}

// BridgeAssetERC20 bridges an ERC20 token. The caller is responsible for having approved the bridge
// for at least req.Amount (see Token.Approve).
func (b *bridgeContract) BridgeAssetERC20(
	ctx context.Context, token common.Address, req BridgeAssetRequest,
) (*BridgeResult, error) {
	if token == (common.Address{}) {
		return nil, fmt.Errorf("bridge ERC20 asset on %s: token must not be the zero address "+
			"(use BridgeAssetNative for the native currency)", b.client.Name())
	}

	return b.bridgeAsset(ctx, "bridgeAsset(erc20)", token, req, new(big.Int))
}

// bridgeAsset packs and submits bridgeAsset, then decodes the resulting BridgeEvent.
func (b *bridgeContract) bridgeAsset(
	ctx context.Context, label string, token common.Address, req BridgeAssetRequest, value *big.Int,
) (*BridgeResult, error) {
	data, err := b.abi.Pack(
		methodBridgeAsset,
		req.DestinationNetwork,
		req.DestinationAddress,
		amountOf(req.Amount),
		token,
		req.ForceUpdateGlobalExitRoot,
		req.PermitData,
	)
	if err != nil {
		return nil, fmt.Errorf("pack %s calldata for %s: %w", methodBridgeAsset, b.client.Name(), err)
	}

	receipt, err := b.client.SendTx(ctx, TxRequest{
		Label:    label,
		To:       &b.address,
		Data:     data,
		Value:    value,
		GasLimit: req.GasLimit,
	})
	if err != nil {
		return nil, err
	}

	event, err := b.BridgeEventFromReceipt(receipt)
	if err != nil {
		return nil, fmt.Errorf("%s on %s: tx %s: %w", label, b.client.Name(), receipt.TxHash, err)
	}

	return &BridgeResult{TxHash: receipt.TxHash, Receipt: receipt, Event: *event}, nil
}

// ClaimAsset submits claimAsset for an asset deposit.
func (b *bridgeContract) ClaimAsset(ctx context.Context, req ClaimRequest) (*ethtypes.Receipt, error) {
	return b.claim(ctx, methodClaimAsset, req)
}

// ClaimMessage submits claimMessage for a message deposit.
func (b *bridgeContract) ClaimMessage(ctx context.Context, req ClaimRequest) (*ethtypes.Receipt, error) {
	return b.claim(ctx, methodClaimMessage, req)
}

// claim packs and submits claimAsset/claimMessage. The argument list is identical for both, and
// identical to the one autoclaim/claimtx.PackClaim builds - packed here directly off the same
// agglayerbridgel2 ABI, because PackClaim's autoclaimtypes.AutoClaimRequest/ClaimProof inputs carry
// autoclaim's own storage-oriented fields this tool has no source for.
func (b *bridgeContract) claim(ctx context.Context, method string, req ClaimRequest) (*ethtypes.Receipt, error) {
	if req.GlobalIndex == nil {
		return nil, fmt.Errorf("%s on %s: GlobalIndex is required (build it with GlobalIndex())",
			method, b.client.Name())
	}

	data, err := b.abi.Pack(
		method,
		req.ProofLocalExitRoot,
		req.ProofRollupExitRoot,
		req.GlobalIndex,
		[32]byte(req.MainnetExitRoot),
		[32]byte(req.RollupExitRoot),
		req.OriginNetwork,
		req.OriginAddress,
		req.DestinationNetwork,
		req.DestinationAddress,
		amountOf(req.Amount),
		req.Metadata,
	)
	if err != nil {
		return nil, fmt.Errorf("pack %s calldata for %s: %w", method, b.client.Name(), err)
	}

	return b.client.SendTx(ctx, TxRequest{
		Label:    method,
		To:       &b.address,
		Data:     data,
		GasLimit: req.GasLimit,
	})
}

// BridgeEventFromReceipt decodes the BridgeEvent log out of a bridge transaction's receipt. It
// prefers logs emitted by this bridge contract and falls back to scanning every log (an ERC20
// deposit also emits a Transfer log, so the BridgeEvent is not necessarily first - DESIGN.md §5).
func (b *bridgeContract) BridgeEventFromReceipt(receipt *ethtypes.Receipt) (*BridgeEvent, error) {
	if receipt == nil {
		return nil, fmt.Errorf("decode BridgeEvent on %s: %w (nil receipt)", b.client.Name(), ErrBridgeEventNotFound)
	}

	if event, ok := b.scanLogs(receipt, true); ok {
		return event, nil
	}
	if event, ok := b.scanLogs(receipt, false); ok {
		return event, nil
	}

	return nil, fmt.Errorf("decode BridgeEvent from tx %s on %s (%d logs scanned): %w",
		receipt.TxHash, b.client.Name(), len(receipt.Logs), ErrBridgeEventNotFound)
}

// scanLogs looks for a decodable BridgeEvent among receipt's logs, optionally restricted to logs
// emitted by this bridge contract.
func (b *bridgeContract) scanLogs(receipt *ethtypes.Receipt, onlyOwnAddress bool) (*BridgeEvent, bool) {
	for _, entry := range receipt.Logs {
		if entry == nil {
			continue
		}
		if onlyOwnAddress && entry.Address != b.address {
			continue
		}
		parsed, err := b.contract.ParseBridgeEvent(*entry)
		if err != nil {
			continue
		}

		return &BridgeEvent{
			LeafType:           parsed.LeafType,
			OriginNetwork:      parsed.OriginNetwork,
			OriginAddress:      parsed.OriginAddress,
			DestinationNetwork: parsed.DestinationNetwork,
			DestinationAddress: parsed.DestinationAddress,
			Amount:             parsed.Amount,
			Metadata:           parsed.Metadata,
			DepositCount:       parsed.DepositCount,
			TxHash:             entry.TxHash,
			BlockNumber:        entry.BlockNumber,
		}, true
	}

	return nil, false
}

// amountOf returns amount, or zero when it is nil, so a nil amount never panics inside abi.Pack.
func amountOf(amount *big.Int) *big.Int {
	if amount == nil {
		return new(big.Int)
	}

	return new(big.Int).Set(amount)
}
