package multidownloader

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestRuntimeData_String(t *testing.T) {
	tests := []struct {
		name     string
		data     RuntimeData
		expected string
	}{
		{
			name: "empty addresses",
			data: RuntimeData{
				ChainID:   1,
				Addresses: []common.Address{},
			},
			expected: "ChainID: 1, Addresses: ",
		},
		{
			name: "single address",
			data: RuntimeData{
				ChainID:   1,
				Addresses: []common.Address{common.HexToAddress("0x123")},
			},
			expected: "ChainID: 1, Addresses: 0x0000000000000000000000000000000000000123, ",
		},
		{
			name: "two addresses",
			data: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
					common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"),
				},
			},
			expected: "ChainID: 1, Addresses: 0x1234567890AbcdEF1234567890aBcdef12345678, 0xABcdEFABcdEFabcdEfAbCdefabcdeFABcDEFabCD, ",
		},
		{
			name: "multiple addresses",
			data: RuntimeData{
				ChainID: 42,
				Addresses: []common.Address{
					common.HexToAddress("0x123"),
					common.HexToAddress("0x456"),
					common.HexToAddress("0x789"),
				},
			},
			expected: "ChainID: 42, Addresses: 0x0000000000000000000000000000000000000123, 0x0000000000000000000000000000000000000456, 0x0000000000000000000000000000000000000789, ",
		},
		{
			name: "zero chain ID",
			data: RuntimeData{
				ChainID:   0,
				Addresses: []common.Address{common.HexToAddress("0xabc")},
			},
			expected: "ChainID: 0, Addresses: 0x0000000000000000000000000000000000000aBc, ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.data.String()
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestRuntimeData_IsCompatible_Success(t *testing.T) {
	tests := []struct {
		name  string
		data1 RuntimeData
		data2 RuntimeData
	}{
		{
			name: "identical data with single address",
			data1: RuntimeData{
				ChainID:   1,
				Addresses: []common.Address{common.HexToAddress("0x123")},
			},
			data2: RuntimeData{
				ChainID:   1,
				Addresses: []common.Address{common.HexToAddress("0x123")},
			},
		},
		{
			name: "identical data with two addresses",
			data1: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
					common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"),
				},
			},
			data2: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
					common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"),
				},
			},
		},
		{
			name: "identical data with multiple addresses",
			data1: RuntimeData{
				ChainID: 42,
				Addresses: []common.Address{
					common.HexToAddress("0x123"),
					common.HexToAddress("0x456"),
					common.HexToAddress("0x789"),
				},
			},
			data2: RuntimeData{
				ChainID: 42,
				Addresses: []common.Address{
					common.HexToAddress("0x123"),
					common.HexToAddress("0x456"),
					common.HexToAddress("0x789"),
				},
			},
		},
		{
			name: "both have empty addresses",
			data1: RuntimeData{
				ChainID:   1,
				Addresses: []common.Address{},
			},
			data2: RuntimeData{
				ChainID:   1,
				Addresses: []common.Address{},
			},
		},
		{
			name: "zero chain ID with matching data",
			data1: RuntimeData{
				ChainID:   0,
				Addresses: []common.Address{common.HexToAddress("0x789")},
			},
			data2: RuntimeData{
				ChainID:   0,
				Addresses: []common.Address{common.HexToAddress("0x789")},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.data1.IsCompatible(tt.data2)
			require.NoError(t, err)
		})
	}
}

func TestRuntimeData_IsCompatible_ChainIDMismatch(t *testing.T) {
	tests := []struct {
		name     string
		data1    RuntimeData
		data2    RuntimeData
		chainID1 uint64
		chainID2 uint64
	}{
		{
			name: "different chain IDs with same address",
			data1: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
				},
			},
			data2: RuntimeData{
				ChainID: 2,
				Addresses: []common.Address{
					common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
				},
			},
			chainID1: 1,
			chainID2: 2,
		},
		{
			name: "chain ID 0 vs 1",
			data1: RuntimeData{
				ChainID:   0,
				Addresses: []common.Address{common.HexToAddress("0x123")},
			},
			data2: RuntimeData{
				ChainID:   1,
				Addresses: []common.Address{common.HexToAddress("0x123")},
			},
			chainID1: 0,
			chainID2: 1,
		},
		{
			name: "large chain ID difference",
			data1: RuntimeData{
				ChainID:   1,
				Addresses: []common.Address{common.HexToAddress("0x123")},
			},
			data2: RuntimeData{
				ChainID:   999999,
				Addresses: []common.Address{common.HexToAddress("0x123")},
			},
			chainID1: 1,
			chainID2: 999999,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.data1.IsCompatible(tt.data2)
			require.Error(t, err)
			require.Contains(t, err.Error(), "chain ID mismatch")
		})
	}
}

func TestRuntimeData_IsCompatible_AddressesLenMismatch(t *testing.T) {
	tests := []struct {
		name  string
		data1 RuntimeData
		data2 RuntimeData
	}{
		{
			name: "data1 has more addresses",
			data1: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
					common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"),
				},
			},
			data2: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
				},
			},
		},
		{
			name: "data2 has more addresses",
			data1: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
				},
			},
			data2: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
					common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"),
				},
			},
		},
		{
			name: "data1 empty, data2 has addresses",
			data1: RuntimeData{
				ChainID:   1,
				Addresses: []common.Address{},
			},
			data2: RuntimeData{
				ChainID:   1,
				Addresses: []common.Address{common.HexToAddress("0x123")},
			},
		},
		{
			name: "data1 has addresses, data2 empty",
			data1: RuntimeData{
				ChainID:   1,
				Addresses: []common.Address{common.HexToAddress("0x123")},
			},
			data2: RuntimeData{
				ChainID:   1,
				Addresses: []common.Address{},
			},
		},
		{
			name: "large difference in address count",
			data1: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x111"),
					common.HexToAddress("0x222"),
					common.HexToAddress("0x333"),
					common.HexToAddress("0x444"),
					common.HexToAddress("0x555"),
				},
			},
			data2: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x111"),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.data1.IsCompatible(tt.data2)
			require.Error(t, err)
			require.Contains(t, err.Error(), "addresses len mismatch")
		})
	}
}

func TestRuntimeData_IsCompatible_AddressMismatch(t *testing.T) {
	tests := []struct {
		name  string
		data1 RuntimeData
		data2 RuntimeData
		index int
	}{
		{
			name: "single address mismatch",
			data1: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
				},
			},
			data2: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"),
				},
			},
			index: 0,
		},
		{
			name: "first address differs",
			data1: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x123"),
					common.HexToAddress("0x456"),
				},
			},
			data2: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x789"),
					common.HexToAddress("0x456"),
				},
			},
			index: 0,
		},
		{
			name: "second address differs",
			data1: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x123"),
					common.HexToAddress("0x456"),
				},
			},
			data2: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x123"),
					common.HexToAddress("0x789"),
				},
			},
			index: 1,
		},
		{
			name: "middle address differs in longer list",
			data1: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x111"),
					common.HexToAddress("0x222"),
					common.HexToAddress("0x333"),
					common.HexToAddress("0x444"),
				},
			},
			data2: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x111"),
					common.HexToAddress("0x222"),
					common.HexToAddress("0x999"),
					common.HexToAddress("0x444"),
				},
			},
			index: 2,
		},
		{
			name: "last address differs",
			data1: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x111"),
					common.HexToAddress("0x222"),
					common.HexToAddress("0x333"),
				},
			},
			data2: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x111"),
					common.HexToAddress("0x222"),
					common.HexToAddress("0x999"),
				},
			},
			index: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.data1.IsCompatible(tt.data2)
			require.Error(t, err)
			require.Contains(t, err.Error(), "addresses")
			require.Contains(t, err.Error(), "mismatch")
		})
	}
}

func TestRuntimeData_IsCompatible_ErrorPrecedence(t *testing.T) {
	t.Run("chain ID mismatch takes precedence over address differences", func(t *testing.T) {
		data1 := RuntimeData{
			ChainID:   1,
			Addresses: []common.Address{common.HexToAddress("0x123")},
		}
		data2 := RuntimeData{
			ChainID:   2,
			Addresses: []common.Address{common.HexToAddress("0x456")},
		}

		err := data1.IsCompatible(data2)
		require.Error(t, err)
		require.Contains(t, err.Error(), "chain ID mismatch")
	})

	t.Run("length mismatch checked before address comparison", func(t *testing.T) {
		data1 := RuntimeData{
			ChainID:   1,
			Addresses: []common.Address{common.HexToAddress("0x123")},
		}
		data2 := RuntimeData{
			ChainID: 1,
			Addresses: []common.Address{
				common.HexToAddress("0x456"),
				common.HexToAddress("0x789"),
			},
		}

		err := data1.IsCompatible(data2)
		require.Error(t, err)
		require.Contains(t, err.Error(), "addresses len mismatch")
	})
}

func TestRuntimeData_IsCompatible_NilAddresses(t *testing.T) {
	t.Run("both nil addresses", func(t *testing.T) {
		data1 := RuntimeData{
			ChainID:   1,
			Addresses: nil,
		}
		data2 := RuntimeData{
			ChainID:   1,
			Addresses: nil,
		}

		err := data1.IsCompatible(data2)
		require.NoError(t, err)
	})

	t.Run("one nil, one empty", func(t *testing.T) {
		data1 := RuntimeData{
			ChainID:   1,
			Addresses: nil,
		}
		data2 := RuntimeData{
			ChainID:   1,
			Addresses: []common.Address{},
		}

		err := data1.IsCompatible(data2)
		require.NoError(t, err)
	})

	t.Run("nil vs non-empty", func(t *testing.T) {
		data1 := RuntimeData{
			ChainID:   1,
			Addresses: nil,
		}
		data2 := RuntimeData{
			ChainID:   1,
			Addresses: []common.Address{common.HexToAddress("0x123")},
		}

		err := data1.IsCompatible(data2)
		require.Error(t, err)
		require.Contains(t, err.Error(), "addresses len mismatch")
	})
}
