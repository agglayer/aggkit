package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestXxx(t *testing.T) {
	params := &CertificateBuildParams{
		FromBlock: 100,
		ToBlock:   200,
	}
	_, err := params.Range(100, 0)
	require.Error(t, err, "should return an error for invalid range")
}
