package backward_forward_let

import (
	"bytes"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestHasOpenPendingAtOrAbove(t *testing.T) {
	t.Parallel()

	pendingHeight := uint64(10)
	openStatus := agglayertypes.Pending
	closedStatus := agglayertypes.Settled

	require.True(t, hasOpenPendingAtOrAbove(agglayertypes.NetworkInfo{
		LatestPendingHeight: &pendingHeight,
		LatestPendingStatus: &openStatus,
	}, 10))
	require.False(t, hasOpenPendingAtOrAbove(agglayertypes.NetworkInfo{
		LatestPendingHeight: &pendingHeight,
		LatestPendingStatus: &closedStatus,
	}, 10))
	require.False(t, hasOpenPendingAtOrAbove(agglayertypes.NetworkInfo{
		LatestPendingHeight: &pendingHeight,
		LatestPendingStatus: &openStatus,
	}, 11))
}

func TestPrintCertStatus(t *testing.T) {
	t.Parallel()

	settledHeight := uint64(12)
	settledID := common.HexToHash("0xabc")
	settledLER := common.HexToHash("0xdef")
	settledDC := uint64(34)
	pendingHeight := uint64(13)
	pendingStatus := agglayertypes.Pending

	var buf bytes.Buffer
	printCertStatusTo(&buf, agglayertypes.NetworkInfo{
		SettledHeight:        &settledHeight,
		SettledCertificateID: &settledID,
		SettledLER:           &settledLER,
		SettledLETLeafCount:  &settledDC,
		LatestPendingHeight:  &pendingHeight,
		LatestPendingStatus:  &pendingStatus,
	}, 1, 12)

	output := buf.String()
	require.Contains(t, output, "Latest settled height: 12")
	require.Contains(t, output, "Requested height status: Settled")
}
