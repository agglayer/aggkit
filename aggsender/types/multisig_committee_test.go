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
		threshold   uint32
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
				require.Equal(t, mc.signers, mc.Signers())
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

	mc, err := NewMultisigCommittee(ptrs, uint32(len(signers)-1))
	require.NoError(t, err)

	cpySigners := mc.Signers()
	require.Equal(t, signers, cpySigners)

	// Update single signer's address
	cpySigners[0].Address = common.HexToAddress("0x4")
	require.NotEqual(t, signers, cpySigners)
}

func TestMultisigCommittee_IsThresholdReached(t *testing.T) {
	s1 := NewSignerInfo("http://localhost:8001", common.HexToAddress("0x1"))
	s2 := NewSignerInfo("http://localhost:8002", common.HexToAddress("0x2"))
	s3 := NewSignerInfo("http://localhost:8003", common.HexToAddress("0x3"))

	tests := []struct {
		name        string
		initial     []*SignerInfo
		threshold   uint32
		signers     []common.Address
		wantQuorum  bool
		errContains string
	}{
		{
			name:       "has quorum",
			initial:    []*SignerInfo{s1, s2, s3},
			threshold:  2,
			signers:    []common.Address{s1.Address, s2.Address},
			wantQuorum: true,
		},
		{
			name:       "insufficient quorum",
			initial:    []*SignerInfo{s1, s2, s3},
			threshold:  3,
			signers:    []common.Address{s1.Address, s2.Address},
			wantQuorum: false,
		},
		{
			name:        "unknown signer",
			initial:     []*SignerInfo{s1, s2},
			threshold:   1,
			signers:     []common.Address{common.HexToAddress("0x99")},
			errContains: "not in the committee",
		},
		{
			name:       "duplicate signers ignored",
			initial:    []*SignerInfo{s1, s2},
			threshold:  2,
			signers:    []common.Address{s1.Address, s1.Address, s2.Address},
			wantQuorum: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mc, err := NewMultisigCommittee(tc.initial, tc.threshold)
			require.NoError(t, err)

			ok, err := mc.IsThresholdReached(tc.signers)
			if tc.errContains != "" {
				require.ErrorContains(t, err, tc.errContains)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.wantQuorum, ok)
			}
		})
	}
}
