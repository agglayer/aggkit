package l1infotreesync

import (
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerger"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayermanager"
	"github.com/agglayer/aggkit/sync"
	aggkittypesmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestBuildAppender(t *testing.T) {
	tests := []struct {
		name        string
		flags       CreationFlags
		mockError   error
		expectError bool
	}{
		{
			name:        "ErrorOnBadContractAddr",
			flags:       FlagNone,
			mockError:   errors.New("test-error"),
			expectError: true,
		},
		{
			name:        "BypassBadContractAddr",
			flags:       FlagAllowWrongContractsAddrs,
			mockError:   nil,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l1Client := aggkittypesmocks.NewEthClienter(t)
			globalExitRoot := common.HexToAddress("0x1")
			rollupManager := common.HexToAddress("0x2")
			if tt.flags == FlagNone {
				l1Client.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).Return(nil, tt.mockError).Twice()
			}
			_, err := buildAppender(l1Client, globalExitRoot, rollupManager, tt.flags)
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestBuildAppenderVerifiedContractAddr(t *testing.T) {
	l1Client := aggkittypesmocks.NewEthClienter(t)
	globalExitRoot := common.HexToAddress("0x1")
	rollupManager := common.HexToAddress("0x2")

	smcAbi, err := abi.JSON(strings.NewReader(agglayerger.AgglayergerABI))
	require.NoError(t, err)
	bigInt := big.NewInt(1)
	returnGER, err := smcAbi.Methods["depositCount"].Outputs.Pack(bigInt)
	require.NoError(t, err)
	l1Client.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).Return(returnGER, nil).Once()
	v := common.HexToAddress("0x1234")
	returnRM, err := smcAbi.Methods["bridgeAddress"].Outputs.Pack(v)
	require.NoError(t, err)
	l1Client.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).Return(returnRM, nil).Once()
	flags := FlagNone
	_, err = buildAppender(l1Client, globalExitRoot, rollupManager, flags)
	require.NoError(t, err)
}

func TestAppenderVerifyPessimisticStateTransition(t *testing.T) {
	l1Client := aggkittypesmocks.NewEthClienter(t)
	appender, err := buildAppender(
		l1Client,
		common.HexToAddress("0x1"),
		common.HexToAddress("0x2"),
		FlagAllowWrongContractsAddrs,
	)
	require.NoError(t, err)

	rollupID := uint32(3)
	newLocalExitRoot := common.HexToHash("0xabc123")
	trustedAggregator := common.HexToAddress("0xdead")

	smcAbi, err := abi.JSON(strings.NewReader(agglayermanager.AgglayermanagerABI))
	require.NoError(t, err)
	event := smcAbi.Events["VerifyPessimisticStateTransition"]
	// Non-indexed fields, in ABI order: prevPessimisticRoot, newPessimisticRoot,
	// prevLocalExitRoot, newLocalExitRoot, l1InfoRoot.
	data, err := event.Inputs.NonIndexed().Pack(
		common.HexToHash("0x01"), // prevPessimisticRoot
		common.HexToHash("0x02"), // newPessimisticRoot
		common.HexToHash("0x03"), // prevLocalExitRoot
		newLocalExitRoot,         // newLocalExitRoot
		common.HexToHash("0x05"), // l1InfoRoot
	)
	require.NoError(t, err)

	log := types.Log{
		Index: 7,
		Topics: []common.Hash{
			verifyPessimisticStateTransitionSignature,
			common.BigToHash(big.NewInt(int64(rollupID))), // indexed rollupID
			common.BytesToHash(trustedAggregator.Bytes()), // indexed trustedAggregator
		},
		Data: data,
	}

	block := &sync.EVMBlock{}
	err = appender[verifyPessimisticStateTransitionSignature](block, log)
	require.NoError(t, err)
	require.Len(t, block.Events, 1)

	ev, ok := block.Events[0].(Event)
	require.True(t, ok)
	decoded := ev.VerifyBatches
	require.NotNil(t, decoded)
	require.Equal(t, rollupID, decoded.RollupID)
	require.Equal(t, uint64(log.Index), decoded.BlockPosition)
	require.Equal(t, newLocalExitRoot, decoded.ExitRoot)
	require.Equal(t, trustedAggregator, decoded.Aggregator)
	// No batch/state-root concept in the pessimistic path.
	require.Zero(t, decoded.NumBatch)
	require.Equal(t, common.Hash{}, decoded.StateRoot)
}
