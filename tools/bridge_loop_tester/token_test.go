package bridgelooptester_test

import (
	"context"
	"math/big"
	"testing"

	"github.com/agglayer/aggkit/test/contracts/mintableerc20"
	bridgelooptester "github.com/agglayer/aggkit/tools/bridge_loop_tester"
	"github.com/agglayer/aggkit/tools/bridge_loop_tester/mocks"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// tokenABI returns the mintableerc20 ABI, used to unpack the calldata the Token wrapper produced.
func tokenABI(t *testing.T) *abi.ABI {
	t.Helper()

	parsed, err := mintableerc20.Mintableerc20MetaData.GetAbi()
	require.NoError(t, err)

	return parsed
}

// newMockedToken builds a Token over a mocked NetworkClient.
func newMockedToken(t *testing.T) (bridgelooptester.Token, *mocks.NetworkClient, *mocks.EthBackend) {
	t.Helper()

	backend := mocks.NewEthBackend(t)
	client := mocks.NewNetworkClient(t)
	client.EXPECT().Backend().Return(backend).Maybe()
	client.EXPECT().Name().Return("L2A").Maybe()

	token, err := bridgelooptester.NewToken(client, testTokenAddr)
	require.NoError(t, err)

	return token, client, backend
}

func TestNewTokenValidation(t *testing.T) {
	t.Parallel()

	_, err := bridgelooptester.NewToken(nil, testTokenAddr)
	require.ErrorContains(t, err, "new token: network client is required")

	client := mocks.NewNetworkClient(t)
	client.EXPECT().Name().Return("L2A").Maybe()
	_, err = bridgelooptester.NewToken(client, common.Address{})
	require.ErrorContains(t, err, "token address must not be the zero address")
}

// TestDeployTokenGoesThroughTheSerializedSender checks the deployment is a plain TxRequest with no
// recipient, so it shares this signer's serialized nonce sequence, and that the resulting Token is
// bound to the address the receipt reports.
func TestDeployTokenGoesThroughTheSerializedSender(t *testing.T) {
	t.Parallel()

	backend := mocks.NewEthBackend(t)
	client := mocks.NewNetworkClient(t)
	client.EXPECT().Backend().Return(backend).Maybe()
	client.EXPECT().Name().Return("L2A").Maybe()

	deployed := common.HexToAddress("0x6666666666666666666666666666666666666666")

	var sent bridgelooptester.TxRequest
	client.EXPECT().
		SendTx(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, req bridgelooptester.TxRequest) (*ethtypes.Receipt, error) {
			sent = req

			return &ethtypes.Receipt{
				Status:          ethtypes.ReceiptStatusSuccessful,
				TxHash:          common.HexToHash("0xfeed"),
				ContractAddress: deployed,
			}, nil
		}).Once()

	token, deployTxHash, err := bridgelooptester.DeployToken(context.Background(), client, "Bridge Loop Token", "BLT")
	require.NoError(t, err)
	require.Equal(t, deployed, token.Address())
	require.Equal(t, common.HexToHash("0xfeed"), deployTxHash)

	require.Equal(t, "deploy mintableerc20", sent.Label)
	require.Nil(t, sent.To, "a deployment must have no recipient")

	creationCode := common.FromHex(mintableerc20.Mintableerc20MetaData.Bin)
	require.Greater(t, len(sent.Data), len(creationCode), "constructor arguments must be appended")
	require.Equal(t, creationCode, sent.Data[:len(creationCode)])

	args, err := tokenABI(t).Constructor.Inputs.Unpack(sent.Data[len(creationCode):])
	require.NoError(t, err)
	require.Equal(t, []any{"Bridge Loop Token", "BLT"}, args)
}

func TestDeployTokenWithoutContractAddress(t *testing.T) {
	t.Parallel()

	client := mocks.NewNetworkClient(t)
	client.EXPECT().Name().Return("L2A").Maybe()
	client.EXPECT().
		SendTx(mock.Anything, mock.Anything).
		Return(&ethtypes.Receipt{
			Status: ethtypes.ReceiptStatusSuccessful,
			TxHash: common.HexToHash("0xfeed"),
		}, nil).Once()

	_, _, err := bridgelooptester.DeployToken(context.Background(), client, "T", "T")
	require.ErrorContains(t, err, "was mined without a contract address")

	_, _, err = bridgelooptester.DeployToken(context.Background(), nil, "T", "T")
	require.ErrorContains(t, err, "deploy token: network client is required")
}

// TestTokenWrites pins the mint/approve calldata and that both go through the serialized sender.
func TestTokenWrites(t *testing.T) {
	t.Parallel()

	token, client, _ := newMockedToken(t)

	var sent []bridgelooptester.TxRequest
	client.EXPECT().
		SendTx(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, req bridgelooptester.TxRequest) (*ethtypes.Receipt, error) {
			sent = append(sent, req)

			return &ethtypes.Receipt{Status: ethtypes.ReceiptStatusSuccessful}, nil
		}).Twice()

	amount := big.NewInt(1_000_000)

	_, err := token.Mint(context.Background(), testDestAddr, amount)
	require.NoError(t, err)
	_, err = token.Approve(context.Background(), testBridgeAddr, amount)
	require.NoError(t, err)

	require.Len(t, sent, 2)

	require.Equal(t, "erc20 mint", sent[0].Label)
	require.Equal(t, &testTokenAddr, sent[0].To)
	mintArgs := unpackCalldata(t, tokenABI(t), "mint", sent[0].Data)
	require.Equal(t, []any{testDestAddr, amount}, mintArgs)

	require.Equal(t, "erc20 approve", sent[1].Label)
	approveArgs := unpackCalldata(t, tokenABI(t), "approve", sent[1].Data)
	require.Equal(t, []any{testBridgeAddr, amount}, approveArgs)
}

// TestTokenReadsUseNilAmountSafely checks a nil amount is treated as zero rather than panicking
// inside abi.Pack.
func TestTokenNilAmountIsZero(t *testing.T) {
	t.Parallel()

	token, client, _ := newMockedToken(t)

	var sent bridgelooptester.TxRequest
	client.EXPECT().
		SendTx(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, req bridgelooptester.TxRequest) (*ethtypes.Receipt, error) {
			sent = req

			return &ethtypes.Receipt{Status: ethtypes.ReceiptStatusSuccessful}, nil
		}).Once()

	_, err := token.Approve(context.Background(), testBridgeAddr, nil)
	require.NoError(t, err)

	args := unpackCalldata(t, tokenABI(t), "approve", sent.Data)
	amount, ok := args[1].(*big.Int)
	require.True(t, ok)
	require.Zero(t, amount.Sign())
}

// TestTokenReads covers balanceOf and allowance against a mocked eth_call.
func TestTokenReads(t *testing.T) {
	t.Parallel()

	token, _, backend := newMockedToken(t)

	parsed := tokenABI(t)
	balanceOutputs, err := parsed.Methods["balanceOf"].Outputs.Pack(big.NewInt(4_200))
	require.NoError(t, err)
	allowanceOutputs, err := parsed.Methods["allowance"].Outputs.Pack(big.NewInt(7))
	require.NoError(t, err)

	backend.EXPECT().
		CallContract(mock.Anything, mock.MatchedBy(func(call ethereum.CallMsg) bool {
			return len(call.Data) >= abiSelectorLen &&
				string(call.Data[:abiSelectorLen]) == string(parsed.Methods["balanceOf"].ID)
		}), mock.Anything).
		Return(balanceOutputs, nil).Once()
	backend.EXPECT().
		CallContract(mock.Anything, mock.MatchedBy(func(call ethereum.CallMsg) bool {
			return len(call.Data) >= abiSelectorLen &&
				string(call.Data[:abiSelectorLen]) == string(parsed.Methods["allowance"].ID)
		}), mock.Anything).
		Return(allowanceOutputs, nil).Once()

	balance, err := token.BalanceOf(context.Background(), testDestAddr)
	require.NoError(t, err)
	require.Equal(t, big.NewInt(4_200), balance)

	allowance, err := token.Allowance(context.Background(), testDestAddr, testBridgeAddr)
	require.NoError(t, err)
	require.Equal(t, big.NewInt(7), allowance)

	require.Equal(t, testTokenAddr, token.Address())
}
