package backward_forward_let

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	bridgeservice "github.com/agglayer/aggkit/bridgeservice/client"
	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	bridgetypes "github.com/agglayer/aggkit/bridgesync/types"
	aggsendertypes "github.com/agglayer/aggkit/aggsender/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// --- stubs for findDivergencePoint unit tests ---

// stubAggsenderRPC implements aggsenderRPCClient for testing.
type stubAggsenderRPC struct {
	// exitsByHeight maps height → exits to return (empty slice = success with no exits).
	exitsByHeight map[uint64][]*agglayertypes.BridgeExit
	// failHeights are heights where GetCertificateBridgeExits returns an error.
	failHeights map[uint64]bool
}

func (s *stubAggsenderRPC) GetCertificateBridgeExits(height *uint64) ([]*agglayertypes.BridgeExit, error) {
	if s.failHeights[*height] {
		return nil, fmt.Errorf("stub: no data for height %d", *height)
	}
	return s.exitsByHeight[*height], nil
}

func (s *stubAggsenderRPC) GetCertificateHeaderPerHeight(_ *uint64) (*aggsendertypes.Certificate, error) {
	return nil, fmt.Errorf("stub: not implemented")
}

func (s *stubAggsenderRPC) DebugSendCertificate(_ *agglayertypes.Certificate, _ *ecdsa.PrivateKey) (common.Hash, error) {
	return common.Hash{}, fmt.Errorf("stub: not implemented")
}

// stubBridgeService implements bridgeServiceClient for testing.
// Returns ErrNotFound for any DC not in bridgesByDC.
type stubBridgeService struct {
	bridgesByDC map[uint32]*bridgeservicetypes.BridgeResponse
}

func (s *stubBridgeService) GetBridgeByDepositCount(
	_ context.Context, _ uint32, dc uint32,
) (*bridgeservicetypes.BridgeResponse, error) {
	if br, ok := s.bridgesByDC[dc]; ok {
		return br, nil
	}
	return nil, bridgeservice.ErrNotFound
}

// TestClassifyCase verifies classifyCase returns the expected RecoveryCase for all 5 cases.
func TestClassifyCase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		l2CurrentDC       uint32
		divergencePoint   uint32 // number of matching leading leaves
		numDivergent      int    // number of divergent L1-settled leaves
		expectedCase      RecoveryCase
	}{
		{
			name:            "Case1: single divergent leaf, no extra L2",
			l2CurrentDC:     6, // L2 has DC 0..5 (≤ divergencePoint)
			divergencePoint: 6, // 6 matching leaves (DC 0..5)
			numDivergent:    1,
			expectedCase:    Case1,
		},
		{
			name:            "Case2: single divergent leaf + extra L2 bridges",
			l2CurrentDC:     8, // L2 has DC 6, 7 (extra real bridges beyond divergencePoint)
			divergencePoint: 6,
			numDivergent:    1,
			expectedCase:    Case2,
		},
		{
			name:            "Case3: multiple divergent L1 leaves, no extra L2",
			l2CurrentDC:     6, // L2 has DC 0..5 (≤ divergencePoint)
			divergencePoint: 6,
			numDivergent:    4, // 4 divergent leaves
			expectedCase:    Case3,
		},
		{
			name:            "Case4: multiple divergent L1 leaves + extra L2 bridges",
			l2CurrentDC:     8, // L2 has DC 6, 7 (extra real bridges)
			divergencePoint: 6,
			numDivergent:    4,
			expectedCase:    Case4,
		},
		{
			name:            "Case1 edge: exactly 1 divergent leaf, zero matching",
			l2CurrentDC:     0, // L2 has no bridges
			divergencePoint: 0, // 0 matching leaves
			numDivergent:    1,
			// hasExtraL2 = 0 > 0 = false; multipleL1 = 1 > 1 = false → Case1
			expectedCase: Case1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := classifyCase(tc.l2CurrentDC, tc.divergencePoint, tc.numDivergent)
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
		DivergencePoint:        6,
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

// TestPrintDiagnosis_AggsenderAPIFailed verifies the actionable missing-cert output
// when all cert IDs are resolved (no UNKNOWN entries).
func TestPrintDiagnosis_AggsenderAPIFailed(t *testing.T) {
	t.Parallel()

	certID := common.HexToHash("0xDEAD")
	result := &DiagnosisResult{
		Case:               Case1,
		AggsenderAPIFailed: true,
		MissingCerts: []MissingCertInfo{
			{Height: 7, CertID: certID, CertIDResolved: true},
		},
	}

	var buf bytes.Buffer
	PrintDiagnosis(&buf, result)
	output := buf.String()

	require.Contains(t, output, "Aggsender RPC returned no bridge exit data")
	require.Contains(t, output, "Recovery cannot proceed")
	require.Contains(t, output, "Missing certificates (1 height):")
	require.Contains(t, output, "Height 7")
	require.Contains(t, output, certID.Hex())
	require.Contains(t, output, "[ID auto-resolved]")
	require.Contains(t, output, "admin_getCertificate")
	require.Contains(t, output, `"7":`)
	require.Contains(t, output, "--cert-exits-file")
	// No UNKNOWN note when all cert IDs are resolved.
	require.NotContains(t, output, "UNKNOWN")
	require.NotContains(t, output, "certificate_per_network_cf")
}

// TestPrintDiagnosis_AggsenderAPIFailed_WithUnknownCertID verifies that the extra
// UNKNOWN note is printed when one or more cert IDs could not be resolved.
func TestPrintDiagnosis_AggsenderAPIFailed_WithUnknownCertID(t *testing.T) {
	t.Parallel()

	certID := common.HexToHash("0xAAAA")
	result := &DiagnosisResult{
		Case:               Case1,
		AggsenderAPIFailed: true,
		MissingCerts: []MissingCertInfo{
			{Height: 5, CertID: certID, CertIDResolved: true},
			{Height: 3, CertIDResolved: false},
		},
	}

	var buf bytes.Buffer
	PrintDiagnosis(&buf, result)
	output := buf.String()

	require.Contains(t, output, "Missing certificates (2 heights):")
	require.Contains(t, output, certID.Hex())
	require.Contains(t, output, "[ID auto-resolved]")
	require.Contains(t, output, "UNKNOWN")
	require.Contains(t, output, "[contact agglayer admin for cert ID]")
	require.Contains(t, output, "certificate_per_network_cf")
	// Both heights appear in the JSON template.
	require.Contains(t, output, `"5":`)
	require.Contains(t, output, `"3":`)
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

// TestFindDivergencePoint_AllHeightsFail verifies that when aggsender fails for every
// height, all heights are reported in MissingCerts with correct cert ID resolution.
func TestFindDivergencePoint_AllHeightsFail(t *testing.T) {
	t.Parallel()

	settledCertID := common.HexToHash("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")

	env := &Env{
		AggsenderRPC: &stubAggsenderRPC{
			failHeights: map[uint64]bool{0: true, 1: true, 2: true},
		},
		// BridgeService is not reached when aggsender fails.
	}

	leaves, divPoint, divFound, missingErr := findDivergencePoint(
		context.Background(), env, 2, 3, settledCertID,
	)

	require.NotNil(t, missingErr)
	require.Nil(t, leaves)
	require.Zero(t, divPoint)
	require.False(t, divFound)

	require.Len(t, missingErr.missing, 3)

	// h=2 is the latest settled height → cert ID auto-resolved.
	require.Equal(t, uint64(2), missingErr.missing[0].Height)
	require.True(t, missingErr.missing[0].CertIDResolved)
	require.Equal(t, settledCertID, missingErr.missing[0].CertID)

	// h=1 and h=0 are below the settled height → cert ID not resolvable.
	require.Equal(t, uint64(1), missingErr.missing[1].Height)
	require.False(t, missingErr.missing[1].CertIDResolved)
	require.Equal(t, common.Hash{}, missingErr.missing[1].CertID)

	require.Equal(t, uint64(0), missingErr.missing[2].Height)
	require.False(t, missingErr.missing[2].CertIDResolved)
	require.Equal(t, common.Hash{}, missingErr.missing[2].CertID)
}

// TestFindDivergencePoint_OnlySettledHeightFails verifies that when only the latest
// settled height fails, exactly one entry is reported and the lower heights are walked.
func TestFindDivergencePoint_OnlySettledHeightFails(t *testing.T) {
	t.Parallel()

	settledCertID := common.HexToHash("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")

	// Heights 1 and 0 return empty cert (no exits) — skipped without touching BridgeService.
	env := &Env{
		AggsenderRPC: &stubAggsenderRPC{
			failHeights:   map[uint64]bool{2: true},
			exitsByHeight: map[uint64][]*agglayertypes.BridgeExit{1: {}, 0: {}},
		},
	}

	_, _, _, missingErr := findDivergencePoint(
		context.Background(), env, 2, 0, settledCertID,
	)

	require.NotNil(t, missingErr)
	require.Len(t, missingErr.missing, 1)

	// Only h=2 (the settled height) is missing, and its cert ID is resolved.
	require.Equal(t, uint64(2), missingErr.missing[0].Height)
	require.True(t, missingErr.missing[0].CertIDResolved)
	require.Equal(t, settledCertID, missingErr.missing[0].CertID)
}

// TestFindDivergencePoint_NoFailure verifies that when aggsender succeeds for all
// heights (with empty certs), no missingCertsError is returned.
func TestFindDivergencePoint_NoFailure(t *testing.T) {
	t.Parallel()

	// All heights return empty exit lists — no comparison against BridgeService needed.
	env := &Env{
		AggsenderRPC: &stubAggsenderRPC{
			exitsByHeight: map[uint64][]*agglayertypes.BridgeExit{
				0: {},
				1: {},
				2: {},
			},
		},
	}

	leaves, divPoint, divFound, missingErr := findDivergencePoint(
		context.Background(), env, 2, 0, common.Hash{},
	)

	require.Nil(t, missingErr, "no error expected when aggsender succeeds for all heights")
	require.Empty(t, leaves)
	require.Zero(t, divPoint)
	require.False(t, divFound)
}

// TestFindDivergencePoint_ZeroCertIDSkipped verifies that a zero settledCertID does
// not produce a resolved entry even for the latest settled height.
func TestFindDivergencePoint_ZeroCertIDSkipped(t *testing.T) {
	t.Parallel()

	env := &Env{
		AggsenderRPC: &stubAggsenderRPC{
			failHeights: map[uint64]bool{0: true},
		},
	}

	_, _, _, missingErr := findDivergencePoint(
		context.Background(), env, 0, 1, common.Hash{}, // zero settledCertID
	)

	require.NotNil(t, missingErr)
	require.Len(t, missingErr.missing, 1)
	require.Equal(t, uint64(0), missingErr.missing[0].Height)
	require.False(t, missingErr.missing[0].CertIDResolved,
		"zero settledCertID must not be reported as resolved")
	require.Equal(t, common.Hash{}, missingErr.missing[0].CertID)
}

// --- getBridgeExitsForHeight unit tests ---

// makeOverride builds a BridgeExitsOverride with a pre-populated parsed map.
// Used to avoid a temp-file round-trip in unit tests within the same package.
func makeOverride(heights map[uint64][]*agglayertypes.BridgeExit) *BridgeExitsOverride {
	return &BridgeExitsOverride{
		NetworkID: 1,
		parsed:    heights,
	}
}

// TestGetBridgeExitsForHeight_AggsenderSucceeds verifies that when the aggsender
// returns data, the override is never consulted and the aggsender result is returned.
func TestGetBridgeExitsForHeight_AggsenderSucceeds(t *testing.T) {
	t.Parallel()

	want := []*agglayertypes.BridgeExit{{DestinationNetwork: 42}}
	env := &Env{
		AggsenderRPC: &stubAggsenderRPC{
			exitsByHeight: map[uint64][]*agglayertypes.BridgeExit{5: want},
		},
		// Override present but must not be reached.
		BridgeExitsOverride: makeOverride(map[uint64][]*agglayertypes.BridgeExit{
			5: {{DestinationNetwork: 99}},
		}),
	}

	got, err := getBridgeExitsForHeight(env, 5)
	require.NoError(t, err)
	require.Equal(t, want, got, "aggsender result must be returned when aggsender succeeds")
}

// TestGetBridgeExitsForHeight_AggsenderFails_OverrideHit verifies that when the
// aggsender fails and the override has an entry for the height, the override is used.
func TestGetBridgeExitsForHeight_AggsenderFails_OverrideHit(t *testing.T) {
	t.Parallel()

	want := []*agglayertypes.BridgeExit{{DestinationNetwork: 7}}
	env := &Env{
		AggsenderRPC: &stubAggsenderRPC{
			failHeights: map[uint64]bool{3: true},
		},
		BridgeExitsOverride: makeOverride(map[uint64][]*agglayertypes.BridgeExit{3: want}),
	}

	got, err := getBridgeExitsForHeight(env, 3)
	require.NoError(t, err)
	require.Equal(t, want, got, "override result must be returned when aggsender fails")
}

// TestGetBridgeExitsForHeight_AggsenderFails_OverrideMiss verifies that when the
// aggsender fails and the override is present but has no entry for the height,
// an error is returned.
func TestGetBridgeExitsForHeight_AggsenderFails_OverrideMiss(t *testing.T) {
	t.Parallel()

	env := &Env{
		AggsenderRPC: &stubAggsenderRPC{
			failHeights: map[uint64]bool{8: true},
		},
		// Override exists but has no entry for height 8.
		BridgeExitsOverride: makeOverride(map[uint64][]*agglayertypes.BridgeExit{
			0: {},
		}),
	}

	_, err := getBridgeExitsForHeight(env, 8)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no bridge exit data for height 8")
}

// TestGetBridgeExitsForHeight_AggsenderFails_NoOverride verifies that when the
// aggsender fails and no override is configured, an error is returned.
func TestGetBridgeExitsForHeight_AggsenderFails_NoOverride(t *testing.T) {
	t.Parallel()

	env := &Env{
		AggsenderRPC: &stubAggsenderRPC{
			failHeights: map[uint64]bool{2: true},
		},
		BridgeExitsOverride: nil,
	}

	_, err := getBridgeExitsForHeight(env, 2)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no bridge exit data for height 2")
}
