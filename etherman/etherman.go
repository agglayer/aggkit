package etherman

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/etherman/contracts"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

type ethereumClient interface {
	ethereum.ChainReader
	ethereum.ChainStateReader
	ethereum.ContractCaller
	ethereum.GasEstimator
	ethereum.GasPricer
	ethereum.GasPricer1559
	ethereum.LogFilterer
	ethereum.TransactionReader
	ethereum.TransactionSender

	bind.DeployBackend
}

// L1Config represents the configuration of the network used in L1
type L1Config struct {
	// Chain ID of the L1 network
	L1ChainID uint64 `json:"chainId" mapstructure:"ChainID"`
	// ZkEVMAddr Address of the L1 contract polygonZkEVMAddress
	ZkEVMAddr common.Address `json:"polygonZkEVMAddress" mapstructure:"ZkEVMAddr"`
	// RollupManagerAddr Address of the L1 contract
	RollupManagerAddr common.Address `json:"polygonRollupManagerAddress" mapstructure:"RollupManagerAddr"`
	// PolAddr Address of the L1 Pol token Contract
	PolAddr common.Address `json:"polTokenAddress" mapstructure:"PolAddr"`
	// GlobalExitRootManagerAddr Address of the L1 GlobalExitRootManager contract
	GlobalExitRootManagerAddr common.Address `json:"polygonZkEVMGlobalExitRootAddress" mapstructure:"GlobalExitRootManagerAddr"` //nolint:lll
}

// Client is a simple implementation of EtherMan.
type Client struct {
	EthClient ethereumClient

	Contracts *contracts.Contracts
	RollupID  uint32

	l1Cfg config.L1Config
	cfg   config.Config
	auth  map[common.Address]bind.TransactOpts // empty in case of read-only client
}

// NewClient creates a new etherman.
func NewClient(cfg config.Config, l1Config config.L1Config, commonConfig aggkitcommon.Config) (*Client, error) {
	// Connect to ethereum node
	ethClient, err := ethclient.Dial(cfg.EthermanConfig.URL)
	if err != nil {
		log.Errorf("error connecting to %s: %+v", cfg.EthermanConfig.URL, err)

		return nil, err
	}
	L1chainID, err := ethClient.ChainID(context.Background())
	if err != nil {
		log.Errorf("error getting L1chainID from %s: %+v", cfg.EthermanConfig.URL, err)

		return nil, err
	}
	log.Infof("L1ChainID: %d", L1chainID.Uint64())
	contracts, err := contracts.NewContracts(l1Config, ethClient)
	if err != nil {
		return nil, err
	}
	log.Info(contracts.String())
	// Get RollupID
	rollupID, err := contracts.Banana.RollupManager.RollupAddressToID(&bind.CallOpts{Pending: false}, l1Config.ZkEVMAddr)
	if err != nil {
		log.Errorf("error getting rollupID from %s : %+v", contracts.Banana.RollupManager.String(), err)

		return nil, err
	}
	if rollupID == 0 {
		return nil, errors.New(
			"rollupID is 0, is not a valid value. Check that rollup Address is correct " +
				l1Config.ZkEVMAddr.String(),
		)
	}
	log.Infof("rollupID: %d (obtenied from SMC: %s )", rollupID, contracts.Banana.RollupManager.String())

	client := &Client{
		EthClient: ethClient,
		Contracts: contracts,

		RollupID: rollupID,
		l1Cfg:    l1Config,
		cfg:      cfg,
		auth:     map[common.Address]bind.TransactOpts{},
	}

	return client, nil
}

func GetRollupID(l1Config config.L1Config, rollupAddr common.Address, ethClient bind.ContractBackend) (uint32, error) {
	contracts, err := contracts.NewContracts(l1Config, ethClient)
	if err != nil {
		return 0, fmt.Errorf("error creating contracts. Err: %w", err)
	}
	rollupID, err := contracts.Banana.RollupManager.RollupAddressToID(&bind.CallOpts{Pending: false}, rollupAddr)
	if err != nil {
		log.Errorf("error getting rollupID from %s: %v", contracts.Banana.RollupManager.String(), err)

		return 0, fmt.Errorf("error calling contract RollupManager.RollupAddressToID(%s). Err: %w", rollupAddr.String(), err)
	}
	log.Infof("rollupID: %d (obtained from contract: %s )", rollupID, contracts.Banana.RollupManager.String())

	return rollupID, nil
}

// HeaderByNumber returns a block header from the current canonical chain. If number is
// nil, the latest known header is returned.
func (c *Client) HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error) {
	return c.EthClient.HeaderByNumber(ctx, number)
}

// BlockByNumber function retrieves the ethereum block information by ethereum block number.
func (c *Client) BlockByNumber(ctx context.Context, blockNumber uint64) (*types.Block, error) {
	block, err := c.EthClient.BlockByNumber(ctx, new(big.Int).SetUint64(blockNumber))
	if err != nil {
		if errors.Is(err, ethereum.NotFound) || err.Error() == "block does not exist in blockchain" {
			return nil, ErrNotFound
		}

		return nil, err
	}

	return block, nil
}

// GetLatestBatchNumber function allows to retrieve the latest proposed batch in the smc
func (c *Client) GetLatestBatchNumber() (uint64, error) {
	rollupData, err := c.Contracts.Banana.RollupManager.RollupIDToRollupData(
		&bind.CallOpts{Pending: false},
		c.RollupID,
	)
	if err != nil {
		return 0, err
	}

	return rollupData.LastBatchSequenced, nil
}

// GetLatestBlockNumber gets the latest block number from the ethereum
func (c *Client) GetLatestBlockNumber(ctx context.Context) (uint64, error) {
	return c.getBlockNumber(ctx, rpc.LatestBlockNumber)
}

// GetSafeBlockNumber gets the safe block number from the ethereum
func (c *Client) GetSafeBlockNumber(ctx context.Context) (uint64, error) {
	return c.getBlockNumber(ctx, rpc.SafeBlockNumber)
}

// GetFinalizedBlockNumber gets the Finalized block number from the ethereum
func (c *Client) GetFinalizedBlockNumber(ctx context.Context) (uint64, error) {
	return c.getBlockNumber(ctx, rpc.FinalizedBlockNumber)
}

// getBlockNumber gets the block header by the provided block number from the ethereum
func (c *Client) getBlockNumber(ctx context.Context, blockNumber rpc.BlockNumber) (uint64, error) {
	header, err := c.EthClient.HeaderByNumber(ctx, big.NewInt(int64(blockNumber)))
	if err != nil || header == nil {
		return 0, err
	}

	return header.Number.Uint64(), nil
}

// GetLatestBlockTimestamp gets the latest block timestamp from the ethereum
func (c *Client) GetLatestBlockTimestamp(ctx context.Context) (uint64, error) {
	header, err := c.EthClient.HeaderByNumber(ctx, nil)
	if err != nil || header == nil {
		return 0, err
	}

	return header.Time, nil
}

// GetLatestVerifiedBatchNum gets latest verified batch from ethereum
func (c *Client) GetLatestVerifiedBatchNum() (uint64, error) {
	rollupData, err := c.Contracts.Banana.RollupManager.RollupIDToRollupData(
		&bind.CallOpts{Pending: false},
		c.RollupID,
	)
	if err != nil {
		return 0, err
	}

	return rollupData.LastVerifiedBatch, nil
}

// GetTx function get ethereum tx
func (c *Client) GetTx(ctx context.Context, txHash common.Hash) (*types.Transaction, bool, error) {
	return c.EthClient.TransactionByHash(ctx, txHash)
}

// GetTxReceipt function gets ethereum tx receipt
func (c *Client) GetTxReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
	return c.EthClient.TransactionReceipt(ctx, txHash)
}

// GetL2ChainID returns L2 Chain ID
func (c *Client) GetL2ChainID() (uint64, error) {
	rollupData, err := c.Contracts.Banana.RollupManager.RollupIDToRollupData(
		&bind.CallOpts{Pending: false},
		c.RollupID,
	)
	log.Debug("chainID read from rollupManager: ", rollupData.ChainID)
	if err != nil {
		log.Debug("error from rollupManager: ", err)

		return 0, err
	} else if rollupData.ChainID == 0 {
		return rollupData.ChainID, fmt.Errorf("error: chainID received is 0")
	}

	return rollupData.ChainID, nil
}

// SendTx sends a tx to L1
func (c *Client) SendTx(ctx context.Context, tx *types.Transaction) error {
	return c.EthClient.SendTransaction(ctx, tx)
}

// CurrentNonce returns the current nonce for the provided account
func (c *Client) CurrentNonce(ctx context.Context, account common.Address) (uint64, error) {
	return c.EthClient.NonceAt(ctx, account, nil)
}

// EstimateGas returns the estimated gas for the tx
func (c *Client) EstimateGas(
	ctx context.Context, from common.Address, to *common.Address, value *big.Int, data []byte,
) (uint64, error) {
	return c.EthClient.EstimateGas(ctx, ethereum.CallMsg{
		From:  from,
		To:    to,
		Value: value,
		Data:  data,
	})
}

// CheckTxWasMined check if a tx was already mined
func (c *Client) CheckTxWasMined(ctx context.Context, txHash common.Hash) (bool, *types.Receipt, error) {
	receipt, err := c.EthClient.TransactionReceipt(ctx, txHash)
	if errors.Is(err, ethereum.NotFound) {
		return false, nil, nil
	} else if err != nil {
		return false, nil, err
	}

	return true, receipt, nil
}

// SignTx tries to sign a transaction accordingly to the provided sender
func (c *Client) SignTx(
	ctx context.Context, sender common.Address, tx *types.Transaction,
) (*types.Transaction, error) {
	auth, err := c.getAuthByAddress(sender)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrPrivateKeyNotFound
	}
	signedTx, err := auth.Signer(auth.From, tx)
	if err != nil {
		return nil, err
	}

	return signedTx, nil
}

// AddOrReplaceAuth adds an authorization or replace an existent one to the same account
func (c *Client) AddOrReplaceAuth(auth bind.TransactOpts) error {
	log.Infof("added or replaced authorization for address: %v", auth.From.String())
	c.auth[auth.From] = auth

	return nil
}

// LoadAuthFromKeyStore loads an authorization from a key store file
func (c *Client) LoadAuthFromKeyStore(path, password string) (*bind.TransactOpts, *ecdsa.PrivateKey, error) {
	auth, pk, err := newAuthFromKeystore(path, password, c.l1Cfg.L1ChainID)
	if err != nil {
		return nil, nil, err
	}

	log.Infof("loaded authorization for address: %v", auth.From.String())
	c.auth[auth.From] = auth

	return &auth, pk, nil
}

// newKeyFromKeystore creates an instance of a keystore key from a keystore file
func newKeyFromKeystore(path, password string) (*keystore.Key, error) {
	if path == "" && password == "" {
		return nil, nil
	}
	keystoreEncrypted, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	log.Infof("decrypting key from: %v", path)
	key, err := keystore.DecryptKey(keystoreEncrypted, password)
	if err != nil {
		return nil, err
	}

	return key, nil
}

// newAuthFromKeystore an authorization instance from a keystore file
func newAuthFromKeystore(path, password string, chainID uint64) (bind.TransactOpts, *ecdsa.PrivateKey, error) {
	log.Infof("reading key from: %v", path)
	key, err := newKeyFromKeystore(path, password)
	if err != nil {
		return bind.TransactOpts{}, nil, err
	}
	if key == nil {
		return bind.TransactOpts{}, nil, nil
	}
	auth, err := bind.NewKeyedTransactorWithChainID(key.PrivateKey, new(big.Int).SetUint64(chainID))
	if err != nil {
		return bind.TransactOpts{}, nil, err
	}

	return *auth, key.PrivateKey, nil
}

// getAuthByAddress tries to get an authorization from the authorizations map
func (c *Client) getAuthByAddress(addr common.Address) (bind.TransactOpts, error) {
	auth, found := c.auth[addr]
	if !found {
		return bind.TransactOpts{}, ErrNotFound
	}

	return auth, nil
}

// GetLatestBlockHeader gets the latest block header from the ethereum
func (c *Client) GetLatestBlockHeader(ctx context.Context) (*types.Header, error) {
	header, err := c.EthClient.HeaderByNumber(ctx, big.NewInt(int64(rpc.LatestBlockNumber)))
	if err != nil || header == nil {
		return nil, err
	}

	return header, nil
}

// GetL1InfoRoot gets the L1 info root from the SC
func (c *Client) GetL1InfoRoot(indexL1InfoRoot uint32) (common.Hash, error) {
	// Get lastL1InfoTreeRoot (if index==0 then root=0, no call is needed)
	var (
		lastL1InfoTreeRoot common.Hash
		err                error
	)

	if indexL1InfoRoot > 0 {
		lastL1InfoTreeRoot, err = c.Contracts.Banana.GlobalExitRoot.L1InfoRootMap(
			&bind.CallOpts{Pending: false},
			indexL1InfoRoot,
		)
		if err != nil {
			log.Errorf("error calling SC globalexitroot L1InfoLeafMap: %v", err)
		}
	}

	return lastL1InfoTreeRoot, err
}
