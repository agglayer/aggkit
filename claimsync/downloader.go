package claimsync

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/polygonzkevmbridge"
	"github.com/agglayer/aggkit/bridgesync"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	dbtypes "github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/sync"
	treetypes "github.com/agglayer/aggkit/tree/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	gethvm "github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
)

var (
	claimEventSignature         = crypto.Keccak256Hash([]byte("ClaimEvent(uint256,uint32,address,address,uint256)"))
	claimEventSignaturePreEtrog = crypto.Keccak256Hash([]byte("ClaimEvent(uint32,uint32,address,address,uint256)"))
	detailedClaimEventSignature = crypto.Keccak256Hash([]byte(
		"DetailedClaimEvent(bytes32[32],bytes32[32]," +
			"uint256,bytes32,bytes32,uint8,uint32," +
			"address,uint32,address,uint256,bytes)",
	))
	unsetClaimEventSignature = crypto.Keccak256Hash([]byte("UpdatedUnsetGlobalIndexHashChain(bytes32,bytes32)"))
	setClaimEventSignature   = crypto.Keccak256Hash([]byte("SetClaim(bytes32)"))
)

// claimQuerier is used by event handlers to check the DetailedClaimEvent boundary.
type ClaimQuerier interface {
	GetBoundaryBlockForClaimType(tx dbtypes.Querier, claimType bridgesync.ClaimType) (uint64, error)
}

// buildAppender creates the LogAppenderMap for claim events from the bridge contract.
func buildAppender(
	ctx context.Context,
	ethClient aggkittypes.EthClienter,
	querier ClaimQuerier,
	bridgeAddr common.Address,
	agglayerBridgeContract *agglayerbridge.Agglayerbridge,
	agglayerBridgeL2Contract *agglayerbridgel2.Agglayerbridgel2,
	isSovereign bool,
	log aggkitcommon.Logger,
) (sync.LogAppenderMap, error) {
	legacyBridge, err := polygonzkevmbridge.NewPolygonzkevmbridge(bridgeAddr, ethClient)
	if err != nil {
		return nil, fmt.Errorf("claimsync: failed to create PolygonZkEVMBridge binding: %w", err)
	}

	appender := make(sync.LogAppenderMap)
	appender[claimEventSignaturePreEtrog] = buildClaimEventHandlerPreEtrog(legacyBridge, log)
	appender[claimEventSignature] = buildClaimEventHandler(ctx, agglayerBridgeContract, querier, log)

	if isSovereign {
		appender[detailedClaimEventSignature] = buildDetailedClaimEventHandler(agglayerBridgeL2Contract)
		appender[unsetClaimEventSignature] = buildUnsetClaimEventHandler(agglayerBridgeL2Contract)
		appender[setClaimEventSignature] = buildSetClaimEventHandler(agglayerBridgeL2Contract)
	}

	return appender, nil
}

// detectSovereignChain returns true if bridgeAddr is a sovereign chain bridge (AgglayerBridgeL2).
// It also returns the AgglayerBridgeL2 binding regardless (always created).
func detectSovereignChain(
	ctx context.Context,
	bridgeAddr common.Address,
	backend bind.ContractBackend,
) (bool, *agglayerbridgel2.Agglayerbridgel2, error) {
	contract, err := agglayerbridgel2.NewAgglayerbridgel2(bridgeAddr, backend)
	if err != nil {
		return false, nil, fmt.Errorf("claimsync: failed to create AgglayerBridgeL2 binding: %w", err)
	}

	callOpts := &bind.CallOpts{Pending: false, Context: ctx}
	if _, err := contract.BridgeManager(callOpts); err == nil {
		return true, contract, nil
	} else if !strings.Contains(err.Error(), gethvm.ErrExecutionReverted.Error()) {
		return false, nil, fmt.Errorf("claimsync: unexpected error querying AgglayerBridgeL2.BridgeManager: %w", err)
	}

	return false, contract, nil
}

// buildClaimEventHandler creates a handler for the ClaimEvent log.
func buildClaimEventHandler(
	ctx context.Context,
	contract *agglayerbridge.Agglayerbridge,
	querier ClaimQuerier,
	log aggkitcommon.Logger,
) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		// Skip if DetailedClaimEvent indexing has already started at this block
		boundaryBlock, err := querier.GetBoundaryBlockForClaimType(nil, bridgesync.DetailedClaimEvent)
		if err != nil && !errors.Is(err, db.ErrNotFound) {
			return fmt.Errorf("claimsync: failed checking DetailedClaimEvent boundary: %w", err)
		}
		if err == nil && l.BlockNumber >= boundaryBlock {
			log.Debugf("claimsync: skipping ClaimEvent at block %d; DetailedClaimEvent started at %d",
				l.BlockNumber, boundaryBlock)
			return nil
		}

		// Skip if a DetailedClaimEvent for the same tx is already in the block's events
		for _, raw := range b.Events {
			if e, ok := raw.(bridgesync.Event); ok && e.Claim != nil &&
				e.Claim.Type == bridgesync.DetailedClaimEvent && e.Claim.TxHash == l.TxHash {
				log.Debugf("claimsync: skipping ClaimEvent at block %d tx %s; DetailedClaimEvent already present",
					l.BlockNumber, l.TxHash.Hex())
				return nil
			}
		}

		claimEvent, err := contract.ParseClaimEvent(l)
		if err != nil {
			return fmt.Errorf("claimsync: error parsing ClaimEvent log: %w", err)
		}

		b.Events = append(b.Events, bridgesync.Event{Claim: &bridgesync.Claim{
			BlockNum:           b.Num,
			BlockPos:           uint64(l.Index),
			BlockTimestamp:     b.Timestamp,
			TxHash:             l.TxHash,
			GlobalIndex:        claimEvent.GlobalIndex,
			OriginNetwork:      claimEvent.OriginNetwork,
			OriginAddress:      claimEvent.OriginAddress,
			DestinationAddress: claimEvent.DestinationAddress,
			Amount:             claimEvent.Amount,
			Type:               bridgesync.ClaimEvent,
		}})
		return nil
	}
}

// buildDetailedClaimEventHandler creates a handler for the DetailedClaimEvent log (sovereign chains).
func buildDetailedClaimEventHandler(
	contract *agglayerbridgel2.Agglayerbridgel2,
) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		claimEvent, err := contract.ParseDetailedClaimEvent(l)
		if err != nil {
			return fmt.Errorf("claimsync: error parsing DetailedClaimEvent log: %w", err)
		}

		claim := &bridgesync.Claim{
			BlockNum:            b.Num,
			BlockPos:            uint64(l.Index),
			BlockTimestamp:      b.Timestamp,
			TxHash:              l.TxHash,
			GlobalIndex:         claimEvent.GlobalIndex,
			OriginNetwork:       claimEvent.OriginNetwork,
			OriginAddress:       claimEvent.OriginTokenAddress,
			DestinationNetwork:  claimEvent.DestinationNetwork,
			DestinationAddress:  claimEvent.DestinationAddress,
			Amount:              claimEvent.Amount,
			Metadata:            claimEvent.Metadata,
			MainnetExitRoot:     claimEvent.MainnetExitRoot,
			RollupExitRoot:      claimEvent.RollupExitRoot,
			ProofLocalExitRoot:  treetypes.NewProof(claimEvent.SmtProofLocalExitRoot),
			ProofRollupExitRoot: treetypes.NewProof(claimEvent.SmtProofRollupExitRoot),
			GlobalExitRoot:      crypto.Keccak256Hash(claimEvent.MainnetExitRoot[:], claimEvent.RollupExitRoot[:]),
			IsMessage:           claimEvent.LeafType == uint8(bridgesynctypes.LeafTypeMessage),
			Type:                bridgesync.DetailedClaimEvent,
		}

		// Remove any ClaimEvent for the same tx (DetailedClaimEvent takes precedence)
		newEvents := make([]interface{}, 0, len(b.Events))
		for _, raw := range b.Events {
			if e, ok := raw.(bridgesync.Event); ok && e.Claim != nil &&
				e.Claim.Type == bridgesync.ClaimEvent && e.Claim.TxHash == l.TxHash {
				continue
			}
			newEvents = append(newEvents, raw)
		}
		b.Events = newEvents
		b.Events = append(b.Events, bridgesync.Event{Claim: claim})
		return nil
	}
}

// buildClaimEventHandlerPreEtrog creates a handler for the pre-Etrog ClaimEvent log.
func buildClaimEventHandlerPreEtrog(
	contract *polygonzkevmbridge.Polygonzkevmbridge,
	log aggkitcommon.Logger,
) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		claimEvent, err := contract.ParseClaimEvent(l)
		if err != nil {
			return fmt.Errorf("claimsync: error parsing pre-Etrog ClaimEvent log: %w", err)
		}

		log.Debugf("claimsync: parsed pre-Etrog ClaimEvent: index %d block %d", claimEvent.Index, b.Num)
		b.Events = append(b.Events, bridgesync.Event{Claim: &bridgesync.Claim{
			BlockNum:           b.Num,
			BlockPos:           uint64(l.Index),
			BlockTimestamp:     b.Timestamp,
			TxHash:             l.TxHash,
			GlobalIndex:        new(big.Int).SetUint64(uint64(claimEvent.Index)),
			OriginNetwork:      claimEvent.OriginNetwork,
			OriginAddress:      claimEvent.OriginAddress,
			DestinationAddress: claimEvent.DestinationAddress,
			Amount:             claimEvent.Amount,
		}})
		return nil
	}
}

// buildUnsetClaimEventHandler creates a handler for the UpdatedUnsetGlobalIndexHashChain log.
func buildUnsetClaimEventHandler(
	contract *agglayerbridgel2.Agglayerbridgel2,
) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		event, err := contract.ParseUpdatedUnsetGlobalIndexHashChain(l)
		if err != nil {
			return fmt.Errorf("claimsync: error parsing UpdatedUnsetGlobalIndexHashChain log: %w", err)
		}

		b.Events = append(b.Events, bridgesync.Event{UnsetClaim: &bridgesync.UnsetClaim{
			BlockNum:                  b.Num,
			BlockPos:                  uint64(l.Index),
			TxHash:                    l.TxHash,
			GlobalIndex:               new(big.Int).SetBytes(event.UnsetGlobalIndex[:]),
			UnsetGlobalIndexHashChain: event.NewUnsetGlobalIndexHashChain,
		}})
		return nil
	}
}

// buildSetClaimEventHandler creates a handler for the SetClaim log.
func buildSetClaimEventHandler(
	contract *agglayerbridgel2.Agglayerbridgel2,
) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		event, err := contract.ParseSetClaim(l)
		if err != nil {
			return fmt.Errorf("claimsync: error parsing SetClaim log: %w", err)
		}

		b.Events = append(b.Events, bridgesync.Event{SetClaim: &bridgesync.SetClaim{
			BlockNum:    b.Num,
			BlockPos:    uint64(l.Index),
			TxHash:      l.TxHash,
			GlobalIndex: new(big.Int).SetBytes(event.GlobalIndex[:]),
		}})
		return nil
	}
}
