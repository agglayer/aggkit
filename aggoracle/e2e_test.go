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
	tests := []struct {
		name                     string
		enableAggOracleCommittee bool
		sleepDuration            time.Duration
		additionalAssertions     func(t *testing.T, setup *helpers.AggoracleWithEVMChain, expectedGER common.Hash)
	}{
		{
			name:                     "DirectInjectionMode",
			enableAggOracleCommittee: false,
			sleepDuration:            time.Millisecond * 100,
			additionalAssertions:     nil,
		},
		{
			name:                     "AggOracleCommitteeMode",
			enableAggOracleCommittee: true,
			sleepDuration:            time.Millisecond * 500,
			additionalAssertions: func(t *testing.T, setup *helpers.AggoracleWithEVMChain, expectedGER common.Hash) {
				t.Helper()
				// fetch proposedGERToReport from committee contract
				proposedGERToReport, err := setup.L2Environment.AggOracleCommitteeContract.ProposedGERToReport(nil, expectedGER)
				require.NoError(t, err)
				require.Equal(t, proposedGERToReport.Votes, uint64(0))
				require.Equal(t, proposedGERToReport.Timestamp, uint64(0))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := helpers.DefaultEnvironmentConfig()
			cfg.AggoraclecommitteeConfig.EnableAggOracleCommittee = tt.enableAggOracleCommittee
			setup := helpers.NewE2EEnvWithEVML2(t, cfg)

			for i := 0; i < 10; i++ {
				rootHash := common.HexToHash(strconv.Itoa(i))
				_, err := setup.L1Environment.GERContract.UpdateExitRoot(setup.L1Environment.Auth, rootHash)
				require.NoError(t, err)
				setup.L1Environment.SimBackend.Commit()

				// wait for the GER to be processed by the InfoTree syncer
				time.Sleep(tt.sleepDuration)
				expectedGER, err := setup.L1Environment.GERContract.GetLastGlobalExitRoot(&bind.CallOpts{Pending: false})
				require.NoError(t, err)

				isInjected, err := setup.L2Environment.AggoracleSender.IsGERInjected(expectedGER)
				require.NoError(t, err)

				// Run additional assertions if provided
				if tt.additionalAssertions != nil {
					tt.additionalAssertions(t, setup, expectedGER)
				}

				require.True(t, isInjected, fmt.Sprintf("iteration %d, GER: %s", i, common.Bytes2Hex(expectedGER[:])))
			}
		})
	}
}

func TestEVM_AggOracleCommitteeModeWithQuorum3(t *testing.T) {
	cfg := helpers.DefaultEnvironmentConfig()
	cfg.AggoraclecommitteeConfig.EnableAggOracleCommittee = true
	cfg.AggoraclecommitteeConfig.Quorum = 3
	setup := helpers.NewE2EEnvWithEVML2(t, cfg)

	rootHash := common.HexToHash(strconv.Itoa(10))
	_, err := setup.L1Environment.GERContract.UpdateExitRoot(setup.L1Environment.Auth, rootHash)
	require.NoError(t, err)
	setup.L1Environment.SimBackend.Commit()

	time.Sleep(time.Millisecond * 500)
	expectedGER, err := setup.L1Environment.GERContract.GetLastGlobalExitRoot(&bind.CallOpts{Pending: false})
	require.NoError(t, err)

	// fetch last proposed GER by the aggoracle committee member
	lastProposedGER, err := setup.L2Environment.AggOracleCommitteeContract.AddressToLastProposedGER(nil, setup.L2Environment.Auth.From)
	require.NoError(t, err)
	require.Equal(t, common.Bytes2Hex(lastProposedGER[:]), common.Bytes2Hex(expectedGER[:]))

	// fetch proposedGERToReport from committee contract
	proposedGERToReport, err := setup.L2Environment.AggOracleCommitteeContract.ProposedGERToReport(nil, expectedGER)
	require.NoError(t, err)
	require.Equal(t, proposedGERToReport.Votes, uint64(1))
	require.NotNil(t, proposedGERToReport.Timestamp)
}
