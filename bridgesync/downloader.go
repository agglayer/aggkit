package bridgesync

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/etrog/polygonzkevmbridge"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/l2-sovereign-chain/polygonzkevmbridgev2"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/sync"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

var (
	bridgeEventSignature = crypto.Keccak256Hash([]byte(
		"BridgeEvent(uint8,uint32,address,uint32,address,uint256,bytes,uint32)",
	))
	claimEventSignature         = crypto.Keccak256Hash([]byte("ClaimEvent(uint256,uint32,address,address,uint256)"))
	claimEventSignaturePreEtrog = crypto.Keccak256Hash([]byte("ClaimEvent(uint32,uint32,address,address,uint256)"))
	tokenMappingEventSignature  = crypto.Keccak256Hash([]byte("NewWrappedToken(uint32,address,address,bytes)"))

	claimAssetEtrogMethodID      = common.Hex2Bytes("ccaa2d11")
	claimMessageEtrogMethodID    = common.Hex2Bytes("f5efcd79")
	claimAssetPreEtrogMethodID   = common.Hex2Bytes("2cffd02e")
	claimMessagePreEtrogMethodID = common.Hex2Bytes("2d2c9d94")
)

// EthClienter defines the methods required to interact with an Ethereum client.
type EthClienter interface {
	ethereum.LogFilterer
	ethereum.BlockNumberReader
	ethereum.ChainReader
	ethereum.TransactionReader
	bind.ContractBackend
}

func buildAppender(client EthClienter, bridgeAddr common.Address, syncFullClaims bool) (sync.LogAppenderMap, error) {
	bridgeContractV1, err := polygonzkevmbridge.NewPolygonzkevmbridge(bridgeAddr, client)
	if err != nil {
		return nil, err
	}

	bridgeContractV2, err := polygonzkevmbridgev2.NewPolygonzkevmbridgev2(bridgeAddr, client)
	if err != nil {
		return nil, err
	}

	appender := make(sync.LogAppenderMap)

	appender[bridgeEventSignature] = func(b *sync.EVMBlock, l types.Log) error {
		bridgeEvent, err := bridgeContractV2.ParseBridgeEvent(l)
		if err != nil {
			return fmt.Errorf(
				"error parsing log %+v using d.bridgeContractV2.ParseBridgeEvent: %w",
				l, err,
			)
		}

		calldata, err := extractCalldata(client, l.TxHash)
		if err != nil {
			return fmt.Errorf("failed to extract the bridge event calldata (tx hash: %s): %w", l.TxHash, err)
		}

		b.Events = append(b.Events, Event{Bridge: &Bridge{
			BlockNum:           b.Num,
			BlockPos:           uint64(l.Index),
			LeafType:           bridgeEvent.LeafType,
			OriginNetwork:      bridgeEvent.OriginNetwork,
			OriginAddress:      bridgeEvent.OriginAddress,
			DestinationNetwork: bridgeEvent.DestinationNetwork,
			DestinationAddress: bridgeEvent.DestinationAddress,
			Amount:             bridgeEvent.Amount,
			Metadata:           bridgeEvent.Metadata,
			DepositCount:       bridgeEvent.DepositCount,
			BlockTimestamp:     b.Timestamp,
			TxHash:             l.TxHash,
			FromAddress:        l.Address,
			Calldata:           calldata,
		}})

		return nil
	}

	appender[claimEventSignature] = func(b *sync.EVMBlock, l types.Log) error {
		claimEvent, err := bridgeContractV2.ParseClaimEvent(l)
		if err != nil {
			return fmt.Errorf(
				"error parsing log %+v using d.bridgeContractV2.ParseClaimEvent: %w",
				l, err,
			)
		}
		claim := &Claim{
			BlockNum:           b.Num,
			BlockPos:           uint64(l.Index),
			GlobalIndex:        claimEvent.GlobalIndex,
			OriginNetwork:      claimEvent.OriginNetwork,
			OriginAddress:      claimEvent.OriginAddress,
			DestinationAddress: claimEvent.DestinationAddress,
			Amount:             claimEvent.Amount,
			BlockTimestamp:     b.Timestamp,
			TxHash:             l.TxHash,
			FromAddress:        l.Address,
		}
		if syncFullClaims {
			if err := claim.populateClaimCalldata(client, l.TxHash); err != nil {
				return err
			}
		}
		b.Events = append(b.Events, Event{Claim: claim})
		return nil
	}

	appender[claimEventSignaturePreEtrog] = func(b *sync.EVMBlock, l types.Log) error {
		claimEvent, err := bridgeContractV1.ParseClaimEvent(l)
		if err != nil {
			return fmt.Errorf(
				"error parsing log %+v using d.bridgeContractV1.ParseClaimEvent: %w",
				l, err,
			)
		}
		claim := &Claim{
			BlockNum:           b.Num,
			BlockPos:           uint64(l.Index),
			GlobalIndex:        big.NewInt(int64(claimEvent.Index)),
			OriginNetwork:      claimEvent.OriginNetwork,
			OriginAddress:      claimEvent.OriginAddress,
			DestinationAddress: claimEvent.DestinationAddress,
			Amount:             claimEvent.Amount,
		}
		if syncFullClaims {
			if err := claim.populateClaimCalldata(client, l.TxHash); err != nil {
				return err
			}
		}
		b.Events = append(b.Events, Event{Claim: claim})
		return nil
	}

	appender[tokenMappingEventSignature] = func(b *sync.EVMBlock, l types.Log) error {
		tokenMappingEvent, err := bridgeContractV2.ParseNewWrappedToken(l)
		if err != nil {
			return fmt.Errorf(
				"error parsing log %+v using d.bridgeContractV2.ParseNewWrappedToken: %w",
				l, err,
			)
		}

		calldata, err := extractCalldata(client, l.TxHash)
		if err != nil {
			return fmt.Errorf("failed to extract the token mapping event calldata (tx hash: %s): %w", l.TxHash, err)
		}

		b.Events = append(b.Events, Event{TokenMapping: &TokenMapping{
			BlockNum:            b.Num,
			BlockPos:            uint64(l.Index),
			BlockTimestamp:      b.Timestamp,
			TxHash:              l.TxHash,
			OriginNetwork:       tokenMappingEvent.OriginNetwork,
			OriginTokenAddress:  tokenMappingEvent.OriginTokenAddress,
			WrappedTokenAddress: tokenMappingEvent.WrappedTokenAddress,
			Metadata:            tokenMappingEvent.Metadata,
			Calldata:            calldata,
		}})
		return nil
	}

	return appender, nil
}

// extractCalldata tries to extract the calldata for the transaction indentified by transaction hash.
// It relies on eth_getTransactionByHash JSON RPC function.
func extractCalldata(client EthClienter, txHash common.Hash) ([]byte, error) {
	tx, _, err := client.TransactionByHash(context.Background(), txHash)
	if err != nil {
		return nil, err
	}

	return tx.Data(), nil
}

// populateClaimCalldata tries to find the claim transaction calldata and decodes it.
func (c *Claim) populateClaimCalldata(client EthClienter, txHash common.Hash) error {
	callData, err := extractCalldata(client, txHash)
	if err != nil {
		return err
	}

	found, err := c.tryDecodeClaimCalldata(callData)
	if err != nil {
		return err
	}

	if !found {
		return db.ErrNotFound
	}

	return nil
}

// tryDecodeClaimCalldata attempts to find and decode the claim calldata from the provided input bytes.
// It checks if the method ID corresponds to either the claim asset or claim message methods.
// If a match is found, it decodes the calldata using the ABI of the bridge contract and updates the claim object.
// Returns true if the calldata is successfully decoded and matches the expected format, otherwise returns false.
func (c *Claim) tryDecodeClaimCalldata(input []byte) (bool, error) {
	methodID := input[:4]
	switch {
	case bytes.Equal(methodID, claimAssetEtrogMethodID):
		fallthrough
	case bytes.Equal(methodID, claimMessageEtrogMethodID):
		bridgeV2ABI, err := abi.JSON(strings.NewReader(polygonzkevmbridgev2.Polygonzkevmbridgev2ABI))
		if err != nil {
			return false, err
		}
		// Recover Method from signature and ABI
		method, err := bridgeV2ABI.MethodById(methodID)
		if err != nil {
			return false, err
		}

		data, err := method.Inputs.Unpack(input[4:])
		if err != nil {
			return false, err
		}

		found, err := c.decodeEtrogCalldata(data)
		if err != nil {
			return false, err
		}

		if found {
			c.IsMessage = bytes.Equal(methodID, claimMessageEtrogMethodID)
		}

		return found, nil

	case bytes.Equal(methodID, claimAssetPreEtrogMethodID):
		fallthrough
	case bytes.Equal(methodID, claimMessagePreEtrogMethodID):
		bridgeABI, err := abi.JSON(strings.NewReader(polygonzkevmbridge.PolygonzkevmbridgeABI))
		if err != nil {
			return false, err
		}

		// Recover Method from signature and ABI
		method, err := bridgeABI.MethodById(methodID)
		if err != nil {
			return false, err
		}

		data, err := method.Inputs.Unpack(input[4:])
		if err != nil {
			return false, err
		}

		found, err := c.decodePreEtrogCalldata(data)
		if err != nil {
			return false, err
		}

		if found {
			c.IsMessage = bytes.Equal(methodID, claimMessagePreEtrogMethodID)
		}

		return found, nil

	default:
		return false, nil
	}
}
