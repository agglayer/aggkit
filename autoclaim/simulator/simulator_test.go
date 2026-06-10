package simulator

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/agglayer/aggkit/autoclaim/policy"
	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

var fixedNow = time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)

func TestSimulateClaimEstimatesAssetGas(t *testing.T) {
	client := &fakeClient{gas: 123_456}
	target := makeTarget()
	from := common.HexToAddress("0x6000000000000000000000000000000000000006")
	simulator := newTestSimulator(t, client, target, from)
	request := makeRequest(bridgesynctypes.LeafTypeAsset)
	proof := makeProof()
	request.Proof = &proof

	result, err := simulator.SimulateClaim(context.Background(), request)
	require.NoError(t, err)

	require.Equal(t, uint64(123_456), result.GasUsed)
	require.Equal(t, policy.NestedBridgeCallNotDetected, result.NestedBridgeCall)
	require.Equal(t, nestedBridgeDetectionSkipped, result.Metadata["nested_bridge_detection"])
	require.Equal(t, 1, client.calls)
	require.Equal(t, from, client.lastMsg.From)
	require.Equal(t, target.BridgeAddr, *client.lastMsg.To)
	require.Zero(t, client.lastMsg.Value.Cmp(common.Big0))
	require.NotEmpty(t, client.lastMsg.Data)
}

func TestSimulateClaimEstimatesMessageGas(t *testing.T) {
	client := &fakeClient{gas: 234_567}
	simulator := newTestSimulator(
		t,
		client,
		makeTarget(),
		common.HexToAddress("0x6000000000000000000000000000000000000006"),
	)
	request := makeRequest(bridgesynctypes.LeafTypeMessage)
	proof := makeProof()
	request.Proof = &proof

	result, err := simulator.SimulateClaim(context.Background(), request)
	require.NoError(t, err)

	require.Equal(t, uint64(234_567), result.GasUsed)
	require.Equal(t, 1, client.calls)
}

func TestSimulateClaimRejectsUnsupportedLeafType(t *testing.T) {
	client := &fakeClient{gas: 1}
	simulator := newTestSimulator(
		t,
		client,
		makeTarget(),
		common.HexToAddress("0x6000000000000000000000000000000000000006"),
	)
	request := makeRequest(bridgesynctypes.LeafType(99))
	proof := makeProof()
	request.Proof = &proof

	result, err := simulator.SimulateClaim(context.Background(), request)

	require.Nil(t, result)
	require.ErrorContains(t, err, "unsupported bridge leaf type")
	require.Equal(t, 0, client.calls)
}

func TestSimulateClaimRequiresPreparedProof(t *testing.T) {
	client := &fakeClient{gas: 1}
	simulator := newTestSimulator(
		t,
		client,
		makeTarget(),
		common.HexToAddress("0x6000000000000000000000000000000000000006"),
	)

	result, err := simulator.SimulateClaim(context.Background(), makeRequest(bridgesynctypes.LeafTypeAsset))

	require.Nil(t, result)
	require.ErrorContains(t, err, "prepared proof is required")
	require.Equal(t, 0, client.calls)
}

func TestSimulateClaimReturnsEstimateGasError(t *testing.T) {
	estimateErr := errors.New("rpc unavailable")
	client := &fakeClient{err: estimateErr}
	simulator := newTestSimulator(
		t,
		client,
		makeTarget(),
		common.HexToAddress("0x6000000000000000000000000000000000000006"),
	)
	request := makeRequest(bridgesynctypes.LeafTypeAsset)
	proof := makeProof()
	request.Proof = &proof

	result, err := simulator.SimulateClaim(context.Background(), request)

	require.Nil(t, result)
	require.ErrorIs(t, err, estimateErr)
	require.ErrorContains(t, err, "estimate claim gas")
}

func newTestSimulator(
	t *testing.T,
	client Client,
	target autoclaimtypes.ClaimerTarget,
	from common.Address,
) *Simulator {
	t.Helper()

	simulator, err := New(client, fakeProofPreparer{}, target, from)
	require.NoError(t, err)
	return simulator
}

func makeTarget() autoclaimtypes.ClaimerTarget {
	return autoclaimtypes.ClaimerTarget{
		ID:                 "claimer-20",
		DestinationNetwork: 20,
		BridgeAddr:         common.HexToAddress("0x5000000000000000000000000000000000000005"),
	}
}

func makeRequest(leafType bridgesynctypes.LeafType) autoclaimtypes.AutoClaimRequest {
	bridge := autoclaimtypes.BridgeExit{
		BlockNum:           10,
		BlockPos:           2,
		TxHash:             common.HexToHash("0x1111"),
		LeafType:           leafType,
		OriginNetwork:      autoclaimtypes.L1OriginNetwork,
		OriginAddress:      common.HexToAddress("0x1000000000000000000000000000000000000001"),
		DestinationNetwork: 20,
		DestinationAddress: common.HexToAddress("0x2000000000000000000000000000000000000002"),
		Amount:             big.NewInt(12345),
		Metadata:           []byte{0xde, 0xad, 0xbe, 0xef},
		DepositCount:       7,
		GlobalIndex:        autoclaimtypes.DeriveGlobalIndex(autoclaimtypes.L1OriginNetwork, 7),
	}

	return autoclaimtypes.AutoClaimRequest{
		Key:         autoclaimtypes.DeriveRequestKey(bridge.OriginNetwork, bridge.DestinationNetwork, 7),
		Status:      autoclaimtypes.RequestStatusDetected,
		Bridge:      bridge,
		GlobalIndex: new(big.Int).Set(bridge.GlobalIndex),
		CreatedAt:   fixedNow,
		UpdatedAt:   fixedNow,
	}
}

func makeProof() autoclaimtypes.ClaimProof {
	proof := autoclaimtypes.ClaimProof{
		MainnetExitRoot: common.HexToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		RollupExitRoot:  common.HexToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
		GlobalExitRoot:  common.HexToHash("0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"),
		PreparedAt:      fixedNow,
	}
	proof.ABILocalExitRoot[0] = common.HexToHash("0x01")
	proof.ABILocalExitRoot[31] = common.HexToHash("0x02")
	proof.ABIRollupExitRoot[0] = common.HexToHash("0x03")
	proof.ABIRollupExitRoot[31] = common.HexToHash("0x04")
	return proof
}

type fakeClient struct {
	gas     uint64
	err     error
	calls   int
	lastMsg ethereum.CallMsg
}

func (c *fakeClient) EstimateGas(_ context.Context, msg ethereum.CallMsg) (uint64, error) {
	c.calls++
	c.lastMsg = msg
	return c.gas, c.err
}

type fakeProofPreparer struct{}

func (fakeProofPreparer) PrepareProof(
	context.Context,
	autoclaimtypes.AutoClaimRequest,
) (*autoclaimtypes.ClaimProof, error) {
	return nil, nil
}
