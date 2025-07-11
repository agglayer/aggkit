package bridgesync

import (
	"context"
	"math/big"
	"testing"

	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/contracts/claimmock"
	"github.com/agglayer/aggkit/test/contracts/claimmockcaller"
	"github.com/agglayer/aggkit/test/contracts/claimmocktest"
	tree "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

type testCase struct {
	description   string
	bridgeAddr    common.Address
	log           types.Log
	expectedClaim Claim
}

func TestClaimCalldata(t *testing.T) {
	testCases := []testCase{}

	ctx, cancelFn := context.WithCancel(context.Background())
	// Setup Docker L1
	client, auth := startGeth(t, ctx, cancelFn)

	// Deploy contracts
	bridgeAddr, _, bridgeContract, err := claimmock.DeployClaimmock(auth, client)
	require.NoError(t, err)
	claimCallerAddr, _, claimCaller, err := claimmockcaller.DeployClaimmockcaller(auth, client, bridgeAddr)
	require.NoError(t, err)
	claimTestAddr, _, claimTest, err := claimmocktest.DeployClaimmocktest(auth, client, bridgeAddr, claimCallerAddr)
	require.NoError(t, err)

	proofLocal := [tree.DefaultHeight][common.HashLength]byte{}
	proofLocalH := tree.Proof{}
	proofLocal[5] = common.HexToHash("beef")
	proofLocalH[5] = common.HexToHash("beef")
	proofRollup := [tree.DefaultHeight][common.HashLength]byte{}
	proofRollupH := tree.Proof{}
	proofRollup[4] = common.HexToHash("a1fa")
	proofRollupH[4] = common.HexToHash("a1fa")
	expectedClaim := Claim{
		OriginNetwork:       69,
		OriginAddress:       common.HexToAddress("ffaaffaa"),
		DestinationAddress:  common.HexToAddress("123456789"),
		Amount:              big.NewInt(3),
		MainnetExitRoot:     common.HexToHash("5ca1e"),
		RollupExitRoot:      common.HexToHash("dead"),
		ProofLocalExitRoot:  proofLocalH,
		ProofRollupExitRoot: proofRollupH,
		DestinationNetwork:  0,
		Metadata:            []byte{},
		GlobalExitRoot:      crypto.Keccak256Hash(common.HexToHash("5ca1e").Bytes(), common.HexToHash("dead").Bytes()),
		FromAddress:         auth.From,
	}
	expectedClaim2 := Claim{
		OriginNetwork:       87,
		OriginAddress:       common.HexToAddress("eebbeebb"),
		DestinationAddress:  common.HexToAddress("2233445566"),
		Amount:              big.NewInt(4),
		MainnetExitRoot:     common.HexToHash("5ca1e"),
		RollupExitRoot:      common.HexToHash("dead"),
		ProofLocalExitRoot:  proofLocalH,
		ProofRollupExitRoot: proofRollupH,
		DestinationNetwork:  0,
		Metadata:            []byte{},
		GlobalExitRoot:      crypto.Keccak256Hash(common.HexToHash("5ca1e").Bytes(), common.HexToHash("dead").Bytes()),
		FromAddress:         auth.From,
	}
	expectedClaim3 := Claim{
		OriginNetwork:       69,
		OriginAddress:       common.HexToAddress("ffaaffaa"),
		DestinationAddress:  common.HexToAddress("2233445566"),
		Amount:              big.NewInt(5),
		MainnetExitRoot:     common.HexToHash("5ca1e"),
		RollupExitRoot:      common.HexToHash("dead"),
		ProofLocalExitRoot:  proofLocalH,
		ProofRollupExitRoot: proofRollupH,
		DestinationNetwork:  0,
		Metadata:            []byte{},
		GlobalExitRoot:      crypto.Keccak256Hash(common.HexToHash("5ca1e").Bytes(), common.HexToHash("dead").Bytes()),
		FromAddress:         auth.From,
	}
	auth.GasLimit = 999999 // for some reason gas estimation fails :(

	abi, err := claimmock.ClaimmockMetaData.GetAbi()
	require.NoError(t, err)

	// direct call claim asset
	expectedClaim.GlobalIndex = big.NewInt(421)
	expectedClaim.IsMessage = false
	tx, err := bridgeContract.ClaimAsset(
		auth,
		proofLocal,
		proofRollup,
		expectedClaim.GlobalIndex,
		expectedClaim.MainnetExitRoot,
		expectedClaim.RollupExitRoot,
		expectedClaim.OriginNetwork,
		expectedClaim.OriginAddress,
		0,
		expectedClaim.DestinationAddress,
		expectedClaim.Amount,
		nil,
	)
	require.NoError(t, err)

	r, err := waitForReceipt(ctx, client, tx.Hash(), 10)
	require.NoError(t, err)
	testCases = append(testCases, testCase{
		description:   "direct call to claim asset",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[0],
		expectedClaim: expectedClaim,
	})

	// indirect call claim asset
	expectedClaim.IsMessage = false
	expectedClaim.GlobalIndex = big.NewInt(422)
	expectedClaim.FromAddress = claimCallerAddr
	tx, err = claimCaller.ClaimAsset(
		auth,
		proofLocal,
		proofRollup,
		expectedClaim.GlobalIndex,
		expectedClaim.MainnetExitRoot,
		expectedClaim.RollupExitRoot,
		expectedClaim.OriginNetwork,
		expectedClaim.OriginAddress,
		0,
		expectedClaim.DestinationAddress,
		expectedClaim.Amount,
		nil,
		false,
	)
	require.NoError(t, err)

	r, err = waitForReceipt(ctx, client, tx.Hash(), 20)
	require.NoError(t, err)
	testCases = append(testCases, testCase{
		description:   "indirect call to claim asset",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[0],
		expectedClaim: expectedClaim,
	})

	// indirect call claim asset bytes
	expectedClaim.GlobalIndex = big.NewInt(423)
	expectedClaim.IsMessage = false
	expectedClaimBytes, err := encodeClaimCalldata(abi, "claimAsset", expectedClaim, proofLocal, proofRollup)
	require.NoError(t, err)
	tx, err = claimCaller.ClaimBytes(
		auth,
		expectedClaimBytes,
		false,
	)
	require.NoError(t, err)

	r, err = waitForReceipt(ctx, client, tx.Hash(), 20)
	require.NoError(t, err)
	testCases = append(testCases, testCase{
		description:   "indirect call to claim asset bytes",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[0],
		expectedClaim: expectedClaim,
	})

	// direct call claim message
	expectedClaim.IsMessage = true
	expectedClaim.GlobalIndex = big.NewInt(424)
	expectedClaim.FromAddress = auth.From
	tx, err = bridgeContract.ClaimMessage(
		auth,
		proofLocal,
		proofRollup,
		expectedClaim.GlobalIndex,
		expectedClaim.MainnetExitRoot,
		expectedClaim.RollupExitRoot,
		expectedClaim.OriginNetwork,
		expectedClaim.OriginAddress,
		0,
		expectedClaim.DestinationAddress,
		expectedClaim.Amount,
		nil,
	)
	require.NoError(t, err)

	r, err = waitForReceipt(ctx, client, tx.Hash(), 20)
	require.NoError(t, err)
	testCases = append(testCases, testCase{
		description:   "direct call to claim message",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[0],
		expectedClaim: expectedClaim,
	})

	// indirect call claim message
	expectedClaim.IsMessage = true
	expectedClaim.GlobalIndex = big.NewInt(425)
	expectedClaim.FromAddress = claimCallerAddr
	tx, err = claimCaller.ClaimMessage(
		auth,
		proofLocal,
		proofRollup,
		expectedClaim.GlobalIndex,
		expectedClaim.MainnetExitRoot,
		expectedClaim.RollupExitRoot,
		expectedClaim.OriginNetwork,
		expectedClaim.OriginAddress,
		0,
		expectedClaim.DestinationAddress,
		expectedClaim.Amount,
		nil,
		false,
	)
	require.NoError(t, err)

	r, err = waitForReceipt(ctx, client, tx.Hash(), 20)
	require.NoError(t, err)
	testCases = append(testCases, testCase{
		description:   "indirect call to claim message",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[0],
		expectedClaim: expectedClaim,
	})

	// indirect call claim message bytes
	expectedClaim.GlobalIndex = big.NewInt(426)
	expectedClaim.IsMessage = true
	expectedClaimBytes, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim, proofLocal, proofRollup)
	require.NoError(t, err)
	tx, err = claimCaller.ClaimBytes(
		auth,
		expectedClaimBytes,
		false,
	)
	require.NoError(t, err)

	r, err = waitForReceipt(ctx, client, tx.Hash(), 20)
	require.NoError(t, err)
	testCases = append(testCases, testCase{
		description:   "indirect call to claim message bytes",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[0],
		expectedClaim: expectedClaim,
	})

	// indirect call claim message bytes
	expectedClaim.GlobalIndex = big.NewInt(427)
	expectedClaim.IsMessage = true
	expectedClaimBytes, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim, proofLocal, proofRollup)
	require.NoError(t, err)
	_, err = claimCaller.ClaimBytes(
		auth,
		expectedClaimBytes,
		true,
	)
	require.NoError(t, err)

	reverted := [2]bool{false, false}

	// 2 indirect call claim message (same global index)
	expectedClaim.IsMessage = true
	expectedClaim.GlobalIndex = big.NewInt(427)
	expectedClaim2.IsMessage = true
	expectedClaim2.GlobalIndex = big.NewInt(427)
	expectedClaim2.FromAddress = claimCallerAddr
	expectedClaimBytes, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes2, err := encodeClaimCalldata(abi, "claimMessage", expectedClaim2, proofLocal, proofRollup)
	require.NoError(t, err)
	tx, err = claimCaller.Claim2Bytes(
		auth,
		expectedClaimBytes,
		expectedClaimBytes2,
		reverted,
	)
	require.NoError(t, err)

	r, err = waitForReceipt(ctx, client, tx.Hash(), 20)
	require.NoError(t, err)
	testCases = append(testCases, testCase{
		description:   "2 indirect call claim message 1 (same globalIndex)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[0],
		expectedClaim: expectedClaim,
	})
	testCases = append(testCases, testCase{
		description:   "2 indirect call claim message 2 (same globalIndex)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[1],
		expectedClaim: expectedClaim2,
	})

	// 2 indirect call claim message (diff global index)
	expectedClaim.IsMessage = true
	expectedClaim.GlobalIndex = big.NewInt(428)
	expectedClaim2.IsMessage = true
	expectedClaim2.GlobalIndex = big.NewInt(429)
	expectedClaimBytes, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes2, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim2, proofLocal, proofRollup)
	require.NoError(t, err)
	tx, err = claimCaller.Claim2Bytes(
		auth,
		expectedClaimBytes,
		expectedClaimBytes2,
		reverted,
	)
	require.NoError(t, err)

	r, err = waitForReceipt(ctx, client, tx.Hash(), 20)
	require.NoError(t, err)
	testCases = append(testCases, testCase{
		description:   "2 indirect call claim message 1 (diff globalIndex)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[0],
		expectedClaim: expectedClaim,
	})
	testCases = append(testCases, testCase{
		description:   "2 indirect call claim message 2 (diff globalIndex)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[1],
		expectedClaim: expectedClaim2,
	})

	reverted = [2]bool{false, true}

	// 2 indirect call claim message (same global index) (1 ok, 1 reverted)
	expectedClaim.IsMessage = true
	expectedClaim.GlobalIndex = big.NewInt(430)
	expectedClaim2.IsMessage = true
	expectedClaim2.GlobalIndex = big.NewInt(430)
	expectedClaimBytes, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes2, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim2, proofLocal, proofRollup)
	require.NoError(t, err)
	tx, err = claimCaller.Claim2Bytes(
		auth,
		expectedClaimBytes,
		expectedClaimBytes2,
		reverted,
	)
	require.NoError(t, err)

	r, err = waitForReceipt(ctx, client, tx.Hash(), 20)
	require.NoError(t, err)
	testCases = append(testCases, testCase{
		description:   "2 indirect call claim message (same globalIndex) (1 ok, 1 reverted)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[0],
		expectedClaim: expectedClaim,
	})

	// 2 indirect call claim message (diff global index) (1 ok, 1 reverted)
	expectedClaim.IsMessage = true
	expectedClaim.GlobalIndex = big.NewInt(431)
	expectedClaim2.IsMessage = true
	expectedClaim2.GlobalIndex = big.NewInt(432)
	expectedClaimBytes, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes2, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim2, proofLocal, proofRollup)
	require.NoError(t, err)
	tx, err = claimCaller.Claim2Bytes(
		auth,
		expectedClaimBytes,
		expectedClaimBytes2,
		reverted,
	)
	require.NoError(t, err)

	r, err = waitForReceipt(ctx, client, tx.Hash(), 20)
	require.NoError(t, err)
	testCases = append(testCases, testCase{
		description:   "2 indirect call claim message (diff globalIndex) (1 ok, 1 reverted)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[0],
		expectedClaim: expectedClaim,
	})

	reverted = [2]bool{true, false}

	// 2 indirect call claim message (same global index) (1 reverted, 1 ok)
	expectedClaim.IsMessage = true
	expectedClaim.GlobalIndex = big.NewInt(433)
	expectedClaim2.IsMessage = true
	expectedClaim2.GlobalIndex = big.NewInt(433)
	expectedClaimBytes, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes2, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim2, proofLocal, proofRollup)
	require.NoError(t, err)
	tx, err = claimCaller.Claim2Bytes(
		auth,
		expectedClaimBytes,
		expectedClaimBytes2,
		reverted,
	)
	require.NoError(t, err)

	r, err = waitForReceipt(ctx, client, tx.Hash(), 20)
	require.NoError(t, err)
	testCases = append(testCases, testCase{
		description:   "2 indirect call claim message (same globalIndex) (reverted,ok)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[0],
		expectedClaim: expectedClaim2,
	})

	// 2 indirect call claim message (diff global index) (1 reverted, 1 ok)
	expectedClaim.IsMessage = true
	expectedClaim.GlobalIndex = big.NewInt(434)
	expectedClaim2.IsMessage = true
	expectedClaim2.GlobalIndex = big.NewInt(435)
	expectedClaimBytes, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes2, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim2, proofLocal, proofRollup)
	require.NoError(t, err)
	tx, err = claimCaller.Claim2Bytes(
		auth,
		expectedClaimBytes,
		expectedClaimBytes2,
		reverted,
	)
	require.NoError(t, err)

	r, err = waitForReceipt(ctx, client, tx.Hash(), 20)
	require.NoError(t, err)
	testCases = append(testCases, testCase{
		description:   "2 indirect call claim message (diff globalIndex) (reverted,ok)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[0],
		expectedClaim: expectedClaim2,
	})

	reverted = [2]bool{false, false}

	// 2 indirect call claim asset (same global index)
	expectedClaim.IsMessage = false
	expectedClaim.GlobalIndex = big.NewInt(436)
	expectedClaim2.IsMessage = false
	expectedClaim2.GlobalIndex = big.NewInt(436)
	expectedClaimBytes, err = encodeClaimCalldata(abi, "claimAsset", expectedClaim, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes2, err = encodeClaimCalldata(abi, "claimAsset", expectedClaim2, proofLocal, proofRollup)
	require.NoError(t, err)
	tx, err = claimCaller.Claim2Bytes(
		auth,
		expectedClaimBytes,
		expectedClaimBytes2,
		reverted,
	)
	require.NoError(t, err)

	r, err = waitForReceipt(ctx, client, tx.Hash(), 20)
	require.NoError(t, err)
	testCases = append(testCases, testCase{
		description:   "2 indirect call claim asset 1 (same globalIndex)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[0],
		expectedClaim: expectedClaim,
	})
	testCases = append(testCases, testCase{
		description:   "2 indirect call claim asset 2 (same globalIndex)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[1],
		expectedClaim: expectedClaim2,
	})

	// 2 indirect call claim asset (diff global index)
	expectedClaim.IsMessage = false
	expectedClaim.GlobalIndex = big.NewInt(437)
	expectedClaim2.IsMessage = false
	expectedClaim2.GlobalIndex = big.NewInt(438)
	expectedClaimBytes, err = encodeClaimCalldata(abi, "claimAsset", expectedClaim, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes2, err = encodeClaimCalldata(abi, "claimAsset", expectedClaim2, proofLocal, proofRollup)
	require.NoError(t, err)
	tx, err = claimCaller.Claim2Bytes(
		auth,
		expectedClaimBytes,
		expectedClaimBytes2,
		reverted,
	)
	require.NoError(t, err)

	r, err = waitForReceipt(ctx, client, tx.Hash(), 20)
	require.NoError(t, err)
	testCases = append(testCases, testCase{
		description:   "2 indirect call claim asset 1 (diff globalIndex)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[0],
		expectedClaim: expectedClaim,
	})
	testCases = append(testCases, testCase{
		description:   "2 indirect call claim asset 2 (diff globalIndex)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[1],
		expectedClaim: expectedClaim2,
	})

	reverted = [2]bool{false, true}

	// 2 indirect call claim asset (same global index) (1 ok, 1 reverted)
	expectedClaim.IsMessage = false
	expectedClaim.GlobalIndex = big.NewInt(439)
	expectedClaim2.IsMessage = false
	expectedClaim2.GlobalIndex = big.NewInt(439)
	expectedClaimBytes, err = encodeClaimCalldata(abi, "claimAsset", expectedClaim, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes2, err = encodeClaimCalldata(abi, "claimAsset", expectedClaim2, proofLocal, proofRollup)
	require.NoError(t, err)
	tx, err = claimCaller.Claim2Bytes(
		auth,
		expectedClaimBytes,
		expectedClaimBytes2,
		reverted,
	)
	require.NoError(t, err)

	r, err = waitForReceipt(ctx, client, tx.Hash(), 20)
	require.NoError(t, err)
	testCases = append(testCases, testCase{
		description:   "2 indirect call claim asset (same globalIndex) (1 ok, 1 reverted)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[0],
		expectedClaim: expectedClaim,
	})

	// 2 indirect call claim message (diff global index) (1 ok, 1 reverted)
	expectedClaim.IsMessage = false
	expectedClaim.GlobalIndex = big.NewInt(440)
	expectedClaim2.IsMessage = false
	expectedClaim2.GlobalIndex = big.NewInt(441)
	expectedClaimBytes, err = encodeClaimCalldata(abi, "claimAsset", expectedClaim, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes2, err = encodeClaimCalldata(abi, "claimAsset", expectedClaim2, proofLocal, proofRollup)
	require.NoError(t, err)
	tx, err = claimCaller.Claim2Bytes(
		auth,
		expectedClaimBytes,
		expectedClaimBytes2,
		reverted,
	)
	require.NoError(t, err)

	r, err = waitForReceipt(ctx, client, tx.Hash(), 20)
	require.NoError(t, err)
	testCases = append(testCases, testCase{
		description:   "2 indirect call claim asset (diff globalIndex) (1 ok, 1 reverted)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[0],
		expectedClaim: expectedClaim,
	})

	reverted = [2]bool{true, false}

	// 2 indirect call claim asset (same global index) (1 reverted, 1 ok)
	expectedClaim.IsMessage = false
	expectedClaim.GlobalIndex = big.NewInt(442)
	expectedClaim2.IsMessage = false
	expectedClaim2.GlobalIndex = big.NewInt(442)
	expectedClaimBytes, err = encodeClaimCalldata(abi, "claimAsset", expectedClaim, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes2, err = encodeClaimCalldata(abi, "claimAsset", expectedClaim2, proofLocal, proofRollup)
	require.NoError(t, err)
	tx, err = claimCaller.Claim2Bytes(
		auth,
		expectedClaimBytes,
		expectedClaimBytes2,
		reverted,
	)
	require.NoError(t, err)

	r, err = waitForReceipt(ctx, client, tx.Hash(), 20)
	require.NoError(t, err)
	testCases = append(testCases, testCase{
		description:   "2 indirect call claim asset (same globalIndex) (reverted,ok)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[0],
		expectedClaim: expectedClaim2,
	})

	// 2 indirect call claim asset (diff global index) (1 reverted, 1 ok)
	expectedClaim.IsMessage = false
	expectedClaim.GlobalIndex = big.NewInt(443)
	expectedClaim2.IsMessage = false
	expectedClaim2.GlobalIndex = big.NewInt(444)
	expectedClaimBytes, err = encodeClaimCalldata(abi, "claimAsset", expectedClaim, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes2, err = encodeClaimCalldata(abi, "claimAsset", expectedClaim2, proofLocal, proofRollup)
	require.NoError(t, err)
	tx, err = claimCaller.Claim2Bytes(
		auth,
		expectedClaimBytes,
		expectedClaimBytes2,
		reverted,
	)
	require.NoError(t, err)

	r, err = waitForReceipt(ctx, client, tx.Hash(), 20)
	require.NoError(t, err)
	testCases = append(testCases, testCase{
		description:   "2 indirect call claim asset (diff globalIndex) (reverted,ok)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[0],
		expectedClaim: expectedClaim2,
	})

	// indirect + indirect call claim message bytes
	expectedClaim.GlobalIndex = big.NewInt(426)
	expectedClaim.IsMessage = true
	expectedClaimBytes, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim, proofLocal, proofRollup)
	require.NoError(t, err)
	tx, err = claimTest.ClaimTestInternal(
		auth,
		expectedClaimBytes,
		false,
	)
	require.NoError(t, err)

	r, err = waitForReceipt(ctx, client, tx.Hash(), 20)
	require.NoError(t, err)
	testCases = append(testCases, testCase{
		description:   "indirect + indirect call to claim message bytes",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[0],
		expectedClaim: expectedClaim,
	})

	reverted = [2]bool{false, false}

	// 2 indirect + indirect call claim message (same global index)
	expectedClaim.IsMessage = true
	expectedClaim.GlobalIndex = big.NewInt(427)
	expectedClaim2.IsMessage = true
	expectedClaim2.GlobalIndex = big.NewInt(427)
	expectedClaimBytes, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes2, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim2, proofLocal, proofRollup)
	require.NoError(t, err)
	tx, err = claimTest.Claim2TestInternal(
		auth,
		expectedClaimBytes,
		expectedClaimBytes2,
		reverted,
	)
	require.NoError(t, err)

	r, err = waitForReceipt(ctx, client, tx.Hash(), 20)
	require.NoError(t, err)
	testCases = append(testCases, testCase{
		description:   "2 indirect + indirect call claim message 1 (same globalIndex)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[0],
		expectedClaim: expectedClaim,
	})
	testCases = append(testCases, testCase{
		description:   "2 indirect + indirect call claim message 2 (same globalIndex)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[1],
		expectedClaim: expectedClaim2,
	})

	reverted3 := [3]bool{false, false, false}

	// 3 ok (indirectx2, indirect, indirectx2) call claim message (same global index)
	expectedClaim.IsMessage = true
	expectedClaim.GlobalIndex = big.NewInt(427)
	expectedClaim2.IsMessage = true
	expectedClaim2.GlobalIndex = big.NewInt(427)
	expectedClaim3.IsMessage = true
	expectedClaim3.GlobalIndex = big.NewInt(427)
	expectedClaim3.FromAddress = claimCallerAddr
	expectedClaimBytes, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes2, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim2, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes3, err := encodeClaimCalldata(abi, "claimMessage", expectedClaim3, proofLocal, proofRollup)
	require.NoError(t, err)
	tx, err = claimTest.Claim3TestInternal(
		auth,
		expectedClaimBytes,
		expectedClaimBytes2,
		expectedClaimBytes3,
		reverted3,
	)
	require.NoError(t, err)

	r, err = waitForReceipt(ctx, client, tx.Hash(), 20)
	require.NoError(t, err)
	testCases = append(testCases, testCase{
		description:   "3 ok (indirectx2, indirect, indirectx2) call claim message 1 (same globalIndex)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[0],
		expectedClaim: expectedClaim,
	})
	testCases = append(testCases, testCase{
		description:   "3 ok (indirectx2, indirect, indirectx2) call claim message 2 (same globalIndex)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[1],
		expectedClaim: expectedClaim2,
	})
	testCases = append(testCases, testCase{
		description:   "3 ok (indirectx2, indirect, indirectx2) call claim message 3 (same globalIndex)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[2],
		expectedClaim: expectedClaim3,
	})

	// 3 ok (indirectx2, indirect, indirectx2) call claim message (diff global index)
	expectedClaim.IsMessage = true
	expectedClaim.GlobalIndex = big.NewInt(427)
	expectedClaim2.IsMessage = true
	expectedClaim2.GlobalIndex = big.NewInt(428)
	expectedClaim2.FromAddress = claimTestAddr
	expectedClaim3.IsMessage = true
	expectedClaim3.GlobalIndex = big.NewInt(429)
	expectedClaim3.FromAddress = claimCallerAddr
	expectedClaimBytes, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes2, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim2, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes3, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim3, proofLocal, proofRollup)
	require.NoError(t, err)
	tx, err = claimTest.Claim3TestInternal(
		auth,
		expectedClaimBytes,
		expectedClaimBytes2,
		expectedClaimBytes3,
		reverted3,
	)
	require.NoError(t, err)

	r, err = waitForReceipt(ctx, client, tx.Hash(), 20)
	require.NoError(t, err)
	testCases = append(testCases, testCase{
		description:   "3 ok (indirectx2, indirect, indirectx2) call claim message 1 (diff globalIndex)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[0],
		expectedClaim: expectedClaim,
	})
	testCases = append(testCases, testCase{
		description:   "3 ok (indirectx2, indirect, indirectx2) call claim message 2 (diff globalIndex)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[1],
		expectedClaim: expectedClaim2,
	})
	testCases = append(testCases, testCase{
		description:   "3 ok (indirectx2, indirect, indirectx2) call claim message 3 (diff globalIndex)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[2],
		expectedClaim: expectedClaim3,
	})

	reverted3 = [3]bool{true, false, false}

	// 1 ko 2 ok (indirectx2, indirect, indirectx2) call claim message (diff global index)
	expectedClaim.IsMessage = true
	expectedClaim.GlobalIndex = big.NewInt(427)
	expectedClaim2.IsMessage = true
	expectedClaim2.GlobalIndex = big.NewInt(428)
	expectedClaim2.FromAddress = claimTestAddr
	expectedClaim3.IsMessage = true
	expectedClaim3.GlobalIndex = big.NewInt(429)
	expectedClaimBytes, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes2, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim2, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes3, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim3, proofLocal, proofRollup)
	require.NoError(t, err)
	tx, err = claimTest.Claim3TestInternal(
		auth,
		expectedClaimBytes,
		expectedClaimBytes2,
		expectedClaimBytes3,
		reverted3,
	)
	require.NoError(t, err)

	r, err = waitForReceipt(ctx, client, tx.Hash(), 20)
	require.NoError(t, err)
	testCases = append(testCases, testCase{
		description:   "1 ko 2 ok (indirectx2, indirect, indirectx2) call claim message 1 (diff globalIndex)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[0],
		expectedClaim: expectedClaim2,
	})
	testCases = append(testCases, testCase{
		description:   "1 ko 2 ok (indirectx2, indirect, indirectx2) call claim message 2 (diff globalIndex)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[1],
		expectedClaim: expectedClaim3,
	})

	reverted3 = [3]bool{false, true, false}

	// 1 ok 1 ko 1 ok (indirectx2, indirect, indirectx2) call claim message (diff global index)
	expectedClaim.IsMessage = true
	expectedClaim.GlobalIndex = big.NewInt(427)
	expectedClaim2.IsMessage = true
	expectedClaim2.GlobalIndex = big.NewInt(428)
	// expectedClaim2.FromAddress = auth.From
	expectedClaim3.IsMessage = true
	expectedClaim3.GlobalIndex = big.NewInt(429)
	// expectedClaim3.FromAddress = auth.From
	expectedClaimBytes, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes2, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim2, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes3, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim3, proofLocal, proofRollup)
	require.NoError(t, err)
	tx, err = claimTest.Claim3TestInternal(
		auth,
		expectedClaimBytes,
		expectedClaimBytes2,
		expectedClaimBytes3,
		reverted3,
	)
	require.NoError(t, err)

	r, err = waitForReceipt(ctx, client, tx.Hash(), 20)
	require.NoError(t, err)
	testCases = append(testCases, testCase{
		description:   "1 ko 2 ok (indirectx2, indirect, indirectx2) call claim message 1 (diff globalIndex)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[0],
		expectedClaim: expectedClaim,
	})
	testCases = append(testCases, testCase{
		description:   "1 ko 2 ok (indirectx2, indirect, indirectx2) call claim message 2 (diff globalIndex)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[1],
		expectedClaim: expectedClaim3,
	})

	reverted3 = [3]bool{false, false, true}

	// 1 ok 1 ok 1 ko (indirectx2, indirect, indirectx2) call claim message (diff global index)
	expectedClaim.IsMessage = true
	expectedClaim.GlobalIndex = big.NewInt(427)
	expectedClaim2.IsMessage = true
	expectedClaim2.GlobalIndex = big.NewInt(428)
	expectedClaim2.FromAddress = claimTestAddr
	expectedClaim3.IsMessage = true
	expectedClaim3.GlobalIndex = big.NewInt(429)
	expectedClaimBytes, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes2, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim2, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes3, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim3, proofLocal, proofRollup)
	require.NoError(t, err)
	tx, err = claimTest.Claim3TestInternal(
		auth,
		expectedClaimBytes,
		expectedClaimBytes2,
		expectedClaimBytes3,
		reverted3,
	)
	require.NoError(t, err)

	r, err = waitForReceipt(ctx, client, tx.Hash(), 20)
	require.NoError(t, err)
	testCases = append(testCases, testCase{
		description:   "1 ok 1 ok 1 ko (indirectx2, indirect, indirectx2) call claim message 1 (diff globalIndex)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[0],
		expectedClaim: expectedClaim,
	})
	testCases = append(testCases, testCase{
		description:   "1 ok 1 ok 1 ko (indirectx2, indirect, indirectx2) call claim message 2 (diff globalIndex)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[1],
		expectedClaim: expectedClaim2,
	})

	reverted3 = [3]bool{true, false, false}

	// 1 ko 2 ok (indirectx2, indirect, indirectx2) call claim message (same global index)
	expectedClaim.IsMessage = true
	expectedClaim.GlobalIndex = big.NewInt(427)
	expectedClaim2.IsMessage = true
	expectedClaim2.GlobalIndex = big.NewInt(427)
	expectedClaim2.FromAddress = claimCallerAddr
	expectedClaim3.IsMessage = true
	expectedClaim3.GlobalIndex = big.NewInt(427)
	expectedClaimBytes, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes2, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim2, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes3, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim3, proofLocal, proofRollup)
	require.NoError(t, err)
	tx, err = claimTest.Claim3TestInternal(
		auth,
		expectedClaimBytes,
		expectedClaimBytes2,
		expectedClaimBytes3,
		reverted3,
	)
	require.NoError(t, err)

	r, err = waitForReceipt(ctx, client, tx.Hash(), 20)
	require.NoError(t, err)
	testCases = append(testCases, testCase{
		description:   "1 ko 2 ok (indirectx2, indirect, indirectx2) call claim message 1 (same globalIndex)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[0],
		expectedClaim: expectedClaim2,
	})
	testCases = append(testCases, testCase{
		description:   "1 ko 2 ok (indirectx2, indirect, indirectx2) call claim message 2 (same globalIndex)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[1],
		expectedClaim: expectedClaim3,
	})

	reverted3 = [3]bool{false, true, false}

	// 1 ok 1 ko 1 ok (indirectx2, indirect, indirectx2) call claim message (same global index)
	expectedClaim.IsMessage = true
	expectedClaim.GlobalIndex = big.NewInt(427)
	expectedClaim2.IsMessage = true
	expectedClaim2.GlobalIndex = big.NewInt(427)
	expectedClaim3.IsMessage = true
	expectedClaim3.GlobalIndex = big.NewInt(427)
	expectedClaimBytes, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes2, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim2, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes3, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim3, proofLocal, proofRollup)
	require.NoError(t, err)
	tx, err = claimTest.Claim3TestInternal(
		auth,
		expectedClaimBytes,
		expectedClaimBytes2,
		expectedClaimBytes3,
		reverted3,
	)
	require.NoError(t, err)

	r, err = waitForReceipt(ctx, client, tx.Hash(), 20)
	require.NoError(t, err)
	testCases = append(testCases, testCase{
		description:   "1 ko 2 ok (indirectx2, indirect, indirectx2) call claim message 1 (same globalIndex)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[0],
		expectedClaim: expectedClaim,
	})
	testCases = append(testCases, testCase{
		description:   "1 ko 2 ok (indirectx2, indirect, indirectx2) call claim message 2 (same globalIndex)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[1],
		expectedClaim: expectedClaim3,
	})

	reverted3 = [3]bool{false, false, true}

	// 1 ok 1 ok 1 ko (indirectx2, indirect, indirectx2) call claim message (same global index)
	expectedClaim.IsMessage = true
	expectedClaim.GlobalIndex = big.NewInt(427)
	expectedClaim.FromAddress = claimTestAddr
	expectedClaim2.IsMessage = true
	expectedClaim2.GlobalIndex = big.NewInt(427)
	expectedClaim2.FromAddress = claimTestAddr
	expectedClaim3.IsMessage = true
	expectedClaim3.GlobalIndex = big.NewInt(427)
	expectedClaimBytes, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes2, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim2, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes3, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim3, proofLocal, proofRollup)
	require.NoError(t, err)
	tx, err = claimTest.Claim3TestInternal(
		auth,
		expectedClaimBytes,
		expectedClaimBytes2,
		expectedClaimBytes3,
		reverted3,
	)
	require.NoError(t, err)

	r, err = waitForReceipt(ctx, client, tx.Hash(), 20)
	require.NoError(t, err)
	testCases = append(testCases, testCase{
		description:   "1 ok 1 ok 1 ko (indirectx2, indirect, indirectx2) call claim message 1 (same globalIndex)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[0],
		expectedClaim: expectedClaim,
	})
	testCases = append(testCases, testCase{
		description:   "1 ok 1 ok 1 ko (indirectx2, indirect, indirectx2) call claim message 2 (same globalIndex)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[1],
		expectedClaim: expectedClaim2,
	})

	reverted3 = [3]bool{true, true, false}

	// 2 ko 1 ok (indirectx2, indirect, indirectx2) call claim message (same global index)
	expectedClaim.IsMessage = true
	expectedClaim.GlobalIndex = big.NewInt(427)
	expectedClaim2.IsMessage = true
	expectedClaim2.GlobalIndex = big.NewInt(427)
	expectedClaim3.IsMessage = true
	expectedClaim3.GlobalIndex = big.NewInt(427)
	expectedClaimBytes, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes2, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim2, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes3, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim3, proofLocal, proofRollup)
	require.NoError(t, err)
	tx, err = claimTest.Claim3TestInternal(
		auth,
		expectedClaimBytes,
		expectedClaimBytes2,
		expectedClaimBytes3,
		reverted3,
	)
	require.NoError(t, err)

	r, err = waitForReceipt(ctx, client, tx.Hash(), 20)
	require.NoError(t, err)
	testCases = append(testCases, testCase{
		description:   "2 ko 1 ok (indirectx2, indirect, indirectx2) call claim message 1 (same globalIndex)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[0],
		expectedClaim: expectedClaim3,
	})

	reverted3 = [3]bool{false, true, true}

	// 1 ok 2 ko (indirectx2, indirect, indirectx2) call claim message (same global index)
	expectedClaim.IsMessage = true
	expectedClaim.GlobalIndex = big.NewInt(427)
	expectedClaim.FromAddress = claimCallerAddr
	expectedClaim2.IsMessage = true
	expectedClaim2.GlobalIndex = big.NewInt(427)
	expectedClaim3.IsMessage = true
	expectedClaim3.GlobalIndex = big.NewInt(427)
	expectedClaimBytes, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes2, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim2, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes3, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim3, proofLocal, proofRollup)
	require.NoError(t, err)
	tx, err = claimTest.Claim3TestInternal(
		auth,
		expectedClaimBytes,
		expectedClaimBytes2,
		expectedClaimBytes3,
		reverted3,
	)
	require.NoError(t, err)

	r, err = waitForReceipt(ctx, client, tx.Hash(), 20)
	require.NoError(t, err)
	testCases = append(testCases, testCase{
		description:   "1 ok 2 ko (indirectx2, indirect, indirectx2) call claim message 1 (same globalIndex)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[0],
		expectedClaim: expectedClaim,
	})

	reverted3 = [3]bool{true, false, true}

	// 1 ko 1 ok 1 ko (indirectx2, indirect, indirectx2) call claim message (same global index)
	expectedClaim.IsMessage = true
	expectedClaim.GlobalIndex = big.NewInt(427)
	expectedClaim2.IsMessage = true
	expectedClaim2.GlobalIndex = big.NewInt(427)
	expectedClaim2.FromAddress = claimTestAddr
	expectedClaim3.IsMessage = true
	expectedClaim3.GlobalIndex = big.NewInt(427)
	expectedClaimBytes, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes2, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim2, proofLocal, proofRollup)
	require.NoError(t, err)
	expectedClaimBytes3, err = encodeClaimCalldata(abi, "claimMessage", expectedClaim3, proofLocal, proofRollup)
	require.NoError(t, err)
	tx, err = claimTest.Claim3TestInternal(
		auth,
		expectedClaimBytes,
		expectedClaimBytes2,
		expectedClaimBytes3,
		reverted3,
	)
	require.NoError(t, err)

	r, err = waitForReceipt(ctx, client, tx.Hash(), 20)
	require.NoError(t, err)
	testCases = append(testCases, testCase{
		description:   "1 ko 1 ok 1 ko (indirectx2, indirect, indirectx2) call claim message 1 (same globalIndex)",
		bridgeAddr:    bridgeAddr,
		log:           *r.Logs[0],
		expectedClaim: expectedClaim2,
	})

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			claimEvent, err := bridgeContract.ParseClaimEvent(tc.log)
			require.NoError(t, err)
			actualClaim := Claim{
				GlobalIndex:        claimEvent.GlobalIndex,
				OriginNetwork:      claimEvent.OriginNetwork,
				OriginAddress:      claimEvent.OriginAddress,
				DestinationAddress: claimEvent.DestinationAddress,
				Amount:             claimEvent.Amount,
			}
			logger := log.WithFields("module", "test")
			err = actualClaim.setClaimCalldata(client.Client(), bridgeAddr, tc.log.TxHash, logger)
			require.NoError(t, err)
			require.Equal(t, tc.expectedClaim, actualClaim)
			require.Equal(t, tc.expectedClaim.FromAddress, actualClaim.FromAddress)
		})
	}
}

// encodeClaimCalldata encodes the calldata for either claimMessage or claimAsset functions
func encodeClaimCalldata(claimMockABI *abi.ABI, funcName string,
	claim Claim, proofLocal, proofRollup [tree.DefaultHeight][common.HashLength]byte) ([]byte, error) {
	return claimMockABI.Pack(funcName, proofLocal, proofRollup,
		claim.GlobalIndex, claim.MainnetExitRoot, claim.RollupExitRoot,
		claim.OriginNetwork, claim.OriginAddress,
		claim.DestinationNetwork, claim.DestinationAddress,
		claim.Amount, claim.Metadata)
}
