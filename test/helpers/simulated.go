package helpers

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/polygonzkevmbridgev2"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/contracts/transparentupgradableproxy"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient/simulated"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/stretchr/testify/require"
)

const (
	defaultBlockGasLimit = uint64(999999999999999999)
	defaultBalance       = "100000000000000000000000000"
	chainID              = 1337

	base10 = 10
)

var _ aggkittypes.EthClienter = (*TestClient)(nil)

type TestClient struct {
	simulated.Client
	aggkittypes.RPCClienter
}

// TestClientOption defines a function signature for optional parameters.
type TestClientOption func(*TestClient)

// NewTestClient creates a new TestClient with optional configurations.
func NewTestClient(ethClient simulated.Client, opts ...TestClientOption) *TestClient {
	tc := &TestClient{
		Client: ethClient,
	}

	// Apply options
	for _, opt := range opts {
		opt(tc)
	}

	return tc
}

// WithRPCClienter sets the optional RPCClienter.
func WithRPCClienter(rpcClient aggkittypes.RPCClienter) TestClientOption {
	return func(tc *TestClient) {
		tc.RPCClienter = rpcClient
	}
}

func (t *TestClient) Call(result any, method string, args ...any) error {
	return t.RPCClienter.Call(result, method, args)
}

// SimulatedBackendSetup defines the setup for a simulated backend.
type SimulatedBackendSetup struct {
	UserAuth            *bind.TransactOpts
	DeployerAuth        *bind.TransactOpts
	BridgeProxyAddr     common.Address
	BridgeProxyContract *polygonzkevmbridgev2.Polygonzkevmbridgev2
}

// DeployBridge deploys the bridge contract
func (s *SimulatedBackendSetup) DeployBridge(client *simulated.Backend,
	gerAddr common.Address, networkID uint32) error {
	// Deploy zkevm bridge contract
	bridgeAddr, _, _, err := polygonzkevmbridgev2.DeployPolygonzkevmbridgev2(s.DeployerAuth, client.Client())
	if err != nil {
		return err
	}
	client.Commit()

	// Create proxy contract for the bridge
	var (
		bridgeProxyAddr     common.Address
		bridgeProxyContract *polygonzkevmbridgev2.Polygonzkevmbridgev2
	)

	bridgeABI, err := polygonzkevmbridgev2.Polygonzkevmbridgev2MetaData.GetAbi()
	if err != nil {
		return err
	}

	dataCallProxy, err := bridgeABI.Pack("initialize",
		networkID,
		common.Address{}, // gasTokenAddressMainnet
		uint32(0),        // gasTokenNetworkMainnet
		gerAddr,          // global exit root manager
		common.Address{}, // rollup manager
		[]byte{},         // gasTokenMetadata
	)
	if err != nil {
		return err
	}

	bridgeProxyAddr, _, _, err = transparentupgradableproxy.DeployTransparentupgradableproxy(
		s.DeployerAuth,
		client.Client(),
		bridgeAddr,
		s.DeployerAuth.From,
		dataCallProxy,
	)
	if err != nil {
		return err
	}
	client.Commit()

	bridgeProxyContract, err = polygonzkevmbridgev2.NewPolygonzkevmbridgev2(bridgeProxyAddr, client.Client())
	if err != nil {
		return err
	}

	actualGERAddr, err := bridgeProxyContract.GlobalExitRootManager(&bind.CallOpts{})
	if err != nil {
		return err
	}

	if gerAddr != actualGERAddr {
		return fmt.Errorf("mismatch between expected %s and actual %s GER addresses on bridge contract (%s)",
			gerAddr, actualGERAddr, bridgeProxyAddr)
	}

	s.BridgeProxyAddr = bridgeProxyAddr
	s.BridgeProxyContract = bridgeProxyContract

	bridgeBalance, err := client.Client().BalanceAt(context.Background(), bridgeProxyAddr, nil)
	if err != nil {
		return err
	}

	log.Debugf("Bridge@%s, balance=%d\n", bridgeProxyAddr, bridgeBalance)

	return nil
}

// NewSimulatedBackend creates a simulated backend with two accounts: user and deployer.
func NewSimulatedBackend(t *testing.T,
	balances map[common.Address]types.Account,
	deployerAuth *bind.TransactOpts) (*simulated.Backend, *SimulatedBackendSetup) {
	t.Helper()

	// Define default balance
	balance, ok := new(big.Int).SetString(defaultBalance, 10) //nolint:mnd
	require.Truef(t, ok, "failed to set balance")

	// Create user account
	userPK, err := crypto.GenerateKey()
	require.NoError(t, err)
	userAuth, err := bind.NewKeyedTransactorWithChainID(userPK, big.NewInt(chainID))
	require.NoError(t, err)

	// Create deployer account
	precalculatedBridgeAddr := crypto.CreateAddress(deployerAuth.From, 1)

	// Define balances map
	if balances == nil {
		balances = make(map[common.Address]types.Account)
	}
	balances[userAuth.From] = types.Account{Balance: balance}
	balances[deployerAuth.From] = types.Account{Balance: balance}
	balances[precalculatedBridgeAddr] = types.Account{Balance: balance}

	client := simulated.NewBackend(balances, simulated.WithBlockGasLimit(defaultBlockGasLimit))

	// Mine the first block
	client.Commit()

	setup := &SimulatedBackendSetup{
		UserAuth:     userAuth,
		DeployerAuth: deployerAuth,
	}

	return client, setup
}

// CreateAccount creates new private key and corresponding transaction signer
func CreateAccount(chainID *big.Int) (*bind.TransactOpts, error) {
	privateKey, err := crypto.GenerateKey()
	if err != nil {
		return nil, err
	}

	return bind.NewKeyedTransactorWithChainID(privateKey, chainID)
}

// ExtractRPCErrorData tries to extract the error data from the provided error
func ExtractRPCErrorData(err error) error {
	var ed rpc.DataError
	if errors.As(err, &ed) {
		if eds, ok := ed.ErrorData().(string); ok {
			return fmt.Errorf("%w (error data: %s)", err, eds)
		}
	}

	return err
}
