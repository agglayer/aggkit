package validator

import (
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestDiffsCertificates(t *testing.T) {
	require.Equal(t, 0, len(DiffsCertificate(nil, nil)))

	require.Equal(t, 1, len(DiffsCertificate(&agglayertypes.Certificate{}, nil)))

	require.Equal(t, 1, len(DiffsCertificate(nil, &agglayertypes.Certificate{})))

	require.Equal(t, 0, len(DiffsCertificate(&agglayertypes.Certificate{}, &agglayertypes.Certificate{})))

	require.Equal(t, []string{
		"network ID mismatch. Expected: 1, Certificate: 2",
	}, DiffsCertificate(
		&agglayertypes.Certificate{NetworkID: 1},
		&agglayertypes.Certificate{NetworkID: 2}))

	require.Equal(t, []string{
		"height mismatch. Expected: 1, Certificate: 0",
	}, DiffsCertificate(
		&agglayertypes.Certificate{Height: 1},
		&agglayertypes.Certificate{}))

	require.Equal(t, []string{
		"prevLocalExitRoot mismatch. Expected: 0x0000000000000000000000000000000000000000000000000000000000000001, Certificate: 0x0000000000000000000000000000000000000000000000000000000000000000",
	}, DiffsCertificate(
		&agglayertypes.Certificate{PrevLocalExitRoot: common.HexToHash("0x1")},
		&agglayertypes.Certificate{}))

	require.Equal(t, []string{
		"L1InfoTreeLeafCount mismatch. Expected: 123, Certificate: 0",
	}, DiffsCertificate(
		&agglayertypes.Certificate{L1InfoTreeLeafCount: 123},
		&agglayertypes.Certificate{}))

	require.Equal(t, []string{
		"BridgeExits length mismatch. Expected: 1, Certificate: 0",
	}, DiffsCertificate(
		&agglayertypes.Certificate{BridgeExits: []*agglayertypes.BridgeExit{
			{DestinationNetwork: 1},
		}},
		&agglayertypes.Certificate{}))

	require.Equal(t, []string{
		"ImportedBridgeExits length mismatch. Expected: 1, Certificate: 0",
	}, DiffsCertificate(
		&agglayertypes.Certificate{ImportedBridgeExits: []*agglayertypes.ImportedBridgeExit{{}}},
		&agglayertypes.Certificate{}))
}

func TestDiffsBridgeExits(t *testing.T) {
	require.Equal(t, 0, len(DiffsBridgeExits([]*agglayertypes.BridgeExit{},
		[]*agglayertypes.BridgeExit{})))

	require.Equal(t, []string{
		"BridgeExits length mismatch. Expected: 1, Certificate: 0",
	}, DiffsBridgeExits([]*agglayertypes.BridgeExit{
		{
			DestinationNetwork: 123,
		},
	},
		[]*agglayertypes.BridgeExit{}))

	require.Equal(t, []string{
		"BridgeExit 0 hash mismatch. Expected: 0x197cceb6ee754931c6c466ccb8fc714dffaee3244626f484c323c61930291132, Certificate: 0x7d44de38613cfaaefd8ed1ae863d8b8678b4931bf3108da6a175bd0b1974721a",
	}, DiffsBridgeExits([]*agglayertypes.BridgeExit{
		{
			DestinationNetwork: 123,
			TokenInfo:          &agglayertypes.TokenInfo{},
		},
	},
		[]*agglayertypes.BridgeExit{
			{
				DestinationNetwork: 456,
				TokenInfo:          &agglayertypes.TokenInfo{},
			},
		}))
}

func TestDiffsImportedBridgeExits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		expected   []*agglayertypes.ImportedBridgeExit
		validating []*agglayertypes.ImportedBridgeExit
		want       []string
	}{
		{
			name:       "both nil -> no diffs",
			expected:   nil,
			validating: nil,
			want:       []string{},
		},
		{
			name:       "expected longer -> length mismatch",
			expected:   []*agglayertypes.ImportedBridgeExit{{}},
			validating: nil,
			want:       []string{"ImportedBridgeExits length mismatch. Expected: 1, Certificate: 0"},
		},
		{
			name:       "validating longer -> length mismatch",
			expected:   nil,
			validating: []*agglayertypes.ImportedBridgeExit{{}},
			want:       []string{"ImportedBridgeExits length mismatch. Expected: 0, Certificate: 1"},
		},
		{
			name: "same length, different content -> no diffs (no content comparison implemented)",
			expected: []*agglayertypes.ImportedBridgeExit{
				{GlobalIndex: &agglayertypes.GlobalIndex{MainnetFlag: true, RollupIndex: 0, LeafIndex: 1}},
			},
			validating: []*agglayertypes.ImportedBridgeExit{
				{GlobalIndex: &agglayertypes.GlobalIndex{MainnetFlag: true, RollupIndex: 0, LeafIndex: 1}},
			},
			want: []string{},
		},
		{
			name: "global index mismatch",
			expected: []*agglayertypes.ImportedBridgeExit{
				{GlobalIndex: &agglayertypes.GlobalIndex{MainnetFlag: true, RollupIndex: 0, LeafIndex: 1}},
			},
			validating: []*agglayertypes.ImportedBridgeExit{
				{GlobalIndex: &agglayertypes.GlobalIndex{MainnetFlag: true, RollupIndex: 0, LeafIndex: 2}},
			},
			want: []string{"ImportedBridgeExit 0 GlobalIndex mismatch. Expected: MainnetFlag: true, RollupIndex: 0, LeafIndex: 1, Certificate: MainnetFlag: true, RollupIndex: 0, LeafIndex: 2"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := DiffsImportedBridgeExits(tc.expected, tc.validating)
			require.Equal(t, tc.want, got)
		})
	}
}
