package flows

import (
	"context"
	"encoding/binary"
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/ethereum/go-ethereum/common"
)

const (
	// qbftCustomChainDataLen is the size of abi.encode(bytes4, bytes32, uint64, bytes32)
	qbftCustomChainDataLen = 4 * 32
	wordLen                = 32

	// word positions inside the ABI encoded custom chain data
	selectorWord      = 0
	blockHashWord     = 1
	blockNumberWord   = 2
	consensusHashWord = 3
)

var _ types.AggsenderVerifierFlow = (*AggchainQBFTVerifierFlow)(nil)

// AggchainQBFTVerifierFlow verifies certificates of AggchainQBFT networks.
// It does not re-prove consensus nor recompute aggchain_params: it decodes the custom chain data produced by the
// prover and checks it against the validator's own L2 RPC and the L1 rollup contract.
type AggchainQBFTVerifierFlow struct {
	*AggchainProverBuilderFlow

	chainQuerier types.QBFTChainQuerier
}

// NewAggchainQBFTVerifierFlow creates a new AggchainQBFTVerifierFlow
func NewAggchainQBFTVerifierFlow(
	builderFlow *AggchainProverBuilderFlow,
	chainQuerier types.QBFTChainQuerier,
) *AggchainQBFTVerifierFlow {
	return &AggchainQBFTVerifierFlow{
		AggchainProverBuilderFlow: builderFlow,
		chainQuerier:              chainQuerier,
	}
}

// QBFTCustomChainData is the decoded custom_chain_data of an AggchainQBFT certificate:
// abi.encode(bytes4 aggchainVKeySelector, bytes32 newBlockHash, uint64 newBlockNumber, bytes32 newConsensusHash)
type QBFTCustomChainData struct {
	AggchainVKeySelector [4]byte
	NewBlockHash         common.Hash
	NewBlockNumber       uint64
	NewConsensusHash     common.Hash
}

// DecodeQBFTCustomChainData decodes the 128 bytes custom_chain_data of an AggchainQBFT certificate
func DecodeQBFTCustomChainData(data []byte) (*QBFTCustomChainData, error) {
	if len(data) != qbftCustomChainDataLen {
		return nil, fmt.Errorf("invalid QBFT custom chain data length: expected %d, got %d",
			qbftCustomChainDataLen, len(data))
	}
	word := func(i int) []byte { return data[i*wordLen : (i+1)*wordLen] }
	isZero := func(b []byte) bool {
		for _, v := range b {
			if v != 0 {
				return false
			}
		}
		return true
	}

	// bytes4 is left-aligned, uint64 is right-aligned in their 32 bytes word
	if !isZero(word(selectorWord)[4:]) {
		return nil, fmt.Errorf("invalid QBFT custom chain data: selector word has non zero padding")
	}
	if !isZero(word(blockNumberWord)[:wordLen-8]) {
		return nil, fmt.Errorf("invalid QBFT custom chain data: block number word has non zero padding")
	}

	res := &QBFTCustomChainData{
		NewBlockHash:     common.BytesToHash(word(blockHashWord)),
		NewBlockNumber:   binary.BigEndian.Uint64(word(blockNumberWord)[wordLen-8:]),
		NewConsensusHash: common.BytesToHash(word(consensusHashWord)),
	}
	copy(res.AggchainVKeySelector[:], word(selectorWord)[:4])
	return res, nil
}

// VerifyCertificate verifies the new certificate.
// This function is used in the validator to verify the certificate
func (a *AggchainQBFTVerifierFlow) VerifyCertificate(
	ctx context.Context,
	cert *agglayertypes.Certificate,
	lastBlockInCert uint64,
	lastSettledBlock uint64) error {
	if cert.AggchainData == nil {
		return fmt.Errorf("aggchainQBFTFlow: certificate AggchainData is nil")
	}
	switch cert.AggchainData.(type) {
	case *agglayertypes.AggchainDataProof, *agglayertypes.AggchainDataMultisigWithProof:
	default:
		return fmt.Errorf("aggchainQBFTFlow: certificate AggchainData is of unknown type %T", cert.AggchainData)
	}

	data, err := DecodeQBFTCustomChainData(cert.CustomChainData)
	if err != nil {
		return fmt.Errorf("aggchainQBFTFlow: %w", err)
	}

	aggchainType, err := a.chainQuerier.GetAggchainTypeFromSelector(data.AggchainVKeySelector)
	if err != nil {
		return fmt.Errorf("aggchainQBFTFlow: %w", err)
	}
	if aggchainType != types.AggchainTypeQBFT {
		return fmt.Errorf("aggchainQBFTFlow: aggchain vkey selector %x has aggchain type %s, expected %s",
			data.AggchainVKeySelector, aggchainType, types.AggchainTypeQBFT)
	}

	if data.NewBlockNumber != lastBlockInCert {
		return fmt.Errorf("aggchainQBFTFlow: custom chain data block number %d does not match the certificate "+
			"last block %d", data.NewBlockNumber, lastBlockInCert)
	}
	if data.NewBlockNumber <= lastSettledBlock {
		return fmt.Errorf("aggchainQBFTFlow: custom chain data block number %d is not greater than the last "+
			"settled block %d", data.NewBlockNumber, lastSettledBlock)
	}

	blockHash, err := a.chainQuerier.GetBlockHash(ctx, data.NewBlockNumber)
	if err != nil {
		return fmt.Errorf("aggchainQBFTFlow: %w", err)
	}
	if blockHash != data.NewBlockHash {
		return fmt.Errorf("aggchainQBFTFlow: block hash mismatch at block %d: L2 node has %s, custom chain data has %s",
			data.NewBlockNumber, blockHash, data.NewBlockHash)
	}

	bridgeRoot, err := a.chainQuerier.GetBridgeRootAtBlock(ctx, data.NewBlockNumber)
	if err != nil {
		return fmt.Errorf("aggchainQBFTFlow: %w", err)
	}
	if bridgeRoot != cert.NewLocalExitRoot {
		return fmt.Errorf("aggchainQBFTFlow: new local exit root mismatch at block %d: L2 bridge root is %s, "+
			"certificate has %s", data.NewBlockNumber, bridgeRoot, cert.NewLocalExitRoot)
	}

	a.log.Infof("aggchainQBFTFlow: custom chain data verified for block %d (hash %s, LER %s)",
		data.NewBlockNumber, blockHash, bridgeRoot)
	return nil
}
