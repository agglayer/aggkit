// Package service decides if a GER can be signed and signs it. A GER is signed only if it is in the AgglayerGER
// contract of L1 and the L1 block that inserted it is canonical and final for the configured finality.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/agglayer/aggkit/tools/ger_validator_signer/cache"
	"github.com/agglayer/aggkit/tools/ger_validator_signer/finality"
	"github.com/agglayer/aggkit/tools/ger_validator_signer/l1"
	"github.com/ethereum/go-ethereum/common"
	"golang.org/x/sync/singleflight"
)

var (
	// ErrInvalidGER is returned for a GER that can't be signed whatever L1 says (the zero hash)
	ErrInvalidGER = errors.New("invalid global exit root")
	// ErrGERUnknown is returned when the GER is not in AgglayerGERs globalExitRootMap
	ErrGERUnknown = errors.New("global exit root not found in AgglayerGER.globalExitRootMap")
	// ErrGERBlockNotCanonical is returned when the L1 block that inserted the GER is not in the canonical chain
	ErrGERBlockNotCanonical = errors.New("the L1 block that inserted the global exit root is not in the canonical chain")
	// ErrGERNotFinal is returned when the L1 block that inserted the GER is not final yet. It is retryable
	ErrGERNotFinal = errors.New("the L1 block that inserted the global exit root is not final yet")
)

// NotFinalError is the detail of ErrGERNotFinal
type NotFinalError struct {
	// InsertionBlock is the L1 block that inserted the GER
	InsertionBlock uint64
	// FinalBlock is the highest L1 block that is final now. Only if HasFinalBlock
	FinalBlock    uint64
	HasFinalBlock bool
}

func (e *NotFinalError) Error() string {
	if !e.HasFinalBlock {
		return fmt.Sprintf("%s: it was inserted in L1 block %d and no block is final yet", ErrGERNotFinal, e.InsertionBlock)
	}
	return fmt.Sprintf("%s: it was inserted in L1 block %d and the final block is %d",
		ErrGERNotFinal, e.InsertionBlock, e.FinalBlock)
}

// Is makes errors.Is(err, ErrGERNotFinal) work
func (e *NotFinalError) Is(target error) bool {
	return target == ErrGERNotFinal
}

// L1Reader is what the service needs to know from L1
type L1Reader interface {
	// GlobalExitRootMap returns what AgglayerGER.globalExitRootMap holds for the GER: the hash of the block
	// previous to the one that inserted it, or zero if it was never inserted
	GlobalExitRootMap(ctx context.Context, agglayerGER common.Address, ger common.Hash) (common.Hash, error)
	// BlockByHash returns the block (that can be an orphan) or l1.ErrBlockNotFound
	BlockByHash(ctx context.Context, hash common.Hash) (*l1.Block, error)
	// BlockByNumber returns the block of the canonical chain or l1.ErrBlockNotFound
	BlockByNumber(ctx context.Context, number uint64) (*l1.Block, error)
	// BlockNumberByTag returns the number of the latest, safe or finalized block
	BlockNumberByTag(ctx context.Context, tag string) (uint64, error)
}

// GERSigner signs a GER as the AggOracleCommittee verifies it
type GERSigner interface {
	// Address is the address of the key that signs
	Address() common.Address
	// SignGER returns the 65 bytes signature of the GER, with v 27 or 28 and a low s
	SignGER(ctx context.Context, ger common.Hash) ([]byte, error)
}

// Config is the configuration of the service
type Config struct {
	// AgglayerGER is the AgglayerGER contract of L1
	AgglayerGER common.Address
	// Finality is the finality the L1 block that inserted the GER needs
	Finality finality.Rule
	// NegativeTTL is how long the cache keeps that a GER can't be signed. 0 disables it
	NegativeTTL time.Duration
	// RequestTimeout limits the work for a GER (the queries to L1 and the signing)
	RequestTimeout time.Duration
}

// Result is a signed GER
type Result struct {
	GER       common.Hash
	Signature []byte
	Signer    common.Address
}

// Info describes the validator
type Info struct {
	Signer          common.Address
	AgglayerGER     common.Address
	FinalityTag     string
	FinalityNumBlks uint64
}

// Service signs GERs
type Service struct {
	logger  *slog.Logger
	cfg     Config
	l1      L1Reader
	signer  GERSigner
	cache   *cache.Cache
	flights singleflight.Group
}

// New creates the service
func New(logger *slog.Logger, cfg Config, l1Reader L1Reader, signer GERSigner, answers *cache.Cache) *Service {
	return &Service{
		logger: logger.With("module", "service"),
		cfg:    cfg,
		l1:     l1Reader,
		signer: signer,
		cache:  answers,
	}
}

// Info returns the data that identifies the validator
func (s *Service) Info() Info {
	return Info{
		Signer:          s.signer.Address(),
		AgglayerGER:     s.cfg.AgglayerGER,
		FinalityTag:     s.cfg.Finality.Tag,
		FinalityNumBlks: s.cfg.Finality.NumBlocks,
	}
}

// SignGER signs the GER if it is in L1 and its block is canonical and final. The answers are cached: a GER that
// was signed is not queried nor signed again, and the same happens, for a short time, to a GER that can't be signed.
// Concurrent requests for the same GER share one query and one signature.
func (s *Service) SignGER(ctx context.Context, ger common.Hash) (*Result, error) {
	if ger == (common.Hash{}) {
		return nil, fmt.Errorf("%w: it is the zero hash", ErrInvalidGER)
	}

	if entry, ok := s.cache.Get(ger); ok {
		if entry.Signature != nil {
			return &Result{GER: ger, Signature: entry.Signature, Signer: s.signer.Address()}, nil
		}
		return nil, entry.Err
	}

	// Detached from the context of the caller: the work is shared with the other callers, and the first
	// one to give up must not make the others fail
	flight := s.flights.DoChan(ger.Hex(), func() (any, error) {
		workCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.RequestTimeout)
		defer cancel()
		return s.signAndCache(workCtx, ger)
	})

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-flight:
		if res.Err != nil {
			return nil, res.Err
		}
		result, _ := res.Val.(*Result)
		return result, nil
	}
}

// signAndCache checks the GER in L1, signs it and keeps the answer in the cache
func (s *Service) signAndCache(ctx context.Context, ger common.Hash) (*Result, error) {
	if err := s.checkGER(ctx, ger); err != nil {
		if isRejection(err) {
			s.logger.Info("GER not signed", "ger", ger, "reason", err)
			s.cache.PutError(ger, err, s.cfg.NegativeTTL)
		}
		return nil, err
	}

	signature, err := s.signer.SignGER(ctx, ger)
	if err != nil {
		return nil, fmt.Errorf("failed to sign the GER %s: %w", ger, err)
	}
	s.cache.PutSignature(ger, signature)
	s.logger.Info("GER signed", "ger", ger, "signer", s.signer.Address())

	return &Result{GER: ger, Signature: signature, Signer: s.signer.Address()}, nil
}

// checkGER returns nil if the GER is in AgglayerGER and the L1 block that inserted it is canonical and final.
// The contract holds the hash of the previous block of the insertion block, so:
//  1. the hash is resolved to its block (the parent, N), and N must be in the canonical chain: a node can return an
//     orphan block when asked by hash, so the canonical block of that height must have the same hash
//  2. the insertion block is N+1 and it must not be after the final block: the block of the finality tag minus
//     the configured number of blocks
func (s *Service) checkGER(ctx context.Context, ger common.Hash) error {
	parentHash, err := s.l1.GlobalExitRootMap(ctx, s.cfg.AgglayerGER, ger)
	if err != nil {
		return fmt.Errorf("failed to read globalExitRootMap: %w", err)
	}
	if parentHash == (common.Hash{}) {
		return ErrGERUnknown
	}

	parent, err := s.l1.BlockByHash(ctx, parentHash)
	if errors.Is(err, l1.ErrBlockNotFound) {
		return fmt.Errorf("%w: block %s is unknown to the node", ErrGERBlockNotCanonical, parentHash)
	}
	if err != nil {
		return fmt.Errorf("failed to get the block %s: %w", parentHash, err)
	}

	canonical, err := s.l1.BlockByNumber(ctx, parent.Number)
	if errors.Is(err, l1.ErrBlockNotFound) {
		return fmt.Errorf("%w: block %d is unknown to the node", ErrGERBlockNotCanonical, parent.Number)
	}
	if err != nil {
		return fmt.Errorf("failed to get the block %d: %w", parent.Number, err)
	}
	if canonical.Hash != parent.Hash {
		return fmt.Errorf("%w: the block %d is %s, not %s", ErrGERBlockNotCanonical,
			parent.Number, canonical.Hash, parent.Hash)
	}

	tagged, err := s.l1.BlockNumberByTag(ctx, s.cfg.Finality.Tag)
	if err != nil {
		return fmt.Errorf("failed to get the block of the tag %s: %w", s.cfg.Finality.Tag, err)
	}
	insertionBlock := parent.Number + 1
	finalBlock, hasFinalBlock := s.cfg.Finality.FinalBlock(tagged)
	if !hasFinalBlock || insertionBlock > finalBlock {
		return &NotFinalError{InsertionBlock: insertionBlock, FinalBlock: finalBlock, HasFinalBlock: hasFinalBlock}
	}
	return nil
}

// isRejection returns true for the errors that say the GER can't be signed (now), as opposed to a failure
// of the validator or of L1
func isRejection(err error) bool {
	return errors.Is(err, ErrGERUnknown) || errors.Is(err, ErrGERBlockNotCanonical) || errors.Is(err, ErrGERNotFinal)
}
