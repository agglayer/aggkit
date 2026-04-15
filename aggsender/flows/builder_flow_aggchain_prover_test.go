package flows

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/aggchainfep"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/query"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
	aggkittypesmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_AggchainProverFlow_GetCertificateBuildParams(t *testing.T) {
	t.Parallel()
	// Set up the test context
	ctx := context.Background()

	finalizedL1Root := common.HexToHash("0x1")
	newRetryProofMismatchMockFn := func(
		certificateID common.Hash,
		cachedLastProvenBlock uint64,
		cachedEndBlock uint64,
	) func(
		*mocks.AggSenderStorage,
		*mocks.BridgeQuerier,
		*mocks.AggchainProofQuerier,
		*mocks.L1InfoTreeDataQuerier,
	) {
		return func(
			mockStorage *mocks.AggSenderStorage,
			mockL2BridgeQuerier *mocks.BridgeQuerier,
			mockAggchainProofQuerier *mocks.AggchainProofQuerier,
			mockL1InfoDataQuery *mocks.L1InfoTreeDataQuerier,
		) {
			rer := common.HexToHash("0x1")
			mer := common.HexToHash("0x2")
			ger := l1infotreesync.CalculateGER(mer, rer)
			mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(ctx).Return(&types.CertificateHeader{
				Height:                  0,
				FromBlock:               1,
				ToBlock:                 10,
				Status:                  agglayertypes.InError,
				FinalizedL1InfoTreeRoot: &finalizedL1Root,
				CertificateID:           certificateID,
				CertType:                types.CertificateTypeFEP,
				L1InfoTreeLeafCount:     11,
			},
				&types.AggchainProof{
					SP1StarkProof:   &types.SP1StarkProof{Proof: []byte("cached-proof")},
					LastProvenBlock: cachedLastProvenBlock,
					EndBlock:        cachedEndBlock,
				}, nil).Once()
			mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(1), uint64(10)).Return(
				[]bridgesync.Bridge{{}},
				[]claimsynctypes.Claim{{
					GlobalIndex:     big.NewInt(1),
					GlobalExitRoot:  ger,
					MainnetExitRoot: mer,
					RollupExitRoot:  rer,
				}},
				nil,
			)
			mockL2BridgeQuerier.EXPECT().GetUnsetClaimsForBlockRange(
				ctx, uint64(1), uint64(10),
			).Return([]claimsynctypes.Unclaim{}, nil)
			mockAggchainProofQuerier.EXPECT().
				GenerateAggchainProof(context.Background(), uint64(0), uint64(10), mock.Anything).
				Return(&types.AggchainProof{
					SP1StarkProof:   &types.SP1StarkProof{Proof: []byte("fresh-proof")},
					LastProvenBlock: 0,
					EndBlock:        10,
				}, nil).Once()
		}
	}
	newRetryProofMismatchExpectedParams := func(certificateID common.Hash) *types.CertificateBuildParams {
		return &types.CertificateBuildParams{
			FromBlock:  1,
			ToBlock:    10,
			RetryCount: 1,
			Bridges:    []bridgesync.Bridge{{}},
			Claims: []claimsynctypes.Claim{{
				GlobalIndex:     big.NewInt(1),
				RollupExitRoot:  common.HexToHash("0x1"),
				MainnetExitRoot: common.HexToHash("0x2"),
				GlobalExitRoot:  l1infotreesync.CalculateGER(common.HexToHash("0x2"), common.HexToHash("0x1")),
			}},
			Unclaims:                       []claimsynctypes.Unclaim{},
			L1InfoTreeRootFromWhichToProve: common.HexToHash("0x1"),
			L1InfoTreeLeafCount:            11,
			AggchainProof: &types.AggchainProof{
				SP1StarkProof:   &types.SP1StarkProof{Proof: []byte("fresh-proof")},
				LastProvenBlock: 0,
				EndBlock:        10,
			},
			LastSentCertificate: &types.CertificateHeader{
				FromBlock:               1,
				ToBlock:                 10,
				Status:                  agglayertypes.InError,
				FinalizedL1InfoTreeRoot: &finalizedL1Root,
				CertificateID:           certificateID,
				CertType:                types.CertificateTypeFEP,
				L1InfoTreeLeafCount:     11,
			},
			CertificateType: types.CertificateTypeFEP,
		}
	}

	testCases := []struct {
		name   string
		mockFn func(*mocks.AggSenderStorage,
			*mocks.BridgeQuerier,
			*mocks.AggchainProofQuerier,
			*mocks.L1InfoTreeDataQuerier,
		)
		expectedParams *types.CertificateBuildParams
		expectedError  string
	}{
		{
			name: "error getting last sent certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockAggchainProofQuerier *mocks.AggchainProofQuerier,
				mockL1InfoDataQuery *mocks.L1InfoTreeDataQuerier) {
				mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(ctx).Return(nil, nil, errors.New("some error"))
			},
			expectedError: "some error",
		},
		{
			name: "resend InError certificate - have aggchain proof in db",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockAggchainProofQuerier *mocks.AggchainProofQuerier,
				mockL1InfoDataQuery *mocks.L1InfoTreeDataQuerier) {
				rer := common.HexToHash("0x1")
				mer := common.HexToHash("0x2")
				ger := l1infotreesync.CalculateGER(mer, rer)
				mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(ctx).Return(&types.CertificateHeader{
					Height:                  0,
					FromBlock:               1,
					ToBlock:                 10,
					Status:                  agglayertypes.InError,
					FinalizedL1InfoTreeRoot: &finalizedL1Root,
					CertificateID:           common.HexToHash("0x1"),
					CertType:                types.CertificateTypeFEP,
				},
					&types.AggchainProof{
						SP1StarkProof:   &types.SP1StarkProof{Proof: []byte("some-proof")},
						LastProvenBlock: 0,
						EndBlock:        10,
					}, nil).Once()
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(1), uint64(10)).Return([]bridgesync.Bridge{{}}, []claimsynctypes.Claim{
					{
						GlobalIndex:     big.NewInt(1),
						GlobalExitRoot:  ger,
						MainnetExitRoot: mer,
						RollupExitRoot:  rer,
					}}, nil)
				mockL2BridgeQuerier.EXPECT().GetUnsetClaimsForBlockRange(ctx, uint64(1), uint64(10)).Return([]claimsynctypes.Unclaim{}, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:  1,
				ToBlock:    10,
				RetryCount: 1,
				Bridges:    []bridgesync.Bridge{{}},
				Claims: []claimsynctypes.Claim{{
					GlobalIndex:     big.NewInt(1),
					RollupExitRoot:  common.HexToHash("0x1"),
					MainnetExitRoot: common.HexToHash("0x2"),
					GlobalExitRoot:  l1infotreesync.CalculateGER(common.HexToHash("0x2"), common.HexToHash("0x1")),
				}},
				Unclaims:                       []claimsynctypes.Unclaim{},
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x1"),
				AggchainProof: &types.AggchainProof{
					SP1StarkProof:   &types.SP1StarkProof{Proof: []byte("some-proof")},
					LastProvenBlock: 0,
					EndBlock:        10,
				},
				LastSentCertificate: &types.CertificateHeader{
					FromBlock:               1,
					ToBlock:                 10,
					Status:                  agglayertypes.InError,
					FinalizedL1InfoTreeRoot: &finalizedL1Root,
					CertificateID:           common.HexToHash("0x1"),
					CertType:                types.CertificateTypeFEP,
				},
				CertificateType: types.CertificateTypeFEP,
			},
		},
		{
			name: "resend InError certificate - cached proof rejected when GER is not provable against selected root",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockAggchainProofQuerier *mocks.AggchainProofQuerier,
				mockL1InfoDataQuery *mocks.L1InfoTreeDataQuerier) {
				rer := common.HexToHash("0x1")
				mer := common.HexToHash("0x2")
				ger := l1infotreesync.CalculateGER(mer, rer)
				mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(ctx).Return(&types.CertificateHeader{
					Height:                  0,
					FromBlock:               1,
					ToBlock:                 10,
					Status:                  agglayertypes.InError,
					FinalizedL1InfoTreeRoot: &finalizedL1Root,
					CertificateID:           common.HexToHash("0x2"),
					CertType:                types.CertificateTypeFEP,
					L1InfoTreeLeafCount:     11,
				},
					&types.AggchainProof{
						SP1StarkProof:   &types.SP1StarkProof{Proof: []byte("cached-proof")},
						LastProvenBlock: 0,
						EndBlock:        10,
					}, nil).Once()
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(1), uint64(10)).Return(
					[]bridgesync.Bridge{{BlockNum: 1}},
					[]claimsynctypes.Claim{{
						BlockNum:        10,
						GlobalIndex:     big.NewInt(1),
						GlobalExitRoot:  ger,
						MainnetExitRoot: mer,
						RollupExitRoot:  rer,
					}},
					nil)
				mockL2BridgeQuerier.EXPECT().GetUnsetClaimsForBlockRange(ctx, uint64(1), uint64(10)).Return(
					[]claimsynctypes.Unclaim{}, nil)
				mockL1InfoDataQuery.EXPECT().GetProofForGER(ctx, ger, finalizedL1Root).
					Return(nil, treetypes.Proof{}, query.ErrGERNotProvableAgainstRoot).Once()
				mockL1InfoDataQuery.EXPECT().DoesGERExistsOnL1(ger).Return(true, nil).Once()
			},
			expectedError: "exists on L1 but cannot be proved against selected root",
		},
		{
			name:           "resend InError certificate - cached proof rejected on EndBlock mismatch",
			mockFn:         newRetryProofMismatchMockFn(common.HexToHash("0x3"), 0, 9),
			expectedParams: newRetryProofMismatchExpectedParams(common.HexToHash("0x3")),
		},
		{
			name:           "resend InError certificate - cached proof rejected on LastProvenBlock mismatch",
			mockFn:         newRetryProofMismatchMockFn(common.HexToHash("0x4"), 7, 10),
			expectedParams: newRetryProofMismatchExpectedParams(common.HexToHash("0x4")),
		},
		{
			name: "resend InError certificate - no aggchain proof in db",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockAggchainProofQuerier *mocks.AggchainProofQuerier,
				mockL1InfoDataQuery *mocks.L1InfoTreeDataQuerier) {
				rer := common.HexToHash("0x1")
				mer := common.HexToHash("0x2")
				ger := l1infotreesync.CalculateGER(mer, rer)
				mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(ctx).Return(&types.CertificateHeader{
					Height:                  0,
					FromBlock:               1,
					ToBlock:                 10,
					Status:                  agglayertypes.InError,
					CertificateID:           common.HexToHash("0x1"),
					CertType:                types.CertificateTypeFEP,
					FinalizedL1InfoTreeRoot: &finalizedL1Root,
					L1InfoTreeLeafCount:     11,
				}, nil, nil).Once()
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(1), uint64(10)).Return([]bridgesync.Bridge{{}}, []claimsynctypes.Claim{
					{
						GlobalIndex:     big.NewInt(1),
						GlobalExitRoot:  ger,
						MainnetExitRoot: mer,
						RollupExitRoot:  rer,
					}}, nil)
				mockL2BridgeQuerier.EXPECT().GetUnsetClaimsForBlockRange(ctx, uint64(1), uint64(10)).Return([]claimsynctypes.Unclaim{}, nil)
				mockAggchainProofQuerier.EXPECT().GenerateAggchainProof(context.Background(), uint64(0), uint64(10), mock.Anything).
					Return(&types.AggchainProof{
						SP1StarkProof:   &types.SP1StarkProof{Proof: []byte("some-proof")},
						LastProvenBlock: 0,
						EndBlock:        10,
					}, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				CertificateType: types.CertificateTypeFEP,
				FromBlock:       1,
				ToBlock:         10,
				RetryCount:      1,
				LastSentCertificate: &types.CertificateHeader{
					FromBlock:               1,
					ToBlock:                 10,
					Status:                  agglayertypes.InError,
					CertificateID:           common.HexToHash("0x1"),
					CertType:                types.CertificateTypeFEP,
					FinalizedL1InfoTreeRoot: &finalizedL1Root,
					L1InfoTreeLeafCount:     11,
				},
				Bridges:             []bridgesync.Bridge{{}},
				L1InfoTreeLeafCount: 11,
				Claims: []claimsynctypes.Claim{{
					GlobalIndex:     big.NewInt(1),
					RollupExitRoot:  common.HexToHash("0x1"),
					MainnetExitRoot: common.HexToHash("0x2"),
					GlobalExitRoot:  l1infotreesync.CalculateGER(common.HexToHash("0x2"), common.HexToHash("0x1")),
				}},
				Unclaims:                       []claimsynctypes.Unclaim{},
				L1InfoTreeRootFromWhichToProve: finalizedL1Root,
				AggchainProof: &types.AggchainProof{
					SP1StarkProof:   &types.SP1StarkProof{Proof: []byte("some-proof")},
					LastProvenBlock: 0,
					EndBlock:        10,
				},
			},
		},
		{
			name: "error fetching aggchain proof for new certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockAggchainProofQuerier *mocks.AggchainProofQuerier,
				mockL1InfoDataQuery *mocks.L1InfoTreeDataQuerier) {
				rer := common.HexToHash("0x1")
				mer := common.HexToHash("0x2")
				ger := l1infotreesync.CalculateGER(mer, rer)
				mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(ctx).Return(nil, nil, nil).Once()
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(nil, nil).Once()
				mockL1InfoDataQuery.EXPECT().GetTargetL1InfoRoot(mock.Anything).Return(
					&treetypes.Root{Hash: finalizedL1Root, BlockNum: 10}, nil, nil)
				mockL2BridgeQuerier.On("GetLastProcessedBlock", ctx).Return(uint64(10), true, nil)
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(1), uint64(10)).Return([]bridgesync.Bridge{{}}, []claimsynctypes.Claim{
					{
						GlobalIndex:     big.NewInt(1),
						GlobalExitRoot:  ger,
						MainnetExitRoot: mer,
						RollupExitRoot:  rer,
					}}, nil)
				mockL2BridgeQuerier.EXPECT().GetUnsetClaimsForBlockRange(ctx, uint64(1), uint64(10)).Return([]claimsynctypes.Unclaim{}, nil)
				mockL1InfoDataQuery.EXPECT().GetProofForGER(ctx, ger, finalizedL1Root).
					Return(&l1infotreesync.L1InfoTreeLeaf{}, treetypes.Proof{}, nil).Once()
				mockAggchainProofQuerier.EXPECT().GenerateAggchainProof(context.Background(), uint64(0), uint64(10), mock.Anything).
					Return(nil, errors.New("some error"))
			},
			expectedError: "aggchainProverFlow - error generating aggchain proof: some error",
		},
		{
			name: "error fetching aggchain proof for new certificate - no proofs built yet",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockAggchainProofQuerier *mocks.AggchainProofQuerier,
				mockL1InfoDataQuery *mocks.L1InfoTreeDataQuerier) {
				mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(ctx).Return(nil, nil, nil).Once()
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(nil, nil).Once()
				mockL2BridgeQuerier.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(10), true, nil)
				mockL1InfoDataQuery.EXPECT().GetTargetL1InfoRoot(mock.Anything).Return(
					&treetypes.Root{Hash: finalizedL1Root, BlockNum: 10}, nil, nil)
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(1), uint64(10)).Return([]bridgesync.Bridge{}, []claimsynctypes.Claim{}, nil)
				mockL2BridgeQuerier.EXPECT().GetUnsetClaimsForBlockRange(ctx, uint64(1), uint64(10)).Return([]claimsynctypes.Unclaim{}, nil)
				wrappedErr := fmt.Errorf("wrapped error: %w", query.ErrNoProofBuiltYet)
				mockAggchainProofQuerier.EXPECT().GenerateAggchainProof(context.Background(), uint64(0), uint64(10), mock.Anything).
					Return(nil, wrappedErr)
			},
			expectedError:  "",
			expectedParams: nil, // expecting no params to be returned since no proof was built
		},
		{
			name: "success fetching aggchain proof for new certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockAggchainProofQuerier *mocks.AggchainProofQuerier,
				mockL1InfoDataQuery *mocks.L1InfoTreeDataQuerier) {
				rer := common.HexToHash("0x1")
				mer := common.HexToHash("0x2")
				ger := l1infotreesync.CalculateGER(mer, rer)
				mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(ctx).Return(&types.CertificateHeader{ToBlock: 5, Status: agglayertypes.Settled}, nil, nil).Once()
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(&types.CertificateHeader{ToBlock: 5}, nil).Once()
				mockL1InfoDataQuery.EXPECT().GetTargetL1InfoRoot(mock.Anything).Return(
					&treetypes.Root{Hash: finalizedL1Root, BlockNum: 10, Index: 10}, nil, nil)
				mockL2BridgeQuerier.On("GetLastProcessedBlock", ctx).Return(uint64(10), true, nil)
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(6), uint64(10)).Return([]bridgesync.Bridge{{}}, []claimsynctypes.Claim{{
					GlobalIndex:     big.NewInt(1),
					GlobalExitRoot:  ger,
					MainnetExitRoot: mer,
					RollupExitRoot:  rer,
				}}, nil)
				mockL2BridgeQuerier.EXPECT().GetUnsetClaimsForBlockRange(ctx, uint64(6), uint64(10)).Return([]claimsynctypes.Unclaim{}, nil)
				mockL1InfoDataQuery.EXPECT().GetProofForGER(ctx, ger, finalizedL1Root).
					Return(&l1infotreesync.L1InfoTreeLeaf{}, treetypes.Proof{}, nil).Once()
				mockAggchainProofQuerier.EXPECT().GenerateAggchainProof(context.Background(), uint64(5), uint64(10), mock.Anything).
					Return(&types.AggchainProof{
						SP1StarkProof:   &types.SP1StarkProof{Proof: []byte("some-proof")},
						LastProvenBlock: 6,
						EndBlock:        10,
					}, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:  6,
				ToBlock:    10,
				RetryCount: 0,
				LastSentCertificate: &types.CertificateHeader{
					ToBlock: 5,
				},
				Bridges:             []bridgesync.Bridge{{}},
				L1InfoTreeLeafCount: 11,
				Claims: []claimsynctypes.Claim{{
					GlobalIndex:     big.NewInt(1),
					RollupExitRoot:  common.HexToHash("0x1"),
					MainnetExitRoot: common.HexToHash("0x2"),
					GlobalExitRoot:  l1infotreesync.CalculateGER(common.HexToHash("0x2"), common.HexToHash("0x1")),
				}},
				Unclaims:                       []claimsynctypes.Unclaim{},
				L1InfoTreeRootFromWhichToProve: finalizedL1Root,
				AggchainProof: &types.AggchainProof{
					SP1StarkProof:   &types.SP1StarkProof{Proof: []byte("some-proof")},
					LastProvenBlock: 6,
					EndBlock:        10,
				},
				CreatedAt:       timeNowUTCForTest(),
				CertificateType: types.CertificateTypeFEP,
			},
		},
		{
			name: "success fetching aggchain proof for new certificate - aggchain prover returns smaller range",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockAggchainProofQuerier *mocks.AggchainProofQuerier,
				mockL1InfoDataQuery *mocks.L1InfoTreeDataQuerier) {
				rer := common.HexToHash("0x1")
				mer := common.HexToHash("0x2")
				ger := l1infotreesync.CalculateGER(mer, rer)
				mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(ctx).Return(&types.CertificateHeader{ToBlock: 5, Status: agglayertypes.Settled}, nil, nil).Once()
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(&types.CertificateHeader{ToBlock: 5}, nil).Once()
				mockL1InfoDataQuery.EXPECT().GetTargetL1InfoRoot(mock.Anything).Return(
					&treetypes.Root{Hash: finalizedL1Root, BlockNum: 10, Index: 10}, nil, nil)
				mockL2BridgeQuerier.On("GetLastProcessedBlock", ctx).Return(uint64(10), true, nil)
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(6), uint64(10)).Return(
					[]bridgesync.Bridge{{BlockNum: 6}, {BlockNum: 10}},
					[]claimsynctypes.Claim{
						{BlockNum: 8, GlobalIndex: big.NewInt(1), GlobalExitRoot: ger, MainnetExitRoot: mer, RollupExitRoot: rer},
						{BlockNum: 9, GlobalIndex: big.NewInt(2), GlobalExitRoot: ger, MainnetExitRoot: mer, RollupExitRoot: rer}},
					nil)
				mockL2BridgeQuerier.EXPECT().GetUnsetClaimsForBlockRange(ctx, uint64(6), uint64(10)).Return([]claimsynctypes.Unclaim{}, nil)
				mockL1InfoDataQuery.EXPECT().GetProofForGER(ctx, ger, finalizedL1Root).
					Return(&l1infotreesync.L1InfoTreeLeaf{}, treetypes.Proof{}, nil).Once()
				mockAggchainProofQuerier.EXPECT().GenerateAggchainProof(context.Background(), uint64(5), uint64(10), mock.Anything).
					Return(&types.AggchainProof{
						SP1StarkProof:   &types.SP1StarkProof{Proof: []byte("some-proof")},
						LastProvenBlock: 6,
						EndBlock:        8,
					}, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:           6,
				ToBlock:             8,
				RetryCount:          0,
				L1InfoTreeLeafCount: 11,
				LastSentCertificate: &types.CertificateHeader{
					ToBlock: 5,
				},
				Bridges: []bridgesync.Bridge{{BlockNum: 6}},
				Claims: []claimsynctypes.Claim{{
					BlockNum:        8,
					GlobalIndex:     big.NewInt(1),
					RollupExitRoot:  common.HexToHash("0x1"),
					MainnetExitRoot: common.HexToHash("0x2"),
					GlobalExitRoot:  l1infotreesync.CalculateGER(common.HexToHash("0x2"), common.HexToHash("0x1")),
				}},
				Unclaims:                       []claimsynctypes.Unclaim{},
				L1InfoTreeRootFromWhichToProve: finalizedL1Root,
				AggchainProof: &types.AggchainProof{
					SP1StarkProof:   &types.SP1StarkProof{Proof: []byte("some-proof")},
					LastProvenBlock: 6,
					EndBlock:        8,
				},
				CreatedAt:       timeNowUTCForTest(),
				CertificateType: types.CertificateTypeFEP,
			},
		},
		{
			name: "error when prover result would require a further range adjustment",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockAggchainProofQuerier *mocks.AggchainProofQuerier,
				mockL1InfoDataQuery *mocks.L1InfoTreeDataQuerier) {
				rer := common.HexToHash("0x1")
				mer := common.HexToHash("0x2")
				ger := l1infotreesync.CalculateGER(mer, rer)
				mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(ctx).
					Return(&types.CertificateHeader{ToBlock: 5, Status: agglayertypes.Settled}, nil, nil).Once()
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(&types.CertificateHeader{ToBlock: 5}, nil).Once()
				mockL1InfoDataQuery.EXPECT().GetTargetL1InfoRoot(mock.Anything).Return(
					&treetypes.Root{Hash: finalizedL1Root, BlockNum: 10, Index: 10}, nil, nil)
				mockL2BridgeQuerier.On("GetLastProcessedBlock", ctx).Return(uint64(10), true, nil)
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(6), uint64(10)).Return(
					[]bridgesync.Bridge{{BlockNum: 6}, {BlockNum: 10}},
					[]claimsynctypes.Claim{{
						BlockNum:        7,
						GlobalIndex:     big.NewInt(1),
						GlobalExitRoot:  ger,
						MainnetExitRoot: mer,
						RollupExitRoot:  rer,
					}}, nil)
				mockL2BridgeQuerier.EXPECT().GetUnsetClaimsForBlockRange(ctx, uint64(6), uint64(10)).Return(
					[]claimsynctypes.Unclaim{{
						BlockNumber: 9,
						GlobalIndex: big.NewInt(1),
					}}, nil)
				mockL1InfoDataQuery.EXPECT().GetProofForGER(ctx, ger, finalizedL1Root).
					Return(nil, treetypes.Proof{}, errors.New("not found")).Twice()
				mockL1InfoDataQuery.EXPECT().DoesGERExistsOnL1(ger).Return(false, nil).Twice()
				mockAggchainProofQuerier.EXPECT().GenerateAggchainProof(context.Background(), uint64(5), uint64(10), mock.Anything).
					Return(&types.AggchainProof{
						SP1StarkProof:   &types.SP1StarkProof{Proof: []byte("some-proof")},
						LastProvenBlock: 6,
						EndBlock:        8,
					}, nil)
			},
			expectedError: "aggchainProverFlow - block range adjustment required after prover result: [6,8] -> [6,6]",
		},
	}

	for _, tca := range testCases {
		tc := tca
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockAggchainProofQuerier := mocks.NewAggchainProofQuerier(t)
			mockStorage := mocks.NewAggSenderStorage(t)
			mockL2BridgeQuerier := mocks.NewBridgeQuerier(t)
			mockOptimistic := mocks.NewOptimisticModeQuerier(t)
			mockL1InfoTreeDataQuerier := mocks.NewL1InfoTreeDataQuerier(t)
			mockSigner := mocks.NewSigner(t)
			logger := log.WithFields("flowManager", "Test_AggchainProverFlow_GetCertificateBuildParams")
			flowBase := NewBaseFlow(
				logger,
				mockL2BridgeQuerier,
				mockStorage,
				mockL1InfoTreeDataQuerier,
				bridgesynctypes.EmptyLER,
				nil,
				NewBaseFlowConfig(0, 0, false, true))
			flowBase.timeNowFunc = timeNowUTCForTest
			aggchainFlow := NewAggchainProverBuilderFlow(
				logger,
				NewAggchainProverFlowConfigDefault(),
				flowBase,
				mockStorage,
				mockL1InfoTreeDataQuerier,
				mockL2BridgeQuerier,
				mockSigner,
				mockOptimistic,
				mockAggchainProofQuerier,
			)
			mockOptimistic.EXPECT().IsOptimisticModeOn().Return(false, nil).Maybe()
			tc.mockFn(mockStorage, mockL2BridgeQuerier, mockAggchainProofQuerier, mockL1InfoTreeDataQuerier)
			mockL1InfoTreeDataQuerier.EXPECT().GetProofForGER(mock.Anything, mock.Anything, mock.Anything).
				Return(&l1infotreesync.L1InfoTreeLeaf{}, treetypes.Proof{}, nil).Maybe()
			mockL1InfoTreeDataQuerier.EXPECT().DoesGERExistsOnL1(mock.Anything).Return(true, nil).Maybe()

			params, err := aggchainFlow.GetCertificateBuildParams(ctx)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedParams, params)
			}

			mockStorage.AssertExpectations(t)
			mockL2BridgeQuerier.AssertExpectations(t)
			mockL1InfoTreeDataQuerier.AssertExpectations(t)
			mockL1InfoTreeDataQuerier.AssertExpectations(t)
			mockAggchainProofQuerier.AssertExpectations(t)
		})
	}
}

func Test_AggchainProverFlow_getLastProvenBlock(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                string
		fromBlock           uint64
		startL2Block        uint64
		expectedResult      uint64
		lastSentCertificate *types.CertificateHeader
	}{
		{
			name:           "fromBlock is 0, return startL2Block",
			fromBlock:      0,
			startL2Block:   1,
			expectedResult: 1,
		},
		{
			name:           "fromBlock is 0, startL2Block is 0",
			fromBlock:      0,
			startL2Block:   0,
			expectedResult: 0,
		},
		{
			name:           "fromBlock is greater than 0",
			fromBlock:      10,
			startL2Block:   1,
			expectedResult: 9,
		},
		{
			name:         "lastSentCertificate settled on PP",
			fromBlock:    10,
			startL2Block: 50,
			lastSentCertificate: &types.CertificateHeader{
				FromBlock: 10,
				ToBlock:   20,
				Status:    agglayertypes.Settled,
			},
			expectedResult: 50,
		},
		{
			name:         "lastSentCertificate settled on PP on the fence",
			fromBlock:    10,
			startL2Block: 50,
			lastSentCertificate: &types.CertificateHeader{
				FromBlock: 10,
				ToBlock:   50,
				Status:    agglayertypes.Settled,
			},
			expectedResult: 50,
		},
		{
			name:                "lastSentCertificate settled on PP on the fence. Case 2",
			fromBlock:           50,
			startL2Block:        50,
			lastSentCertificate: nil,
			expectedResult:      50,
		},
		{
			name:                "lastSentCertificate settled on PP on the fence. Case 3",
			fromBlock:           51,
			startL2Block:        50,
			lastSentCertificate: nil,
			expectedResult:      50,
		},
		{
			name:                "lastSentCertificate settled on PP on the fence. Case 4",
			fromBlock:           52,
			startL2Block:        50,
			lastSentCertificate: nil,
			expectedResult:      51,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			logger := log.WithFields("flowManager", "Test_AggchainProverFlow_GetCertificateBuildParams")

			flowBase := NewBaseFlow(
				logger,
				nil, // l2BridgeQuerier
				nil, // sotrage
				nil, // l1InfoTreeDataQuerier,
				bridgesynctypes.EmptyLER,
				nil, // certQuerier
				NewBaseFlowConfig(0, tc.startL2Block, false, true),
			)
			flow := NewAggchainProverBuilderFlow(
				logger,
				NewAggchainProverFlowConfigDefault(),
				flowBase,
				nil, // mockStorage
				nil, // mockL1InfoTreeDataQuerier
				nil, // mockL2BridgeQuerier
				nil, // mockSigner
				nil, // optimisticModeQuerier
				nil, // aggchainProofQuerier
			)

			result := flow.getLastProvenBlock(tc.fromBlock, tc.lastSentCertificate)
			require.Equal(t, tc.expectedResult, result)
		})
	}
}

func Test_AggchainProverFlow_BuildCertificate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	createdAt := time.Now().UTC()

	testCases := []struct {
		name           string
		mockFn         func(*mocks.BridgeQuerier)
		buildParams    *types.CertificateBuildParams
		expectedError  string
		expectedResult *agglayertypes.Certificate
	}{
		{
			name: "error building certificate",
			mockFn: func(mockL2BridgeQuerier *mocks.BridgeQuerier) {
				mockL2BridgeQuerier.EXPECT().GetExitRootByIndex(mock.Anything, uint32(0)).Return(common.Hash{}, errors.New("some error"))
			},
			buildParams: &types.CertificateBuildParams{
				FromBlock:                      1,
				ToBlock:                        10,
				Bridges:                        []bridgesync.Bridge{{}},
				Claims:                         []claimsynctypes.Claim{},
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x1"),
			},
			expectedError: "error getting exit root by index",
		},
		{
			name: "success building certificate",
			mockFn: func(mockL2BridgeQuerier *mocks.BridgeQuerier) {
				mockL2BridgeQuerier.EXPECT().OriginNetwork().Return(uint32(1))
			},
			buildParams: &types.CertificateBuildParams{
				FromBlock:                      1,
				ToBlock:                        10,
				Bridges:                        []bridgesync.Bridge{},
				Claims:                         []claimsynctypes.Claim{},
				CreatedAt:                      uint32(createdAt.Unix()),
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x1"),
				CertificateType:                types.CertificateTypeFEP,
				AggchainProof: &types.AggchainProof{
					SP1StarkProof: &types.SP1StarkProof{
						Proof:   []byte("some-proof"),
						Version: "0.1",
						Vkey:    []byte("some-vkey"),
					},
					LastProvenBlock: 1,
					EndBlock:        10,
					CustomChainData: []byte("some-data"),
					LocalExitRoot:   common.HexToHash("0x1"),
					AggchainParams:  common.HexToHash("0x2"),
					Context: map[string][]byte{
						"key1": []byte("value1"),
					},
				},
			},
			expectedResult: &agglayertypes.Certificate{
				NetworkID:           1,
				Height:              0,
				NewLocalExitRoot:    bridgesynctypes.EmptyLER,
				CustomChainData:     []byte("some-data"),
				BridgeExits:         []*agglayertypes.BridgeExit{},
				ImportedBridgeExits: []*agglayertypes.ImportedBridgeExit{},
				PrevLocalExitRoot:   bridgesynctypes.EmptyLER,
				L1InfoTreeLeafCount: 0,
				AggchainData: &agglayertypes.AggchainDataProof{
					Proof:          []byte("some-proof"),
					Version:        "0.1",
					Vkey:           []byte("some-vkey"),
					AggchainParams: common.HexToHash("0x2"),
					Context: map[string][]byte{
						"key1": []byte("value1"),
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			logger := log.WithFields("flowManager", "Test_AggchainProverFlow_BuildCertificate")
			mockSigner := mocks.NewSigner(t)
			mockL2BridgeQuerier := mocks.NewBridgeQuerier(t)
			if tc.mockFn != nil {
				tc.mockFn(mockL2BridgeQuerier)
			}
			flowBase := NewBaseFlow(
				logger,
				mockL2BridgeQuerier,
				nil, // mockStorage
				nil, // mockL1InfoTreeDataQuerier
				bridgesynctypes.EmptyLER,
				nil, // certQuerier
				NewBaseFlowConfigDefault(),
			)
			aggchainFlow := NewAggchainProverBuilderFlow(
				logger,
				NewAggchainProverFlowConfigDefault(),
				flowBase,
				nil, // mockStorage
				nil, // mockL1InfoTreeDataQuerier
				mockL2BridgeQuerier,
				mockSigner,
				nil, // optimisticModeQuerier
				nil, // aggchainProofQuerier
			)

			certificate, err := aggchainFlow.BuildCertificate(ctx, tc.buildParams)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.NotNil(t, certificate)
				require.Equal(t, tc.expectedResult, certificate)
			}
		})
	}
}

func getResponseContractCallStartingBlockNumber(returnValue int64) ([]byte, error) {
	expectedBlockNumber := big.NewInt(returnValue)
	parsedABI, err := abi.JSON(strings.NewReader(aggchainfep.AggchainfepABI))
	if err != nil {
		return nil, fmt.Errorf("failed to parse ABI: %w", err)
	}
	method := parsedABI.Methods["startingBlockNumber"]
	encodedReturnValue, err := method.Outputs.Pack(expectedBlockNumber)
	if err != nil {
		return nil, fmt.Errorf("failed to pack method: %w", err)
	}
	return encodedReturnValue, nil
}

func Test_AggchainProverFlow_getL2StartBlock(t *testing.T) {
	t.Parallel()
	sovereignRollupAddr := common.HexToAddress("0x123")

	testCases := []struct {
		name          string
		mockFn        func(mockEthClient *aggkittypesmocks.BaseEthereumClienter)
		expectedBlock uint64
		expectedError string
	}{
		{
			name: "error creating sovereign rollup caller",
			mockFn: func(mockEthClient *aggkittypesmocks.BaseEthereumClienter) {
				mockEthClient.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("some error")).Once()
			},
			expectedError: "aggchainProverFlow",
		},
		{
			name: "ok fetching starting block number",
			mockFn: func(mockEthClient *aggkittypesmocks.BaseEthereumClienter) {
				encodedReturnValue, err := getResponseContractCallStartingBlockNumber(12345)
				if err != nil {
					t.Fatalf("failed to pack method: %v", err)
				}
				mockEthClient.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).Return(
					encodedReturnValue, nil)
			},
			expectedBlock: 12345,
			expectedError: "",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockEthClient := aggkittypesmocks.NewBaseEthereumClienter(t)

			tc.mockFn(mockEthClient)

			block, err := getL2StartBlock(sovereignRollupAddr, mockEthClient)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedBlock, block)
			}

			mockEthClient.AssertExpectations(t)
		})
	}
}

func Test_AggchainProverFlow_CheckInitialStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	testCases := []struct {
		name                 string
		requireNoFEPBlockGap bool
		mockFn               func(
			mockStorage *mocks.AggSenderStorage,
			mockBaseFlow *mocks.AggsenderFlowBaser,
			mockL2BridgeSyncer *mocks.BridgeQuerier,
		)
		expectedError string
	}{
		{
			name: "error getting last sent certificate",
			mockFn: func(
				mockStorage *mocks.AggSenderStorage,
				mockBaseFlow *mocks.AggsenderFlowBaser,
				mockL2BridgeSyncer *mocks.BridgeQuerier,
			) {
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(nil, errors.New("db error")).Once()
			},
			expectedError: "aggchainProverFlow - error getting last sent certificate: db error",
		},
		{
			name: "error waiting for syncer to catch up",
			mockFn: func(
				mockStorage *mocks.AggSenderStorage,
				mockBaseFlow *mocks.AggsenderFlowBaser,
				mockL2BridgeSyncer *mocks.BridgeQuerier,
			) {
				lastCert := &types.CertificateHeader{ToBlock: 10}
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(lastCert, nil).Once()
				mockBaseFlow.EXPECT().StartL2Block().Return(uint64(15)).Once()
				mockL2BridgeSyncer.EXPECT().WaitForSyncerToCatchUp(ctx, uint64(15)).Return(errors.New("sync error")).Once()
			},
			expectedError: "aggchainProverFlow - error waiting for syncer to catch up: sync error",
		},
		{
			name: "error verifying block range gaps - has bridge transactions in gap",
			mockFn: func(
				mockStorage *mocks.AggSenderStorage,
				mockBaseFlow *mocks.AggsenderFlowBaser,
				mockL2BridgeSyncer *mocks.BridgeQuerier,
			) {
				lastCert := &types.CertificateHeader{ToBlock: 10}
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(lastCert, nil).Once()
				mockBaseFlow.EXPECT().StartL2Block().Return(uint64(15)).Once()
				mockL2BridgeSyncer.EXPECT().WaitForSyncerToCatchUp(ctx, uint64(15)).Return(nil).Once()
				mockBaseFlow.EXPECT().VerifyBlockRangeGaps(ctx, lastCert, uint64(15), uint64(15)).
					Return(errors.New("gap error")).Once()
			},
			expectedError: "aggchainProverFlow - error verifying block range gaps on startup",
		},
		{
			name:                 "success ",
			requireNoFEPBlockGap: true,
			mockFn: func(
				mockStorage *mocks.AggSenderStorage,
				mockBaseFlow *mocks.AggsenderFlowBaser,
				mockL2BridgeSyncer *mocks.BridgeQuerier,
			) {
				lastCert := &types.CertificateHeader{ToBlock: 10}
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(lastCert, nil).Once()
				mockBaseFlow.EXPECT().StartL2Block().Return(uint64(11)).Once()
				mockL2BridgeSyncer.EXPECT().WaitForSyncerToCatchUp(ctx, uint64(11)).Return(nil).Once()
				mockBaseFlow.EXPECT().VerifyBlockRangeGaps(ctx, lastCert, uint64(11), uint64(11)).
					Return(nil).Once()
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mockStorage := mocks.NewAggSenderStorage(t)
			mockBaseFlow := mocks.NewAggsenderFlowBaser(t)
			mockL2BridgeSyncer := mocks.NewBridgeQuerier(t)
			logger := log.WithFields("flowManager", "Test_AggchainProverFlow_CheckInitialStatus")

			flow := &AggchainProverBuilderFlow{
				log:             logger,
				storage:         mockStorage,
				baseFlow:        mockBaseFlow,
				l2BridgeQuerier: mockL2BridgeSyncer,
			}

			tc.mockFn(mockStorage, mockBaseFlow, mockL2BridgeSyncer)

			err := flow.CheckInitialStatus(ctx)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
			}

			mockStorage.AssertExpectations(t)
			mockBaseFlow.AssertExpectations(t)
			mockL2BridgeSyncer.AssertExpectations(t)
		})
	}
}

func Test_AggchainProverFlow_GenerateBuildParams(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	testCases := []struct {
		name           string
		preParams      *types.CertificatePreBuildParams
		mockFn         func(*mocks.AggsenderFlowBaser)
		expectedParams *types.CertificateBuildParams
		expectedError  string
	}{
		{
			name:          "preParams is nil",
			preParams:     nil,
			expectedError: "aggchainProverFlow - preParams is nil",
		},
		{
			name: "error generating build params from baseFlow",
			preParams: &types.CertificatePreBuildParams{
				BlockRange: aggkitcommon.NewBlockRange(1, 10),
			},
			mockFn: func(mockBaseFlow *mocks.AggsenderFlowBaser) {
				mockBaseFlow.EXPECT().GenerateBuildParams(ctx, types.CertificatePreBuildParams{
					BlockRange: aggkitcommon.NewBlockRange(1, 10),
				}).Return(nil, errors.New("base flow error")).Once()
			},
			expectedError: "aggchainProverFlow - error generating build params: base flow error",
		},
		{
			name: "success generating build params",
			preParams: &types.CertificatePreBuildParams{
				BlockRange: aggkitcommon.NewBlockRange(1, 10),
			},
			mockFn: func(mockBaseFlow *mocks.AggsenderFlowBaser) {
				expectedParams := &types.CertificateBuildParams{
					FromBlock:       1,
					ToBlock:         10,
					RetryCount:      0,
					Bridges:         []bridgesync.Bridge{{}},
					Claims:          []claimsynctypes.Claim{},
					CreatedAt:       timeNowUTCForTest(),
					CertificateType: types.CertificateTypeFEP,
				}
				mockBaseFlow.EXPECT().GenerateBuildParams(ctx, types.CertificatePreBuildParams{
					BlockRange: aggkitcommon.NewBlockRange(1, 10),
				}).Return(expectedParams, nil).Once()
				mockBaseFlow.EXPECT().AdjustBlockRange(ctx, expectedParams, types.BlockRangeAdjustmentOptions{
					MaxL2BlockNumber:              0,
					AllowResizeRetryCert:          false,
					RequireOneBridgeInCertificate: false,
					ValidateRootToProve:           true,
					DisableSizeLimit:              true,
				}).Return(expectedParams, nil).Once()
				mockBaseFlow.EXPECT().VerifyBuildParams(ctx, expectedParams).Return(nil).Once()
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:       1,
				ToBlock:         10,
				RetryCount:      0,
				Bridges:         []bridgesync.Bridge{{}},
				Claims:          []claimsynctypes.Claim{},
				CreatedAt:       timeNowUTCForTest(),
				CertificateType: types.CertificateTypeFEP,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockBaseFlow := mocks.NewAggsenderFlowBaser(t)
			logger := log.WithFields("flowManager", "Test_AggchainProverFlow_GenerateBuildParams")

			if tc.mockFn != nil {
				tc.mockFn(mockBaseFlow)
			}

			flow := &AggchainProverBuilderFlow{
				log:      logger,
				baseFlow: mockBaseFlow,
			}

			params, err := flow.GenerateBuildParams(ctx, tc.preParams)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
				require.Nil(t, params)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedParams, params)
			}

			mockBaseFlow.AssertExpectations(t)
		})
	}
}

func Test_AggchainProverFlow_UpdateAggchainData(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                string
		certificate         *agglayertypes.Certificate
		multisig            *agglayertypes.Multisig
		expectedError       string
		expectedCertificate *agglayertypes.Certificate
	}{
		{
			name: "multisig nil - returns original certificate unchanged",
			certificate: &agglayertypes.Certificate{
				AggchainData: &agglayertypes.AggchainDataProof{
					Proof: []byte("orig-proof"),
				},
			},
			multisig: nil,
			expectedCertificate: &agglayertypes.Certificate{
				AggchainData: &agglayertypes.AggchainDataProof{
					Proof: []byte("orig-proof"),
				},
			},
		},
		{
			name: "aggchain data not AggchainDataProof - returns error",
			certificate: &agglayertypes.Certificate{
				AggchainData: &agglayertypes.AggchainDataSignature{}, // wrong type
			},
			multisig:      &agglayertypes.Multisig{},
			expectedError: "aggchainProverFlow: AggchainData of unknown type *types.AggchainDataSignature received",
		},
		{
			name: "successful update - wraps proof with multisig",
			certificate: &agglayertypes.Certificate{
				AggchainData: &agglayertypes.AggchainDataProof{
					Proof:          []byte("some-proof"),
					Version:        "0.1",
					Vkey:           []byte("vkey"),
					AggchainParams: common.HexToHash("0x2"),
					Context:        map[string][]byte{"k": []byte("v")},
				},
			},
			multisig: &agglayertypes.Multisig{},
			expectedCertificate: &agglayertypes.Certificate{
				AggchainData: &agglayertypes.AggchainDataMultisigWithProof{
					Multisig: &agglayertypes.Multisig{},
					AggchainProof: &agglayertypes.AggchainDataProof{
						Proof:          []byte("some-proof"),
						Version:        "0.1",
						Vkey:           []byte("vkey"),
						AggchainParams: common.HexToHash("0x2"),
						Context:        map[string][]byte{"k": []byte("v")},
					},
				},
			},
		},
		{
			name: "successful update - with aggchain data proof with multisig",
			certificate: &agglayertypes.Certificate{
				AggchainData: &agglayertypes.AggchainDataMultisigWithProof{
					AggchainProof: &agglayertypes.AggchainDataProof{
						Proof:          []byte("some-proof"),
						Version:        "0.1",
						Vkey:           []byte("vkey"),
						AggchainParams: common.HexToHash("0x2"),
						Context:        map[string][]byte{"k": []byte("v")},
					},
				},
			},
			multisig: &agglayertypes.Multisig{},
			expectedCertificate: &agglayertypes.Certificate{
				AggchainData: &agglayertypes.AggchainDataMultisigWithProof{
					Multisig: &agglayertypes.Multisig{},
					AggchainProof: &agglayertypes.AggchainDataProof{
						Proof:          []byte("some-proof"),
						Version:        "0.1",
						Vkey:           []byte("vkey"),
						AggchainParams: common.HexToHash("0x2"),
						Context:        map[string][]byte{"k": []byte("v")},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			logger := log.WithFields("flowManager", "Test_AggchainProverFlow_UpdateAggchainData")
			flow := &AggchainProverBuilderFlow{
				log: logger,
			}

			err := flow.UpdateAggchainData(tc.certificate, tc.multisig)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedCertificate, tc.certificate)
			}
		})
	}
}
