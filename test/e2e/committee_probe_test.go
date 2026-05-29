package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/agglayer/aggkit/test/e2e/envs"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/stretchr/testify/require"
)

// TestAggOracleCommitteeQuorumProbe is a minimal load + on-chain read probe (NOT
// a migrated committee test). It proves that, through the loaded op-fep-committee
// env's exposed clients/contracts, the on-chain AggOracleCommittee reports the
// expected quorum and membership (the distinctive op-fep-committee acceptance
// proof: a 2-of-3 committee).
//
// It is a no-op for every other env: it skips unless the loaded L2 actually has
// the AggOracleCommittee contract bound (only op-fep-committee carries the
// committee proxy address in summary.json). This keeps the probe inert for
// op-pp / op-fep / cdk-erigon runs and means it does not behave like a migrated
// bats test.
func TestAggOracleCommitteeQuorumProbe(t *testing.T) {
	// Expected committee shape for the op-fep-committee preset
	// (use_agg_oracle_committee: true, quorum 2, total_members 3).
	const (
		expectedQuorum         = uint64(2)
		expectedConfiguredMins = 3 // configured committee signer keystores (aggoracle + aggoracle-1 + aggoracle-2)
	)

	env := testEnv
	if env == nil {
		// Fall back to loading directly so the probe can run standalone via
		// `E2E_ENV=op-fep-committee go test -run TestAggOracleCommitteeQuorumProbe`.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		envName, err := envs.ParseENVName("op-fep-committee")
		require.NoError(t, err)
		loaded, err := envs.LoadEnv(ctx, envName)
		require.NoError(t, err, "LoadEnv(op-fep-committee) should succeed")
		env = loaded
	}

	// Locate an L2 with the committee contract bound. Only op-fep-committee has it.
	var committeeL2 *envs.L2Config
	for i := range env.L2s {
		if env.L2s[i].Contracts.AggOracleCommittee != nil {
			committeeL2 = &env.L2s[i]
			break
		}
	}
	if committeeL2 == nil {
		t.Skip("no AggOracleCommittee bound on any L2 (not the op-fep-committee env); skipping committee-quorum probe")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	callOpts := &bind.CallOpts{Context: ctx}

	committee := committeeL2.Contracts.AggOracleCommittee

	// Read the on-chain quorum.
	quorum, err := committee.Quorum(callOpts)
	require.NoError(t, err, "read AggOracleCommittee.Quorum")
	t.Logf("[committee-probe] AggOracleCommittee @ %s (network %s/%d): quorum=%d",
		committeeL2.Contracts.AggOracleCommitteeAddress.Hex(),
		committeeL2.SummaryKey, committeeL2.NetworkID, quorum)
	require.Equal(t, expectedQuorum, quorum, "committee quorum should be 2")

	// Read the on-chain membership.
	members, err := committee.GetAllAggOracleMembers(callOpts)
	require.NoError(t, err, "read AggOracleCommittee.GetAllAggOracleMembers")
	for i, m := range members {
		t.Logf("[committee-probe]   member[%d] = %s", i, m.Hex())
	}

	count, err := committee.GetAggOracleMembersCount(callOpts)
	require.NoError(t, err, "read AggOracleCommittee.GetAggOracleMembersCount")
	require.Equal(t, int64(len(members)), count.Int64(), "members count should match the returned slice length")

	// The committee must contain at least the configured signer set and have a
	// quorum strictly below the member count (a real M-of-N threshold) — i.e. a
	// genuine 2-of-3(+) committee, not a degenerate 1-of-1.
	require.GreaterOrEqual(t, len(members), expectedConfiguredMins,
		"committee should expose at least the 3 configured signer members")
	require.Less(t, quorum, uint64(len(members)),
		"quorum must be below the total member count (true M-of-N threshold)")
	t.Logf("[committee-probe] OK: %d-of-%d committee verified on-chain via the loader's bound contract",
		quorum, len(members))
}
