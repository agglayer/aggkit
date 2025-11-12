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
		additionalAssertions     func(t *testing.T, setup *helpers.L2Environment, expectedGER common.Hash)
	}{
		{
			name:                     "DirectInjectionMode",
			enableAggOracleCommittee: false,
			sleepDuration:            time.Millisecond * 500,
			additionalAssertions:     nil,
		},
		{
			name:                     "AggOracleCommitteeMode",
			enableAggOracleCommittee: true,
			sleepDuration:            time.Millisecond * 500,
			additionalAssertions: func(t *testing.T, l2 *helpers.L2Environment, expectedGER common.Hash) {
				t.Helper()
				// fetch proposedGERToReport from committee contract
				proposedGERToReport, err := l2.AggOracleCommitteeContract.ProposedGERToReport(nil, expectedGER)
				require.NoError(t, err)
				require.Equal(t, proposedGERToReport.Votes, uint64(0))
				require.Equal(t, proposedGERToReport.Timestamp, uint64(0))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := helpers.DefaultEnvironmentConfig(helpers.SovereignChainL2GERContract)
			cfg.AggOracleCommitteeCfg.EnableAggOracleCommittee = tt.enableAggOracleCommittee
			l1, l2 := helpers.NewSimulatedEVMEnvironment(t, cfg)

			for i := range 10 {
				rootHash := common.HexToHash(strconv.Itoa(i))
				_, err := l1.GERContract.UpdateExitRoot(l1.Auth, rootHash)
				require.NoError(t, err)
				l1.SimBackend.Commit()

				// wait for the GER to be processed by the InfoTree syncer
				time.Sleep(tt.sleepDuration)
				expectedGER, err := l1.GERContract.GetLastGlobalExitRoot(&bind.CallOpts{Pending: false})
				require.NoError(t, err)

				isInjected, err := l2.AggoracleSender.IsGERInjected(expectedGER)
				require.NoError(t, err)

				// Run additional assertions if provided
				if tt.additionalAssertions != nil {
					tt.additionalAssertions(t, l2, expectedGER)
				}

				require.True(t, isInjected, fmt.Sprintf("iteration %d, GER: %s", i, common.Bytes2Hex(expectedGER[:])))
			}
		})
	}
}

func TestEVM_AggOracleCommitteeModeWithQuorum3(t *testing.T) {
	cfg := helpers.DefaultEnvironmentConfig(helpers.SovereignChainL2GERContract)
	cfg.AggOracleCommitteeCfg.EnableAggOracleCommittee = true
	cfg.AggOracleCommitteeCfg.Quorum = 3
	l1, l2 := helpers.NewSimulatedEVMEnvironment(t, cfg)

	rootHash := common.HexToHash(strconv.Itoa(10))
	_, err := l1.GERContract.UpdateExitRoot(l1.Auth, rootHash)
	require.NoError(t, err)
	l1.SimBackend.Commit()

	time.Sleep(time.Millisecond * 500)
	expectedGER, err := l1.GERContract.GetLastGlobalExitRoot(&bind.CallOpts{Pending: false})
	require.NoError(t, err)

	// fetch last proposed GER by the aggoracle committee member
	lastProposedGER, err := l2.AggOracleCommitteeContract.AddressToLastProposedGER(nil, l2.Auth.From)
	require.NoError(t, err)
	require.Equal(t, common.Bytes2Hex(lastProposedGER[:]), common.Bytes2Hex(expectedGER[:]))

	// fetch proposedGERToReport from committee contract
	proposedGERToReport, err := l2.AggOracleCommitteeContract.ProposedGERToReport(nil, expectedGER)
	require.NoError(t, err)
	require.Equal(t, proposedGERToReport.Votes, uint64(1))
	require.NotNil(t, proposedGERToReport.Timestamp)
}
