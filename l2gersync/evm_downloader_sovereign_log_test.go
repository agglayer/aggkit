package l2gersync

import (
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerger"
	aggkitcommon "github.com/agglayer/aggkit/common"
	commonmocks "github.com/agglayer/aggkit/common/mocks"
	l2gersyncmocks "github.com/agglayer/aggkit/l2gersync/mocks"
	aggkittypesmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type capturedLog struct {
	level string
	msg   string
}

// newCaptureLogger returns a Logger mock that records the Debugf/Warnf/Errorf lines (formatted) with their level
func newCaptureLogger(t *testing.T) (*commonmocks.Logger, *[]capturedLog) {
	t.Helper()
	logger := commonmocks.NewLogger(t)
	lines := &[]capturedLog{}
	capture := func(level string) func(format string, args ...interface{}) {
		return func(format string, args ...interface{}) {
			*lines = append(*lines, capturedLog{level: level, msg: fmt.Sprintf(format, args...)})
		}
	}
	for n := 0; n <= 8; n++ {
		args := make([]interface{}, n)
		for i := range args {
			args[i] = mock.Anything
		}
		logger.EXPECT().Debugf(mock.Anything, args...).Run(capture("DEBUG")).Maybe()
		logger.EXPECT().Warnf(mock.Anything, args...).Run(capture("WARN")).Maybe()
		logger.EXPECT().Errorf(mock.Anything, args...).Run(capture("ERROR")).Maybe()
	}
	return logger, lines
}

func countLevel(lines []capturedLog, level string) int {
	n := 0
	for _, l := range lines {
		if l.level == level {
			n++
		}
	}
	return n
}

type missingGERLogTestData struct {
	d              *downloaderSovereign
	lines          *[]capturedLog
	l1InfoTreeSync *l2gersyncmocks.L1InfoTreeQuerier
	l1Client       *aggkittypesmocks.BaseEthereumClienter
}

// newMissingGERLogTestData builds a downloaderSovereign whose L1 GER manager globalExitRootMap answers
// l1Timestamp (or l1Err)
func newMissingGERLogTestData(t *testing.T, l1Timestamp *big.Int, l1Err error) *missingGERLogTestData {
	t.Helper()
	l1Client := aggkittypesmocks.NewBaseEthereumClienter(t)
	result := make([]byte, 32)
	if l1Timestamp != nil {
		l1Timestamp.FillBytes(result)
	}
	l1Client.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).Return(result, l1Err).Maybe()
	l1GERManager, err := agglayerger.NewAgglayerger(common.HexToAddress("0x1"), l1Client)
	require.NoError(t, err)
	logger, lines := newCaptureLogger(t)
	l1InfoTreeSync := l2gersyncmocks.NewL1InfoTreeQuerier(t)
	return &missingGERLogTestData{
		d: &downloaderSovereign{
			l1InfoTreeSync: l1InfoTreeSync,
			l1GERManager:   l1GERManager,
			l1Client:       l1Client,
			logger:         logger,
			now:            time.Now,
		},
		lines:          lines,
		l1InfoTreeSync: l1InfoTreeSync,
		l1Client:       l1Client,
	}
}

func TestLogGERMissingFromL1InfoTree_Level(t *testing.T) {
	ger := common.HexToHash("0xabcd")
	lookupErr := errors.New("not found")

	t.Run("GER in the L1 contract: the l1 info tree syncer is behind, WARN", func(t *testing.T) {
		// This is the case of a syncer that follows a finality more recent than finalized (e.g. LatestBlock/-6):
		// IsUpToDate would answer true, so it must not be used to pick the level
		td := newMissingGERLogTestData(t, big.NewInt(1234567890), nil)
		require.True(t, td.d.logGERMissingFromL1InfoTree(t.Context(), ger, 100, lookupErr), "expected race")
		require.Len(t, *td.lines, 1)
		require.Equal(t, "WARN", (*td.lines)[0].level)
		require.Contains(t, (*td.lines)[0].msg, "l1 info tree not synced yet")
		require.Contains(t, (*td.lines)[0].msg, "exists in the L1 contract")
		td.l1InfoTreeSync.AssertNotCalled(t, "IsUpToDate", mock.Anything, mock.Anything)
	})

	t.Run("GER not in the L1 contract: possible fake GER, ERROR", func(t *testing.T) {
		td := newMissingGERLogTestData(t, big.NewInt(0), nil)
		require.False(t, td.d.logGERMissingFromL1InfoTree(t.Context(), ger, 100, lookupErr))
		require.Len(t, *td.lines, 1)
		require.Equal(t, "ERROR", (*td.lines)[0].level)
		require.Contains(t, (*td.lines)[0].msg, "failed to fetch l1 info tree for global exit root")
		require.Contains(t, (*td.lines)[0].msg, "not found in L1 contract")
		td.l1InfoTreeSync.AssertNotCalled(t, "IsUpToDate", mock.Anything, mock.Anything)
	})

	t.Run("L1 read error and l1 info tree not synced: WARN", func(t *testing.T) {
		td := newMissingGERLogTestData(t, nil, errors.New("L1 RPC unavailable"))
		td.l1InfoTreeSync.EXPECT().IsUpToDate(mock.Anything, td.l1Client).Return(false, nil).Once()
		require.True(t, td.d.logGERMissingFromL1InfoTree(t.Context(), ger, 100, lookupErr), "expected race")
		require.Len(t, *td.lines, 1, "a single line, no extra ERROR for the L1 read")
		require.Equal(t, "WARN", (*td.lines)[0].level)
		require.Contains(t, (*td.lines)[0].msg, "l1 info tree not synced yet")
		require.Contains(t, (*td.lines)[0].msg, "L1 RPC unavailable")
	})

	t.Run("L1 read error and l1 info tree synced: ERROR", func(t *testing.T) {
		td := newMissingGERLogTestData(t, nil, errors.New("L1 RPC unavailable"))
		td.l1InfoTreeSync.EXPECT().IsUpToDate(mock.Anything, td.l1Client).Return(true, nil).Once()
		require.False(t, td.d.logGERMissingFromL1InfoTree(t.Context(), ger, 100, lookupErr))
		require.Len(t, *td.lines, 1)
		require.Equal(t, "ERROR", (*td.lines)[0].level)
		require.Contains(t, (*td.lines)[0].msg, "failed to fetch l1 info tree for global exit root")
		require.Contains(t, (*td.lines)[0].msg, "L1 RPC unavailable")
	})

	t.Run("L1 read error and unknown sync status: ERROR", func(t *testing.T) {
		td := newMissingGERLogTestData(t, nil, errors.New("L1 RPC unavailable"))
		td.l1InfoTreeSync.EXPECT().IsUpToDate(mock.Anything, td.l1Client).
			Return(false, errors.New("finalized block unavailable")).Once()
		require.False(t, td.d.logGERMissingFromL1InfoTree(t.Context(), ger, 100, lookupErr))
		require.Len(t, *td.lines, 1)
		require.Equal(t, "ERROR", (*td.lines)[0].level)
		require.Contains(t, (*td.lines)[0].msg, "could not check whether the l1 info tree is synced")
	})
}

func TestLogGERMissingFromL1InfoTree_RetriesAreBounded(t *testing.T) {
	ger := common.HexToHash("0xabcd")
	otherGER := common.HexToHash("0xef01")
	lookupErr := errors.New("not found")
	td := newMissingGERLogTestData(t, big.NewInt(1234567890), nil)

	const retries = 2 * aggkitcommon.RetryErrorLogInterval
	for range retries {
		td.d.logGERMissingFromL1InfoTree(t.Context(), ger, 100, lookupErr)
	}
	require.Len(t, *td.lines, retries)
	// the first RetryErrorLogBurst and then one of every RetryErrorLogInterval
	require.Equal(t, aggkitcommon.RetryErrorLogBurst+2, countLevel(*td.lines, "WARN"))
	require.Equal(t, retries-aggkitcommon.RetryErrorLogBurst-2, countLevel(*td.lines, "DEBUG"))
	// a GER on L1 doesn't go away: the L1 contract is read only once
	td.l1Client.AssertNumberOfCalls(t, "CallContract", 1)

	// A different GER starts its own count
	*td.lines = nil
	td.d.logGERMissingFromL1InfoTree(t.Context(), otherGER, 101, lookupErr)
	require.Equal(t, "WARN", (*td.lines)[0].level)
	require.Contains(t, (*td.lines)[0].msg, "attempt 1")

	// Once resolved, the same GER starts again
	td.d.resetMissingGER()
	*td.lines = nil
	td.d.logGERMissingFromL1InfoTree(t.Context(), otherGER, 101, lookupErr)
	require.Contains(t, (*td.lines)[0].msg, "attempt 1")
}

func TestLogGERMissingFromL1InfoTree_EscalatesWhenStuck(t *testing.T) {
	ger := common.HexToHash("0xabcd")
	lookupErr := errors.New("not found")
	td := newMissingGERLogTestData(t, big.NewInt(1234567890), nil)
	start := time.Now()
	now := start
	td.d.now = func() time.Time { return now }

	require.True(t, td.d.logGERMissingFromL1InfoTree(t.Context(), ger, 100, lookupErr))
	require.Equal(t, "WARN", (*td.lines)[0].level)

	// Still missing well after an L1 info tree syncer following finalized would have indexed it
	now = start.Add(missingGERStuckAfter + time.Minute)
	require.False(t, td.d.logGERMissingFromL1InfoTree(t.Context(), ger, 100, lookupErr),
		"no longer the expected race")
	require.Equal(t, "ERROR", (*td.lines)[1].level)
	require.Contains(t, (*td.lines)[1].msg, "may be stuck or misconfigured")
}

func TestLogGERMissingFromL1InfoTree_NoL1ReadForDebugLines(t *testing.T) {
	ger := common.HexToHash("0xabcd")
	lookupErr := errors.New("not found")
	// GER not in L1: the cause is not cached, so it is re-read, but only for the lines that are not DEBUG
	td := newMissingGERLogTestData(t, big.NewInt(0), nil)
	const retries = aggkitcommon.RetryErrorLogInterval
	for range retries {
		require.False(t, td.d.logGERMissingFromL1InfoTree(t.Context(), ger, 100, lookupErr))
	}
	require.Equal(t, aggkitcommon.RetryErrorLogBurst+1, countLevel(*td.lines, "ERROR"))
	td.l1Client.AssertNumberOfCalls(t, "CallContract", aggkitcommon.RetryErrorLogBurst+1)
}
