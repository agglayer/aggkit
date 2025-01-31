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
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/helpers"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestE2EL1toEVML2(t *testing.T) {
	// start other needed components
	ctx := context.Background()
	setup := helpers.NewE2EEnvWithEVML2(t)

	// start claim sponsor
	dbPathClaimSponsor := path.Join(t.TempDir(), "claimsponsorTestE2EL1toEVML2_cs.sqlite")
	claimer, err := claimsponsor.NewEVMClaimSponsor(
		log.GetDefaultLogger(),
		dbPathClaimSponsor,
		setup.L2Environment.SimBackend.Client(),
		setup.L2Environment.BridgeAddr,
		setup.L2Environment.Auth.From,
		200_000,
		0,
		setup.EthTxManagerMock,
		0, 0, time.Millisecond*10, time.Millisecond*10,
	)
	require.NoError(t, err)
	go claimer.Start(ctx)

	// test
	for i := uint32(0); i < 3; i++ {
		// Send bridges to L2, wait for GER to be injected on L2
		amount := new(big.Int).SetUint64(uint64(i) + 1)
		setup.L1Environment.Auth.Value = amount
		_, err := setup.L1Environment.BridgeContract.BridgeAsset(setup.L1Environment.Auth, setup.NetworkIDL2, setup.L2Environment.Auth.From, amount, common.Address{}, true, nil)
		require.NoError(t, err)
		setup.L1Environment.SimBackend.Commit()
		time.Sleep(time.Millisecond * 300)

		expectedGER, err := setup.L1Environment.GERContract.GetLastGlobalExitRoot(&bind.CallOpts{Pending: false})
		require.NoError(t, err)
		isInjected, err := setup.L2Environment.AggoracleSender.IsGERInjected(expectedGER)
		require.NoError(t, err)
		require.True(t, isInjected, fmt.Sprintf("iteration %d, GER: %s", i, common.Bytes2Hex(expectedGER[:])))

		// Build MP using bridgeSyncL1 & env.InfoTreeSync
		info, err := setup.L1Environment.InfoTreeSync.GetInfoByIndex(ctx, i)
		require.NoError(t, err)

		localProof, err := setup.L1Environment.BridgeSync.GetProof(ctx, i, info.MainnetExitRoot)
		require.NoError(t, err)

		rollupProof, err := setup.L1Environment.InfoTreeSync.GetRollupExitTreeMerkleProof(ctx, 0, common.Hash{})
		require.NoError(t, err)

		// Request to sponsor claim
		globalIndex := bridgesync.GenerateGlobalIndex(true, 0, i)
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

		// Wait until success
		succeed := false
		for i := 0; i < 10; i++ {
			claim, err := claimer.GetClaim(globalIndex)
			require.NoError(t, err)
			if claim.Status == claimsponsor.FailedClaimStatus {
				require.NoError(t, errors.New("claim failed"))
			} else if claim.Status == claimsponsor.SuccessClaimStatus {
				succeed = true

				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		require.True(t, succeed)

		// Check on contract that is claimed
		isClaimed, err := setup.L2Environment.BridgeContract.IsClaimed(&bind.CallOpts{Pending: false}, i, 0)
		require.NoError(t, err)
		require.True(t, isClaimed)
	}
}
