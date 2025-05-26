package types

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestMetadataConversions_V0_toBlock_Only(t *testing.T) {
	toBlock := uint64(123567890)
	hash := common.BigToHash(new(big.Int).SetUint64(toBlock))
	meta := NewCertificateMetadataFromHash(hash)
	require.Equal(t, toBlock, meta.ToBlock)
	metabuild := meta.ToHash()
	require.Equal(t, hash, metabuild)
}

func TestMetadataConversions_V1(t *testing.T) {
	meta := &CertificateMetadata{
		Version:   CertificateMetadataV1,
		FromBlock: 123567890,
		Offset:    1000,
		CreatedAt: 123,
	}
	hash := meta.ToHash()
	metabuild := NewCertificateMetadataFromHash(hash)
	require.Equal(t, meta.FromBlock, metabuild.FromBlock)
	require.Equal(t, meta.Offset, metabuild.Offset)
	require.Equal(t, meta.CreatedAt, metabuild.CreatedAt)
	require.Equal(t, meta.Version, metabuild.Version)
}

func TestMetadataConversions_V2(t *testing.T) {
	meta := &CertificateMetadata{
		Version:   CertificateMetadataV2,
		FromBlock: 123567890,
		Offset:    1000,
		CreatedAt: 123,
		CertType:  32,
	}
	hash := meta.ToHash()
	metabuild := NewCertificateMetadataFromHash(hash)
	require.Equal(t, meta.FromBlock, metabuild.FromBlock)
	require.Equal(t, meta.Offset, metabuild.Offset)
	require.Equal(t, meta.CreatedAt, metabuild.CreatedAt)
	require.Equal(t, meta.Version, metabuild.Version)
	require.Equal(t, meta.CertType, metabuild.CertType)
}

func TestMetadataConversions_UnknownMetadataVersion(t *testing.T) {
	b := make([]byte, common.HashLength)
	b[0] = 254 // Unknown version
	hash := common.BytesToHash(b)
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("NewCertificateMetadataFromHash for unknown version did not panic")
		}
	}()
	_ = NewCertificateMetadataFromHash(hash)
}

func TestMetadataConversions(t *testing.T) {
	fromBlock := uint64(123567890)
	offset := uint32(1000)
	createdAt := uint32(0)
	certType := uint8(123)
	meta := NewCertificateMetadata(fromBlock, offset, createdAt, certType)
	c := meta.ToHash()
	extractBlock := NewCertificateMetadataFromHash(c)
	require.Equal(t, fromBlock, extractBlock.FromBlock)
	require.Equal(t, offset, extractBlock.Offset)
	require.Equal(t, createdAt, extractBlock.CreatedAt)
	require.Equal(t, certType, extractBlock.CertType)
	require.Equal(t, CertificateMetadataV2, extractBlock.Version)
}
