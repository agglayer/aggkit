package query

import (
	"errors"
	"testing"

	agglayermocks "github.com/agglayer/aggkit/agglayer/mocks"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestGetLastSettledCertificateToBlock(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	testCases := []struct {
		name          string
		certificate   *agglayertypes.CertificateHeader
		mockFn        func(*mocks.AggchainFEPRollupQuerier, *agglayermocks.AgglayerClientMock, *mocks.L2BridgeSyncer)
		expectedErr   string
		expectedBlock uint64
	}{
		{
			name: "certificate not settled",
			certificate: &agglayertypes.CertificateHeader{
				Status: agglayertypes.Pending,
			},
			expectedErr: "certificate",
		},
		{
			name: "successful with all sources returning data",
			certificate: &agglayertypes.CertificateHeader{
				Status:           agglayertypes.Settled,
				NewLocalExitRoot: common.HexToHash("0x123"),
			},
			mockFn: func(aggchainQuerier *mocks.AggchainFEPRollupQuerier, agglayerClient *agglayermocks.AgglayerClientMock, bridgeSyncer *mocks.L2BridgeSyncer) {
				bridgeSyncer.EXPECT().GetExitRootByHash(ctx, common.HexToHash("0x123")).Return(&treetypes.Root{
					BlockNum: uint64(100),
				}, nil)

				importedBridgeExit := &agglayertypes.GlobalIndex{}
				agglayerClient.EXPECT().GetLatestSettledImportedBridgeExit(ctx).Return(importedBridgeExit, nil)

				bridgeSyncer.EXPECT().GetClaimByGlobalIndex(ctx, importedBridgeExit.ToBigInt()).Return(bridgesync.Claim{
					BlockNum: 150,
				}, nil)

				aggchainQuerier.EXPECT().GetLastSettledL2Block().Return(uint64(200), nil)
			},
			expectedBlock: 200, // max of 100, 150, 200
		},
		{
			name: "empty local exit root with imported bridge exit",
			certificate: &agglayertypes.CertificateHeader{
				Status:           agglayertypes.Settled,
				NewLocalExitRoot: types.EmptyLER,
			},
			mockFn: func(aggchainQuerier *mocks.AggchainFEPRollupQuerier, agglayerClient *agglayermocks.AgglayerClientMock, bridgeSyncer *mocks.L2BridgeSyncer) {
				importedBridgeExit := &agglayertypes.GlobalIndex{}
				agglayerClient.EXPECT().GetLatestSettledImportedBridgeExit(ctx).Return(importedBridgeExit, nil)
				bridgeSyncer.EXPECT().GetClaimByGlobalIndex(ctx, importedBridgeExit.ToBigInt()).Return(bridgesync.Claim{
					BlockNum: 50,
				}, nil)
				aggchainQuerier.EXPECT().GetLastSettledL2Block().Return(uint64(75), nil)
			},
			expectedBlock: 75, // max of 0, 50, 75
		},
		{
			name: "no imported bridge exit data",
			certificate: &agglayertypes.CertificateHeader{
				Status:           agglayertypes.Settled,
				NewLocalExitRoot: common.HexToHash("0x456"),
			},
			mockFn: func(aggchainQuerier *mocks.AggchainFEPRollupQuerier, agglayerClient *agglayermocks.AgglayerClientMock, bridgeSyncer *mocks.L2BridgeSyncer) {
				bridgeSyncer.EXPECT().GetExitRootByHash(ctx, common.HexToHash("0x456")).Return(&treetypes.Root{
					BlockNum: uint64(300),
				}, nil)
				agglayerClient.EXPECT().GetLatestSettledImportedBridgeExit(ctx).Return(nil, nil)
				aggchainQuerier.EXPECT().GetLastSettledL2Block().Return(uint64(250), nil)
			},
			expectedBlock: 300, // max of 300, 0, 250
		},
		{
			name: "error getting exit root by hash",
			certificate: &agglayertypes.CertificateHeader{
				Status:           agglayertypes.Settled,
				NewLocalExitRoot: common.HexToHash("0x789"),
			},
			mockFn: func(aggchainQuerier *mocks.AggchainFEPRollupQuerier, agglayerClient *agglayermocks.AgglayerClientMock, bridgeSyncer *mocks.L2BridgeSyncer) {
				bridgeSyncer.EXPECT().GetExitRootByHash(ctx, common.HexToHash("0x789")).Return(nil, errors.New("exit root not found"))
			},
			expectedErr: "failed to resolve the bridge exit block number for NewLocalExitRoot",
		},
		{
			name: "error getting latest settled imported bridge exit",
			certificate: &agglayertypes.CertificateHeader{
				Status:           agglayertypes.Settled,
				NewLocalExitRoot: types.EmptyLER,
			},
			mockFn: func(aggchainQuerier *mocks.AggchainFEPRollupQuerier, agglayerClient *agglayermocks.AgglayerClientMock, bridgeSyncer *mocks.L2BridgeSyncer) {
				agglayerClient.EXPECT().GetLatestSettledImportedBridgeExit(ctx).Return(nil, errors.New("agglayer error"))
			},
			expectedErr: "failed to get latest settled imported bridge exit from agglayer",
		},
		{
			name: "error getting claim by global index",
			certificate: &agglayertypes.CertificateHeader{
				Status:           agglayertypes.Settled,
				NewLocalExitRoot: types.EmptyLER,
			},
			mockFn: func(aggchainQuerier *mocks.AggchainFEPRollupQuerier, agglayerClient *agglayermocks.AgglayerClientMock, bridgeSyncer *mocks.L2BridgeSyncer) {
				importedBridgeExit := &agglayertypes.GlobalIndex{}
				agglayerClient.EXPECT().GetLatestSettledImportedBridgeExit(ctx).Return(importedBridgeExit, nil)
				bridgeSyncer.EXPECT().GetClaimByGlobalIndex(ctx, importedBridgeExit.ToBigInt()).Return(bridgesync.Claim{}, errors.New("claim not found"))
			},
			expectedErr: "failed to get claim by global index",
		},
		{
			name: "error getting last settled L2 block",
			certificate: &agglayertypes.CertificateHeader{
				Status:           agglayertypes.Settled,
				NewLocalExitRoot: types.EmptyLER,
			},
			mockFn: func(aggchainQuerier *mocks.AggchainFEPRollupQuerier, agglayerClient *agglayermocks.AgglayerClientMock, bridgeSyncer *mocks.L2BridgeSyncer) {
				agglayerClient.EXPECT().GetLatestSettledImportedBridgeExit(ctx).Return(nil, nil)
				aggchainQuerier.EXPECT().GetLastSettledL2Block().Return(uint64(0), errors.New("L2 block query failed"))
			},
			expectedErr: "failed to get last settled L2 block",
		},
		{
			name: "all sources return zero values",
			certificate: &agglayertypes.CertificateHeader{
				Status:           agglayertypes.Settled,
				NewLocalExitRoot: types.EmptyLER,
			},
			mockFn: func(aggchainQuerier *mocks.AggchainFEPRollupQuerier, agglayerClient *agglayermocks.AgglayerClientMock, bridgeSyncer *mocks.L2BridgeSyncer) {
				agglayerClient.EXPECT().GetLatestSettledImportedBridgeExit(ctx).Return(nil, nil)
				aggchainQuerier.EXPECT().GetLastSettledL2Block().Return(uint64(0), nil)
			},
			expectedBlock: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockAggchainFEPQuerier := mocks.NewAggchainFEPRollupQuerier(t)
			mockAgglayerClient := agglayermocks.NewAgglayerClientMock(t)
			mockL2BridgeSyncer := mocks.NewL2BridgeSyncer(t)

			if tc.mockFn != nil {
				tc.mockFn(mockAggchainFEPQuerier, mockAgglayerClient, mockL2BridgeSyncer)
			}

			certRangeQuerier := NewCertificateQuerier(
				mockL2BridgeSyncer,
				mockAggchainFEPQuerier,
				mockAgglayerClient,
			)

			block, err := certRangeQuerier.GetLastSettledCertificateToBlock(ctx, tc.certificate)
			if tc.expectedErr != "" {
				require.ErrorContains(t, err, tc.expectedErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedBlock, block)
			}

			mockAgglayerClient.AssertExpectations(t)
			mockL2BridgeSyncer.AssertExpectations(t)
			mockAggchainFEPQuerier.AssertExpectations(t)
		})
	}
}

func TestGetNewCertificateToBlock(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	testCases := []struct {
		name          string
		certificate   *agglayertypes.Certificate
		mockFn        func(*mocks.L2BridgeSyncer)
		expectedErr   string
		expectedBlock uint64
	}{
		{
			name: "successful with both local exit root and imported bridge exits",
			certificate: &agglayertypes.Certificate{
				NewLocalExitRoot: common.HexToHash("0x123"),
				ImportedBridgeExits: []*agglayertypes.ImportedBridgeExit{
					{GlobalIndex: &agglayertypes.GlobalIndex{}},
					{GlobalIndex: &agglayertypes.GlobalIndex{}},
				},
			},
			mockFn: func(bridgeSyncer *mocks.L2BridgeSyncer) {
				bridgeSyncer.EXPECT().GetExitRootByHash(ctx, common.HexToHash("0x123")).Return(&treetypes.Root{
					BlockNum: uint64(100),
				}, nil)
				lastImportedBridgeExit := &agglayertypes.GlobalIndex{}
				bridgeSyncer.EXPECT().GetClaimByGlobalIndex(ctx, lastImportedBridgeExit.ToBigInt()).Return(bridgesync.Claim{
					BlockNum: 150,
				}, nil)
			},
			expectedBlock: 150, // max of 100, 150
		},
		{
			name: "empty local exit root with imported bridge exits",
			certificate: &agglayertypes.Certificate{
				NewLocalExitRoot: types.EmptyLER,
				ImportedBridgeExits: []*agglayertypes.ImportedBridgeExit{
					{GlobalIndex: &agglayertypes.GlobalIndex{}},
				},
			},
			mockFn: func(bridgeSyncer *mocks.L2BridgeSyncer) {
				importedBridgeExit := &agglayertypes.GlobalIndex{}
				bridgeSyncer.EXPECT().GetClaimByGlobalIndex(ctx, importedBridgeExit.ToBigInt()).Return(bridgesync.Claim{
					BlockNum: 75,
				}, nil)
			},
			expectedBlock: 75, // max of 0, 75
		},
		{
			name: "non-empty local exit root with no imported bridge exits",
			certificate: &agglayertypes.Certificate{
				NewLocalExitRoot:    common.HexToHash("0x456"),
				ImportedBridgeExits: []*agglayertypes.ImportedBridgeExit{},
			},
			mockFn: func(bridgeSyncer *mocks.L2BridgeSyncer) {
				bridgeSyncer.EXPECT().GetExitRootByHash(ctx, common.HexToHash("0x456")).Return(&treetypes.Root{
					BlockNum: uint64(200),
				}, nil)
			},
			expectedBlock: 200, // max of 200, 0
		},
		{
			name: "empty local exit root with no imported bridge exits",
			certificate: &agglayertypes.Certificate{
				NewLocalExitRoot:    types.EmptyLER,
				ImportedBridgeExits: []*agglayertypes.ImportedBridgeExit{},
			},
			expectedBlock: 0, // max of 0, 0
		},
		{
			name: "nil imported bridge exits",
			certificate: &agglayertypes.Certificate{
				NewLocalExitRoot:    common.HexToHash("0x789"),
				ImportedBridgeExits: nil,
			},
			mockFn: func(bridgeSyncer *mocks.L2BridgeSyncer) {
				bridgeSyncer.EXPECT().GetExitRootByHash(ctx, common.HexToHash("0x789")).Return(&treetypes.Root{
					BlockNum: uint64(300),
				}, nil)
			},
			expectedBlock: 300, // max of 300, 0
		},
		{
			name: "error getting exit root by hash",
			certificate: &agglayertypes.Certificate{
				NewLocalExitRoot:    common.HexToHash("0xabc"),
				ImportedBridgeExits: []*agglayertypes.ImportedBridgeExit{},
			},
			mockFn: func(bridgeSyncer *mocks.L2BridgeSyncer) {
				bridgeSyncer.EXPECT().GetExitRootByHash(ctx, common.HexToHash("0xabc")).Return(nil, errors.New("exit root not found"))
			},
			expectedErr: "failed to resolve the bridge exit block number for NewLocalExitRoot",
		},
		{
			name: "error getting claim by global index",
			certificate: &agglayertypes.Certificate{
				NewLocalExitRoot: types.EmptyLER,
				ImportedBridgeExits: []*agglayertypes.ImportedBridgeExit{
					{GlobalIndex: &agglayertypes.GlobalIndex{}},
				},
			},
			mockFn: func(bridgeSyncer *mocks.L2BridgeSyncer) {
				importedBridgeExit := &agglayertypes.GlobalIndex{}
				bridgeSyncer.EXPECT().GetClaimByGlobalIndex(ctx, importedBridgeExit.ToBigInt()).Return(bridgesync.Claim{}, errors.New("claim not found"))
			},
			expectedErr: "failed to get claim by global index",
		},
		{
			name: "multiple imported bridge exits uses last one",
			certificate: &agglayertypes.Certificate{
				NewLocalExitRoot: types.EmptyLER,
				ImportedBridgeExits: []*agglayertypes.ImportedBridgeExit{
					{GlobalIndex: &agglayertypes.GlobalIndex{}}, // First one - should not be used
					{GlobalIndex: &agglayertypes.GlobalIndex{}}, // Second one - should not be used
					{GlobalIndex: &agglayertypes.GlobalIndex{}}, // Last one - should be used
				},
			},
			mockFn: func(bridgeSyncer *mocks.L2BridgeSyncer) {
				// Mock claim by global index for last imported bridge exit only
				lastImportedBridgeExit := &agglayertypes.GlobalIndex{}
				bridgeSyncer.EXPECT().GetClaimByGlobalIndex(ctx, lastImportedBridgeExit.ToBigInt()).Return(bridgesync.Claim{
					BlockNum: 250,
				}, nil)
			},
			expectedBlock: 250, // max of 0, 250
		},
		{
			name: "local exit root block higher than imported bridge exit block",
			certificate: &agglayertypes.Certificate{
				NewLocalExitRoot: common.HexToHash("0xdef"),
				ImportedBridgeExits: []*agglayertypes.ImportedBridgeExit{
					{GlobalIndex: &agglayertypes.GlobalIndex{}},
				},
			},
			mockFn: func(bridgeSyncer *mocks.L2BridgeSyncer) {
				bridgeSyncer.EXPECT().GetExitRootByHash(ctx, common.HexToHash("0xdef")).Return(&treetypes.Root{
					BlockNum: uint64(400),
				}, nil)

				importedBridgeExit := &agglayertypes.GlobalIndex{}
				bridgeSyncer.EXPECT().GetClaimByGlobalIndex(ctx, importedBridgeExit.ToBigInt()).Return(bridgesync.Claim{
					BlockNum: 100,
				}, nil)
			},
			expectedBlock: 400, // max of 400, 100
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockAggchainFEPQuerier := mocks.NewAggchainFEPRollupQuerier(t)
			mockAgglayerClient := agglayermocks.NewAgglayerClientMock(t)
			mockL2BridgeSyncer := mocks.NewL2BridgeSyncer(t)

			if tc.mockFn != nil {
				tc.mockFn(mockL2BridgeSyncer)
			}

			certQuerier := NewCertificateQuerier(
				mockL2BridgeSyncer,
				mockAggchainFEPQuerier,
				mockAgglayerClient,
			)

			block, err := certQuerier.GetNewCertificateToBlock(ctx, tc.certificate)
			if tc.expectedErr != "" {
				require.ErrorContains(t, err, tc.expectedErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedBlock, block)
			}

			mockL2BridgeSyncer.AssertExpectations(t)
		})
	}
}

func TestCalculateCertificateTypeFromToBlock(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name             string
		certToBlock      uint64
		isFEP            bool
		startL2Block     uint64
		expectedCertType types.CertificateType
	}{
		{
			name:             "FEP network - certToBlock before startL2Block returns PP",
			certToBlock:      50,
			isFEP:            true,
			startL2Block:     100,
			expectedCertType: types.CertificateTypePP,
		},
		{
			name:             "FEP network - certToBlock equal to startL2Block returns FEP",
			certToBlock:      100,
			isFEP:            true,
			startL2Block:     100,
			expectedCertType: types.CertificateTypeFEP,
		},
		{
			name:             "FEP network - certToBlock after startL2Block returns FEP",
			certToBlock:      150,
			isFEP:            true,
			startL2Block:     100,
			expectedCertType: types.CertificateTypeFEP,
		},
		{
			name:             "FEP network - zero certToBlock and non-zero startL2Block returns PP",
			certToBlock:      0,
			isFEP:            true,
			startL2Block:     50,
			expectedCertType: types.CertificateTypePP,
		},
		{
			name:             "FEP network - non-zero certToBlock and zero startL2Block returns FEP",
			certToBlock:      50,
			isFEP:            true,
			startL2Block:     0,
			expectedCertType: types.CertificateTypeFEP,
		},
		{
			name:             "FEP network - both zero values returns FEP",
			certToBlock:      0,
			isFEP:            true,
			startL2Block:     0,
			expectedCertType: types.CertificateTypeFEP,
		},
		{
			name:             "non-FEP network - any certToBlock returns PP",
			certToBlock:      100,
			isFEP:            false,
			startL2Block:     50, // Should not be called
			expectedCertType: types.CertificateTypePP,
		},
		{
			name:             "non-FEP network - zero certToBlock returns PP",
			certToBlock:      0,
			isFEP:            false,
			startL2Block:     0, // Should not be called
			expectedCertType: types.CertificateTypePP,
		},
		{
			name:             "non-FEP network - large certToBlock returns PP",
			certToBlock:      999999,
			isFEP:            false,
			startL2Block:     100, // Should not be called
			expectedCertType: types.CertificateTypePP,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockAggchainFEPQuerier := mocks.NewAggchainFEPRollupQuerier(t)
			mockAgglayerClient := agglayermocks.NewAgglayerClientMock(t)
			mockL2BridgeSyncer := mocks.NewL2BridgeSyncer(t)

			// Always expect IsFEP call
			mockAggchainFEPQuerier.EXPECT().IsFEP().Return(tc.isFEP).Once()

			// Only expect StartL2Block call if IsFEP returns true
			if tc.isFEP {
				mockAggchainFEPQuerier.EXPECT().StartL2Block().Return(tc.startL2Block).Once()
			}

			certQuerier := NewCertificateQuerier(
				mockL2BridgeSyncer,
				mockAggchainFEPQuerier,
				mockAgglayerClient,
			)

			certType := certQuerier.CalculateCertificateTypeFromToBlock(tc.certToBlock)
			require.Equal(t, tc.expectedCertType, certType)

			mockAggchainFEPQuerier.AssertExpectations(t)
		})
	}
}

func TestCalculateCertificateType(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name             string
		certificate      *agglayertypes.Certificate
		certToBlock      uint64
		isFEP            bool
		startL2Block     uint64
		expectedCertType types.CertificateType
	}{
		{
			name: "AggchainData is AggchainDataProof - returns PP",
			certificate: &agglayertypes.Certificate{
				AggchainData: &agglayertypes.AggchainDataProof{},
			},
			certToBlock:      100,
			expectedCertType: types.CertificateTypePP,
		},
		{
			name: "AggchainData is not AggchainDataProof - returns FEP",
			certificate: &agglayertypes.Certificate{
				AggchainData: &agglayertypes.AggchainDataSignature{},
			},
			certToBlock:      100,
			expectedCertType: types.CertificateTypeFEP,
		},
		{
			name: "AggchainData is nil - falls back to CalculateCertificateTypeFromToBlock with FEP network and block before start",
			certificate: &agglayertypes.Certificate{
				AggchainData: nil,
			},
			certToBlock:      50,
			isFEP:            true,
			startL2Block:     100,
			expectedCertType: types.CertificateTypePP,
		},
		{
			name: "AggchainData is nil - falls back to CalculateCertificateTypeFromToBlock with FEP network and block after start",
			certificate: &agglayertypes.Certificate{
				AggchainData: nil,
			},
			certToBlock:      150,
			isFEP:            true,
			startL2Block:     100,
			expectedCertType: types.CertificateTypeFEP,
		},
		{
			name: "AggchainData is nil - falls back to CalculateCertificateTypeFromToBlock with non-FEP network",
			certificate: &agglayertypes.Certificate{
				AggchainData: nil,
			},
			certToBlock:      100,
			isFEP:            false,
			expectedCertType: types.CertificateTypePP,
		},
		{
			name: "AggchainData is nil - falls back with FEP network and equal blocks",
			certificate: &agglayertypes.Certificate{
				AggchainData: nil,
			},
			certToBlock:      100,
			isFEP:            true,
			startL2Block:     100,
			expectedCertType: types.CertificateTypeFEP,
		},
		{
			name: "AggchainData is nil - falls back with zero certToBlock in FEP network",
			certificate: &agglayertypes.Certificate{
				AggchainData: nil,
			},
			certToBlock:      0,
			isFEP:            true,
			startL2Block:     50,
			expectedCertType: types.CertificateTypePP,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockAggchainFEPQuerier := mocks.NewAggchainFEPRollupQuerier(t)
			mockAgglayerClient := agglayermocks.NewAgglayerClientMock(t)
			mockL2BridgeSyncer := mocks.NewL2BridgeSyncer(t)

			// Only expect calls to querier if AggchainData is nil (fallback case)
			if tc.certificate.AggchainData == nil {
				mockAggchainFEPQuerier.EXPECT().IsFEP().Return(tc.isFEP).Once()
				if tc.isFEP {
					mockAggchainFEPQuerier.EXPECT().StartL2Block().Return(tc.startL2Block).Once()
				}
			}

			certQuerier := NewCertificateQuerier(
				mockL2BridgeSyncer,
				mockAggchainFEPQuerier,
				mockAgglayerClient,
			)

			certType := certQuerier.CalculateCertificateType(tc.certificate, tc.certToBlock)
			require.Equal(t, tc.expectedCertType, certType)

			mockAggchainFEPQuerier.AssertExpectations(t)
		})
	}
}
