package types

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestMultisigCommittee_NewMultisigCommittee(t *testing.T) {
	s1 := NewSignerInfo("http://localhost:7001", common.HexToAddress("0x1"))
	s2 := NewSignerInfo("http://localhost:7002", common.HexToAddress("0x2"))
	s3 := NewSignerInfo("http://localhost:7003", common.HexToAddress("0x1"))

	tests := []struct {
		name        string
		members     []*SignerInfo
		threshold   uint64
		errContains string
	}{
		{
			name:      "valid initialization (unique signers)",
			members:   []*SignerInfo{s1, s2},
			threshold: 1,
		},
		{
			name:        "initialize with duplicate signer (same url and address)",
			members:     []*SignerInfo{s1, s1},
			threshold:   1,
			errContains: "already in committee",
		},
		{
			name:        "initialize with duplicate signer (same url diff address)",
			members:     []*SignerInfo{s1, s3},
			threshold:   1,
			errContains: "already in committee",
		},
		{
			name:        "initialize committee size less than threshold",
			members:     []*SignerInfo{s1, s2},
			threshold:   5,
			errContains: "committee size (2) must be greater than or equal to the signatures threshold (5)",
		},
		{
			name:        "initialize empty committee",
			members:     nil,
			threshold:   5,
			errContains: errEmptyCommittee.Error(),
		},
		{
			name:        "initialize zero threshold",
			members:     []*SignerInfo{s1},
			threshold:   0,
			errContains: errZeroThreshold.Error(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mc, err := NewMultisigCommittee(tc.members, tc.threshold)
			if tc.errContains != "" {
				require.ErrorContains(t, err, tc.errContains)
				require.Nil(t, mc)
			} else {
				require.NoError(t, err)
				require.NotNil(t, mc)
				require.Equal(t, len(tc.members), mc.Size())
			}
		})
	}
}

func TestMultisigCommittee_AddSigner(t *testing.T) {
	existing := NewSignerInfo("http://localhost:7001", common.HexToAddress("0x1"))

	tests := []struct {
		name        string
		initial     []*SignerInfo
		toAdd       *SignerInfo
		errContains string
	}{
		{
			name:    "add new signer",
			initial: []*SignerInfo{existing},
			toAdd:   NewSignerInfo("http://localhost:7002", common.HexToAddress("0x2")),
		},
		{
			name:        "add duplicate signer",
			initial:     []*SignerInfo{existing},
			toAdd:       NewSignerInfo("http://localhost:7001", common.HexToAddress("0x1")),
			errContains: "already in committee",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mc, err := NewMultisigCommittee(tc.initial, 1)
			require.NoError(t, err)

			err = mc.AddSigner(tc.toAdd)
			if tc.errContains != "" {
				require.ErrorContains(t, err, tc.errContains)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestMultisigCommittee_Signers(t *testing.T) {
	signers := []SignerInfo{
		{Address: common.HexToAddress("0x1"), URL: "http://localhost:8001"},
		{Address: common.HexToAddress("0x2"), URL: "http://localhost:8002"},
		{Address: common.HexToAddress("0x3"), URL: "http://localhost:8003"},
	}

	ptrs := make([]*SignerInfo, len(signers))
	for i := range signers {
		ptrs[i] = &signers[i]
	}

	mc, err := NewMultisigCommittee(ptrs, uint64(len(signers)-1))
	require.NoError(t, err)

	cpySigners := mc.Signers()
	require.Equal(t, signers, cpySigners)

	// Update single signer's address
	cpySigners[0].Address = common.HexToAddress("0x4")
	require.NotEqual(t, signers, cpySigners)
}

func TestMultisigCommittee_IsMember(t *testing.T) {
	s1 := NewSignerInfo("http://localhost:7001", common.HexToAddress("0x1"))
	s2 := NewSignerInfo("http://localhost:7002", common.HexToAddress("0x2"))

	mc, err := NewMultisigCommittee([]*SignerInfo{s1}, 1)
	require.NoError(t, err)

	// existing member
	require.True(t, mc.IsMember(s1.Address))

	// non-member
	require.False(t, mc.IsMember(s2.Address))

	// add new signer and verify membership
	require.NoError(t, mc.AddSigner(s2))
	require.True(t, mc.IsMember(s2.Address))

	// zero address should not be a member
	require.False(t, mc.IsMember(common.Address{}))
}

func TestMultisigCommittee_String(t *testing.T) {
	s1 := NewSignerInfo("http://localhost:7001", common.HexToAddress("0x1"))
	s2 := NewSignerInfo("http://localhost:7002", common.HexToAddress("0x2"))
	s3 := NewSignerInfo("http://localhost:7003", common.HexToAddress("0x3"))

	tests := []struct {
		name      string
		members   []*SignerInfo
		threshold uint64
		expected  string
	}{
		{
			name:      "single signer",
			members:   []*SignerInfo{s1},
			threshold: 1,
			expected:  "{Committee: {0x0000000000000000000000000000000000000001=http://localhost:7001},  Threshold: 1}",
		},
		{
			name:      "two signers",
			members:   []*SignerInfo{s1, s2},
			threshold: 2,
			expected:  "{Committee: {0x0000000000000000000000000000000000000001=http://localhost:7001, 0x0000000000000000000000000000000000000002=http://localhost:7002},  Threshold: 2}",
		},
		{
			name:      "three signers, threshold less than size",
			members:   []*SignerInfo{s1, s2, s3},
			threshold: 2,
			expected:  "{Committee: {0x0000000000000000000000000000000000000001=http://localhost:7001, 0x0000000000000000000000000000000000000002=http://localhost:7002, 0x0000000000000000000000000000000000000003=http://localhost:7003},  Threshold: 2}",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mc, err := NewMultisigCommittee(tc.members, tc.threshold)
			require.NoError(t, err)
			require.Equal(t, tc.expected, mc.String())
		})
	}
}
