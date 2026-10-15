package flows

import (
	"context"
	"errors"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/stretchr/testify/require"
)

// Test vector: prover-besu-qbft/vectors/aggchain_params/a-genesis-to-plain-no-change.json (aggchain_data)
// abi.encode(bytes4 selector, bytes32 newBlockHash, uint64 newBlockNumber, bytes32 newConsensusHash)
const (
	qbftVectorAggchainData = "0x" +
		"0001000200000000000000000000000000000000000000000000000000000000" +
		"d72d82bde323540130e61c67ec0e3afdae5dd9aad3c1fa2585e43cbee2bd7093" +
		"0000000000000000000000000000000000000000000000000000000000000002" +
		"01b5ab9a778e5587aea0973952753ad523cc250877eb1116fe50f74dfc420464"
	qbftVectorBlockHash     = "0xd72d82bde323540130e61c67ec0e3afdae5dd9aad3c1fa2585e43cbee2bd7093"
	qbftVectorConsensusHash = "0x01b5ab9a778e5587aea0973952753ad523cc250877eb1116fe50f74dfc420464"
)

func TestDecodeQBFTCustomChainData(t *testing.T) {
	t.Parallel()

	t.Run("decodes the shared vector", func(t *testing.T) {
		t.Parallel()
		data, err := DecodeQBFTCustomChainData(hexutil.MustDecode(qbftVectorAggchainData))
		require.NoError(t, err)
		require.Equal(t, [4]byte{0x00, 0x01, 0x00, 0x02}, data.AggchainVKeySelector)
		require.Equal(t, common.HexToHash(qbftVectorBlockHash), data.NewBlockHash)
		require.Equal(t, uint64(2), data.NewBlockNumber)
		require.Equal(t, common.HexToHash(qbftVectorConsensusHash), data.NewConsensusHash)
	})

	t.Run("wrong length", func(t *testing.T) {
		t.Parallel()
		_, err := DecodeQBFTCustomChainData(hexutil.MustDecode(qbftVectorAggchainData)[:127])
		require.ErrorContains(t, err, "invalid QBFT custom chain data length: expected 128, got 127")
		_, err = DecodeQBFTCustomChainData(nil)
		require.ErrorContains(t, err, "invalid QBFT custom chain data length")
	})

	t.Run("non zero selector padding", func(t *testing.T) {
		t.Parallel()
		raw := hexutil.MustDecode(qbftVectorAggchainData)
		raw[31] = 1
		_, err := DecodeQBFTCustomChainData(raw)
		require.ErrorContains(t, err, "selector word has non zero padding")
	})

	t.Run("non zero block number padding", func(t *testing.T) {
		t.Parallel()
		raw := hexutil.MustDecode(qbftVectorAggchainData)
		raw[64] = 1
		_, err := DecodeQBFTCustomChainData(raw)
		require.ErrorContains(t, err, "block number word has non zero padding")
	})
}

func TestAggchainQBFTVerifierFlow_VerifyCertificate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	blockHash := common.HexToHash(qbftVectorBlockHash)
	ler := common.HexToHash("0xaaaa")
	selector := [4]byte{0x00, 0x01, 0x00, 0x02}
	errRPC := errors.New("rpc error")

	newCert := func() *agglayertypes.Certificate {
		return &agglayertypes.Certificate{
			AggchainData:     &agglayertypes.AggchainDataProof{AggchainParams: common.HexToHash("0x1")},
			CustomChainData:  hexutil.MustDecode(qbftVectorAggchainData),
			NewLocalExitRoot: ler,
		}
	}

	testCases := []struct {
		name             string
		cert             func() *agglayertypes.Certificate
		lastBlockInCert  uint64
		lastSettledBlock uint64
		mockFn           func(*mocks.QBFTChainQuerier)
		expectedError    string
	}{
		{
			name:            "happy path",
			cert:            newCert,
			lastBlockInCert: 2,
			mockFn: func(m *mocks.QBFTChainQuerier) {
				m.EXPECT().GetAggchainTypeFromSelector(selector).Return(types.AggchainTypeQBFT, nil).Once()
				m.EXPECT().GetBlockHash(ctx, uint64(2)).Return(blockHash, nil).Once()
				m.EXPECT().GetBridgeRootAtBlock(ctx, uint64(2)).Return(ler, nil).Once()
			},
		},
		{
			name: "happy path with multisig and proof aggchain data",
			cert: func() *agglayertypes.Certificate {
				c := newCert()
				c.AggchainData = &agglayertypes.AggchainDataMultisigWithProof{
					AggchainProof: &agglayertypes.AggchainDataProof{},
				}
				return c
			},
			lastBlockInCert: 2,
			mockFn: func(m *mocks.QBFTChainQuerier) {
				m.EXPECT().GetAggchainTypeFromSelector(selector).Return(types.AggchainTypeQBFT, nil).Once()
				m.EXPECT().GetBlockHash(ctx, uint64(2)).Return(blockHash, nil).Once()
				m.EXPECT().GetBridgeRootAtBlock(ctx, uint64(2)).Return(ler, nil).Once()
			},
		},
		{
			name: "AggchainData is nil",
			cert: func() *agglayertypes.Certificate {
				c := newCert()
				c.AggchainData = nil
				return c
			},
			expectedError: "certificate AggchainData is nil",
		},
		{
			name: "AggchainData of unknown type",
			cert: func() *agglayertypes.Certificate {
				c := newCert()
				c.AggchainData = &agglayertypes.AggchainDataMultisig{}
				return c
			},
			expectedError: "certificate AggchainData is of unknown type",
		},
		{
			name: "custom chain data can not be decoded",
			cert: func() *agglayertypes.Certificate {
				c := newCert()
				c.CustomChainData = []byte{1, 2, 3}
				return c
			},
			expectedError: "invalid QBFT custom chain data length",
		},
		{
			name:            "error getting aggchain type from selector",
			cert:            newCert,
			lastBlockInCert: 2,
			mockFn: func(m *mocks.QBFTChainQuerier) {
				m.EXPECT().GetAggchainTypeFromSelector(selector).Return("", errRPC).Once()
			},
			expectedError: "rpc error",
		},
		{
			name:            "selector is not a QBFT one",
			cert:            newCert,
			lastBlockInCert: 2,
			mockFn: func(m *mocks.QBFTChainQuerier) {
				m.EXPECT().GetAggchainTypeFromSelector(selector).Return(types.AggchainTypeFEP, nil).Once()
			},
			expectedError: "has aggchain type FEP, expected QBFT",
		},
		{
			name:            "block number differs from the certificate last block",
			cert:            newCert,
			lastBlockInCert: 3,
			mockFn: func(m *mocks.QBFTChainQuerier) {
				m.EXPECT().GetAggchainTypeFromSelector(selector).Return(types.AggchainTypeQBFT, nil).Once()
			},
			expectedError: "block number 2 does not match the certificate last block 3",
		},
		{
			name:             "block number not greater than last settled block",
			cert:             newCert,
			lastBlockInCert:  2,
			lastSettledBlock: 2,
			mockFn: func(m *mocks.QBFTChainQuerier) {
				m.EXPECT().GetAggchainTypeFromSelector(selector).Return(types.AggchainTypeQBFT, nil).Once()
			},
			expectedError: "is not greater than the last settled block 2",
		},
		{
			name:            "error getting block hash",
			cert:            newCert,
			lastBlockInCert: 2,
			mockFn: func(m *mocks.QBFTChainQuerier) {
				m.EXPECT().GetAggchainTypeFromSelector(selector).Return(types.AggchainTypeQBFT, nil).Once()
				m.EXPECT().GetBlockHash(ctx, uint64(2)).Return(common.Hash{}, errRPC).Once()
			},
			expectedError: "rpc error",
		},
		{
			name:            "block hash mismatch",
			cert:            newCert,
			lastBlockInCert: 2,
			mockFn: func(m *mocks.QBFTChainQuerier) {
				m.EXPECT().GetAggchainTypeFromSelector(selector).Return(types.AggchainTypeQBFT, nil).Once()
				m.EXPECT().GetBlockHash(ctx, uint64(2)).Return(common.HexToHash("0xdead"), nil).Once()
			},
			expectedError: "block hash mismatch at block 2",
		},
		{
			name:            "error getting bridge root",
			cert:            newCert,
			lastBlockInCert: 2,
			mockFn: func(m *mocks.QBFTChainQuerier) {
				m.EXPECT().GetAggchainTypeFromSelector(selector).Return(types.AggchainTypeQBFT, nil).Once()
				m.EXPECT().GetBlockHash(ctx, uint64(2)).Return(blockHash, nil).Once()
				m.EXPECT().GetBridgeRootAtBlock(ctx, uint64(2)).Return(common.Hash{}, errRPC).Once()
			},
			expectedError: "rpc error",
		},
		{
			name:            "new local exit root mismatch",
			cert:            newCert,
			lastBlockInCert: 2,
			mockFn: func(m *mocks.QBFTChainQuerier) {
				m.EXPECT().GetAggchainTypeFromSelector(selector).Return(types.AggchainTypeQBFT, nil).Once()
				m.EXPECT().GetBlockHash(ctx, uint64(2)).Return(blockHash, nil).Once()
				m.EXPECT().GetBridgeRootAtBlock(ctx, uint64(2)).Return(common.HexToHash("0xbbbb"), nil).Once()
			},
			expectedError: "new local exit root mismatch at block 2",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			chainQuerier := mocks.NewQBFTChainQuerier(t)
			if tc.mockFn != nil {
				tc.mockFn(chainQuerier)
			}
			flow := NewAggchainQBFTVerifierFlow(
				&AggchainProverBuilderFlow{log: log.WithFields("test", "qbftVerifier")}, chainQuerier)

			err := flow.VerifyCertificate(ctx, tc.cert(), tc.lastBlockInCert, tc.lastSettledBlock)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
				return
			}
			require.NoError(t, err)
		})
	}
}
