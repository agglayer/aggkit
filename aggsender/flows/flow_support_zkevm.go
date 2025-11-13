package flows

import (
	"context"
	"fmt"
	"math/big"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/config"
	"github.com/agglayer/aggkit/aggsender/converters"
	aggsendertypes "github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

var claimEventSignature = crypto.Keccak256Hash([]byte("ClaimEvent(uint256,uint32,address,address,uint256)"))

type zkEVMSupportStatus struct {
	cfg                  config.SupportLegacyZKEVMConfig
	l2Client             aggkittypes.BaseEthereumClienter
	l1BridgeSyncer       aggsendertypes.L1BridgeSyncer
	lowerBlockTested     uint64
	etrogActivationBlock uint64
}

// GetProof(ctx context.Context, depositCount uint32, localExitRoot common.Hash) (tree.Proof, error)

func (f *baseFlow) AddZKEVMSupport(cfg config.SupportLegacyZKEVMConfig,
	l2Client aggkittypes.BaseEthereumClienter,
	l1BridgeSyncer aggsendertypes.L1BridgeSyncer) {
	f.zkEVMStatus = zkEVMSupportStatus{
		cfg:                  cfg,
		l2Client:             l2Client,
		l1BridgeSyncer:       l1BridgeSyncer,
		etrogActivationBlock: 0,
	}
}
func (f *baseFlow) getImportedBridgeExitsZKEVMSupport(
	ctx context.Context, claims []bridgesync.Claim,
	rootFromWhichToProve common.Hash,
) ([]*agglayertypes.ImportedBridgeExit, error) {
	postEtrogBlockNumber, err := f.GetEtrogActivationBlock(ctx, claims)
	if err != nil {
		return nil, fmt.Errorf("error getting etrog activation block: %w", err)
	}
	// split claims into pre-etrog and post-etrog
	preEtrogClaims, regularClaims := f.splitClaims(claims, postEtrogBlockNumber)
	f.log.Infof("ZKEVM Support: etrogActivationBlock=%d preEtrogClaims=%d regularClaims=%d",
		postEtrogBlockNumber, len(preEtrogClaims), len(regularClaims))
	// only pre-etrog claims
	preEtrogClaims = f.convertGlobalIndexPreEtrog(preEtrogClaims)
	// Just checking if it's synced L1bridge
	block, err := f.zkEVMStatus.l1BridgeSyncer.GetBlockByLER(ctx, preEtrogClaims[0].MainnetExitRoot)
	if err != nil {
		return nil, fmt.Errorf("checking if synced l1bridge: error getting block by LER: %w", err)
	}
	if block == 0 {
		return nil, fmt.Errorf("checking if synced l1bridge: block for LER %s is 0, so L1BridgeSyncer is not synced",
			preEtrogClaims[0].MainnetExitRoot)
	}
	f.log.Infof("ZKEVM Support: L1BridgeSyncer is synced for LER %s at block %d",
		preEtrogClaims[0].MainnetExitRoot, block)
	preEtrogImportedBridgeExits, err := f.getPreEtrogImportedBridgeExits(ctx, preEtrogClaims,
		rootFromWhichToProve, regularClaims[0])
	if err != nil {
		return nil, fmt.Errorf("error getting pre-etrog imported bridge exits: %w", err)
	}

	importedBridgeExits, err := f.getImportedBridgeExits(ctx, regularClaims, rootFromWhichToProve)
	if err != nil {
		return nil, fmt.Errorf("error getting regular imported bridge exits: %w", err)
	}
	// combine both slices
	importedBridgeExits = append(preEtrogImportedBridgeExits, importedBridgeExits...)
	return importedBridgeExits, nil
}

func (f *baseFlow) convertGlobalIndexPreEtrog(claims []bridgesync.Claim) []bridgesync.Claim {
	result := make([]bridgesync.Claim, len(claims))
	for i, claim := range claims {
		newClaim := claim
		newClaim.GlobalIndex = bridgesync.GenerateGlobalIndex(true, 0, uint32(claim.GlobalIndex.Uint64()))
		result[i] = newClaim
	}
	return result
}

func (f *baseFlow) getPreEtrogImportedBridgeExits(
	ctx context.Context,
	claims []bridgesync.Claim,
	rootFromWhichToProve common.Hash,
	imperson bridgesync.Claim,
) ([]*agglayertypes.ImportedBridgeExit, error) {
	importedBridgeExits := make([]*agglayertypes.ImportedBridgeExit, 0, len(claims))
	for _, claim := range claims {
		bridgeExit, err := f.convertToPreEtrogImportedBridgeExit(ctx, claim, rootFromWhichToProve, imperson)
		if err != nil {
			return nil, fmt.Errorf("error converting claim to imported bridge exit: %w", err)
		}
		importedBridgeExits = append(importedBridgeExits, bridgeExit)
	}
	return importedBridgeExits, nil
}

func (f *baseFlow) convertToPreEtrogImportedBridgeExit(
	ctx context.Context,
	claim bridgesync.Claim,
	rootFromWhichToProve common.Hash,
	imperson bridgesync.Claim,
) (*agglayertypes.ImportedBridgeExit, error) {
	ibe, err := converters.ConvertToImportedBridgeExitWithoutClaimData(claim)
	if err != nil {
		return nil, fmt.Errorf("error converting claim to imported bridge exit without claim data: %w", err)
	}

	l1Info, gerToL1Proof, err := f.l1InfoTreeDataQuerier.GetProofForGER(ctx,
		imperson.GlobalExitRoot, rootFromWhichToProve)
	if err != nil {
		return nil, fmt.Errorf(
			"error getting L1 Info tree merkle proof for GER: %s and root: %s. Error: %w",
			imperson.GlobalExitRoot, rootFromWhichToProve, err,
		)
	}
	proofMER, err := f.zkEVMStatus.l1BridgeSyncer.GetProof(ctx, ibe.GlobalIndex.LeafIndex, imperson.MainnetExitRoot)
	if err != nil {
		return nil, fmt.Errorf("error getting merkle proof for mainnet exit root: %w", err)
	}
	// zkEVM preEtrog only could do claims from L1
	ibe.ClaimData = &agglayertypes.ClaimFromMainnet{
		L1Leaf: &agglayertypes.L1InfoTreeLeaf{
			L1InfoTreeIndex: l1Info.L1InfoTreeIndex,
			RollupExitRoot:  imperson.RollupExitRoot,
			MainnetExitRoot: imperson.MainnetExitRoot,
			Inner: &agglayertypes.L1InfoTreeLeafInner{
				GlobalExitRoot: l1Info.GlobalExitRoot,
				Timestamp:      l1Info.Timestamp,
				BlockHash:      l1Info.PreviousBlockHash,
			},
		},
		ProofLeafMER: &agglayertypes.MerkleProof{
			Root:  imperson.MainnetExitRoot,
			Proof: proofMER,
		},
		ProofGERToL1Root: &agglayertypes.MerkleProof{
			Root:  rootFromWhichToProve,
			Proof: gerToL1Proof,
		},
	}

	return ibe, nil
}

func (f *baseFlow) GetEtrogActivationBlock(ctx context.Context, claims []bridgesync.Claim) (uint64, error) {
	if f.zkEVMStatus.etrogActivationBlock != 0 {
		// We already known which block is it
		return f.zkEVMStatus.etrogActivationBlock, nil
	}
	if len(claims) == 0 {
		return 0, fmt.Errorf("cannot deduce etrog activation block without claims")
	}
	fromBlock := max(claims[0].BlockNum, f.zkEVMStatus.lowerBlockTested+1)
	toBlock := claims[len(claims)-1].BlockNum
	result, err := f.GetEtrogActivationBlockFromBlockRange(ctx, fromBlock, toBlock)
	if err != nil {
		return 0, fmt.Errorf("error getting etrog activation block from block range: %w", err)
	}
	// Update the lower block tested to avoid re-checking the same blocks
	f.zkEVMStatus.lowerBlockTested = toBlock
	return result, nil
}

func (f *baseFlow) GetEtrogActivationBlockFromBlockRange(ctx context.Context,
	fromBlock, toBlock uint64) (uint64, error) {
	var logs []types.Log
	var err error
	maxErigonBlockRange := f.zkEVMStatus.cfg.RPCFilterChunkSize
	log.Infof("Getting etrog activation block from block range [%d : %d] chunk: %d",
		fromBlock, toBlock, maxErigonBlockRange)
	from := fromBlock
	to := min(fromBlock+maxErigonBlockRange, toBlock)
	for from != toBlock {
		filterQuery := ethereum.FilterQuery{
			Addresses: []common.Address{f.zkEVMStatus.cfg.L2BridgeAddr},
			FromBlock: big.NewInt(int64(from)),
			ToBlock:   big.NewInt(int64(to)),
			Topics:    [][]common.Hash{{claimEventSignature}},
		}
		log.Debugf("Find first post-etrog claim in subrange block %d to block %d", from, to)
		logs, err = f.zkEVMStatus.l2Client.FilterLogs(ctx, filterQuery)
		if err != nil {
			return 0, fmt.Errorf("error filtering logs to find etrog activation block: %w", err)
		}
		if len(logs) > 0 {
			firstPostEtrogBlockNumber := logs[0].BlockNumber
			log.Infof("Filtering logs from block %d to block %d for etrog activation "+
				"block logs=%d firstPostEtrogBlockNumber=%d", from, to, len(logs), firstPostEtrogBlockNumber)
			f.zkEVMStatus.etrogActivationBlock = firstPostEtrogBlockNumber
			return f.zkEVMStatus.etrogActivationBlock, nil
		}
		from = min(to+1, toBlock)
		to = min(from+maxErigonBlockRange, toBlock)
	}
	// Not found
	return 0, fmt.Errorf("etrog activation block not found in range [%d : %d]", fromBlock, toBlock)
}

func (f *baseFlow) splitClaims(claims []bridgesync.Claim, etrogActivationBlock uint64,
) (preEtrogClaims []bridgesync.Claim, regularClaims []bridgesync.Claim) {
	for _, claim := range claims {
		if claim.BlockNum < etrogActivationBlock {
			preEtrogClaims = append(preEtrogClaims, claim)
		} else {
			regularClaims = append(regularClaims, claim)
		}
	}
	return preEtrogClaims, regularClaims
}
