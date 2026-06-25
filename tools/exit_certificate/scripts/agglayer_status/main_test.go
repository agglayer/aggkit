package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/agglayer/mocks"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func ptrUint64(v uint64) *uint64 { return &v }

func ptrStatus(s agglayertypes.CertificateStatus) *agglayertypes.CertificateStatus { return &s }

// fullHeader returns a header with every optional field populated so printHeader
// exercises all of its branches.
func fullHeader() *agglayertypes.CertificateHeader {
	prev := common.HexToHash("0x01")
	settleTx := common.HexToHash("0x02")
	return &agglayertypes.CertificateHeader{
		NetworkID:             1,
		Height:                7,
		EpochNumber:           ptrUint64(3),
		CertificateIndex:      ptrUint64(4),
		CertificateID:         common.HexToHash("0xaa"),
		PreviousLocalExitRoot: &prev,
		NewLocalExitRoot:      common.HexToHash("0xbb"),
		Status:                agglayertypes.Settled,
		SettlementTxHash:      &settleTx,
		Error:                 errors.New("boom"),
	}
}

func TestLatestHeader(t *testing.T) {
	t.Parallel()

	pending := &agglayertypes.CertificateHeader{Height: 10, Status: agglayertypes.Pending}
	settled := &agglayertypes.CertificateHeader{Height: 9, Status: agglayertypes.Settled}
	errPending := errors.New("pending failed")
	errSettled := errors.New("settled failed")

	tests := []struct {
		name          string
		setup         func(m *mocks.AgglayerClientMock)
		expectedHdr   *agglayertypes.CertificateHeader
		expectedLabel string
		expectedErr   string
	}{
		{
			name: "pending found",
			setup: func(m *mocks.AgglayerClientMock) {
				m.EXPECT().GetLatestPendingCertificateHeader(mockCtx(), uint32(1)).Return(pending, nil)
			},
			expectedHdr:   pending,
			expectedLabel: "Latest certificate (pending):",
		},
		{
			name: "no pending, settled found",
			setup: func(m *mocks.AgglayerClientMock) {
				m.EXPECT().GetLatestPendingCertificateHeader(mockCtx(), uint32(1)).Return(nil, nil)
				m.EXPECT().GetLatestSettledCertificateHeader(mockCtx(), uint32(1)).Return(settled, nil)
			},
			expectedHdr:   settled,
			expectedLabel: "Latest certificate (settled):",
		},
		{
			name: "no certificate at all",
			setup: func(m *mocks.AgglayerClientMock) {
				m.EXPECT().GetLatestPendingCertificateHeader(mockCtx(), uint32(1)).Return(nil, nil)
				m.EXPECT().GetLatestSettledCertificateHeader(mockCtx(), uint32(1)).Return(nil, nil)
			},
			expectedHdr:   nil,
			expectedLabel: "",
		},
		{
			name: "pending query fails",
			setup: func(m *mocks.AgglayerClientMock) {
				m.EXPECT().GetLatestPendingCertificateHeader(mockCtx(), uint32(1)).Return(nil, errPending)
			},
			expectedErr: "get latest pending certificate header: pending failed",
		},
		{
			name: "settled query fails",
			setup: func(m *mocks.AgglayerClientMock) {
				m.EXPECT().GetLatestPendingCertificateHeader(mockCtx(), uint32(1)).Return(nil, nil)
				m.EXPECT().GetLatestSettledCertificateHeader(mockCtx(), uint32(1)).Return(nil, errSettled)
			},
			expectedErr: "get latest settled certificate header: settled failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client := mocks.NewAgglayerClientMock(t)
			tt.setup(client)

			hdr, label, err := latestHeader(context.Background(), client, 1)
			if tt.expectedErr != "" {
				require.EqualError(t, err, tt.expectedErr)
				require.Nil(t, hdr)
				require.Empty(t, label)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.expectedHdr, hdr)
			require.Equal(t, tt.expectedLabel, label)
		})
	}
}

func TestPrintStatus(t *testing.T) {
	t.Parallel()

	t.Run("no certificate", func(t *testing.T) {
		t.Parallel()
		client := mocks.NewAgglayerClientMock(t)
		client.EXPECT().GetLatestPendingCertificateHeader(mockCtx(), uint32(2)).Return(nil, nil)
		client.EXPECT().GetLatestSettledCertificateHeader(mockCtx(), uint32(2)).Return(nil, nil)

		require.NoError(t, printStatus(context.Background(), client, 2))
	})

	t.Run("prints header", func(t *testing.T) {
		t.Parallel()
		client := mocks.NewAgglayerClientMock(t)
		client.EXPECT().GetLatestPendingCertificateHeader(mockCtx(), uint32(1)).Return(fullHeader(), nil)

		require.NoError(t, printStatus(context.Background(), client, 1))
	})

	t.Run("propagates error", func(t *testing.T) {
		t.Parallel()
		client := mocks.NewAgglayerClientMock(t)
		client.EXPECT().GetLatestPendingCertificateHeader(mockCtx(), uint32(1)).
			Return(nil, errors.New("rpc down"))

		require.EqualError(t, printStatus(context.Background(), client, 1),
			"get latest pending certificate header: rpc down")
	})
}

func TestWaitForSettled(t *testing.T) {
	t.Parallel()

	const interval = 5 * time.Millisecond

	t.Run("no pending certificate settles immediately", func(t *testing.T) {
		t.Parallel()
		client := mocks.NewAgglayerClientMock(t)
		client.EXPECT().GetNetworkInfo(mockCtx(), uint32(1)).
			Return(agglayertypes.NetworkInfo{LatestPendingStatus: nil}, nil)
		// printStatus is invoked after settlement.
		client.EXPECT().GetLatestPendingCertificateHeader(mockCtx(), uint32(1)).Return(nil, nil)
		client.EXPECT().GetLatestSettledCertificateHeader(mockCtx(), uint32(1)).Return(nil, nil)

		require.NoError(t, waitForSettled(context.Background(), client, 1, interval, time.Minute))
	})

	t.Run("pending already settled", func(t *testing.T) {
		t.Parallel()
		client := mocks.NewAgglayerClientMock(t)
		client.EXPECT().GetNetworkInfo(mockCtx(), uint32(1)).Return(agglayertypes.NetworkInfo{
			LatestPendingHeight: ptrUint64(5),
			LatestPendingStatus: ptrStatus(agglayertypes.Settled),
		}, nil)
		client.EXPECT().GetLatestPendingCertificateHeader(mockCtx(), uint32(1)).Return(nil, nil)
		client.EXPECT().GetLatestSettledCertificateHeader(mockCtx(), uint32(1)).Return(nil, nil)

		require.NoError(t, waitForSettled(context.Background(), client, 1, interval, time.Minute))
	})

	t.Run("pending in error", func(t *testing.T) {
		t.Parallel()
		client := mocks.NewAgglayerClientMock(t)
		client.EXPECT().GetNetworkInfo(mockCtx(), uint32(1)).Return(agglayertypes.NetworkInfo{
			LatestPendingHeight: ptrUint64(8),
			LatestPendingStatus: ptrStatus(agglayertypes.InError),
		}, nil)

		err := waitForSettled(context.Background(), client, 1, interval, time.Minute)
		require.EqualError(t, err, "latest certificate (height 8) is in error state")
	})

	t.Run("get network info fails", func(t *testing.T) {
		t.Parallel()
		client := mocks.NewAgglayerClientMock(t)
		client.EXPECT().GetNetworkInfo(mockCtx(), uint32(1)).
			Return(agglayertypes.NetworkInfo{}, errors.New("conn refused"))

		err := waitForSettled(context.Background(), client, 1, interval, time.Minute)
		require.EqualError(t, err, "get network info: conn refused")
	})

	t.Run("polls until settled", func(t *testing.T) {
		t.Parallel()
		client := mocks.NewAgglayerClientMock(t)
		// First poll: still proving. Second poll: settled.
		client.EXPECT().GetNetworkInfo(mockCtx(), uint32(1)).Return(agglayertypes.NetworkInfo{
			LatestPendingHeight: ptrUint64(5),
			LatestPendingStatus: ptrStatus(agglayertypes.Proven),
		}, nil).Once()
		client.EXPECT().GetNetworkInfo(mockCtx(), uint32(1)).Return(agglayertypes.NetworkInfo{
			LatestPendingHeight: ptrUint64(5),
			LatestPendingStatus: ptrStatus(agglayertypes.Settled),
		}, nil).Once()
		client.EXPECT().GetLatestPendingCertificateHeader(mockCtx(), uint32(1)).Return(nil, nil)
		client.EXPECT().GetLatestSettledCertificateHeader(mockCtx(), uint32(1)).Return(nil, nil)

		require.NoError(t, waitForSettled(context.Background(), client, 1, interval, time.Minute))
	})

	t.Run("times out while still pending", func(t *testing.T) {
		t.Parallel()
		client := mocks.NewAgglayerClientMock(t)
		client.EXPECT().GetNetworkInfo(mockCtx(), uint32(1)).Return(agglayertypes.NetworkInfo{
			LatestPendingHeight: ptrUint64(5),
			LatestPendingStatus: ptrStatus(agglayertypes.Proven),
		}, nil)

		err := waitForSettled(context.Background(), client, 1, interval, 20*time.Millisecond)
		require.Error(t, err)
		require.Contains(t, err.Error(), "timed out waiting for settlement")
	})
}

func TestRun(t *testing.T) {
	// okFactory returns a clientFactory that always yields the given mock client,
	// ignoring the config/logger so run can be driven without a live endpoint.
	okFactory := func(c agglayer.AgglayerClientInterface) clientFactory {
		return func(agglayer.ClientConfig, aggkitcommon.Logger) (agglayer.AgglayerClientInterface, error) {
			return c, nil
		}
	}

	t.Run("missing grpc url", func(t *testing.T) {
		t.Setenv("AGGLAYER_GRPC_URL", "")
		err := run(nil, func(agglayer.ClientConfig, aggkitcommon.Logger) (agglayer.AgglayerClientInterface, error) {
			t.Fatal("client factory must not be called when the URL is missing")
			return nil, nil
		})
		require.EqualError(t, err, "agglayer gRPC URL is required (set AGGLAYER_GRPC_URL or pass -grpc)")
	})

	t.Run("invalid flag", func(t *testing.T) {
		err := run([]string{"-does-not-exist"}, okFactory(mocks.NewAgglayerClientMock(t)))
		require.Error(t, err)
	})

	t.Run("client factory fails", func(t *testing.T) {
		err := run([]string{"-grpc", "localhost:1"},
			func(agglayer.ClientConfig, aggkitcommon.Logger) (agglayer.AgglayerClientInterface, error) {
				return nil, errors.New("dial boom")
			})
		require.EqualError(t, err, "create agglayer client: dial boom")
	})

	t.Run("status path without wait", func(t *testing.T) {
		client := mocks.NewAgglayerClientMock(t)
		client.EXPECT().GetLatestPendingCertificateHeader(mockCtx(), uint32(1)).Return(fullHeader(), nil)
		require.NoError(t, run([]string{"-grpc", "localhost:1"}, okFactory(client)))
	})

	t.Run("network index from env", func(t *testing.T) {
		t.Setenv("NETWORK_INDEX", "2")
		client := mocks.NewAgglayerClientMock(t)
		client.EXPECT().GetLatestPendingCertificateHeader(mockCtx(), uint32(2)).Return(fullHeader(), nil)
		require.NoError(t, run([]string{"-grpc", "localhost:1"}, okFactory(client)))
	})

	t.Run("wait path settles", func(t *testing.T) {
		client := mocks.NewAgglayerClientMock(t)
		client.EXPECT().GetNetworkInfo(mockCtx(), uint32(1)).
			Return(agglayertypes.NetworkInfo{LatestPendingStatus: nil}, nil)
		client.EXPECT().GetLatestPendingCertificateHeader(mockCtx(), uint32(1)).Return(nil, nil)
		client.EXPECT().GetLatestSettledCertificateHeader(mockCtx(), uint32(1)).Return(nil, nil)
		require.NoError(t, run([]string{"-grpc", "localhost:1", "-wait", "-interval", "5ms"}, okFactory(client)))
	})
}

func TestU64Ptr(t *testing.T) {
	t.Parallel()
	require.Equal(t, "—", u64ptr(nil))
	require.Equal(t, "42", u64ptr(ptrUint64(42)))
}

func TestDurStr(t *testing.T) {
	t.Parallel()
	require.Equal(t, "none", durStr(0))
	require.Equal(t, "5s", durStr(5*time.Second))
}

// mockCtx matches any context.Context argument.
func mockCtx() any {
	return mock.Anything
}
