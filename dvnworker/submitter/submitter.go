// Package submitter signs and submits claimAndVerify transactions to the
// AggLayerDVNCoordinator on the destination chain.
package submitter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/agglayer/aggkit/dvnworker/bindings"
	"github.com/agglayer/aggkit/dvnworker/proofbuilder"
	"github.com/agglayer/aggkit/log"
)

const (
	maxAttempts    = 3
	baseRetryDelay = time.Second
)

// alreadyProcessedSelector is the 4-byte ABI selector for AlreadyProcessed(bytes32).
// Computed as keccak256("AlreadyProcessed(bytes32)")[:4] = 0x1a20d3e6.
var alreadyProcessedSelector = []byte{0x1a, 0x20, 0xd3, 0xe6}

// Origin mirrors the Solidity Origin struct (srcEid, sender, nonce).
type Origin struct {
	SrcEid uint32
	Sender [32]byte
	Nonce  uint64
}

// Job carries all parameters needed for a single claimAndVerify submission.
type Job struct {
	// Origin is the LayerZero packet origin (srcEid, sender, nonce).
	Origin Origin
	// GUID is the LayerZero globally unique message identifier.
	GUID [32]byte
	// Message is the raw LayerZero OFT message payload (guid ++ content).
	Message []byte
	// PayloadHash is keccak256(guid ++ message) as expected by the coordinator.
	PayloadHash [32]byte
	// Claim is the AggLayer bridge claim data assembled by the proof builder.
	Claim proofbuilder.AggLayerClaim
	// PacketHeader is the 81-byte LayerZero packet header.
	PacketHeader []byte
	// Confirmations is the number of block confirmations the coordinator must
	// pass to IReceiveUlnE2.verify.
	Confirmations uint64
}

// TxSender is the narrow interface the Submitter needs from an Ethereum client.
type TxSender interface {
	bind.ContractTransactor
}

// CoordinatorCaller is the narrow interface wrapping the AggLayerDVNCoordinator
// binding that the Submitter needs.  It is defined here so tests can mock it
// without deploying a real contract.
type CoordinatorCaller interface {
	// ClaimAndVerify submits the combined claim+verify transaction.
	ClaimAndVerify(
		opts *bind.TransactOpts,
		origin bindings.Origin,
		guid [32]byte,
		message []byte,
		claim bindings.AggLayerClaim,
		packetHeader []byte,
		payloadHash [32]byte,
		confirmations uint64,
	) (*types.Transaction, error)
}

// ReceiptWaiter waits for a transaction to be mined and returns its receipt.
type ReceiptWaiter interface {
	WaitMined(ctx context.Context, tx *types.Transaction) (*types.Receipt, error)
}

// Submitter signs and submits claimAndVerify transactions to the destination
// chain coordinator, handling idempotent reverts gracefully.
type Submitter struct {
	coordinator    CoordinatorCaller
	txOpts         *bind.TransactOpts
	waiter         ReceiptWaiter
	logger         *log.Logger
	baseRetryDelay time.Duration
}

// New creates a Submitter.
//
// coordinator wraps the AggLayerDVNCoordinator contract on the destination chain.
// txOpts holds the signing context (key, chain ID, gas settings).
// waiter is used to wait for transactions to be mined; pass nil to skip receipt waiting.
// logger may be nil (falls back to the package-level default logger).
func New(
	coordinator CoordinatorCaller,
	txOpts *bind.TransactOpts,
	waiter ReceiptWaiter,
	logger *log.Logger,
) *Submitter {
	if logger == nil {
		logger = log.GetDefaultLogger()
	}
	return &Submitter{
		coordinator:    coordinator,
		txOpts:         txOpts,
		waiter:         waiter,
		logger:         logger,
		baseRetryDelay: baseRetryDelay,
	}
}

// WithRetryDelay returns a copy of the Submitter with the given base retry delay
// (1st retry waits d, 2nd waits 2d, etc.).  Primarily intended for tests.
func (s *Submitter) WithRetryDelay(d time.Duration) *Submitter {
	cp := *s
	cp.baseRetryDelay = d
	return &cp
}

// Submit sends claimAndVerify to the coordinator and waits for the receipt.
// It retries up to maxAttempts times with exponential back-off on transient
// failures.  An AlreadyProcessed revert from the coordinator is treated as
// success (idempotent).
func (s *Submitter) Submit(ctx context.Context, job Job) error {
	bindOrigin := bindings.Origin{
		SrcEid: job.Origin.SrcEid,
		Sender: job.Origin.Sender,
		Nonce:  job.Origin.Nonce,
	}
	bindClaim := toBindingsClaim(job.Claim)

	var lastErr error
	delay := s.baseRetryDelay

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return fmt.Errorf("submitter: context cancelled before attempt %d: %w", attempt, ctx.Err())
		}

		tx, err := s.coordinator.ClaimAndVerify(
			s.txOpts,
			bindOrigin,
			job.GUID,
			job.Message,
			bindClaim,
			job.PacketHeader,
			job.PayloadHash,
			job.Confirmations,
		)
		if err != nil {
			if isAlreadyProcessed(err) {
				s.logger.Infow("submitter: already processed, treating as success",
					"guid", common.Bytes2Hex(job.GUID[:]),
					"attempt", attempt,
				)
				return nil
			}

			lastErr = fmt.Errorf("submitter: attempt %d: send tx: %w", attempt, err)
			s.logger.Warnw("submitter: send failed, will retry",
				"attempt", attempt,
				"maxAttempts", maxAttempts,
				"err", lastErr,
			)

			if attempt < maxAttempts {
				select {
				case <-ctx.Done():
					return fmt.Errorf("submitter: context cancelled during retry back-off: %w", ctx.Err())
				case <-time.After(delay):
				}
				delay *= 2
			}
			continue
		}

		s.logger.Infow("submitter: tx submitted",
			"txHash", tx.Hash().Hex(),
			"guid", common.Bytes2Hex(job.GUID[:]),
			"attempt", attempt,
		)

		if s.waiter != nil {
			receipt, waitErr := s.waiter.WaitMined(ctx, tx)
			if waitErr != nil {
				if errors.Is(waitErr, context.Canceled) || errors.Is(waitErr, context.DeadlineExceeded) {
					return fmt.Errorf("submitter: wait mined: %w", waitErr)
				}
				lastErr = fmt.Errorf("submitter: attempt %d: wait mined: %w", attempt, waitErr)
				s.logger.Warnw("submitter: wait mined failed, will retry",
					"attempt", attempt,
					"err", lastErr,
				)
				if attempt < maxAttempts {
					select {
					case <-ctx.Done():
						return fmt.Errorf("submitter: context cancelled during retry back-off: %w", ctx.Err())
					case <-time.After(delay):
					}
					delay *= 2
				}
				continue
			}

			if receipt.Status == types.ReceiptStatusFailed {
				lastErr = fmt.Errorf("submitter: attempt %d: tx reverted: txHash=%s", attempt, tx.Hash().Hex())
				s.logger.Warnw("submitter: tx reverted",
					"txHash", tx.Hash().Hex(),
					"attempt", attempt,
				)
				if attempt < maxAttempts {
					select {
					case <-ctx.Done():
						return fmt.Errorf("submitter: context cancelled during retry back-off: %w", ctx.Err())
					case <-time.After(delay):
					}
					delay *= 2
				}
				continue
			}

			s.logger.Infow("submitter: tx mined successfully",
				"txHash", tx.Hash().Hex(),
				"blockNumber", receipt.BlockNumber,
				"guid", common.Bytes2Hex(job.GUID[:]),
			)
		}

		return nil
	}

	return fmt.Errorf("submitter: all %d attempts exhausted: %w", maxAttempts, lastErr)
}

// NewTransactOpts builds a *bind.TransactOpts from a hex-encoded ECDSA private key and
// a chain ID.  This is a convenience helper for callers that construct the Submitter
// from configuration values.
func NewTransactOpts(privateKeyHex string, chainID *big.Int) (*bind.TransactOpts, error) {
	key, err := crypto.HexToECDSA(privateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("submitter: parse private key: %w", err)
	}
	opts, err := bind.NewKeyedTransactorWithChainID(key, chainID)
	if err != nil {
		return nil, fmt.Errorf("submitter: new keyed transactor: %w", err)
	}
	return opts, nil
}

// toBindingsClaim converts a proofbuilder.AggLayerClaim to the ABI-generated
// bindings.AggLayerClaim struct that ClaimAndVerify expects.
func toBindingsClaim(c proofbuilder.AggLayerClaim) bindings.AggLayerClaim {
	return bindings.AggLayerClaim{
		SmtProofLocalExitRoot:  c.SMTProofLocalExitRoot,
		SmtProofRollupExitRoot: c.SMTProofRollupExitRoot,
		GlobalIndex:            c.GlobalIndex,
		MainnetExitRoot:        c.MainnetExitRoot,
		RollupExitRoot:         c.RollupExitRoot,
		OriginNetwork:          c.OriginNetwork,
		OriginTokenAddress:     c.OriginTokenAddress,
		DestinationNetwork:     c.DestinationNetwork,
		DestinationAddress:     c.DestinationAddress,
		Amount:                 c.Amount,
		Metadata:               c.Metadata,
	}
}

// isAlreadyProcessed returns true when err encodes the AlreadyProcessed(bytes32)
// custom error selector (0x1a20d3e6).  go-ethereum propagates the raw revert
// payload inside the error message; we check both the typed error interface and
// a raw byte-prefix scan.
func isAlreadyProcessed(err error) bool {
	if err == nil {
		return false
	}
	// go-ethereum wraps EVM reverts in a DataError whose Data() returns the
	// ABI-encoded revert payload as a hex string or []byte.
	type dataErr interface {
		ErrorData() interface{}
	}
	var de dataErr
	if errors.As(err, &de) {
		switch v := de.ErrorData().(type) {
		case []byte:
			return bytes.HasPrefix(v, alreadyProcessedSelector)
		case string:
			raw := common.FromHex(v)
			return bytes.HasPrefix(raw, alreadyProcessedSelector)
		}
	}

	// Fallback: scan the error string for the hex selector.
	msg := err.Error()
	return len(msg) >= 10 && containsSelector(msg, alreadyProcessedSelector)
}

// containsSelector checks whether a string contains the 4-byte selector encoded
// as a lowercase hex run (without the "0x" prefix) anywhere within it.
func containsSelector(s string, selector []byte) bool {
	hex := fmt.Sprintf("%x", selector)
	return len(s) >= len(hex) && stringContains(s, hex)
}

// stringContains is a simple substring search (avoids importing strings in this file).
func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
