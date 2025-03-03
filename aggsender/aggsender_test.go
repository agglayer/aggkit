package aggsender

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path"
	"testing"
	"time"

	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/mocks"
	aggsendertypes "github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/signer"
	treeTypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	networkIDTest = uint32(1234)
)

var (
	errTest = errors.New("unitest  error")
	ler1    = common.HexToHash("0x123")
)

func TestConfigString(t *testing.T) {
	config := Config{
		StoragePath:                 "/path/to/storage",
		AggLayerURL:                 "http://agglayer.url",
		AggsenderPrivateKey:         signer.NewLocalSignerConfig("/path/to/key", "password"),
		URLRPCL2:                    "http://l2.rpc.url",
		BlockFinality:               "latestBlock",
		EpochNotificationPercentage: 50,
		Mode:                        "PP",
	}

	expected := "StoragePath: /path/to/storage\n" +
		"AggLayerURL: http://agglayer.url\n" +
		"AggsenderPrivateKey: local\n" +
		"URLRPCL2: http://l2.rpc.url\n" +
		"BlockFinality: latestBlock\n" +
		"EpochNotificationPercentage: 50\n" +
		"DryRun: false\n" +
		"EnableRPC: false\n" +
		"AggchainProofURL: \n" +
		"Mode: PP\n" +
		"CheckStatusCertificateInterval: 0s\n" +
		"RetryCertImmediatelyAfterInError: false\n" +
		"MaxSubmitRate: RateLimitConfig{Unlimited}\n" +
		"MaxEpochPercentageAllowedToSendCertificate: 0\n"

	require.Equal(t, expected, config.String())
}

func TestAggSenderStart(t *testing.T) {
	aggLayerMock := agglayer.NewAgglayerClientMock(t)
	epochNotifierMock := mocks.NewEpochNotifier(t)
	bridgeL2SyncerMock := mocks.NewL2BridgeSyncer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	aggSender, err := New(
		ctx,
		log.WithFields("test", "unittest"),
		Config{
			StoragePath:          path.Join(t.TempDir(), "aggsenderTestAggSenderStart.sqlite"),
			DelayBeetweenRetries: types.Duration{Duration: 1 * time.Microsecond},
		},
		aggLayerMock,
		nil,
		bridgeL2SyncerMock,
		epochNotifierMock, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, aggSender)
	ch := make(chan aggsendertypes.EpochEvent)
	epochNotifierMock.EXPECT().Subscribe("aggsender").Return(ch)
	bridgeL2SyncerMock.EXPECT().OriginNetwork().Return(uint32(1))
	bridgeL2SyncerMock.EXPECT().GetLastProcessedBlock(mock.Anything).Return(uint64(0), nil)
	aggLayerMock.EXPECT().GetLatestPendingCertificateHeader(mock.Anything).Return(nil, nil)
	aggLayerMock.EXPECT().GetLatestSettledCertificateHeader(mock.Anything).Return(nil, nil)

	go aggSender.Start(ctx)
	ch <- aggsendertypes.EpochEvent{
		Epoch: 1,
	}
	time.Sleep(200 * time.Millisecond)
}

func TestAggSenderSendCertificates(t *testing.T) {
	AggLayerMock := agglayer.NewAgglayerClientMock(t)
	epochNotifierMock := mocks.NewEpochNotifier(t)
	bridgeL2SyncerMock := mocks.NewL2BridgeSyncer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	config := Config{
		MaxSubmitCertificateRate: aggkitcommon.RateLimitConfig{NumRequests: 1, Interval: types.Duration{Duration: 1 * time.Second}},
		StoragePath:              path.Join(t.TempDir(), "aggsenderTestAggSenderSendCertificates.sqlite"),
	}
	aggSender, err := New(
		ctx,
		log.WithFields("test", "unittest"),
		config,
		AggLayerMock,
		nil,
		bridgeL2SyncerMock,
		epochNotifierMock, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, aggSender)

	t.Run("regular case (1 cert send)", func(t *testing.T) {
		aggSender.cfg.CheckStatusCertificateInterval = types.Duration{Duration: time.Microsecond}
		ch := make(chan aggsendertypes.EpochEvent, 2)
		epochNotifierMock.EXPECT().Subscribe("aggsender").Return(ch).Once()
		err = aggSender.storage.SaveLastSentCertificate(ctx, aggsendertypes.CertificateInfo{
			Height: 1,
			Status: agglayer.Pending,
		})
		require.NoError(t, err)
		AggLayerMock.EXPECT().GetCertificateHeader(mock.Anything).Return(&agglayer.CertificateHeader{
			Status: agglayer.Pending,
		}, nil).Once()

		aggSender.sendCertificates(ctx, 1)
		AggLayerMock.AssertExpectations(t)
		epochNotifierMock.AssertExpectations(t)
	})

	t.Run("check cert status and retry cert", func(t *testing.T) {
		aggSender, err := New(
			ctx,
			log.WithFields("test", "unittest"),
			config,
			AggLayerMock,
			nil,
			bridgeL2SyncerMock,
			epochNotifierMock,
			nil,
			nil)
		require.NoError(t, err)
		require.NotNil(t, aggSender)
		aggSender.cfg.CheckStatusCertificateInterval = types.Duration{Duration: 1 * time.Millisecond}
		ch := make(chan aggsendertypes.EpochEvent, 2)
		epochNotifierMock.EXPECT().Subscribe("aggsender").Return(ch)
		err = aggSender.storage.SaveLastSentCertificate(ctx, aggsendertypes.CertificateInfo{
			Height: 1,
			Status: agglayer.Pending,
		})
		AggLayerMock.EXPECT().GetCertificateHeader(mock.Anything).Return(&agglayer.CertificateHeader{
			Status: agglayer.InError,
		}, nil).Once()
		require.NoError(t, err)
		ch <- aggsendertypes.EpochEvent{
			Epoch: 1,
		}
		bridgeL2SyncerMock.EXPECT().GetLastProcessedBlock(mock.Anything).Return(uint64(1), nil).Once()
		bridgeL2SyncerMock.EXPECT().GetBridgesPublished(mock.Anything, mock.Anything, mock.Anything).Return([]bridgesync.Bridge{}, nil).Once()
		aggSender.sendCertificates(ctx, 1)
		bridgeL2SyncerMock.AssertExpectations(t)
	})
}

func TestCheckIfCertificatesAreSettled(t *testing.T) {
	tests := []struct {
		name                     string
		pendingCertificates      []*aggsendertypes.CertificateInfo
		certificateHeaders       map[common.Hash]*agglayer.CertificateHeader
		getFromDBError           error
		clientError              error
		updateDBError            error
		expectedErrorLogMessages []string
		expectedInfoMessages     []string
		expectedError            bool
	}{
		{
			name: "All certificates settled - update successful",
			pendingCertificates: []*aggsendertypes.CertificateInfo{
				{CertificateID: common.HexToHash("0x1"), Height: 1},
				{CertificateID: common.HexToHash("0x2"), Height: 2},
			},
			certificateHeaders: map[common.Hash]*agglayer.CertificateHeader{
				common.HexToHash("0x1"): {Status: agglayer.Settled},
				common.HexToHash("0x2"): {Status: agglayer.Settled},
			},
			expectedInfoMessages: []string{
				"certificate %s changed status to %s",
			},
		},
		{
			name: "Some certificates in error - update successful",
			pendingCertificates: []*aggsendertypes.CertificateInfo{
				{CertificateID: common.HexToHash("0x1"), Height: 1},
				{CertificateID: common.HexToHash("0x2"), Height: 2},
			},
			certificateHeaders: map[common.Hash]*agglayer.CertificateHeader{
				common.HexToHash("0x1"): {Status: agglayer.InError},
				common.HexToHash("0x2"): {Status: agglayer.Settled},
			},
			expectedInfoMessages: []string{
				"certificate %s changed status to %s",
			},
		},
		{
			name:           "Error getting pending certificates",
			getFromDBError: fmt.Errorf("storage error"),
			expectedErrorLogMessages: []string{
				"error getting pending certificates: %w",
			},
			expectedError: true,
		},
		{
			name: "Error getting certificate header",
			pendingCertificates: []*aggsendertypes.CertificateInfo{
				{CertificateID: common.HexToHash("0x1"), Height: 1},
			},
			certificateHeaders: map[common.Hash]*agglayer.CertificateHeader{
				common.HexToHash("0x1"): {Status: agglayer.InError},
			},
			clientError: fmt.Errorf("client error"),
			expectedErrorLogMessages: []string{
				"error getting header of certificate %s with height: %d from agglayer: %w",
			},
			expectedError: true,
		},
		{
			name: "Error updating certificate status",
			pendingCertificates: []*aggsendertypes.CertificateInfo{
				{CertificateID: common.HexToHash("0x1"), Height: 1},
			},
			certificateHeaders: map[common.Hash]*agglayer.CertificateHeader{
				common.HexToHash("0x1"): {Status: agglayer.Settled},
			},
			updateDBError: fmt.Errorf("update error"),
			expectedErrorLogMessages: []string{
				"error updating certificate status in storage: %w",
			},
			expectedInfoMessages: []string{
				"certificate %s changed status to %s",
			},
			expectedError: true,
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			mockStorage := mocks.NewAggSenderStorage(t)
			mockAggLayerClient := agglayer.NewAgglayerClientMock(t)
			mockLogger := log.WithFields("test", "unittest")

			mockStorage.On("GetCertificatesByStatus", agglayer.NonSettledStatuses).Return(
				tt.pendingCertificates, tt.getFromDBError)
			for certID, header := range tt.certificateHeaders {
				mockAggLayerClient.On("GetCertificateHeader", certID).Return(header, tt.clientError)
			}
			if tt.updateDBError != nil {
				mockStorage.On("UpdateCertificate", mock.Anything, mock.Anything).Return(tt.updateDBError)
			} else if tt.clientError == nil && tt.getFromDBError == nil {
				mockStorage.On("UpdateCertificate", mock.Anything, mock.Anything).Return(nil)
			}

			aggSender := &AggSender{
				log:            mockLogger,
				storage:        mockStorage,
				aggLayerClient: mockAggLayerClient,
				cfg:            Config{},
			}

			ctx := context.TODO()
			checkResult := aggSender.checkPendingCertificatesStatus(ctx)
			require.Equal(t, tt.expectedError, checkResult.existPendingCerts)
			mockAggLayerClient.AssertExpectations(t)
			mockStorage.AssertExpectations(t)
		})
	}
}

func TestExtractSignatureData(t *testing.T) {
	t.Parallel()

	testR := common.HexToHash("0x1")
	testV := common.HexToHash("0x2")

	tests := []struct {
		name              string
		signature         []byte
		expectedR         common.Hash
		expectedS         common.Hash
		expectedOddParity bool
		expectedError     error
	}{
		{
			name:              "Valid signature - odd parity",
			signature:         append(append(testR.Bytes(), testV.Bytes()...), 1),
			expectedR:         testR,
			expectedS:         testV,
			expectedOddParity: true,
			expectedError:     nil,
		},
		{
			name:              "Valid signature - even parity",
			signature:         append(append(testR.Bytes(), testV.Bytes()...), 2),
			expectedR:         testR,
			expectedS:         testV,
			expectedOddParity: false,
			expectedError:     nil,
		},
		{
			name:          "Invalid signature size",
			signature:     make([]byte, 64), // Invalid size
			expectedError: errInvalidSignatureSize,
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r, s, isOddParity, err := extractSignatureData(tt.signature)

			if tt.expectedError != nil {
				require.Error(t, err)
				require.Equal(t, tt.expectedError, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedR, r)
				require.Equal(t, tt.expectedS, s)
				require.Equal(t, tt.expectedOddParity, isOddParity)
			}
		})
	}
}

func TestExploratoryGenerateCert(t *testing.T) {
	t.Skip("This test is only for exploratory purposes, to generate json format of the certificate")

	key, err := crypto.GenerateKey()
	require.NoError(t, err)

	signature, err := crypto.Sign(common.HexToHash("0x1").Bytes(), key)
	require.NoError(t, err)

	r, s, v, err := extractSignatureData(signature)
	require.NoError(t, err)

	certificate := &agglayer.SignedCertificate{
		Certificate: &agglayer.Certificate{
			NetworkID:         1,
			Height:            1,
			PrevLocalExitRoot: common.HexToHash("0x1"),
			NewLocalExitRoot:  common.HexToHash("0x2"),
			BridgeExits: []*agglayer.BridgeExit{
				{
					LeafType: agglayer.LeafTypeAsset,
					TokenInfo: &agglayer.TokenInfo{
						OriginNetwork:      1,
						OriginTokenAddress: common.HexToAddress("0x11"),
					},
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x22"),
					Amount:             big.NewInt(100),
					Metadata:           []byte("metadata"),
				},
			},
			ImportedBridgeExits: []*agglayer.ImportedBridgeExit{
				{
					GlobalIndex: &agglayer.GlobalIndex{
						MainnetFlag: false,
						RollupIndex: 1,
						LeafIndex:   11,
					},
					BridgeExit: &agglayer.BridgeExit{
						LeafType: agglayer.LeafTypeAsset,
						TokenInfo: &agglayer.TokenInfo{
							OriginNetwork:      1,
							OriginTokenAddress: common.HexToAddress("0x11"),
						},
						DestinationNetwork: 2,
						DestinationAddress: common.HexToAddress("0x22"),
						Amount:             big.NewInt(100),
						Metadata:           []byte("metadata"),
					},
					ClaimData: &agglayer.ClaimFromMainnnet{
						ProofLeafMER: &agglayer.MerkleProof{
							Root:  common.HexToHash("0x1"),
							Proof: [32]common.Hash{},
						},
						ProofGERToL1Root: &agglayer.MerkleProof{
							Root:  common.HexToHash("0x3"),
							Proof: [32]common.Hash{},
						},
						L1Leaf: &agglayer.L1InfoTreeLeaf{
							L1InfoTreeIndex: 1,
							RollupExitRoot:  common.HexToHash("0x4"),
							MainnetExitRoot: common.HexToHash("0x5"),
							Inner: &agglayer.L1InfoTreeLeafInner{
								GlobalExitRoot: common.HexToHash("0x6"),
								BlockHash:      common.HexToHash("0x7"),
								Timestamp:      1231,
							},
						},
					},
				},
			},
		},
		Signature: &agglayer.Signature{
			R:         r,
			S:         s,
			OddParity: v,
		},
	}

	file, err := os.Create("test.json")
	require.NoError(t, err)

	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	require.NoError(t, encoder.Encode(certificate))
}

func TestSendCertificate_NoClaims(t *testing.T) {
	privateKey, err := crypto.GenerateKey()
	require.NoError(t, err)

	ctx := context.Background()
	mockStorage := mocks.NewAggSenderStorage(t)
	mockL2Syncer := mocks.NewL2BridgeSyncer(t)
	mockAggLayerClient := agglayer.NewAgglayerClientMock(t)
	mockL1InfoTreeSyncer := mocks.NewL1InfoTreeSyncer(t)
	logger := log.WithFields("aggsender-test", "no claims test")
	signer := signer.NewLocalSignFromPrivateKey("ut", log.WithFields("aggsender", 1), privateKey)
	aggSender := &AggSender{
		log:              logger,
		storage:          mockStorage,
		l2Syncer:         mockL2Syncer,
		aggLayerClient:   mockAggLayerClient,
		l1infoTreeSyncer: mockL1InfoTreeSyncer,
		signer:           signer,
		cfg:              Config{},
		flow:             newPPFlow(logger, Config{}, mockStorage, nil, mockL2Syncer),
		rateLimiter:      aggkitcommon.NewRateLimit(aggkitcommon.RateLimitConfig{}),
	}

	mockStorage.On("GetLastSentCertificate").Return(&aggsendertypes.CertificateInfo{
		NewLocalExitRoot: common.HexToHash("0x123"),
		Height:           1,
		FromBlock:        0,
		ToBlock:          10,
		Status:           agglayer.Settled,
	}, nil).Once()
	mockStorage.On("SaveLastSentCertificate", mock.Anything, mock.Anything).Return(nil).Once()
	mockL2Syncer.On("GetLastProcessedBlock", mock.Anything).Return(uint64(50), nil)
	mockL2Syncer.On("GetBridgesPublished", mock.Anything, uint64(11), uint64(50)).Return([]bridgesync.Bridge{
		{
			BlockNum:           30,
			BlockPos:           0,
			LeafType:           agglayer.LeafTypeAsset.Uint8(),
			OriginNetwork:      1,
			OriginAddress:      common.HexToAddress("0x1"),
			DestinationNetwork: 2,
			DestinationAddress: common.HexToAddress("0x2"),
			Amount:             big.NewInt(100),
			Metadata:           []byte("metadata"),
			DepositCount:       1,
		},
	}, nil)
	mockL2Syncer.On("GetClaims", mock.Anything, uint64(11), uint64(50)).Return([]bridgesync.Claim{}, nil)
	mockL2Syncer.On("GetExitRootByIndex", mock.Anything, uint32(1)).Return(treeTypes.Root{}, nil).Once()
	mockL2Syncer.On("OriginNetwork").Return(uint32(1), nil).Once()
	mockAggLayerClient.On("SendCertificate", mock.Anything).Return(common.Hash{}, nil).Once()

	signedCertificate, err := aggSender.sendCertificate(ctx)
	require.NoError(t, err)
	require.NotNil(t, signedCertificate)
	require.NotNil(t, signedCertificate.Signature)
	require.NotNil(t, signedCertificate.Certificate)
	require.NotNil(t, signedCertificate.Certificate.ImportedBridgeExits)
	require.Len(t, signedCertificate.Certificate.BridgeExits, 1)

	mockStorage.AssertExpectations(t)
	mockL2Syncer.AssertExpectations(t)
	mockAggLayerClient.AssertExpectations(t)
	mockL1InfoTreeSyncer.AssertExpectations(t)
}

func TestExtractFromCertificateMetadataToBlock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		metadata common.Hash
		expected aggsendertypes.CertificateMetadata
	}{
		{
			name:     "Valid metadata",
			metadata: aggsendertypes.NewCertificateMetadata(0, 1000, 123567890).ToHash(),
			expected: aggsendertypes.CertificateMetadata{
				Version:   1,
				FromBlock: 0,
				Offset:    1000,
				CreatedAt: 123567890,
			},
		},
		{
			name:     "Zero metadata",
			metadata: aggsendertypes.NewCertificateMetadata(0, 0, 0).ToHash(),
			expected: aggsendertypes.CertificateMetadata{
				Version:   1,
				FromBlock: 0,
				Offset:    0,
				CreatedAt: 0,
			},
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := *aggsendertypes.NewCertificateMetadataFromHash(tt.metadata)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestCheckLastCertificateFromAgglayer_ErrorAggLayer(t *testing.T) {
	testData := newAggsenderTestData(t, testDataFlagMockStorage)

	t.Run("error getting last settled cert", func(t *testing.T) {
		testData.l2syncerMock.EXPECT().OriginNetwork().Return(networkIDTest).Maybe()
		testData.agglayerClientMock.EXPECT().GetLatestSettledCertificateHeader(networkIDTest).Return(nil, fmt.Errorf("unittest error")).Once()
		err := testData.sut.checkLastCertificateFromAgglayer(testData.ctx)
		require.Error(t, err)
	})
	t.Run("error getting last pending cert", func(t *testing.T) {
		testData.l2syncerMock.EXPECT().OriginNetwork().Return(networkIDTest).Maybe()
		testData.agglayerClientMock.EXPECT().GetLatestSettledCertificateHeader(networkIDTest).Return(nil, nil).Once()
		testData.agglayerClientMock.EXPECT().GetLatestPendingCertificateHeader(networkIDTest).Return(nil, fmt.Errorf("unittest error")).Maybe()
		err := testData.sut.checkLastCertificateFromAgglayer(testData.ctx)
		require.Error(t, err)
	})
}

func TestCheckLastCertificateFromAgglayer_ErrorStorageGetLastSentCertificate(t *testing.T) {
	testData := newAggsenderTestData(t, testDataFlagMockStorage)
	testData.l2syncerMock.EXPECT().OriginNetwork().Return(networkIDTest).Maybe()
	testData.agglayerClientMock.EXPECT().GetLatestSettledCertificateHeader(networkIDTest).Return(nil, nil).Maybe()
	testData.agglayerClientMock.EXPECT().GetLatestPendingCertificateHeader(networkIDTest).Return(nil, nil).Maybe()
	testData.storageMock.EXPECT().GetLastSentCertificate().Return(nil, fmt.Errorf("unittest error"))

	err := testData.sut.checkLastCertificateFromAgglayer(testData.ctx)

	require.Error(t, err)
}

// TestCheckLastCertificateFromAgglayer_Case1NoCerts
// CASE 1: No certificates in local storage and agglayer
// Aggsender and agglayer are empty so it's ok
func TestCheckLastCertificateFromAgglayer_Case1NoCerts(t *testing.T) {
	testData := newAggsenderTestData(t, testDataFlagNone)
	testData.l2syncerMock.EXPECT().OriginNetwork().Return(networkIDTest).Once()
	testData.agglayerClientMock.EXPECT().GetLatestSettledCertificateHeader(networkIDTest).Return(nil, nil).Maybe()
	testData.agglayerClientMock.EXPECT().GetLatestPendingCertificateHeader(networkIDTest).Return(nil, nil).Maybe()

	err := testData.sut.checkLastCertificateFromAgglayer(testData.ctx)

	require.NoError(t, err)
}

// TestCheckLastCertificateFromAgglayer_Case2NoCertLocalCertRemote
// CASE 2: No certificates in local storage but agglayer has one
// The local DB is empty and we set the lastCert reported by AggLayer
func TestCheckLastCertificateFromAgglayer_Case2NoCertLocalCertRemote(t *testing.T) {
	testData := newAggsenderTestData(t, testDataFlagNone)
	testData.l2syncerMock.EXPECT().OriginNetwork().Return(networkIDTest).Once()
	testData.agglayerClientMock.EXPECT().GetLatestSettledCertificateHeader(mock.Anything).Return(nil, nil)
	testData.agglayerClientMock.EXPECT().GetLatestPendingCertificateHeader(networkIDTest).
		Return(certInfoToCertHeader(t, &testData.testCerts[0], networkIDTest), nil).Once()

	err := testData.sut.checkLastCertificateFromAgglayer(testData.ctx)

	require.NoError(t, err)
	localCert, err := testData.sut.storage.GetLastSentCertificate()
	require.NoError(t, err)
	require.Equal(t, testData.testCerts[0].CertificateID, localCert.CertificateID)
}

// TestCheckLastCertificateFromAgglayer_Case2NoCertLocalCertRemoteErrorStorage
// sub case of previous one that fails to update local storage
func TestCheckLastCertificateFromAgglayer_Case2NoCertLocalCertRemoteErrorStorage(t *testing.T) {
	testData := newAggsenderTestData(t, testDataFlagMockStorage)
	testData.l2syncerMock.EXPECT().OriginNetwork().Return(networkIDTest).Once()
	testData.agglayerClientMock.EXPECT().GetLatestSettledCertificateHeader(mock.Anything).Return(nil, nil)
	testData.agglayerClientMock.EXPECT().GetLatestPendingCertificateHeader(networkIDTest).
		Return(certInfoToCertHeader(t, &testData.testCerts[0], networkIDTest), nil).Once()

	testData.storageMock.EXPECT().GetLastSentCertificate().Return(nil, nil)
	testData.storageMock.EXPECT().SaveLastSentCertificate(mock.Anything, mock.Anything).Return(errTest).Once()
	err := testData.sut.checkLastCertificateFromAgglayer(testData.ctx)

	require.Error(t, err)
}

// CASE 2.1: certificate in storage but not in agglayer
// sub case of previous one that fails to update local storage
func TestCheckLastCertificateFromAgglayer_Case2_1NoCertRemoteButCertLocal(t *testing.T) {
	testData := newAggsenderTestData(t, testDataFlagMockStorage)
	testData.l2syncerMock.EXPECT().OriginNetwork().Return(networkIDTest).Once()
	testData.agglayerClientMock.EXPECT().GetLatestSettledCertificateHeader(mock.Anything).Return(nil, nil)
	testData.agglayerClientMock.EXPECT().GetLatestPendingCertificateHeader(mock.Anything).Return(nil, nil)
	testData.storageMock.EXPECT().GetLastSentCertificate().Return(&testData.testCerts[0], nil)
	err := testData.sut.checkLastCertificateFromAgglayer(testData.ctx)

	require.Error(t, err)
}

// CASE 3.1: the certificate on the agglayer has less height than the one stored in the local storage

func TestCheckLastCertificateFromAgglayer_Case3_1LessHeight(t *testing.T) {
	testData := newAggsenderTestData(t, testDataFlagMockStorage)
	testData.l2syncerMock.EXPECT().OriginNetwork().Return(networkIDTest).Once()
	testData.agglayerClientMock.EXPECT().GetLatestSettledCertificateHeader(mock.Anything).Return(nil, nil)
	testData.agglayerClientMock.EXPECT().GetLatestPendingCertificateHeader(networkIDTest).
		Return(certInfoToCertHeader(t, &testData.testCerts[0], networkIDTest), nil).Once()
	testData.storageMock.EXPECT().GetLastSentCertificate().Return(&testData.testCerts[1], nil)

	err := testData.sut.checkLastCertificateFromAgglayer(testData.ctx)

	require.ErrorContains(t, err, "recovery: the last certificate in the agglayer has less height (0) than the one in the local storage (1)")
}

// CASE 3.2: AggSender and AggLayer not same height. AggLayer has a new certificate

func TestCheckLastCertificateFromAgglayer_Case3_2Mismatch(t *testing.T) {
	testData := newAggsenderTestData(t, testDataFlagMockStorage)
	testData.l2syncerMock.EXPECT().OriginNetwork().Return(networkIDTest).Once()
	testData.agglayerClientMock.EXPECT().GetLatestSettledCertificateHeader(mock.Anything).
		Return(certInfoToCertHeader(t, &testData.testCerts[0], networkIDTest), nil).Once()
	testData.agglayerClientMock.EXPECT().GetLatestPendingCertificateHeader(networkIDTest).
		Return(certInfoToCertHeader(t, &testData.testCerts[1], networkIDTest), nil).Once()
	testData.storageMock.EXPECT().GetLastSentCertificate().Return(&testData.testCerts[0], nil)
	testData.storageMock.EXPECT().SaveLastSentCertificate(mock.Anything, mock.Anything).Return(nil).Once()

	err := testData.sut.checkLastCertificateFromAgglayer(testData.ctx)

	require.NoError(t, err)
}

// CASE 4: AggSender and AggLayer not same certificateID

func TestCheckLastCertificateFromAgglayer_Case4Mismatch(t *testing.T) {
	testData := newAggsenderTestData(t, testDataFlagMockStorage)
	testData.l2syncerMock.EXPECT().OriginNetwork().Return(networkIDTest).Once()
	testData.agglayerClientMock.EXPECT().GetLatestSettledCertificateHeader(mock.Anything).Return(nil, nil)
	testData.agglayerClientMock.EXPECT().GetLatestPendingCertificateHeader(networkIDTest).
		Return(certInfoToCertHeader(t, &testData.testCerts[0], networkIDTest), nil).Once()
	testData.storageMock.EXPECT().GetLastSentCertificate().Return(&testData.testCerts[1], nil)

	err := testData.sut.checkLastCertificateFromAgglayer(testData.ctx)

	require.Error(t, err)
}

// CASE 5: AggSender and AggLayer same certificateID and same status

func TestCheckLastCertificateFromAgglayer_Case5SameStatus(t *testing.T) {
	testData := newAggsenderTestData(t, testDataFlagMockStorage)
	testData.l2syncerMock.EXPECT().OriginNetwork().Return(networkIDTest).Once()
	testData.agglayerClientMock.EXPECT().GetLatestSettledCertificateHeader(mock.Anything).Return(nil, nil)
	testData.agglayerClientMock.EXPECT().GetLatestPendingCertificateHeader(networkIDTest).
		Return(certInfoToCertHeader(t, &testData.testCerts[0], networkIDTest), nil).Once()
	testData.storageMock.EXPECT().GetLastSentCertificate().Return(&testData.testCerts[0], nil)

	err := testData.sut.checkLastCertificateFromAgglayer(testData.ctx)

	require.NoError(t, err)
}

func setupCase5Expectations(t *testing.T, testData *aggsenderTestData) {
	t.Helper()
	testData.l2syncerMock.EXPECT().OriginNetwork().Return(networkIDTest)
	aggLayerCert := certInfoToCertHeader(t, &testData.testCerts[0], networkIDTest)
	aggLayerCert.Status = agglayer.Settled
	testData.agglayerClientMock.EXPECT().GetLatestSettledCertificateHeader(mock.Anything).Return(nil, nil)
	testData.agglayerClientMock.EXPECT().GetLatestPendingCertificateHeader(networkIDTest).Return(aggLayerCert, nil)

	testData.storageMock.EXPECT().GetLastSentCertificate().Return(&testData.testCerts[0], nil)
}

// CASE 5: AggSender and AggLayer same certificateID and differ on status
func TestCheckLastCertificateFromAgglayer_Case5UpdateStatus(t *testing.T) {
	testData := newAggsenderTestData(t, testDataFlagMockStorage)
	setupCase5Expectations(t, testData)
	testData.storageMock.EXPECT().UpdateCertificate(mock.Anything, mock.Anything).Return(nil).Once()

	err := testData.sut.checkLastCertificateFromAgglayer(testData.ctx)

	require.NoError(t, err)
}

// CASE 4: AggSender and AggLayer same certificateID and differ on status but fails update
func TestCheckLastCertificateFromAgglayer_Case4ErrorUpdateStatus(t *testing.T) {
	testData := newAggsenderTestData(t, testDataFlagMockStorage)
	setupCase5Expectations(t, testData)
	testData.storageMock.EXPECT().UpdateCertificate(mock.Anything, mock.Anything).Return(errTest).Once()

	err := testData.sut.checkLastCertificateFromAgglayer(testData.ctx)

	require.Error(t, err)
}

func TestSendCertificate(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		mockFn        func(*mocks.AggSenderStorage, *mocks.AggsenderFlow, *mocks.L1InfoTreeSyncer, *agglayer.AgglayerClientMock)
		expectedError string
	}{
		{
			name: "error getting certificate build params",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockFlow *mocks.AggsenderFlow,
				mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer,
				mockAgglayerClient *agglayer.AgglayerClientMock) {
				mockFlow.On("GetCertificateBuildParams", mock.Anything).Return(nil, errors.New("some error")).Once()
			},
			expectedError: "error getting certificate build params",
		},
		{
			name: "no consumed bridges",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockFlow *mocks.AggsenderFlow,
				mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer,
				mockAgglayerClient *agglayer.AgglayerClientMock) {
				mockFlow.On("GetCertificateBuildParams", mock.Anything).Return(&aggsendertypes.CertificateBuildParams{
					Bridges: []bridgesync.Bridge{},
				}, nil).Once()
			},
		},
		{
			name: "error building certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockFlow *mocks.AggsenderFlow,
				mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer,
				mockAgglayerClient *agglayer.AgglayerClientMock) {
				mockFlow.On("GetCertificateBuildParams", mock.Anything).Return(&aggsendertypes.CertificateBuildParams{
					Bridges: []bridgesync.Bridge{{}},
				}, nil).Once()
				mockFlow.On("BuildCertificate", mock.Anything, mock.Anything).Return(nil, errors.New("some error")).Once()
			},
			expectedError: "error building certificate",
		},
		{
			name: "error sending certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockFlow *mocks.AggsenderFlow,
				mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer,
				mockAgglayerClient *agglayer.AgglayerClientMock) {
				mockFlow.On("GetCertificateBuildParams", mock.Anything).Return(&aggsendertypes.CertificateBuildParams{
					Bridges: []bridgesync.Bridge{{}},
				}, nil).Once()
				mockFlow.On("BuildCertificate", mock.Anything, mock.Anything).Return(&agglayer.Certificate{
					NetworkID:        1,
					Height:           0,
					NewLocalExitRoot: common.HexToHash("0x1"),
					BridgeExits:      []*agglayer.BridgeExit{{}},
				}, nil).Once()
				mockAgglayerClient.On("SendCertificate", mock.Anything).Return(common.Hash{}, errors.New("some error")).Once()
			},
			expectedError: "error sending certificate",
		},
		{
			name: "error saving certificate to storage",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockFlow *mocks.AggsenderFlow,
				mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer,
				mockAgglayerClient *agglayer.AgglayerClientMock) {
				mockFlow.On("GetCertificateBuildParams", mock.Anything).Return(&aggsendertypes.CertificateBuildParams{
					Bridges: []bridgesync.Bridge{{}},
				}, nil).Once()
				mockFlow.On("BuildCertificate", mock.Anything, mock.Anything).Return(&agglayer.Certificate{
					NetworkID:        11,
					Height:           0,
					NewLocalExitRoot: common.HexToHash("0x11"),
					BridgeExits:      []*agglayer.BridgeExit{{}},
				}, nil).Once()
				mockAgglayerClient.On("SendCertificate", mock.Anything).Return(common.HexToHash("0x22"), nil).Once()
				mockStorage.On("SaveLastSentCertificate", mock.Anything, mock.Anything).Return(errors.New("some error")).Once()
			},
			expectedError: "error saving last sent certificate",
		},
		{
			name: "successful sending and saving of a certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockFlow *mocks.AggsenderFlow,
				mockL1InfoTreeSyncer *mocks.L1InfoTreeSyncer,
				mockAgglayerClient *agglayer.AgglayerClientMock) {
				mockFlow.On("GetCertificateBuildParams", mock.Anything).Return(&aggsendertypes.CertificateBuildParams{
					Bridges: []bridgesync.Bridge{{}},
				}, nil).Once()
				mockFlow.On("BuildCertificate", mock.Anything, mock.Anything).Return(&agglayer.Certificate{
					NetworkID:        11,
					Height:           0,
					NewLocalExitRoot: common.HexToHash("0x11"),
					BridgeExits:      []*agglayer.BridgeExit{{}},
				}, nil).Once()
				mockAgglayerClient.On("SendCertificate", mock.Anything).Return(common.HexToHash("0x22"), nil).Once()
				mockStorage.On("SaveLastSentCertificate", mock.Anything, mock.Anything).Return(nil).Once()
			},
		},
	}

	for _, tt := range testCases {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockStorage := mocks.NewAggSenderStorage(t)
			mockAggsenderFlow := mocks.NewAggsenderFlow(t)
			mockL1InfoTreeSyncer := mocks.NewL1InfoTreeSyncer(t)
			mockAgglayerClient := agglayer.NewAgglayerClientMock(t)
			tt.mockFn(mockStorage, mockAggsenderFlow, mockL1InfoTreeSyncer, mockAgglayerClient)

			logger := log.WithFields("aggsender-test", "sendCertificate")
			privKey, err := ecdsa.GenerateKey(crypto.S256(), rand.Reader)
			require.NoError(t, err)
			signer := signer.NewLocalSignFromPrivateKey("ut", logger, privKey)

			aggsender := &AggSender{
				log:              logger,
				signer:           signer,
				storage:          mockStorage,
				flow:             mockAggsenderFlow,
				aggLayerClient:   mockAgglayerClient,
				l1infoTreeSyncer: mockL1InfoTreeSyncer,
				rateLimiter:      aggkitcommon.NewRateLimit(aggkitcommon.RateLimitConfig{}),
				cfg: Config{
					MaxRetriesStoreCertificate: 1,
				},
			}

			_, err = aggsender.sendCertificate(context.Background())

			if tt.expectedError != "" {
				require.ErrorContains(t, err, tt.expectedError)
			} else {
				require.NoError(t, err)
			}

			mockStorage.AssertExpectations(t)
			mockAggsenderFlow.AssertExpectations(t)
			mockL1InfoTreeSyncer.AssertExpectations(t)
		})
	}
}

func TestLimitEpochPercent_Greater(t *testing.T) {
	testData := newAggsenderTestData(t, testDataFlagMockStorage)
	testData.sut.cfg.MaxCertSize = (aggsendertypes.EstimatedSizeBridgeExit * 3) + 1
	testData.sut.cfg.MaxEpochPercentageAllowedToSendCertificate = 80

	ctx := context.TODO()
	testData.l2syncerMock.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(100), nil).Once()
	testData.storageMock.EXPECT().GetLastSentCertificate().Return(&aggsendertypes.CertificateInfo{
		FromBlock: 1,
		ToBlock:   20,
		Status:    agglayer.Settled,
	}, nil).Once()
	testData.l2syncerMock.EXPECT().GetBridgesPublished(ctx, uint64(21), uint64(100)).Return(NewBridgesData(t, 0, []uint64{21, 21, 21, 22, 22, 22}), nil).Once()
	testData.l2syncerMock.EXPECT().GetClaims(ctx, uint64(21), uint64(100)).Return(nil, nil).Once()
	testData.l2syncerMock.EXPECT().GetExitRootByIndex(ctx, mock.Anything).Return(treeTypes.Root{}, nil).Once()
	testData.l2syncerMock.EXPECT().OriginNetwork().Return(uint32(1)).Once()
	testData.epochNotifierMock.EXPECT().GetEpochStatus().Return(aggsendertypes.EpochStatus{
		Epoch:        1,
		PercentEpoch: 90,
	}).Once()
	_, err := testData.sut.sendCertificate(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "epoch percentage")
}

func TestNewAggSender(t *testing.T) {
	sut, err := New(context.TODO(), log.WithFields("module", "ut"), Config{}, nil, nil, nil, nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, sut)
	require.Contains(t, sut.rateLimiter.String(), "Unlimited")
}

type testDataFlags = int

const (
	testDataFlagNone        testDataFlags = 0
	testDataFlagMockStorage testDataFlags = 1
)

type aggsenderTestData struct {
	ctx                  context.Context
	agglayerClientMock   *agglayer.AgglayerClientMock
	l2syncerMock         *mocks.L2BridgeSyncer
	l1InfoTreeSyncerMock *mocks.L1InfoTreeSyncer
	storageMock          *mocks.AggSenderStorage
	epochNotifierMock    *mocks.EpochNotifier
	sut                  *AggSender
	testCerts            []aggsendertypes.CertificateInfo
}

func NewBridgesData(t *testing.T, num int, blockNum []uint64) []bridgesync.Bridge {
	t.Helper()
	if num == 0 {
		num = len(blockNum)
	}
	res := make([]bridgesync.Bridge, 0)
	for i := 0; i < num; i++ {
		res = append(res, bridgesync.Bridge{
			BlockNum:      blockNum[i%len(blockNum)],
			BlockPos:      0,
			LeafType:      agglayer.LeafTypeAsset.Uint8(),
			OriginNetwork: 1,
		})
	}
	return res
}

func NewClaimData(t *testing.T, num int, blockNum []uint64) []bridgesync.Claim {
	t.Helper()
	if num == 0 {
		num = len(blockNum)
	}
	res := make([]bridgesync.Claim, 0)
	for i := 0; i < num; i++ {
		res = append(res, bridgesync.Claim{
			BlockNum: blockNum[i%len(blockNum)],
			BlockPos: 0,
		})
	}
	return res
}

func certInfoToCertHeader(t *testing.T, certInfo *aggsendertypes.CertificateInfo, networkID uint32) *agglayer.CertificateHeader {
	t.Helper()
	if certInfo == nil {
		return nil
	}
	return &agglayer.CertificateHeader{
		Height:           certInfo.Height,
		NetworkID:        networkID,
		CertificateID:    certInfo.CertificateID,
		NewLocalExitRoot: certInfo.NewLocalExitRoot,
		Status:           agglayer.Pending,
		Metadata: aggsendertypes.NewCertificateMetadata(
			certInfo.FromBlock,
			uint32(certInfo.FromBlock-certInfo.ToBlock),
			certInfo.CreatedAt,
		).ToHash(),
	}
}

func newAggsenderTestData(t *testing.T, creationFlags testDataFlags) *aggsenderTestData {
	t.Helper()
	l2syncerMock := mocks.NewL2BridgeSyncer(t)
	agglayerClientMock := agglayer.NewAgglayerClientMock(t)
	l1InfoTreeSyncerMock := mocks.NewL1InfoTreeSyncer(t)
	epochNotifierMock := mocks.NewEpochNotifier(t)
	logger := log.WithFields("aggsender-test", "checkLastCertificateFromAgglayer")
	var storageMock *mocks.AggSenderStorage
	var storage db.AggSenderStorage
	var err error
	if creationFlags&testDataFlagMockStorage != 0 {
		storageMock = mocks.NewAggSenderStorage(t)
		storage = storageMock
	} else {
		dbPath := path.Join(t.TempDir(), "newAggsenderTestData.sqlite")
		storageConfig := db.AggSenderSQLStorageConfig{
			DBPath:                  dbPath,
			KeepCertificatesHistory: true,
		}
		storage, err = db.NewAggSenderSQLStorage(logger, storageConfig)
		require.NoError(t, err)
	}
	privKey, err := ecdsa.GenerateKey(crypto.S256(), rand.Reader)
	require.NoError(t, err)
	signer := signer.NewLocalSignFromPrivateKey("ut", logger, privKey)
	ctx := context.TODO()
	sut := &AggSender{
		log:              logger,
		l2Syncer:         l2syncerMock,
		aggLayerClient:   agglayerClientMock,
		storage:          storage,
		l1infoTreeSyncer: l1InfoTreeSyncerMock,
		cfg: Config{
			MaxCertSize: 1024 * 1024,
		},
		rateLimiter:   aggkitcommon.NewRateLimit(aggkitcommon.RateLimitConfig{}),
		signer:        signer,
		epochNotifier: epochNotifierMock,
		flow:          newPPFlow(logger, Config{}, storage, l1InfoTreeSyncerMock, l2syncerMock),
	}
	testCerts := []aggsendertypes.CertificateInfo{
		{
			Height:           0,
			CertificateID:    common.HexToHash("0x1"),
			NewLocalExitRoot: common.HexToHash("0x2"),
			Status:           agglayer.Pending,
		},
		{
			Height:           1,
			CertificateID:    common.HexToHash("0x1a111"),
			NewLocalExitRoot: common.HexToHash("0x2a2"),
			Status:           agglayer.Pending,
		},
	}

	return &aggsenderTestData{
		ctx:                  ctx,
		agglayerClientMock:   agglayerClientMock,
		l2syncerMock:         l2syncerMock,
		l1InfoTreeSyncerMock: l1InfoTreeSyncerMock,
		storageMock:          storageMock,
		epochNotifierMock:    epochNotifierMock,
		sut:                  sut,
		testCerts:            testCerts,
	}
}
