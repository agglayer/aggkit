package bridgesync

import (
	"context"
	"testing"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	"github.com/agglayer/aggkit/types/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestBridgeEventAppender_CancelWhileRPCStalls checks that the bridge event appender resolves the
// transaction with the active download context: an eth_getTransactionByHash that stalls until its
// context is canceled must return once the download generation is canceled (the driver joins the
// downloader before a rollback, so an appender that ignored the context would block the reorg forever).
func TestBridgeEventAppender_CancelWhileRPCStalls(t *testing.T) {
	f := newMultiCallerFixture(t)

	reached := make(chan struct{})
	ethClient := mocks.NewEthClienter(t)
	ethClient.EXPECT().CallContext(mock.Anything, mock.Anything, GetTransactionByHashEndpoint, mock.Anything).
		RunAndReturn(func(ctx context.Context, _ any, _ string, _ ...any) error {
			close(reached)
			<-ctx.Done()
			return ctx.Err()
		}).Once()

	contract, err := agglayerbridge.NewAgglayerbridge(f.bridgeAddr, ethClient)
	require.NoError(t, err)
	appender := buildBridgeEventAppender(contract, f.bridgeAddr, ethClient, true, log.WithFields("module", "test"))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- appender(ctx, &sync.EVMBlock{EVMBlockHeader: sync.EVMBlockHeader{Num: 1}},
			f.bridgeEventLog(t, bridgeLeafTypeAsset, f.token, 99, 3))
	}()

	<-reached
	cancel()
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("the bridge event appender did not return after the download context was canceled")
	}
}
