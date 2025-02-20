package aggsender

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	treeTypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	gethTypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

func Test_AggchainProverFlow_GetCertificateBuildParams(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	ibe1 := &agglayer.ImportedBridgeExit{
		BridgeExit: &agglayer.BridgeExit{
			LeafType:  0,
			TokenInfo: &agglayer.TokenInfo{},
		},
		GlobalIndex: &agglayer.GlobalIndex{
			LeafIndex: 1,
		},
	}

	ibe2 := &agglayer.ImportedBridgeExit{
		BridgeExit: &agglayer.BridgeExit{
			LeafType:  0,
			TokenInfo: &agglayer.TokenInfo{},
		},
		GlobalIndex: &agglayer.GlobalIndex{
			LeafIndex: 2,
		},
	}

	testCases := []struct {
		name   string
		mockFn func(*mocks.AggSenderStorage,
			*mocks.L2BridgeSyncer,
			*mocks.AggchainProofClientInterface,
			*mocks.EthClient,
			*mocks.L1InfoTreeSyncer,
			*mocks.ChainGERReader,
		)
		expectedParams *types.CertificateBuildParams
		expectedError  string
	}{
		{
			name: "error getting last sent certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockProverClient *mocks.AggchainProofClientInterface,
				mockL1Client *mocks.EthClient,
				mockL1InfTreeSyncer *mocks.L1InfoTreeSyncer,
				mockL2Etherman *mocks.ChainGERReader) {
				mockStorage.On("GetLastSentCertificate").Return(nil, errors.New("some error"))
			},
			expectedError: "some error",
		},
		{
			name: "resend InError certificate with no bridges",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockProverClient *mocks.AggchainProofClientInterface,
				mockL1Client *mocks.EthClient,
				mockL1InfTreeSyncer *mocks.L1InfoTreeSyncer,
				mockL2Etherman *mocks.ChainGERReader) {
				mockStorage.On("GetLastSentCertificate").Return(&types.CertificateInfo{
					FromBlock: 1,
					ToBlock:   10,
					Status:    agglayer.InError,
				}, nil)
				mockL2Syncer.On("GetBridgesPublished", ctx, uint64(1), uint64(10)).Return([]bridgesync.Bridge{}, nil)
			},
			expectedError: "no bridges to resend the same certificate",
		},
		{
			name: "resend InError certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockProverClient *mocks.AggchainProofClientInterface,
				mockL1Client *mocks.EthClient,
				mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer,
				mockL2Etherman *mocks.ChainGERReader) {
				l1Header := &gethTypes.Header{Number: big.NewInt(10)}
				mockStorage.On("GetLastSentCertificate").Return(&types.CertificateInfo{
					FromBlock: 1,
					ToBlock:   10,
					Status:    agglayer.InError,
				}, nil)
				mockL2Syncer.On("GetBridgesPublished", ctx, uint64(1), uint64(10)).Return([]bridgesync.Bridge{{}}, nil)
				mockL2Syncer.On("GetClaims", ctx, uint64(1), uint64(10)).Return([]bridgesync.Claim{{GlobalIndex: big.NewInt(1)}}, nil)
				mockL1Client.On("HeaderByNumber", ctx, finalizedBlockBigInt).Return(l1Header, nil)
				mockL1InfoTreeSyncer.On("GetProcessedBlockUntil", ctx, l1Header.Number.Uint64()).Return(l1Header.Number.Uint64(), l1Header.Hash(), nil)
				mockL1InfoTreeSyncer.On("GetLastL1InfoTreeRootByBlockNum", ctx, l1Header.Number.Uint64()).Return(
					&treeTypes.Root{Hash: common.HexToHash("0x1"), Index: 0}, nil)
				mockL1InfoTreeSyncer.On("GetInfoByIndex", ctx, uint32(0)).Return(&l1infotreesync.L1InfoTreeLeaf{
					BlockNumber: l1Header.Number.Uint64(), Hash: common.HexToHash("0x2")}, nil)
				mockL1InfoTreeSyncer.On("GetL1InfoTreeMerkleProofFromIndexToRoot", ctx, uint32(0), common.HexToHash("0x1")).Return(
					treeTypes.Proof{}, nil)
				mockL2Etherman.On("GetInjectedGERsForRange", ctx, uint64(1), uint64(10)).Return([]common.Hash{}, nil)
				mockProverClient.On("GenerateAggchainProof", uint64(1), uint64(10),
					common.HexToHash("0x1"), l1infotreesync.L1InfoTreeLeaf{
						BlockNumber: l1Header.Number.Uint64(),
						Hash:        common.HexToHash("0x2"),
					},
					treeTypes.Proof{}, make(map[common.Hash]treeTypes.Proof, 0),
					[]*agglayer.ImportedBridgeExit{ibe1}).Return(&types.AggchainProof{
					Proof: []byte("some-proof"), StartBlock: 1, EndBlock: 10}, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:                      1,
				ToBlock:                        10,
				RetryCount:                     1,
				Bridges:                        []bridgesync.Bridge{{}},
				Claims:                         []bridgesync.Claim{{GlobalIndex: big.NewInt(1)}},
				L1InfoTreeRootFromWhichToProve: &treeTypes.Root{Hash: common.HexToHash("0x1"), Index: 0},
				AggchainProof:                  []byte("some-proof"),
				LastSentCertificate: &types.CertificateInfo{
					FromBlock: 1,
					ToBlock:   10,
					Status:    agglayer.InError,
				},
			},
		},
		{
			name: "resend InError certificate - aggchain prover returned smaller range",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockProverClient *mocks.AggchainProofClientInterface,
				mockL1Client *mocks.EthClient,
				mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer,
				mockL2Etherman *mocks.ChainGERReader) {
				l1Header := &gethTypes.Header{Number: big.NewInt(10)}
				mockStorage.On("GetLastSentCertificate").Return(&types.CertificateInfo{
					FromBlock: 1,
					ToBlock:   10,
					Status:    agglayer.InError,
				}, nil)
				mockL2Syncer.On("GetBridgesPublished", ctx, uint64(1), uint64(10)).Return([]bridgesync.Bridge{
					{BlockNum: 5}, {BlockNum: 10}}, nil)
				mockL2Syncer.On("GetClaims", ctx, uint64(1), uint64(10)).Return([]bridgesync.Claim{
					{BlockNum: 6, GlobalIndex: big.NewInt(1)}, {BlockNum: 9, GlobalIndex: big.NewInt(2)}}, nil)
				mockL1Client.On("HeaderByNumber", ctx, finalizedBlockBigInt).Return(l1Header, nil)
				mockL1InfoTreeSyncer.On("GetProcessedBlockUntil", ctx, l1Header.Number.Uint64()).Return(l1Header.Number.Uint64(), l1Header.Hash(), nil)
				mockL1InfoTreeSyncer.On("GetLastL1InfoTreeRootByBlockNum", ctx, l1Header.Number.Uint64()).Return(
					&treeTypes.Root{Hash: common.HexToHash("0x1"), Index: 0}, nil)
				mockL1InfoTreeSyncer.On("GetInfoByIndex", ctx, uint32(0)).Return(&l1infotreesync.L1InfoTreeLeaf{
					BlockNumber: l1Header.Number.Uint64(), Hash: common.HexToHash("0x2")}, nil)
				mockL1InfoTreeSyncer.On("GetL1InfoTreeMerkleProofFromIndexToRoot", ctx, uint32(0), common.HexToHash("0x1")).Return(
					treeTypes.Proof{}, nil)
				mockL2Etherman.On("GetInjectedGERsForRange", ctx, uint64(1), uint64(10)).Return([]common.Hash{}, nil)
				mockProverClient.On("GenerateAggchainProof", uint64(1), uint64(10),
					common.HexToHash("0x1"), l1infotreesync.L1InfoTreeLeaf{
						BlockNumber: l1Header.Number.Uint64(),
						Hash:        common.HexToHash("0x2"),
					},
					treeTypes.Proof{}, make(map[common.Hash]treeTypes.Proof, 0),
					[]*agglayer.ImportedBridgeExit{ibe1, ibe2}).Return(&types.AggchainProof{
					Proof: []byte("some-proof"), StartBlock: 1, EndBlock: 8}, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:                      1,
				ToBlock:                        8,
				RetryCount:                     1,
				Bridges:                        []bridgesync.Bridge{{BlockNum: 5}},
				Claims:                         []bridgesync.Claim{{BlockNum: 6, GlobalIndex: big.NewInt(1)}},
				L1InfoTreeRootFromWhichToProve: &treeTypes.Root{Hash: common.HexToHash("0x1"), Index: 0},
				AggchainProof:                  []byte("some-proof"),
				LastSentCertificate: &types.CertificateInfo{
					FromBlock: 1,
					ToBlock:   10,
					Status:    agglayer.InError,
				},
			},
		},
		{
			name: "error fetching aggchain proof for new certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockProverClient *mocks.AggchainProofClientInterface,
				mockL1Client *mocks.EthClient,
				mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer,
				mockL2Etherman *mocks.ChainGERReader) {
				l1Header := &gethTypes.Header{Number: big.NewInt(10)}
				mockStorage.On("GetLastSentCertificate").Return(nil, nil).Twice()
				mockL2Syncer.On("GetLastProcessedBlock", ctx).Return(uint64(10), nil)
				mockL2Syncer.On("GetBridgesPublished", ctx, uint64(1), uint64(10)).Return([]bridgesync.Bridge{{}}, nil)
				mockL2Syncer.On("GetClaims", ctx, uint64(1), uint64(10)).Return([]bridgesync.Claim{{GlobalIndex: big.NewInt(1)}}, nil)
				mockL1Client.On("HeaderByNumber", ctx, finalizedBlockBigInt).Return(l1Header, nil)
				mockL1InfoTreeSyncer.On("GetProcessedBlockUntil", ctx, l1Header.Number.Uint64()).Return(l1Header.Number.Uint64(), l1Header.Hash(), nil)
				mockL1InfoTreeSyncer.On("GetLastL1InfoTreeRootByBlockNum", ctx, l1Header.Number.Uint64()).Return(
					&treeTypes.Root{Hash: common.HexToHash("0x1"), Index: 0}, nil)
				mockL1InfoTreeSyncer.On("GetInfoByIndex", ctx, uint32(0)).Return(&l1infotreesync.L1InfoTreeLeaf{
					BlockNumber: l1Header.Number.Uint64(), Hash: common.HexToHash("0x2")}, nil)
				mockL1InfoTreeSyncer.On("GetL1InfoTreeMerkleProofFromIndexToRoot", ctx, uint32(0), common.HexToHash("0x1")).Return(
					treeTypes.Proof{}, nil)
				mockL2Etherman.On("GetInjectedGERsForRange", ctx, uint64(1), uint64(10)).Return([]common.Hash{}, nil)
				mockProverClient.On("GenerateAggchainProof", uint64(1), uint64(10),
					common.HexToHash("0x1"), l1infotreesync.L1InfoTreeLeaf{
						BlockNumber: l1Header.Number.Uint64(),
						Hash:        common.HexToHash("0x2"),
					},
					treeTypes.Proof{}, make(map[common.Hash]treeTypes.Proof, 0),
					[]*agglayer.ImportedBridgeExit{ibe1}).Return(nil, errors.New("some error"))
			},
			expectedError: "error fetching aggchain proof for block range 1 : 10 : some error",
		},
		{
			name: "success fetching aggchain proof for new certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockProverClient *mocks.AggchainProofClientInterface,
				mockL1Client *mocks.EthClient,
				mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer,
				mockL2Etherman *mocks.ChainGERReader) {
				l1Header := &gethTypes.Header{Number: big.NewInt(10)}
				mockStorage.On("GetLastSentCertificate").Return(&types.CertificateInfo{ToBlock: 5}, nil).Twice()
				mockL2Syncer.On("GetLastProcessedBlock", ctx).Return(uint64(10), nil)
				mockL2Syncer.On("GetBridgesPublished", ctx, uint64(6), uint64(10)).Return([]bridgesync.Bridge{{}}, nil)
				mockL2Syncer.On("GetClaims", ctx, uint64(6), uint64(10)).Return([]bridgesync.Claim{{GlobalIndex: big.NewInt(1)}}, nil)
				mockL1Client.On("HeaderByNumber", ctx, finalizedBlockBigInt).Return(l1Header, nil)
				mockL1InfoTreeSyncer.On("GetProcessedBlockUntil", ctx, l1Header.Number.Uint64()).Return(l1Header.Number.Uint64(), l1Header.Hash(), nil)
				mockL1InfoTreeSyncer.On("GetLastL1InfoTreeRootByBlockNum", ctx, l1Header.Number.Uint64()).Return(
					&treeTypes.Root{Hash: common.HexToHash("0x1"), Index: 0}, nil)
				mockL1InfoTreeSyncer.On("GetInfoByIndex", ctx, uint32(0)).Return(&l1infotreesync.L1InfoTreeLeaf{
					BlockNumber: l1Header.Number.Uint64(), Hash: common.HexToHash("0x2")}, nil)
				mockL1InfoTreeSyncer.On("GetL1InfoTreeMerkleProofFromIndexToRoot", ctx, uint32(0), common.HexToHash("0x1")).Return(
					treeTypes.Proof{}, nil)
				mockL2Etherman.On("GetInjectedGERsForRange", ctx, uint64(6), uint64(10)).Return([]common.Hash{}, nil)
				mockProverClient.On("GenerateAggchainProof", uint64(6), uint64(10),
					common.HexToHash("0x1"), l1infotreesync.L1InfoTreeLeaf{
						BlockNumber: l1Header.Number.Uint64(),
						Hash:        common.HexToHash("0x2"),
					}, treeTypes.Proof{}, make(map[common.Hash]treeTypes.Proof, 0),
					[]*agglayer.ImportedBridgeExit{ibe1}).Return(&types.AggchainProof{
					Proof: []byte("some-proof"), StartBlock: 6, EndBlock: 10}, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:                      6,
				ToBlock:                        10,
				RetryCount:                     0,
				LastSentCertificate:            &types.CertificateInfo{ToBlock: 5},
				Bridges:                        []bridgesync.Bridge{{}},
				Claims:                         []bridgesync.Claim{{GlobalIndex: big.NewInt(1)}},
				L1InfoTreeRootFromWhichToProve: &treeTypes.Root{Hash: common.HexToHash("0x1"), Index: 0},
				AggchainProof:                  []byte("some-proof"),
				CreatedAt:                      uint32(time.Now().UTC().Unix()),
			},
		},
		{
			name: "success fetching aggchain proof for new certificate - aggchain prover returns smaller range",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2Syncer *mocks.L2BridgeSyncer,
				mockProverClient *mocks.AggchainProofClientInterface,
				mockL1Client *mocks.EthClient,
				mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer,
				mockL2Etherman *mocks.ChainGERReader) {
				l1Header := &gethTypes.Header{Number: big.NewInt(10)}
				mockStorage.On("GetLastSentCertificate").Return(&types.CertificateInfo{ToBlock: 5}, nil).Twice()
				mockL2Syncer.On("GetLastProcessedBlock", ctx).Return(uint64(10), nil)
				mockL2Syncer.On("GetBridgesPublished", ctx, uint64(6), uint64(10)).Return([]bridgesync.Bridge{
					{BlockNum: 6}, {BlockNum: 10}}, nil)
				mockL2Syncer.On("GetClaims", ctx, uint64(6), uint64(10)).Return([]bridgesync.Claim{
					{BlockNum: 8, GlobalIndex: big.NewInt(1)}, {BlockNum: 9, GlobalIndex: big.NewInt(2)}}, nil)
				mockL1Client.On("HeaderByNumber", ctx, finalizedBlockBigInt).Return(l1Header, nil)
				mockL1InfoTreeSyncer.On("GetProcessedBlockUntil", ctx, l1Header.Number.Uint64()).Return(l1Header.Number.Uint64(), l1Header.Hash(), nil)
				mockL1InfoTreeSyncer.On("GetLastL1InfoTreeRootByBlockNum", ctx, l1Header.Number.Uint64()).Return(
					&treeTypes.Root{Hash: common.HexToHash("0x1"), Index: 0}, nil)
				mockL1InfoTreeSyncer.On("GetInfoByIndex", ctx, uint32(0)).Return(&l1infotreesync.L1InfoTreeLeaf{
					BlockNumber: l1Header.Number.Uint64(), Hash: common.HexToHash("0x2")}, nil)
				mockL1InfoTreeSyncer.On("GetL1InfoTreeMerkleProofFromIndexToRoot", ctx, uint32(0), common.HexToHash("0x1")).Return(
					treeTypes.Proof{}, nil)
				mockL2Etherman.On("GetInjectedGERsForRange", ctx, uint64(6), uint64(10)).Return([]common.Hash{}, nil)
				mockProverClient.On("GenerateAggchainProof", uint64(6), uint64(10),
					common.HexToHash("0x1"), l1infotreesync.L1InfoTreeLeaf{
						BlockNumber: l1Header.Number.Uint64(),
						Hash:        common.HexToHash("0x2"),
					}, treeTypes.Proof{}, make(map[common.Hash]treeTypes.Proof, 0),
					[]*agglayer.ImportedBridgeExit{ibe1, ibe2}).Return(&types.AggchainProof{
					Proof: []byte("some-proof"), StartBlock: 6, EndBlock: 8}, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:                      6,
				ToBlock:                        8,
				RetryCount:                     0,
				LastSentCertificate:            &types.CertificateInfo{ToBlock: 5},
				Bridges:                        []bridgesync.Bridge{{BlockNum: 6}},
				Claims:                         []bridgesync.Claim{{BlockNum: 8, GlobalIndex: big.NewInt(1)}},
				L1InfoTreeRootFromWhichToProve: &treeTypes.Root{Hash: common.HexToHash("0x1"), Index: 0},
				AggchainProof:                  []byte("some-proof"),
				CreatedAt:                      uint32(time.Now().UTC().Unix()),
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockAggchainProofClient := mocks.NewAggchainProofClientInterface(t)
			mockL1InfoTreeSyncer := mocks.NewL1InfoTreeSyncer(t)
			mockStorage := mocks.NewAggSenderStorage(t)
			mockL2Syncer := mocks.NewL2BridgeSyncer(t)
			mockL2Etherman := mocks.NewChainGERReader(t)
			mockL1Client := mocks.NewEthClient(t)
			aggchainFlow := &aggchainProverFlow{
				l1Client:            mockL1Client,
				gerReader:           mockL2Etherman,
				aggchainProofClient: mockAggchainProofClient,
				baseFlow: &baseFlow{
					l1InfoTreeSyncer: mockL1InfoTreeSyncer,
					l2Syncer:         mockL2Syncer,
					storage:          mockStorage,
					log:              log.WithFields("flowManager", "Test_AggchainProverFlow_GetCertificateBuildParams"),
					cfg:              Config{},
				},
			}

			tc.mockFn(mockStorage, mockL2Syncer, mockAggchainProofClient, mockL1Client, mockL1InfoTreeSyncer, mockL2Etherman)

			params, err := aggchainFlow.GetCertificateBuildParams(ctx)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedParams, params)
			}

			mockStorage.AssertExpectations(t)
			mockL2Syncer.AssertExpectations(t)
			mockL1Client.AssertExpectations(t)
			mockL1InfoTreeSyncer.AssertExpectations(t)
			mockAggchainProofClient.AssertExpectations(t)
		})
	}
}

func Test_AggchainProverFlow_GetFinalizedL1InfoTreeData(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	testCases := []struct {
		name          string
		mockFn        func(*mocks.L1InfoTreeSyncer, *mocks.EthClient)
		expectedProof treeTypes.Proof
		expectedLeaf  *l1infotreesync.L1InfoTreeLeaf
		expectedRoot  *treeTypes.Root
		expectedError string
	}{
		{
			name: "error getting latest processed finalized block",
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer, mockL1Client *mocks.EthClient) {
				mockL1Client.On("HeaderByNumber", ctx, finalizedBlockBigInt).Return(nil, errors.New("some error"))
			},
			expectedError: "error getting latest processed finalized block",
		},
		{
			name: "error getting last L1 Info tree root by block num",
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer, mockL1Client *mocks.EthClient) {
				l1Header := &gethTypes.Header{Number: big.NewInt(10)}
				mockL1Client.On("HeaderByNumber", ctx, finalizedBlockBigInt).Return(l1Header, nil)
				mockL1InfoTreeSyncer.On("GetProcessedBlockUntil", ctx, l1Header.Number.Uint64()).Return(l1Header.Number.Uint64(), l1Header.Hash(), nil)
				mockL1InfoTreeSyncer.On("GetLastL1InfoTreeRootByBlockNum", ctx, l1Header.Number.Uint64()).Return(nil, errors.New("some error"))
			},
			expectedError: "error getting last L1 Info tree root by block num 10: some error",
		},
		{
			name: "error getting L1 Info tree leaf by index",
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer, mockL1Client *mocks.EthClient) {
				l1Header := &gethTypes.Header{Number: big.NewInt(10)}
				mockL1Client.On("HeaderByNumber", ctx, finalizedBlockBigInt).Return(l1Header, nil)
				mockL1InfoTreeSyncer.On("GetProcessedBlockUntil", ctx, l1Header.Number.Uint64()).Return(l1Header.Number.Uint64(), l1Header.Hash(), nil)
				mockL1InfoTreeSyncer.On("GetLastL1InfoTreeRootByBlockNum", ctx, l1Header.Number.Uint64()).Return(&treeTypes.Root{Index: 0}, nil)
				mockL1InfoTreeSyncer.On("GetInfoByIndex", ctx, uint32(0)).Return(nil, errors.New("some error"))
			},
			expectedError: "error getting L1 Info tree leaf by index 0: some error",
		},
		{
			name: "error getting L1 Info tree merkle proof from index to root",
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer, mockL1Client *mocks.EthClient) {
				l1Header := &gethTypes.Header{Number: big.NewInt(10)}
				mockL1Client.On("HeaderByNumber", ctx, finalizedBlockBigInt).Return(l1Header, nil)
				mockL1InfoTreeSyncer.On("GetProcessedBlockUntil", ctx, l1Header.Number.Uint64()).Return(l1Header.Number.Uint64(), l1Header.Hash(), nil)
				mockL1InfoTreeSyncer.On("GetLastL1InfoTreeRootByBlockNum", ctx, l1Header.Number.Uint64()).Return(&treeTypes.Root{Index: 0, Hash: common.HexToHash("0x1")}, nil)
				mockL1InfoTreeSyncer.On("GetInfoByIndex", ctx, uint32(0)).Return(&l1infotreesync.L1InfoTreeLeaf{Hash: common.HexToHash("0x2")}, nil)
				mockL1InfoTreeSyncer.On("GetL1InfoTreeMerkleProofFromIndexToRoot", ctx, uint32(0), common.HexToHash("0x1")).Return(treeTypes.Proof{}, errors.New("some error"))
			},
			expectedError: "error getting L1 Info tree merkle proof from index 0 to root",
		},
		{
			name: "success",
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer, mockL1Client *mocks.EthClient) {
				l1Header := &gethTypes.Header{Number: big.NewInt(10)}
				mockL1Client.On("HeaderByNumber", ctx, finalizedBlockBigInt).Return(l1Header, nil)
				mockL1InfoTreeSyncer.On("GetProcessedBlockUntil", ctx, l1Header.Number.Uint64()).Return(l1Header.Number.Uint64(), l1Header.Hash(), nil)
				mockL1InfoTreeSyncer.On("GetLastL1InfoTreeRootByBlockNum", ctx, l1Header.Number.Uint64()).Return(&treeTypes.Root{Index: 0, Hash: common.HexToHash("0x1")}, nil)
				mockL1InfoTreeSyncer.On("GetInfoByIndex", ctx, uint32(0)).Return(&l1infotreesync.L1InfoTreeLeaf{Hash: common.HexToHash("0x2")}, nil)
				mockL1InfoTreeSyncer.On("GetL1InfoTreeMerkleProofFromIndexToRoot", ctx, uint32(0), common.HexToHash("0x1")).Return(treeTypes.Proof{}, nil)
			},
			expectedProof: treeTypes.Proof{},
			expectedLeaf:  &l1infotreesync.L1InfoTreeLeaf{Hash: common.HexToHash("0x2")},
			expectedRoot:  &treeTypes.Root{Index: 0, Hash: common.HexToHash("0x1")},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockL1InfoTreeSyncer := mocks.NewL1InfoTreeSyncer(t)
			mockL1Client := mocks.NewEthClient(t)
			aggchainFlow := &aggchainProverFlow{
				l1Client: mockL1Client,
				baseFlow: &baseFlow{
					l1InfoTreeSyncer: mockL1InfoTreeSyncer,
					log:              log.WithFields("flowManager", "Test_AggchainProverFlow_GetFinalizedL1InfoTreeData"),
					cfg:              Config{},
				},
			}

			tc.mockFn(mockL1InfoTreeSyncer, mockL1Client)

			proof, leaf, root, err := aggchainFlow.getFinalizedL1InfoTreeData(ctx)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedProof, proof)
				require.Equal(t, tc.expectedLeaf, leaf)
				require.Equal(t, tc.expectedRoot, root)
			}

			mockL1InfoTreeSyncer.AssertExpectations(t)
			mockL1Client.AssertExpectations(t)
		})
	}
}

func Test_AggchainProverFlow_GetLatestProcessedFinalizedBlock(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	testCases := []struct {
		name          string
		mockFn        func(*mocks.L1InfoTreeSyncer, *mocks.EthClient)
		expectedBlock uint64
		expectedError string
	}{
		{
			name: "error getting latest finalized L1 block",
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer, mockL1Client *mocks.EthClient) {
				mockL1Client.On("HeaderByNumber", ctx, finalizedBlockBigInt).Return(nil, errors.New("some error"))
			},
			expectedError: "error getting latest finalized L1 block: some error",
		},
		{
			name: "error getting latest processed block from l1infotreesyncer",
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer, mockL1Client *mocks.EthClient) {
				l1Header := &gethTypes.Header{Number: big.NewInt(10)}
				mockL1Client.On("HeaderByNumber", ctx, finalizedBlockBigInt).Return(l1Header, nil)
				mockL1InfoTreeSyncer.On("GetProcessedBlockUntil", ctx, l1Header.Number.Uint64()).Return(uint64(0), common.Hash{}, errors.New("some error"))
			},
			expectedError: "error getting latest processed block from l1infotreesyncer: some error",
		},
		{
			name: "l1infotreesyncer did not process any block yet",
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer, mockL1Client *mocks.EthClient) {
				l1Header := &gethTypes.Header{Number: big.NewInt(10)}
				mockL1Client.On("HeaderByNumber", ctx, finalizedBlockBigInt).Return(l1Header, nil)
				mockL1InfoTreeSyncer.On("GetProcessedBlockUntil", ctx, l1Header.Number.Uint64()).Return(uint64(0), common.Hash{}, nil)
			},
			expectedError: "l1infotreesyncer did not process any block yet",
		},
		{
			name: "error getting latest processed finalized block",
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer, mockL1Client *mocks.EthClient) {
				l1Header := &gethTypes.Header{Number: big.NewInt(10)}
				mockL1Client.On("HeaderByNumber", ctx, finalizedBlockBigInt).Return(l1Header, nil)
				mockL1InfoTreeSyncer.On("GetProcessedBlockUntil", ctx, l1Header.Number.Uint64()).Return(uint64(9), common.Hash{}, nil)
				mockL1Client.On("HeaderByNumber", ctx, big.NewInt(9)).Return(nil, errors.New("some error"))
			},
			expectedError: "error getting latest processed finalized block: 9: some error",
		},
		{
			name: "l1infotreesyncer returned a different hash for the latest finalized block",
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer, mockL1Client *mocks.EthClient) {
				l1Header := &gethTypes.Header{Number: big.NewInt(10)}
				mockL1Client.On("HeaderByNumber", ctx, finalizedBlockBigInt).Return(l1Header, nil)
				mockL1InfoTreeSyncer.On("GetProcessedBlockUntil", ctx, l1Header.Number.Uint64()).Return(
					l1Header.Number.Uint64(), common.HexToHash("0x2"), nil)
			},
			expectedError: "l1infotreesyncer returned a different hash for the latest finalized block: 10. " +
				"Might be that syncer did not process a reorg yet.",
		},
		{
			name: "success",
			mockFn: func(mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer, mockL1Client *mocks.EthClient) {
				l1Header := &gethTypes.Header{Number: big.NewInt(10)}
				mockL1Client.On("HeaderByNumber", ctx, finalizedBlockBigInt).Return(l1Header, nil)
				mockL1InfoTreeSyncer.On("GetProcessedBlockUntil", ctx, l1Header.Number.Uint64()).Return(
					l1Header.Number.Uint64(), l1Header.Hash(), nil)
			},
			expectedBlock: 10,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockL1InfoTreeSyncer := mocks.NewL1InfoTreeSyncer(t)
			mockL1Client := mocks.NewEthClient(t)
			aggchainFlow := &aggchainProverFlow{
				l1Client: mockL1Client,
				baseFlow: &baseFlow{
					l1InfoTreeSyncer: mockL1InfoTreeSyncer,
					log:              log.WithFields("flowManager", "Test_AggchainProverFlow_GetLatestProcessedFinalizedBlock"),
					cfg:              Config{},
				},
			}

			tc.mockFn(mockL1InfoTreeSyncer, mockL1Client)

			block, err := aggchainFlow.getLatestProcessedFinalizedBlock(ctx)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedBlock, block)
			}

			mockL1InfoTreeSyncer.AssertExpectations(t)
			mockL1Client.AssertExpectations(t)
		})
	}
}

func Test_AggchainProverFlow_GetInjectedGERsProofs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	testCases := []struct {
		name           string
		mockFn         func(*mocks.ChainGERReader, *mocks.L1InfoTreeSyncer)
		expectedProofs map[common.Hash]treeTypes.Proof
		expectedError  string
	}{
		{
			name: "error getting injected GERs for range",
			mockFn: func(mockL2Etherman *mocks.ChainGERReader, mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer) {
				mockL2Etherman.On("GetInjectedGERsForRange", ctx, uint64(1), uint64(10)).Return(nil, errors.New("some error"))
			},
			expectedError: "error getting injected GERs for range 1 : 10: some error",
		},
		{
			name: "error getting L1 Info tree leaf by global exit root",
			mockFn: func(mockL2Etherman *mocks.ChainGERReader, mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer) {
				mockL2Etherman.On("GetInjectedGERsForRange", ctx, uint64(1), uint64(10)).Return([]common.Hash{common.HexToHash("0x1")}, nil)
				mockL1InfoTreeSyncer.On("GetInfoByGlobalExitRoot", common.HexToHash("0x1")).Return(nil, errors.New("some error"))
			},
			expectedError: "error getting L1 Info tree leaf by global exit root 0x0000000000000000000000000000000000000000000000000000000000000001: some error",
		},
		{
			name: "error getting L1 Info tree merkle proof from index to root",
			mockFn: func(mockL2Etherman *mocks.ChainGERReader, mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer) {
				mockL2Etherman.On("GetInjectedGERsForRange", ctx, uint64(1), uint64(10)).Return([]common.Hash{common.HexToHash("0x1")}, nil)
				mockL1InfoTreeSyncer.On("GetInfoByGlobalExitRoot", common.HexToHash("0x1")).Return(&l1infotreesync.L1InfoTreeLeaf{L1InfoTreeIndex: 0}, nil)
				mockL1InfoTreeSyncer.On("GetL1InfoTreeMerkleProofFromIndexToRoot", ctx, uint32(0), common.HexToHash("0x2")).Return(treeTypes.Proof{}, errors.New("some error"))
			},
			expectedError: "error getting L1 Info tree merkle proof from index 0 to root 0x0000000000000000000000000000000000000000000000000000000000000002: some error",
		},
		{
			name: "error injected GER l1 info tree index greater than the finalized l1 info tree root",
			mockFn: func(mockL2Etherman *mocks.ChainGERReader, mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer) {
				mockL2Etherman.On("GetInjectedGERsForRange", ctx, uint64(1), uint64(10)).Return([]common.Hash{common.HexToHash("0x1")}, nil)
				mockL1InfoTreeSyncer.On("GetInfoByGlobalExitRoot", common.HexToHash("0x1")).Return(&l1infotreesync.L1InfoTreeLeaf{L1InfoTreeIndex: 11}, nil)
			},
			expectedError: "is higher than the last finalized l1 info tree root",
		},
		{
			name: "success",
			mockFn: func(mockL2Etherman *mocks.ChainGERReader, mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer) {
				mockL2Etherman.On("GetInjectedGERsForRange", ctx, uint64(1), uint64(10)).Return([]common.Hash{common.HexToHash("0x1")}, nil)
				mockL1InfoTreeSyncer.On("GetInfoByGlobalExitRoot", common.HexToHash("0x1")).Return(&l1infotreesync.L1InfoTreeLeaf{L1InfoTreeIndex: 0}, nil)
				mockL1InfoTreeSyncer.On("GetL1InfoTreeMerkleProofFromIndexToRoot", ctx, uint32(0), common.HexToHash("0x2")).Return(treeTypes.Proof{}, nil)
			},
			expectedProofs: map[common.Hash]treeTypes.Proof{
				common.HexToHash("0x1"): {},
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockL2Etherman := mocks.NewChainGERReader(t)
			mockL1InfoTreeSyncer := mocks.NewL1InfoTreeSyncer(t)
			aggchainFlow := &aggchainProverFlow{
				gerReader: mockL2Etherman,
				baseFlow: &baseFlow{
					l1InfoTreeSyncer: mockL1InfoTreeSyncer,
					log:              log.WithFields("flowManager", "Test_AggchainProverFlow_GetInjectedGERsProofs"),
					cfg:              Config{},
				},
			}

			tc.mockFn(mockL2Etherman, mockL1InfoTreeSyncer)

			proofs, err := aggchainFlow.getInjectedGERsProofs(ctx, &treeTypes.Root{Hash: common.HexToHash("0x2"), Index: 10}, 1, 10)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedProofs, proofs)
			}

			mockL2Etherman.AssertExpectations(t)
			mockL1InfoTreeSyncer.AssertExpectations(t)
		})
	}
}

func TestGetImportedBridgeExitsForProver(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		claims        []bridgesync.Claim
		expectedExits []*agglayer.ImportedBridgeExit
		expectedError string
	}{
		{
			name: "error getting imported bridge exits",
			claims: []bridgesync.Claim{
				{
					IsMessage:          false,
					OriginNetwork:      1,
					OriginAddress:      common.HexToAddress("0x123"),
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x456"),
					Amount:             big.NewInt(100),
					Metadata:           []byte("metadata"),
					GlobalIndex:        new(big.Int).SetBytes([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}),
				},
			},
			expectedError: "aggchainProverFlow - error converting claim to imported bridge exit",
		},
		{
			name: "success",
			claims: []bridgesync.Claim{
				{
					IsMessage:          false,
					OriginNetwork:      1,
					OriginAddress:      common.HexToAddress("0x123"),
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x456"),
					Amount:             big.NewInt(100),
					Metadata:           []byte("metadata"),
					GlobalIndex:        big.NewInt(1),
				},
				{
					IsMessage:          true,
					OriginNetwork:      1,
					OriginAddress:      common.HexToAddress("0x123"),
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x456"),
					Amount:             big.NewInt(100),
					Metadata:           []byte("metadata"),
					GlobalIndex:        big.NewInt(2),
				},
			},
			expectedExits: []*agglayer.ImportedBridgeExit{
				{
					BridgeExit: &agglayer.BridgeExit{
						LeafType: agglayer.LeafTypeAsset,
						TokenInfo: &agglayer.TokenInfo{
							OriginNetwork:      1,
							OriginTokenAddress: common.HexToAddress("0x123"),
						},
						DestinationNetwork: 2,
						DestinationAddress: common.HexToAddress("0x456"),
						Amount:             big.NewInt(100),
						Metadata:           []byte("metadata"),
					},
					GlobalIndex: &agglayer.GlobalIndex{
						MainnetFlag: false,
						RollupIndex: 0,
						LeafIndex:   1,
					},
				},
				{
					BridgeExit: &agglayer.BridgeExit{
						LeafType: agglayer.LeafTypeMessage,
						TokenInfo: &agglayer.TokenInfo{
							OriginNetwork:      1,
							OriginTokenAddress: common.HexToAddress("0x123"),
						},
						DestinationNetwork: 2,
						DestinationAddress: common.HexToAddress("0x456"),
						Amount:             big.NewInt(100),
						Metadata:           []byte("metadata"),
					},
					GlobalIndex: &agglayer.GlobalIndex{
						MainnetFlag: false,
						RollupIndex: 0,
						LeafIndex:   2,
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			flow := &aggchainProverFlow{
				baseFlow: &baseFlow{
					log: log.WithFields("flowManager", "TestGetImportedBridgeExitsForProver"),
					cfg: Config{},
				},
			}

			exits, err := flow.getImportedBridgeExitsForProver(tc.claims)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedExits, exits)
			}
		})
	}
}
