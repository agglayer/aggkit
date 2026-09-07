package bridgelooptester

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/go_signer/signer"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Defaults for the receipt wait of NetworkClient.SendTx, overridable with WithReceiptTimeout and
// WithReceiptPollInterval.
const (
	defaultReceiptTimeout      = 3 * time.Minute
	defaultReceiptPollInterval = 2 * time.Second
)

// defaultPreBroadcastTimeout bounds how long a TxRequest.OnSigned hook may take. It runs inside
// SendTx's nonce-serialization critical section, so a hook that never returns would stall every
// other submission from the same account - see notifySigned.
const defaultPreBroadcastTimeout = 30 * time.Second

// baseFeeMultiplier is how many times the latest block's base fee a dynamic-fee transaction is
// willing to pay, on top of the suggested tip. Bridge and claim transactions in a soak run must not
// get stuck behind a base-fee spike, and any unused headroom is refunded by the protocol.
const baseFeeMultiplier = 2

// EthBackend is the JSON-RPC surface the bridge_loop_tester network layer needs from a network's
// node. Both *ethclient.Client and the go-ethereum simulated backend's client satisfy it, so a
// NetworkClient can be built against a real endpoint or against an in-process chain.
type EthBackend interface {
	bind.ContractBackend

	// ChainID returns the EVM chain ID reported by the node.
	ChainID(ctx context.Context) (*big.Int, error)
	// BalanceAt returns the native-currency balance of account; a nil blockNumber means latest.
	BalanceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error)
	// TransactionReceipt returns the receipt of txHash, or ethereum.NotFound while it is unmined.
	TransactionReceipt(ctx context.Context, txHash common.Hash) (*ethtypes.Receipt, error)
	// NonceAt returns the number of transactions account has *mined*; a nil blockNumber means
	// latest. It is deliberately distinct from bind.ContractTransactor's PendingNonceAt (which
	// also counts what is sitting in the node's transaction pool): telling "this nonce has been
	// mined" from "this nonce is only queued" is what makes an interrupted submission decidable
	// - see HopState's HopStateBridging resume contract.
	NonceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (uint64, error)
}

// TxSigner signs transactions for one network. It is deliberately narrower than
// github.com/agglayer/go_signer/signer/types.Signer (which satisfies it) so tests and callers that
// already hold a key do not need the whole signer stack.
type TxSigner interface {
	// PublicAddress returns the address transactions are signed for.
	PublicAddress() common.Address
	// SignTx returns tx signed for the network the signer was built for.
	SignTx(ctx context.Context, tx *ethtypes.Transaction) (*ethtypes.Transaction, error)
}

// PendingTx is the pre-broadcast view of a signed transaction: everything a crash-resumable caller
// must know about a submission *before* the node can possibly have accepted it.
//
// A transaction's hash is fixed by its signature, so it is known before the broadcast. That is what
// closes the otherwise unobservable window between "the node accepted this transaction" and "the
// caller learned its hash": a caller that persists Hash and Nonce from a TxRequest.OnSigned hook
// can always decide, after a restart, whether the submission happened (a receipt exists, or the
// nonce was consumed) or never left the process (the nonce is untouched).
type PendingTx struct {
	// Label is the originating TxRequest's Label, for log lines and error messages.
	Label string
	// Hash is the hash the transaction will have on-chain, already final: it is derived from the
	// signature, not from the broadcast.
	Hash common.Hash
	// Nonce is the account nonce this transaction consumes. Compared against the account's mined
	// nonce, it is what distinguishes "this submission landed or was replaced" from "this
	// submission never reached the node".
	Nonce uint64
	// From is the account the transaction is signed for.
	From common.Address
	// Network is the aggkit network ID the transaction is being submitted to.
	Network uint32
}

// TxRequest describes one transaction NetworkClient.SendTx should submit. The nonce, fee fields and
// (unless GasLimit is set) the gas limit are filled in by the sender, not by the caller.
type TxRequest struct {
	// Label is a short human-readable description of the call ("bridgeAsset", "claimAsset", ...)
	// used verbatim in log lines and error messages. Required in practice, though an empty label
	// only degrades diagnostics.
	Label string
	// To is the call target. A nil To deploys a contract, with Data as the creation code.
	To *common.Address
	// Data is the calldata (or, for a deployment, the creation code plus constructor arguments).
	Data []byte
	// Value is the native-currency value to attach. A nil Value means zero.
	Value *big.Int
	// GasLimit, when non-zero, is used verbatim: neither eth_estimateGas nor the network's
	// GasOffset is applied. Use it to work around nodes whose estimate races the state the
	// transaction will actually execute against (see test/e2e/bridge_utils.go's l1BridgeGasLimit).
	GasLimit uint64
	// OnSigned, when non-nil, is called once with the signed transaction's identity after it has
	// been signed and strictly *before* it is broadcast. It exists so a caller can durably record
	// the hash and nonce of a submission it is about to make, and therefore resume across a crash
	// in the broadcast window (see PendingTx and HopState's HopStateBridging contract).
	//
	// Contract, all of which matters because the hook runs inside SendTx's nonce-serialization
	// critical section:
	//   - Returning a non-nil error aborts the submission: nothing is broadcast, the reserved
	//     nonce stays unconsumed, and SendTx returns that error wrapped. This is the safe
	//     direction - a caller that cannot record the hash should not let the transaction out.
	//   - The hook MUST NOT call back into this NetworkClient (SendTx is not reentrant: it holds
	//     a plain mutex and would deadlock). It should do one bounded thing - persist the hash.
	//   - It is called with a context derived from SendTx's, bounded by the client's
	//     pre-broadcast timeout (WithPreBroadcastTimeout, 30s by default). A hook that honours
	//     that context therefore cannot stall the critical section; one that ignores it and
	//     blocks anyway will hold up every other submission from this account, which is the
	//     deliberate trade-off: broadcasting a transaction whose hash was never recorded is worse
	//     than a visible stall.
	//   - A panic inside the hook is recovered and turned into that same aborting error, so a
	//     buggy hook cannot take the process down or leave sendMu locked forever.
	OnSigned func(ctx context.Context, pending PendingTx) error
}

// NetworkClient is the per-network on-chain layer: JSON-RPC access, chain identity, the signing
// account, and a transaction sender that serializes submissions so several concurrent loops can
// share one EOA on the same network without producing a nonce gap or a nonce reuse.
//
// Concurrency contract: every method is safe for concurrent use, and SendTx additionally
// guarantees that concurrent calls on the *same* NetworkClient value get distinct, strictly
// increasing, gap-free nonces. That guarantee is per NetworkClient instance, so all callers that
// share a signing key on a network MUST share a single NetworkClient: two instances wrapping the
// same EOA each track the pending nonce independently and will collide.
type NetworkClient interface {
	// NetworkID returns the configured aggkit network ID (0 for L1).
	NetworkID() uint32
	// Name returns the configured human-readable network label used in logs and errors.
	Name() string
	// ChainID returns the EVM chain ID, resolved from the node at construction time.
	ChainID() *big.Int
	// From returns the address SendTx signs and sends from.
	From() common.Address
	// Backend returns the JSON-RPC backend, for binding contracts to this network.
	Backend() EthBackend
	// NativeBalance returns account's native-currency balance at the latest block.
	NativeBalance(ctx context.Context, account common.Address) (*big.Int, error)
	// SendTx submits req and waits for its receipt. A transaction that is mined with a failed
	// status is reported as a *RevertError carrying the decoded revert reason, together with the
	// receipt (so a caller that wants the receipt anyway can still read it).
	SendTx(ctx context.Context, req TxRequest) (*ethtypes.Receipt, error)
	// Close releases the underlying RPC connection. It is a no-op for an injected backend.
	Close()
}

// NetworkOption tunes a NetworkClient built by NewNetworkClient or NewNetworkClientWithBackend.
type NetworkOption func(*networkClient)

// WithReceiptTimeout bounds how long SendTx waits for a submitted transaction's receipt. Values
// that are not strictly positive are ignored.
func WithReceiptTimeout(timeout time.Duration) NetworkOption {
	return func(c *networkClient) {
		if timeout > 0 {
			c.receiptTimeout = timeout
		}
	}
}

// WithReceiptPollInterval sets how often SendTx re-polls for a submitted transaction's receipt.
// Values that are not strictly positive are ignored.
func WithReceiptPollInterval(interval time.Duration) NetworkOption {
	return func(c *networkClient) {
		if interval > 0 {
			c.receiptPollInterval = interval
		}
	}
}

// WithPreBroadcastTimeout bounds how long a TxRequest.OnSigned hook may take before its submission
// is abandoned unsent. Values that are not strictly positive are ignored.
func WithPreBroadcastTimeout(timeout time.Duration) NetworkOption {
	return func(c *networkClient) {
		if timeout > 0 {
			c.preBroadcastTimeout = timeout
		}
	}
}

// networkClient is the NetworkClient implementation. Its zero value is not usable; build one with
// NewNetworkClient or NewNetworkClientWithBackend.
type networkClient struct {
	networkID uint32
	name      string
	chainID   *big.Int
	from      common.Address
	gasOffset uint64

	backend EthBackend
	signer  TxSigner
	logger  aggkitcommon.Logger
	closeFn func()

	receiptTimeout      time.Duration
	receiptPollInterval time.Duration
	preBroadcastTimeout time.Duration

	// sendMu serializes the nonce-reservation/estimate/sign/submit critical section of SendTx. It
	// is deliberately NOT held across the receipt wait, so several transactions from this EOA can
	// be in flight at once while still being nonce-ordered.
	sendMu sync.Mutex
	// nextNonce is the nonce to use for the next submission; nonceValid says whether it is
	// trustworthy. Both are guarded by sendMu. A failed submission clears nonceValid so the next
	// submission re-reads the pending nonce from the node instead of leaving a gap behind.
	nextNonce  uint64
	nonceValid bool
}

var _ NetworkClient = (*networkClient)(nil)

// NewNetworkClient dials cfg.RPCURL, resolves the chain ID, builds cfg.Signer's signer (local
// keystore, AWS KMS or GCP KMS, per github.com/agglayer/go_signer) and returns a NetworkClient for
// the network. When cfg.ChainID is non-zero it is checked against the chain ID the node reports and
// a mismatch is an error, so a stale config fails fast instead of producing unsignable
// transactions.
func NewNetworkClient(
	ctx context.Context, cfg Network, logger aggkitcommon.Logger, opts ...NetworkOption,
) (NetworkClient, error) {
	if logger == nil {
		return nil, fmt.Errorf("new network client %q: logger is required", cfg.Name)
	}

	rpcClient, err := ethclient.DialContext(ctx, cfg.RPCURL)
	if err != nil {
		return nil, fmt.Errorf("new network client %q: dial %s: %w", cfg.Name, cfg.RPCURL, err)
	}

	chainID, err := resolveChainID(ctx, cfg, rpcClient)
	if err != nil {
		rpcClient.Close()
		return nil, err
	}

	txSigner, err := signer.NewSigner(ctx, chainID.Uint64(), cfg.Signer, "bridge-loop-tester-"+cfg.Name, logger)
	if err != nil {
		rpcClient.Close()
		return nil, fmt.Errorf("new network client %q: build signer: %w", cfg.Name, err)
	}
	if err := txSigner.Initialize(ctx); err != nil {
		rpcClient.Close()
		return nil, fmt.Errorf("new network client %q: initialize signer: %w", cfg.Name, err)
	}

	client, err := newNetworkClient(cfg, chainID, rpcClient, txSigner, logger, opts...)
	if err != nil {
		rpcClient.Close()
		return nil, err
	}
	client.closeFn = rpcClient.Close

	return client, nil
}

// NewNetworkClientWithBackend builds a NetworkClient over an already-dialed backend and an
// already-initialized signer, without touching cfg.RPCURL or cfg.Signer. It is the entry point for
// tests and for an e2e harness that already holds clients and keys for the target env. Close is a
// no-op on the result: the caller keeps ownership of backend.
func NewNetworkClientWithBackend(
	ctx context.Context,
	cfg Network,
	backend EthBackend,
	txSigner TxSigner,
	logger aggkitcommon.Logger,
	opts ...NetworkOption,
) (NetworkClient, error) {
	if backend == nil {
		return nil, fmt.Errorf("new network client %q: backend is required", cfg.Name)
	}
	if txSigner == nil {
		return nil, fmt.Errorf("new network client %q: signer is required", cfg.Name)
	}
	if logger == nil {
		return nil, fmt.Errorf("new network client %q: logger is required", cfg.Name)
	}

	chainID, err := resolveChainID(ctx, cfg, backend)
	if err != nil {
		return nil, err
	}

	return newNetworkClient(cfg, chainID, backend, txSigner, logger, opts...)
}

// resolveChainID reads the chain ID from the node and, when cfg.ChainID is set, checks it matches.
func resolveChainID(ctx context.Context, cfg Network, backend EthBackend) (*big.Int, error) {
	chainID, err := backend.ChainID(ctx)
	if err != nil {
		return nil, fmt.Errorf("new network client %q: read chain id: %w", cfg.Name, err)
	}
	if chainID == nil || chainID.Sign() <= 0 {
		return nil, fmt.Errorf("new network client %q: node reported an invalid chain id %v", cfg.Name, chainID)
	}
	if cfg.ChainID != 0 && chainID.Uint64() != cfg.ChainID {
		return nil, fmt.Errorf("new network client %q: configured ChainID %d does not match the chain id %s "+
			"reported by %s", cfg.Name, cfg.ChainID, chainID, cfg.RPCURL)
	}

	return chainID, nil
}

// newNetworkClient assembles a networkClient from already-validated pieces.
func newNetworkClient(
	cfg Network,
	chainID *big.Int,
	backend EthBackend,
	txSigner TxSigner,
	logger aggkitcommon.Logger,
	opts ...NetworkOption,
) (*networkClient, error) {
	gasOffset, err := gasOffsetOf(cfg)
	if err != nil {
		return nil, err
	}

	client := &networkClient{
		networkID:           cfg.NetworkID,
		name:                cfg.Name,
		chainID:             new(big.Int).Set(chainID),
		from:                txSigner.PublicAddress(),
		gasOffset:           gasOffset,
		backend:             backend,
		signer:              txSigner,
		logger:              logger,
		closeFn:             func() {},
		receiptTimeout:      defaultReceiptTimeout,
		receiptPollInterval: defaultReceiptPollInterval,
		preBroadcastTimeout: defaultPreBroadcastTimeout,
	}
	for _, opt := range opts {
		opt(client)
	}

	return client, nil
}

// gasOffsetOf converts cfg.GasOffset (a wei-typed config value that is in fact a gas-unit margin)
// into the uint64 the transaction's Gas field needs, refusing values that do not fit.
func gasOffsetOf(cfg Network) (uint64, error) {
	offset := cfg.GasOffset.BigInt()
	if !offset.IsUint64() {
		return 0, fmt.Errorf("new network client %q: GasOffset %s does not fit in a uint64 gas limit",
			cfg.Name, offset)
	}

	return offset.Uint64(), nil
}

// NetworkID returns the configured aggkit network ID (0 for L1).
func (c *networkClient) NetworkID() uint32 { return c.networkID }

// Name returns the configured human-readable network label used in logs and errors.
func (c *networkClient) Name() string { return c.name }

// ChainID returns a copy of the EVM chain ID resolved from the node at construction time.
func (c *networkClient) ChainID() *big.Int { return new(big.Int).Set(c.chainID) }

// From returns the address SendTx signs and sends from.
func (c *networkClient) From() common.Address { return c.from }

// Backend returns the JSON-RPC backend, for binding contracts to this network.
func (c *networkClient) Backend() EthBackend { return c.backend }

// Close releases the underlying RPC connection, and is a no-op for an injected backend.
func (c *networkClient) Close() { c.closeFn() }

// NativeBalance returns account's native-currency balance at the latest block.
func (c *networkClient) NativeBalance(ctx context.Context, account common.Address) (*big.Int, error) {
	balance, err := c.backend.BalanceAt(ctx, account, nil)
	if err != nil {
		return nil, fmt.Errorf("read native balance of %s on %s: %w", account, c.name, err)
	}

	return balance, nil
}

// SendTx submits req from this network's signing account and waits for its receipt.
//
// The nonce-reservation, gas-estimation, signing and submission steps run under a per-client mutex,
// so concurrent callers get distinct, gap-free nonces; the receipt wait runs outside that mutex, so
// several transactions from the same EOA can be in flight at once. A submission that the node
// rejects clears the cached nonce, so the next SendTx re-reads the pending nonce rather than
// leaving a permanent gap behind (the failure mode that silently wedges a long soak run).
//
// A transaction mined with a failed status is returned as a *RevertError with a decoded, human
// readable reason, alongside the receipt.
func (c *networkClient) SendTx(ctx context.Context, req TxRequest) (*ethtypes.Receipt, error) {
	tx, err := c.signAndSubmit(ctx, req)
	if err != nil {
		return nil, err
	}

	c.logger.Debugf("bridge_loop_tester: %s: submitted %s tx=%s nonce=%d gas=%d",
		c.name, labelOf(req), tx.Hash(), tx.Nonce(), tx.Gas())

	receipt, err := c.waitReceipt(ctx, tx)
	if err != nil {
		return nil, err
	}
	if receipt.Status != ethtypes.ReceiptStatusSuccessful {
		return receipt, c.revertErrorForReceipt(ctx, req, tx, receipt)
	}

	return receipt, nil
}

// signAndSubmit reserves a nonce, prices and signs the transaction and hands it to the node. It
// holds sendMu for the whole sequence: that is what makes concurrent SendTx calls nonce-safe.
func (c *networkClient) signAndSubmit(ctx context.Context, req TxRequest) (*ethtypes.Transaction, error) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	nonce, err := c.reserveNonce(ctx)
	if err != nil {
		return nil, err
	}

	value := req.Value
	if value == nil {
		value = new(big.Int)
	}

	gasLimit := req.GasLimit
	if gasLimit == 0 {
		if gasLimit, err = c.estimateGas(ctx, req, value); err != nil {
			return nil, err
		}
	}

	inner, err := c.buildTxData(ctx, req, nonce, gasLimit, value)
	if err != nil {
		return nil, err
	}

	signed, err := c.signer.SignTx(ctx, ethtypes.NewTx(inner))
	if err != nil {
		return nil, fmt.Errorf("%s on %s: sign transaction (nonce %d): %w", labelOf(req), c.name, nonce, err)
	}

	// The hash is final as of the signature, so hand it to the caller before the node can possibly
	// have seen the transaction. A hook that fails aborts the submission with the nonce still
	// unconsumed, which is the safe direction: nothing is on-chain and the next SendTx reuses it.
	if err := c.notifySigned(ctx, req, signed, nonce); err != nil {
		return nil, err
	}

	if err := c.backend.SendTransaction(ctx, signed); err != nil {
		// The reserved nonce was never consumed: force a re-read on the next submission so a
		// rejected transaction cannot leave a permanent hole in this EOA's nonce sequence.
		c.nonceValid = false
		return nil, fmt.Errorf("%s on %s: submit transaction (nonce %d): %w",
			labelOf(req), c.name, nonce, decorateRevert(err))
	}

	c.nextNonce = nonce + 1
	c.nonceValid = true

	return signed, nil
}

// notifySigned hands req.OnSigned the signed transaction's identity, between the signature and the
// broadcast. Callers must hold sendMu, which is exactly why this function is defensive:
//
//   - the hook is called synchronously, so nothing it writes can race with the caller's own use of
//     SendTx's result: it has either finished or failed by the time the broadcast is attempted, and
//     nothing of it is left running afterwards;
//   - its context is bounded by preBroadcastTimeout, so a hook that honours cancellation cannot
//     hold the critical section for longer than that;
//   - a hook that ignores its context and blocks anyway *does* hold sendMu, stalling every other
//     submission from this account. That is the deliberate choice: letting a transaction out
//     without its hash recorded would recreate the unobservable window this hook exists to close.
//     The symptom is loud - the hop never leaves its bridge phase - and the fix belongs in the
//     hook;
//   - a panic in the hook is recovered here and reported as a failure to submit, so a buggy hook
//     cannot unwind through the locked mutex and wedge the client permanently.
func (c *networkClient) notifySigned(
	ctx context.Context, req TxRequest, signed *ethtypes.Transaction, nonce uint64,
) error {
	if req.OnSigned == nil {
		return nil
	}

	pending := PendingTx{
		Label:   labelOf(req),
		Hash:    signed.Hash(),
		Nonce:   nonce,
		From:    c.from,
		Network: c.networkID,
	}

	hookCtx, cancel := context.WithTimeout(ctx, c.preBroadcastTimeout)
	defer cancel()

	err := callOnSigned(hookCtx, req.OnSigned, pending)
	if err == nil {
		return nil
	}

	return fmt.Errorf("%s on %s: the pre-broadcast hook for tx %s (nonce %d) failed, so it was NOT "+
		"submitted: %w", pending.Label, c.name, pending.Hash, nonce, err)
}

// callOnSigned invokes hook, converting a panic into an error so it cannot escape through
// signAndSubmit's held mutex.
func callOnSigned(
	ctx context.Context, hook func(context.Context, PendingTx) error, pending PendingTx,
) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("pre-broadcast hook panicked: %v", recovered)
		}
	}()

	return hook(ctx, pending)
}

// reserveNonce returns the nonce to use for the next submission. Callers must hold sendMu.
func (c *networkClient) reserveNonce(ctx context.Context) (uint64, error) {
	if c.nonceValid {
		return c.nextNonce, nil
	}

	nonce, err := c.backend.PendingNonceAt(ctx, c.from)
	if err != nil {
		return 0, fmt.Errorf("read pending nonce of %s on %s: %w", c.from, c.name, err)
	}

	return nonce, nil
}

// estimateGas runs eth_estimateGas for req and adds the network's GasOffset margin. Callers must
// hold sendMu, so the estimate is made against the same pending state the transaction is queued
// behind.
func (c *networkClient) estimateGas(ctx context.Context, req TxRequest, value *big.Int) (uint64, error) {
	gas, err := c.backend.EstimateGas(ctx, ethereum.CallMsg{
		From:  c.from,
		To:    req.To,
		Value: value,
		Data:  req.Data,
	})
	if err != nil {
		return 0, fmt.Errorf("%s on %s: estimate gas: %w", labelOf(req), c.name, decorateRevert(err))
	}

	return gas + c.gasOffset, nil
}

// buildTxData prices the transaction, preferring an EIP-1559 dynamic-fee transaction and falling
// back to a legacy one on a chain whose latest header carries no base fee. Callers must hold sendMu.
func (c *networkClient) buildTxData(
	ctx context.Context, req TxRequest, nonce, gasLimit uint64, value *big.Int,
) (ethtypes.TxData, error) {
	header, err := c.backend.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("%s on %s: read latest header: %w", labelOf(req), c.name, err)
	}

	if header.BaseFee == nil {
		gasPrice, err := c.backend.SuggestGasPrice(ctx)
		if err != nil {
			return nil, fmt.Errorf("%s on %s: suggest gas price: %w", labelOf(req), c.name, err)
		}

		return &ethtypes.LegacyTx{
			Nonce:    nonce,
			GasPrice: gasPrice,
			Gas:      gasLimit,
			To:       req.To,
			Value:    value,
			Data:     req.Data,
		}, nil
	}

	tip, err := c.backend.SuggestGasTipCap(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s on %s: suggest gas tip cap: %w", labelOf(req), c.name, err)
	}
	feeCap := new(big.Int).Add(new(big.Int).Mul(header.BaseFee, big.NewInt(baseFeeMultiplier)), tip)

	return &ethtypes.DynamicFeeTx{
		ChainID:   c.chainID,
		Nonce:     nonce,
		GasTipCap: tip,
		GasFeeCap: feeCap,
		Gas:       gasLimit,
		To:        req.To,
		Value:     value,
		Data:      req.Data,
	}, nil
}

// waitReceipt polls for tx's receipt until it appears, the receipt timeout elapses, or ctx is done.
func (c *networkClient) waitReceipt(ctx context.Context, tx *ethtypes.Transaction) (*ethtypes.Receipt, error) {
	waitCtx, cancel := context.WithTimeout(ctx, c.receiptTimeout)
	defer cancel()

	ticker := time.NewTicker(c.receiptPollInterval)
	defer ticker.Stop()

	for {
		receipt, err := c.backend.TransactionReceipt(waitCtx, tx.Hash())
		if err == nil {
			return receipt, nil
		}
		if !errors.Is(err, ethereum.NotFound) {
			c.logger.Debugf("bridge_loop_tester: %s: receipt of tx=%s not readable yet, still polling: %v",
				c.name, tx.Hash(), err)
		}

		select {
		case <-waitCtx.Done():
			return nil, fmt.Errorf("gave up waiting for the receipt of tx %s on %s after %s (nonce %d): %w",
				tx.Hash(), c.name, c.receiptTimeout, tx.Nonce(), waitCtx.Err())
		case <-ticker.C:
		}
	}
}

// revertErrorForReceipt turns a failed receipt into a *RevertError, replaying the transaction with
// eth_call at the block it was mined in to recover the revert payload the receipt itself does not
// carry.
func (c *networkClient) revertErrorForReceipt(
	ctx context.Context, req TxRequest, tx *ethtypes.Transaction, receipt *ethtypes.Receipt,
) error {
	revertErr := &RevertError{
		Label:       labelOf(req),
		Network:     c.name,
		TxHash:      tx.Hash(),
		BlockNumber: receipt.BlockNumber,
		GasUsed:     receipt.GasUsed,
		Reason:      noRevertDataReason,
	}

	_, callErr := c.backend.CallContract(ctx, ethereum.CallMsg{
		From:  c.from,
		To:    tx.To(),
		Gas:   tx.Gas(),
		Value: tx.Value(),
		Data:  tx.Data(),
	}, receipt.BlockNumber)
	if callErr == nil {
		revertErr.Reason = "reverted on-chain but the eth_call replay at the mined block succeeded " +
			"(likely an out-of-gas or a state race)"
		return revertErr
	}

	reason, data := DecodeRevertError(callErr)
	revertErr.Reason = reason
	revertErr.Data = data
	revertErr.Err = callErr

	return revertErr
}

// labelOf returns req's label, or a placeholder when it has none, so error messages stay readable.
func labelOf(req TxRequest) string {
	if req.Label == "" {
		return "transaction"
	}

	return req.Label
}
