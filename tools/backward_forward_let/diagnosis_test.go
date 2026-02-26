package backward_forward_let

import (
	"bytes"
	"fmt"
	"math/big"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	bridgetypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// TestClassifyCase verifies classifyCase returns the expected RecoveryCase for all 5 cases.
func TestClassifyCase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                  string
		l1SettledDepositCount uint32
		l2CurrentDepositCount uint32
		divergencePoint       uint32
		expectedCase          RecoveryCase
	}{
		{
			// No divergent leaves, no extra L2 bridges → NoDivergence is handled before
			// classifyCase is called, but if L1 == divergencePoint+1 and L2 <= divergencePoint → Case1.
			name:                  "Case1: single divergent leaf, no extra L2",
			l1SettledDepositCount: 6, // DC 6 is divergent (divergencePoint=5, so 1 divergent leaf)
			l2CurrentDepositCount: 5, // L2 has DC 0..4 (≤ divergencePoint)
			divergencePoint:       5,
			expectedCase:          Case1,
		},
		{
			name:                  "Case2: single divergent leaf + extra L2 bridges",
			l1SettledDepositCount: 6, // 1 divergent L1 leaf (DC 5)
			l2CurrentDepositCount: 8, // L2 has DC 6, 7 (extra real bridges)
			divergencePoint:       5,
			expectedCase:          Case2,
		},
		{
			name:                  "Case3: multiple divergent L1 leaves, no extra L2",
			l1SettledDepositCount: 10, // DC 6..9 are divergent (4 divergent leaves)
			l2CurrentDepositCount: 5,  // L2 has DC 0..4 (≤ divergencePoint)
			divergencePoint:       5,
			expectedCase:          Case3,
		},
		{
			name:                  "Case4: multiple divergent L1 leaves + extra L2 bridges",
			l1SettledDepositCount: 10, // DC 6..9 divergent
			l2CurrentDepositCount: 8,  // L2 has DC 6, 7 (extra real bridges)
			divergencePoint:       5,
			expectedCase:          Case4,
		},
		{
			// DivergencePoint == L1SettledDepositCount-1 and L2 == L1 → NoDivergence
			// but let's test the edge where extraL2 and extraL1 are both false except for Case1.
			name:                  "Case1 edge: exactly 1 divergent leaf",
			l1SettledDepositCount: 1, // DC 0 is divergent
			l2CurrentDepositCount: 0, // L2 has no bridges
			divergencePoint:       0,
			// extraL2 = 0 > 0 = false; extraL1 = 1 > 1 = false → Case1
			expectedCase: Case1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := classifyCase(tc.l1SettledDepositCount, tc.l2CurrentDepositCount, tc.divergencePoint)
			require.Equal(t, tc.expectedCase, got)
		})
	}
}

// TestComputeUndercollateralization verifies token amounts are grouped and summed correctly.
func TestComputeUndercollateralization(t *testing.T) {
	t.Parallel()

	tokenA := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	tokenB := common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")

	leaves := []*agglayertypes.BridgeExit{
		{
			LeafType:           bridgetypes.LeafTypeAsset,
			TokenInfo:          &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: tokenA},
			DestinationNetwork: 1,
			DestinationAddress: common.HexToAddress("0x1111111111111111111111111111111111111111"),
			Amount:             big.NewInt(100),
		},
		{
			LeafType:           bridgetypes.LeafTypeAsset,
			TokenInfo:          &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: tokenA},
			DestinationNetwork: 1,
			DestinationAddress: common.HexToAddress("0x2222222222222222222222222222222222222222"),
			Amount:             big.NewInt(200),
		},
		{
			LeafType:           bridgetypes.LeafTypeAsset,
			TokenInfo:          &agglayertypes.TokenInfo{OriginNetwork: 1, OriginTokenAddress: tokenB},
			DestinationNetwork: 0,
			DestinationAddress: common.HexToAddress("0x3333333333333333333333333333333333333333"),
			Amount:             big.NewInt(50),
		},
	}

	result := computeUndercollateralization(leaves)

	require.Len(t, result, 2)

	// Token A should be first (encountered first).
	require.Equal(t, uint32(0), result[0].TokenOriginNetwork)
	require.Equal(t, tokenA, result[0].TokenOriginAddress)
	require.Equal(t, big.NewInt(300), result[0].Amount)

	// Token B should be second.
	require.Equal(t, uint32(1), result[1].TokenOriginNetwork)
	require.Equal(t, tokenB, result[1].TokenOriginAddress)
	require.Equal(t, big.NewInt(50), result[1].Amount)
}

// TestComputeUndercollateralization_NilAmount verifies nil amounts are treated as zero.
func TestComputeUndercollateralization_NilAmount(t *testing.T) {
	t.Parallel()

	token := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	leaves := []*agglayertypes.BridgeExit{
		{
			LeafType:  bridgetypes.LeafTypeAsset,
			TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: token},
			Amount:    nil,
		},
	}

	result := computeUndercollateralization(leaves)
	require.Len(t, result, 1)
	require.Equal(t, big.NewInt(0), result[0].Amount)
}

// TestPrintDiagnosis verifies PrintDiagnosis produces expected output for the normal case.
func TestPrintDiagnosis(t *testing.T) {
	t.Parallel()

	tokenA := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	ler := common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111")
	l2ler := common.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222")
	certID := common.HexToHash("0x3333333333333333333333333333333333333333333333333333333333333333")

	result := &DiagnosisResult{
		Case:                   Case3,
		L1SettledLER:           ler,
		L1SettledDepositCount:  10,
		L1SettledHeight:        5,
		L1SettledCertificateID: certID,
		L2CurrentLER:           l2ler,
		L2CurrentDepositCount:  6,
		DivergencePoint:        5,
		DivergentLeaves: []*agglayertypes.BridgeExit{
			{
				LeafType:           bridgetypes.LeafTypeAsset,
				TokenInfo:          &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: tokenA},
				DestinationNetwork: 1,
				DestinationAddress: common.HexToAddress("0x4444444444444444444444444444444444444444"),
				Amount:             big.NewInt(500),
			},
		},
		Undercollateralization: []UndercollateralizedToken{
			{
				TokenOriginNetwork: 0,
				TokenOriginAddress: tokenA,
				Amount:             big.NewInt(500),
			},
		},
	}

	var buf bytes.Buffer
	PrintDiagnosis(&buf, result)
	output := buf.String()

	require.Contains(t, output, "Case3")
	require.Contains(t, output, ler.Hex())
	require.Contains(t, output, tokenA.Hex())
	require.Contains(t, output, "500")
	require.Contains(t, output, "Divergence Point")
}

// TestPrintDiagnosis_NoDivergence verifies the NoDivergence path.
func TestPrintDiagnosis_NoDivergence(t *testing.T) {
	t.Parallel()

	result := &DiagnosisResult{Case: NoDivergence}
	var buf bytes.Buffer
	PrintDiagnosis(&buf, result)

	require.Contains(t, buf.String(), "NoDivergence")
}

// TestPrintDiagnosis_AggsenderAPIFailed verifies the fallback message when aggsender is unreachable.
func TestPrintDiagnosis_AggsenderAPIFailed(t *testing.T) {
	t.Parallel()

	certID := common.HexToHash("0xDEAD")
	result := &DiagnosisResult{
		Case:               Case1,
		AggsenderAPIFailed: true,
		FailedCertHeight:   7,
		FailedCertID:       certID,
	}

	var buf bytes.Buffer
	PrintDiagnosis(&buf, result)
	output := buf.String()

	require.Contains(t, output, "Aggsender RPC was unreachable")
	require.Contains(t, output, "7")
	require.Contains(t, output, certID.Hex())
}

// TestBridgeResponseLeafHash verifies that BridgeResponseLeafHash and BridgeExitLeafHash
// produce identical hashes for equivalent data.
func TestBridgeResponseLeafHash(t *testing.T) {
	t.Parallel()

	originAddr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	destAddr := common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
	amount := big.NewInt(12345)

	be := &agglayertypes.BridgeExit{
		LeafType:           bridgetypes.LeafTypeAsset,
		TokenInfo:          &agglayertypes.TokenInfo{OriginNetwork: 1, OriginTokenAddress: originAddr},
		DestinationNetwork: 2,
		DestinationAddress: destAddr,
		Amount:             amount,
		Metadata:           nil,
	}

	br := &bridgeservicetypes.BridgeResponse{
		LeafType:           0,
		OriginNetwork:      1,
		OriginAddress:      bridgeservicetypes.Address(originAddr.Hex()),
		DestinationNetwork: 2,
		DestinationAddress: bridgeservicetypes.Address(destAddr.Hex()),
		Amount:             bridgeservicetypes.BigIntString(fmt.Sprintf("%d", amount.Int64())),
		Metadata:           "",
	}

	hashFromExit := BridgeExitLeafHash(be)
	hashFromResponse := BridgeResponseLeafHash(br)

	require.Equal(t, hashFromExit, hashFromResponse,
		"BridgeExitLeafHash and BridgeResponseLeafHash must produce identical hashes for equivalent data")
}
