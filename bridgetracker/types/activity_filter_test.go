package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestActivityFilterString(t *testing.T) {
	require.Equal(t, "all", ActivityFilterAll.String())
	require.Equal(t, "claimed", ActivityFilterClaimed.String())
	require.Equal(t, "pending", ActivityFilterPending.String())
	require.Equal(t, "error", ActivityFilterError.String())
	require.Equal(t, "Unknown(99)", ActivityFilter(99).String())
}

func TestParseActivityFilter(t *testing.T) {
	tests := []struct {
		input   string
		want    ActivityFilter
		wantErr bool
	}{
		{input: "", want: ActivityFilterAll},
		{input: "all", want: ActivityFilterAll},
		{input: "claimed", want: ActivityFilterClaimed},
		{input: "pending", want: ActivityFilterPending},
		{input: "error", want: ActivityFilterError},
		{input: "bogus", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseActivityFilter(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
