package force_ger_update

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/0xPolygon/zkevm-ethtx-manager/ethtxmanager"
	ethtxtypes "github.com/0xPolygon/zkevm-ethtx-manager/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// bridgeMessageFuncName is the name of the L1 bridge function used to force a GER update.
const bridgeMessageFuncName = "bridgeMessage"

// defaultResultPollInterval is how often SendForcedGERUpdate polls the ethtxmanager for the
// submitted transaction's status while none of the terminal states has been reached yet.
const defaultResultPollInterval = 2 * time.Second

// forceUpdateGlobalExitRoot is always true for this tool: the whole point of sending this
// bridgeMessage transaction is to force a new L1 info root update.
const forceUpdateGlobalExitRoot = true

// Option configures optional Sender behavior (primarily used by tests to speed up polling).
type Option func(*Sender)

// WithPollInterval overrides the interval used to poll the ethtxmanager for the submitted
// transaction's result. Ignored if interval is not positive.
func WithPollInterval(interval time.Duration) Option {
	return func(s *Sender) {
		if interval > 0 {
			s.pollInterval = interval
		}
	}
}

// Sender implements ForcedUpdateSender: it packs and submits the bridgeMessage transaction with
// forceUpdateGlobalExitRoot = true through the ethtxmanager, and waits for it to reach a terminal
// status before returning.
type Sender struct {
	bridgeAddr         common.Address
	destinationNetwork uint32
	destinationAddress common.Address
	dryRun             bool
	ethTxManager       EthTxManager
	bridgeAbi          *abi.ABI
	pollInterval       time.Duration
}

var _ ForcedUpdateSender = (*Sender)(nil)

// NewSender builds a Sender from the tool configuration and an already-constructed ethtxmanager.
// When cfg.DestinationAddress is the zero address, it defaults to ethTxManager.From().
func NewSender(cfg ForceGERUpdateConfig, ethTxManager EthTxManager, opts ...Option) (*Sender, error) {
	bridgeAbi, err := agglayerbridge.AgglayerbridgeMetaData.GetAbi()
	if err != nil {
		return nil, fmt.Errorf("retrieve agglayerbridge ABI: %w", err)
	}

	destinationAddress := cfg.DestinationAddress
	if destinationAddress == (common.Address{}) {
		destinationAddress = ethTxManager.From()
	}

	s := &Sender{
		bridgeAddr:         cfg.BridgeAddr,
		destinationNetwork: cfg.DestinationNetwork,
		destinationAddress: destinationAddress,
		dryRun:             cfg.DryRun,
		ethTxManager:       ethTxManager,
		bridgeAbi:          bridgeAbi,
		pollInterval:       defaultResultPollInterval,
	}

	for _, opt := range opts {
		opt(s)
	}

	return s, nil
}

// SendForcedGERUpdate submits (or, in DryRun mode, only logs) a bridgeMessage transaction with
// forceUpdateGlobalExitRoot = true, and waits for it to be mined before returning.
func (s *Sender) SendForcedGERUpdate(ctx context.Context) error {
	data, err := s.bridgeAbi.Pack(
		bridgeMessageFuncName,
		s.destinationNetwork,
		s.destinationAddress,
		forceUpdateGlobalExitRoot,
		[]byte{},
	)
	if err != nil {
		return fmt.Errorf("pack %s call: %w", bridgeMessageFuncName, err)
	}

	if s.dryRun {
		log.Infof("force_ger_update dry-run: would send bridgeMessage tx to %s, calldata=0x%x",
			s.bridgeAddr, data)
		return nil
	}

	id, err := s.ethTxManager.Add(ctx, &s.bridgeAddr, common.Big0, data, 0, nil)
	if err == nil {
		log.Infof("forced GER update transaction submitted with ID: %s", id.Hex())
	} else if !errors.Is(err, ethtxmanager.ErrAlreadyExists) {
		return fmt.Errorf("add forced GER update transaction: %w", err)
	}
	if err != nil {
		log.Infof("forced GER update transaction already exists in monitoring DB with ID: %s", id.Hex())
	}

	return s.waitForResult(ctx, id)
}

// waitForResult polls the ethtxmanager for id's status until a terminal state is reached:
// Mined/Safe/Finalized succeed, Failed returns an error, and Evicted is logged and returns nil
// (mirrors aggoracle/chaingersender/evm.go's submitTransaction monitoring loop).
func (s *Sender) waitForResult(ctx context.Context, id common.Hash) error {
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Infof("context cancelled while waiting for forced GER update tx %s", id.Hex())
			return nil

		case <-ticker.C:
			res, err := s.ethTxManager.Result(ctx, id)
			if err != nil {
				return fmt.Errorf("check forced GER update transaction %s status: %w", id.Hex(), err)
			}

			switch res.Status {
			case ethtxtypes.MonitoredTxStatusCreated,
				ethtxtypes.MonitoredTxStatusSent:
				continue
			case ethtxtypes.MonitoredTxStatusFailed:
				return fmt.Errorf("forced GER update tx %s failed", id.Hex())
			case ethtxtypes.MonitoredTxStatusEvicted:
				log.Infof("forced GER update tx %s was evicted", id.Hex())
				return nil
			case ethtxtypes.MonitoredTxStatusMined,
				ethtxtypes.MonitoredTxStatusSafe,
				ethtxtypes.MonitoredTxStatusFinalized:
				log.Infof("forced GER update tx %s was successfully mined at block %d", id.Hex(), res.MinedAtBlockNumber)
				return nil
			default:
				log.Errorf("unexpected forced GER update tx status: %s", res.Status)
			}
		}
	}
}
