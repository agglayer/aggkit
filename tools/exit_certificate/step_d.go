package exit_certificate

import (
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
//  2. Every token with SC-locked value, directed to exitAddress
func RunStepD(cfg *Config, stepB *StepBResult, stepC *StepCResult) (*StepDResult, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP D — Build exit certificate")
	log.Info("═══════════════════════════════════════════")

	destNetwork := cfg.DestinationNetwork
	exitAddr := cfg.ExitAddress

	var bridgeExits []*agglayertypes.BridgeExit

	// Part 1: EOA balance exits
	log.Infof("Processing %d EOA balance entries...", len(stepB.EOABalances))
	for _, eoa := range stepB.EOABalances {
		if amount := parseDecimalBigInt(eoa.ETHBalance); amount.Sign() > 0 {
			bridgeExits = append(bridgeExits, makeBridgeExit(0, common.Address{}, destNetwork, eoa.Address, amount))
		}
		for _, token := range eoa.Tokens {
			if amount := parseDecimalBigInt(token.Balance); amount.Sign() > 0 {
				bridgeExits = append(bridgeExits, makeBridgeExit(
					token.OriginNetwork, token.OriginTokenAddress, destNetwork, eoa.Address, amount,
				))
			}
		}
	}
	eoaExitCount := len(bridgeExits)
	log.Infof("EOA exits: %d", eoaExitCount)

	// Part 2: SC-locked value exits
	log.Infof("Processing SC-locked values → exit address: %s", exitAddr.Hex())
	for _, entry := range stepC.SCLockedValues {
		amount := parseDecimalBigInt(entry.SCLockedBalance)
		if amount.Sign() <= 0 {
			continue
		}

		originNetwork := entry.OriginNetwork
		originAddr := entry.OriginTokenAddress
		if entry.WrappedTokenAddress == (common.Address{}) {
			originNetwork = 0
			originAddr = common.Address{}
		}

		bridgeExits = append(bridgeExits, makeBridgeExit(originNetwork, originAddr, destNetwork, exitAddr, amount))
	}
	scExitCount := len(bridgeExits) - eoaExitCount
	log.Infof("SC-locked exits: %d", scExitCount)

	certificate := &agglayertypes.Certificate{
		NetworkID:         cfg.L2NetworkID,
		PrevLocalExitRoot: common.Hash{},
		NewLocalExitRoot:  common.Hash{},
		BridgeExits:       bridgeExits,
	}

	log.Infof("STEP D complete: certificate has %d bridge exits (%d EOA + %d SC-locked)",
		len(bridgeExits), eoaExitCount, scExitCount)

	return &StepDResult{Certificate: certificate}, nil
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
