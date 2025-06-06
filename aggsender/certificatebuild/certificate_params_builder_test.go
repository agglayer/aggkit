package certificatebuild

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var ler1 = common.HexToHash("0x123")

func Test_limitCertSize(t *testing.T) {
	tests := []struct {
		name           string
		maxCertSize    uint
		fullCert       *types.CertificateBuildParams
		allowEmptyCert bool
		expectedCert   *types.CertificateBuildParams
		expectedError  string
	}{
		{
			name:        "certificate size within limit",
			maxCertSize: 1000,
			fullCert: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   10,
				Bridges:   []bridgesync.Bridge{{}, {}},
			},
			allowEmptyCert: false,
			expectedCert: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   10,
				Bridges:   []bridgesync.Bridge{{}, {}},
			},
		},
		{
			name:        "certificate size exceeds limit - reducing with some bridges",
			maxCertSize: 500,
			fullCert: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   10,
				Bridges:   []bridgesync.Bridge{{BlockNum: 9}, {BlockNum: 10}, {BlockNum: 10}, {BlockNum: 10}, {BlockNum: 10}},
			},
			allowEmptyCert: false,
			expectedCert: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   9,
				Bridges:   []bridgesync.Bridge{{BlockNum: 9}},
				Claims:    []bridgesync.Claim{},
			},
		},
		{
			name:        "certificate size exceeds limit - reducing to no bridges",
			maxCertSize: 500,
			fullCert: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   10,
				Bridges:   []bridgesync.Bridge{{BlockNum: 10}, {BlockNum: 10}, {BlockNum: 10}, {BlockNum: 10}, {BlockNum: 10}},
			},
			allowEmptyCert: false,
			expectedError:  "error on reducing the certificate size. No bridge exits found",
		},
		{
			name:        "certificate size exceeds limit with minimum blocks",
			maxCertSize: 500,
			fullCert: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   2,
				Bridges:   []bridgesync.Bridge{{}},
			},
			allowEmptyCert: false,
			expectedCert: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   2,
				Bridges:   []bridgesync.Bridge{{}},
			},
		},
		{
			name:        "empty certificate allowed",
			maxCertSize: 500,
			fullCert: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   10,
				Bridges:   []bridgesync.Bridge{},
			},
			allowEmptyCert: true,
			expectedCert: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   10,
				Bridges:   []bridgesync.Bridge{},
			},
		},
		{
			name:        "empty certificate not allowed",
			maxCertSize: 500,
			fullCert: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   10,
				Bridges:   []bridgesync.Bridge{},
			},
			allowEmptyCert: false,
			expectedError:  "error on reducing the certificate size. No bridge exits found in range from: 1, to: 10 and empty certificate is not allowed",
		},
		{
			name:        "maxCertSize is 0 with bridges and claims",
			maxCertSize: 0,
			fullCert: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   10,
				Bridges:   []bridgesync.Bridge{{}, {}},
				Claims:    []bridgesync.Claim{{}, {}},
			},
			allowEmptyCert: false,
			expectedCert: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   10,
				Bridges:   []bridgesync.Bridge{{}, {}},
				Claims:    []bridgesync.Claim{{}, {}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := NewCertificateBuilder(
				nil, // logger
				nil, // storage
				nil, // l1InfoTreeDataQuerier
				nil, // l2BridgeQuerier
				NewCertificateBuilderConfig(tt.maxCertSize, 0),
			)

			result, err := builder.limitCertSize(tt.fullCert, tt.allowEmptyCert)

			if tt.expectedError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedCert, result)
			}
		})
	}
}

func TestGetLastSentBlockAndRetryCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		lastSentCertificate *types.CertificateHeader
		expectedBlock       uint64
		startL2Block        uint64
		expectedRetryCount  int
	}{
		{
			name:                "No last sent certificate, start block is 0",
			lastSentCertificate: nil,
			expectedBlock:       0,
			startL2Block:        0,
			expectedRetryCount:  0,
		},
		{
			name:                "No last sent certificate, start block is 1000",
			lastSentCertificate: nil,
			expectedBlock:       1000,
			startL2Block:        1000,
			expectedRetryCount:  0,
		},
		{
			name: "Last sent certificate with no error",
			lastSentCertificate: &types.CertificateHeader{
				ToBlock: 10,
				Status:  agglayertypes.Settled,
			},
			expectedBlock:      10,
			expectedRetryCount: 0,
		},
		{
			name: "Last sent certificate with error and non-zero FromBlock",
			lastSentCertificate: &types.CertificateHeader{
				FromBlock:  5,
				ToBlock:    10,
				Status:     agglayertypes.InError,
				RetryCount: 1,
			},
			expectedBlock:      4,
			expectedRetryCount: 2,
		},
		{
			name: "Last sent certificate with error and zero FromBlock",
			lastSentCertificate: &types.CertificateHeader{
				FromBlock:  0,
				ToBlock:    10,
				Status:     agglayertypes.InError,
				RetryCount: 1,
			},
			expectedBlock:      10,
			expectedRetryCount: 2,
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			builder := NewCertificateBuilder(
				nil, // logger
				nil, // storage
				nil, // l1InfoTreeDataQuerier
				nil, // l2BridgeQuerier
				NewCertificateBuilderConfig(0, tt.startL2Block),
			)

			block, retryCount := builder.getLastSentBlockAndRetryCount(tt.lastSentCertificate)

			require.Equal(t, tt.expectedBlock, block)
			require.Equal(t, tt.expectedRetryCount, retryCount)
		})
	}
}

func Test_getNewLocalExitRoot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		certParams      *types.CertificateBuildParams
		mockFn          func(mockL2BridgeQuerier *mocks.BridgeQuerier)
		previousLER     common.Hash
		expectedLER     common.Hash
		expectedError   string
		numberOfBridges int
	}{
		{
			name: "no bridges, return previous LER",
			certParams: &types.CertificateBuildParams{
				Bridges: []bridgesync.Bridge{},
			},
			previousLER: common.HexToHash("0x123"),
			expectedLER: common.HexToHash("0x123"),
		},
		{
			name: "exit root found, return new exit root",
			certParams: &types.CertificateBuildParams{
				Bridges: []bridgesync.Bridge{{}, {}},
				ToBlock: 10,
			},
			previousLER: common.HexToHash("0x123"),
			expectedLER: common.HexToHash("0x456"),
			mockFn: func(mockL2BridgeQuerier *mocks.BridgeQuerier) {
				mockL2BridgeQuerier.EXPECT().GetExitRootByIndex(mock.Anything, mock.Anything).
					Return(common.HexToHash("0x456"), nil)
			},
		},
		{
			name: "exit root not found, return previous LER",
			certParams: &types.CertificateBuildParams{
				Bridges: []bridgesync.Bridge{{}, {}},
				ToBlock: 10,
			},
			previousLER:   common.HexToHash("0x123"),
			expectedLER:   common.HexToHash("0x123"),
			expectedError: "not found",
			mockFn: func(mockL2BridgeQuerier *mocks.BridgeQuerier) {
				mockL2BridgeQuerier.EXPECT().GetExitRootByIndex(mock.Anything, mock.Anything).
					Return(common.Hash{}, db.ErrNotFound)
			},
		},
		{
			name: "error fetching exit root, return error",
			certParams: &types.CertificateBuildParams{
				Bridges: []bridgesync.Bridge{{}, {}},
				ToBlock: 10,
			},
			previousLER:   common.HexToHash("0x123"),
			expectedLER:   common.Hash{},
			expectedError: "error getting exit root by index: 0. Error: unexpected error",
			mockFn: func(mockL2BridgeQuerier *mocks.BridgeQuerier) {
				mockL2BridgeQuerier.EXPECT().GetExitRootByIndex(mock.Anything, mock.Anything).
					Return(common.Hash{}, errors.New("unexpected error"))
			},
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockL2BridgeQuerier := mocks.NewBridgeQuerier(t)
			if tt.mockFn != nil {
				tt.mockFn(mockL2BridgeQuerier)
			}

			builder := &CertificateBuilder{
				l2BridgeQuerier: mockL2BridgeQuerier,
			}

			result, err := builder.getNewLocalExitRoot(context.Background(), tt.certParams, tt.previousLER)

			if tt.expectedError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedLER, result)
			}
		})
	}
}

func TestGetNextHeightAndPreviousLER(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                       string
		lastSentCertificate        *types.CertificateHeader
		lastSettleCertificateCall  bool
		lastSettledCertificate     *types.CertificateHeader
		lastSettleCertificateError error
		expectedHeight             uint64
		expectedPreviousLER        common.Hash
		expectedError              bool
	}{
		{
			name: "Normal case",
			lastSentCertificate: &types.CertificateHeader{
				Height:           10,
				NewLocalExitRoot: common.HexToHash("0x123"),
				Status:           agglayertypes.Settled,
			},
			expectedHeight:      11,
			expectedPreviousLER: common.HexToHash("0x123"),
		},
		{
			name:                "First certificate",
			lastSentCertificate: nil,
			expectedHeight:      0,
			expectedPreviousLER: ZeroLER,
		},
		{
			name: "First certificate error, with prevLER",
			lastSentCertificate: &types.CertificateHeader{
				Height:                0,
				NewLocalExitRoot:      common.HexToHash("0x123"),
				Status:                agglayertypes.InError,
				PreviousLocalExitRoot: &ler1,
			},
			expectedHeight:      0,
			expectedPreviousLER: ler1,
		},
		{
			name: "First certificate error, no prevLER",
			lastSentCertificate: &types.CertificateHeader{
				Height:           0,
				NewLocalExitRoot: common.HexToHash("0x123"),
				Status:           agglayertypes.InError,
			},
			expectedHeight:      0,
			expectedPreviousLER: ZeroLER,
		},
		{
			name: "n certificate error, prevLER",
			lastSentCertificate: &types.CertificateHeader{
				Height:                10,
				NewLocalExitRoot:      common.HexToHash("0x123"),
				PreviousLocalExitRoot: &ler1,
				Status:                agglayertypes.InError,
			},
			expectedHeight:      10,
			expectedPreviousLER: ler1,
		},
		{
			name: "last cert not closed, error",
			lastSentCertificate: &types.CertificateHeader{
				Height:                10,
				NewLocalExitRoot:      common.HexToHash("0x123"),
				PreviousLocalExitRoot: &ler1,
				Status:                agglayertypes.Pending,
			},
			expectedHeight:      10,
			expectedPreviousLER: ler1,
			expectedError:       true,
		},
		{
			name: "Previous certificate in error, no prevLER",
			lastSentCertificate: &types.CertificateHeader{
				Height:           10,
				NewLocalExitRoot: common.HexToHash("0x123"),
				Status:           agglayertypes.InError,
			},
			lastSettledCertificate: &types.CertificateHeader{
				Height:           9,
				NewLocalExitRoot: common.HexToHash("0x3456"),
				Status:           agglayertypes.Settled,
			},
			expectedHeight:      10,
			expectedPreviousLER: common.HexToHash("0x3456"),
		},
		{
			name: "Previous certificate in error, no prevLER. Error getting previous cert",
			lastSentCertificate: &types.CertificateHeader{
				Height:           10,
				NewLocalExitRoot: common.HexToHash("0x123"),
				Status:           agglayertypes.InError,
			},
			lastSettledCertificate:     nil,
			lastSettleCertificateError: errors.New("error getting last settle certificate"),
			expectedError:              true,
		},
		{
			name: "Previous certificate in error, no prevLER. prev cert not available on storage",
			lastSentCertificate: &types.CertificateHeader{
				Height:           10,
				NewLocalExitRoot: common.HexToHash("0x123"),
				Status:           agglayertypes.InError,
			},
			lastSettleCertificateCall:  true,
			lastSettledCertificate:     nil,
			lastSettleCertificateError: nil,
			expectedError:              true,
		},
		{
			name: "Previous certificate in error, no prevLER. prev cert not available on storage",
			lastSentCertificate: &types.CertificateHeader{
				Height:           10,
				NewLocalExitRoot: common.HexToHash("0x123"),
				Status:           agglayertypes.InError,
			},
			lastSettledCertificate: &types.CertificateHeader{
				Height:           9,
				NewLocalExitRoot: common.HexToHash("0x3456"),
				Status:           agglayertypes.InError,
			},
			lastSettleCertificateError: nil,
			expectedError:              true,
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			storageMock := mocks.NewAggSenderStorage(t)

			builder := &CertificateBuilder{log: log.WithFields("aggsender-test", "getNextHeightAndPreviousLER"), storage: storageMock}
			if tt.lastSettleCertificateCall || tt.lastSettledCertificate != nil || tt.lastSettleCertificateError != nil {
				storageMock.EXPECT().GetCertificateHeaderByHeight(mock.Anything).Return(tt.lastSettledCertificate, tt.lastSettleCertificateError).Once()
			}

			height, previousLER, err := builder.getNextHeightAndPreviousLER(tt.lastSentCertificate)
			if tt.expectedError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedHeight, height)
				require.Equal(t, tt.expectedPreviousLER, previousLER)
			}
		})
	}
}

func TestBuildCertificate(t *testing.T) {
	mockL2BridgeQuerier := mocks.NewBridgeQuerier(t)
	mockL1InfoTreeQuerier := mocks.NewL1InfoTreeDataQuerier(t)
	mockProof := generateTestProof(t)

	tests := []struct {
		name                string
		bridges             []bridgesync.Bridge
		claims              []bridgesync.Claim
		lastSentCertificate types.CertificateHeader
		fromBlock           uint64
		toBlock             uint64
		mockFn              func()
		expectedCert        *agglayertypes.Certificate
		expectedError       bool
	}{
		{
			name: "Valid certificate with bridges and claims",
			bridges: []bridgesync.Bridge{
				{
					LeafType:           agglayertypes.LeafTypeAsset.Uint8(),
					OriginNetwork:      1,
					OriginAddress:      common.HexToAddress("0x123"),
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x456"),
					Amount:             big.NewInt(100),
					Metadata:           []byte("metadata"),
					DepositCount:       1,
				},
			},
			claims: []bridgesync.Claim{
				{
					IsMessage:           false,
					OriginNetwork:       1,
					OriginAddress:       common.HexToAddress("0x1234"),
					DestinationNetwork:  2,
					DestinationAddress:  common.HexToAddress("0x4567"),
					Amount:              big.NewInt(111),
					Metadata:            []byte("metadata1"),
					GlobalIndex:         big.NewInt(1),
					GlobalExitRoot:      common.HexToHash("0x7891"),
					RollupExitRoot:      common.HexToHash("0xaaab"),
					MainnetExitRoot:     common.HexToHash("0xbbba"),
					ProofLocalExitRoot:  mockProof,
					ProofRollupExitRoot: mockProof,
				},
			},
			lastSentCertificate: types.CertificateHeader{
				NewLocalExitRoot: common.HexToHash("0x123"),
				Height:           1,
				Status:           agglayertypes.Settled,
			},
			fromBlock: 0,
			toBlock:   10,
			expectedCert: &agglayertypes.Certificate{
				NetworkID:         1,
				PrevLocalExitRoot: common.HexToHash("0x123"),
				NewLocalExitRoot:  common.HexToHash("0x789"),
				Metadata:          types.NewCertificateMetadata(0, 10, 0, types.CertificateTypePP.ToInt()).ToHash(),
				BridgeExits: []*agglayertypes.BridgeExit{
					{
						LeafType: agglayertypes.LeafTypeAsset,
						TokenInfo: &agglayertypes.TokenInfo{
							OriginNetwork:      1,
							OriginTokenAddress: common.HexToAddress("0x123"),
						},
						DestinationNetwork: 2,
						DestinationAddress: common.HexToAddress("0x456"),
						Amount:             big.NewInt(100),
						Metadata:           crypto.Keccak256([]byte("metadata")),
					},
				},
				ImportedBridgeExits: []*agglayertypes.ImportedBridgeExit{
					{
						BridgeExit: &agglayertypes.BridgeExit{
							LeafType: agglayertypes.LeafTypeAsset,
							TokenInfo: &agglayertypes.TokenInfo{
								OriginNetwork:      1,
								OriginTokenAddress: common.HexToAddress("0x1234"),
							},
							DestinationNetwork: 2,
							DestinationAddress: common.HexToAddress("0x4567"),
							Amount:             big.NewInt(111),
							Metadata:           crypto.Keccak256([]byte("metadata1")),
						},
						GlobalIndex: &agglayertypes.GlobalIndex{
							MainnetFlag: false,
							RollupIndex: 0,
							LeafIndex:   1,
						},
						ClaimData: &agglayertypes.ClaimFromRollup{
							L1Leaf: &agglayertypes.L1InfoTreeLeaf{
								L1InfoTreeIndex: 1,
								RollupExitRoot:  common.HexToHash("0xaaab"),
								MainnetExitRoot: common.HexToHash("0xbbba"),
								Inner: &agglayertypes.L1InfoTreeLeafInner{
									GlobalExitRoot: common.HexToHash("0x7891"),
									Timestamp:      123456789,
									BlockHash:      common.HexToHash("0xabc"),
								},
							},
							ProofLeafLER: &agglayertypes.MerkleProof{
								Root:  common.HexToHash("0xc52019815b51acf67a715cae6794a20083d63fd9af45783b7adf69123dae92c8"),
								Proof: mockProof,
							},
							ProofLERToRER: &agglayertypes.MerkleProof{
								Root:  common.HexToHash("0xaaab"),
								Proof: mockProof,
							},
							ProofGERToL1Root: &agglayertypes.MerkleProof{
								Root:  common.HexToHash("0x7891"),
								Proof: mockProof,
							},
						},
					},
				},
				Height: 2,
			},
			mockFn: func() {
				mockL2BridgeQuerier.EXPECT().OriginNetwork().Return(uint32(1))
				mockL2BridgeQuerier.EXPECT().GetExitRootByIndex(mock.Anything, mock.Anything).Return(common.HexToHash("0x789"), nil)
				mockL1InfoTreeQuerier.EXPECT().GetProofForGER(mock.Anything, mock.Anything, mock.Anything).Return(&l1infotreesync.L1InfoTreeLeaf{
					L1InfoTreeIndex:   1,
					Timestamp:         123456789,
					PreviousBlockHash: common.HexToHash("0xabc"),
					GlobalExitRoot:    common.HexToHash("0x7891"),
				}, mockProof, nil)
			},
			expectedError: false,
		},
		{
			name:    "No bridges or claims",
			bridges: []bridgesync.Bridge{},
			claims:  []bridgesync.Claim{},
			lastSentCertificate: types.CertificateHeader{
				NewLocalExitRoot: common.HexToHash("0x123"),
				Height:           1,
			},
			expectedCert:  nil,
			expectedError: true,
		},
		{
			name: "Error getting imported bridge exits",
			bridges: []bridgesync.Bridge{
				{
					LeafType:           agglayertypes.LeafTypeAsset.Uint8(),
					OriginNetwork:      1,
					OriginAddress:      common.HexToAddress("0x123"),
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x456"),
					Amount:             big.NewInt(100),
					Metadata:           []byte("metadata"),
					DepositCount:       1,
				},
			},
			claims: []bridgesync.Claim{
				{
					IsMessage:          false,
					OriginNetwork:      1,
					OriginAddress:      common.HexToAddress("0x1234"),
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x4567"),
					Amount:             big.NewInt(111),
					Metadata:           []byte("metadata1"),
					GlobalIndex:        new(big.Int).SetBytes([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}),
					GlobalExitRoot:     common.HexToHash("0x7891"),
					RollupExitRoot:     common.HexToHash("0xaaab"),
					MainnetExitRoot:    common.HexToHash("0xbbba"),
					ProofLocalExitRoot: mockProof,
				},
			},
			lastSentCertificate: types.CertificateHeader{
				NewLocalExitRoot: common.HexToHash("0x123"),
				Height:           1,
			},
			mockFn: func() {
				mockL1InfoTreeQuerier.EXPECT().GetProofForGER(mock.Anything, mock.Anything, mock.Anything).Return(&l1infotreesync.L1InfoTreeLeaf{
					L1InfoTreeIndex:   1,
					Timestamp:         123456789,
					PreviousBlockHash: common.HexToHash("0xabc"),
					GlobalExitRoot:    common.HexToHash("0x7891"),
				}, mockProof, nil)
			},
			expectedCert:  nil,
			expectedError: true,
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			mockL1InfoTreeQuerier.ExpectedCalls = nil
			mockL2BridgeQuerier.ExpectedCalls = nil

			if tt.mockFn != nil {
				tt.mockFn()
			}

			builder := NewCertificateBuilder(
				log.WithFields("aggsender-test", "buildCertificate"),
				nil, // storage
				mockL1InfoTreeQuerier,
				mockL2BridgeQuerier,
				NewCertificateBuilderConfigDefault(),
			)

			certParam := &types.CertificateBuildParams{
				ToBlock:                        tt.toBlock,
				Bridges:                        tt.bridges,
				Claims:                         tt.claims,
				CertificateType:                types.CertificateTypePP,
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x7891"),
			}
			cert, err := builder.BuildCertificate(context.Background(), certParam, &tt.lastSentCertificate, false)

			if tt.expectedError {
				require.Error(t, err)
				require.Nil(t, cert)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedCert, cert)
			}
		})
	}
}

func generateTestProof(t *testing.T) treetypes.Proof {
	t.Helper()

	proof := treetypes.Proof{}

	for i := 0; i < int(treetypes.DefaultHeight) && i < 10; i++ {
		proof[i] = common.HexToHash(fmt.Sprintf("0x%d", i))
	}

	return proof
}
