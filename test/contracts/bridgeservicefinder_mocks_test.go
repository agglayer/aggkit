package contracts

// This file is a smoke test for the test-only mock contracts under
// test/contracts/rollupmanagermock and test/contracts/aggchainrollupmock, added to support the
// bridgeservicefinder package (see bridgeservicefinder/doc.go). It does NOT exercise any
// bridgeservicefinder code. Its only purpose is to prove that the mocks are selector/event
// compatible with the REAL cdk-contracts-tooling bindings: it deploys each mock on a simulated
// backend, then calls getters and parses emitted events using the REAL agglayermanager,
// aggchainbase and polygonrollupbaseetrog bindings (not the mocks' own generated bindings),
// pointed at the mocks' addresses.

import (
	"context"
	"math/big"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/aggchainbase"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayermanager"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/polygonrollupbaseetrog"
	"github.com/agglayer/aggkit/test/contracts/aggchainrollupmock"
	"github.com/agglayer/aggkit/test/contracts/rollupmanagermock"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient/simulated"
	"github.com/stretchr/testify/require"
)

const (
	smokeTestChainID      = 1337
	smokeTestGasLimit     = uint64(999999999999999999)
	smokeTestRollupID     = uint32(7)
	smokeTestBridgeURL    = "https://bridge-service.example.com:5577"
	smokeTestSequencerURL = "https://sequencer.example.com:8545"
	smokeTestMetadataKey  = "BRIDGE_SERVICE_URL"
)

// newSmokeTestBackend creates a simulated backend with a single funded account, mirroring the
// setup used elsewhere in the repo (see test/helpers/simulated.go) but self-contained so this
// smoke test has no dependency on the bridge/GER deployment helpers, which this test doesn't need.
func newSmokeTestBackend(t *testing.T) (*simulated.Backend, *bind.TransactOpts) {
	t.Helper()

	pk, err := crypto.GenerateKey()
	require.NoError(t, err)

	auth, err := bind.NewKeyedTransactorWithChainID(pk, big.NewInt(smokeTestChainID))
	require.NoError(t, err)

	balance, ok := new(big.Int).SetString("100000000000000000000000000", 10)
	require.True(t, ok)

	backend := simulated.NewBackend(map[common.Address]types.Account{
		auth.From: {Balance: balance},
	}, simulated.WithBlockGasLimit(smokeTestGasLimit))
	backend.Commit()

	return backend, auth
}

// TestRollupManagerMock_CompatibleWithRealAgglayermanagerBinding deploys RollupManagerMock and
// verifies that the REAL agglayermanager binding can call RollupCount and RollupIDToRollupData
// against it and correctly decode the rollupContract field of the returned struct.
func TestRollupManagerMock_CompatibleWithRealAgglayermanagerBinding(t *testing.T) {
	backend, auth := newSmokeTestBackend(t)
	client := backend.Client()

	mockAddr, _, mockContract, err := rollupmanagermock.DeployRollupmanagermock(auth, client)
	require.NoError(t, err)
	backend.Commit()

	// Configure the mock via its own generated binding.
	wantRollupContract := common.HexToAddress("0x00000000000000000000000000000000c0ffee")
	_, err = mockContract.SetRollupContract(auth, smokeTestRollupID, wantRollupContract)
	require.NoError(t, err)
	backend.Commit()

	// Now read it back through the REAL agglayermanager binding pointed at the mock's address.
	realRollupManager, err := agglayermanager.NewAgglayermanager(mockAddr, client)
	require.NoError(t, err)

	count, err := realRollupManager.RollupCount(&bind.CallOpts{Context: context.Background()})
	require.NoError(t, err)
	require.Equal(t, smokeTestRollupID, count)

	data, err := realRollupManager.RollupIDToRollupData(&bind.CallOpts{Context: context.Background()}, smokeTestRollupID)
	require.NoError(t, err)
	require.Equal(t, wantRollupContract, data.RollupContract)
}

// TestAggchainRollupMock_TrustedSequencerURL_CompatibleWithRealBindings deploys
// AggchainRollupMock, sets the trusted sequencer URL through the mock's own binding, and verifies
// that both REAL bindings that expose trustedSequencerURL/SetTrustedSequencerURL
// (polygonrollupbaseetrog and aggchainbase) can read the getter and parse the emitted event.
func TestAggchainRollupMock_TrustedSequencerURL_CompatibleWithRealBindings(t *testing.T) {
	backend, auth := newSmokeTestBackend(t)
	client := backend.Client()

	mockAddr, _, mockContract, err := aggchainrollupmock.DeployAggchainrollupmock(auth, client)
	require.NoError(t, err)
	backend.Commit()

	tx, err := mockContract.SetTrustedSequencerURL(auth, smokeTestSequencerURL)
	require.NoError(t, err)
	backend.Commit()

	receipt, err := client.TransactionReceipt(context.Background(), tx.Hash())
	require.NoError(t, err)
	require.Len(t, receipt.Logs, 1)

	// Getter, via the real polygonrollupbaseetrog binding.
	realEtrog, err := polygonrollupbaseetrog.NewPolygonrollupbaseetrog(mockAddr, client)
	require.NoError(t, err)
	gotURL, err := realEtrog.TrustedSequencerURL(&bind.CallOpts{Context: context.Background()})
	require.NoError(t, err)
	require.Equal(t, smokeTestSequencerURL, gotURL)

	etrogEvt, err := realEtrog.ParseSetTrustedSequencerURL(*receipt.Logs[0])
	require.NoError(t, err)
	require.Equal(t, smokeTestSequencerURL, etrogEvt.NewTrustedSequencerURL)

	// Getter, via the real aggchainbase binding (also exposes trustedSequencerURL identically).
	realAggchain, err := aggchainbase.NewAggchainbase(mockAddr, client)
	require.NoError(t, err)
	gotURL2, err := realAggchain.TrustedSequencerURL(&bind.CallOpts{Context: context.Background()})
	require.NoError(t, err)
	require.Equal(t, smokeTestSequencerURL, gotURL2)

	aggchainEvt, err := realAggchain.ParseSetTrustedSequencerURL(*receipt.Logs[0])
	require.NoError(t, err)
	require.Equal(t, smokeTestSequencerURL, aggchainEvt.NewTrustedSequencerURL)
}

// TestAggchainRollupMock_AggchainMetadata_CompatibleWithRealBinding deploys AggchainRollupMock,
// sets aggchainMetadata["BRIDGE_SERVICE_URL"] through the mock's own binding, and verifies the
// REAL aggchainbase binding can read the getter and parse the emitted AggchainMetadataSet event,
// including the indexed-string-as-keccak256-hash Key field.
func TestAggchainRollupMock_AggchainMetadata_CompatibleWithRealBinding(t *testing.T) {
	backend, auth := newSmokeTestBackend(t)
	client := backend.Client()

	mockAddr, _, mockContract, err := aggchainrollupmock.DeployAggchainrollupmock(auth, client)
	require.NoError(t, err)
	backend.Commit()

	tx, err := mockContract.SetAggchainMetadata(auth, smokeTestMetadataKey, smokeTestBridgeURL)
	require.NoError(t, err)
	backend.Commit()

	receipt, err := client.TransactionReceipt(context.Background(), tx.Hash())
	require.NoError(t, err)
	require.Len(t, receipt.Logs, 1)

	realAggchain, err := aggchainbase.NewAggchainbase(mockAddr, client)
	require.NoError(t, err)

	gotValue, err := realAggchain.AggchainMetadata(&bind.CallOpts{Context: context.Background()}, smokeTestMetadataKey)
	require.NoError(t, err)
	require.Equal(t, smokeTestBridgeURL, gotValue)

	evt, err := realAggchain.ParseAggchainMetadataSet(*receipt.Logs[0])
	require.NoError(t, err)
	require.Equal(t, crypto.Keccak256Hash([]byte(smokeTestMetadataKey)), evt.Key)
	require.Equal(t, smokeTestBridgeURL, evt.Value)
}
