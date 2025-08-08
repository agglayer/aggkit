package validator

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/polygonrollupmanager"
	agglayermocks "github.com/agglayer/aggkit/agglayer/mocks"
	"github.com/agglayer/aggkit/aggsender/flows"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/query"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	mocksethclient "github.com/agglayer/aggkit/types/mocks"
	"github.com/agglayer/go_signer/signer"
	signertypes "github.com/agglayer/go_signer/signer/types"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestValidateFullAggsenderDB(t *testing.T) {
	t.Skip("This test uses a real database that has certificates with non empty metadata, so we skip it for now")

	// Test real db verification
	testDataPath := getTestDataPath(t)
	logger := log.WithFields("test", "TestValidateFullAggsenderDB")
	ctx := context.TODO()
	mockL2EthClient := mocksethclient.NewEthClienter(t)
	mockL2EthClient.EXPECT().BlockByNumber(ctx, mock.Anything).Return(&ethtypes.Block{}, nil).Maybe()
	bridgeSyncL2, err := bridgesync.NewL2ReadOnly(
		ctx,
		testDataPath+"/bridgel2sync.sqlite",
		1, // OrigNetwork
	)
	require.NoError(t, err)
	lastProcessBlock, err := bridgeSyncL2.GetLastProcessedBlock(ctx)
	require.NoError(t, err)
	require.Greater(t, lastProcessBlock, uint64(522), "Last processed block should be greater than 0")

	l2BridgeQuerier := query.NewBridgeDataQuerier(logger, bridgeSyncL2, 1)

	mockL1EthClient := mocksethclient.NewEthClienter(t)
	l1InfoTreeSync, err := l1infotreesync.NewReadOnly(
		ctx,
		testDataPath+"/L1InfoTreeSync.sqlite",
	)
	require.NoError(t, err)
	l1InfoTreeDataQuerier := query.NewL1InfoTreeDataQuerier(mockL1EthClient, l1InfoTreeSync)

	mockRollupDataQuerier := mocks.NewRollupDataQuerier(t)
	lerQuerier := query.NewLERDataQuerier(1, mockRollupDataQuerier)
	signer, err := signer.NewSigner(ctx, 0, signertypes.SignerConfig{
		Method: signertypes.MethodMock,
	}, "test", logger)
	require.NoError(t, err)

	cfgBase := flows.NewBaseFlowConfigDefault()
	ppFlow := flows.NewPPFlow(
		logger,
		flows.NewBaseFlow(logger, l2BridgeQuerier, nil, l1InfoTreeDataQuerier, lerQuerier, cfgBase),
		nil, // storage
		l1InfoTreeDataQuerier,
		l2BridgeQuerier,
		signer,
		false, // forceOneBridgeExit
		0,     // maxL2BlockNumber)
	)

	aggchainFEPQuerier, err := query.NewAggchainFEPQuerier(
		logger,
		types.PessimisticProofMode,
		aggkitcommon.ZeroAddress,
		nil,
	)
	require.NoError(t, err)

	mockAgglayerClient := agglayermocks.NewAgglayerClientMock(t)
	certQuerier := query.NewCertificateQuerier(
		bridgeSyncL2,
		aggchainFEPQuerier,
		mockAgglayerClient,
	)

	certificateValidator := NewAggsenderValidator(
		logger,
		ppFlow,
		l1InfoTreeDataQuerier,
		certQuerier,
		nil,
	)
	require.NotNil(t, certificateValidator)

	dbValidator := NewDBValidator(
		logger,
		certificateValidator)

	mockRollupDataQuerier.EXPECT().GetRollupData(mock.Anything).Return(
		polygonrollupmanager.PolygonRollupManagerRollupDataReturn{}, nil)

	_, err = dbValidator.ValidateDB("testData/aggsender.sqlite")
	require.NoError(t, err, "DB validation should not return an error")
}

func getTestDataPath(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok, "failed to get caller information")
	testDir := filepath.Dir(filename)
	path := filepath.Join(testDir, "testData")
	return path
}
