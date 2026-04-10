package types

import (
	"math"
	"testing"

	"github.com/agglayer/aggkit/bridgesync"
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	"github.com/stretchr/testify/require"
)

func TestInvalidRangeToBlock(t *testing.T) {
	params := &CertificateBuildParams{
		FromBlock: 100,
		ToBlock:   200,
	}
	_, err := params.Range(100, 0)
	require.Error(t, err, "should return an error for invalid range")
}

func TestInvalidRangeOutsideOriginalRange(t *testing.T) {
	params := &CertificateBuildParams{
		FromBlock: 100,
		ToBlock:   200,
	}
	_, err := params.Range(99, 110)
	require.Error(t, err, "should return an error for invalid range")
}

func TestNumberOfBlocks(t *testing.T) {
	tests := []struct {
		name     string
		params   *CertificateBuildParams
		expected int
	}{
		{
			name:     "Nil params",
			params:   nil,
			expected: 0,
		},
		{
			name: "Normal range",
			params: &CertificateBuildParams{
				FromBlock: 100,
				ToBlock:   200,
			},
			expected: 101,
		},
		{
			name: "Overflow - range exceeds MaxInt",
			params: &CertificateBuildParams{
				FromBlock: 0,
				ToBlock:   uint64(math.MaxInt) + 1,
			},
			expected: math.MaxInt,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.params.NumberOfBlocks()
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestCertificateBuildParamsString(t *testing.T) {
	tests := []struct {
		name     string
		params   *CertificateBuildParams
		expected string
	}{
		{
			name: "Empty certificate",
			params: &CertificateBuildParams{
				CertificateType: CertificateTypePP,
				FromBlock:       100,
				ToBlock:         200,
				Bridges:         []bridgesync.Bridge{},
				Claims:          []claimsynctypes.Claim{},
				Unclaims:        []claimsynctypes.Unclaim{},
				CreatedAt:       1234567890,
			},
			expected: "Type: pp FromBlock: 100, ToBlock: 200, numBridges: 0, numClaims: 0, numUnclaims: 0, createdAt: 1234567890",
		},
		{
			name: "Certificate with bridges, claims, and unclaims",
			params: &CertificateBuildParams{
				CertificateType: CertificateTypeFEP,
				FromBlock:       1000,
				ToBlock:         2000,
				Bridges: []bridgesync.Bridge{
					{BlockNum: 1001},
					{BlockNum: 1002},
				},
				Claims: []claimsynctypes.Claim{
					{BlockNum: 1500},
				},
				Unclaims: []claimsynctypes.Unclaim{
					{BlockNumber: 1600},
					{BlockNumber: 1700},
					{BlockNumber: 1800},
				},
				CreatedAt: 987654321,
			},
			expected: "Type: fep FromBlock: 1000, ToBlock: 2000, numBridges: 2, numClaims: 1, numUnclaims: 3, createdAt: 987654321",
		},
		{
			name: "Optimistic certificate type",
			params: &CertificateBuildParams{
				CertificateType: CertificateTypeOptimistic,
				FromBlock:       500,
				ToBlock:         600,
				Bridges: []bridgesync.Bridge{
					{BlockNum: 550},
				},
				Claims:    []claimsynctypes.Claim{},
				Unclaims:  []claimsynctypes.Unclaim{},
				CreatedAt: 1111111111,
			},
			expected: "Type: optimistic FromBlock: 500, ToBlock: 600, numBridges: 1, numClaims: 0, numUnclaims: 0, createdAt: 1111111111",
		},
		{
			name: "Unknown certificate type",
			params: &CertificateBuildParams{
				CertificateType: CertificateTypeUnknown,
				FromBlock:       1,
				ToBlock:         10,
				Bridges:         []bridgesync.Bridge{},
				Claims:          []claimsynctypes.Claim{},
				Unclaims:        []claimsynctypes.Unclaim{},
				CreatedAt:       0,
			},
			expected: "Type:  FromBlock: 1, ToBlock: 10, numBridges: 0, numClaims: 0, numUnclaims: 0, createdAt: 0",
		},
		{
			name: "Large numbers",
			params: &CertificateBuildParams{
				CertificateType: CertificateTypePP,
				FromBlock:       999999999,
				ToBlock:         9999999999,
				Bridges: []bridgesync.Bridge{
					{BlockNum: 1000000000},
					{BlockNum: 2000000000},
					{BlockNum: 3000000000},
				},
				Claims: []claimsynctypes.Claim{
					{BlockNum: 4000000000},
					{BlockNum: 5000000000},
				},
				Unclaims:  []claimsynctypes.Unclaim{},
				CreatedAt: 4294967295,
			},
			expected: "Type: pp FromBlock: 999999999, ToBlock: 9999999999, numBridges: 3, numClaims: 2, numUnclaims: 0, createdAt: 4294967295",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.params.String()
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestAdjustToBlock(t *testing.T) {
	tests := []struct {
		name       string
		params     *CertificateBuildParams
		newToBlock uint64
		errorMsg   string
		validate   func(t *testing.T, result *CertificateBuildParams)
	}{
		{
			name: "Cannot adjust to higher block",
			params: &CertificateBuildParams{
				FromBlock: 100,
				ToBlock:   200,
			},
			newToBlock: 300,
			errorMsg:   "cannot adjust toBlock to a higher value",
		},
		{
			name: "Same toBlock returns original params",
			params: &CertificateBuildParams{
				FromBlock:       100,
				ToBlock:         200,
				CertificateType: CertificateTypePP,
				CreatedAt:       1234567890,
				Bridges: []bridgesync.Bridge{
					{BlockNum: 150, DepositCount: 1},
				},
				Claims: []claimsynctypes.Claim{
					{BlockNum: 180},
				},
			},
			newToBlock: 200,
			validate: func(t *testing.T, result *CertificateBuildParams) {
				t.Helper()

				require.Equal(t, uint64(100), result.FromBlock)
				require.Equal(t, uint64(200), result.ToBlock)
				require.Len(t, result.Bridges, 1)
				require.Len(t, result.Claims, 1)
			},
		},
		{
			name: "Adjust to lower block - filters bridges and claims",
			params: &CertificateBuildParams{
				FromBlock:       100,
				ToBlock:         300,
				CertificateType: CertificateTypeFEP,
				CreatedAt:       1234567890,
				RetryCount:      1,
				Bridges: []bridgesync.Bridge{
					{BlockNum: 120, DepositCount: 1},
					{BlockNum: 180, DepositCount: 2},
					{BlockNum: 250, DepositCount: 3}, // This should be excluded
				},
				Claims: []claimsynctypes.Claim{
					{BlockNum: 150},
					{BlockNum: 220}, // This should be excluded
				},
				Unclaims: []claimsynctypes.Unclaim{
					{BlockNumber: 140},
					{BlockNumber: 280}, // This should be excluded
				},
			},
			newToBlock: 200,
			validate: func(t *testing.T, result *CertificateBuildParams) {
				t.Helper()

				require.Equal(t, uint64(100), result.FromBlock)
				require.Equal(t, uint64(200), result.ToBlock)
				require.Equal(t, CertificateTypeFEP, result.CertificateType)
				require.Equal(t, uint32(1234567890), result.CreatedAt)
				require.Equal(t, 1, result.RetryCount)

				// Should have 2 bridges (120, 180) - excluding 250
				require.Len(t, result.Bridges, 2)
				require.Equal(t, uint64(120), result.Bridges[0].BlockNum)
				require.Equal(t, uint64(180), result.Bridges[1].BlockNum)

				// Should have 1 claim (150) - excluding 220
				require.Len(t, result.Claims, 1)
				require.Equal(t, uint64(150), result.Claims[0].BlockNum)

				// Should have 1 unclaim (140) - excluding 280
				require.Len(t, result.Unclaims, 1)
				require.Equal(t, uint64(140), result.Unclaims[0].BlockNumber)
			},
		},
		{
			name: "Adjust to block at boundary - includes exact match",
			params: &CertificateBuildParams{
				FromBlock: 100,
				ToBlock:   200,
				Bridges: []bridgesync.Bridge{
					{BlockNum: 100, DepositCount: 1}, // At fromBlock
					{BlockNum: 150, DepositCount: 2}, // In range
					{BlockNum: 150, DepositCount: 3}, // Exactly at newToBlock
					{BlockNum: 200, DepositCount: 4}, // Should be excluded
				},
			},
			newToBlock: 150,
			validate: func(t *testing.T, result *CertificateBuildParams) {
				t.Helper()

				require.Equal(t, uint64(100), result.FromBlock)
				require.Equal(t, uint64(150), result.ToBlock)
				require.Len(t, result.Bridges, 3) // Includes blocks 100, 150, 150
				require.Equal(t, uint64(100), result.Bridges[0].BlockNum)
				require.Equal(t, uint64(150), result.Bridges[1].BlockNum)
				require.Equal(t, uint64(150), result.Bridges[2].BlockNum)
			},
		},
		{
			name: "Empty certificate adjustment",
			params: &CertificateBuildParams{
				FromBlock:       100,
				ToBlock:         200,
				CertificateType: CertificateTypeOptimistic,
				Bridges:         []bridgesync.Bridge{},
				Claims:          []claimsynctypes.Claim{},
				Unclaims:        []claimsynctypes.Unclaim{},
			},
			newToBlock: 150,
			validate: func(t *testing.T, result *CertificateBuildParams) {
				t.Helper()

				require.Equal(t, uint64(100), result.FromBlock)
				require.Equal(t, uint64(150), result.ToBlock)
				require.Len(t, result.Bridges, 0)
				require.Len(t, result.Claims, 0)
				require.Len(t, result.Unclaims, 0)
			},
		},
		{
			name: "Adjust to fromBlock - minimal range",
			params: &CertificateBuildParams{
				FromBlock: 100,
				ToBlock:   200,
				Bridges: []bridgesync.Bridge{
					{BlockNum: 100, DepositCount: 1},
					{BlockNum: 150, DepositCount: 2},
				},
			},
			newToBlock: 100,
			validate: func(t *testing.T, result *CertificateBuildParams) {
				t.Helper()

				require.Equal(t, uint64(100), result.FromBlock)
				require.Equal(t, uint64(100), result.ToBlock)
				require.Len(t, result.Bridges, 1)
				require.Equal(t, uint64(100), result.Bridges[0].BlockNum)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.params.AdjustToBlock(tt.newToBlock)

			if tt.errorMsg != "" {
				require.ErrorContains(t, err, tt.errorMsg)
				require.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				if tt.validate != nil {
					tt.validate(t, result)
				}
			}
		})
	}
}

func TestEstimateSize(t *testing.T) {
	sut := &CertificateBuildParams{
		FromBlock: 100,
		ToBlock:   200,
		Bridges:   make([]bridgesync.Bridge, 50),
		Claims:    make([]claimsynctypes.Claim, 150),
	}

	estimatedSize := sut.EstimatedSize()

	require.Equal(t, uint(0x6bb47), estimatedSize, "Estimated size should match expected size")
}
