package chaingersender

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/agglayer/aggkit/aggoracle/mocks"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// When the context is cancelled and the ticker fires, both cases of the select are ready and Go picks one at
// random. The cancellation must always win: the transaction manager is never asked for the result of the
// transaction with a cancelled context, and the submitter returns nil as documented. A single run could pass by
// chance, so it is repeated: with the random choice each run would fail with a probability of one half.
func TestTxSubmitter_CancellationWinsOverTheTicker(t *testing.T) {
	to := common.HexToAddress("0x1")
	txID := common.HexToHash("0xabc1")

	for i := 0; i < 100; i++ {
		ethTxMan := mocks.NewEthTxManager(t)
		submitter := &txSubmitter{
			logger:              log.GetDefaultLogger(),
			ethTxMan:            ethTxMan,
			waitPeriodMonitorTx: time.Nanosecond,
		}
		ctx, cancel := context.WithCancel(context.Background())

		// The transaction is added and the context is cancelled: the ticker (1ns) has fired by the time the
		// monitoring loop starts. There is no expectation for Result: calling it fails the test
		ethTxMan.EXPECT().Add(mock.Anything, &to, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			RunAndReturn(func(context.Context, *common.Address, *big.Int, []byte, uint64,
				*gethtypes.BlobTxSidecar) (common.Hash, error) {
				cancel()
				time.Sleep(time.Millisecond)
				return txID, nil
			}).Once()

		require.NoError(t, submitter.submitAndWait(ctx, &to, []byte{0x01}, "inject", common.Hash{}))
		cancel()
	}
}
