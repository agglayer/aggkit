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
)

// defaultProposerAddressCacheTTL is how long the proposer address is kept before it is
// read again from the multisig committee.
const defaultProposerAddressCacheTTL = time.Minute

// ProposerAddressProvider resolves the address of the aggsender allowed to call the
// validator service.
type ProposerAddressProvider interface {
	GetProposerAddress(ctx context.Context) (common.Address, error)
}

// CommitteeProposerAddressProvider reads the proposer address from the current multisig
// committee: the aggsender that proposes certificates is the first member of the committee.
// The address is cached for a short time so a request does not hit the chain.
type CommitteeProposerAddressProvider struct {
	committeeQuerier types.MultisigQuerier
	ttl              time.Duration
	now              func() time.Time

	mu       sync.Mutex
	cached   common.Address
	cachedAt time.Time
}

// NewCommitteeProposerAddressProvider creates a provider backed by the multisig committee.
func NewCommitteeProposerAddressProvider(
	committeeQuerier types.MultisigQuerier) *CommitteeProposerAddressProvider {
	return &CommitteeProposerAddressProvider{
		committeeQuerier: committeeQuerier,
		ttl:              defaultProposerAddressCacheTTL,
		now:              time.Now,
	}
}

// GetProposerAddress returns the address of the chain's proposer.
func (p *CommitteeProposerAddressProvider) GetProposerAddress(ctx context.Context) (common.Address, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.cachedAt.IsZero() && p.now().Sub(p.cachedAt) < p.ttl {
		return p.cached, nil
	}

	committee, err := p.committeeQuerier.GetMultisigCommittee(ctx, big.NewInt(int64(aggkittypes.Latest)))
	if err != nil {
		return common.Address{}, fmt.Errorf("failed to get the multisig committee: %w", err)
	}
	if committee == nil || len(committee.Signers()) == 0 {
		return common.Address{}, fmt.Errorf("the multisig committee has no signers")
	}

	p.cached = committee.Signers()[0].Address
	p.cachedAt = p.now()

	return p.cached, nil
}
