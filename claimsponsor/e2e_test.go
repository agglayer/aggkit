package claimsponsor_test

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"path"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/claimsponsor"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/helpers"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestE2EL1toEVML2(t *testing.T) {
	type tc struct {
		name          string
		maxGas        uint64
		expectSuccess bool
		mockAddErr    error
	}
	tests := []tc{
		{"sufficient gas", 200_000, true, nil},
		{"no-limit (maxGas=0)", 0, true, nil},
		{"insufficient gas", 1, false, nil},
		{"tx already claimed", 0, false, errors.New("execution reverted (0x646cf558)")},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			setup := helpers.NewE2EEnvWithEVML2(t, helpers.DefaultEnvironmentConfig())

			txMgrMock := setup.EthTxManagerMock
			if tt.mockAddErr != nil {
				txMgrMock = helpers.NewEthTxManager(t)
				txMgrMock.On("Add", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(common.Hash{}, tt.mockAddErr)
			}

			dbPath := path.Join(t.TempDir(), fmt.Sprintf("cs_%s.sqlite", tt.name))
			claimer, err := claimsponsor.NewEVMClaimSponsor(
				log.GetDefaultLogger(),
				dbPath,
				setup.L2Environment.SimBackend.Client(),
				setup.L2Environment.BridgeAddr,
				setup.L2Environment.Auth.From,
				tt.maxGas,
				0,
				txMgrMock,
				0, 0, // retry periods
				10*time.Millisecond, // waitTxToBeMinedPeriod
				10*time.Millisecond, // waitOnEmptyQueue
			)
			require.NoError(t, err)

			go claimer.Start(ctx)

			const idx = uint32(0)
			amount := big.NewInt(1)
			setup.L1Environment.Auth.Value = amount
			_, err = setup.L1Environment.BridgeContract.BridgeAsset(
				setup.L1Environment.Auth,
				setup.NetworkIDL2,
				setup.L2Environment.Auth.From,
				amount,
				common.Address{},
				true,
				nil,
			)
			require.NoError(t, err)
			setup.L1Environment.SimBackend.Commit()
			time.Sleep(300 * time.Millisecond) // wait for GER injection

			info, err := setup.L1Environment.InfoTreeSync.GetInfoByIndex(ctx, idx)
			require.NoError(t, err)
			localProof, err := setup.L1Environment.BridgeSync.GetProof(ctx, idx, info.MainnetExitRoot)
			require.NoError(t, err)
			rollupProof, err := setup.L1Environment.InfoTreeSync.GetRollupExitTreeMerkleProof(ctx, 0, common.Hash{})
			require.NoError(t, err)

			// Request to sponsor claim
			globalIndex := bridgesync.GenerateGlobalIndex(true, 0, idx)
			err = claimer.AddClaimToQueue(&claimsponsor.Claim{
				LeafType:            claimsponsor.LeafTypeAsset,
				ProofLocalExitRoot:  localProof,
				ProofRollupExitRoot: rollupProof,
				GlobalIndex:         globalIndex,
				MainnetExitRoot:     info.MainnetExitRoot,
				RollupExitRoot:      info.RollupExitRoot,
				OriginNetwork:       0,
				OriginTokenAddress:  common.Address{},
				DestinationNetwork:  setup.NetworkIDL2,
				DestinationAddress:  setup.L2Environment.Auth.From,
				Amount:              amount,
				Metadata:            nil,
			})
			require.NoError(t, err)

			if tt.expectSuccess {
				require.Eventually(t, func() bool {
					claim, err := claimer.GetClaim(globalIndex)
					if err != nil {
						return false
					}
					return claim.Status == claimsponsor.SuccessClaimStatus
				}, 5*time.Second, 100*time.Millisecond, "claim should succeed")

				// Check on contract that is claimed
				isClaimed, err := setup.L2Environment.BridgeContract.IsClaimed(
					&bind.CallOpts{Pending: false}, idx, 0,
				)
				require.NoError(t, err)
				require.True(t, isClaimed)
			} else {
				require.Eventually(t, func() bool {
					claim, err := claimer.GetClaim(globalIndex)
					return errors.Is(err, db.ErrNotFound) && claim == nil
				}, 5*time.Second, 100*time.Millisecond,
					"claim should be deleted when gas is too low")
			}
		})
	}
}
