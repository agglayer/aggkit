package claimer

import (
	"context"
	"testing"

	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	exitcertificate "github.com/agglayer/aggkit/tools/exit_certificate"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestCheckOK(t *testing.T) {
	t.Parallel()

	cert, err := LoadCertificate(writeSampleCert(t))
	require.NoError(t, err)

	claimer, _ := buildTestClaimer(t, cert.NewLocalExitRoot)
	require.NoError(t, claimer.Check(context.Background()))
}

func TestCheckNotSettled(t *testing.T) {
	t.Parallel()

	claimer, _ := buildTestClaimer(t, common.HexToHash("0xdeadbeef"))
	require.ErrorIs(t, claimer.Check(context.Background()), ErrLocalExitRootNotSettled)
}

func TestCheckNilWaitResult(t *testing.T) {
	t.Parallel()

	cert, err := LoadCertificate(writeSampleCert(t))
	require.NoError(t, err)

	claimer := NewClaimer(log.GetDefaultLogger(), cert, &fakeLocalTree{}, &fakeL1{}, 0, nil)
	require.ErrorContains(t, claimer.Check(context.Background()), "no updateL1InfoTree event")
}

func TestBuildClaimParamsDepositCountFilter(t *testing.T) {
	t.Parallel()

	cert, err := LoadCertificate(writeSampleCert(t))
	require.NoError(t, err)
	destAddr := cert.Leaves[0].DestinationAddress

	claimer, _ := buildTestClaimer(t, cert.NewLocalExitRoot)

	// Leaf 0 maps to deposit count 5 (offset +5 in buildTestClaimer); a non-matching filter yields none.
	other := uint32(99)
	claims, err := claimer.BuildClaimParams(context.Background(), destAddr, &other)
	require.NoError(t, err)
	require.Empty(t, claims)

	// The exact deposit count returns just that exit.
	match := uint32(5)
	claims, err = claimer.BuildClaimParams(context.Background(), destAddr, &match)
	require.NoError(t, err)
	require.Len(t, claims, 1)
	require.Equal(t, uint32(5), claims[0].DepositCount)
}

func TestBuildClaimParamsNilWaitResult(t *testing.T) {
	t.Parallel()

	cert, err := LoadCertificate(writeSampleCert(t))
	require.NoError(t, err)

	claimer := NewClaimer(log.GetDefaultLogger(), cert, &fakeLocalTree{}, &fakeL1{}, 0, nil)
	_, err = claimer.BuildClaimParams(context.Background(), cert.Leaves[0].DestinationAddress, nil)
	require.ErrorContains(t, err, "wait result is nil")
}

func TestListBridgesLeafNotFound(t *testing.T) {
	t.Parallel()

	cert, err := LoadCertificate(writeSampleCert(t))
	require.NoError(t, err)

	// An empty local tree resolves no deposit counts.
	l1 := &fakeL1{
		leaf: &l1infotreesync.L1InfoTreeLeaf{},
	}
	waitResult := &exitcertificate.StepWaitResult{
		UpdateL1InfoTree: &exitcertificate.L1InfoTreeUpdate{},
	}
	claimer := NewClaimer(log.GetDefaultLogger(), cert, &fakeLocalTree{}, l1, 0, waitResult)

	_, err = claimer.ListBridges(cert.Leaves[0].DestinationAddress)
	require.ErrorContains(t, err, "not found in local exit tree")
}

func TestNetworkIDOverride(t *testing.T) {
	t.Parallel()

	cert, err := LoadCertificate(writeSampleCert(t))
	require.NoError(t, err)

	// An explicit non-zero networkID overrides the certificate's network_id.
	claimer := NewClaimer(log.GetDefaultLogger(), cert, &fakeLocalTree{}, &fakeL1{}, 42, nil)
	require.Equal(t, uint32(42), claimer.NetworkID())
}

func TestSettlementWaitResultGetter(t *testing.T) {
	t.Parallel()

	cert, err := LoadCertificate(writeSampleCert(t))
	require.NoError(t, err)

	waitResult := &exitcertificate.StepWaitResult{}
	claimer := NewClaimer(log.GetDefaultLogger(), cert, &fakeLocalTree{}, &fakeL1{}, 0, waitResult)
	require.Same(t, waitResult, claimer.SettlementWaitResult())
}
