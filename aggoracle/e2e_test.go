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

func TestEVM(t *testing.T) {
	l1Setup, l2Setup := helpers.NewSimulatedEVMEnvironment(t, helpers.DefaultEnvironmentConfig(helpers.SovereignChainL2GERContract))

	for i := range 10 {
		_, err := l1Setup.GERContract.UpdateExitRoot(l1Setup.Auth, common.HexToHash(strconv.Itoa(i)))
		require.NoError(t, err)
		l1Setup.SimBackend.Commit()

		// wait for the GER to be processed by the InfoTree syncer
		time.Sleep(time.Millisecond * 100)
		expectedGER, err := l1Setup.GERContract.GetLastGlobalExitRoot(&bind.CallOpts{Pending: false})
		require.NoError(t, err)

		isInjected, err := l2Setup.AggoracleSender.IsGERInjected(expectedGER)
		require.NoError(t, err)

		require.True(t, isInjected, fmt.Sprintf("iteration %d, GER: %s", i, common.Bytes2Hex(expectedGER[:])))
	}
}
