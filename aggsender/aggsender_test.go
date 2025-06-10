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
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/config"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/flows"
	"github.com/agglayer/aggkit/aggsender/mocks"
	aggsendertypes "github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config/types"
	mocksdb "github.com/agglayer/aggkit/db/compatibility/mocks"
	aggkitgrpc "github.com/agglayer/aggkit/grpc"
	"github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/agglayer/go_signer/signer"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	networkIDTest = uint32(1234)
)

func TestConfigString(t *testing.T) {
	config := config.Config{
		StoragePath:                 "/path/to/storage",
		AgglayerClient:              &aggkitgrpc.ClientConfig{URL: "http://agglayer.url"},
		AggsenderPrivateKey:         signer.NewLocalSignerConfig("/path/to/key", "password"),
		URLRPCL2:                    "http://l2.rpc.url",
		BlockFinality:               "latestBlock",
		EpochNotificationPercentage: 50,
		Mode:                        "PP",
		SovereignRollupAddr:         common.HexToAddress("0x1"),
	}

	expected := fmt.Sprintf("StoragePath: /path/to/storage\n"+
		"AgglayerClient: %s\n"+
		"AggsenderPrivateKey: local\n"+
		"BlockFinality: latestBlock\n"+
		"EpochNotificationPercentage: 50\n"+
		"DryRun: false\n"+
		"EnableRPC: false\n"+
		"AggkitProverClient: none\n"+
		"Mode: PP\n"+
		"CheckStatusCertificateInterval: 0s\n"+
		"RetryCertAfterInError: false\n"+
		"MaxSubmitRate: RateLimitConfig{Unlimited}\n"+
		"SovereignRollupAddr: 0x0000000000000000000000000000000000000001\n"+
		"RequireNoFEPBlockGap: false\n",
		config.AgglayerClient.String())

	require.Equal(t, expected, config.String())
}

func TestAggSenderStart(t *testing.T) {
	aggLayerMock := agglayer.NewAgglayerClientMock(t)
	epochNotifierMock := mocks.NewEpochNotifier(t)
	bridgeL2SyncerMock := mocks.NewL2BridgeSyncer(t)
	ch := make(chan aggsendertypes.EpochEvent)
	epochNotifierMock.EXPECT().Subscribe("aggsender").Return(ch)
	epochNotifierMock.EXPECT().GetEpochStatus().Return(aggsendertypes.EpochStatus{}).Once()
	bridgeL2SyncerMock.EXPECT().OriginNetwork().Return(uint32(1))
	bridgeL2SyncerMock.EXPECT().GetLastProcessedBlock(mock.Anything).Return(uint64(0), nil)
	aggLayerMock.EXPECT().GetLatestPendingCertificateHeader(mock.Anything, mock.Anything).Return(nil, nil)
	aggLayerMock.EXPECT().GetLatestSettledCertificateHeader(mock.Anything, mock.Anything).Return(nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	aggSender, err := New(
		ctx,
		log.WithFields("test", "unittest"),
		config.Config{
			Mode:                 "PessimisticProof",
			StoragePath:          path.Join(t.TempDir(), "aggsenderTestAggSenderStart.sqlite"),
			DelayBeetweenRetries: types.Duration{Duration: 1 * time.Microsecond},
			AggsenderPrivateKey: signertypes.SignerConfig{
				Method: signertypes.MethodNone,
			},
		},
		aggLayerMock,
		nil,
		bridgeL2SyncerMock,
		epochNotifierMock, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, aggSender)

	go aggSender.Start(ctx)
	ch <- aggsendertypes.EpochEvent{
		Epoch: 1,
	}
	time.Sleep(200 * time.Millisecond)
}

func TestExploratoryGenerateCert(t *testing.T) {
	t.Skip("This test is only for exploratory purposes, to generate json format of the certificate")

	key, err := crypto.GenerateKey()
	require.NoError(t, err)

	signature, err := crypto.Sign(common.HexToHash("0x1").Bytes(), key)
	require.NoError(t, err)

	certificate := &agglayertypes.Certificate{
		NetworkID:         1,
		Height:            1,
		PrevLocalExitRoot: common.HexToHash("0x1"),
		NewLocalExitRoot:  common.HexToHash("0x2"),
		BridgeExits: []*agglayertypes.BridgeExit{
			{
				LeafType: agglayertypes.LeafTypeAsset,
				TokenInfo: &agglayertypes.TokenInfo{
					OriginNetwork:      1,
					OriginTokenAddress: common.HexToAddress("0x11"),
				},
				DestinationNetwork: 2,
				DestinationAddress: common.HexToAddress("0x22"),
				Amount:             big.NewInt(100),
				Metadata:           []byte("metadata"),
			},
		},
		ImportedBridgeExits: []*agglayertypes.ImportedBridgeExit{
			{
				GlobalIndex: &agglayertypes.GlobalIndex{
					MainnetFlag: false,
					RollupIndex: 1,
					LeafIndex:   11,
				},
				BridgeExit: &agglayertypes.BridgeExit{
					LeafType: agglayertypes.LeafTypeAsset,
					TokenInfo: &agglayertypes.TokenInfo{
						OriginNetwork:      1,
						OriginTokenAddress: common.HexToAddress("0x11"),
					},
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x22"),
					Amount:             big.NewInt(100),
					Metadata:           []byte("metadata"),
				},
				ClaimData: &agglayertypes.ClaimFromMainnnet{
					ProofLeafMER: &agglayertypes.MerkleProof{
						Root:  common.HexToHash("0x1"),
						Proof: [32]common.Hash{},
					},
					ProofGERToL1Root: &agglayertypes.MerkleProof{
						Root:  common.HexToHash("0x3"),
						Proof: [32]common.Hash{},
					},
					L1Leaf: &agglayertypes.L1InfoTreeLeaf{
						L1InfoTreeIndex: 1,
						RollupExitRoot:  common.HexToHash("0x4"),
						MainnetExitRoot: common.HexToHash("0x5"),
						Inner: &agglayertypes.L1InfoTreeLeafInner{
							GlobalExitRoot: common.HexToHash("0x6"),
							BlockHash:      common.HexToHash("0x7"),
							Timestamp:      1231,
						},
					},
				},
			},
		},
		AggchainData: &agglayertypes.AggchainDataSignature{
			Signature: signature,
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
	mockL2BridgeQuerier := mocks.NewBridgeQuerier(t)
	mockL1Querier := mocks.NewL1InfoTreeDataQuerier(t)
	mockAggLayerClient := agglayer.NewAgglayerClientMock(t)
	mockEpochNotifier := mocks.NewEpochNotifier(t)
	logger := log.WithFields("aggsender-test", "no claims test")
	signer := signer.NewLocalSignFromPrivateKey("ut", log.WithFields("aggsender", 1), privateKey)
	aggSender := &AggSender{
		log:             logger,
		storage:         mockStorage,
		l2OriginNetwork: 1,
		aggLayerClient:  mockAggLayerClient,
		epochNotifier:   mockEpochNotifier,
		cfg:             config.Config{},
		flow: flows.NewPPFlow(logger,
			flows.NewBaseFlow(logger, mockL2BridgeQuerier, mockStorage,
				mockL1Querier, flows.NewBaseFlowConfigDefault()),
			mockStorage, mockL1Querier, mockL2BridgeQuerier, signer),
		rateLimiter: aggkitcommon.NewRateLimit(aggkitcommon.RateLimitConfig{}),
	}

	mockStorage.EXPECT().GetLastSentCertificateHeader().Return(&aggsendertypes.CertificateHeader{
		NewLocalExitRoot: common.HexToHash("0x123"),
		Height:           1,
		FromBlock:        0,
		ToBlock:          10,
		Status:           agglayertypes.Settled,
	}, nil).Once()
	mockStorage.EXPECT().SaveLastSentCertificate(mock.Anything, mock.Anything).Return(nil).Once()
	mockL2BridgeQuerier.EXPECT().GetLastProcessedBlock(mock.Anything).Return(uint64(50), nil)
	mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(mock.Anything, uint64(11), uint64(50), aggsendertypes.CertificateTypePP).Return([]bridgesync.Bridge{
		{
			BlockNum:           30,
			BlockPos:           0,
			LeafType:           agglayertypes.LeafTypeAsset.Uint8(),
			OriginNetwork:      1,
			OriginAddress:      common.HexToAddress("0x1"),
			DestinationNetwork: 2,
			DestinationAddress: common.HexToAddress("0x2"),
			Amount:             big.NewInt(100),
			Metadata:           []byte("metadata"),
			DepositCount:       1,
		},
	}, []bridgesync.Claim{}, nil).Once()
	mockL1Querier.EXPECT().GetLatestFinalizedL1InfoRoot(ctx).Return(&treetypes.Root{}, nil, nil).Once()
	mockL2BridgeQuerier.EXPECT().GetExitRootByIndex(mock.Anything, uint32(1)).Return(common.Hash{}, nil).Once()
	mockL2BridgeQuerier.EXPECT().OriginNetwork().Return(uint32(1)).Once()
	mockAggLayerClient.EXPECT().SendCertificate(mock.Anything, mock.Anything).Return(common.Hash{}, nil).Once()
	mockEpochNotifier.EXPECT().GetEpochStatus().Return(aggsendertypes.EpochStatus{})
	signedCertificate, err := aggSender.sendCertificate(ctx)
	require.NoError(t, err)
	require.NotNil(t, signedCertificate)
	require.NotNil(t, signedCertificate.AggchainData)
	require.NotNil(t, signedCertificate.ImportedBridgeExits)
	require.Len(t, signedCertificate.BridgeExits, 1)

	mockStorage.AssertExpectations(t)
	mockL2BridgeQuerier.AssertExpectations(t)
	mockAggLayerClient.AssertExpectations(t)
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
			metadata: aggsendertypes.NewCertificateMetadata(0, 1000, 123567890, 123).ToHash(),
			expected: aggsendertypes.CertificateMetadata{
				Version:   aggsendertypes.CertificateMetadataV2,
				FromBlock: 0,
				Offset:    1000,
				CreatedAt: 123567890,
				CertType:  123,
			},
		},
		{
			name:     "Zero metadata",
			metadata: aggsendertypes.NewCertificateMetadata(0, 0, 0, 0).ToHash(),
			expected: aggsendertypes.CertificateMetadata{
				Version:   aggsendertypes.CertificateMetadataV2,
				FromBlock: 0,
				Offset:    0,
				CreatedAt: 0,
				CertType:  0,
			},
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := aggsendertypes.NewCertificateMetadataFromHash(tt.metadata)
			require.NoError(t, err)
			require.Equal(t, tt.expected, *result)
		})
	}
}

func TestSendCertificate(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		mockFn        func(*mocks.AggSenderStorage, *mocks.AggsenderFlow, *agglayer.AgglayerClientMock)
		expectedError string
	}{
		{
			name: "error getting certificate build params",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockFlow *mocks.AggsenderFlow,
				mockAgglayerClient *agglayer.AgglayerClientMock) {
				mockFlow.EXPECT().GetCertificateBuildParams(mock.Anything).Return(nil, errors.New("some error")).Once()
			},
			expectedError: "error getting certificate build params",
		},
		{
			name: "no new blocks consumed",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockFlow *mocks.AggsenderFlow,
				mockAgglayerClient *agglayer.AgglayerClientMock) {
				mockFlow.EXPECT().GetCertificateBuildParams(mock.Anything).Return(nil, nil).Once()
			},
		},
		{
			name: "error building certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockFlow *mocks.AggsenderFlow,
				mockAgglayerClient *agglayer.AgglayerClientMock) {
				mockFlow.EXPECT().GetCertificateBuildParams(mock.Anything).Return(&aggsendertypes.CertificateBuildParams{
					Bridges: []bridgesync.Bridge{{}},
				}, nil).Once()
				mockFlow.EXPECT().BuildCertificate(mock.Anything, mock.Anything).Return(nil, errors.New("some error")).Once()
			},
			expectedError: "error building certificate",
		},
		{
			name: "error sending certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockFlow *mocks.AggsenderFlow,
				mockAgglayerClient *agglayer.AgglayerClientMock) {
				mockFlow.EXPECT().GetCertificateBuildParams(mock.Anything).Return(&aggsendertypes.CertificateBuildParams{
					Bridges: []bridgesync.Bridge{{}},
				}, nil).Once()
				mockFlow.EXPECT().BuildCertificate(mock.Anything, mock.Anything).Return(&agglayertypes.Certificate{
					NetworkID:        1,
					Height:           0,
					NewLocalExitRoot: common.HexToHash("0x1"),
					BridgeExits:      []*agglayertypes.BridgeExit{{}},
				}, nil).Once()
				mockAgglayerClient.EXPECT().SendCertificate(mock.Anything, mock.Anything).Return(common.Hash{}, errors.New("some error")).Once()
				mockStorage.EXPECT().SaveNonAcceptedCertificate(mock.Anything, mock.Anything).Return(nil).Once()
			},
			expectedError: "error sending certificate",
		},
		{
			name: "error saving certificate to storage",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockFlow *mocks.AggsenderFlow,
				mockAgglayerClient *agglayer.AgglayerClientMock) {
				mockFlow.EXPECT().GetCertificateBuildParams(mock.Anything).Return(&aggsendertypes.CertificateBuildParams{
					Bridges: []bridgesync.Bridge{{}},
				}, nil).Once()
				mockFlow.EXPECT().BuildCertificate(mock.Anything, mock.Anything).Return(&agglayertypes.Certificate{
					NetworkID:        11,
					Height:           0,
					NewLocalExitRoot: common.HexToHash("0x11"),
					BridgeExits:      []*agglayertypes.BridgeExit{{}},
				}, nil).Once()
				mockAgglayerClient.EXPECT().SendCertificate(mock.Anything, mock.Anything).Return(common.HexToHash("0x22"), nil).Once()
				mockStorage.EXPECT().SaveLastSentCertificate(mock.Anything, mock.Anything).Return(errors.New("some error")).Once()
			},
			expectedError: "error saving last sent certificate",
		},
		{
			name: "successful sending and saving of a certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockFlow *mocks.AggsenderFlow,
				mockAgglayerClient *agglayer.AgglayerClientMock) {
				mockFlow.EXPECT().GetCertificateBuildParams(mock.Anything).Return(&aggsendertypes.CertificateBuildParams{
					Bridges: []bridgesync.Bridge{{}},
				}, nil).Once()
				mockFlow.EXPECT().BuildCertificate(mock.Anything, mock.Anything).Return(&agglayertypes.Certificate{
					NetworkID:        11,
					Height:           0,
					NewLocalExitRoot: common.HexToHash("0x11"),
					BridgeExits:      []*agglayertypes.BridgeExit{{}},
				}, nil).Once()
				mockAgglayerClient.EXPECT().SendCertificate(mock.Anything, mock.Anything).Return(common.HexToHash("0x22"), nil).Once()
				mockStorage.EXPECT().SaveLastSentCertificate(mock.Anything, mock.Anything).Return(nil).Once()
			},
		},
	}

	for _, tt := range testCases {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockStorage := mocks.NewAggSenderStorage(t)
			mockAggsenderFlow := mocks.NewAggsenderFlow(t)
			mockAgglayerClient := agglayer.NewAgglayerClientMock(t)
			mockEpochNotifier := mocks.NewEpochNotifier(t)
			tt.mockFn(mockStorage, mockAggsenderFlow, mockAgglayerClient)

			logger := log.WithFields("aggsender-test", "sendCertificate")

			aggsender := &AggSender{
				log:            logger,
				storage:        mockStorage,
				epochNotifier:  mockEpochNotifier,
				flow:           mockAggsenderFlow,
				aggLayerClient: mockAgglayerClient,
				rateLimiter:    aggkitcommon.NewRateLimit(aggkitcommon.RateLimitConfig{}),
				cfg: config.Config{
					MaxRetriesStoreCertificate: 1,
				},
			}
			mockEpochNotifier.EXPECT().GetEpochStatus().Return(aggsendertypes.EpochStatus{})
			_, err := aggsender.sendCertificate(context.Background())

			if tt.expectedError != "" {
				require.ErrorContains(t, err, tt.expectedError)
			} else {
				require.NoError(t, err)
			}

			mockStorage.AssertExpectations(t)
			mockAggsenderFlow.AssertExpectations(t)
		})
	}
}

func TestNewAggSender(t *testing.T) {
	mockBridgeSyncer := mocks.NewL2BridgeSyncer(t)
	mockBridgeSyncer.EXPECT().OriginNetwork().Return(uint32(1)).Twice()
	sut, err := New(context.TODO(), log.WithFields("module", "ut"), config.Config{
		AggsenderPrivateKey: signertypes.SignerConfig{
			Method: signertypes.MethodNone,
		},
		Mode: "PessimisticProof",
	}, nil, nil, mockBridgeSyncer, nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, sut)
	require.Contains(t, sut.rateLimiter.String(), "Unlimited")
}

func TestCheckDBCompatibility(t *testing.T) {
	testData := newAggsenderTestData(t, testDataFlagMockStorage)
	testData.sut.cfg.RequireStorageContentCompatibility = false
	testData.sut.checkDBCompatibility(testData.ctx)
}

func TestAggSenderStartFailFlowCheckInitialStatus(t *testing.T) {
	testData := newAggsenderTestData(t, testDataFlagMockStorage|testDataFlagMockFlow|testDataFlagMockStatusChecker)
	testData.sut.cfg.RequireStorageContentCompatibility = false
	testData.certStatusCheckerMock.EXPECT().CheckInitialStatus(mock.Anything, mock.Anything, testData.sut.status).Once()
	testData.flowMock.EXPECT().CheckInitialStatus(mock.Anything).Return(fmt.Errorf("error")).Once()

	require.Panics(t, func() {
		testData.sut.Start(testData.ctx)
	}, "Expected panic when starting AggSender")
}

func TestAggSenderStartFailsCompatibilityChecker(t *testing.T) {
	testData := newAggsenderTestData(t, testDataFlagMockStorage|testDataFlagMockCompatibilityChecker|testDataFlagMockStatusChecker)
	testData.sut.cfg.RequireStorageContentCompatibility = true
	testData.compatibilityChekerMock.EXPECT().Check(mock.Anything, mock.Anything).Return(fmt.Errorf("error")).Once()

	require.Panics(t, func() {
		testData.sut.Start(testData.ctx)
	}, "Expected panic when starting AggSender")
}

func TestSendCertificates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                    string
		mockFn                  func(*mocks.CertificateStatusChecker, *mocks.EpochNotifier, *mocks.AggSenderStorage, *mocks.AggsenderFlow)
		returnAfterNIterations  int
		certStatusCheckInterval time.Duration
	}{
		{
			name: "context canceled",
			mockFn: func(mockCertStatusChecker *mocks.CertificateStatusChecker, mockEpochNotifier *mocks.EpochNotifier, mockStorage *mocks.AggSenderStorage, mockFlow *mocks.AggsenderFlow) {
				mockEpochNotifier.EXPECT().Subscribe("aggsender").Return(make(chan aggsendertypes.EpochEvent)).Once()
			},
			returnAfterNIterations: 0,
		},
		{
			name: "retry certificate after in-error",
			mockFn: func(mockCertStatusChecker *mocks.CertificateStatusChecker, mockEpochNotifier *mocks.EpochNotifier, mockStorage *mocks.AggSenderStorage, mockFlow *mocks.AggsenderFlow) {
				mockCertStatusChecker.EXPECT().CheckPendingCertificatesStatus(mock.Anything).Return(aggsendertypes.CertStatus{
					ExistPendingCerts:   false,
					ExistNewInErrorCert: true,
				}).Once()
				mockEpochNotifier.EXPECT().Subscribe("aggsender").Return(make(chan aggsendertypes.EpochEvent)).Once()
				mockEpochNotifier.EXPECT().GetEpochStatus().Return(aggsendertypes.EpochStatus{}).Once()
				mockFlow.EXPECT().GetCertificateBuildParams(mock.Anything).Return(nil, nil).Once()
			},
			returnAfterNIterations:  1,
			certStatusCheckInterval: 100 * time.Millisecond,
		},
		{
			name: "epoch received with no pending certificates",
			mockFn: func(mockCertStatusChecker *mocks.CertificateStatusChecker, mockEpochNotifier *mocks.EpochNotifier, mockStorage *mocks.AggSenderStorage, mockFlow *mocks.AggsenderFlow) {
				chEpoch := make(chan aggsendertypes.EpochEvent, 1)
				chEpoch <- aggsendertypes.EpochEvent{Epoch: 1}
				mockEpochNotifier.EXPECT().Subscribe("aggsender").Return(chEpoch).Once()
				mockEpochNotifier.EXPECT().GetEpochStatus().Return(aggsendertypes.EpochStatus{}).Once()
				mockCertStatusChecker.EXPECT().CheckPendingCertificatesStatus(mock.Anything).Return(aggsendertypes.CertStatus{
					ExistPendingCerts: false,
				}).Once()
				mockFlow.EXPECT().GetCertificateBuildParams(mock.Anything).Return(nil, nil).Once()
			},
			returnAfterNIterations: 1,
		},
		{
			name: "epoch received with pending certificates",
			mockFn: func(mockCertStatusChecker *mocks.CertificateStatusChecker, mockEpochNotifier *mocks.EpochNotifier, mockStorage *mocks.AggSenderStorage, mockFlow *mocks.AggsenderFlow) {
				chEpoch := make(chan aggsendertypes.EpochEvent, 1)
				chEpoch <- aggsendertypes.EpochEvent{Epoch: 1}
				mockEpochNotifier.EXPECT().Subscribe("aggsender").Return(chEpoch).Once()
				mockCertStatusChecker.EXPECT().CheckPendingCertificatesStatus(mock.Anything).Return(aggsendertypes.CertStatus{
					ExistPendingCerts: true,
				}).Once()
			},
			returnAfterNIterations: 1,
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockCertStatusChecker := mocks.NewCertificateStatusChecker(t)
			mockEpochNotifier := mocks.NewEpochNotifier(t)
			mockStorage := mocks.NewAggSenderStorage(t)
			mockFlow := mocks.NewAggsenderFlow(t)

			tt.mockFn(mockCertStatusChecker, mockEpochNotifier, mockStorage, mockFlow)

			logger := log.WithFields("aggsender-test", tt.name)
			aggSender := &AggSender{
				log:               logger,
				certStatusChecker: mockCertStatusChecker,
				epochNotifier:     mockEpochNotifier,
				storage:           mockStorage,
				flow:              mockFlow,
				cfg: config.Config{
					RetryCertAfterInError:          true,
					CheckStatusCertificateInterval: types.NewDuration(tt.certStatusCheckInterval),
				},
				status: &aggsendertypes.AggsenderStatus{},
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			go func() {
				time.Sleep(300 * time.Millisecond)
				cancel()
			}()

			aggSender.sendCertificates(ctx, tt.returnAfterNIterations)

			mockCertStatusChecker.AssertExpectations(t)
			mockEpochNotifier.AssertExpectations(t)
			mockStorage.AssertExpectations(t)
			mockFlow.AssertExpectations(t)
		})
	}
}

type testDataFlags = int

const (
	testDataFlagNone                     testDataFlags = 0
	testDataFlagMockStorage              testDataFlags = 1
	testDataFlagMockFlow                 testDataFlags = 2
	testDataFlagMockCompatibilityChecker testDataFlags = 4
	testDataFlagMockStatusChecker        testDataFlags = 8
)

type aggsenderTestData struct {
	ctx                     context.Context
	agglayerClientMock      *agglayer.AgglayerClientMock
	l1InfoQuerier           *mocks.L1InfoTreeDataQuerier
	l2BridgeQuerier         *mocks.BridgeQuerier
	storageMock             *mocks.AggSenderStorage
	epochNotifierMock       *mocks.EpochNotifier
	flowMock                *mocks.AggsenderFlow
	compatibilityChekerMock *mocksdb.CompatibilityChecker
	certStatusCheckerMock   *mocks.CertificateStatusChecker
	sut                     *AggSender
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
			LeafType:      agglayertypes.LeafTypeAsset.Uint8(),
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

func newAggsenderTestData(t *testing.T, creationFlags testDataFlags) *aggsenderTestData {
	t.Helper()
	l2BridgeQuerier := mocks.NewBridgeQuerier(t)
	agglayerClientMock := agglayer.NewAgglayerClientMock(t)
	l1InfoTreeQuerierMock := mocks.NewL1InfoTreeDataQuerier(t)
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
		log:             logger,
		l2OriginNetwork: networkIDTest,
		aggLayerClient:  agglayerClientMock,
		storage:         storage,
		status:          &aggsendertypes.AggsenderStatus{},
		cfg: config.Config{
			MaxCertSize:          1024 * 1024,
			DelayBeetweenRetries: types.Duration{Duration: time.Millisecond},
		},
		rateLimiter:   aggkitcommon.NewRateLimit(aggkitcommon.RateLimitConfig{}),
		epochNotifier: epochNotifierMock,
		flow: flows.NewPPFlow(logger,
			flows.NewBaseFlow(logger, l2BridgeQuerier, storage,
				l1InfoTreeQuerierMock, flows.NewBaseFlowConfigDefault()),
			storage, l1InfoTreeQuerierMock, l2BridgeQuerier, signer),
	}
	var flowMock *mocks.AggsenderFlow
	if creationFlags&testDataFlagMockFlow != 0 {
		flowMock = mocks.NewAggsenderFlow(t)
		sut.flow = flowMock
	}

	var compatibilityCheckerMock *mocksdb.CompatibilityChecker
	if creationFlags&testDataFlagMockCompatibilityChecker != 0 {
		compatibilityCheckerMock = mocksdb.NewCompatibilityChecker(t)
		sut.compatibilityStoragedChecker = compatibilityCheckerMock
	}

	var statusCheckerMock *mocks.CertificateStatusChecker
	if creationFlags&testDataFlagMockStatusChecker != 0 {
		statusCheckerMock = mocks.NewCertificateStatusChecker(t)
		sut.certStatusChecker = statusCheckerMock
	}

	return &aggsenderTestData{
		ctx:                     ctx,
		agglayerClientMock:      agglayerClientMock,
		l2BridgeQuerier:         l2BridgeQuerier,
		l1InfoQuerier:           l1InfoTreeQuerierMock,
		storageMock:             storageMock,
		epochNotifierMock:       epochNotifierMock,
		flowMock:                flowMock,
		compatibilityChekerMock: compatibilityCheckerMock,
		certStatusCheckerMock:   statusCheckerMock,
		sut:                     sut,
	}
}
