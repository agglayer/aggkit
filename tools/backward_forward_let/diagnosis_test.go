package backward_forward_let

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	aggsendertypes "github.com/agglayer/aggkit/aggsender/types"
	bridgeservice "github.com/agglayer/aggkit/bridgeservice/client"
	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	bridgetypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// stubBridgeService implements bridgeServiceClient for testing.
type stubBridgeService struct {
	// bridges maps depositCount → BridgeResponse to return.
	bridges map[uint32]*bridgeservicetypes.BridgeResponse
	// errAtDC maps depositCount → error to return.
	errAtDC map[uint32]error
}

func (s *stubBridgeService) GetBridgeByDepositCount(
	_ context.Context, _ uint32, depositCount uint32,
) (*bridgeservicetypes.BridgeResponse, error) {
	if s.errAtDC != nil {
		if err, ok := s.errAtDC[depositCount]; ok {
			return nil, err
		}
	}
	if br, ok := s.bridges[depositCount]; ok {
		return br, nil
	}
	return nil, bridgeservice.ErrNotFound
}

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

// TestClassifyCase verifies classifyCase returns the expected RecoveryCase for all 5 cases.
func TestClassifyCase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		l2CurrentDC     uint32
		divergencePoint uint32 // number of matching leading leaves
		numDivergent    int    // number of divergent L1-settled leaves
		expectedCase    RecoveryCase
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

	_, _, _, missingErr := findDivergencePoint( //nolint:dogsled
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

	_, _, _, missingErr := findDivergencePoint( //nolint:dogsled
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

// TestIsNotFound verifies that isNotFound correctly identifies the bridgeservice sentinel.
func TestIsNotFound(t *testing.T) {
	t.Parallel()

	require.True(t, isNotFound(bridgeservice.ErrNotFound))
	require.True(t, isNotFound(fmt.Errorf("wrapped: %w", bridgeservice.ErrNotFound)))
	require.False(t, isNotFound(errors.New("some other error")))
}

// TestCaseDescription verifies caseDescription returns the correct string for each case.
func TestCaseDescription(t *testing.T) {
	t.Parallel()

	tests := []struct {
		c    RecoveryCase
		want string
	}{
		{Case1, "Case1"},
		{Case2, "Case2"},
		{Case3, "Case3"},
		{Case4, "Case4"},
		{NoDivergence, string(NoDivergence)}, // default branch
	}

	for _, tc := range tests {
		t.Run(string(tc.c), func(t *testing.T) {
			t.Parallel()
			got := caseDescription(tc.c)
			require.Contains(t, got, tc.want)
		})
	}
}

// TestPrintRecoveryPlanSummary_Case1 verifies ForwardLET-only output.
func TestPrintRecoveryPlanSummary_Case1(t *testing.T) {
	t.Parallel()

	tokenA := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	result := &DiagnosisResult{
		Case: Case1,
		DivergentLeaves: []*agglayertypes.BridgeExit{
			{
				TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: tokenA},
				Amount:    big.NewInt(100),
			},
		},
	}

	var buf bytes.Buffer
	printRecoveryPlanSummary(&buf, result)
	output := buf.String()

	require.Contains(t, output, "ForwardLET")
	require.Contains(t, output, "1 divergent leaf")
	require.NotContains(t, output, "BackwardLET")
}

// TestPrintRecoveryPlanSummary_Case2 verifies BackwardLET+ForwardLET+ForwardLET output.
func TestPrintRecoveryPlanSummary_Case2(t *testing.T) {
	t.Parallel()

	tokenA := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	result := &DiagnosisResult{
		Case:            Case2,
		DivergencePoint: 3,
		DivergentLeaves: []*agglayertypes.BridgeExit{
			{
				TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: tokenA},
				Amount:    big.NewInt(200),
			},
		},
		ExtraL2Bridges: []bridgesync.LeafData{
			{LeafType: 0, OriginNetwork: 1, Amount: big.NewInt(50)},
		},
	}

	var buf bytes.Buffer
	printRecoveryPlanSummary(&buf, result)
	output := buf.String()

	require.Contains(t, output, "BackwardLET")
	require.Contains(t, output, "ForwardLET #1")
	require.Contains(t, output, "ForwardLET #2")
	require.Contains(t, output, "Verify")
}

// makeBridgeResponse builds a minimal BridgeResponse suitable for leaf hash comparison.
func makeBridgeResponse(
	leafType uint8, originNet uint32, originAddr, destAddr string, destNet uint32, amount string,
) *bridgeservicetypes.BridgeResponse {
	return &bridgeservicetypes.BridgeResponse{
		LeafType:           leafType,
		OriginNetwork:      originNet,
		OriginAddress:      bridgeservicetypes.Address(originAddr),
		DestinationNetwork: destNet,
		DestinationAddress: bridgeservicetypes.Address(destAddr),
		Amount:             bridgeservicetypes.BigIntString(amount),
	}
}

// makeBridgeExit builds a BridgeExit whose leaf hash matches a given BridgeResponse.
func makeBridgeExitFromResponse(br *bridgeservicetypes.BridgeResponse) *agglayertypes.BridgeExit {
	amount := parseAmount(string(br.Amount))
	return &agglayertypes.BridgeExit{
		LeafType:           bridgetypes.LeafType(br.LeafType),
		TokenInfo:          &agglayertypes.TokenInfo{OriginNetwork: br.OriginNetwork, OriginTokenAddress: common.HexToAddress(string(br.OriginAddress))},
		DestinationNetwork: br.DestinationNetwork,
		DestinationAddress: common.HexToAddress(string(br.DestinationAddress)),
		Amount:             amount,
		Metadata:           decodeMetadata(br.Metadata),
	}
}

// TestCheckCertExitsMatchL2_AllMatch verifies true when all exits match.
func TestCheckCertExitsMatchL2_AllMatch(t *testing.T) {
	t.Parallel()

	br0 := makeBridgeResponse(0, 1, "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", 2, "1000")
	br1 := makeBridgeResponse(0, 1, "0xCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC",
		"0xDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD", 3, "2000")

	exits := []*agglayertypes.BridgeExit{
		makeBridgeExitFromResponse(br0),
		makeBridgeExitFromResponse(br1),
	}

	env := &Env{
		BridgeService: &stubBridgeService{
			bridges: map[uint32]*bridgeservicetypes.BridgeResponse{
				5: br0,
				6: br1,
			},
		},
		L2NetworkID: 1,
	}

	result := checkCertExitsMatchL2(context.Background(), env, exits, 5)
	require.True(t, result)
}

// TestCheckCertExitsMatchL2_Mismatch verifies false when hashes differ.
func TestCheckCertExitsMatchL2_Mismatch(t *testing.T) {
	t.Parallel()

	br0 := makeBridgeResponse(0, 1, "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", 2, "1000")
	// Create an exit that does NOT match br0.
	differentExit := &agglayertypes.BridgeExit{
		LeafType:           0,
		TokenInfo:          &agglayertypes.TokenInfo{OriginNetwork: 9, OriginTokenAddress: common.HexToAddress("0x9999")},
		DestinationNetwork: 9,
		DestinationAddress: common.HexToAddress("0x9999"),
		Amount:             big.NewInt(9999),
	}

	env := &Env{
		BridgeService: &stubBridgeService{
			bridges: map[uint32]*bridgeservicetypes.BridgeResponse{0: br0},
		},
		L2NetworkID: 1,
	}

	result := checkCertExitsMatchL2(context.Background(), env, []*agglayertypes.BridgeExit{differentExit}, 0)
	require.False(t, result)
}

// TestCheckCertExitsMatchL2_ServiceError verifies false when bridge service returns error.
func TestCheckCertExitsMatchL2_ServiceError(t *testing.T) {
	t.Parallel()

	exit := &agglayertypes.BridgeExit{
		LeafType:           0,
		DestinationNetwork: 1,
		Amount:             big.NewInt(100),
	}

	env := &Env{
		BridgeService: &stubBridgeService{
			errAtDC: map[uint32]error{0: errors.New("service down")},
		},
		L2NetworkID: 1,
	}

	result := checkCertExitsMatchL2(context.Background(), env, []*agglayertypes.BridgeExit{exit}, 0)
	require.False(t, result)
}

// TestCollectExtraL2Bridges_HappyPath verifies bridges are collected correctly.
func TestCollectExtraL2Bridges_HappyPath(t *testing.T) {
	t.Parallel()

	br3 := makeBridgeResponse(0, 1, "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", 2, "500")
	br4 := makeBridgeResponse(1, 2, "0xCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC",
		"0xDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD", 3, "600")

	env := &Env{
		BridgeService: &stubBridgeService{
			bridges: map[uint32]*bridgeservicetypes.BridgeResponse{
				3: br3,
				4: br4,
			},
		},
		L2NetworkID: 1,
	}

	extra, err := collectExtraL2Bridges(context.Background(), env, 3, 5)
	require.NoError(t, err)
	require.Len(t, extra, 2)
}

// TestCollectExtraL2Bridges_NotFound verifies that NotFound entries are skipped.
func TestCollectExtraL2Bridges_NotFound(t *testing.T) {
	t.Parallel()

	br3 := makeBridgeResponse(0, 1, "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", 2, "100")

	env := &Env{
		BridgeService: &stubBridgeService{
			bridges: map[uint32]*bridgeservicetypes.BridgeResponse{
				3: br3,
				// DC 4 is absent → returns ErrNotFound → skipped
			},
		},
		L2NetworkID: 1,
	}

	extra, err := collectExtraL2Bridges(context.Background(), env, 3, 5)
	require.NoError(t, err)
	require.Len(t, extra, 1)
}

// TestCollectExtraL2Bridges_ServiceError verifies a non-NotFound error is propagated.
func TestCollectExtraL2Bridges_ServiceError(t *testing.T) {
	t.Parallel()

	env := &Env{
		BridgeService: &stubBridgeService{
			errAtDC: map[uint32]error{2: errors.New("connection refused")},
		},
		L2NetworkID: 1,
	}

	_, err := collectExtraL2Bridges(context.Background(), env, 2, 3)
	require.Error(t, err)
	require.Contains(t, err.Error(), "DC=2")
}

// TestEnvClose_Nil verifies that Close on a nil *Env returns nil without panic.
func TestEnvClose_Nil(t *testing.T) {
	t.Parallel()

	var e *Env
	require.NoError(t, e.Close())
}

// TestEnvClose_NilL2Client verifies that Close on an Env with no L2Client returns nil.
func TestEnvClose_NilL2Client(t *testing.T) {
	t.Parallel()

	e := &Env{}
	require.NoError(t, e.Close())
}

// TestPrintDiagnosis_EmergencyState verifies the emergency state warning is printed.
func TestPrintDiagnosis_EmergencyState(t *testing.T) {
	t.Parallel()

	tokenA := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	result := &DiagnosisResult{
		Case:             Case1,
		IsEmergencyState: true,
		DivergentLeaves: []*agglayertypes.BridgeExit{
			{
				LeafType:           bridgetypes.LeafTypeAsset,
				TokenInfo:          &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: tokenA},
				DestinationNetwork: 1,
				DestinationAddress: common.HexToAddress("0x1111"),
				Amount:             big.NewInt(100),
			},
		},
	}

	var buf bytes.Buffer
	PrintDiagnosis(&buf, result)
	output := buf.String()

	require.Contains(t, output, "emergency state")
	require.Contains(t, output, "WARNING")
}

// TestPrintDiagnosis_WithExtraL2Bridges verifies the ExtraL2Bridges table is printed.
func TestPrintDiagnosis_WithExtraL2Bridges(t *testing.T) {
	t.Parallel()

	tokenA := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	result := &DiagnosisResult{
		Case:            Case2,
		DivergencePoint: 3,
		DivergentLeaves: []*agglayertypes.BridgeExit{
			{
				LeafType:           bridgetypes.LeafTypeAsset,
				TokenInfo:          &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: tokenA},
				DestinationNetwork: 1,
				DestinationAddress: common.HexToAddress("0x1111"),
				Amount:             big.NewInt(500),
			},
		},
		ExtraL2Bridges: []bridgesync.LeafData{
			{
				LeafType:           0,
				OriginNetwork:      1,
				OriginAddress:      tokenA,
				DestinationNetwork: 2,
				DestinationAddress: common.HexToAddress("0x2222"),
				Amount:             big.NewInt(200),
			},
		},
		Undercollateralization: []UndercollateralizedToken{
			{TokenOriginNetwork: 0, TokenOriginAddress: tokenA, Amount: big.NewInt(500)},
		},
	}

	var buf bytes.Buffer
	PrintDiagnosis(&buf, result)
	output := buf.String()

	require.Contains(t, output, "Extra Real L2 Bridges")
	require.Contains(t, output, "200")
	require.Contains(t, output, "Case2")
}

// TestFindDivergencePoint_NonMatchingExits verifies the path where exits from a cert
// do NOT match the L2 bridge service data. In this case, the exits are prepended to
// divergentLeaves and the walk continues. With no further matching cert, the function
// returns (divergentLeaves, 0, false, nil).
func TestFindDivergencePoint_NonMatchingExits(t *testing.T) {
	t.Parallel()

	// Height 0 returns one exit that does NOT match the bridge service response.
	mismatchedExit := &agglayertypes.BridgeExit{
		LeafType:           0,
		TokenInfo:          &agglayertypes.TokenInfo{OriginNetwork: 99, OriginTokenAddress: common.HexToAddress("0x9999")},
		DestinationNetwork: 99,
		DestinationAddress: common.HexToAddress("0x9999"),
		Amount:             big.NewInt(9999),
	}
	br0 := makeBridgeResponse(0, 1, "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", 2, "1000")

	env := &Env{
		AggsenderRPC: &stubAggsenderRPC{
			exitsByHeight: map[uint64][]*agglayertypes.BridgeExit{
				0: {mismatchedExit},
			},
		},
		BridgeService: &stubBridgeService{
			bridges: map[uint32]*bridgeservicetypes.BridgeResponse{
				0: br0,
			},
		},
		L2NetworkID: 1,
	}

	// settledHeight=0, 1 total leaf → height 0 has one exit that doesn't match.
	leaves, divPoint, divFound, missingErr := findDivergencePoint(
		context.Background(), env, 0, 1, common.Hash{},
	)

	require.Nil(t, missingErr)
	require.Len(t, leaves, 1, "mismatched exit should be in divergentLeaves")
	require.Equal(t, mismatchedExit, leaves[0])
	require.Equal(t, uint32(0), divPoint)
	require.False(t, divFound, "no matching cert found when exits don't match")
}

// TestFindDivergencePoint_AllCertsMatch verifies the early-return path when all exits
// at a height match the L2 bridge service data and there are no missing heights.
func TestFindDivergencePoint_AllCertsMatch(t *testing.T) {
	t.Parallel()

	// Create two matching bridge exit / bridge response pairs for DC 0 and DC 1.
	br0 := makeBridgeResponse(0, 1, "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", 2, "1000")
	br1 := makeBridgeResponse(0, 1, "0xCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC",
		"0xDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD", 3, "2000")
	exit0 := makeBridgeExitFromResponse(br0)
	exit1 := makeBridgeExitFromResponse(br1)

	env := &Env{
		AggsenderRPC: &stubAggsenderRPC{
			// Height 0 returns 2 exits that match DCs 0 and 1.
			exitsByHeight: map[uint64][]*agglayertypes.BridgeExit{
				0: {exit0, exit1},
			},
		},
		BridgeService: &stubBridgeService{
			bridges: map[uint32]*bridgeservicetypes.BridgeResponse{
				0: br0,
				1: br1,
			},
		},
		L2NetworkID: 1,
	}

	// settledHeight=0, totalSettledLeaves=2 → one cert at height 0 with 2 exits.
	leaves, divPoint, divFound, missingErr := findDivergencePoint(
		context.Background(), env, 0, 2, common.Hash{},
	)

	require.Nil(t, missingErr)
	require.Empty(t, leaves, "no divergent leaves expected when all certs match")
	require.Equal(t, uint32(2), divPoint, "divergence point should be after all settled leaves")
	require.True(t, divFound)
}

// TestComputeUndercollateralization_NilTokenInfo verifies that leaves with nil TokenInfo
// are skipped and do not contribute to the result.
func TestComputeUndercollateralization_NilTokenInfo(t *testing.T) {
	t.Parallel()

	token := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	leaves := []*agglayertypes.BridgeExit{
		// This leaf has nil TokenInfo — should be skipped.
		{
			LeafType:           0,
			TokenInfo:          nil,
			DestinationNetwork: 1,
			Amount:             big.NewInt(999),
		},
		// This leaf has valid TokenInfo.
		{
			LeafType:  0,
			TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: token},
			Amount:    big.NewInt(42),
		},
	}

	result := computeUndercollateralization(leaves)
	require.Len(t, result, 1, "only the leaf with non-nil TokenInfo should appear")
	require.Equal(t, token, result[0].TokenOriginAddress)
	require.Equal(t, big.NewInt(42), result[0].Amount)
}

// TestFindDivergencePoint_MatchingThenMissingAbove verifies that when a matching cert is found
// but there are missing certs above it, the walk reports missing entries.
func TestFindDivergencePoint_MatchingThenMissingAbove(t *testing.T) {
	t.Parallel()

	settledCertID := common.HexToHash("0xCCCC")

	// Height 2 fails, heights 0 and 1 have exits that match (empty).
	env := &Env{
		AggsenderRPC: &stubAggsenderRPC{
			failHeights: map[uint64]bool{2: true},
			exitsByHeight: map[uint64][]*agglayertypes.BridgeExit{
				0: {},
				1: {},
			},
		},
	}

	// settledHeight=2, but height 2 fails → one missing entry.
	_, _, _, missingErr := findDivergencePoint( //nolint:dogsled
		context.Background(), env, 2, 0, settledCertID,
	)

	require.NotNil(t, missingErr)
	require.Len(t, missingErr.missing, 1)
	require.Equal(t, uint64(2), missingErr.missing[0].Height)
	require.True(t, missingErr.missing[0].CertIDResolved)
}
