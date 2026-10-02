package bridgesync

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/agglayer/aggkit/log"
	mocksethclient "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestGetContractDepositCount_HonoursContextDeadline asserts that GetContractDepositCount
// bounds the underlying eth_call by the caller's context, instead of blocking indefinitely
// on a hung L1 RPC. The mocked CallContract blocks until its context is cancelled: if
// GetContractDepositCount ever calls DepositCount(nil) again, this test hangs until the
// suite-level timeout instead of returning promptly with a ctx error.
func TestGetContractDepositCount_HonoursContextDeadline(t *testing.T) {
	bridgeAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	mockEthClient := mocksethclient.NewEthClienter(t)

	mockEthClient.EXPECT().
		CallContract(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, _ ethereum.CallMsg, _ *big.Int) ([]byte, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		})

	agglayerBridge, err := agglayerbridge.NewAgglayerbridge(bridgeAddr, mockEthClient)
	require.NoError(t, err)

	s := BridgeSync{
		processor:      &processor{log: log.WithFields("module", "test")},
		agglayerBridge: agglayerBridge,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = s.GetContractDepositCount(ctx)
	elapsed := time.Since(start)

	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, elapsed, 5*time.Second, "GetContractDepositCount should return once ctx is done, not hang")
}
