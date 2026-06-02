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
//  2. Every holder of an ERC-20 vault/staking contract (from Step C HolderBridges)
//  3. Every token with remaining SC-locked value, directed to exitAddress
func RunStepD(cfg *Config, stepB *StepBResult, stepC *StepCResult) (*StepDResult, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP D — Build exit certificate")
	log.Info("═══════════════════════════════════════════")

	destNetwork := cfg.DestinationNetwork
	exitAddr := cfg.ExitAddress

	eoaExits := buildEOAExits(stepB, destNetwork)
	log.Infof("EOA exits: %d", len(eoaExits))

	holderExits := buildHolderBridgeExits(stepC, destNetwork)
	log.Infof("Holder exits: %d", len(holderExits))

	scExits := buildSCLockedExits(stepC, destNetwork, exitAddr)
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

func buildEOAExits(stepB *StepBResult, destNetwork uint32) []*agglayertypes.BridgeExit {
	totalEOAs := len(stepB.EOABalances)
	log.Infof("Processing %d EOA balance entries...", totalEOAs)

	logInterval := max(totalEOAs/logGranularity, 1)
	exits := make([]*agglayertypes.BridgeExit, 0, len(stepB.EOABalances))
	for i, eoa := range stepB.EOABalances {
		if totalEOAs > 0 && (i+1)%logInterval == 0 {
			log.Infof("  EOA progress: %d/%d", i+1, totalEOAs)
		}
		exits = append(exits, eoaToExits(eoa, destNetwork)...)
	}
	return exits
}

func eoaToExits(eoa EOABalance, destNetwork uint32) []*agglayertypes.BridgeExit {
	var exits []*agglayertypes.BridgeExit
	if amount := parseDecimalBigInt(eoa.ETHBalance); amount.Sign() > 0 {
		exits = append(exits, makeBridgeExit(0, common.Address{}, destNetwork, eoa.Address, amount))
	}
	for _, token := range eoa.Tokens {
		if amount := parseDecimalBigInt(token.Balance); amount.Sign() > 0 {
			exits = append(exits, makeBridgeExit(
				token.OriginNetwork, token.OriginTokenAddress, destNetwork, eoa.Address, amount,
			))
		}
	}
	return exits
}

func buildHolderBridgeExits(stepC *StepCResult, destNetwork uint32) []*agglayertypes.BridgeExit {
	exits := make([]*agglayertypes.BridgeExit, 0, len(stepC.HolderBridges))
	for _, hb := range stepC.HolderBridges {
		amount := parseDecimalBigInt(hb.Amount)
		if amount.Sign() <= 0 {
			continue
		}
		exits = append(exits, makeBridgeExit(hb.OriginNetwork, hb.OriginTokenAddress, destNetwork, hb.HolderAddress, amount))
	}
	return exits
}

func buildSCLockedExits(
	stepC *StepCResult, destNetwork uint32, exitAddr common.Address,
) []*agglayertypes.BridgeExit {
	log.Infof("Processing SC-locked values → exit address: %s", exitAddr.Hex())

	exits := make([]*agglayertypes.BridgeExit, 0, len(stepC.SCLockedValues))
	for _, entry := range stepC.SCLockedValues {
		amount := parseDecimalBigInt(entry.PendingSCLockedBalance)
		if amount.Sign() <= 0 {
			continue
		}
		exits = append(exits, makeBridgeExit(entry.OriginNetwork, entry.OriginTokenAddress, destNetwork, exitAddr, amount))
	}
	return exits
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
