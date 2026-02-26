package backward_forward_let

import (
	"math/big"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/ethereum/go-ethereum/common"
)

// RecoveryCase classifies the divergence between the L1 settled LET and the L2 bridge state.
type RecoveryCase string

const (
	// NoDivergence indicates L1 settled state and L2 on-chain state are in sync.
	NoDivergence RecoveryCase = "NoDivergence"
	// Case1 is ForwardLET only — a single divergent leaf batch, no extra L2 bridges.
	Case1 RecoveryCase = "Case1"
	// Case2 is BackwardLET + ForwardLET — single divergent leaf + extra real L2 bridges.
	Case2 RecoveryCase = "Case2"
	// Case3 is ForwardLET only — multiple divergent leaf batches, no extra L2 bridges.
	Case3 RecoveryCase = "Case3"
	// Case4 is BackwardLET + ForwardLET — multiple divergent leaves + extra real L2 bridges.
	Case4 RecoveryCase = "Case4"
)

// UndercollateralizedToken tracks the net under-collateralization amount per token.
type UndercollateralizedToken struct {
	TokenOriginNetwork uint32
	TokenOriginAddress common.Address
	Amount             *big.Int
}

// DiagnosisResult holds the complete output of the diagnosis phase.
type DiagnosisResult struct {
	Case RecoveryCase

	// L1 settled state (from AggLayer NetworkInfo).
	L1SettledLER           common.Hash
	L1SettledDepositCount  uint32 // = SettledLETLeafCount from NetworkInfo
	L1SettledHeight        uint64
	L1SettledCertificateID common.Hash

	// L2 on-chain bridge state.
	L2CurrentLER          common.Hash
	L2CurrentDepositCount uint32

	// DivergencePoint is the last deposit count where L1 settled and L2 bridge agree.
	DivergencePoint uint32

	// ExtraL2Bridges contains real L2 bridges (bridgesync.LeafData) after DivergencePoint.
	// Populated for Cases 2 and 4.
	ExtraL2Bridges []bridgesync.LeafData

	// DivergentLeaves are the bridge exits settled on L1 that are absent or different on L2.
	DivergentLeaves []*agglayertypes.BridgeExit

	// Undercollateralization summarises token under-collateralization from DivergentLeaves.
	Undercollateralization []UndercollateralizedToken

	// IsEmergencyState reports whether the L2 bridge is already paused.
	IsEmergencyState bool

	// AggsenderAPIFailed is set when the aggsender RPC was unreachable during the divergence walk.
	AggsenderAPIFailed bool

	// FailedCertHeight and FailedCertID are set when AggsenderAPIFailed is true.
	FailedCertHeight uint64
	FailedCertID     common.Hash
}
