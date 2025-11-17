package storage

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDBRuntimeDataIsCompatible(t *testing.T) {
	sut := DBRuntimeData{
		NetworkID:   1,
		DataVersion: 1,
	}
	tests := []struct {
		name        string
		storage     DBRuntimeData
		expectError string
	}{
		{
			name: "compatible runtime data",
			storage: DBRuntimeData{
				NetworkID:   1,
				DataVersion: 1,
			},
			expectError: "",
		},
		{
			name: "incompatible network ID",
			storage: DBRuntimeData{
				NetworkID:   2,
				DataVersion: 1,
			},
			expectError: "network ID mismatch",
		},
		{
			name: "incompatible data version",
			storage: DBRuntimeData{
				NetworkID:   1,
				DataVersion: 2,
			},
			expectError: "data version mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := sut.IsCompatible(tt.storage)
			if tt.expectError == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tt.expectError)
			}
		})
	}

}

func TestDBRuntimeDataString(t *testing.T) {
	sut := DBRuntimeData{
		NetworkID:   42,
		DataVersion: 3,
	}
	expected := "NetworkID: 42, DataVersion: 3"
	require.Equal(t, expected, sut.String())
}
