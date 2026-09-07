package bridgelooptester_test

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/agglayer/aggkit/log"
	bridgelooptester "github.com/agglayer/aggkit/tools/bridge_loop_tester"
	"github.com/agglayer/aggkit/tools/bridge_loop_tester/mocks"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const testChainID = 20201

// keySigner is a TxSigner backed by an ephemeral key, so tests exercise the real signing path (and
// therefore the real nonce encoded into the signed transaction) without a keystore.
type keySigner struct {
	key    *ecdsa.PrivateKey
	addr   common.Address
	signer ethtypes.Signer
}

func newKeySigner(t *testing.T, chainID *big.Int) *keySigner {
	t.Helper()

	key, err := crypto.GenerateKey()
	require.NoError(t, err)

	return &keySigner{
		key:    key,
		addr:   crypto.PubkeyToAddress(key.PublicKey),
		signer: ethtypes.LatestSignerForChainID(chainID),
	}
}

func (s *keySigner) PublicAddress() common.Address { return s.addr }

func (s *keySigner) SignTx(_ context.Context, tx *ethtypes.Transaction) (*ethtypes.Transaction, error) {
	return ethtypes.SignTx(tx, s.signer, s.key)
}

// dataError is an rpc.DataError carrying a hex revert payload, as a node returns for a rejected
// eth_estimateGas or eth_call.
type dataError struct {
	message string
	data    any
}

func (e *dataError) Error() string  { return e.message }
func (e *dataError) ErrorCode() int { return 3 }
func (e *dataError) ErrorData() any { return e.data }

// errorStringSelector is the selector of Solidity's built-in Error(string) revert.
const errorStringSelector = "0x08c379a0"

// encodeErrorString ABI-encodes a Solidity `revert("...")`, i.e. an Error(string) payload.
func encodeErrorString(t *testing.T, reason string) []byte {
	t.Helper()

	stringType, err := abi.NewType("string", "", nil)
	require.NoError(t, err)

	encoded, err := abi.Arguments{{Type: stringType}}.Pack(reason)
	require.NoError(t, err)

	return append(hexutil.MustDecode(errorStringSelector), encoded...)
}

// testNetworkConfig returns a minimal Network for the network-layer tests.
func testNetworkConfig() bridgelooptester.Network {
	return bridgelooptester.Network{
		NetworkID:  1,
		Name:       "L2A",
		RPCURL:     "http://127.0.0.1:14545",
		BridgeAddr: common.HexToAddress("0x1111111111111111111111111111111111111111"),
		ChainID:    testChainID,
	}
}

// expectFees registers the pricing calls every submission makes, without call-count limits.
func expectFees(backend *mocks.EthBackend) {
	backend.EXPECT().
		HeaderByNumber(mock.Anything, mock.Anything).
		Return(&ethtypes.Header{Number: big.NewInt(1), BaseFee: big.NewInt(1_000_000_000)}, nil)
	backend.EXPECT().SuggestGasTipCap(mock.Anything).Return(big.NewInt(1_000_000_000), nil)
}

// successReceipt returns a successful receipt for txHash.
func successReceipt(txHash common.Hash) *ethtypes.Receipt {
	return &ethtypes.Receipt{
		Status:      ethtypes.ReceiptStatusSuccessful,
		TxHash:      txHash,
		BlockNumber: big.NewInt(10),
		GasUsed:     21_000,
	}
}

// newTestClient builds a NetworkClient over backend with an ephemeral signer.
func newTestClient(
	t *testing.T, backend *mocks.EthBackend, cfg bridgelooptester.Network, opts ...bridgelooptester.NetworkOption,
) (bridgelooptester.NetworkClient, *keySigner) {
	t.Helper()

	signer := newKeySigner(t, big.NewInt(testChainID))
	backend.EXPECT().ChainID(mock.Anything).Return(big.NewInt(testChainID), nil).Once()

	client, err := bridgelooptester.NewNetworkClientWithBackend(
		context.Background(), cfg, backend, signer, log.NewLoggerNil(), opts...)
	require.NoError(t, err)

	return client, signer
}

// TestSendTxSerializesNonces is the first-class regression test for the failure mode that silently
// wedges a long soak run: several loops sharing one EOA on one network must never reuse a nonce or
// leave a gap in the sequence. It also pins the caching behaviour - eth_getTransactionCount is
// queried exactly once, not once per submission.
func TestSendTxSerializesNonces(t *testing.T) {
	t.Parallel()

	const (
		baseNonce   = uint64(7)
		concurrency = 32
	)

	backend := mocks.NewEthBackend(t)
	client, signer := newTestClient(t, backend, testNetworkConfig())

	backend.EXPECT().PendingNonceAt(mock.Anything, signer.PublicAddress()).Return(baseNonce, nil).Once()
	backend.EXPECT().EstimateGas(mock.Anything, mock.Anything).Return(uint64(21_000), nil)
	expectFees(backend)

	var (
		mu     sync.Mutex
		nonces []uint64
	)
	backend.EXPECT().
		SendTransaction(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, tx *ethtypes.Transaction) error {
			mu.Lock()
			defer mu.Unlock()
			nonces = append(nonces, tx.Nonce())

			return nil
		})
	backend.EXPECT().
		TransactionReceipt(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, txHash common.Hash) (*ethtypes.Receipt, error) {
			return successReceipt(txHash), nil
		})

	to := common.HexToAddress("0x2222222222222222222222222222222222222222")

	var wg sync.WaitGroup
	errs := make([]error, concurrency)
	for i := range concurrency {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = client.SendTx(context.Background(), bridgelooptester.TxRequest{
				Label: fmt.Sprintf("call-%d", idx),
				To:    &to,
				Data:  []byte{0x01},
			})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "send %d failed", i)
	}

	require.Len(t, nonces, concurrency)
	sort.Slice(nonces, func(i, j int) bool { return nonces[i] < nonces[j] })
	for i, nonce := range nonces {
		require.Equal(t, baseNonce+uint64(i), nonce, "nonce sequence has a gap or a reuse at index %d", i)
	}
}

// TestSendTxResyncsNonceAfterRejectedSubmission checks that a submission the node rejects does not
// leave a permanent hole in the nonce sequence: the next submission re-reads the pending nonce and
// reuses the one that was never consumed.
func TestSendTxResyncsNonceAfterRejectedSubmission(t *testing.T) {
	t.Parallel()

	const baseNonce = uint64(5)

	backend := mocks.NewEthBackend(t)
	client, signer := newTestClient(t, backend, testNetworkConfig())

	backend.EXPECT().PendingNonceAt(mock.Anything, signer.PublicAddress()).Return(baseNonce, nil).Twice()
	backend.EXPECT().EstimateGas(mock.Anything, mock.Anything).Return(uint64(21_000), nil)
	expectFees(backend)

	var sent []uint64
	backend.EXPECT().
		SendTransaction(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, tx *ethtypes.Transaction) error {
			sent = append(sent, tx.Nonce())
			if len(sent) == 1 {
				return errors.New("replacement transaction underpriced")
			}

			return nil
		}).Twice()
	backend.EXPECT().
		TransactionReceipt(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, txHash common.Hash) (*ethtypes.Receipt, error) {
			return successReceipt(txHash), nil
		}).Once()

	to := common.HexToAddress("0x2222222222222222222222222222222222222222")

	_, err := client.SendTx(context.Background(), bridgelooptester.TxRequest{Label: "first", To: &to})
	require.ErrorContains(t, err, "first on L2A: submit transaction (nonce 5)")

	_, err = client.SendTx(context.Background(), bridgelooptester.TxRequest{Label: "second", To: &to})
	require.NoError(t, err)

	require.Equal(t, []uint64{baseNonce, baseNonce}, sent)
}

// TestSendTxReceiptTimeout checks that an unmined transaction fails with a message naming the
// transaction, the network, the timeout and the nonce, rather than hanging forever.
func TestSendTxReceiptTimeout(t *testing.T) {
	t.Parallel()

	backend := mocks.NewEthBackend(t)
	client, signer := newTestClient(t, backend, testNetworkConfig(),
		bridgelooptester.WithReceiptTimeout(60*time.Millisecond), bridgelooptester.WithReceiptPollInterval(5*time.Millisecond))

	backend.EXPECT().PendingNonceAt(mock.Anything, signer.PublicAddress()).Return(uint64(3), nil).Once()
	backend.EXPECT().EstimateGas(mock.Anything, mock.Anything).Return(uint64(21_000), nil)
	expectFees(backend)
	backend.EXPECT().SendTransaction(mock.Anything, mock.Anything).Return(nil)
	backend.EXPECT().
		TransactionReceipt(mock.Anything, mock.Anything).
		Return(nil, ethereum.NotFound)

	to := common.HexToAddress("0x2222222222222222222222222222222222222222")

	receipt, err := client.SendTx(context.Background(), bridgelooptester.TxRequest{Label: "bridgeAsset", To: &to})
	require.Nil(t, receipt)
	require.ErrorContains(t, err, "gave up waiting for the receipt of tx")
	require.ErrorContains(t, err, "on L2A after 60ms (nonce 3)")
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

// TestSendTxSurfacesRevertReasonFromFailedReceipt checks that a mined-but-failed claim produces an
// error a human can act on (the named custom error, the tx hash, the block) and that
// IsAlreadyClaimed recognises it - the branch DESIGN.md §4's S5submit depends on.
func TestSendTxSurfacesRevertReasonFromFailedReceipt(t *testing.T) {
	t.Parallel()

	backend := mocks.NewEthBackend(t)
	client, signer := newTestClient(t, backend, testNetworkConfig())

	backend.EXPECT().PendingNonceAt(mock.Anything, signer.PublicAddress()).Return(uint64(1), nil).Once()
	backend.EXPECT().EstimateGas(mock.Anything, mock.Anything).Return(uint64(100_000), nil)
	expectFees(backend)
	backend.EXPECT().SendTransaction(mock.Anything, mock.Anything).Return(nil)
	backend.EXPECT().
		TransactionReceipt(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, txHash common.Hash) (*ethtypes.Receipt, error) {
			return &ethtypes.Receipt{
				Status:      ethtypes.ReceiptStatusFailed,
				TxHash:      txHash,
				BlockNumber: big.NewInt(42),
				GasUsed:     99_000,
			}, nil
		})
	backend.EXPECT().
		CallContract(mock.Anything, mock.Anything, mock.Anything).
		Return(nil, &dataError{message: "execution reverted", data: bridgelooptester.AlreadyClaimedSelector})

	to := common.HexToAddress("0x2222222222222222222222222222222222222222")

	receipt, err := client.SendTx(context.Background(), bridgelooptester.TxRequest{Label: "claimAsset", To: &to})
	require.Error(t, err)
	require.NotNil(t, receipt, "the receipt of a reverted transaction is still returned to the caller")

	var revertErr *bridgelooptester.RevertError
	require.ErrorAs(t, err, &revertErr)
	require.Equal(t, "claimAsset", revertErr.Label)
	require.Equal(t, "L2A", revertErr.Network)
	require.Equal(t, big.NewInt(42), revertErr.BlockNumber)
	require.Equal(t, uint64(99_000), revertErr.GasUsed)
	require.Equal(t, bridgelooptester.AlreadyClaimedSelector, hexutil.Encode(revertErr.Data))

	require.ErrorContains(t, err, "claimAsset on L2A: tx ")
	require.ErrorContains(t, err, "reverted in block 42 (gas used 99000)")
	require.ErrorContains(t, err, "AlreadyClaimed()")
	require.True(t, bridgelooptester.IsAlreadyClaimed(err))
}

// TestSendTxSurfacesRevertReasonFromEstimateGas checks the pre-flight path: a node that rejects
// eth_estimateGas with a revert payload must produce the decoded reason, not "execution reverted".
func TestSendTxSurfacesRevertReasonFromEstimateGas(t *testing.T) {
	t.Parallel()

	backend := mocks.NewEthBackend(t)
	client, signer := newTestClient(t, backend, testNetworkConfig())

	backend.EXPECT().PendingNonceAt(mock.Anything, signer.PublicAddress()).Return(uint64(1), nil).Once()
	backend.EXPECT().
		EstimateGas(mock.Anything, mock.Anything).
		Return(0, &dataError{
			message: "execution reverted",
			data:    hexutil.Encode(encodeErrorString(t, "ERC20: insufficient allowance")),
		})

	to := common.HexToAddress("0x2222222222222222222222222222222222222222")

	_, err := client.SendTx(context.Background(), bridgelooptester.TxRequest{Label: "bridgeAsset(erc20)", To: &to})
	require.ErrorContains(t, err, "bridgeAsset(erc20) on L2A: estimate gas")
	require.ErrorContains(t, err, "execution reverted: ERC20: insufficient allowance")
	require.False(t, bridgelooptester.IsAlreadyClaimed(err))
}

// TestSendTxAppliesGasOffsetAndHonoursExplicitGasLimit pins both gas paths: an estimate gets the
// network's GasOffset added, an explicit TxRequest.GasLimit skips estimation entirely.
func TestSendTxAppliesGasOffsetAndHonoursExplicitGasLimit(t *testing.T) {
	t.Parallel()

	cfg := testNetworkConfig()
	cfg.GasOffset = bridgelooptester.NewWeiAmount(50_000)

	backend := mocks.NewEthBackend(t)
	client, signer := newTestClient(t, backend, cfg)

	backend.EXPECT().PendingNonceAt(mock.Anything, signer.PublicAddress()).Return(uint64(0), nil).Once()
	// Registered .Once(): the second submission passes an explicit gas limit and must not estimate.
	backend.EXPECT().EstimateGas(mock.Anything, mock.Anything).Return(uint64(21_000), nil).Once()
	expectFees(backend)

	var gasLimits []uint64
	backend.EXPECT().
		SendTransaction(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, tx *ethtypes.Transaction) error {
			gasLimits = append(gasLimits, tx.Gas())

			return nil
		}).Twice()
	backend.EXPECT().
		TransactionReceipt(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, txHash common.Hash) (*ethtypes.Receipt, error) {
			return successReceipt(txHash), nil
		})

	to := common.HexToAddress("0x2222222222222222222222222222222222222222")

	_, err := client.SendTx(context.Background(), bridgelooptester.TxRequest{Label: "estimated", To: &to})
	require.NoError(t, err)

	_, err = client.SendTx(context.Background(), bridgelooptester.TxRequest{Label: "fixed", To: &to, GasLimit: 500_000})
	require.NoError(t, err)

	require.Equal(t, []uint64{71_000, 500_000}, gasLimits)
}

// TestSendTxFallsBackToLegacyFeesWithoutBaseFee checks the pre-London fee path.
func TestSendTxFallsBackToLegacyFeesWithoutBaseFee(t *testing.T) {
	t.Parallel()

	backend := mocks.NewEthBackend(t)
	client, signer := newTestClient(t, backend, testNetworkConfig())

	backend.EXPECT().PendingNonceAt(mock.Anything, signer.PublicAddress()).Return(uint64(0), nil).Once()
	backend.EXPECT().EstimateGas(mock.Anything, mock.Anything).Return(uint64(21_000), nil)
	backend.EXPECT().
		HeaderByNumber(mock.Anything, mock.Anything).
		Return(&ethtypes.Header{Number: big.NewInt(1)}, nil)
	backend.EXPECT().SuggestGasPrice(mock.Anything).Return(big.NewInt(7), nil)

	var txType uint8
	backend.EXPECT().
		SendTransaction(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, tx *ethtypes.Transaction) error {
			txType = tx.Type()

			return nil
		})
	backend.EXPECT().
		TransactionReceipt(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, txHash common.Hash) (*ethtypes.Receipt, error) {
			return successReceipt(txHash), nil
		})

	to := common.HexToAddress("0x2222222222222222222222222222222222222222")
	_, err := client.SendTx(context.Background(), bridgelooptester.TxRequest{Label: "legacy", To: &to})
	require.NoError(t, err)
	require.Equal(t, ethtypes.LegacyTxType, int(txType))
}

// TestNewNetworkClientWithBackendValidation covers the constructor's guard rails, including the
// configured-vs-reported chain ID check that turns a stale config into an immediate failure.
func TestNewNetworkClientWithBackendValidation(t *testing.T) {
	t.Parallel()

	cfg := testNetworkConfig()
	logger := log.NewLoggerNil()

	t.Run("nil backend", func(t *testing.T) {
		t.Parallel()
		_, err := bridgelooptester.NewNetworkClientWithBackend(context.Background(), cfg, nil, newKeySigner(t, big.NewInt(1)), logger)
		require.ErrorContains(t, err, `new network client "L2A": backend is required`)
	})

	t.Run("nil signer", func(t *testing.T) {
		t.Parallel()
		_, err := bridgelooptester.NewNetworkClientWithBackend(context.Background(), cfg, mocks.NewEthBackend(t), nil, logger)
		require.ErrorContains(t, err, `new network client "L2A": signer is required`)
	})

	t.Run("nil logger", func(t *testing.T) {
		t.Parallel()
		_, err := bridgelooptester.NewNetworkClientWithBackend(
			context.Background(), cfg, mocks.NewEthBackend(t), newKeySigner(t, big.NewInt(1)), nil)
		require.ErrorContains(t, err, `new network client "L2A": logger is required`)
	})

	t.Run("chain id mismatch", func(t *testing.T) {
		t.Parallel()
		backend := mocks.NewEthBackend(t)
		backend.EXPECT().ChainID(mock.Anything).Return(big.NewInt(999), nil).Once()
		_, err := bridgelooptester.NewNetworkClientWithBackend(
			context.Background(), cfg, backend, newKeySigner(t, big.NewInt(1)), logger)
		require.ErrorContains(t, err, "configured ChainID 20201 does not match the chain id 999")
	})

	t.Run("gas offset overflow", func(t *testing.T) {
		t.Parallel()
		overflowing := cfg
		overflowing.GasOffset.SetString("100000000000000000000", 10)
		backend := mocks.NewEthBackend(t)
		backend.EXPECT().ChainID(mock.Anything).Return(big.NewInt(testChainID), nil).Once()
		_, err := bridgelooptester.NewNetworkClientWithBackend(
			context.Background(), overflowing, backend, newKeySigner(t, big.NewInt(1)), logger)
		require.ErrorContains(t, err, "does not fit in a uint64 gas limit")
	})
}

// TestNetworkClientAccessors pins the identity accessors S6/S7 read.
func TestNetworkClientAccessors(t *testing.T) {
	t.Parallel()

	backend := mocks.NewEthBackend(t)
	client, signer := newTestClient(t, backend, testNetworkConfig())

	require.Equal(t, uint32(1), client.NetworkID())
	require.Equal(t, "L2A", client.Name())
	require.Equal(t, big.NewInt(testChainID), client.ChainID())
	require.Equal(t, signer.PublicAddress(), client.From())
	require.Equal(t, backend, client.Backend())

	// ChainID must hand out a copy, not the client's own value.
	client.ChainID().SetInt64(1)
	require.Equal(t, big.NewInt(testChainID), client.ChainID())

	account := common.HexToAddress("0x3333333333333333333333333333333333333333")
	backend.EXPECT().BalanceAt(mock.Anything, account, (*big.Int)(nil)).Return(big.NewInt(1234), nil).Once()
	balance, err := client.NativeBalance(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, big.NewInt(1234), balance)

	backend.EXPECT().
		BalanceAt(mock.Anything, account, (*big.Int)(nil)).
		Return(nil, errors.New("boom")).Once()
	_, err = client.NativeBalance(context.Background(), account)
	require.ErrorContains(t, err, "read native balance of")
	require.ErrorContains(t, err, "on L2A: boom")

	client.Close() // no-op for an injected backend
}

// TestSendTxHandsTheSignedHashToThePreBroadcastHook is the mechanism the resumable bridge
// submission rests on: the hook sees the transaction's real hash and nonce, and it sees them
// *before* the node does. The test asserts the ordering directly - the broadcast records what the
// hook had already observed.
func TestSendTxHandsTheSignedHashToThePreBroadcastHook(t *testing.T) {
	t.Parallel()

	const baseNonce = uint64(4)

	backend := mocks.NewEthBackend(t)
	client, signer := newTestClient(t, backend, testNetworkConfig())

	backend.EXPECT().PendingNonceAt(mock.Anything, signer.PublicAddress()).Return(baseNonce, nil).Once()
	backend.EXPECT().EstimateGas(mock.Anything, mock.Anything).Return(uint64(21_000), nil)
	expectFees(backend)

	var (
		observed  bridgelooptester.PendingTx
		hookCalls int
		broadcast common.Hash
	)
	backend.EXPECT().
		SendTransaction(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, tx *ethtypes.Transaction) error {
			require.Equal(t, 1, hookCalls, "the hook must run before the broadcast")
			broadcast = tx.Hash()

			return nil
		}).Once()
	backend.EXPECT().
		TransactionReceipt(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, txHash common.Hash) (*ethtypes.Receipt, error) {
			return successReceipt(txHash), nil
		}).Once()

	to := common.HexToAddress("0x2222222222222222222222222222222222222222")
	receipt, err := client.SendTx(context.Background(), bridgelooptester.TxRequest{
		Label: "bridgeAsset",
		To:    &to,
		OnSigned: func(ctx context.Context, pending bridgelooptester.PendingTx) error {
			hookCalls++
			observed = pending
			require.NoError(t, ctx.Err(), "the hook's context must still be live")

			return nil
		},
	})
	require.NoError(t, err)

	require.Equal(t, 1, hookCalls)
	require.Equal(t, broadcast, observed.Hash, "the hook's hash is the hash that was broadcast")
	require.Equal(t, receipt.TxHash, observed.Hash)
	require.Equal(t, baseNonce, observed.Nonce)
	require.Equal(t, signer.PublicAddress(), observed.From)
	require.Equal(t, uint32(1), observed.Network)
	require.Equal(t, "bridgeAsset", observed.Label)
}

// TestSendTxAbortsWhenThePreBroadcastHookFails covers the safe direction: a caller that cannot
// record the hash stops the transaction from going out at all, and the nonce it reserved is still
// free for the next submission.
func TestSendTxAbortsWhenThePreBroadcastHookFails(t *testing.T) {
	t.Parallel()

	const baseNonce = uint64(9)

	backend := mocks.NewEthBackend(t)
	client, signer := newTestClient(t, backend, testNetworkConfig())

	// Twice: an aborted submission never consumed its nonce, and it does not cache one either, so
	// the next submission re-reads the pending nonce and gets the very same value back.
	backend.EXPECT().PendingNonceAt(mock.Anything, signer.PublicAddress()).Return(baseNonce, nil).Twice()
	backend.EXPECT().EstimateGas(mock.Anything, mock.Anything).Return(uint64(21_000), nil)
	expectFees(backend)

	var sent []uint64
	backend.EXPECT().
		SendTransaction(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, tx *ethtypes.Transaction) error {
			sent = append(sent, tx.Nonce())

			return nil
		}).Once()
	backend.EXPECT().
		TransactionReceipt(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, txHash common.Hash) (*ethtypes.Receipt, error) {
			return successReceipt(txHash), nil
		}).Once()

	to := common.HexToAddress("0x2222222222222222222222222222222222222222")

	_, err := client.SendTx(context.Background(), bridgelooptester.TxRequest{
		Label: "bridgeAsset",
		To:    &to,
		OnSigned: func(context.Context, bridgelooptester.PendingTx) error {
			return errors.New("disk full")
		},
	})
	require.ErrorContains(t, err, "the pre-broadcast hook for tx")
	require.ErrorContains(t, err, "was NOT submitted")
	require.ErrorContains(t, err, "disk full")
	require.Empty(t, sent, "nothing may reach the node when the hook refuses")

	// The reserved nonce was never consumed, so the next submission gets it again.
	_, err = client.SendTx(context.Background(), bridgelooptester.TxRequest{Label: "second", To: &to})
	require.NoError(t, err)
	require.Equal(t, []uint64{baseNonce}, sent)
}

// TestSendTxSurvivesAPanickingPreBroadcastHook checks that a buggy hook cannot take the process
// down, cannot unwind through the sender's held mutex, and cannot let the transaction out: the
// panic becomes an ordinary aborting error and the client keeps working.
func TestSendTxSurvivesAPanickingPreBroadcastHook(t *testing.T) {
	t.Parallel()

	backend := mocks.NewEthBackend(t)
	client, signer := newTestClient(t, backend, testNetworkConfig())

	backend.EXPECT().PendingNonceAt(mock.Anything, signer.PublicAddress()).Return(uint64(1), nil).Twice()
	backend.EXPECT().EstimateGas(mock.Anything, mock.Anything).Return(uint64(21_000), nil)
	expectFees(backend)
	backend.EXPECT().SendTransaction(mock.Anything, mock.Anything).Return(nil).Once()
	backend.EXPECT().
		TransactionReceipt(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, txHash common.Hash) (*ethtypes.Receipt, error) {
			return successReceipt(txHash), nil
		}).Once()

	to := common.HexToAddress("0x2222222222222222222222222222222222222222")

	_, err := client.SendTx(context.Background(), bridgelooptester.TxRequest{
		Label: "bridgeAsset",
		To:    &to,
		OnSigned: func(context.Context, bridgelooptester.PendingTx) error {
			panic("checkpoint writer is broken")
		},
	})
	require.ErrorContains(t, err, "pre-broadcast hook panicked: checkpoint writer is broken")
	require.ErrorContains(t, err, "was NOT submitted")

	// The mutex was released, so the client is still usable.
	_, err = client.SendTx(context.Background(), bridgelooptester.TxRequest{Label: "second", To: &to})
	require.NoError(t, err)
}

// TestSendTxPreBroadcastHookTimeout checks that a slow hook cannot stall the nonce-serialization
// critical section indefinitely: it is given a bounded context, and a hook that honours it aborts
// the submission instead of holding the mutex forever.
func TestSendTxPreBroadcastHookTimeout(t *testing.T) {
	t.Parallel()

	backend := mocks.NewEthBackend(t)
	client, signer := newTestClient(t, backend, testNetworkConfig(),
		bridgelooptester.WithPreBroadcastTimeout(30*time.Millisecond))

	backend.EXPECT().PendingNonceAt(mock.Anything, signer.PublicAddress()).Return(uint64(2), nil).Once()
	backend.EXPECT().EstimateGas(mock.Anything, mock.Anything).Return(uint64(21_000), nil)
	expectFees(backend)

	to := common.HexToAddress("0x2222222222222222222222222222222222222222")

	start := time.Now()
	_, err := client.SendTx(context.Background(), bridgelooptester.TxRequest{
		Label: "bridgeAsset",
		To:    &to,
		OnSigned: func(ctx context.Context, _ bridgelooptester.PendingTx) error {
			<-ctx.Done()

			return ctx.Err()
		},
	})
	require.ErrorContains(t, err, "was NOT submitted")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), time.Second, "the hook must not hold the sender open")
	// No SendTransaction expectation is registered: mocks.EthBackend fails the test if the
	// transaction was broadcast anyway.
}
