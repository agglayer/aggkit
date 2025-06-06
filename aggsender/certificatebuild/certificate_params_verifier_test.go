package certificatebuild

import (
	"testing"

	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/ethereum/go-ethereum/common"
)

func TestCertificateBuildVerifier_VerifyBuildParams(t *testing.T) {
	mainnetExitRoot := common.HexToHash("0x01")
	rollupExitRoot := common.HexToHash("0x02")
	testCases := []struct {
		name        string
		claims      []bridgesync.Claim
		expectedErr bool
	}{
		{
			name: "success",
			claims: []bridgesync.Claim{
				{
					MainnetExitRoot: mainnetExitRoot,
					RollupExitRoot:  rollupExitRoot,
					GlobalExitRoot:  calculateGER(mainnetExitRoot, rollupExitRoot),
					GlobalIndex:     common.Big1,
					BlockNum:        100,
				},
			},
			expectedErr: false,
		},
		{
			name: "GER mismatch",
			claims: []bridgesync.Claim{
				{
					MainnetExitRoot: mainnetExitRoot,
					RollupExitRoot:  rollupExitRoot,
					GlobalExitRoot:  common.HexToHash("0xdeadbeef"),
					GlobalIndex:     common.Big1,
					BlockNum:        100,
				},
			},
			expectedErr: true,
		},
		{
			name:        "empty claims",
			claims:      []bridgesync.Claim{},
			expectedErr: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			params := &types.CertificateBuildParams{
				Claims: tc.claims,
			}
			verifier := NewCertificateBuildVerifier()
			err := verifier.VerifyBuildParams(params)
			if tc.expectedErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Fatalf("expected no error, got: %v", err)
				}
			}
		})
	}
}
