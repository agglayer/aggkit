package helpers

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayermanager"
	"github.com/agglayer/aggkit/etherman"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/contracts/proxy"
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

	// Nonce values for contract address calculation
	bridgeProxyNonce = 1
)

var _ aggkittypes.EthClienter = (*TestClient)(nil)

type TestClient struct {
	simulated.Client
	aggkittypes.RPCClienter
	aggkittypes.CustomEthereumClienter
	defaultEthClient aggkittypes.EthClienter
}

// TestClientOption defines a function signature for optional parameters.
type TestClientOption func(*TestClient)

// NewTestClient creates a new TestClient with optional configurations.
func NewTestClient(ethClient simulated.Client, opts ...TestClientOption) *TestClient {
	tc := &TestClient{
		Client:           ethClient,
		defaultEthClient: etherman.NewDefaultEthClient(ethClient, nil, nil),
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

func (t *TestClient) CallContext(ctx context.Context, result interface{}, method string, args ...interface{}) error {
	return t.RPCClienter.CallContext(ctx, result, method, args)
}

func (t *TestClient) BatchCallContext(ctx context.Context, b []rpc.BatchElem) error {
	return t.RPCClienter.BatchCallContext(ctx, b)
}

func (t *TestClient) CustomHeaderByNumber(ctx context.Context, number *aggkittypes.BlockNumberFinality) (*aggkittypes.BlockHeader, error) {
	return t.defaultEthClient.CustomHeaderByNumber(ctx, number)
}

// SimulatedBackendSetup defines the setup for a simulated backend.
type SimulatedBackendSetup struct {
	UserAuth            *bind.TransactOpts
	DeployerAuth        *bind.TransactOpts
	BridgeProxyAddr     common.Address
	BridgeProxyContract *agglayerbridge.Agglayerbridge
}

// DeployBridge deploys the bridge contract
func (s *SimulatedBackendSetup) DeployBridge(client *simulated.Backend,
	gerAddr common.Address, networkID uint32) error {
	// Deploy agglayer bridge contract
	bridgeAddr, _, _, err := agglayerbridge.DeployAgglayerbridge(s.DeployerAuth, client.Client())
	if err != nil {
		return err
	}
	client.Commit()

	// Create proxy contract for the bridge
	var (
		bridgeProxyAddr     common.Address
		bridgeProxyContract *agglayerbridge.Agglayerbridge
	)

	bridgeProxyAddr, _, _, err = proxy.DeployProxy(
		s.DeployerAuth,
		client.Client(),
		bridgeAddr,
		s.DeployerAuth.From,
		[]byte{},
	)
	if err != nil {
		return err
	}
	client.Commit()

	bridgeProxyContract, err = agglayerbridge.NewAgglayerbridge(bridgeProxyAddr, client.Client())
	if err != nil {
		return err
	}

	_, err = bridgeProxyContract.Initialize(
		s.UserAuth,
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
	client.Commit()

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

func (s *SimulatedBackendSetup) DeployAgglayerManager(
	client *simulated.Backend, gerAddr, polAddr, bridgeAddr common.Address) (common.Address, *agglayermanager.Agglayermanager, error) {
	// Deploy agglayer manager contract
	agglayerManagerAddr, _, _, err := agglayermanager.DeployAgglayermanager(
		s.DeployerAuth, client.Client(), gerAddr, polAddr, bridgeAddr, s.UserAuth.From)
	if err != nil {
		return common.Address{}, nil, err
	}
	client.Commit()

	// Create proxy contract for the agglayer manager
	var (
		agglayerManagerProxyAddr     common.Address
		agglayerManagerProxyContract *agglayermanager.Agglayermanager
	)

	agglayerManagerProxyAddr, _, _, err = proxy.DeployProxy(
		s.DeployerAuth,
		client.Client(),
		agglayerManagerAddr,
		s.DeployerAuth.From,
		[]byte{},
	)
	if err != nil {
		return common.Address{}, nil, err
	}
	client.Commit()

	agglayerManagerProxyContract, err = agglayermanager.NewAgglayermanager(agglayerManagerProxyAddr, client.Client())
	if err != nil {
		return common.Address{}, nil, err
	}

	_, err = agglayerManagerProxyContract.Initialize(s.UserAuth)
	if err != nil {
		return common.Address{}, nil, err
	}
	client.Commit()

	return agglayerManagerProxyAddr, agglayerManagerProxyContract, nil
}

// NewSimulatedBackend creates a simulated backend with two accounts: user and deployer.
func NewSimulatedBackend(t *testing.T,
	balances map[common.Address]types.Account,
	deployerAuth *bind.TransactOpts) (*simulated.Backend, *SimulatedBackendSetup) {
	t.Helper()

	// Define default balance
	balance, ok := new(big.Int).SetString(defaultBalance, 10)
	require.Truef(t, ok, "failed to set balance")

	// Create user account
	userPK, err := crypto.GenerateKey()
	require.NoError(t, err)
	userAuth, err := bind.NewKeyedTransactorWithChainID(userPK, big.NewInt(chainID))
	require.NoError(t, err)

	// Create deployer account
	precalculatedBridgeAddr := crypto.CreateAddress(deployerAuth.From, bridgeProxyNonce)

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
