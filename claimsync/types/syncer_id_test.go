package types

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClaimSyncerID_String(t *testing.T) {
	tests := []struct {
		id       ClaimSyncerID
		expected string
	}{
		{L1ClaimSyncer, "L1ClaimSyncer"},
		{L2ClaimSyncer, "L2ClaimSyncer"},
		{ClaimSyncerID(99), fmt.Sprintf("UnknownClaimSyncerID(%d)", 99)},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.id.String())
		})
	}
}
