package chaingersender

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/0xPolygon/zkevm-ethtx-manager/ethtxmanager"
	ethtxtypes "github.com/0xPolygon/zkevm-ethtx-manager/types"
	"github.com/agglayer/aggkit/aggoracle/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
)

// txSubmitter sends a transaction through the EthTxManager and monitors it until it is mined.
// It is shared by all the GER submission modes.
type txSubmitter struct {
	logger              *log.Logger
	ethTxMan            types.EthTxManager
	gasOffset           uint64
	waitPeriodMonitorTx time.Duration
}

// submitAndWait adds the transaction (to, data) to the transaction manager and waits until it is mined.
// action ("inject", "propose"...) and ger are only used to log. If the transaction already exists in the
// monitoring DB, the existing one is monitored. It returns nil if the context is cancelled or the tx is evicted.
func (s *txSubmitter) submitAndWait(
	ctx context.Context,
	to *common.Address,
	data []byte,
	action string,
	ger common.Hash,
) error {
	ticker := time.NewTicker(s.waitPeriodMonitorTx)
	defer ticker.Stop()

	// Add the transaction to the transaction manager
	id, err := s.ethTxMan.Add(ctx, to, common.Big0, data, s.gasOffset, nil)
	if err == nil {
		s.logger.Infof("%s GER transaction submitted with ID: %s. GER: %s", action, id.Hex(), ger.Hex())
	} else if !errors.Is(err, ethtxmanager.ErrAlreadyExists) {
		return fmt.Errorf("failed to add %s GER transaction: %w", action, err)
	}
	if err != nil {
		s.logger.Infof("%s GER transaction already exists in monitoring DB with ID: %s. GER: %s", action, id.Hex(), ger.Hex())
	}

	s.logger.Debugf("monitoring every %s", s.waitPeriodMonitorTx)

	// Monitor the transaction status
	for {
		select {
		case <-ctx.Done():
			s.logger.Infof("context cancelled handled in %s for tx %s", action, id.Hex())
			return nil

		case <-ticker.C:
			// If the context was cancelled when the ticker fired, both cases are ready and select picks one at
			// random: the cancellation always wins, so it is always handled the same way
			if ctx.Err() != nil {
				s.logger.Infof("context cancelled handled in %s for tx %s", action, id.Hex())
				return nil
			}
			s.logger.Debugf("waiting for %s GER tx %s to be mined", action, id.Hex())
			res, err := s.ethTxMan.Result(ctx, id)
			if err != nil {
				s.logger.Errorf("failed to check the %s GER transaction %s status: %s", action, id.Hex(), err)
				return err
			}

			switch res.Status {
			case ethtxtypes.MonitoredTxStatusCreated,
				ethtxtypes.MonitoredTxStatusSent:
				continue
			case ethtxtypes.MonitoredTxStatusFailed:
				return fmt.Errorf("%s GER tx %s failed", action, id.Hex())
			case ethtxtypes.MonitoredTxStatusEvicted:
				s.logger.Debugf("%s GER tx %s was evicted", action, id.Hex())
				return nil
			case ethtxtypes.MonitoredTxStatusMined,
				ethtxtypes.MonitoredTxStatusSafe,
				ethtxtypes.MonitoredTxStatusFinalized:
				s.logger.Debugf("%s GER tx %s was successfully mined at block %d", action, id.Hex(), res.MinedAtBlockNumber)
				return nil
			default:
				s.logger.Error("unexpected tx status:", res.Status)
			}
		}
	}
}
