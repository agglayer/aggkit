package types

import (
	"fmt"
	"math/big"
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

func TestCertificateInfo_String(t *testing.T) {
	t.Run("NilCertificateInfo", func(t *testing.T) {
		var certInfo *CertificateInfo
		require.Equal(t, NilStr, certInfo.String())
	})

	t.Run("CompleteCertificateInfo", func(t *testing.T) {
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

		certInfo := &CertificateInfo{
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
			AggchainProof:           aggchainProof,
			FinalizedL1InfoTreeRoot: &finalizedL1InfoTreeRoot,
		}

		expected := fmt.Sprintf("aggsender.CertificateInfo: \n"+
			"Height: %d \n"+
			"RetryCount: %d \n"+
			"CertificateID: %s \n"+
			"PreviousLocalExitRoot: %s \n"+
			"NewLocalExitRoot: %s \n"+
			"Status: %s \n"+
			"FromBlock: %d \n"+
			"ToBlock: %d \n"+
			"CreatedAt: %s \n"+
			"UpdatedAt: %s \n"+
			"AggchainProof: %s \n"+
			"FinalizedL1InfoTreeRoot: %s \n",
			certInfo.Height,
			certInfo.RetryCount,
			certInfo.CertificateID.String(),
			previousLocalExitRoot.String(),
			certInfo.NewLocalExitRoot.String(),
			certInfo.Status.String(),
			certInfo.FromBlock,
			certInfo.ToBlock,
			time.Unix(int64(certInfo.CreatedAt), 0),
			time.Unix(int64(certInfo.UpdatedAt), 0),
			aggchainProof.String(),
			finalizedL1InfoTreeRoot.String(),
		)

		require.Equal(t, expected, certInfo.String())
	})
}
