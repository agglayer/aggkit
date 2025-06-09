package types

import (
	"database/sql/driver"
	"fmt"
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
