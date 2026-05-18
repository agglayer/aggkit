package exit_certificate

import (
	"encoding/json"
	"math/big"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestFormatTokenAmount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		amount   *big.Int
		decimals uint8
		expected string
	}{
		{"nil amount", nil, 18, "0"},
		{"zero decimals", big.NewInt(12345), 0, "12345 (raw)"},
		{"exact whole number", new(big.Int).Mul(big.NewInt(3), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)), 18, "3"},
		{"1.5 tokens", new(big.Int).SetUint64(1_500_000_000_000_000_000), 18, "1.5"},
		{"small sub-unit value (user case)", big.NewInt(4938271560), 18, "0.00000000493827156"},
		{"trailing zeros stripped", big.NewInt(1_500_000), 6, "1.5"},
		{"fractional only, no leading fraction digit trimmed", big.NewInt(1234567890), 6, "1234.56789"},
		{"zero amount", big.NewInt(0), 18, "0"},
		{"one wei", big.NewInt(1), 18, "0.000000000000000001"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.expected, formatTokenAmount(tc.amount, tc.decimals))
		})
	}
}

func TestDecodeBridgeEvent_Valid(t *testing.T) {
	t.Parallel()

	data := make([]byte, 9*32)

	data[31] = 0
	data[63] = 1
	copy(data[64+12:96], common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA").Bytes())
	data[127] = 2
	copy(data[128+12:160], common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB").Bytes())
	new(big.Int).SetInt64(1000).FillBytes(data[160:192])
	new(big.Int).SetInt64(256).FillBytes(data[192:224])
	new(big.Int).SetInt64(42).FillBytes(data[224:256])
	new(big.Int).SetInt64(0).FillBytes(data[256:288])

	dataHex := "0x" + common.Bytes2Hex(data)
	dep, err := decodeBridgeEvent(dataHex, "0xa", "0x1234")
	require.NoError(t, err)

	require.Equal(t, uint8(0), dep.LeafType)
	require.Equal(t, uint32(1), dep.OriginNetwork)
	require.Equal(t, common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"), dep.OriginAddress)
	require.Equal(t, uint32(2), dep.DestinationNetwork)
	require.Equal(t, common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"), dep.DestinationAddress)
	require.Equal(t, big.NewInt(1000), dep.Amount)
	require.Equal(t, uint32(42), dep.DepositCount)
	require.Equal(t, uint64(10), dep.BlockNumber)
}

func TestDecodeBridgeEvent_DataTooShort(t *testing.T) {
	t.Parallel()

	_, err := decodeBridgeEvent("0x0000", "0x1", "0x1234")
	require.Error(t, err)
	require.Contains(t, err.Error(), "data too short")
}

func TestEncodeIsClaimed(t *testing.T) {
	t.Parallel()

	// isClaimed(leafIndex=42, sourceBridgeNetwork=0)
	encoded := encodeIsClaimed(42, 0)

	require.Equal(t, "0xcc461632", encoded[:10])

	// Next 32 bytes = leafIndex = 42 (0x2a)
	require.Equal(t, "000000000000000000000000000000000000000000000000000000000000002a", encoded[10:74])

	// Next 32 bytes = sourceBridgeNetwork = 0
	require.Equal(t, "0000000000000000000000000000000000000000000000000000000000000000", encoded[74:138])
}

func TestEncodeIsClaimed_NonZeroSource(t *testing.T) {
	t.Parallel()

	encoded := encodeIsClaimed(100, 5)

	require.Equal(t, "0xcc461632", encoded[:10])
	require.Equal(t, "0000000000000000000000000000000000000000000000000000000000000064", encoded[10:74])
	require.Equal(t, "0000000000000000000000000000000000000000000000000000000000000005", encoded[74:138])
}

func TestParseClaimedResults(t *testing.T) {
	t.Parallel()

	deposits := []L1Deposit{
		{DepositCount: 1},
		{DepositCount: 2},
		{DepositCount: 3},
	}

	trueHex := json.RawMessage(`"0x0000000000000000000000000000000000000000000000000000000000000001"`)
	falseHex := json.RawMessage(`"0x0000000000000000000000000000000000000000000000000000000000000000"`)

	results := []json.RawMessage{trueHex, falseHex, trueHex}

	claimed := parseClaimedResults(results, deposits)

	require.Contains(t, claimed, uint32(1))
	require.NotContains(t, claimed, uint32(2))
	require.Contains(t, claimed, uint32(3))
}

func TestFilterUnclaimedDeposits(t *testing.T) {
	t.Parallel()

	deposits := []L1Deposit{
		{DepositCount: 1, Amount: big.NewInt(100)},
		{DepositCount: 2, Amount: big.NewInt(200)},
		{DepositCount: 3, Amount: big.NewInt(300)},
	}

	claimed := map[uint32]struct{}{1: {}, 3: {}}
	unclaimed := filterUnclaimedDeposits(deposits, claimed)

	require.Len(t, unclaimed, 1)
	require.Equal(t, uint32(2), unclaimed[0].DepositCount)
}

func TestReportPendingDiscrepancies_NoDiscrepancy(t *testing.T) {
	t.Parallel()

	unclaimed := []L1Deposit{
		{DepositCount: 1},
		{DepositCount: 2},
		{DepositCount: 3},
	}
	svcCounts := map[uint32]struct{}{1: {}, 2: {}, 3: {}}

	err := reportPendingDiscrepancies("test service", unclaimed, svcCounts)
	require.NoError(t, err)
}

func TestReportPendingDiscrepancies_BothEmpty(t *testing.T) {
	t.Parallel()

	err := reportPendingDiscrepancies("test service", nil, map[uint32]struct{}{})
	require.NoError(t, err)
}

func TestReportPendingDiscrepancies_InSvcOnly(t *testing.T) {
	t.Parallel()

	unclaimed := []L1Deposit{{DepositCount: 1}}
	svcCounts := map[uint32]struct{}{1: {}, 5: {}, 9: {}}

	err := reportPendingDiscrepancies("test service", unclaimed, svcCounts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "test service reports 2 deposit(s) not found by L1 scan")
	require.Contains(t, err.Error(), "[5 9]")
	require.NotContains(t, err.Error(), "L1 scan found")
}

func TestReportPendingDiscrepancies_InScanOnly(t *testing.T) {
	t.Parallel()

	unclaimed := []L1Deposit{{DepositCount: 1}, {DepositCount: 7}, {DepositCount: 3}}
	svcCounts := map[uint32]struct{}{1: {}}

	err := reportPendingDiscrepancies("test service", unclaimed, svcCounts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "L1 scan found 2 deposit(s) not reported by test service")
	require.Contains(t, err.Error(), "[3 7]")
	require.NotContains(t, err.Error(), "test service reports")
}

func TestReportPendingDiscrepancies_BothSides(t *testing.T) {
	t.Parallel()

	unclaimed := []L1Deposit{{DepositCount: 1}, {DepositCount: 2}}
	svcCounts := map[uint32]struct{}{1: {}, 99: {}}

	err := reportPendingDiscrepancies("test service", unclaimed, svcCounts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "test service reports 1 deposit(s) not found by L1 scan")
	require.Contains(t, err.Error(), "L1 scan found 1 deposit(s) not reported by test service")
}

func TestStepE_MergeCertificateExits(t *testing.T) {
	t.Parallel()

	existingExit := &agglayertypes.BridgeExit{
		TokenInfo: &agglayertypes.TokenInfo{
			OriginNetwork:      0,
			OriginTokenAddress: common.Address{},
		},
		DestinationNetwork: 0,
		DestinationAddress: common.HexToAddress("0x1111"),
		Amount:             big.NewInt(100),
	}

	newExit := &agglayertypes.BridgeExit{
		TokenInfo: &agglayertypes.TokenInfo{
			OriginNetwork:      0,
			OriginTokenAddress: common.Address{},
		},
		DestinationNetwork: 0,
		DestinationAddress: common.HexToAddress("0x2222"),
		Amount:             big.NewInt(200),
	}

	certificate := &agglayertypes.Certificate{
		NetworkID:   1,
		BridgeExits: []*agglayertypes.BridgeExit{existingExit},
	}

	finalCert := mergeCertificate(certificate, []*agglayertypes.BridgeExit{newExit}, nil)

	require.Len(t, finalCert.BridgeExits, 2)
	require.Equal(t, big.NewInt(100), finalCert.BridgeExits[0].Amount)
	require.Equal(t, big.NewInt(200), finalCert.BridgeExits[1].Amount)
}
