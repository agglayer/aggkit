package flows

import (
	"context"
	"errors"
	"fmt"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	aggkitdb "github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/tree"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"golang.org/x/crypto/sha3"
)

var (
	errNoBridgesAndClaims = errors.New("no bridges and claims to build certificate")
	errNoNewBlocks        = errors.New("no new blocks to send a certificate")

	zeroLER = common.HexToHash("0x27ae5ba08d7291c96c8cbddcc148bf48a6d68c7974b94356f53754ef6171d757")
)

// baseFlow is a struct that holds the common logic for the different prover types
type baseFlow struct {
	l2Syncer              types.L2BridgeSyncer
	storage               db.AggSenderStorage
	l1InfoTreeDataQuerier types.L1InfoTreeDataQuerier

	log types.Logger

	maxCertSize          uint
	bridgeMetaDataAsHash bool
	startL2Block         uint64
}

// getBridgesAndClaims returns the bridges and claims consumed from the L2 fromBlock to toBlock
func (f *baseFlow) getBridgesAndClaims(
	ctx context.Context,
	fromBlock, toBlock uint64,
	allowEmptyCert bool,
) ([]bridgesync.Bridge, []bridgesync.Claim, error) {
	bridges, err := f.l2Syncer.GetBridgesPublished(ctx, fromBlock, toBlock)
	if err != nil {
		return nil, nil, fmt.Errorf("error getting bridges: %w", err)
	}

	if len(bridges) == 0 && !allowEmptyCert {
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
func (f *baseFlow) getCertificateBuildParamsInternal(
	ctx context.Context, allowEmptyCert bool) (*types.CertificateBuildParams, error) {
	lastL2BlockSynced, err := f.l2Syncer.GetLastProcessedBlock(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting last processed block from l2: %w", err)
	}

	lastSentCertificateInfo, err := f.storage.GetLastSentCertificate()
	if err != nil {
		return nil, err
	}

	previousToBlock, retryCount := f.getLastSentBlockAndRetryCount(lastSentCertificateInfo)

	if previousToBlock >= lastL2BlockSynced {
		f.log.Warnf("no new blocks to send a certificate, last certificate block: %d, last L2 block: %d",
			previousToBlock, lastL2BlockSynced)
		return nil, errNoNewBlocks
	}

	fromBlock := previousToBlock + 1
	toBlock := lastL2BlockSynced

	bridges, claims, err := f.getBridgesAndClaims(ctx, fromBlock, toBlock, allowEmptyCert)
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

	if !allowEmptyCert && buildParams.NumberOfBridges() == 0 {
		// no bridges so no need to build the certificate
		return nil, nil
	}

	buildParams, err = f.limitCertSize(buildParams, allowEmptyCert)
	if err != nil {
		return nil, fmt.Errorf("error limitCertSize: %w", err)
	}

	return buildParams, nil
}

// verifyBuildParams verifies the build parameters
func (f *baseFlow) verifyBuildParams(fullCert *types.CertificateBuildParams) error {
	// this will be a good place to add more verification checks in the future
	return f.verifyClaimGERs(fullCert.Claims)
}

// limitCertSize limits certificate size based on the max size configuration parameter
// size is expressed in bytes
func (f *baseFlow) limitCertSize(
	fullCert *types.CertificateBuildParams, allowEmptyCert bool) (*types.CertificateBuildParams, error) {
	currentCert := fullCert
	var err error

	for {
		if currentCert.NumberOfBridges() == 0 && !allowEmptyCert {
			f.log.Warnf("Minimum certificate size reached. Estimated size: %d > max size: %d",
				currentCert.EstimatedSize(), f.maxCertSize)
			return currentCert, nil
		}

		if f.maxCertSize == 0 || currentCert.EstimatedSize() <= f.maxCertSize {
			return currentCert, nil
		}

		if currentCert.NumberOfBlocks() <= 1 {
			f.log.Warnf("Minimum number of blocks reached [%d to %d]. Estimated size: %d > max size: %d",
				currentCert.FromBlock, currentCert.ToBlock, currentCert.EstimatedSize(), f.maxCertSize)
			return currentCert, nil
		}

		currentCert, err = currentCert.Range(currentCert.FromBlock, currentCert.ToBlock-1)
		if err != nil {
			return nil, fmt.Errorf("error reducing certificate: %w", err)
		}
	}
}

func (f *baseFlow) buildCertificate(ctx context.Context,
	certParams *types.CertificateBuildParams,
	lastSentCertificateInfo *types.CertificateInfo,
	allowEmptyCert bool) (*agglayertypes.Certificate, error) {
	f.log.Infof("building certificate for %s estimatedSize=%d", certParams.String(), certParams.EstimatedSize())

	if !allowEmptyCert && certParams.IsEmpty() {
		return nil, errNoBridgesAndClaims
	}

	bridgeExits := f.getBridgeExits(certParams.Bridges)
	importedBridgeExits, err := f.getImportedBridgeExits(ctx, certParams.Claims, certParams.L1InfoTreeRootFromWhichToProve)
	if err != nil {
		return nil, fmt.Errorf("error getting imported bridge exits: %w", err)
	}

	height, previousLER, err := f.getNextHeightAndPreviousLER(lastSentCertificateInfo)
	if err != nil {
		return nil, fmt.Errorf("error getting next height and previous LER: %w", err)
	}

	newLER, err := f.getNewLocalExitRoot(ctx, certParams, previousLER)
	if err != nil {
		return nil, fmt.Errorf("error getting new local exit root: %w", err)
	}

	meta := types.NewCertificateMetadata(
		certParams.FromBlock,
		uint32(certParams.ToBlock-certParams.FromBlock),
		certParams.CreatedAt,
	)

	return &agglayertypes.Certificate{
		NetworkID:           f.l2Syncer.OriginNetwork(),
		PrevLocalExitRoot:   previousLER,
		NewLocalExitRoot:    newLER,
		BridgeExits:         bridgeExits,
		ImportedBridgeExits: importedBridgeExits,
		Height:              height,
		Metadata:            meta.ToHash(),
	}, nil
}

// getNewLocalExitRoot gets the new local exit root for the certificate
func (f *baseFlow) getNewLocalExitRoot(
	ctx context.Context,
	certParams *types.CertificateBuildParams,
	previousLER common.Hash) (common.Hash, error) {
	if certParams.NumberOfBridges() == 0 {
		// if there is no bridge exits we return the previous LER
		// since there was no change in the local exit root
		return previousLER, nil
	}

	exitRoot, err := f.l2Syncer.GetExitRootByIndexAndBlockNumber(ctx, certParams.MaxDepositCount(), certParams.ToBlock)
	if err != nil {
		if errors.Is(err, aggkitdb.ErrNotFound) {
			// if the exit root is not found, we return the previous LER
			// this means that there are no new exits in the range
			return previousLER, nil
		}

		return common.Hash{}, fmt.Errorf("error getting exit root by index: %d. Error: %w", 0, err)
	}

	return exitRoot.Hash, nil
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
	metaData, isMetadataIsHashed := convertBridgeMetadata(claim.Metadata, f.bridgeMetaDataAsHash)

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
		metaData, isMetadataHashed := convertBridgeMetadata(bridge.Metadata, f.bridgeMetaDataAsHash)
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
	rootFromWhichToProve common.Hash,
) ([]*agglayertypes.ImportedBridgeExit, error) {
	if len(claims) == 0 {
		// no claims to convert
		return []*agglayertypes.ImportedBridgeExit{}, nil
	}

	importedBridgeExits := make([]*agglayertypes.ImportedBridgeExit, 0, len(claims))

	for i, claim := range claims {
		f.log.Debugf("claim[%d]: destAddr: %s GER: %s Block: %d Pos: %d GlobalIndex: 0x%x",
			i, claim.DestinationAddress.String(), claim.GlobalExitRoot.String(),
			claim.BlockNum, claim.BlockPos, claim.GlobalIndex)
		ibe, err := f.convertClaimToImportedBridgeExit(claim)
		if err != nil {
			return nil, fmt.Errorf("error converting claim to imported bridge exit: %w", err)
		}

		importedBridgeExits = append(importedBridgeExits, ibe)

		l1Info, gerToL1Proof, err := f.l1InfoTreeDataQuerier.GetProofForGER(ctx,
			claim.GlobalExitRoot, rootFromWhichToProve)
		if err != nil {
			return nil, fmt.Errorf(
				"error getting L1 Info tree merkle proof for leaf index: %d and root: %s. Error: %w",
				l1Info.L1InfoTreeIndex, rootFromWhichToProve, err,
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
					Root:  rootFromWhichToProve,
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
					Root:  rootFromWhichToProve,
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

// verifyClaimGERs verifies the correctnes GERs of the claims
func (f *baseFlow) verifyClaimGERs(claims []bridgesync.Claim) error {
	for _, claim := range claims {
		ger := calculateGER(claim.MainnetExitRoot, claim.RollupExitRoot)
		if ger != claim.GlobalExitRoot {
			return fmt.Errorf("claim[GlobalIndex: %s, BlockNum: %d]: GER mismatch. Expected: %s, got: %s",
				claim.GlobalIndex.String(), claim.BlockNum, claim.GlobalExitRoot.String(), ger.String())
		}
	}

	return nil
}

// getLastSentBlockAndRetryCount returns the last sent block of the last sent certificate
// if there is no previosly sent certificate, it returns startL2Block and 0
func (f *baseFlow) getLastSentBlockAndRetryCount(lastSentCertificateInfo *types.CertificateInfo) (uint64, int) {
	if lastSentCertificateInfo == nil {
		// this is the first certificate so we start from what we have set in start L2 block
		return f.startL2Block, 0
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

// calculateGER calculates the GER hash based on the mainnet exit root and the rollup exit root
func calculateGER(mainnetExitRoot, rollupExitRoot common.Hash) common.Hash {
	var gerBytes [common.HashLength]byte
	hasher := sha3.NewLegacyKeccak256()
	hasher.Write(mainnetExitRoot.Bytes())
	hasher.Write(rollupExitRoot.Bytes())
	copy(gerBytes[:], hasher.Sum(nil))

	return gerBytes
}
