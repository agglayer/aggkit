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
	// ErrGERUnknown is returned when the GER is not in L1: globalExitRootMap of the L1 contract AgglayerGER has no
	// entry for it. The detail (which contract, which chain) is in UnknownGERError
	ErrGERUnknown = errors.New("global exit root not found in L1: " +
		"it has no entry in globalExitRootMap of the L1 contract AgglayerGER")
	// ErrGERBlockNotCanonical is returned when the L1 block that inserted the GER is not in the canonical chain
	ErrGERBlockNotCanonical = errors.New("the L1 block that inserted the global exit root is not in the canonical chain")
	// ErrGERNotFinal is returned when the L1 block that inserted the GER is not final yet. It is retryable
	ErrGERNotFinal = errors.New("the L1 block that inserted the global exit root is not final yet")
)

// NotFinalError is the detail of ErrGERNotFinal: where the GER was inserted, the finality it needs and how far
// it is from it
type NotFinalError struct {
	// InsertionBlock is the L1 block that inserted the GER
	InsertionBlock uint64
	// Finality is the finality the insertion block needs (BlockFinality in the configuration)
	Finality finality.Rule
	// TaggedBlock is the L1 block of the tag of the finality now (the latest, safe or finalized block)
	TaggedBlock uint64
	// LatestBlock is the latest L1 block now
	LatestBlock uint64
}

// FinalBlock returns the highest L1 block that is final now. ok is false if no block is final yet
func (e *NotFinalError) FinalBlock() (finalBlock uint64, ok bool) {
	return e.Finality.FinalBlock(e.TaggedBlock)
}

// BlocksToFinal returns how many blocks the block of the tag has to advance for the insertion block to be final
func (e *NotFinalError) BlocksToFinal() uint64 {
	return e.Finality.BlocksToFinal(e.InsertionBlock, e.TaggedBlock)
}

func (e *NotFinalError) Error() string {
	var finalBlock string
	if block, ok := e.FinalBlock(); ok {
		finalBlock = fmt.Sprintf("the final block is %d", block)
	} else {
		finalBlock = "no block is final yet"
	}
	var howFinal string
	switch {
	case e.Finality.Tag != finality.TagLatest && e.Finality.NumBlocks > 0:
		howFinal = fmt.Sprintf("%s block %d - %d, latest block %d",
			e.Finality.Tag, e.TaggedBlock, e.Finality.NumBlocks, e.LatestBlock)
	case e.Finality.Tag != finality.TagLatest:
		howFinal = fmt.Sprintf("%s block %d, latest block %d", e.Finality.Tag, e.TaggedBlock, e.LatestBlock)
	default:
		howFinal = fmt.Sprintf("latest block %d - %d", e.LatestBlock, e.Finality.NumBlocks)
	}
	return fmt.Sprintf("%s: it was inserted in L1 block %d and, with BlockFinality %s, %s (%s): "+
		"%d blocks to go", ErrGERNotFinal, e.InsertionBlock, e.Finality, finalBlock, howFinal, e.BlocksToFinal())
}

// Is makes errors.Is(err, ErrGERNotFinal) work
func (e *NotFinalError) Is(target error) bool {
	return target == ErrGERNotFinal
}

// UnknownGERError is the detail of ErrGERUnknown: the L1 contract and chain where the GER was looked up
type UnknownGERError struct {
	// AgglayerGER is the AgglayerGER contract of L1
	AgglayerGER common.Address
	// L1ChainID is the chain id of L1
	L1ChainID uint64
}

func (e *UnknownGERError) Error() string {
	return fmt.Sprintf("%s %s (L1 chain %d)", ErrGERUnknown, e.AgglayerGER, e.L1ChainID)
}

// Is makes errors.Is(err, ErrGERUnknown) work
func (e *UnknownGERError) Is(target error) bool {
	return target == ErrGERUnknown
}

// L1Reader is what the service needs to know from L1
type L1Reader interface {
	// GlobalExitRootMap returns what AgglayerGER.globalExitRootMap holds for the GER in the latest block: the hash
	// of the block previous to the one that inserted it, or zero if it was never inserted
	GlobalExitRootMap(ctx context.Context, agglayerGER common.Address, ger common.Hash) (common.Hash, error)
	// GlobalExitRootMapAt is GlobalExitRootMap in the state of the block with the hash. It fails if that block is
	// not in the canonical chain
	GlobalExitRootMapAt(
		ctx context.Context, agglayerGER common.Address, ger common.Hash, blockHash common.Hash,
	) (common.Hash, error)
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
	// L1ChainID is the chain id of L1, to say where a GER was looked up
	L1ChainID uint64
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
			s.logger.Debug("GER already signed, answering from the cache", "ger", ger)
			return &Result{GER: ger, Signature: entry.Signature, Signer: s.signer.Address()}, nil
		}
		s.logger.Debug("GER recently not signed, answering from the cache", "ger", ger, "reason", entry.Err)
		return nil, entry.Err
	}

	s.logger.Debug("checking the GER in L1 before signing it", "ger", ger)

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
//  1. the GER is read in the latest block: the hash of the block before the insertion (the parent, N)
//  2. the hash is resolved to its block, and N must be in the canonical chain: a node can return an orphan block when
//     asked by hash, so the canonical block of that height must have the same hash
//  3. the insertion block is N+1 and it must not be after the final block: the block of the finality tag minus
//     the configured number of blocks
//  4. the GER is read again in the state of the final block, pinned by its hash, and it must hold the same parent.
//     Steps 1 to 3 look at the chain at different moments: a reorg between them could replace the insertion block
//     (keeping N) and the height N+1 would look final although the block that inserted the GER is an orphan. Only
//     the state of a final block of the canonical chain says that the GER is there to stay
func (s *Service) checkGER(ctx context.Context, ger common.Hash) error {
	parentHash, err := s.l1.GlobalExitRootMap(ctx, s.cfg.AgglayerGER, ger)
	if err != nil {
		return fmt.Errorf("failed to read globalExitRootMap: %w", err)
	}
	if parentHash == (common.Hash{}) {
		s.logger.Debug("GER check 1/4 (in L1?): no: AgglayerGER.globalExitRootMap(ger) is zero at the latest block",
			"ger", ger, "agglayerGER", s.cfg.AgglayerGER, "l1ChainID", s.cfg.L1ChainID)
		return &UnknownGERError{AgglayerGER: s.cfg.AgglayerGER, L1ChainID: s.cfg.L1ChainID}
	}
	s.logger.Debug("GER check 1/4 (in L1?): yes: AgglayerGER.globalExitRootMap(ger) at the latest block "+
		"holds the hash of the block before the insertion",
		"ger", ger, "agglayerGER", s.cfg.AgglayerGER, "l1ChainID", s.cfg.L1ChainID, "parentBlockHash", parentHash)

	parent, err := s.l1.BlockByHash(ctx, parentHash)
	if errors.Is(err, l1.ErrBlockNotFound) {
		s.logger.Debug("GER check 2/4 (canonical?): no: the node doesn't know the block of the hash",
			"ger", ger, "parentBlockHash", parentHash)
		return fmt.Errorf("%w: block %s is unknown to the node", ErrGERBlockNotCanonical, parentHash)
	}
	if err != nil {
		return fmt.Errorf("failed to get the block %s: %w", parentHash, err)
	}

	canonical, err := s.l1.BlockByNumber(ctx, parent.Number)
	if errors.Is(err, l1.ErrBlockNotFound) {
		s.logger.Debug("GER check 2/4 (canonical?): no: the canonical chain has no block with the number "+
			"of the block of the hash", "ger", ger, "parentBlock", parent.Number, "parentBlockHash", parentHash)
		return fmt.Errorf("%w: block %d is unknown to the node", ErrGERBlockNotCanonical, parent.Number)
	}
	if err != nil {
		return fmt.Errorf("failed to get the block %d: %w", parent.Number, err)
	}
	if canonical.Hash != parent.Hash {
		s.logger.Debug("GER check 2/4 (canonical?): no: the canonical block with that number has another hash "+
			"(the block of the hash is an orphan)", "ger", ger, "parentBlock", parent.Number,
			"parentBlockHash", parent.Hash, "canonicalBlockHash", canonical.Hash)
		return fmt.Errorf("%w: the block %d is %s, not %s", ErrGERBlockNotCanonical,
			parent.Number, canonical.Hash, parent.Hash)
	}
	insertionBlock := parent.Number + 1
	s.logger.Debug("GER check 2/4 (canonical?): yes: the block before the insertion is the canonical one "+
		"with its number", "ger", ger, "parentBlock", parent.Number, "parentBlockHash", parent.Hash,
		"insertionBlock", insertionBlock)

	tagged, err := s.l1.BlockNumberByTag(ctx, s.cfg.Finality.Tag)
	if err != nil {
		return fmt.Errorf("failed to get the block of the tag %s: %w", s.cfg.Finality.Tag, err)
	}
	attrs := []any{"ger", ger, "insertionBlock", insertionBlock, "blockFinality", s.cfg.Finality.String(),
		"tag", s.cfg.Finality.Tag, "taggedBlock", tagged, "numBlocks", s.cfg.Finality.NumBlocks,
		"rule", "final if insertionBlock <= taggedBlock - numBlocks"}
	if s.cfg.Finality.IsFinal(insertionBlock, tagged) {
		finalBlock, _ := s.cfg.Finality.FinalBlock(tagged)
		s.logger.Debug("GER check 3/4 (final?): yes: the insertion block is not after the final block",
			append(attrs, "finalBlock", finalBlock)...)
		return s.checkGERInFinalState(ctx, ger, parentHash, finalBlock)
	}

	// Not final: the latest block says how far the chain is, for the error
	latest := tagged
	if s.cfg.Finality.Tag != finality.TagLatest {
		if latest, err = s.l1.BlockNumberByTag(ctx, finality.TagLatest); err != nil {
			return fmt.Errorf("failed to get the latest block: %w", err)
		}
	}
	notFinal := &NotFinalError{
		InsertionBlock: insertionBlock, Finality: s.cfg.Finality, TaggedBlock: tagged, LatestBlock: latest,
	}
	attrs = append(attrs, "latestBlock", latest, "blocksToFinal", notFinal.BlocksToFinal())
	if finalBlock, ok := notFinal.FinalBlock(); ok {
		s.logger.Debug("GER check 3/4 (final?): no: the insertion block is after the final block",
			append(attrs, "finalBlock", finalBlock)...)
	} else {
		s.logger.Debug("GER check 3/4 (final?): no: the chain is shorter than numBlocks, no block is final yet",
			attrs...)
	}
	return notFinal
}

// checkGERInFinalState checks that, in the state of the final block, AgglayerGER holds the GER with the same parent
// block that was read in the latest block (step 4 of checkGER)
func (s *Service) checkGERInFinalState(ctx context.Context, ger, parentHash common.Hash, finalBlock uint64) error {
	pinned, err := s.l1.BlockByNumber(ctx, finalBlock)
	if err != nil {
		return fmt.Errorf("failed to get the final block %d: %w", finalBlock, err)
	}
	// It fails if the block stopped being canonical meanwhile: an error, not a rejection, so it is asked again
	atFinal, err := s.l1.GlobalExitRootMapAt(ctx, s.cfg.AgglayerGER, ger, pinned.Hash)
	if err != nil {
		return fmt.Errorf("failed to read globalExitRootMap in the final block %d (%s): %w",
			finalBlock, pinned.Hash, err)
	}
	attrs := []any{"ger", ger, "finalBlock", finalBlock, "finalBlockHash", pinned.Hash,
		"parentBlockHash", parentHash, "parentBlockHashAtFinal", atFinal}
	if atFinal != parentHash {
		s.logger.Debug("GER check 4/4 (in the final state?): no: in the state of the final block AgglayerGER "+
			"doesn't hold the GER with the same parent block (an L1 reorg)", attrs...)
		if atFinal == (common.Hash{}) {
			return fmt.Errorf("%w: the GER is not in AgglayerGER in the state of the final block %d (%s): "+
				"the block that inserted it was reorganized", ErrGERBlockNotCanonical, finalBlock, pinned.Hash)
		}
		return fmt.Errorf("%w: in the state of the final block %d (%s) the GER was inserted after block %s, "+
			"not %s: the chain was reorganized", ErrGERBlockNotCanonical, finalBlock, pinned.Hash, atFinal, parentHash)
	}
	s.logger.Debug("GER check 4/4 (in the final state?): yes: in the state of the final block AgglayerGER holds "+
		"the GER with the same parent block", attrs...)
	return nil
}

// isRejection returns true for the errors that say the GER can't be signed (now), as opposed to a failure
// of the validator or of L1
func isRejection(err error) bool {
	return errors.Is(err, ErrGERUnknown) || errors.Is(err, ErrGERBlockNotCanonical) || errors.Is(err, ErrGERNotFinal)
}
