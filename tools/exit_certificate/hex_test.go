package exit_certificate

import (
	"math"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToBlockTag(t *testing.T) {
	t.Parallel()
	require.Equal(t, "0x0", toBlockTag(0))
	require.Equal(t, "0x1", toBlockTag(1))
	require.Equal(t, "0x64", toBlockTag(100))
	require.Equal(t, "0xff", toBlockTag(255))
	require.Equal(t, "0x100", toBlockTag(256))
}

func TestParseDecimalBigInt_Valid(t *testing.T) {
	t.Parallel()
	require.Equal(t, big.NewInt(12345), parseDecimalBigInt("12345"))
}

func TestParseDecimalBigInt_Empty(t *testing.T) {
	t.Parallel()
	require.Equal(t, new(big.Int), parseDecimalBigInt(""))
}

func TestParseDecimalBigInt_Invalid(t *testing.T) {
	t.Parallel()
	require.Equal(t, new(big.Int), parseDecimalBigInt("not-a-number"))
}

func TestSafeUint32_OK(t *testing.T) {
	t.Parallel()
	v, err := safeUint32(big.NewInt(42))
	require.NoError(t, err)
	require.Equal(t, uint32(42), v)
}

func TestSafeUint32_MaxValue(t *testing.T) {
	t.Parallel()
	v, err := safeUint32(new(big.Int).SetUint64(math.MaxUint32))
	require.NoError(t, err)
	require.Equal(t, uint32(math.MaxUint32), v)
}

func TestSafeUint32_Overflow(t *testing.T) {
	t.Parallel()
	_, err := safeUint32(new(big.Int).SetUint64(math.MaxUint32 + 1))
	require.Error(t, err)
	require.Contains(t, err.Error(), "overflows uint32")
}

func TestSafeUint8_OK(t *testing.T) {
	t.Parallel()
	v, err := safeUint8(big.NewInt(200))
	require.NoError(t, err)
	require.Equal(t, uint8(200), v)
}

func TestSafeUint8_MaxValue(t *testing.T) {
	t.Parallel()
	v, err := safeUint8(big.NewInt(math.MaxUint8))
	require.NoError(t, err)
	require.Equal(t, uint8(math.MaxUint8), v)
}

func TestSafeUint8_Overflow(t *testing.T) {
	t.Parallel()
	_, err := safeUint8(big.NewInt(256))
	require.Error(t, err)
	require.Contains(t, err.Error(), "overflows uint8")
}
