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

func TestEVM_DirectInjectionMode(t *testing.T) {
	setup := helpers.NewE2EEnvWithEVML2(t, helpers.DefaultEnvironmentConfig())

	for i := 2; i < 12; i++ {
		rootHash := common.HexToHash(strconv.Itoa(i))
		_, err := setup.L1Environment.GERContract.UpdateExitRoot(setup.L1Environment.Auth, rootHash)
		require.NoError(t, err)
		setup.L1Environment.SimBackend.Commit()

		// wait for the GER to be processed by the InfoTree syncer
		time.Sleep(time.Millisecond * 100)
		expectedGER, err := setup.L1Environment.GERContract.GetLastGlobalExitRoot(&bind.CallOpts{Pending: false})
		require.NoError(t, err)

		isInjected, err := setup.L2Environment.AggoracleSender.IsGERInjected(expectedGER)
		require.NoError(t, err)

		require.True(t, isInjected, fmt.Sprintf("iteration %d, GER: %s", i, common.Bytes2Hex(expectedGER[:])))
	}
}

func TestEVM_AggOracleCommitteeMode(t *testing.T) {
	cfg := helpers.DefaultEnvironmentConfig()
	cfg.AggoraclecommitteeConfig.EnableAggOracleCommittee = true
	setup := helpers.NewE2EEnvWithEVML2(t, cfg)

	for i := 2; i < 12; i++ {
		rootHash := common.HexToHash(strconv.Itoa(10))
		_, err := setup.L1Environment.GERContract.UpdateExitRoot(setup.L1Environment.Auth, rootHash)
		require.NoError(t, err)
		setup.L1Environment.SimBackend.Commit()

		// wait for the GER to be processed by the InfoTree syncer
		time.Sleep(time.Millisecond * 500)
		expectedGER, err := setup.L1Environment.GERContract.GetLastGlobalExitRoot(&bind.CallOpts{Pending: false})
		require.NoError(t, err)

		isInjected, err := setup.L2Environment.AggoracleSender.IsGERInjected(expectedGER)
		require.NoError(t, err)

		require.True(t, isInjected, fmt.Sprintf("Root: %s", common.Bytes2Hex(expectedGER[:])))
	}
}
