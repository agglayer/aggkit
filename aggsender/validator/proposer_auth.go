package validator

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/agglayer/aggkit/aggsender/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"golang.org/x/sync/singleflight"
)

const (
	// defaultProposerAddressCacheTTL is how long the proposer address is kept before it is
	// read again from the multisig committee. After a committee rotation the previous
	// proposer stays accepted for up to this long.
	defaultProposerAddressCacheTTL = time.Minute
	// defaultProposerAddressErrorTTL is how long a failed committee lookup is remembered, so
	// requests that arrive while the chain cannot be read do not each query it again.
	defaultProposerAddressErrorTTL = 5 * time.Second
)

// ProposerAddressProvider resolves the address of the aggsender allowed to call the
// validator service.
type ProposerAddressProvider interface {
	GetProposerAddress(ctx context.Context) (common.Address, error)
}

// CommitteeProposerAddressProvider reads the proposer address from the current multisig
// committee: the aggsender that proposes certificates is the first member of the committee.
// The address is cached for a short time so a request does not hit the chain, and a failed
// lookup is cached for a shorter time so a burst of requests does not repeat it.
type CommitteeProposerAddressProvider struct {
	committeeQuerier types.MultisigQuerier
	ttl              time.Duration
	errTTL           time.Duration
	now              func() time.Time

	group singleflight.Group

	mu       sync.Mutex
	cached   common.Address
	cachedAt time.Time
	err      error
	errAt    time.Time
}

// NewCommitteeProposerAddressProvider creates a provider backed by the multisig committee.
func NewCommitteeProposerAddressProvider(
	committeeQuerier types.MultisigQuerier) *CommitteeProposerAddressProvider {
	return &CommitteeProposerAddressProvider{
		committeeQuerier: committeeQuerier,
		ttl:              defaultProposerAddressCacheTTL,
		errTTL:           defaultProposerAddressErrorTTL,
		now:              time.Now,
	}
}

// GetProposerAddress returns the address of the chain's proposer. Concurrent callers share
// one committee lookup, and each caller stops waiting when its own context is done.
func (p *CommitteeProposerAddressProvider) GetProposerAddress(ctx context.Context) (common.Address, error) {
	if addr, err, ok := p.fromCache(); ok {
		return addr, err
	}

	resultCh := p.group.DoChan("proposer", func() (interface{}, error) {
		// The lookup is shared, so it must not be cancelled by the first caller's context.
		addr, err := p.lookup(context.WithoutCancel(ctx))
		p.store(addr, err)
		return addr, err
	})

	select {
	case <-ctx.Done():
		return common.Address{}, ctx.Err()
	case res := <-resultCh:
		if res.Err != nil {
			return common.Address{}, res.Err
		}
		addr, ok := res.Val.(common.Address)
		if !ok {
			return common.Address{}, fmt.Errorf("unexpected proposer address type %T", res.Val)
		}
		return addr, nil
	}
}

func (p *CommitteeProposerAddressProvider) fromCache() (common.Address, error, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := p.now()
	if !p.cachedAt.IsZero() && now.Sub(p.cachedAt) < p.ttl {
		return p.cached, nil, true
	}
	if p.err != nil && now.Sub(p.errAt) < p.errTTL {
		return common.Address{}, p.err, true
	}

	return common.Address{}, nil, false
}

func (p *CommitteeProposerAddressProvider) store(addr common.Address, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err != nil {
		p.err = err
		p.errAt = p.now()
		return
	}

	p.cached = addr
	p.cachedAt = p.now()
	p.err = nil
}

func (p *CommitteeProposerAddressProvider) lookup(ctx context.Context) (common.Address, error) {
	committee, err := p.committeeQuerier.GetMultisigCommittee(ctx, big.NewInt(int64(aggkittypes.Latest)))
	if err != nil {
		return common.Address{}, fmt.Errorf("failed to get the multisig committee: %w", err)
	}
	if committee == nil || len(committee.Signers()) == 0 {
		return common.Address{}, fmt.Errorf("the multisig committee has no signers")
	}

	return committee.Signers()[0].Address, nil
}
