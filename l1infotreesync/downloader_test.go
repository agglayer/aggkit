package l1infotreesync

import (
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/polygonzkevmglobalexitrootv2"
	mocks_l1infotreesync "github.com/agglayer/aggkit/l1infotreesync/mocks"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
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
			l1Client := mocks_l1infotreesync.NewEthClienter(t)
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
	l1Client := mocks_l1infotreesync.NewEthClienter(t)
	globalExitRoot := common.HexToAddress("0x1")
	rollupManager := common.HexToAddress("0x2")

	smcAbi, err := abi.JSON(strings.NewReader(polygonzkevmglobalexitrootv2.Polygonzkevmglobalexitrootv2ABI))
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
