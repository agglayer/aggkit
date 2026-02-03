package multidownloader

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestRuntimeData_String(t *testing.T) {
	data := RuntimeData{
		ChainID: 1,
		Addresses: []common.Address{
			common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
			common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"),
		},
	}

	expected := "ChainID: 1, Addresses: 0x1234567890abcdef1234567890abcdef12345678, 0xabcdefabcdefabcdefabcdefabcdefabcdefabcd, "
	require.Equal(t, expected, data.String())
}

func TestRuntimeData_IsCompatible_Success(t *testing.T) {
	data1 := RuntimeData{
		ChainID: 1,
		Addresses: []common.Address{
			common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
			common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"),
		},
	}

	data2 := RuntimeData{
		ChainID: 1,
		Addresses: []common.Address{
			common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
			common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"),
		},
	}

	err := data1.IsCompatible(data2)
	require.NoError(t, err)
}

func TestRuntimeData_IsCompatible_ChainIDMismatch(t *testing.T) {
	data1 := RuntimeData{
		ChainID: 1,
		Addresses: []common.Address{
			common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
		},
	}

	data2 := RuntimeData{
		ChainID: 2,
		Addresses: []common.Address{
			common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
		},
	}

	err := data1.IsCompatible(data2)
	require.Error(t, err)
	require.Contains(t, err.Error(), "chain ID mismatch")
}

func TestRuntimeData_IsCompatible_AddressLengthMismatch(t *testing.T) {
	data1 := RuntimeData{
		ChainID: 1,
		Addresses: []common.Address{
			common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
		},
	}

	data2 := RuntimeData{
		ChainID: 1,
		Addresses: []common.Address{
			common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
			common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"),
		},
	}

	err := data1.IsCompatible(data2)
	require.Error(t, err)
	require.Contains(t, err.Error(), "addresses len mismatch")
}

func TestRuntimeData_IsCompatible_AddressMismatch(t *testing.T) {
	data1 := RuntimeData{
		ChainID: 1,
		Addresses: []common.Address{
			common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
		},
	}

	data2 := RuntimeData{
		ChainID: 1,
		Addresses: []common.Address{
			common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"),
		},
	}

	err := data1.IsCompatible(data2)
	require.Error(t, err)
	require.Contains(t, err.Error(), "addresses[0] mismatch")
}
