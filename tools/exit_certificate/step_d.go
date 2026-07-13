package exit_certificate

import (
	"fmt"
	"math/big"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	bridgetypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
)

// RunStepD builds the exit certificate from EOA balances (Step B) and SC-locked values (Step C).
//
// Creates BridgeExit entries for:
//  1. Every (EOA, token) pair with a non-zero balance
//  2. Every holder of an ERC-20 vault/staking contract (from Step C HolderBridges)
//  3. Every token with remaining SC-locked value, directed to exitAddress
func RunStepD(cfg *Config, stepB *StepBResult, stepC *StepCResult) (*StepDResult, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP D — Build exit certificate")
	log.Info("═══════════════════════════════════════════")

	destNetwork := cfg.DestinationNetwork
	exitAddr := cfg.ExitAddress

	eoaExits, err := buildEOAExits(stepB, destNetwork)
	if err != nil {
		return nil, err
	}
	log.Infof("EOA exits: %d", len(eoaExits))

	holderExits, err := buildHolderBridgeExits(stepC, destNetwork)
	if err != nil {
		return nil, err
	}
	log.Infof("Holder exits: %d", len(holderExits))

	scExits, err := buildSCLockedExits(stepC, destNetwork, exitAddr)
	if err != nil {
		return nil, err
	}
	log.Infof("SC-locked exits: %d", len(scExits))

	bridgeExits := make([]*agglayertypes.BridgeExit, 0, len(eoaExits)+len(holderExits)+len(scExits))
	bridgeExits = append(bridgeExits, eoaExits...)
	bridgeExits = append(bridgeExits, holderExits...)
	bridgeExits = append(bridgeExits, scExits...)

	certificate := &agglayertypes.Certificate{
		NetworkID:         cfg.L2NetworkID,
		PrevLocalExitRoot: common.Hash{},
		NewLocalExitRoot:  common.Hash{},
		BridgeExits:       bridgeExits,
	}

	log.Infof("STEP D complete: certificate has %d bridge exits (%d EOA + %d holder + %d SC-locked)",
		len(bridgeExits), len(eoaExits), len(holderExits), len(scExits))

	return &StepDResult{Certificate: certificate}, nil
}

func buildEOAExits(stepB *StepBResult, destNetwork uint32) ([]*agglayertypes.BridgeExit, error) {
	totalEOAs := len(stepB.EOABalances)
	log.Infof("Processing %d EOA balance entries...", totalEOAs)

	logInterval := max(totalEOAs/logGranularity, 1)
	exits := make([]*agglayertypes.BridgeExit, 0, len(stepB.EOABalances))
	for i, eoa := range stepB.EOABalances {
		if totalEOAs > 0 && (i+1)%logInterval == 0 {
			log.Infof("  EOA progress: %d/%d", i+1, totalEOAs)
		}
		eoaExits, err := eoaToExits(eoa, destNetwork)
		if err != nil {
			return nil, err
		}
		exits = append(exits, eoaExits...)
	}
	return exits, nil
}

func eoaToExits(eoa EOABalance, destNetwork uint32) ([]*agglayertypes.BridgeExit, error) {
	var exits []*agglayertypes.BridgeExit
	amount, err := parseDecimalBigInt(eoa.ETHBalance)
	if err != nil {
		return nil, fmt.Errorf("EOA %s ETH balance: %w", eoa.Address.Hex(), err)
	}
	if amount.Sign() > 0 {
		exits = append(exits, makeBridgeExit(0, common.Address{}, destNetwork, eoa.Address, amount))
	}
	for _, token := range eoa.Tokens {
		amount, err := parseDecimalBigInt(token.Balance)
		if err != nil {
			return nil, fmt.Errorf("EOA %s balance of token %s: %w",
				eoa.Address.Hex(), token.WrappedTokenAddress.Hex(), err)
		}
		if amount.Sign() > 0 {
			exits = append(exits, makeBridgeExit(
				token.OriginNetwork, token.OriginTokenAddress, destNetwork, eoa.Address, amount,
			))
		}
	}
	return exits, nil
}

func buildHolderBridgeExits(stepC *StepCResult, destNetwork uint32) ([]*agglayertypes.BridgeExit, error) {
	exits := make([]*agglayertypes.BridgeExit, 0, len(stepC.HolderBridges))
	for _, hb := range stepC.HolderBridges {
		amount, err := parseDecimalBigInt(hb.Amount)
		if err != nil {
			return nil, fmt.Errorf("holder bridge amount for %s (vault %s): %w",
				hb.HolderAddress.Hex(), hb.VaultAddress.Hex(), err)
		}
		if amount.Sign() <= 0 {
			continue
		}
		exits = append(exits, makeBridgeExit(hb.OriginNetwork, hb.OriginTokenAddress, destNetwork, hb.HolderAddress, amount))
	}
	return exits, nil
}

func buildSCLockedExits(
	stepC *StepCResult, destNetwork uint32, exitAddr common.Address,
) ([]*agglayertypes.BridgeExit, error) {
	log.Infof("Processing SC-locked values → exit address: %s", exitAddr.Hex())

	exits := make([]*agglayertypes.BridgeExit, 0, len(stepC.SCLockedValues))
	for _, entry := range stepC.SCLockedValues {
		amount, err := parseDecimalBigInt(entry.PendingSCLockedBalance)
		if err != nil {
			return nil, fmt.Errorf("pending SC-locked balance for token %s: %w",
				entry.WrappedTokenAddress.Hex(), err)
		}
		if amount.Sign() <= 0 {
			continue
		}
		exits = append(exits, makeBridgeExit(entry.OriginNetwork, entry.OriginTokenAddress, destNetwork, exitAddr, amount))
	}
	return exits, nil
}

// MakeBridgeExit creates a BridgeExit for an asset transfer. Exported for tests.
func MakeBridgeExit(
	originNetwork uint32, originTokenAddress common.Address,
	destNetwork uint32, destAddress common.Address, amount *big.Int,
) *agglayertypes.BridgeExit {
	return makeBridgeExit(originNetwork, originTokenAddress, destNetwork, destAddress, amount)
}

func makeBridgeExit(
	originNetwork uint32, originTokenAddress common.Address,
	destNetwork uint32, destAddress common.Address, amount *big.Int,
) *agglayertypes.BridgeExit {
	return &agglayertypes.BridgeExit{
		LeafType: bridgetypes.LeafTypeAsset,
		TokenInfo: &agglayertypes.TokenInfo{
			OriginNetwork:      originNetwork,
			OriginTokenAddress: originTokenAddress,
		},
		DestinationNetwork: destNetwork,
		DestinationAddress: destAddress,
		Amount:             amount,
	}
}
