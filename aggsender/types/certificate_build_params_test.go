package types

import (
	"math"
	"testing"

	"github.com/agglayer/aggkit/bridgesync"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
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
				Claims:          []bridgesync.Claim{},
				Unclaims:        []bridgesynctypes.Unclaim{},
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
				Claims: []bridgesync.Claim{
					{BlockNum: 1500},
				},
				Unclaims: []bridgesynctypes.Unclaim{
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
				Claims:    []bridgesync.Claim{},
				Unclaims:  []bridgesynctypes.Unclaim{},
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
				Claims:          []bridgesync.Claim{},
				Unclaims:        []bridgesynctypes.Unclaim{},
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
				Claims: []bridgesync.Claim{
					{BlockNum: 4000000000},
					{BlockNum: 5000000000},
				},
				Unclaims:  []bridgesynctypes.Unclaim{},
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
