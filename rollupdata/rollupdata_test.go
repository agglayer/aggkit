package rollupdata

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient/simulated"
	"github.com/stretchr/testify/require"

	"github.com/agglayer/aggkit/etherman"
	"github.com/agglayer/aggkit/test/contracts/rollupmanagermock"
	"github.com/agglayer/aggkit/test/contracts/sequencerurlrollupmock"
	"github.com/agglayer/aggkit/test/helpers"
)

const simulatedChainID = 1337

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	require.ErrorIs(t, Config{}.Validate(), ErrMissingRollupManagerAddress)
	require.ErrorIs(
		t,
		Config{RollupManagerAddr: common.HexToAddress("0x1"), UpdateBufferSize: -1}.Validate(),
		ErrInvalidUpdateBufferSize,
	)
	require.NoError(t, Config{RollupManagerAddr: common.HexToAddress("0x1")}.Validate())
}

func TestGetSequencerURLsAndSubscribeReturnsInitialURLsAndUpdates(t *testing.T) {
	t.Parallel()

	client, userAuth, managerAddr, manager := newSimulatedRollupManager(t)
	rollupOne := deployAndAttachRollup(t, client, userAuth, manager, 1, "http://sequencer-one")
	deployAndAttachRollup(t, client, userAuth, manager, 2, "http://sequencer-two")

	rollupData := newRollupData(t, client, managerAddr)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	initialURLs, updates, err := rollupData.GetSequencerURLsAndSubscribe(ctx)
	require.NoError(t, helpers.ExtractRPCErrorData(err))
	require.Equal(t, map[uint32]string{
		1: "http://sequencer-one",
		2: "http://sequencer-two",
	}, initialURLs)

	rollupOne.setTrustedSequencerURL(t, userAuth, "http://sequencer-one-new")
	client.Commit()

	update := requireUpdate(t, updates)
	require.Equal(t, uint32(1), update.RollupID)
	require.Equal(t, rollupOne.address, update.RollupAddress)
	require.Equal(t, "http://sequencer-one-new", update.SequencerURL)
	require.False(t, update.NewRollup)
	require.NotZero(t, update.BlockNumber)

	cancel()
	requireChannelClosed(t, updates)
}

func TestGetSequencerURLsAndSubscribeSendsNewRollupsAndSubscribesToThem(t *testing.T) {
	t.Parallel()

	client, userAuth, managerAddr, manager := newSimulatedRollupManager(t)
	deployAndAttachRollup(t, client, userAuth, manager, 1, "http://sequencer-one")

	rollupData := newRollupData(t, client, managerAddr)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	initialURLs, updates, err := rollupData.GetSequencerURLsAndSubscribe(ctx)
	require.NoError(t, err)
	require.Equal(t, map[uint32]string{1: "http://sequencer-one"}, initialURLs)

	rollupTwo := deployAndAttachRollup(t, client, userAuth, manager, 2, "http://sequencer-two")

	newRollupUpdate := requireUpdate(t, updates)
	require.Equal(t, uint32(2), newRollupUpdate.RollupID)
	require.Equal(t, rollupTwo.address, newRollupUpdate.RollupAddress)
	require.Equal(t, "http://sequencer-two", newRollupUpdate.SequencerURL)
	require.True(t, newRollupUpdate.NewRollup)

	rollupTwo.setTrustedSequencerURL(t, userAuth, "http://sequencer-two-new")
	client.Commit()

	urlUpdate := requireUpdate(t, updates)
	require.Equal(t, uint32(2), urlUpdate.RollupID)
	require.Equal(t, rollupTwo.address, urlUpdate.RollupAddress)
	require.Equal(t, "http://sequencer-two-new", urlUpdate.SequencerURL)
	require.False(t, urlUpdate.NewRollup)
}

type deployedRollup struct {
	address  common.Address
	contract *sequencerurlrollupmock.Sequencerurlrollupmock
}

func (r deployedRollup) setTrustedSequencerURL(t *testing.T, auth *bind.TransactOpts, sequencerURL string) {
	t.Helper()

	_, err := r.contract.SetTrustedSequencerURL(auth, sequencerURL)
	require.NoError(t, err)
}

func newSimulatedRollupManager(
	t *testing.T,
) (*simulated.Backend, *bind.TransactOpts, common.Address, *rollupmanagermock.Rollupmanagermock) {
	t.Helper()

	deployerAuth, err := helpers.CreateAccount(big.NewInt(simulatedChainID))
	require.NoError(t, err)

	client, setup := helpers.NewSimulatedBackend(t, nil, deployerAuth)

	managerAddr, _, manager, err := rollupmanagermock.DeployRollupmanagermock(
		setup.UserAuth,
		client.Client(),
	)
	require.NoError(t, err)
	client.Commit()

	return client, setup.UserAuth, managerAddr, manager
}

func deployAndAttachRollup(
	t *testing.T,
	client *simulated.Backend,
	userAuth *bind.TransactOpts,
	manager *rollupmanagermock.Rollupmanagermock,
	chainID uint64,
	sequencerURL string,
) deployedRollup {
	t.Helper()

	rollupAddr, _, rollup, err := sequencerurlrollupmock.DeploySequencerurlrollupmock(
		userAuth,
		client.Client(),
		sequencerURL,
	)
	require.NoError(t, err)
	client.Commit()

	_, err = manager.AddExistingRollup(
		userAuth,
		rollupAddr,
		common.HexToAddress("0x4000"),
		1,
		chainID,
		[32]byte{},
		0,
		[32]byte{},
		[32]byte{},
	)
	require.NoError(t, err)
	client.Commit()

	return deployedRollup{address: rollupAddr, contract: rollup}
}

func newRollupData(t *testing.T, client *simulated.Backend, managerAddr common.Address) *RollupData {
	t.Helper()

	rollupData, err := New(
		Config{RollupManagerAddr: managerAddr},
		etherman.NewDefaultEthClient(client.Client(), nil, nil),
	)
	require.NoError(t, err)

	return rollupData
}

func requireUpdate(t *testing.T, updates <-chan SequencerURLUpdate) SequencerURLUpdate {
	t.Helper()

	select {
	case update, ok := <-updates:
		require.True(t, ok)
		return update
	case <-time.After(5 * time.Second):
		require.FailNow(t, "timed out waiting for sequencer URL update")
	}

	return SequencerURLUpdate{}
}

func requireChannelClosed(t *testing.T, updates <-chan SequencerURLUpdate) {
	t.Helper()

	select {
	case _, ok := <-updates:
		require.False(t, ok)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "timed out waiting for update channel to close")
	}
}

func TestNewRejectsNilEthClient(t *testing.T) {
	t.Parallel()

	_, err := New(Config{RollupManagerAddr: common.HexToAddress("0x1")}, nil)
	require.ErrorIs(t, err, ErrNilEthereumClient)
}
