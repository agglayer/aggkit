package remove_ger

import (
	"context"
	"math/big"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

type stubL1GERLookup struct {
	timestamps map[common.Hash]*big.Int
}

func (s *stubL1GERLookup) GlobalExitRootMap(_ *bind.CallOpts, gerHash [32]byte) (*big.Int, error) {
	if ts, ok := s.timestamps[common.Hash(gerHash)]; ok {
		return ts, nil
	}
	return big.NewInt(0), nil
}

func TestDecodeClaimGERFromTxDataEtrog(t *testing.T) {
	t.Helper()

	contractABI, err := agglayerbridgel2.Agglayerbridgel2MetaData.GetAbi()
	require.NoError(t, err)

	var proofLocal [32][32]byte
	var proofRollup [32][32]byte
	globalIndex := big.NewInt(123)
	mainnetExitRoot := common.HexToHash("0x1111")
	rollupExitRoot := common.HexToHash("0x2222")
	expectedGER := l1infotreesync.CalculateGER(mainnetExitRoot, rollupExitRoot)

	txData, err := contractABI.Pack(
		"claimAsset",
		proofLocal,
		proofRollup,
		globalIndex,
		mainnetExitRoot,
		rollupExitRoot,
		uint32(0),
		common.HexToAddress("0x0000000000000000000000000000000000000001"),
		uint32(12),
		common.HexToAddress("0x0000000000000000000000000000000000000002"),
		big.NewInt(1),
		[]byte{},
	)
	require.NoError(t, err)

	ger, err := decodeClaimGERFromTxData(txData, globalIndex)
	require.NoError(t, err)
	require.Equal(t, expectedGER, ger)
}

func TestFindInvalidGERUsages(t *testing.T) {
	t.Helper()

	invalidGER := common.HexToHash("0xaaaa")
	validGER := common.HexToHash("0xbbbb")
	claims := []scanClaimRecord{
		{BlockNum: 10, TxHash: common.HexToHash("0x1"), GlobalGER: invalidGER, ClaimType: claimsynctypes.DetailedClaimEvent},
		{BlockNum: 11, TxHash: common.HexToHash("0x2"), GlobalGER: invalidGER, ClaimType: claimsynctypes.ClaimEvent},
		{BlockNum: 12, TxHash: common.HexToHash("0x3"), GlobalGER: validGER, ClaimType: claimsynctypes.DetailedClaimEvent},
	}

	usages, err := findInvalidGERUsages(context.Background(), &stubL1GERLookup{
		timestamps: map[common.Hash]*big.Int{
			validGER: big.NewInt(1),
		},
	}, claims)
	require.NoError(t, err)
	require.Len(t, usages, 1)
	require.Equal(t, invalidGER, usages[0].GER)
	require.Equal(t, 2, usages[0].ClaimCount)
	require.Equal(t, uint64(10), usages[0].FirstBlock)
	require.Equal(t, uint64(11), usages[0].LastBlock)
	require.Equal(t, []common.Hash{common.HexToHash("0x1"), common.HexToHash("0x2")}, usages[0].TxHashes)
}
