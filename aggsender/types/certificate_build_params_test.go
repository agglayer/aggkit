package types

import (
	"testing"

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
