package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEpochStatus_String(t *testing.T) {
	epochStatus := EpochStatus{
		Epoch:        1,
		PercentEpoch: 0.52143244564354354,
	}
	res := epochStatus.String()
	require.Equal(t, "EpochStatus: [1, 52.14%]", res)
}
