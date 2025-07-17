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
	// setup := helpers.NewE2EEnvWithEVML2(t, helpers.DefaultEnvironmentConfig())
	cfg := helpers.DefaultEnvironmentConfig()
	cfg.AggOracleCommitteeMode = true
	setup := helpers.NewE2EEnvWithEVML2(t, cfg)

	for i := 10; i < 11; i++ {
		gerHash := common.HexToHash(strconv.Itoa(i))
		fmt.Println("--------------------------------gerHash", gerHash.Hex())
		_, err := setup.L1Environment.GERContract.UpdateExitRoot(setup.L1Environment.Auth, gerHash)
		require.NoError(t, err)
		setup.L1Environment.SimBackend.Commit()

		gerUpdater, err := setup.L2Environment.GERContract.GlobalExitRootUpdater(nil)
		require.NoError(t, err)
		fmt.Println("--------------------------------gerUpdater", gerUpdater.Hex())

		// wait for the GER to be processed by the InfoTree syncer
		time.Sleep(time.Millisecond * 100)
		expectedGER, err := setup.L1Environment.GERContract.GetLastGlobalExitRoot(&bind.CallOpts{Pending: false})
		require.NoError(t, err)
		fmt.Println("--------------------------------expectedGER", common.Bytes2Hex(expectedGER[:]))

		isInjected, err := setup.L2Environment.AggoracleSender.IsGERInjected(expectedGER)
		require.NoError(t, err)
		fmt.Println("--------------------------------isInjected", isInjected)

		time.Sleep(time.Millisecond * 300)

		// find last proposed GER for the user
		lastProposedGER, err := setup.L2Environment.AggOracleCommitteeContract.AddressToLastProposedGER(nil, setup.L2Environment.Auth.From)
		require.NoError(t, err)
		fmt.Println("--------------------------------lastProposedGER", common.Bytes2Hex(lastProposedGER[:]))

		// find proposedGERToReport from committee contract
		proposedGERToReport, err := setup.L2Environment.AggOracleCommitteeContract.ProposedGERToReport(nil, expectedGER)
		require.NoError(t, err)
		fmt.Println("--------------------------------proposedGERToReport", proposedGERToReport)

		// require.True(t, isInjected, fmt.Sprintf("iteration %d, GER: %s", i, common.Bytes2Hex(expectedGER[:])))
	}
}

func TestEVM_ProposeGER(t *testing.T) {
	cfg := helpers.DefaultEnvironmentConfig()
	cfg.AggOracleCommitteeMode = true
	setup := helpers.NewE2EEnvWithEVML2(t, cfg)

	// Create a GER on L1 for the oracle to process
	gerHash := common.HexToHash(strconv.Itoa(55))
	_, err := setup.L1Environment.GERContract.UpdateExitRoot(setup.L1Environment.Auth, gerHash)
	require.NoError(t, err)
	setup.L1Environment.SimBackend.Commit()

	// Wait for the GER to be processed by the InfoTree syncer
	time.Sleep(time.Millisecond * 100)
	expectedGER, err := setup.L1Environment.GERContract.GetLastGlobalExitRoot(&bind.CallOpts{Pending: false})
	require.NoError(t, err)

	// Convert expectedGER from bytes32 to common.Hash
	expectedGERHash := common.Hash(expectedGER)

	// Wait for the oracle to process the GER
	time.Sleep(time.Millisecond * 200)

	// Check if GER was proposed by the oracle member
	lastProposedGER, err := setup.L2Environment.AggOracleCommitteeContract.AddressToLastProposedGER(nil, setup.L2Environment.Auth.From)
	require.NoError(t, err)

	lastProposedGERHash := common.Hash(lastProposedGER)
	require.Equal(t, expectedGERHash, lastProposedGERHash, fmt.Sprintf("GER: %s was not proposed by oracle member", common.Bytes2Hex(expectedGER[:])))
}

func TestEVM_DirectProposeGER(t *testing.T) {
	cfg := helpers.DefaultEnvironmentConfig()
	cfg.AggOracleCommitteeMode = true

	l2Setup := helpers.L2Setup(t, cfg)

	// Propose a GER directly on L2 using the AggOracleCommittee contract
	gerHash := common.HexToHash(strconv.Itoa(123))

	// err := l2Setup.AggoracleSender.ProposeGER(context.Background(), gerHash)
	// require.NoError(t, err)
	// l2Setup.SimBackend.Commit()

	_, err := l2Setup.AggOracleCommitteeContract.ProposeGlobalExitRoot(l2Setup.Auth, gerHash)
	require.NoError(t, err)
	l2Setup.SimBackend.Commit()

	// wait for sometime
	time.Sleep(time.Millisecond * 400)

	// Check if the GER was successfully proposed by checking the last proposed GER for this address
	lastProposedGER, err := l2Setup.AggOracleCommitteeContract.AddressToLastProposedGER(nil, l2Setup.Auth.From)
	require.NoError(t, err)

	fmt.Println("lastProposedGER", common.Hash(lastProposedGER))

	// err = l2Setup.AggoracleSender.ProposeGER(context.Background(), gerHash)
	// fmt.Println("second propose err", err)
	// l2Setup.SimBackend.Commit()

	// Check if the GER was successfully proposed by checking the last proposed GER for this address
	lastProposedGER, err = l2Setup.AggOracleCommitteeContract.AddressToLastProposedGER(nil, l2Setup.Auth.From)
	require.NoError(t, err)

	fmt.Println("lastProposedGER", common.Hash(lastProposedGER))

	// lastProposedGERHash := common.Hash(lastProposedGER)
	// // Verify that the proposed GER matches what we submitted
	// require.Equal(t, gerHash, lastProposedGERHash, fmt.Sprintf("GER: %s was not successfully proposed to committee contract", gerHash.Hex()))
}
