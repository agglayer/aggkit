package etherman

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"testing"

	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/stretchr/testify/require"
)

// limitedRPCClient is a fake RPCClienter that rejects batches larger than maxBatch.
type limitedRPCClient struct {
	maxBatch int
	// reject returns the top-level error for an oversized batch; if elementShape is true the
	// rejection is reported per element (go-ethereum server-side limit) with a nil top-level error.
	rejectErr    error
	elementShape bool

	mu       sync.Mutex
	sizes    []int
	oversize []int
}

func (f *limitedRPCClient) Call(any, string, ...any) error { return errors.New("not implemented") }

func (f *limitedRPCClient) CallContext(context.Context, any, string, ...any) error {
	return errors.New("not implemented")
}

func (f *limitedRPCClient) BatchCallContext(_ context.Context, b []rpc.BatchElem) error {
	f.mu.Lock()
	f.sizes = append(f.sizes, len(b))
	over := len(b) > f.maxBatch
	if over {
		f.oversize = append(f.oversize, len(b))
	}
	f.mu.Unlock()
	if over {
		if f.elementShape {
			for i := range b {
				if i == 0 {
					b[i].Error = errors.New("batch too large")
				} else {
					b[i].Error = errors.New("missing batch response")
				}
			}
			return nil
		}
		return f.rejectErr
	}
	for i := range b {
		arg := fmt.Sprint(b[i].Args[0])
		raw, ok := b[i].Result.(*blockRawEth)
		if !ok {
			return errors.New("unexpected result type")
		}
		raw.Number = arg
		raw.Hash = fmt.Sprintf("0x%064x", i+1)
		raw.Timestamp = "0x1"
		raw.ParentHash = fmt.Sprintf("0x%064x", i)
	}
	return nil
}

func (f *limitedRPCClient) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sizes = nil
	f.oversize = nil
}

func (f *limitedRPCClient) maxSeenSince() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := 0
	for _, s := range f.sizes {
		m = max(m, s)
	}
	return m
}

func distinctDescending(values []int) []int {
	set := make(map[int]struct{}, len(values))
	for _, v := range values {
		set[v] = struct{}{}
	}
	res := make([]int, 0, len(set))
	for v := range set {
		res = append(res, v)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(res)))
	return res
}

func blockNumbersRange(n int) []uint64 {
	res := make([]uint64, n)
	for i := range res {
		res[i] = uint64(100 + i)
	}
	return res
}

func requireAllHeaders(t *testing.T, blocks []uint64, headers map[uint64]struct{}, nHeaders int, nErrors int) {
	t.Helper()
	require.Equal(t, len(blocks), nHeaders)
	require.Zero(t, nErrors)
	for _, bn := range blocks {
		_, ok := headers[bn]
		require.True(t, ok, "missing block %d", bn)
	}
}

func headerSet(res *BlockHeadersResult) map[uint64]struct{} {
	set := make(map[uint64]struct{}, len(res.Headers))
	for bn := range res.Headers {
		set[bn] = struct{}{}
	}
	return set
}

func TestRetrieveBlockHeadersAdaptive(t *testing.T) {
	tooManyWithNumber := rpc.HTTPError{StatusCode: http.StatusBadRequest, Status: "400 Bad Request",
		Body: []byte("too many batch requests, max is 40")}
	tooManyNoNumber := rpc.HTTPError{StatusCode: http.StatusBadRequest, Status: "400 Bad Request",
		Body: []byte("too many batch requests")}

	tests := []struct {
		name         string
		fake         *limitedRPCClient
		initial      int
		wantSize     int
		wantRejected []int // distinct oversized batch sizes the provider rejected, descending (nil: not asserted)
	}{
		{
			name:         "parsed limit shrinks straight to 40",
			fake:         &limitedRPCClient{maxBatch: 40, rejectErr: tooManyWithNumber},
			initial:      1000,
			wantSize:     40,
			wantRejected: []int{1000},
		},
		{
			name:         "no number halves 1000 -> 31",
			fake:         &limitedRPCClient{maxBatch: 40, rejectErr: tooManyNoNumber},
			initial:      1000,
			wantSize:     31,
			wantRejected: []int{1000, 500, 250, 125, 62},
		},
		{
			name:         "geth per element shape halves",
			fake:         &limitedRPCClient{maxBatch: 40, elementShape: true},
			initial:      1000,
			wantSize:     31,
			wantRejected: []int{1000, 500, 250, 125, 62},
		},
		{
			name:     "limit of 1",
			fake:     &limitedRPCClient{maxBatch: 1, rejectErr: tooManyNoNumber},
			initial:  1000,
			wantSize: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := NewDefaultEthClient(nil, tc.fake, &ethermanconfig.RPCClientConfig{
				BatchBlockHeaderRetrieval: true, BatchRequestMaxSize: tc.initial,
			})
			blocks := blockNumbersRange(1000)

			res, err := client.RetrieveBlockHeaders(context.Background(), blocks, 4)
			require.NoError(t, err)
			requireAllHeaders(t, blocks, headerSet(res), len(res.Headers), len(res.Errors))
			require.Equal(t, tc.wantSize, client.batchSize.Size())
			if tc.wantRejected != nil {
				require.Equal(t, tc.wantRejected, distinctDescending(tc.fake.oversize))
			}

			// Second call on the same client starts at the remembered size: no oversized batch at all.
			tc.fake.reset()
			res, err = client.RetrieveBlockHeaders(context.Background(), blocks, 4)
			require.NoError(t, err)
			requireAllHeaders(t, blocks, headerSet(res), len(res.Headers), len(res.Errors))
			require.Empty(t, tc.fake.oversize)
			require.LessOrEqual(t, tc.fake.maxSeenSince(), tc.wantSize)
			require.Equal(t, tc.wantSize, client.batchSize.Size())
		})
	}
}

func TestRetrieveBlockHeadersAdaptive_ConfiguredStartSize(t *testing.T) {
	fake := &limitedRPCClient{maxBatch: 1000}
	client := NewDefaultEthClient(nil, fake, &ethermanconfig.RPCClientConfig{
		BatchBlockHeaderRetrieval: true, BatchRequestMaxSize: 25,
	})
	blocks := blockNumbersRange(100)
	res, err := client.RetrieveBlockHeaders(context.Background(), blocks, 2)
	require.NoError(t, err)
	require.Len(t, res.Headers, 100)
	require.Equal(t, 25, fake.maxSeenSince())
	require.Equal(t, 25, client.batchSize.Size())
}

func TestRetrieveBlockHeadersAdaptive_ZeroConfigUsesDefault(t *testing.T) {
	client := NewDefaultEthClient(nil, &limitedRPCClient{maxBatch: 1000},
		&ethermanconfig.RPCClientConfig{BatchBlockHeaderRetrieval: true})
	require.Equal(t, ethermanconfig.DefaultBatchRequestMaxSize, client.batchSize.Size())
}

func TestRetrieveBlockHeadersAdaptive_GenericErrorDoesNotShrink(t *testing.T) {
	fake := &limitedRPCClient{maxBatch: 0, rejectErr: errors.New("connection refused")}
	client := NewDefaultEthClient(nil, fake, &ethermanconfig.RPCClientConfig{BatchBlockHeaderRetrieval: true})
	res, err := client.RetrieveBlockHeaders(context.Background(), blockNumbersRange(10), 2)
	require.Error(t, err)
	require.Nil(t, res)
	require.ErrorContains(t, err, "connection refused")
	require.Equal(t, ethermanconfig.DefaultBatchRequestMaxSize, client.batchSize.Size())
}

func TestRetrieveBlockHeadersAdaptive_SizeOneRejectedReturnsError(t *testing.T) {
	// the provider rejects even a single request as "too large": stop with the error
	fake := &limitedRPCClient{maxBatch: 0,
		rejectErr: errors.New("too many batch requests")}
	client := NewDefaultEthClient(nil, fake, &ethermanconfig.RPCClientConfig{BatchBlockHeaderRetrieval: true})
	res, err := client.RetrieveBlockHeaders(context.Background(), blockNumbersRange(10), 2)
	require.Error(t, err)
	require.Nil(t, res)
	require.Equal(t, 1, client.batchSize.Size())
}

func TestRetrieveBlockHeadersAdaptive_SizeOneElementRejectionIsPerBlockError(t *testing.T) {
	// a per-element limit error on a single-request batch cannot be shrunk further: it must stay a
	// per-block error instead of failing the whole header retrieval
	fake := &limitedRPCClient{maxBatch: 0, elementShape: true}
	client := NewDefaultEthClient(nil, fake, &ethermanconfig.RPCClientConfig{BatchBlockHeaderRetrieval: true})
	blocks := blockNumbersRange(3)
	res, err := client.RetrieveBlockHeaders(context.Background(), blocks, 2)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Empty(t, res.Headers)
	require.Len(t, res.Errors, len(blocks))
	require.Equal(t, 1, client.batchSize.Size())
}

func TestRetrieveBlockHeadersAdaptive_ContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	limiter := newBatchSizeLimiter(10)
	res, err := retrieveBlockHeadersBatchAdaptive(ctx, log.WithFields("test", "x"),
		&limitedRPCClient{maxBatch: 10}, limiter, blockNumbersRange(5), 2)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, res)
}

func TestRetrieveBlockHeadersAdaptive_ConcurrentRejectionsNotOverShrunk(t *testing.T) {
	// 30 concurrent chunks of 100 are rejected at once with a parsed limit: the size must end at exactly 40.
	fake := &limitedRPCClient{maxBatch: 40, rejectErr: rpc.HTTPError{StatusCode: http.StatusBadRequest,
		Body: []byte("too many batch requests, max is 40")}}
	limiter := newBatchSizeLimiter(100)
	blocks := blockNumbersRange(3000)
	res, err := retrieveBlockHeadersBatchAdaptive(context.Background(), log.WithFields("test", "x"),
		fake, limiter, blocks, 30)
	require.NoError(t, err)
	requireAllHeaders(t, blocks, headerSet(res), len(res.Headers), len(res.Errors))
	require.Equal(t, 40, limiter.Size())
}

func TestBatchSizeLimiter_ShrinkNeverRaises(t *testing.T) {
	l := newBatchSizeLimiter(100)
	oldSize, newSize, changed := l.shrinkAfterRejection(100, errors.New("too many batch requests, max is 40"))
	require.True(t, changed)
	require.Equal(t, 100, oldSize)
	require.Equal(t, 40, newSize)

	// parsed limit not below the sent size -> halve the sent size, which is already above current: no change
	oldSize, newSize, changed = l.shrinkAfterRejection(100, errors.New("too many batch requests, max is 500"))
	require.False(t, changed)
	require.Equal(t, 40, oldSize)
	require.Equal(t, 40, newSize)
	require.Equal(t, 40, l.Size())

	require.Equal(t, ethermanconfig.DefaultBatchRequestMaxSize, newBatchSizeLimiter(0).Size())
	require.Equal(t, ethermanconfig.DefaultBatchRequestMaxSize, newBatchSizeLimiter(-5).Size())
}
