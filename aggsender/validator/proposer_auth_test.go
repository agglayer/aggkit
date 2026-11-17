package validator

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newTestCommittee(t *testing.T, addresses ...common.Address) *types.MultisigCommittee {
	t.Helper()

	signers := make([]*types.SignerInfo, 0, len(addresses))
	for _, address := range addresses {
		signers = append(signers, types.NewSignerInfo("http://validator", address))
	}

	committee, err := types.NewMultisigCommittee(signers, 1)
	require.NoError(t, err)

	return committee
}

func TestCommitteeProposerAddressProvider(t *testing.T) {
	t.Parallel()

	proposer := common.HexToAddress("0x1111111111111111111111111111111111111111")
	second := common.HexToAddress("0x2222222222222222222222222222222222222222")

	t.Run("returns the first member of the committee", func(t *testing.T) {
		t.Parallel()

		querier := mocks.NewMultisigQuerier(t)
		querier.EXPECT().GetMultisigCommittee(mock.Anything, mock.Anything).
			Return(newTestCommittee(t, proposer, second), nil).Once()

		provider := NewCommitteeProposerAddressProvider(querier)
		address, err := provider.GetProposerAddress(t.Context())

		require.NoError(t, err)
		require.Equal(t, proposer, address)
	})

	t.Run("caches the address", func(t *testing.T) {
		t.Parallel()

		querier := mocks.NewMultisigQuerier(t)
		querier.EXPECT().GetMultisigCommittee(mock.Anything, mock.Anything).
			Return(newTestCommittee(t, proposer, second), nil).Once()

		provider := NewCommitteeProposerAddressProvider(querier)
		for i := 0; i < 5; i++ {
			address, err := provider.GetProposerAddress(t.Context())
			require.NoError(t, err)
			require.Equal(t, proposer, address)
		}
	})

	t.Run("reads the committee again after the cache expires", func(t *testing.T) {
		t.Parallel()

		querier := mocks.NewMultisigQuerier(t)
		querier.EXPECT().GetMultisigCommittee(mock.Anything, mock.Anything).
			Return(newTestCommittee(t, proposer, second), nil).Once()
		querier.EXPECT().GetMultisigCommittee(mock.Anything, mock.Anything).
			Return(newTestCommittee(t, second, proposer), nil).Once()

		provider := NewCommitteeProposerAddressProvider(querier)
		provider.ttl = time.Minute
		now := time.Now()
		provider.now = func() time.Time { return now }

		address, err := provider.GetProposerAddress(t.Context())
		require.NoError(t, err)
		require.Equal(t, proposer, address)

		now = now.Add(provider.ttl + time.Second)
		address, err = provider.GetProposerAddress(t.Context())
		require.NoError(t, err)
		require.Equal(t, second, address)
	})

	t.Run("fails when the committee cannot be read", func(t *testing.T) {
		t.Parallel()

		querier := mocks.NewMultisigQuerier(t)
		querier.EXPECT().GetMultisigCommittee(mock.Anything, mock.Anything).
			Return(nil, errors.New("l1 down")).Once()

		provider := NewCommitteeProposerAddressProvider(querier)
		_, err := provider.GetProposerAddress(t.Context())

		require.ErrorContains(t, err, "l1 down")
	})

	t.Run("remembers a failed lookup for a short time", func(t *testing.T) {
		t.Parallel()

		querier := mocks.NewMultisigQuerier(t)
		querier.EXPECT().GetMultisigCommittee(mock.Anything, mock.Anything).
			Return(nil, errors.New("l1 down")).Once()
		querier.EXPECT().GetMultisigCommittee(mock.Anything, mock.Anything).
			Return(newTestCommittee(t, proposer, second), nil).Once()

		provider := NewCommitteeProposerAddressProvider(querier)
		now := time.Now()
		provider.now = func() time.Time { return now }

		for i := 0; i < 3; i++ {
			_, err := provider.GetProposerAddress(t.Context())
			require.ErrorContains(t, err, "l1 down")
		}

		now = now.Add(provider.errTTL + time.Second)
		address, err := provider.GetProposerAddress(t.Context())
		require.NoError(t, err)
		require.Equal(t, proposer, address)
	})

	t.Run("concurrent callers share one lookup", func(t *testing.T) {
		t.Parallel()

		release := make(chan time.Time)
		querier := mocks.NewMultisigQuerier(t)
		querier.EXPECT().GetMultisigCommittee(mock.Anything, mock.Anything).
			WaitUntil(release).
			Return(newTestCommittee(t, proposer, second), nil).Once()

		provider := NewCommitteeProposerAddressProvider(querier)

		const callers = 8
		var wg sync.WaitGroup
		results := make(chan common.Address, callers)
		for i := 0; i < callers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				address, err := provider.GetProposerAddress(t.Context())
				assert.NoError(t, err)
				results <- address
			}()
		}

		time.Sleep(50 * time.Millisecond)
		close(release)
		wg.Wait()
		close(results)
		for address := range results {
			require.Equal(t, proposer, address)
		}
	})

	t.Run("a caller stops waiting when its context is done", func(t *testing.T) {
		t.Parallel()

		release := make(chan time.Time)
		defer close(release)
		querier := mocks.NewMultisigQuerier(t)
		querier.EXPECT().GetMultisigCommittee(mock.Anything, mock.Anything).
			WaitUntil(release).
			Return(newTestCommittee(t, proposer, second), nil).Maybe()

		provider := NewCommitteeProposerAddressProvider(querier)
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		defer cancel()

		_, err := provider.GetProposerAddress(ctx)
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})
}
