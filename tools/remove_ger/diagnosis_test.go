package remove_ger

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func mustBigInt(t *testing.T, s string) *big.Int {
	t.Helper()

	v, ok := new(big.Int).SetString(s, 10)
	require.True(t, ok)
	return v
}

func TestDiagnosisResultHasRecoveryActions(t *testing.T) {
	t.Helper()

	tests := []struct {
		name   string
		result *DiagnosisResult
		want   bool
	}{
		{
			name:   "nil result",
			result: nil,
			want:   false,
		},
		{
			name: "no ger and no claims",
			result: &DiagnosisResult{
				InvalidGER: common.HexToHash("0x1"),
				Scenario:   ScenarioNoClaims,
			},
			want: false,
		},
		{
			name: "ger still exists",
			result: &DiagnosisResult{
				InvalidGER:    common.HexToHash("0x2"),
				GERExistsOnL2: true,
				Scenario:      ScenarioNoClaims,
			},
			want: true,
		},
		{
			name: "ger already removed but claims remain",
			result: &DiagnosisResult{
				InvalidGER: common.HexToHash("0x3"),
				Claims: []ClaimDiagnosis{
					{GlobalIndex: big.NewInt(42), Category: ScenarioCategoryA},
				},
				Scenario: ScenarioCategoryA,
			},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.result.hasRecoveryActions())
		})
	}
}

func TestBuildRecoveryPlanSteps(t *testing.T) {
	t.Helper()

	t.Run("no actions", func(t *testing.T) {
		result := &DiagnosisResult{
			InvalidGER: common.HexToHash("0x1"),
			Scenario:   ScenarioNoClaims,
		}

		require.Nil(t, buildRecoveryPlanSteps(result))
	})

	t.Run("ger already removed but claim remains", func(t *testing.T) {
		result := &DiagnosisResult{
			InvalidGER: common.HexToHash("0xc25c5ca89b3565ba44c3d4690704c837c20b1dda78eb30f03d65c9b47a2fcfe2"),
			Claims: []ClaimDiagnosis{
				{GlobalIndex: mustBigInt(t, "18446744073709593685"), Category: ScenarioCategoryA},
			},
			Scenario: ScenarioCategoryA,
		}

		require.Equal(t, []string{
			"Freeze bridge (activateEmergencyState)",
			"Unset claim 0x1000000000000a455 (unsetMultipleClaims)",
			"Restore bridge (deactivateEmergencyState)",
		}, buildRecoveryPlanSteps(result))
	})

	t.Run("ger exists and claim remains", func(t *testing.T) {
		result := &DiagnosisResult{
			InvalidGER:    common.HexToHash("0xc25c5ca89b3565ba44c3d4690704c837c20b1dda78eb30f03d65c9b47a2fcfe2"),
			GERExistsOnL2: true,
			Claims: []ClaimDiagnosis{
				{GlobalIndex: mustBigInt(t, "18446744073709593685"), Category: ScenarioCategoryA},
			},
			Scenario: ScenarioCategoryA,
		}

		require.Equal(t, []string{
			"Freeze bridge (activateEmergencyState)",
			"Remove GER 0xc25c5ca89b3565ba44c3d4690704c837c20b1dda78eb30f03d65c9b47a2fcfe2 (removeGlobalExitRoots)",
			"Unset claim 0x1000000000000a455 (unsetMultipleClaims)",
			"Restore bridge (deactivateEmergencyState)",
		}, buildRecoveryPlanSteps(result))
	})
}
