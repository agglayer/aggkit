package rollupdata

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayermanager"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/polygonrollupbaseetrog"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/event"

	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
)

// SequencerURLUpdate contains an on-chain sequencer URL update for a rollup.
type SequencerURLUpdate struct {
	RollupID      uint32
	RollupAddress common.Address
	SequencerURL  string
	BlockNumber   uint64
	NewRollup     bool
}

// RollupData queries rollup data from the rollup manager.
type RollupData struct {
	cfg           Config
	ethClient     aggkittypes.BaseEthereumClienter
	rollupManager *agglayermanager.Agglayermanager
}

type rollupInfo struct {
	rollupID      uint32
	rollupAddress common.Address
	sequencerURL  string
}

type rollupContract interface {
	TrustedSequencerURL(opts *bind.CallOpts) (string, error)
	WatchSetTrustedSequencerURL(
		opts *bind.WatchOpts,
		sink chan<- *polygonrollupbaseetrog.PolygonrollupbaseetrogSetTrustedSequencerURL,
	) (event.Subscription, error)
}

// New creates a rollup data querier.
func New(cfg Config, ethClient aggkittypes.BaseEthereumClienter) (*RollupData, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if ethClient == nil {
		return nil, ErrNilEthereumClient
	}

	rollupManager, err := agglayermanager.NewAgglayermanager(cfg.RollupManagerAddr, ethClient)
	if err != nil {
		return nil, fmt.Errorf("create rollup manager binding: %w", err)
	}

	return &RollupData{
		cfg:           cfg,
		ethClient:     ethClient,
		rollupManager: rollupManager,
	}, nil
}

// GetSequencerURLsAndSubscribe returns the current sequencer URLs by rollup ID and a channel for future updates.
func (r *RollupData) GetSequencerURLsAndSubscribe(
	ctx context.Context,
) (map[uint32]string, <-chan SequencerURLUpdate, error) {
	snapshotBlock, err := r.ethClient.BlockNumber(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("get snapshot block number: %w", err)
	}

	rollups, err := r.fetchRollups(ctx, snapshotBlock)
	if err != nil {
		return nil, nil, err
	}

	initialURLs := make(map[uint32]string, len(rollups))
	for _, rollup := range rollups {
		initialURLs[rollup.rollupID] = rollup.sequencerURL
	}

	watcher := newSequencerURLWatcher(
		ctx,
		r.ethClient,
		r.rollupManager,
		snapshotBlock,
		r.cfg.updateBufferSize(),
		rollups,
	)
	if err := watcher.start(); err != nil {
		return nil, nil, err
	}

	return initialURLs, watcher.updates, nil
}

func (r *RollupData) fetchRollups(ctx context.Context, blockNumber uint64) ([]rollupInfo, error) {
	callOpts := &bind.CallOpts{
		Context:     ctx,
		BlockNumber: newBlockNumber(blockNumber),
	}
	rollupCount, err := r.rollupManager.RollupCount(callOpts)
	if err != nil {
		return nil, fmt.Errorf("get rollup count at block %d: %w", blockNumber, err)
	}

	rollups := make([]rollupInfo, 0, rollupCount)
	for rollupID := uint32(1); rollupID <= rollupCount; rollupID++ {
		rollupData, err := r.rollupManager.RollupIDToRollupData(callOpts, rollupID)
		if err != nil {
			return nil, fmt.Errorf("get rollup %d data at block %d: %w", rollupID, blockNumber, err)
		}
		if rollupData.RollupContract == (common.Address{}) {
			continue
		}

		rollupContract, err := newRollupContract(rollupData.RollupContract, r.ethClient)
		if err != nil {
			return nil, fmt.Errorf("create rollup %d binding: %w", rollupID, err)
		}
		sequencerURL, err := rollupContract.TrustedSequencerURL(callOpts)
		if err != nil {
			return nil, fmt.Errorf("get rollup %d sequencer URL at block %d: %w", rollupID, blockNumber, err)
		}

		rollups = append(rollups, rollupInfo{
			rollupID:      rollupID,
			rollupAddress: rollupData.RollupContract,
			sequencerURL:  sequencerURL,
		})
	}

	return rollups, nil
}

func newRollupContract(addr common.Address, ethClient aggkittypes.BaseEthereumClienter) (rollupContract, error) {
	return polygonrollupbaseetrog.NewPolygonrollupbaseetrog(addr, ethClient)
}

func newBlockNumber(blockNumber uint64) *big.Int {
	return new(big.Int).SetUint64(blockNumber)
}

type sequencerURLWatcher struct {
	ctx           context.Context
	cancel        context.CancelFunc
	ethClient     aggkittypes.BaseEthereumClienter
	rollupManager *agglayermanager.Agglayermanager
	startBlock    uint64
	updates       chan SequencerURLUpdate

	wg sync.WaitGroup

	mu            sync.Mutex
	subscriptions []event.Subscription
	seenRollups   map[uint32]struct{}
}

func newSequencerURLWatcher(
	parentCtx context.Context,
	ethClient aggkittypes.BaseEthereumClienter,
	rollupManager *agglayermanager.Agglayermanager,
	startBlock uint64,
	bufferSize int,
	initialRollups []rollupInfo,
) *sequencerURLWatcher {
	ctx, cancel := context.WithCancel(parentCtx)
	watcher := &sequencerURLWatcher{
		ctx:           ctx,
		cancel:        cancel,
		ethClient:     ethClient,
		rollupManager: rollupManager,
		startBlock:    startBlock,
		updates:       make(chan SequencerURLUpdate, bufferSize),
		seenRollups:   make(map[uint32]struct{}, len(initialRollups)),
	}

	for _, rollup := range initialRollups {
		watcher.seenRollups[rollup.rollupID] = struct{}{}
	}

	return watcher
}

func (w *sequencerURLWatcher) start() error {
	for _, rollupID := range w.initialRollupIDs() {
		rollupData, err := w.rollupManager.RollupIDToRollupData(
			&bind.CallOpts{Context: w.ctx, BlockNumber: newBlockNumber(w.startBlock)},
			rollupID,
		)
		if err != nil {
			w.shutdown()
			return fmt.Errorf("get initial rollup %d data: %w", rollupID, err)
		}
		if err := w.startRollupWatcher(rollupID, rollupData.RollupContract, w.startBlock); err != nil {
			w.shutdown()
			return err
		}
	}

	if err := w.startAddExistingRollupWatcher(); err != nil {
		w.shutdown()
		return err
	}
	if err := w.startCreateNewRollupWatcher(); err != nil {
		w.shutdown()
		return err
	}
	if err := w.startCreateNewAggchainWatcher(); err != nil {
		w.shutdown()
		return err
	}

	go w.closeOnDone()

	return nil
}

func (w *sequencerURLWatcher) initialRollupIDs() []uint32 {
	w.mu.Lock()
	defer w.mu.Unlock()

	rollupIDs := make([]uint32, 0, len(w.seenRollups))
	for rollupID := range w.seenRollups {
		rollupIDs = append(rollupIDs, rollupID)
	}

	return rollupIDs
}

func (w *sequencerURLWatcher) closeOnDone() {
	<-w.ctx.Done()
	w.unsubscribeAll()
	w.wg.Wait()
	close(w.updates)
}

func (w *sequencerURLWatcher) shutdown() {
	w.cancel()
	w.unsubscribeAll()
	w.wg.Wait()
	close(w.updates)
}

func (w *sequencerURLWatcher) addSubscription(subscription event.Subscription) bool {
	select {
	case <-w.ctx.Done():
		subscription.Unsubscribe()
		return false
	default:
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	select {
	case <-w.ctx.Done():
		subscription.Unsubscribe()
		return false
	default:
		w.subscriptions = append(w.subscriptions, subscription)
		return true
	}
}

func (w *sequencerURLWatcher) unsubscribeAll() {
	w.mu.Lock()
	defer w.mu.Unlock()

	for _, subscription := range w.subscriptions {
		subscription.Unsubscribe()
	}
	w.subscriptions = nil
}

func (w *sequencerURLWatcher) signalError(err error) {
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Errorf("rollupdata subscription error: %v", err)
	}
	w.cancel()
}

func (w *sequencerURLWatcher) markSeen(rollupID uint32) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, ok := w.seenRollups[rollupID]; ok {
		return false
	}

	w.seenRollups[rollupID] = struct{}{}
	return true
}

func (w *sequencerURLWatcher) startRollupWatcher(
	rollupID uint32,
	rollupAddress common.Address,
	ignoreThroughBlock uint64,
) error {
	if rollupAddress == (common.Address{}) {
		return nil
	}

	rollupContract, err := newRollupContract(rollupAddress, w.ethClient)
	if err != nil {
		return fmt.Errorf("create rollup %d binding: %w", rollupID, err)
	}

	events := make(chan *polygonrollupbaseetrog.PolygonrollupbaseetrogSetTrustedSequencerURL, 1)
	subscription, err := rollupContract.WatchSetTrustedSequencerURL(
		&bind.WatchOpts{Context: w.ctx, Start: &w.startBlock},
		events,
	)
	if err != nil {
		return fmt.Errorf("subscribe rollup %d sequencer URL updates: %w", rollupID, err)
	}
	if !w.addSubscription(subscription) {
		return nil
	}

	w.wg.Add(1)
	go func() {
		defer w.wg.Done()

		for {
			select {
			case err := <-subscription.Err():
				w.signalError(err)
				return
			case eventData, ok := <-events:
				if !ok {
					return
				}
				if eventData.Raw.BlockNumber <= ignoreThroughBlock {
					continue
				}
				w.sendUpdate(SequencerURLUpdate{
					RollupID:      rollupID,
					RollupAddress: rollupAddress,
					SequencerURL:  eventData.NewTrustedSequencerURL,
					BlockNumber:   eventData.Raw.BlockNumber,
				})
			case <-w.ctx.Done():
				return
			}
		}
	}()

	return nil
}

func (w *sequencerURLWatcher) sendUpdate(update SequencerURLUpdate) {
	select {
	case w.updates <- update:
	case <-w.ctx.Done():
	}
}

func (w *sequencerURLWatcher) handleNewRollup(rollupID uint32, rollupAddress common.Address, eventBlock uint64) {
	if eventBlock <= w.startBlock {
		return
	}
	if !w.markSeen(rollupID) {
		return
	}

	rollupContract, err := newRollupContract(rollupAddress, w.ethClient)
	if err != nil {
		w.signalError(fmt.Errorf("create rollup %d binding: %w", rollupID, err))
		return
	}

	sequencerURL, err := rollupContract.TrustedSequencerURL(&bind.CallOpts{
		Context:     w.ctx,
		BlockNumber: newBlockNumber(eventBlock),
	})
	if err != nil {
		w.signalError(fmt.Errorf("get new rollup %d sequencer URL at block %d: %w", rollupID, eventBlock, err))
		return
	}

	w.sendUpdate(SequencerURLUpdate{
		RollupID:      rollupID,
		RollupAddress: rollupAddress,
		SequencerURL:  sequencerURL,
		BlockNumber:   eventBlock,
		NewRollup:     true,
	})

	if err := w.startRollupWatcher(rollupID, rollupAddress, eventBlock); err != nil {
		w.signalError(err)
	}
}

func (w *sequencerURLWatcher) startAddExistingRollupWatcher() error {
	events := make(chan *agglayermanager.AgglayermanagerAddExistingRollup, 1)
	subscription, err := w.rollupManager.WatchAddExistingRollup(
		&bind.WatchOpts{Context: w.ctx, Start: &w.startBlock},
		events,
		nil,
	)
	if err != nil {
		return fmt.Errorf("subscribe add existing rollup events: %w", err)
	}
	if !w.addSubscription(subscription) {
		return nil
	}

	w.wg.Add(1)
	go func() {
		defer w.wg.Done()

		for {
			select {
			case err := <-subscription.Err():
				w.signalError(err)
				return
			case eventData, ok := <-events:
				if !ok {
					return
				}
				w.handleNewRollup(eventData.RollupID, eventData.RollupAddress, eventData.Raw.BlockNumber)
			case <-w.ctx.Done():
				return
			}
		}
	}()

	return nil
}

func (w *sequencerURLWatcher) startCreateNewRollupWatcher() error {
	events := make(chan *agglayermanager.AgglayermanagerCreateNewRollup, 1)
	subscription, err := w.rollupManager.WatchCreateNewRollup(
		&bind.WatchOpts{Context: w.ctx, Start: &w.startBlock},
		events,
		nil,
	)
	if err != nil {
		return fmt.Errorf("subscribe create new rollup events: %w", err)
	}
	if !w.addSubscription(subscription) {
		return nil
	}

	w.wg.Add(1)
	go func() {
		defer w.wg.Done()

		for {
			select {
			case err := <-subscription.Err():
				w.signalError(err)
				return
			case eventData, ok := <-events:
				if !ok {
					return
				}
				w.handleNewRollup(eventData.RollupID, eventData.RollupAddress, eventData.Raw.BlockNumber)
			case <-w.ctx.Done():
				return
			}
		}
	}()

	return nil
}

func (w *sequencerURLWatcher) startCreateNewAggchainWatcher() error {
	events := make(chan *agglayermanager.AgglayermanagerCreateNewAggchain, 1)
	subscription, err := w.rollupManager.WatchCreateNewAggchain(
		&bind.WatchOpts{Context: w.ctx, Start: &w.startBlock},
		events,
		nil,
	)
	if err != nil {
		return fmt.Errorf("subscribe create new aggchain events: %w", err)
	}
	if !w.addSubscription(subscription) {
		return nil
	}

	w.wg.Add(1)
	go func() {
		defer w.wg.Done()

		for {
			select {
			case err := <-subscription.Err():
				w.signalError(err)
				return
			case eventData, ok := <-events:
				if !ok {
					return
				}
				w.handleNewRollup(eventData.RollupID, eventData.RollupAddress, eventData.Raw.BlockNumber)
			case <-w.ctx.Done():
				return
			}
		}
	}()

	return nil
}
