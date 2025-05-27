package types

import (
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
