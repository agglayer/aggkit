package validator

import (
	"errors"
	"testing"
	"time"

	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/ethereum/go-ethereum/common"
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
}
