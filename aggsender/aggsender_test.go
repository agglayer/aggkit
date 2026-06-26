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
	"path/filepath"
	"testing"
	"time"

	"github.com/agglayer/aggkit/agglayer"
	agglayermocks "github.com/agglayer/aggkit/agglayer/mocks"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/config"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/flows"
	"github.com/agglayer/aggkit/aggsender/mocks"
	triggertypes "github.com/agglayer/aggkit/aggsender/trigger/types"
	aggsendertypes "github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	bridgetypes "github.com/agglayer/aggkit/bridgesync/types"
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config/types"
	mocksdb "github.com/agglayer/aggkit/db/compatibility/mocks"
	"github.com/agglayer/aggkit/grpc"
	"github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
	aggkittypes "github.com/agglayer/aggkit/types"
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
		StoragePath:         "/path/to/storage",
		AgglayerClient:      agglayer.ClientConfig{GRPC: &grpc.ClientConfig{URL: "http://agglayer.url"}},
		AggsenderPrivateKey: signer.NewLocalSignerConfig("/path/to/key", "password"),
		URLRPCL2:            "http://l2.rpc.url",
		TriggerEpochBased: config.TriggerEpochBasedConfig{
			EpochNotificationPercentage: 50,
		},
		Mode:                       "PP",
		SovereignRollupAddr:        common.HexToAddress("0x1"),
		BlockFinalityForL1InfoTree: aggkittypes.FinalizedBlock,
		TriggerCertMode:            aggsendertypes.AutoTriggerMode,
	}

	expected := fmt.Sprintf("StoragePath: /path/to/storage\n"+
		"CertificatesDir: \n"+
		"AgglayerClient: %s\n"+
		"AggsenderPrivateKey: local\n"+
		"DryRun: false\n"+
		"EnableRPC: false\n"+
		"AggkitProverClient: none\n"+
		"Mode: PP\n"+
		"CheckStatusCertificateInterval: 0s\n"+
		"RetryCertAfterInError: false\n"+
		"SovereignRollupAddr: 0x0000000000000000000000000000000000000001\n"+
		"RequireNoFEPBlockGap: false\n"+
		"RetriesToBuildAndSendCertificate: RetryPolicyConfig{Mode: , Config: RetryDelaysConfig{Delays: [], MaxRetries: NO RETRIES}}\n"+
		"StorageRetainCertificatesPolicy: retain all certificates, keep history: false\n"+
		"BlockFinalityForL1InfoTree: FinalizedBlock\n"+
		"TriggerCertMode: Auto\nTriggerEpochBased: EpochNotificationPercentage: 50\n",
		config.AgglayerClient.String())

	require.Equal(t, expected, config.String())
}

func TestAggSenderStart(t *testing.T) {
	ctx := t.Context()
	aggLayerMock := agglayermocks.NewAgglayerClientMock(t)
	bridgeL2SyncerMock := mocks.NewL2BridgeSyncer(t)
	rollupQuerierMock := mocks.NewRollupDataQuerier(t)
	committeQuerierMock := mocks.NewMultisigQuerier(t)
	mockL1InfoTreeSyncer := mocks.NewL1InfoTreeSyncer(t)
	mockL1InfoTreeSyncer.EXPECT().Finality().Return(aggkittypes.FinalizedBlock).Maybe()
	sendTrigger := mocks.NewCertificateSendTrigger(t)
	sendTrigger.EXPECT().Setup(ctx)
	ch := make(chan aggsendertypes.CertificateTriggerEvent)
	sendTrigger.EXPECT().TriggerCh(ctx).Return(ch).Once()
	sendTrigger.EXPECT().Status().Return("test status").Once()
	sendTrigger.EXPECT().OnIdle().Maybe()
	bridgeL2SyncerMock.EXPECT().OriginNetwork().Return(uint32(2))
	bridgeL2SyncerMock.EXPECT().GetLastProcessedBlock(mock.Anything).Return(uint64(0), true, nil)
	aggLayerMock.EXPECT().GetLatestPendingCertificateHeader(mock.Anything, mock.Anything).Return(nil, nil).Twice()
	aggLayerMock.EXPECT().GetLatestSettledCertificateHeader(mock.Anything, mock.Anything).Return(nil, nil).Twice()
	rollupQuerierMock.EXPECT().GetRollupChainID().Return(uint64(1234), nil)
	committee, err := aggsendertypes.NewMultisigCommittee([]*aggsendertypes.SignerInfo{aggsendertypes.NewSignerInfo("", common.Address{})}, 1)
	require.NoError(t, err)
	committeQuerierMock.EXPECT().GetMultisigCommittee(mock.Anything, mock.Anything).Return(committee, nil).Once()
	aggSender, err := newAggsender(
		ctx,
		log.WithFields("test", "unittest"),
		config.Config{
			Mode:                aggsendertypes.PessimisticProofMode,
			StoragePath:         path.Join(t.TempDir(), "aggsenderTestAggSenderStart.sqlite"),
			DelayBetweenRetries: types.Duration{Duration: 1 * time.Microsecond},
			AggsenderPrivateKey: signertypes.SignerConfig{
				Method: signertypes.MethodNone,
			},
			BlockFinalityForL1InfoTree: aggkittypes.FinalizedBlock,
			TriggerCertMode:            aggsendertypes.AutoTriggerMode,
		},
		aggLayerMock,
		mockL1InfoTreeSyncer, // l1 info tree syncer
		bridgeL2SyncerMock,
		nil, // claim syncer
		nil, // l1 client
		nil, // l2 client
		rollupQuerierMock,
		committeQuerierMock,
		sendTrigger,
		bridgetypes.EmptyLER,
	)
	require.NoError(t, err)
	require.NotNil(t, aggSender)

	go aggSender.Start(ctx)
	ch <- triggertypes.EpochEvent{
		Epoch: 1,
	}
	time.Sleep(200 * time.Millisecond)

	aggLayerMock.AssertExpectations(t)
	bridgeL2SyncerMock.AssertExpectations(t)
	rollupQuerierMock.AssertExpectations(t)
	committeQuerierMock.AssertExpectations(t)
	sendTrigger.AssertExpectations(t)
}

func TestExploratoryGenerateCert(t *testing.T) {
	t.Skip("This test is only for exploratory purposes, to generate json format of the certificate")

	key, err := crypto.GenerateKey()
	require.NoError(t, err)

	signature, err := crypto.Sign(common.Hex2Bytes("0x1"), key)
	require.NoError(t, err)

	certificate := &agglayertypes.Certificate{
		NetworkID:         1,
		Height:            1,
		PrevLocalExitRoot: common.HexToHash("0x1"),
		NewLocalExitRoot:  common.HexToHash("0x2"),
		BridgeExits: []*agglayertypes.BridgeExit{
			{
				LeafType: bridgetypes.LeafTypeAsset,
				TokenInfo: &agglayertypes.TokenInfo{
					OriginNetwork:      1,
					OriginTokenAddress: common.HexToAddress("0x11"),
				},
				DestinationNetwork: 2,
				DestinationAddress: common.HexToAddress("0x22"),
				Amount:             big.NewInt(100),
				Metadata:           aggkitcommon.ZeroHash[:],
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
					LeafType: bridgetypes.LeafTypeAsset,
					TokenInfo: &agglayertypes.TokenInfo{
						OriginNetwork:      1,
						OriginTokenAddress: common.HexToAddress("0x11"),
					},
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x22"),
					Amount:             big.NewInt(100),
					Metadata:           []byte("metadata"),
				},
				ClaimData: &agglayertypes.ClaimFromMainnet{
					ProofLeafMER: &agglayertypes.MerkleProof{
						Root:  common.HexToHash("0x1"),
						Proof: [common.HashLength]common.Hash{},
					},
					ProofGERToL1Root: &agglayertypes.MerkleProof{
						Root:  common.HexToHash("0x3"),
						Proof: [common.HashLength]common.Hash{},
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
	mockAggLayerClient := agglayermocks.NewAgglayerClientMock(t)
	mockSendTrigger := mocks.NewCertificateSendTrigger(t)
	logger := log.WithFields("aggsender-test", "no claims test")
	signer := signer.NewLocalSignFromPrivateKey("ut", log.WithFields("aggsender", 1), privateKey, 0)
	mockLocalValidator := mocks.NewCertificateValidateAndSigner(t)
	mockLocalValidator.EXPECT().ValidateAndSignCertificate(ctx, mock.Anything, mock.Anything).Return(nil, nil).Once()
	mockValidatorPoller := mocks.NewValidatorPoller(t)
	mockValidatorPoller.EXPECT().PollValidators(ctx, mock.Anything).Return(&agglayertypes.Multisig{}, nil).Once()
	aggSender := &AggSender{
		log:                    logger,
		storage:                mockStorage,
		l2OriginNetwork:        1,
		aggLayerClient:         mockAggLayerClient,
		certificateSendTrigger: mockSendTrigger,
		cfg:                    config.Config{},
		validatorPoller:        mockValidatorPoller,
		localValidator:         mockLocalValidator,
		flow: flows.NewPPBuilderFlow(logger,
			flows.NewBaseFlow(logger, mockL2BridgeQuerier, mockStorage,
				mockL1Querier, bridgetypes.EmptyLER, nil, flows.NewBaseFlowConfigDefault()),
			mockStorage, mockL1Querier, mockL2BridgeQuerier, signer, true, 0, 0),
	}

	mockStorage.EXPECT().GetLastSentCertificateHeader().Return(&aggsendertypes.CertificateHeader{
		NewLocalExitRoot: common.HexToHash("0x123"),
		Height:           1,
		FromBlock:        0,
		ToBlock:          10,
		Status:           agglayertypes.Settled,
	}, nil).Once()
	mockStorage.EXPECT().SaveLastSentCertificate(mock.Anything, mock.Anything).Return(nil).Once()
	mockL2BridgeQuerier.EXPECT().GetLastProcessedBlock(mock.Anything).Return(uint64(50), true, nil)
	mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(mock.Anything, uint64(11), uint64(50)).Return([]bridgesync.Bridge{
		{
			BlockNum:           30,
			BlockPos:           0,
			LeafType:           bridgetypes.LeafTypeAsset.Uint8(),
			OriginNetwork:      1,
			OriginAddress:      common.HexToAddress("0x1"),
			DestinationNetwork: 2,
			DestinationAddress: common.HexToAddress("0x2"),
			Amount:             big.NewInt(100),
			Metadata:           []byte("metadata"),
			DepositCount:       1,
		},
	}, []claimsynctypes.Claim{}, nil).Once()
	mockL2BridgeQuerier.EXPECT().GetUnsetClaimsForBlockRange(mock.Anything, uint64(11), uint64(50)).Return([]claimsynctypes.Unclaim{}, nil).Once()
	mockL1Querier.EXPECT().GetTargetL1InfoRoot(ctx).Return(&treetypes.Root{}, nil, nil).Once()
	mockL2BridgeQuerier.EXPECT().GetExitRootByIndex(mock.Anything, uint32(1)).Return(common.Hash{}, nil).Once()
	mockL2BridgeQuerier.EXPECT().OriginNetwork().Return(uint32(1)).Once()
	mockAggLayerClient.EXPECT().SendCertificate(mock.Anything, mock.Anything).Return(common.Hash{}, nil).Once()
	mockSendTrigger.EXPECT().Status().Return("test runner status")
	signedCertificate, err := aggSender.sendCertificate(ctx)
	require.NoError(t, err)
	require.NotNil(t, signedCertificate)
	require.NotNil(t, signedCertificate.ImportedBridgeExits)
	require.Len(t, signedCertificate.BridgeExits, 1)

	mockStorage.AssertExpectations(t)
	mockL2BridgeQuerier.AssertExpectations(t)
	mockAggLayerClient.AssertExpectations(t)
	mockValidatorPoller.AssertExpectations(t)
	mockLocalValidator.AssertExpectations(t)
}

//nolint:dupl
func TestSendCertificate(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name            string
		mockFn          func(*mocks.AggSenderStorage, *mocks.AggsenderBuilderFlow, *agglayermocks.AgglayerClientMock)
		mockValidatorFn func() (*mocks.ValidatorPoller, *mocks.CertificateValidateAndSigner)
		expectedError   string
	}{
		{
			name: "error getting certificate build params",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockFlow *mocks.AggsenderBuilderFlow,
				mockAgglayerClient *agglayermocks.AgglayerClientMock) {
				mockFlow.EXPECT().GetCertificateBuildParams(mock.Anything).Return(nil, errors.New("some error")).Once()
			},
			expectedError: "error getting certificate build params",
		},
		{
			name: "no new blocks consumed",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockFlow *mocks.AggsenderBuilderFlow,
				mockAgglayerClient *agglayermocks.AgglayerClientMock) {
				mockFlow.EXPECT().GetCertificateBuildParams(mock.Anything).Return(nil, nil).Once()
			},
		},
		{
			name: "error building certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockFlow *mocks.AggsenderBuilderFlow,
				mockAgglayerClient *agglayermocks.AgglayerClientMock) {
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
				mockFlow *mocks.AggsenderBuilderFlow,
				mockAgglayerClient *agglayermocks.AgglayerClientMock) {
				mockFlow.EXPECT().GetCertificateBuildParams(mock.Anything).Return(&aggsendertypes.CertificateBuildParams{
					Bridges: []bridgesync.Bridge{{}},
				}, nil).Once()
				cert := &agglayertypes.Certificate{
					NetworkID:        1,
					Height:           0,
					NewLocalExitRoot: common.HexToHash("0x1"),
					BridgeExits:      []*agglayertypes.BridgeExit{{}},
				}
				mockFlow.EXPECT().BuildCertificate(mock.Anything, mock.Anything).Return(cert, nil).Once()
				mockFlow.EXPECT().UpdateAggchainData(cert, mock.Anything).Return(nil).Once()
				mockAgglayerClient.EXPECT().SendCertificate(mock.Anything, mock.Anything).Return(common.Hash{}, errors.New("some error")).Once()
				mockStorage.EXPECT().SaveNonAcceptedCertificate(mock.Anything, mock.Anything).Return(nil).Once()
			},
			mockValidatorFn: func() (*mocks.ValidatorPoller, *mocks.CertificateValidateAndSigner) {
				mockValidator := mocks.NewValidatorPoller(t)
				mockValidator.EXPECT().
					PollValidators(mock.Anything, mock.Anything).
					Return(&agglayertypes.Multisig{}, nil).Once()
				mockLocalValidator := mocks.NewCertificateValidateAndSigner(t)
				mockLocalValidator.EXPECT().ValidateAndSignCertificate(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()
				return mockValidator, mockLocalValidator
			},
			expectedError: "error sending certificate",
		},
		{
			name: "error saving certificate to storage",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockFlow *mocks.AggsenderBuilderFlow,
				mockAgglayerClient *agglayermocks.AgglayerClientMock) {
				mockFlow.EXPECT().GetCertificateBuildParams(mock.Anything).Return(&aggsendertypes.CertificateBuildParams{
					Bridges: []bridgesync.Bridge{{}},
				}, nil).Once()
				cert := &agglayertypes.Certificate{
					NetworkID:        11,
					Height:           0,
					NewLocalExitRoot: common.HexToHash("0x11"),
					BridgeExits:      []*agglayertypes.BridgeExit{{}},
				}
				mockFlow.EXPECT().BuildCertificate(mock.Anything, mock.Anything).Return(cert, nil).Once()
				mockFlow.EXPECT().UpdateAggchainData(cert, mock.Anything).Return(nil).Once()
				mockAgglayerClient.EXPECT().SendCertificate(mock.Anything, mock.Anything).Return(common.HexToHash("0x22"), nil).Once()
				mockStorage.EXPECT().SaveLastSentCertificate(mock.Anything, mock.Anything).Return(errors.New("some error")).Once()
			},
			mockValidatorFn: func() (*mocks.ValidatorPoller, *mocks.CertificateValidateAndSigner) {
				mockValidator := mocks.NewValidatorPoller(t)
				mockValidator.EXPECT().
					PollValidators(mock.Anything, mock.Anything).
					Return(&agglayertypes.Multisig{}, nil).Once()
				mockLocalValidator := mocks.NewCertificateValidateAndSigner(t)
				mockLocalValidator.EXPECT().ValidateAndSignCertificate(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()
				return mockValidator, mockLocalValidator
			},
			expectedError: "error saving last sent certificate",
		},
		{
			name: "error getting validator signature",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockFlow *mocks.AggsenderBuilderFlow,
				mockAgglayerClient *agglayermocks.AgglayerClientMock) {
				mockFlow.EXPECT().GetCertificateBuildParams(mock.Anything).Return(&aggsendertypes.CertificateBuildParams{
					Bridges: []bridgesync.Bridge{{}},
				}, nil).Once()
				mockFlow.EXPECT().BuildCertificate(mock.Anything, mock.Anything).Return(&agglayertypes.Certificate{
					NetworkID:        1,
					Height:           0,
					NewLocalExitRoot: common.HexToHash("0x1"),
					BridgeExits:      []*agglayertypes.BridgeExit{{}},
				}, nil).Once()
			},
			mockValidatorFn: func() (*mocks.ValidatorPoller, *mocks.CertificateValidateAndSigner) {
				mockLocalValidator := mocks.NewCertificateValidateAndSigner(t)
				mockLocalValidator.EXPECT().ValidateAndSignCertificate(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()
				mockValidator := mocks.NewValidatorPoller(t)
				mockValidator.EXPECT().
					PollValidators(mock.Anything, mock.Anything).
					Return(nil, errors.New("some error")).Once()
				return mockValidator, mockLocalValidator
			},
			expectedError: "error polling validator committee: some error",
		},
		{
			name: "successful validation and sending of a certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockFlow *mocks.AggsenderBuilderFlow,
				mockAgglayerClient *agglayermocks.AgglayerClientMock) {
				mockFlow.EXPECT().GetCertificateBuildParams(mock.Anything).Return(&aggsendertypes.CertificateBuildParams{
					Bridges: []bridgesync.Bridge{{}},
				}, nil).Once()
				cert := &agglayertypes.Certificate{
					NetworkID:        11,
					Height:           0,
					NewLocalExitRoot: common.HexToHash("0x11"),
					BridgeExits:      []*agglayertypes.BridgeExit{{}},
				}
				mockFlow.EXPECT().BuildCertificate(mock.Anything, mock.Anything).Return(cert, nil).Once()
				mockFlow.EXPECT().UpdateAggchainData(cert, mock.Anything).Return(nil).Once()
				mockAgglayerClient.EXPECT().SendCertificate(mock.Anything, mock.Anything).
					Return(common.HexToHash("0x22"), nil).Once()
				mockStorage.EXPECT().SaveLastSentCertificate(mock.Anything, mock.Anything).Return(nil).Once()
			},
			mockValidatorFn: func() (*mocks.ValidatorPoller, *mocks.CertificateValidateAndSigner) {
				mockValidator := mocks.NewValidatorPoller(t)
				mockValidator.EXPECT().PollValidators(mock.Anything, mock.Anything).
					Return(&agglayertypes.Multisig{}, nil).Once()
				mockLocalValidator := mocks.NewCertificateValidateAndSigner(t)
				mockLocalValidator.EXPECT().ValidateAndSignCertificate(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()
				return mockValidator, mockLocalValidator
			},
		},
		{
			name: "successful sending and saving of a certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockFlow *mocks.AggsenderBuilderFlow,
				mockAgglayerClient *agglayermocks.AgglayerClientMock) {
				mockFlow.EXPECT().GetCertificateBuildParams(mock.Anything).Return(&aggsendertypes.CertificateBuildParams{
					Bridges: []bridgesync.Bridge{{}},
				}, nil).Once()
				cert := &agglayertypes.Certificate{
					NetworkID:        11,
					Height:           0,
					NewLocalExitRoot: common.HexToHash("0x11"),
					BridgeExits:      []*agglayertypes.BridgeExit{{}},
				}
				mockFlow.EXPECT().BuildCertificate(mock.Anything, mock.Anything).Return(cert, nil).Once()
				mockFlow.EXPECT().UpdateAggchainData(cert, mock.Anything).Return(nil).Once()
				mockAgglayerClient.EXPECT().SendCertificate(mock.Anything, mock.Anything).Return(common.HexToHash("0x22"), nil).Once()
				mockStorage.EXPECT().SaveLastSentCertificate(mock.Anything, mock.Anything).Return(nil).Once()
			},
			mockValidatorFn: func() (*mocks.ValidatorPoller, *mocks.CertificateValidateAndSigner) {
				mockValidator := mocks.NewValidatorPoller(t)
				mockValidator.EXPECT().
					PollValidators(mock.Anything, mock.Anything).
					Return(&agglayertypes.Multisig{}, nil).Once()
				mockLocalValidator := mocks.NewCertificateValidateAndSigner(t)
				mockLocalValidator.EXPECT().ValidateAndSignCertificate(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()
				return mockValidator, mockLocalValidator
			},
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockStorage := mocks.NewAggSenderStorage(t)
			mockAggsenderFlow := mocks.NewAggsenderBuilderFlow(t)
			mockAgglayerClient := agglayermocks.NewAgglayerClientMock(t)
			mockSendTrigger := mocks.NewCertificateSendTrigger(t)
			tt.mockFn(mockStorage, mockAggsenderFlow, mockAgglayerClient)

			logger := log.WithFields("aggsender-test", "sendCertificate")

			aggsender := &AggSender{
				log:                    logger,
				storage:                mockStorage,
				certificateSendTrigger: mockSendTrigger,
				flow:                   mockAggsenderFlow,
				aggLayerClient:         mockAgglayerClient,
				cfg: config.Config{
					MaxRetriesStoreCertificate: 1,
				},
			}

			if tt.mockValidatorFn != nil {
				aggsender.validatorPoller, aggsender.localValidator = tt.mockValidatorFn()
			}

			mockSendTrigger.EXPECT().Status().Return("test send trigger status")
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
	mockAgglayerClient := agglayermocks.NewAgglayerClientMock(t)
	mockAgglayerClient.EXPECT().GetEpochConfiguration(t.Context()).Return(&agglayertypes.ClockConfiguration{
		EpochDuration: 10,
		GenesisBlock:  1000,
	}, nil).Once()
	mockBridgeSyncer := mocks.NewL2BridgeSyncer(t)
	mockRollupQuerier := mocks.NewRollupDataQuerier(t)
	mockCommitteeQuerier := mocks.NewMultisigQuerier(t)

	mockL1InfoTreeSyncer := mocks.NewL1InfoTreeSyncer(t)
	mockL1InfoTreeSyncer.EXPECT().Finality().Return(aggkittypes.FinalizedBlock).Maybe()

	mockBridgeSyncer.EXPECT().OriginNetwork().Return(uint32(1)).Times(2)
	mockRollupQuerier.EXPECT().GetRollupChainID().Return(uint64(1234), nil)
	committee, err := aggsendertypes.NewMultisigCommittee([]*aggsendertypes.SignerInfo{aggsendertypes.NewSignerInfo("", common.Address{})},
		1)
	require.NoError(t, err)
	mockCommitteeQuerier.EXPECT().GetMultisigCommittee(mock.Anything, mock.Anything).Return(committee, nil).Once()
	mockCommitteeQuerier.EXPECT().ResolveAutoMode(mock.Anything).Return(aggsendertypes.PessimisticProofMode, nil).Once()
	sut, err := New(t.Context(), log.WithFields("module", "ut"),
		config.Config{
			AggsenderPrivateKey: signertypes.SignerConfig{
				Method: signertypes.MethodNone,
			},
			Mode:                       aggsendertypes.PessimisticProofMode,
			BlockFinalityForL1InfoTree: aggkittypes.FinalizedBlock,
			TriggerCertMode:            aggsendertypes.AutoTriggerMode,
		},
		mockAgglayerClient,
		mockL1InfoTreeSyncer, // l1 info tree syncer
		mockBridgeSyncer,
		nil, // claim syncer
		nil, // l1 client
		nil, // l2 client
		mockRollupQuerier,
		mockCommitteeQuerier,
		bridgetypes.EmptyLER,
	)
	require.NoError(t, err)
	require.NotNil(t, sut)
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
	mockInitialBlockSetter := mocks.NewInitialBlockClaimSyncerSetter(t)
	mockInitialBlockSetter.EXPECT().SetClaimSyncerNextRequiredBlock(mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	testData.sut.initialBlockClaimSyncerSetter = mockInitialBlockSetter
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

func TestRetrySendCertificates(t *testing.T) {
	mockCertStatusChecker := mocks.NewCertificateStatusChecker(t)
	mockSendTrigger := mocks.NewCertificateSendTrigger(t)
	mockStorage := mocks.NewAggSenderStorage(t)
	mockFlow := mocks.NewAggsenderBuilderFlow(t)

	logger := log.WithFields("aggsender-test", "TestSendCertificates")
	aggSender := &AggSender{
		log:                    logger,
		certStatusChecker:      mockCertStatusChecker,
		certificateSendTrigger: mockSendTrigger,
		storage:                mockStorage,
		flow:                   mockFlow,
		cfg: config.Config{
			RetryCertAfterInError:          true,
			CheckStatusCertificateInterval: types.NewDuration(0),
			RetriesToBuildAndSendCertificate: aggkitcommon.RetryPolicyGenericConfig{
				Mode:       aggkitcommon.RetryConfigModeDelays,
				MaxRetries: 1,
				Delays: []types.Duration{{Duration: time.Millisecond},
					{Duration: time.Millisecond},
				},
			},
		},
		status: &aggsendertypes.AggsenderStatus{},
	}

	ctx := t.Context()

	chEpoch := make(chan aggsendertypes.CertificateTriggerEvent)
	mockSendTrigger.EXPECT().TriggerCh(mock.Anything).Return(chEpoch).Once()
	expectedNumAttempts := 4
	mockCertStatusChecker.EXPECT().CheckPeriodicallyStatus(mock.Anything, mock.Anything).Return(aggsendertypes.CertStatus{
		ExistPendingCerts:   false,
		ExistNewInErrorCert: false,
	}, nil).Times(2)
	mockSendTrigger.EXPECT().Status().Return("send trigger status").Times(expectedNumAttempts)
	mockFlow.EXPECT().GetCertificateBuildParams(mock.Anything).
		Return(nil, errors.New("err")).Times(expectedNumAttempts)
	mockSendTrigger.EXPECT().OnIdle().Maybe()
	go func() {
		fmt.Println("send epoch 1")
		chEpoch <- triggertypes.EpochEvent{Epoch: 1}
		fmt.Println("send epoch 2")
		chEpoch <- triggertypes.EpochEvent{Epoch: 2}
	}()
	aggSender.sendCertificates(ctx, 2)
}

func TestSendEpochBasedCertificates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mockFn func(
			*mocks.CertificateStatusChecker,
			*mocks.AggSenderStorage,
			*mocks.AggsenderBuilderFlow,
			*mocks.CertificateSendTrigger)
		returnAfterNIterations  int
		certStatusCheckInterval time.Duration
		needTimeoutCancel       bool
	}{
		{
			name: "fails CheckPeriodicallyStatus",
			mockFn: func(mockCertStatusChecker *mocks.CertificateStatusChecker, mockStorage *mocks.AggSenderStorage, mockFlow *mocks.AggsenderBuilderFlow, mockSendTrigger *mocks.CertificateSendTrigger) {
				chEpoch := make(chan aggsendertypes.CertificateTriggerEvent, 1)
				chEpoch <- triggertypes.EpochEvent{Epoch: 1}
				mockSendTrigger.EXPECT().TriggerCh(mock.Anything).Return(chEpoch).Once()
				mockSendTrigger.EXPECT().OnIdle().Maybe()
				mockCertStatusChecker.EXPECT().CheckPeriodicallyStatus(mock.Anything, mock.Anything).Return(aggsendertypes.CertStatus{}, errors.New("some error")).Once()
			},
			returnAfterNIterations: 1,
		},
		{
			name: "context canceled",
			mockFn: func(mockCertStatusChecker *mocks.CertificateStatusChecker, mockStorage *mocks.AggSenderStorage, mockFlow *mocks.AggsenderBuilderFlow, mockSendTrigger *mocks.CertificateSendTrigger) {
				mockSendTrigger.EXPECT().TriggerCh(mock.Anything).Return(make(chan aggsendertypes.CertificateTriggerEvent)).Once()
				mockSendTrigger.EXPECT().OnIdle().Maybe()
			},
			returnAfterNIterations: 0,
			needTimeoutCancel:      true,
		},
		{
			name: "retry certificate after in-error",
			mockFn: func(mockCertStatusChecker *mocks.CertificateStatusChecker, mockStorage *mocks.AggSenderStorage, mockFlow *mocks.AggsenderBuilderFlow, mockSendTrigger *mocks.CertificateSendTrigger) {
				mockCertStatusChecker.EXPECT().CheckPeriodicallyStatus(mock.Anything, mock.Anything).Return(aggsendertypes.CertStatus{
					ExistPendingCerts:   false,
					ExistNewInErrorCert: true,
				}, nil).Once()
				mockSendTrigger.EXPECT().TriggerCh(mock.Anything).Return(make(chan aggsendertypes.CertificateTriggerEvent)).Once()
				mockSendTrigger.EXPECT().OnIdle().Maybe()
				mockSendTrigger.EXPECT().Status().Return("test status").Once()
				mockFlow.EXPECT().GetCertificateBuildParams(mock.Anything).Return(nil, nil).Once()
			},
			returnAfterNIterations:  1,
			certStatusCheckInterval: 100 * time.Millisecond,
			needTimeoutCancel:       true,
		},
		{
			name: "epoch received with no pending certificates",
			mockFn: func(mockCertStatusChecker *mocks.CertificateStatusChecker, mockStorage *mocks.AggSenderStorage, mockFlow *mocks.AggsenderBuilderFlow, mockSendTrigger *mocks.CertificateSendTrigger) {
				chEpoch := make(chan aggsendertypes.CertificateTriggerEvent, 1)
				chEpoch <- triggertypes.EpochEvent{Epoch: 1}
				mockSendTrigger.EXPECT().TriggerCh(mock.Anything).Return(chEpoch).Once()
				mockSendTrigger.EXPECT().OnIdle().Maybe()
				mockSendTrigger.EXPECT().Status().Return("test status").Once()
				mockCertStatusChecker.EXPECT().CheckPeriodicallyStatus(mock.Anything, mock.Anything).Return(aggsendertypes.CertStatus{
					ExistPendingCerts: false,
				}, nil).Once()
				mockFlow.EXPECT().GetCertificateBuildParams(mock.Anything).Return(nil, nil).Once()
			},
			returnAfterNIterations: 1,
			needTimeoutCancel:      true,
		},
		{
			name: "epoch received with pending certificates",
			mockFn: func(mockCertStatusChecker *mocks.CertificateStatusChecker, mockStorage *mocks.AggSenderStorage, mockFlow *mocks.AggsenderBuilderFlow, mockSendTrigger *mocks.CertificateSendTrigger) {
				chEpoch := make(chan aggsendertypes.CertificateTriggerEvent, 1)
				chEpoch <- triggertypes.EpochEvent{Epoch: 1}
				mockSendTrigger.EXPECT().TriggerCh(mock.Anything).Return(chEpoch).Once()
				mockSendTrigger.EXPECT().OnIdle().Maybe()
				mockCertStatusChecker.EXPECT().CheckPeriodicallyStatus(mock.Anything, mock.Anything).Return(aggsendertypes.CertStatus{
					ExistPendingCerts: true,
				}, nil).Once()
			},
			returnAfterNIterations: 1,
			needTimeoutCancel:      true,
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockCertStatusChecker := mocks.NewCertificateStatusChecker(t)
			mockStorage := mocks.NewAggSenderStorage(t)
			mockFlow := mocks.NewAggsenderBuilderFlow(t)
			mockSendTrigger := mocks.NewCertificateSendTrigger(t)

			tt.mockFn(mockCertStatusChecker, mockStorage, mockFlow, mockSendTrigger)

			logger := log.WithFields("aggsender-test", tt.name)
			aggSender := &AggSender{
				log:               logger,
				certStatusChecker: mockCertStatusChecker,
				storage:           mockStorage,
				flow:              mockFlow,
				cfg: config.Config{
					RetryCertAfterInError:          true,
					CheckStatusCertificateInterval: types.NewDuration(tt.certStatusCheckInterval),
				},
				status:                 &aggsendertypes.AggsenderStatus{},
				certificateSendTrigger: mockSendTrigger,
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.needTimeoutCancel {
				go func() {
					time.Sleep(300 * time.Millisecond)
					cancel()
				}()
			}

			aggSender.sendCertificates(ctx, tt.returnAfterNIterations)

			mockCertStatusChecker.AssertExpectations(t)
			mockStorage.AssertExpectations(t)
			mockSendTrigger.AssertExpectations(t)
			mockFlow.AssertExpectations(t)
		})
	}
}

type testDataFlags = int

const (
	_                                    testDataFlags = 0
	testDataFlagMockStorage              testDataFlags = 1
	testDataFlagMockFlow                 testDataFlags = 2
	testDataFlagMockCompatibilityChecker testDataFlags = 4
	testDataFlagMockStatusChecker        testDataFlags = 8
)

type aggsenderTestData struct {
	ctx                     context.Context
	agglayerClientMock      *agglayermocks.AgglayerClientMock
	l1InfoQuerier           *mocks.L1InfoTreeDataQuerier
	l2BridgeQuerier         *mocks.BridgeQuerier
	storageMock             *mocks.AggSenderStorage
	flowMock                *mocks.AggsenderBuilderFlow
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
			LeafType:      bridgetypes.LeafTypeAsset.Uint8(),
			OriginNetwork: 1,
		})
	}
	return res
}

func NewClaimData(t *testing.T, num int, blockNum []uint64) []claimsynctypes.Claim {
	t.Helper()
	if num == 0 {
		num = len(blockNum)
	}
	res := make([]claimsynctypes.Claim, 0)
	for i := 0; i < num; i++ {
		res = append(res, claimsynctypes.Claim{
			BlockNum: blockNum[i%len(blockNum)],
			BlockPos: 0,
		})
	}
	return res
}

func newAggsenderTestData(t *testing.T, creationFlags testDataFlags) *aggsenderTestData {
	t.Helper()
	l2BridgeQuerier := mocks.NewBridgeQuerier(t)
	agglayerClientMock := agglayermocks.NewAgglayerClientMock(t)
	l1InfoTreeQuerierMock := mocks.NewL1InfoTreeDataQuerier(t)
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
			DBPath:                   dbPath,
			CertificatesDir:          filepath.Join(filepath.Dir(dbPath), "certificates"),
			RetainCertificatesPolicy: *db.NewStorageRetainCertificatesPolicyDefault(),
		}
		storage, err = db.NewAggSenderSQLStorage(logger, storageConfig)
		require.NoError(t, err)
	}
	privKey, err := ecdsa.GenerateKey(crypto.S256(), rand.Reader)
	require.NoError(t, err)
	signer := signer.NewLocalSignFromPrivateKey("ut", logger, privKey, 0)
	ctx := context.TODO()

	sut := &AggSender{
		log:             logger,
		l2OriginNetwork: networkIDTest,
		aggLayerClient:  agglayerClientMock,
		storage:         storage,
		status:          &aggsendertypes.AggsenderStatus{},
		cfg: config.Config{
			MaxCertSize:         1024 * 1024,
			DelayBetweenRetries: types.Duration{Duration: time.Millisecond},
		},
		flow: flows.NewPPBuilderFlow(logger,
			flows.NewBaseFlow(logger, l2BridgeQuerier, storage,
				l1InfoTreeQuerierMock, bridgetypes.EmptyLER, nil, flows.NewBaseFlowConfigDefault()),
			storage, l1InfoTreeQuerierMock, l2BridgeQuerier, signer, true, 0, 0),
	}
	var flowMock *mocks.AggsenderBuilderFlow
	if creationFlags&testDataFlagMockFlow != 0 {
		flowMock = mocks.NewAggsenderBuilderFlow(t)
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
		flowMock:                flowMock,
		compatibilityChekerMock: compatibilityCheckerMock,
		certStatusCheckerMock:   statusCheckerMock,
		sut:                     sut,
	}
}
