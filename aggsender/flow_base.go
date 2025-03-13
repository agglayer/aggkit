package aggsender

import (
	"context"
	"fmt"
	"math/big"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/tree"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// baseFlow is a struct that holds the common logic for the different prover types
type baseFlow struct {
	l1Client         types.EthClient
	l1InfoTreeSyncer types.L1InfoTreeSyncer
	l2Syncer         types.L2BridgeSyncer
	storage          db.AggSenderStorage

	log types.Logger
	cfg Config
}

// getBridgesAndClaims returns the bridges and claims consumed from the L2 fromBlock to toBlock
func (f *baseFlow) getBridgesAndClaims(
	ctx context.Context,
	fromBlock, toBlock uint64,
) ([]bridgesync.Bridge, []bridgesync.Claim, error) {
	bridges, err := f.l2Syncer.GetBridgesPublished(ctx, fromBlock, toBlock)
	if err != nil {
		return nil, nil, fmt.Errorf("error getting bridges: %w", err)
	}

	if len(bridges) == 0 {
		f.log.Infof("no bridges consumed, no need to send a certificate from block: %d to block: %d",
			fromBlock, toBlock)
		return nil, nil, nil
	}

	claims, err := f.l2Syncer.GetClaims(ctx, fromBlock, toBlock)
	if err != nil {
		return nil, nil, fmt.Errorf("error getting claims: %w", err)
	}

	return bridges, claims, nil
}

// getCertificateBuildParamsInternal returns the parameters to build a certificate
func (f *baseFlow) getCertificateBuildParamsInternal(ctx context.Context) (*types.CertificateBuildParams, error) {
	lastL2BlockSynced, err := f.l2Syncer.GetLastProcessedBlock(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting last processed block from l2: %w", err)
	}

	lastSentCertificateInfo, err := f.storage.GetLastSentCertificate()
	if err != nil {
		return nil, err
	}

	previousToBlock, retryCount := getLastSentBlockAndRetryCount(lastSentCertificateInfo)

	if previousToBlock >= lastL2BlockSynced {
		f.log.Infof("no new blocks to send a certificate, last certificate block: %d, last L2 block: %d",
			previousToBlock, lastL2BlockSynced)
		return nil, nil
	}

	fromBlock := previousToBlock + 1
	toBlock := lastL2BlockSynced

	bridges, claims, err := f.getBridgesAndClaims(ctx, fromBlock, toBlock)
	if err != nil {
		return nil, err
	}

	buildParams := &types.CertificateBuildParams{
		FromBlock:           fromBlock,
		ToBlock:             toBlock,
		RetryCount:          retryCount,
		LastSentCertificate: lastSentCertificateInfo,
		Bridges:             bridges,
		Claims:              claims,
		CreatedAt:           uint32(time.Now().UTC().Unix()),
	}

	if buildParams.NumberOfBridges() == 0 {
		// no bridges so no need to build the certificate
		return nil, nil
	}

	buildParams, err = f.limitCertSize(buildParams)
	if err != nil {
		return nil, fmt.Errorf("error limitCertSize: %w", err)
	}

	return buildParams, nil
}

// limitCertSize limits certificate size based on the max size configuration parameter
// size is expressed in bytes
func (f *baseFlow) limitCertSize(fullCert *types.CertificateBuildParams) (*types.CertificateBuildParams, error) {
	currentCert := fullCert
	var previousCert *types.CertificateBuildParams
	var err error
	for {
		if currentCert.NumberOfBridges() == 0 {
			// We can't reduce more the certificate, so this is the minium size
			f.log.Warnf("We reach the minium size of bridge. Certificate size: %d >max size: %d",
				previousCert.EstimatedSize(), f.cfg.MaxCertSize)
			return previousCert, nil
		}

		if f.cfg.MaxCertSize == 0 || currentCert.EstimatedSize() < f.cfg.MaxCertSize {
			return currentCert, nil
		}

		// Minimum size of the certificate
		if currentCert.NumberOfBlocks() <= 1 {
			f.log.Warnf("reach the minium num blocks [%d to %d]. Certificate size: %d >max size: %d",
				currentCert.FromBlock, currentCert.ToBlock, currentCert.EstimatedSize(), f.cfg.MaxCertSize)
			return currentCert, nil
		}
		previousCert = currentCert
		currentCert, err = currentCert.Range(currentCert.FromBlock, currentCert.ToBlock-1)
		if err != nil {
			return nil, fmt.Errorf("error reducing certificate: %w", err)
		}
	}
}

func (f *baseFlow) buildCertificate(ctx context.Context,
	certParams *types.CertificateBuildParams,
	lastSentCertificateInfo *types.CertificateInfo) (*agglayertypes.Certificate, error) {
	f.log.Infof("building certificate for %s estimatedSize=%d", certParams.String(), certParams.EstimatedSize())

	if certParams.IsEmpty() {
		return nil, errNoBridgesAndClaims
	}

	bridgeExits := f.getBridgeExits(certParams.Bridges)
	importedBridgeExits, err := f.getImportedBridgeExits(ctx, certParams.Claims, certParams.L1InfoTreeRootFromWhichToProve)
	if err != nil {
		return nil, fmt.Errorf("error getting imported bridge exits: %w", err)
	}

	depositCount := certParams.MaxDepositCount()

	exitRoot, err := f.l2Syncer.GetExitRootByIndex(ctx, depositCount)
	if err != nil {
		return nil, fmt.Errorf("error getting exit root by index: %d. Error: %w", depositCount, err)
	}

	height, previousLER, err := f.getNextHeightAndPreviousLER(lastSentCertificateInfo)
	if err != nil {
		return nil, fmt.Errorf("error getting next height and previous LER: %w", err)
	}

	meta := types.NewCertificateMetadata(
		certParams.FromBlock,
		uint32(certParams.ToBlock-certParams.FromBlock),
		certParams.CreatedAt,
	)

	return &agglayertypes.Certificate{
		NetworkID:           f.l2Syncer.OriginNetwork(),
		PrevLocalExitRoot:   previousLER,
		NewLocalExitRoot:    exitRoot.Hash,
		BridgeExits:         bridgeExits,
		ImportedBridgeExits: importedBridgeExits,
		Height:              height,
		Metadata:            meta.ToHash(),
	}, nil
}

// createCertificateMetadata creates the metadata for the certificate
// it returns: newMetadata + bool if the metadata is hashed or not
func convertBridgeMetadata(metadata []byte, importedBridgeMetadataAsHash bool) ([]byte, bool) {
	var (
		metaData         []byte
		isMetadataHashed bool
	)

	if importedBridgeMetadataAsHash && len(metadata) > 0 {
		metaData = crypto.Keccak256(metadata)
		isMetadataHashed = true
	} else {
		metaData = metadata
		isMetadataHashed = false
	}
	return metaData, isMetadataHashed
}

// convertClaimToImportedBridgeExit converts a claim to an ImportedBridgeExit object
func (f *baseFlow) convertClaimToImportedBridgeExit(claim bridgesync.Claim) (*agglayertypes.ImportedBridgeExit, error) {
	leafType := agglayertypes.LeafTypeAsset
	if claim.IsMessage {
		leafType = agglayertypes.LeafTypeMessage
	}
	metaData, isMetadataIsHashed := convertBridgeMetadata(claim.Metadata, f.cfg.BridgeMetadataAsHash)

	bridgeExit := &agglayertypes.BridgeExit{
		LeafType: leafType,
		TokenInfo: &agglayertypes.TokenInfo{
			OriginNetwork:      claim.OriginNetwork,
			OriginTokenAddress: claim.OriginAddress,
		},
		DestinationNetwork: claim.DestinationNetwork,
		DestinationAddress: claim.DestinationAddress,
		Amount:             claim.Amount,
		IsMetadataHashed:   isMetadataIsHashed,
		Metadata:           metaData,
	}

	mainnetFlag, rollupIndex, leafIndex, err := bridgesync.DecodeGlobalIndex(claim.GlobalIndex)
	if err != nil {
		return nil, fmt.Errorf("error decoding global index: %w", err)
	}

	return &agglayertypes.ImportedBridgeExit{
		BridgeExit: bridgeExit,
		GlobalIndex: &agglayertypes.GlobalIndex{
			MainnetFlag: mainnetFlag,
			RollupIndex: rollupIndex,
			LeafIndex:   leafIndex,
		},
	}, nil
}

// getBridgeExits converts bridges to agglayer.BridgeExit objects
func (f *baseFlow) getBridgeExits(bridges []bridgesync.Bridge) []*agglayertypes.BridgeExit {
	bridgeExits := make([]*agglayertypes.BridgeExit, 0, len(bridges))

	for _, bridge := range bridges {
		metaData, isMetadataHashed := convertBridgeMetadata(bridge.Metadata, f.cfg.BridgeMetadataAsHash)
		bridgeExits = append(bridgeExits, &agglayertypes.BridgeExit{
			LeafType: agglayertypes.LeafType(bridge.LeafType),
			TokenInfo: &agglayertypes.TokenInfo{
				OriginNetwork:      bridge.OriginNetwork,
				OriginTokenAddress: bridge.OriginAddress,
			},
			DestinationNetwork: bridge.DestinationNetwork,
			DestinationAddress: bridge.DestinationAddress,
			Amount:             bridge.Amount,
			IsMetadataHashed:   isMetadataHashed,
			Metadata:           metaData,
		})
	}

	return bridgeExits
}

// getImportedBridgeExits converts claims to agglayertypes.ImportedBridgeExit objects and calculates necessary proofs
func (f *baseFlow) getImportedBridgeExits(
	ctx context.Context, claims []bridgesync.Claim,
	rootFromWhichToProve *treetypes.Root,
) ([]*agglayertypes.ImportedBridgeExit, error) {
	if len(claims) == 0 {
		// no claims to convert
		return []*agglayertypes.ImportedBridgeExit{}, nil
	}

	importedBridgeExits := make([]*agglayertypes.ImportedBridgeExit, 0, len(claims))

	for i, claim := range claims {
		l1Info, err := f.l1InfoTreeSyncer.GetInfoByGlobalExitRoot(claim.GlobalExitRoot)
		if err != nil {
			return nil, fmt.Errorf("error getting info by global exit root: %w", err)
		}

		f.log.Debugf("claim[%d]: destAddr: %s GER: %s Block: %d Pos: %d GlobalIndex: 0x%x",
			i, claim.DestinationAddress.String(), claim.GlobalExitRoot.String(),
			claim.BlockNum, claim.BlockPos, claim.GlobalIndex)
		ibe, err := f.convertClaimToImportedBridgeExit(claim)
		if err != nil {
			return nil, fmt.Errorf("error converting claim to imported bridge exit: %w", err)
		}

		importedBridgeExits = append(importedBridgeExits, ibe)

		gerToL1Proof, err := f.l1InfoTreeSyncer.GetL1InfoTreeMerkleProofFromIndexToRoot(
			ctx, l1Info.L1InfoTreeIndex, rootFromWhichToProve.Hash,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"error getting L1 Info tree merkle proof for leaf index: %d and root: %s. Error: %w",
				l1Info.L1InfoTreeIndex, rootFromWhichToProve.Hash, err,
			)
		}

		if ibe.GlobalIndex.MainnetFlag {
			ibe.ClaimData = &agglayertypes.ClaimFromMainnnet{
				L1Leaf: &agglayertypes.L1InfoTreeLeaf{
					L1InfoTreeIndex: l1Info.L1InfoTreeIndex,
					RollupExitRoot:  claim.RollupExitRoot,
					MainnetExitRoot: claim.MainnetExitRoot,
					Inner: &agglayertypes.L1InfoTreeLeafInner{
						GlobalExitRoot: l1Info.GlobalExitRoot,
						Timestamp:      l1Info.Timestamp,
						BlockHash:      l1Info.PreviousBlockHash,
					},
				},
				ProofLeafMER: &agglayertypes.MerkleProof{
					Root:  claim.MainnetExitRoot,
					Proof: claim.ProofLocalExitRoot,
				},
				ProofGERToL1Root: &agglayertypes.MerkleProof{
					Root:  rootFromWhichToProve.Hash,
					Proof: gerToL1Proof,
				},
			}
		} else {
			ibe.ClaimData = &agglayertypes.ClaimFromRollup{
				L1Leaf: &agglayertypes.L1InfoTreeLeaf{
					L1InfoTreeIndex: l1Info.L1InfoTreeIndex,
					RollupExitRoot:  claim.RollupExitRoot,
					MainnetExitRoot: claim.MainnetExitRoot,
					Inner: &agglayertypes.L1InfoTreeLeafInner{
						GlobalExitRoot: l1Info.GlobalExitRoot,
						Timestamp:      l1Info.Timestamp,
						BlockHash:      l1Info.PreviousBlockHash,
					},
				},
				ProofLeafLER: &agglayertypes.MerkleProof{
					Root: tree.CalculateRoot(ibe.BridgeExit.Hash(),
						claim.ProofLocalExitRoot, ibe.GlobalIndex.LeafIndex),
					Proof: claim.ProofLocalExitRoot,
				},
				ProofLERToRER: &agglayertypes.MerkleProof{
					Root:  claim.RollupExitRoot,
					Proof: claim.ProofRollupExitRoot,
				},
				ProofGERToL1Root: &agglayertypes.MerkleProof{
					Root:  rootFromWhichToProve.Hash,
					Proof: gerToL1Proof,
				},
			}
		}
	}

	return importedBridgeExits, nil
}

// getNextHeightAndPreviousLER returns the height and previous LER for the new certificate
func (f *baseFlow) getNextHeightAndPreviousLER(
	lastSentCertificateInfo *types.CertificateInfo) (uint64, common.Hash, error) {
	if lastSentCertificateInfo == nil {
		return 0, zeroLER, nil
	}
	if !lastSentCertificateInfo.Status.IsClosed() {
		return 0, zeroLER, fmt.Errorf("last certificate %s is not closed (status: %s)",
			lastSentCertificateInfo.ID(), lastSentCertificateInfo.Status.String())
	}
	if lastSentCertificateInfo.Status.IsSettled() {
		return lastSentCertificateInfo.Height + 1, lastSentCertificateInfo.NewLocalExitRoot, nil
	}

	if lastSentCertificateInfo.Status.IsInError() {
		// We can reuse last one of lastCert?
		if lastSentCertificateInfo.PreviousLocalExitRoot != nil {
			return lastSentCertificateInfo.Height, *lastSentCertificateInfo.PreviousLocalExitRoot, nil
		}
		// Is the first one, so we can set the zeroLER
		if lastSentCertificateInfo.Height == 0 {
			return 0, zeroLER, nil
		}
		// We get previous certificate that must be settled
		f.log.Debugf("last certificate %s is in error, getting previous settled certificate height:%d",
			lastSentCertificateInfo.Height-1)
		lastSettleCert, err := f.storage.GetCertificateByHeight(lastSentCertificateInfo.Height - 1)
		if err != nil {
			return 0, common.Hash{}, fmt.Errorf("error getting last settled certificate: %w", err)
		}
		if lastSettleCert == nil {
			return 0, common.Hash{}, fmt.Errorf("none settled certificate: %w", err)
		}
		if !lastSettleCert.Status.IsSettled() {
			return 0, common.Hash{}, fmt.Errorf("last settled certificate %s is not settled (status: %s)",
				lastSettleCert.ID(), lastSettleCert.Status.String())
		}

		return lastSentCertificateInfo.Height, lastSettleCert.NewLocalExitRoot, nil
	}
	return 0, zeroLER, fmt.Errorf("last certificate %s has an unknown status: %s",
		lastSentCertificateInfo.ID(), lastSentCertificateInfo.Status.String())
}

// getLatestProcessedFinalizedBlock returns the latest processed l1 info tree root
// based on the latest finalized l1 block
func (f *baseFlow) getLatestFinalizedL1InfoRoot(ctx context.Context) (*treeTypes.Root, error) {
	lastFinalizedProcessedBlock, err := f.getLatestProcessedFinalizedBlock(ctx)
	if err != nil {
		return nil,
			fmt.Errorf("error getting latest processed finalized block: %w", err)
	}

	root, err := f.l1InfoTreeSyncer.GetLastL1InfoTreeRootByBlockNum(ctx, lastFinalizedProcessedBlock)
	if err != nil {
		return nil,
			fmt.Errorf("error getting last L1 Info tree root by block num %d: %w",
				lastFinalizedProcessedBlock, err)
	}

	return root, nil
}

// getLatestProcessedFinalizedBlock returns the latest processed finalized block from the l1infotreesyncer
func (f *baseFlow) getLatestProcessedFinalizedBlock(ctx context.Context) (uint64, error) {
	lastFinalizedL1Block, err := f.l1Client.HeaderByNumber(ctx, finalizedBlockBigInt)
	if err != nil {
		return 0, fmt.Errorf("error getting latest finalized L1 block: %w", err)
	}

	lastProcessedBlockNum, lastProcessedBlockHash, err := f.l1InfoTreeSyncer.GetProcessedBlockUntil(ctx,
		lastFinalizedL1Block.Number.Uint64())
	if err != nil {
		return 0, fmt.Errorf("error getting latest processed block from l1infotreesyncer: %w", err)
	}

	if lastProcessedBlockNum == 0 {
		return 0, fmt.Errorf("l1infotreesyncer did not process any block yet")
	}

	if lastFinalizedL1Block.Number.Uint64() > lastProcessedBlockNum {
		// syncer has a lower block than the finalized block, so we need to get that block from the l1 node
		lastFinalizedL1Block, err = f.l1Client.HeaderByNumber(ctx, new(big.Int).SetUint64(lastProcessedBlockNum))
		if err != nil {
			return 0, fmt.Errorf("error getting latest processed finalized block: %d: %w",
				lastProcessedBlockNum, err)
		}
	}

	if (lastProcessedBlockHash == common.Hash{}) || (lastProcessedBlockHash == lastFinalizedL1Block.Hash()) {
		// if the hash is empty it means that this is an old block that was processed before this
		// feature was added, so we will consider it finalized
		return lastFinalizedL1Block.Number.Uint64(), nil
	}

	return 0, fmt.Errorf("l1infotreesyncer returned a different hash for "+
		"the latest finalized block: %d. Might be that syncer did not process a reorg yet. "+
		"Expected hash: %s, got: %s", lastProcessedBlockNum,
		lastFinalizedL1Block.Hash().String(), lastProcessedBlockHash.String())
}

// getLastSentBlockAndRetryCount returns the last sent block of the last sent certificate
// if there is no previosly sent certificate, it returns 0 and 0
func getLastSentBlockAndRetryCount(lastSentCertificateInfo *types.CertificateInfo) (uint64, int) {
	if lastSentCertificateInfo == nil {
		return 0, 0
	}

	retryCount := 0
	lastSentBlock := lastSentCertificateInfo.ToBlock

	if lastSentCertificateInfo.Status == agglayertypes.InError {
		// if the last certificate was in error, we need to resend it
		// from the block before the error
		if lastSentCertificateInfo.FromBlock > 0 {
			lastSentBlock = lastSentCertificateInfo.FromBlock - 1
		}

		retryCount = lastSentCertificateInfo.RetryCount + 1
	}

	return lastSentBlock, retryCount
}
