package aggoracle_test

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/agglayer/aggkit/test/helpers"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestEVM_GERInjection(t *testing.T) {
	const (
		// The GER has to go through the InfoTree syncer and the AggOracle before it is injected on the L2: it is
		// waited for, instead of sleeping a fixed time, so a slow machine doesn't make the test fail
		injectionTimeout = 15 * time.Second
		injectionPoll    = 50 * time.Millisecond
	)

	cfg := helpers.DefaultEnvironmentConfig(helpers.SovereignChainL2GERContract)
	l1, l2 := helpers.NewSimulatedEVMEnvironment(t, cfg)

	for i := range 10 {
		rootHash := common.HexToHash(strconv.Itoa(i))
		_, err := l1.GERContract.UpdateExitRoot(l1.Auth, rootHash)
		require.NoError(t, err)
		l1.SimBackend.Commit()

		expectedGER, err := l1.GERContract.GetLastGlobalExitRoot(&bind.CallOpts{Pending: false})
		require.NoError(t, err)
		t.Log("Expected GER:", common.Bytes2Hex(expectedGER[:]))

		require.Eventually(t, func() bool {
			isInjected, err := l2.AggoracleSender.IsGERInjected(expectedGER)
			return err == nil && isInjected
		}, injectionTimeout, injectionPoll, fmt.Sprintf("iteration %d, GER: %s", i, common.Bytes2Hex(expectedGER[:])))
	}
}
