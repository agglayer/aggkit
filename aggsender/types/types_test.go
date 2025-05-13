package types

import (
	"fmt"
	"math/big"
	"reflect"
	"testing"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestMetadataConversions_toBlock_Only(t *testing.T) {
	toBlock := uint64(123567890)
	hash := common.BigToHash(new(big.Int).SetUint64(toBlock))
	meta := NewCertificateMetadataFromHash(hash)
	require.Equal(t, toBlock, meta.ToBlock)
}

func TestMetadataConversions(t *testing.T) {
	fromBlock := uint64(123567890)
	offset := uint32(1000)
	createdAt := uint32(0)
	meta := NewCertificateMetadata(fromBlock, offset, createdAt)
	c := meta.ToHash()
	extractBlock := NewCertificateMetadataFromHash(c)
	require.Equal(t, fromBlock, extractBlock.FromBlock)
	require.Equal(t, offset, extractBlock.Offset)
	require.Equal(t, createdAt, extractBlock.CreatedAt)
}

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

func TestCertificateFieldsMatchCertificateInfo(t *testing.T) {
	// Check that all fields in CertificateInfo are present in Certificate
	certificateInfoType := reflect.TypeOf(CertificateInfo{})

	for i := range certificateInfoType.NumField() {
		field := certificateInfoType.Field(i)
		_, found := reflect.TypeOf(CertificateHeader{}).FieldByName(field.Name)
		if !found {
			_, found = reflect.TypeOf(Certificate{}).FieldByName(field.Name)
		}
		require.True(t, found, "Field %s is missing in Certificate", field.Name)
	}

	// Check that all fields in Certificate are present in CertificateInfo
	certificateHeaderType := reflect.TypeOf(CertificateHeader{})

	// Check that all fields in CertificateHeader are present in CertificateInfo
	for i := range certificateHeaderType.NumField() {
		field := certificateHeaderType.Field(i)
		_, found := certificateInfoType.FieldByName(field.Name)
		require.True(t, found, "Field %s from CertificateHeader is missing in CertificateInfo", field.Name)
	}

	// Check that all fields in Certificate are present in CertificateInfo
	certificateType := reflect.TypeOf(Certificate{})

	for i := range certificateType.NumField() {
		field := certificateType.Field(i)
		if field.Name == "Header" {
			continue
		}
		_, found := certificateInfoType.FieldByName(field.Name)
		require.True(t, found, "Field %s from Certificate is missing in CertificateInfo", field.Name)
	}
}
