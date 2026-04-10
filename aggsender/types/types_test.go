package types

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestCertificate_String(t *testing.T) {
	t.Run("NilCertificate", func(t *testing.T) {
		var certInfo *Certificate
		require.Equal(t, NilStr, certInfo.String())
	})

	t.Run("CompleteCertificate", func(t *testing.T) {
		previousLocalExitRoot := common.HexToHash("0xabc123")
		finalizedL1InfoTreeRoot := common.HexToHash("0xdef456")
		aggchainProof := &AggchainProof{
			LastProvenBlock: 100,
			EndBlock:        200,
			CustomChainData: []byte{0x01, 0x02},
			LocalExitRoot:   common.HexToHash("0x123abc"),
			AggchainParams:  common.HexToHash("0x456def"),
			Context:         map[string][]byte{"key": []byte("value")},
			SP1StarkProof: &SP1StarkProof{
				Version: "1.0",
				Proof:   []byte{0x03, 0x04},
				Vkey:    []byte{0x05, 0x06},
			},
		}

		cert := &Certificate{
			Header: &CertificateHeader{
				Height:                  10,
				RetryCount:              2,
				CertificateID:           common.HexToHash("0x789abc"),
				PreviousLocalExitRoot:   &previousLocalExitRoot,
				NewLocalExitRoot:        common.HexToHash("0x123456"),
				FromBlock:               1000,
				ToBlock:                 2000,
				Status:                  agglayertypes.CertificateStatus(1),
				CreatedAt:               uint32(time.Now().Unix()),
				UpdatedAt:               uint32(time.Now().Unix()),
				FinalizedL1InfoTreeRoot: &finalizedL1InfoTreeRoot,
			},
			AggchainProof: aggchainProof,
		}

		expected := fmt.Sprintf("aggsender.Certificate: \n"+
			"Header: %s \n"+
			"AggchainProof: %s \n",
			cert.Header.String(),
			cert.AggchainProof.String(),
		)

		require.Equal(t, expected, cert.String())
	})

	t.Run("CreatedAt and UpdatedAt are 0", func(t *testing.T) {
		cert := &Certificate{
			Header: &CertificateHeader{
				CreatedAt: 0,
				UpdatedAt: 0,
			},
		}

		certStr := cert.String()

		require.Containsf(t, certStr, "CreatedAt: N/A", "Expected CreatedAt to be N/A")
		require.Containsf(t, certStr, "UpdatedAt: N/A", "Expected UpdatedAt to be N/A")
	})
}

func TestCertificateType_String(t *testing.T) {
	tests := []struct {
		input    CertificateType
		expected string
	}{
		{CertificateTypeFEP, CertificateTypeFEPStr},
		{CertificateTypePP, CertificateTypePPStr},
		{CertificateTypeOptimistic, CertificateTypeOptimisticStr},
		{CertificateTypeUnknown, CertificateTypeUnknownStr},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("CertificateType_%d", tt.input), func(t *testing.T) {
			require.Equal(t, tt.expected, tt.input.String())
		})
	}
}

func TestCertificateType_Value(t *testing.T) {
	tests := []struct {
		input    CertificateType
		expected driver.Value
	}{
		{CertificateTypeFEP, CertificateTypeFEPStr},
		{CertificateTypePP, CertificateTypePPStr},
		{CertificateTypeOptimistic, CertificateTypeOptimisticStr},
		{CertificateTypeUnknown, CertificateTypeUnknownStr},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("CertificateType_Value_%d", tt.input), func(t *testing.T) {
			value, err := tt.input.Value()
			require.NoError(t, err)
			require.Equal(t, tt.expected, value)
		})
	}
}

func TestCertificateType_Scan(t *testing.T) {
	tests := []struct {
		input    interface{}
		expected CertificateType
		hasError bool
	}{
		{CertificateTypeFEPStr, CertificateTypeFEP, false},
		{CertificateTypePPStr, CertificateTypePP, false},
		{CertificateTypeOptimisticStr, CertificateTypeOptimistic, false},
		{CertificateTypeUnknownStr, CertificateTypeUnknown, false},
		{"invalid", CertificateTypeUnknown, true},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("CertificateType_Scan_%v", tt.input), func(t *testing.T) {
			var ct CertificateType
			err := ct.Scan(tt.input)
			if tt.hasError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expected, ct)
			}
		})
	}
}

func TestCertificateType_ToInt(t *testing.T) {
	tests := []struct {
		input    CertificateType
		expected uint8
	}{
		{CertificateTypeFEP, uint8(CertificateTypeFEP)},
		{CertificateTypePP, uint8(CertificateTypePP)},
		{CertificateTypeOptimistic, uint8(CertificateTypeOptimistic)},
		{CertificateTypeUnknown, uint8(CertificateTypeUnknown)},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("CertificateType_ToInt_%d", tt.input), func(t *testing.T) {
			require.Equal(t, tt.expected, tt.input.ToInt())
		})
	}
}

func TestNewCertificateTypeFromInt(t *testing.T) {
	tests := []struct {
		input    uint8
		expected CertificateType
	}{
		{uint8(CertificateTypeFEP), CertificateTypeFEP},
		{uint8(CertificateTypePP), CertificateTypePP},
		{uint8(CertificateTypeOptimistic), CertificateTypeOptimistic},
		{uint8(CertificateTypeUnknown), CertificateTypeUnknown},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("NewCertificateTypeFromInt_%d", tt.input), func(t *testing.T) {
			require.Equal(t, tt.expected, NewCertificateTypeFromInt(tt.input))
		})
	}
}

func TestNewCertificateTypeFromStr(t *testing.T) {
	tests := []struct {
		input    string
		expected CertificateType
		hasError bool
	}{
		{CertificateTypeFEPStr, CertificateTypeFEP, false},
		{CertificateTypePPStr, CertificateTypePP, false},
		{CertificateTypeOptimisticStr, CertificateTypeOptimistic, false},
		{CertificateTypeUnknownStr, CertificateTypeUnknown, false},
		{"invalid", CertificateTypeUnknown, true},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("NewCertificateTypeFromStr_%s", tt.input), func(t *testing.T) {
			result, err := NewCertificateTypeFromStr(tt.input)
			if tt.hasError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestCertificateSource_String(t *testing.T) {
	tests := []struct {
		input    CertificateSource
		expected string
	}{
		{CertificateSourceAggLayer, "agglayer"},
		{CertificateSourceLocal, "local"},
		{CertificateSourceUnknown, ""},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("CertificateSource_%s", tt.input), func(t *testing.T) {
			require.Equal(t, tt.expected, tt.input.String())
		})
	}
}

func TestCertificateHeader_ElapsedTimeSinceCreation(t *testing.T) {
	t.Run("NilCertificateHeader", func(t *testing.T) {
		var ch *CertificateHeader
		require.Equal(t, NAStr, ch.ElapsedTimeSinceCreationString())
	})

	t.Run("CreatedAtIsZero", func(t *testing.T) {
		ch := &CertificateHeader{CreatedAt: 0}
		require.Equal(t, NAStr, ch.ElapsedTimeSinceCreationString())
	})

	t.Run("CreatedAtIsNow", func(t *testing.T) {
		now := uint32(time.Now().Unix())
		ch := &CertificateHeader{CreatedAt: now}
		result := ch.ElapsedTimeSinceCreationString()
		// Should be a duration string, e.g., "0s"
		require.Contains(t, result, "s")
	})

	t.Run("CreatedAtIsPast", func(t *testing.T) {
		past := uint32(time.Now().Add(-10 * time.Second).Unix())
		ch := &CertificateHeader{CreatedAt: past}
		result := ch.ElapsedTimeSinceCreationString()
		// Should be at least 10s
		dur, err := time.ParseDuration(result)
		require.NoError(t, err)
		require.GreaterOrEqual(t, int64(dur.Seconds()), int64(10))
	})
}

func TestAggsenderMode_Scan(t *testing.T) {
	tests := []struct {
		input       interface{}
		expected    AggsenderMode
		expectedErr string
	}{
		{"PessimisticProof", AggsenderMode("PessimisticProof"), ""},
		{"AggchainProof", AggsenderMode("AggchainProof"), ""},
		{"Auto", AggsenderMode("Auto"), ""},
		{"invalid", AggsenderMode(""), "unknown AggsenderMode"},
		{123, AggsenderMode(""), "expected string, got int"}, // Non-string input
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("AggsenderMode_Scan_%v", tt.input), func(t *testing.T) {
			var mode AggsenderMode
			err := mode.Scan(tt.input)
			if tt.expectedErr != "" {
				require.ErrorContains(t, err, tt.expectedErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expected, mode)
			}
		})
	}
}

func TestNewAggsenderMode(t *testing.T) {
	tests := []struct {
		input       string
		expected    AggsenderMode
		expectedErr string
	}{
		{"PessimisticProof", PessimisticProofMode, ""},
		{"pessimisticproof", PessimisticProofMode, ""},
		{"AggchainProof", AggchainProofMode, ""},
		{"aggchainproof", AggchainProofMode, ""},
		{"Auto", AutoMode, ""},
		{"auto", AutoMode, ""},
		{"invalid", "", "unknown AggsenderMode"},
		{"", "", "unknown AggsenderMode"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("NewAggsenderMode_%s", tt.input), func(t *testing.T) {
			result, err := NewAggsenderMode(tt.input)
			if tt.expectedErr != "" {
				require.ErrorContains(t, err, tt.expectedErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestAggsenderMode_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mode    func() *AggsenderMode
		wantErr bool
		errMsg  string
	}{
		{
			name:    "NilAggsenderMode",
			mode:    func() *AggsenderMode { return nil },
			wantErr: true,
			errMsg:  "AggsenderMode is nil",
		},
		{
			name: "ValidPessimisticProofMode",
			mode: func() *AggsenderMode {
				m := PessimisticProofMode
				return &m
			},
			wantErr: false,
		},
		{
			name: "ValidAggchainProofMode",
			mode: func() *AggsenderMode {
				m := AggchainProofMode
				return &m
			},
			wantErr: false,
		},
		{
			name: "ValidAutoMode",
			mode: func() *AggsenderMode {
				m := AutoMode
				return &m
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mode := tt.mode()
			err := mode.Validate()
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestSettledBlocks_String(t *testing.T) {
	t.Parallel()

	ibeGlobalIndex := big.NewInt(42)
	ibeBridgeExitHash := common.HexToHash("0xabcd")

	tests := []struct {
		name     string
		input    SettledBlocks
		expected string
	}{
		{
			name:  "all zero, no errors",
			input: SettledBlocks{},
			expected: "SettledBlocks{LastBridgeExitBlock: 0, LastImportedBridgeExitBlock: 0, " +
				"LastSettledL2BlockNum: 0, SettledImportedBridgeExit: nil}",
		},
		{
			name: "all sources with values, no errors",
			input: SettledBlocks{
				LastBridgeExitBlock:         100,
				LastImportedBridgeExitBlock: 200,
				LastSettledL2BlockNum:       300,
			},
			expected: "SettledBlocks{LastBridgeExitBlock: 100, LastImportedBridgeExitBlock: 200, " +
				"LastSettledL2BlockNum: 300, SettledImportedBridgeExit: nil}",
		},
		{
			name: "bridge exit block error hides its value",
			input: SettledBlocks{
				LastBridgeExitBlock:    99,
				LastBridgeExitBlockErr: errors.New("bridge error"),
			},
			expected: "SettledBlocks{LastBridgeExitBlock: err(bridge error), LastImportedBridgeExitBlock: 0, " +
				"LastSettledL2BlockNum: 0, SettledImportedBridgeExit: nil}",
		},
		{
			name: "imported bridge exit error hides its value",
			input: SettledBlocks{
				LastBridgeExitBlock:            50,
				LastImportedBridgeExitBlock:    99,
				LastImportedBridgeExitBlockErr: errors.New("ibe error"),
			},
			expected: "SettledBlocks{LastBridgeExitBlock: 50, LastImportedBridgeExitBlock: err(ibe error), " +
				"LastSettledL2BlockNum: 0, SettledImportedBridgeExit: nil}",
		},
		{
			name: "L2 block error hides its value",
			input: SettledBlocks{
				LastSettledL2BlockNum:    99,
				LastSettledL2BlockNumErr: errors.New("l2 error"),
			},
			expected: "SettledBlocks{LastBridgeExitBlock: 0, LastImportedBridgeExitBlock: 0, " +
				"LastSettledL2BlockNum: err(l2 error), SettledImportedBridgeExit: nil}",
		},
		{
			name: "with SettledImportedBridgeExit set",
			input: SettledBlocks{
				LastBridgeExitBlock:         10,
				LastImportedBridgeExitBlock: 20,
				LastSettledL2BlockNum:       30,
				SettledImportedBridgeExit: &agglayertypes.SettledImportedBridgeExit{
					GlobalIndex:    ibeGlobalIndex,
					BridgeExitHash: ibeBridgeExitHash,
				},
			},
			expected: fmt.Sprintf(
				"SettledBlocks{LastBridgeExitBlock: 10, LastImportedBridgeExitBlock: 20, "+
					"LastSettledL2BlockNum: 30, SettledImportedBridgeExit: {GlobalIndex: %s, BridgeExitHash: %s}}",
				ibeGlobalIndex.String(), ibeBridgeExitHash.String(),
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.expected, tt.input.String())
		})
	}
}
