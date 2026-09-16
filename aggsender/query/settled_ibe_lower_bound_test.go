package query

import (
	"encoding/json"
	"math/big"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	aggsendertypes "github.com/agglayer/aggkit/aggsender/types"
	bridgetypes "github.com/agglayer/aggkit/bridgesync/types"
	aggkitdb "github.com/agglayer/aggkit/db"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// testBridgeExit returns a deterministic BridgeExit; changing destAddr produces a different hash
// while keeping every other field identical, which is used to build a non-matching bridge exit
// that nonetheless has the same GlobalIndex as the matching one.
func testBridgeExit(destAddr common.Address) *agglayertypes.BridgeExit {
	return &agglayertypes.BridgeExit{
		LeafType: bridgetypes.LeafTypeAsset,
		TokenInfo: &agglayertypes.TokenInfo{
			OriginNetwork:      1,
			OriginTokenAddress: common.HexToAddress("0xa"),
		},
		DestinationNetwork: 2,
		DestinationAddress: destAddr,
		Amount:             big.NewInt(100),
		Metadata:           []byte("metadata"),
	}
}

// marshalAgglayerCert marshals an agglayertypes.Certificate the same way aggsender does when
// storing a locally-produced signed certificate (see aggsender.go's SaveOrUpdateCertificate
// callers), so it round-trips through the same UnmarshalJSON code path the bounder uses.
func marshalAgglayerCert(t *testing.T, ibes ...*agglayertypes.ImportedBridgeExit) *string {
	t.Helper()
	cert := &agglayertypes.Certificate{
		NetworkID:           1,
		Height:              1,
		ImportedBridgeExits: ibes,
	}
	raw, err := json.Marshal(cert)
	require.NoError(t, err)
	s := string(raw)
	return &s
}

// storedCert builds a locally-sourced stored certificate at the given height/FromBlock, whose
// signed certificate contains the given imported bridge exits.
func storedCert(
	t *testing.T,
	height, fromBlock uint64,
	ibes ...*agglayertypes.ImportedBridgeExit,
) *aggsendertypes.Certificate {
	t.Helper()
	return &aggsendertypes.Certificate{
		Header: &aggsendertypes.CertificateHeader{
			Height:     height,
			CertSource: aggsendertypes.CertificateSourceLocal,
			FromBlock:  fromBlock,
		},
		SignedCertificate: marshalAgglayerCert(t, ibes...),
	}
}

func TestLowerBoundForSettledIBE_MatchAtLastSettledCertificate(t *testing.T) {
	t.Parallel()

	targetGlobalIndex := &agglayertypes.GlobalIndex{MainnetFlag: true, LeafIndex: 5}
	targetBridgeExit := testBridgeExit(common.HexToAddress("0x1"))
	settledIBE := &agglayertypes.SettledImportedBridgeExit{
		GlobalIndex:    targetGlobalIndex.ToBigInt(),
		BridgeExitHash: targetBridgeExit.Hash(),
	}
	matchingIBE := &agglayertypes.ImportedBridgeExit{BridgeExit: targetBridgeExit, GlobalIndex: targetGlobalIndex}

	storage := mocks.NewAggSenderStorage(t)
	storage.EXPECT().GetLastSettledCertificate().Return(&aggsendertypes.CertificateHeader{Height: 10}, nil)
	storage.EXPECT().GetCertificateByHeight(uint64(10)).Return(storedCert(t, 10, 42, matchingIBE), nil)

	bounder := NewStorageIBELowerBounder(storage)
	bound, found, err := bounder.LowerBoundForSettledIBE(t.Context(), settledIBE)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(42), bound)
}

func TestLowerBoundForSettledIBE_MatchSeveralCertificatesBack(t *testing.T) {
	t.Parallel()

	targetGlobalIndex := &agglayertypes.GlobalIndex{MainnetFlag: true, LeafIndex: 5}
	targetBridgeExit := testBridgeExit(common.HexToAddress("0x1"))
	settledIBE := &agglayertypes.SettledImportedBridgeExit{
		GlobalIndex:    targetGlobalIndex.ToBigInt(),
		BridgeExitHash: targetBridgeExit.Hash(),
	}
	matchingIBE := &agglayertypes.ImportedBridgeExit{BridgeExit: targetBridgeExit, GlobalIndex: targetGlobalIndex}
	nonMatchingIBE := &agglayertypes.ImportedBridgeExit{
		BridgeExit:  testBridgeExit(common.HexToAddress("0x2")),
		GlobalIndex: &agglayertypes.GlobalIndex{MainnetFlag: true, LeafIndex: 99},
	}

	storage := mocks.NewAggSenderStorage(t)
	storage.EXPECT().GetLastSettledCertificate().Return(&aggsendertypes.CertificateHeader{Height: 10}, nil)
	storage.EXPECT().GetCertificateByHeight(uint64(10)).Return(storedCert(t, 10, 100, nonMatchingIBE), nil)
	storage.EXPECT().GetCertificateByHeight(uint64(9)).Return(storedCert(t, 9, 90, nonMatchingIBE), nil)
	storage.EXPECT().GetCertificateByHeight(uint64(8)).Return(storedCert(t, 8, 80, nonMatchingIBE), nil)
	storage.EXPECT().GetCertificateByHeight(uint64(7)).Return(storedCert(t, 7, 70, matchingIBE), nil)

	bounder := NewStorageIBELowerBounder(storage)
	bound, found, err := bounder.LowerBoundForSettledIBE(t.Context(), settledIBE)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(70), bound)
}

func TestLowerBoundForSettledIBE_StopsOnAggLayerSourcedCertificate(t *testing.T) {
	t.Parallel()

	targetGlobalIndex := &agglayertypes.GlobalIndex{MainnetFlag: true, LeafIndex: 5}
	targetBridgeExit := testBridgeExit(common.HexToAddress("0x1"))
	settledIBE := &agglayertypes.SettledImportedBridgeExit{
		GlobalIndex:    targetGlobalIndex.ToBigInt(),
		BridgeExitHash: targetBridgeExit.Hash(),
	}
	nonMatchingIBE := &agglayertypes.ImportedBridgeExit{
		BridgeExit:  testBridgeExit(common.HexToAddress("0x2")),
		GlobalIndex: &agglayertypes.GlobalIndex{MainnetFlag: true, LeafIndex: 99},
	}
	// The matching IBE lives at height 8, but height 9 is AggLayer-sourced, so the walk must give
	// up at height 9 without ever calling GetCertificateByHeight(8).
	matchingIBE := &agglayertypes.ImportedBridgeExit{BridgeExit: targetBridgeExit, GlobalIndex: targetGlobalIndex}

	storage := mocks.NewAggSenderStorage(t)
	storage.EXPECT().GetLastSettledCertificate().Return(&aggsendertypes.CertificateHeader{Height: 10}, nil)
	storage.EXPECT().GetCertificateByHeight(uint64(10)).Return(storedCert(t, 10, 100, nonMatchingIBE), nil)
	storage.EXPECT().GetCertificateByHeight(uint64(9)).Return(&aggsendertypes.Certificate{
		Header: &aggsendertypes.CertificateHeader{
			Height:     9,
			CertSource: aggsendertypes.CertificateSourceAggLayer,
			FromBlock:  90,
		},
		SignedCertificate: marshalAgglayerCert(t, matchingIBE),
	}, nil)

	bounder := NewStorageIBELowerBounder(storage)
	bound, found, err := bounder.LowerBoundForSettledIBE(t.Context(), settledIBE)
	require.NoError(t, err)
	require.False(t, found)
	require.Equal(t, uint64(0), bound)
	storage.AssertNotCalled(t, "GetCertificateByHeight", uint64(8))
}

func TestLowerBoundForSettledIBE_StopsOnMissingCertificate(t *testing.T) {
	t.Parallel()

	targetGlobalIndex := &agglayertypes.GlobalIndex{MainnetFlag: true, LeafIndex: 5}
	targetBridgeExit := testBridgeExit(common.HexToAddress("0x1"))
	settledIBE := &agglayertypes.SettledImportedBridgeExit{
		GlobalIndex:    targetGlobalIndex.ToBigInt(),
		BridgeExitHash: targetBridgeExit.Hash(),
	}
	nonMatchingIBE := &agglayertypes.ImportedBridgeExit{
		BridgeExit:  testBridgeExit(common.HexToAddress("0x2")),
		GlobalIndex: &agglayertypes.GlobalIndex{MainnetFlag: true, LeafIndex: 99},
	}

	storage := mocks.NewAggSenderStorage(t)
	storage.EXPECT().GetLastSettledCertificate().Return(&aggsendertypes.CertificateHeader{Height: 10}, nil)
	storage.EXPECT().GetCertificateByHeight(uint64(10)).Return(storedCert(t, 10, 100, nonMatchingIBE), nil)
	storage.EXPECT().GetCertificateByHeight(uint64(9)).Return(nil, aggkitdb.ErrNotFound)

	bounder := NewStorageIBELowerBounder(storage)
	bound, found, err := bounder.LowerBoundForSettledIBE(t.Context(), settledIBE)
	require.NoError(t, err)
	require.False(t, found)
	require.Equal(t, uint64(0), bound)
	storage.AssertNotCalled(t, "GetCertificateByHeight", uint64(8))
}

func TestLowerBoundForSettledIBE_BoundedWalkTerminatesAtLimit(t *testing.T) {
	t.Parallel()

	targetGlobalIndex := &agglayertypes.GlobalIndex{MainnetFlag: true, LeafIndex: 5}
	targetBridgeExit := testBridgeExit(common.HexToAddress("0x1"))
	settledIBE := &agglayertypes.SettledImportedBridgeExit{
		GlobalIndex:    targetGlobalIndex.ToBigInt(),
		BridgeExitHash: targetBridgeExit.Hash(),
	}
	nonMatchingIBE := &agglayertypes.ImportedBridgeExit{
		BridgeExit:  testBridgeExit(common.HexToAddress("0x2")),
		GlobalIndex: &agglayertypes.GlobalIndex{MainnetFlag: true, LeafIndex: 99},
	}
	nonMatchingSignedCert := marshalAgglayerCert(t, nonMatchingIBE)

	const startHeight = 10000

	storage := mocks.NewAggSenderStorage(t)
	storage.EXPECT().GetLastSettledCertificate().Return(&aggsendertypes.CertificateHeader{Height: startHeight}, nil)

	callCount := 0
	storage.EXPECT().GetCertificateByHeight(mock.Anything).RunAndReturn(
		func(height uint64) (*aggsendertypes.Certificate, error) {
			callCount++
			return &aggsendertypes.Certificate{
				Header: &aggsendertypes.CertificateHeader{
					Height:     height,
					CertSource: aggsendertypes.CertificateSourceLocal,
					FromBlock:  height,
				},
				SignedCertificate: nonMatchingSignedCert,
			}, nil
		})

	bounder := NewStorageIBELowerBounder(storage)
	bound, found, err := bounder.LowerBoundForSettledIBE(t.Context(), settledIBE)
	require.NoError(t, err)
	require.False(t, found)
	require.Equal(t, uint64(0), bound)
	require.Equal(t, maxSettledIBELowerBoundWalk, callCount)
}

func TestLowerBoundForSettledIBE_GlobalIndexMatchWithDifferentHashIsNotAMatch(t *testing.T) {
	t.Parallel()

	targetGlobalIndex := &agglayertypes.GlobalIndex{MainnetFlag: true, LeafIndex: 5}
	targetBridgeExit := testBridgeExit(common.HexToAddress("0x1"))
	settledIBE := &agglayertypes.SettledImportedBridgeExit{
		GlobalIndex:    targetGlobalIndex.ToBigInt(),
		BridgeExitHash: targetBridgeExit.Hash(),
	}
	// sameGlobalIndexDifferentHash shares the target's GlobalIndex but has a different bridge
	// exit (different destination address), so it hashes differently and must NOT be accepted.
	sameGlobalIndexDifferentHash := &agglayertypes.ImportedBridgeExit{
		BridgeExit:  testBridgeExit(common.HexToAddress("0xdead")),
		GlobalIndex: targetGlobalIndex,
	}

	storage := mocks.NewAggSenderStorage(t)
	storage.EXPECT().GetLastSettledCertificate().Return(&aggsendertypes.CertificateHeader{Height: 5}, nil)
	storage.EXPECT().GetCertificateByHeight(uint64(5)).
		Return(storedCert(t, 5, 50, sameGlobalIndexDifferentHash), nil)
	storage.EXPECT().GetCertificateByHeight(uint64(4)).Return(nil, aggkitdb.ErrNotFound)

	bounder := NewStorageIBELowerBounder(storage)
	bound, found, err := bounder.LowerBoundForSettledIBE(t.Context(), settledIBE)
	require.NoError(t, err)
	require.False(t, found)
	require.Equal(t, uint64(0), bound)
}
