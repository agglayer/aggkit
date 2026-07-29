package bridgedetector

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/agglayer/aggkit/bridgeservice/client"
	bridgetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgeservicefinder"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

type fakeURLResolver struct {
	url string
	err error
}

func (f fakeURLResolver) GetURL(uint32) (string, error) {
	return f.url, f.err
}

type fakeCandidatesClient struct {
	result *bridgetypes.ClaimCandidatesResult
	err    error
}

func (f fakeCandidatesClient) GetClaimCandidates(
	context.Context, client.GetClaimCandidatesParams,
) (*bridgetypes.ClaimCandidatesResult, error) {
	return f.result, f.err
}

func newTestFetcher(resolver urlResolver, cl candidatesClient) *ServiceFetcher {
	return &ServiceFetcher{
		finder:    resolver,
		newClient: func(string) candidatesClient { return cl },
	}
}

func TestNewServiceFetcher(t *testing.T) {
	f := NewServiceFetcher(nil)
	require.NotNil(t, f)
	require.Nil(t, f.finder)
	require.NotNil(t, f.newClient("http://src"))
}

func TestServiceFetcherGetURL(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		f := newTestFetcher(fakeURLResolver{url: "http://src"}, nil)
		url, err := f.GetURL(10)
		require.NoError(t, err)
		require.Equal(t, "http://src", url)
	})

	t.Run("url not found is wrapped", func(t *testing.T) {
		f := newTestFetcher(fakeURLResolver{err: bridgeservicefinder.ErrURLNotFound}, nil)
		_, err := f.GetURL(10)
		require.ErrorIs(t, err, ErrURLNotFound)
		require.ErrorIs(t, err, bridgeservicefinder.ErrURLNotFound)
	})

	t.Run("other error is passed through", func(t *testing.T) {
		otherErr := errors.New("boom")
		f := newTestFetcher(fakeURLResolver{err: otherErr}, nil)
		_, err := f.GetURL(10)
		require.ErrorIs(t, err, otherErr)
		require.NotErrorIs(t, err, ErrURLNotFound)
	})
}

func TestServiceFetcherGetClaimCandidates(t *testing.T) {
	fromLER := common.HexToHash("0x1")
	toLER := common.HexToHash("0x2")
	baseQuery := ClaimCandidatesQuery{
		URL:                   "http://src",
		DestinationNetworkIDs: []uint32{1, 2},
		ToLER:                 toLER,
		PageNumber:            1,
		PageSize:              10,
	}

	t.Run("success without FromLER", func(t *testing.T) {
		result := &bridgetypes.ClaimCandidatesResult{
			ClaimCandidates: []*bridgetypes.ClaimCandidateResponse{
				{
					Bridge: &bridgetypes.BridgeResponse{
						BlockNum:           1,
						OriginAddress:      "0xaaaa",
						DestinationAddress: "0xbbbb",
						TxHash:             "0xcccc",
						TxnSender:          "0xdddd",
						ToAddress:          "0xeeee",
						Amount:             "1000",
						Metadata:           "0xdead",
					},
				},
			},
			Count: 1,
		}
		f := newTestFetcher(nil, fakeCandidatesClient{result: result})
		candidates, count, err := f.GetClaimCandidates(context.Background(), baseQuery)
		require.NoError(t, err)
		require.Equal(t, 1, count)
		require.Len(t, candidates, 1)
		require.Equal(t, uint64(1), candidates[0].Bridge.BlockNum)
		require.Nil(t, candidates[0].Bridge.FromAddress)
	})

	t.Run("success with FromLER and FromAddress", func(t *testing.T) {
		fromAddr := bridgetypes.Address("0x1234")
		result := &bridgetypes.ClaimCandidatesResult{
			ClaimCandidates: []*bridgetypes.ClaimCandidateResponse{
				{
					Bridge: &bridgetypes.BridgeResponse{
						FromAddress:        &fromAddr,
						OriginAddress:      "0xaaaa",
						DestinationAddress: "0xbbbb",
						TxHash:             "0xcccc",
						TxnSender:          "0xdddd",
						ToAddress:          "0xeeee",
						Amount:             "",
						Metadata:           "",
					},
				},
			},
			Count: 1,
		}
		f := newTestFetcher(nil, fakeCandidatesClient{result: result})
		query := baseQuery
		query.FromLER = &fromLER
		candidates, count, err := f.GetClaimCandidates(context.Background(), query)
		require.NoError(t, err)
		require.Equal(t, 1, count)
		require.NotNil(t, candidates[0].Bridge.FromAddress)
		require.Equal(t, common.HexToAddress(string(fromAddr)), *candidates[0].Bridge.FromAddress)
		require.Equal(t, big.NewInt(0), candidates[0].Bridge.Amount)
	})

	t.Run("not found is mapped to ErrCandidatesNotSynced", func(t *testing.T) {
		f := newTestFetcher(nil, fakeCandidatesClient{err: client.ErrNotFound})
		_, _, err := f.GetClaimCandidates(context.Background(), baseQuery)
		require.ErrorIs(t, err, ErrCandidatesNotSynced)
	})

	t.Run("other client error is passed through", func(t *testing.T) {
		otherErr := errors.New("network down")
		f := newTestFetcher(nil, fakeCandidatesClient{err: otherErr})
		_, _, err := f.GetClaimCandidates(context.Background(), baseQuery)
		require.ErrorIs(t, err, otherErr)
	})

	t.Run("conversion error is propagated", func(t *testing.T) {
		result := &bridgetypes.ClaimCandidatesResult{
			ClaimCandidates: []*bridgetypes.ClaimCandidateResponse{
				{Bridge: &bridgetypes.BridgeResponse{Amount: "not-a-number"}},
			},
		}
		f := newTestFetcher(nil, fakeCandidatesClient{result: result})
		_, _, err := f.GetClaimCandidates(context.Background(), baseQuery)
		require.Error(t, err)
	})
}

func TestToClaimCandidate(t *testing.T) {
	t.Run("nil dto", func(t *testing.T) {
		_, err := toClaimCandidate(nil)
		require.Error(t, err)
	})

	t.Run("nil bridge", func(t *testing.T) {
		_, err := toClaimCandidate(&bridgetypes.ClaimCandidateResponse{})
		require.Error(t, err)
	})

	t.Run("invalid amount", func(t *testing.T) {
		_, err := toClaimCandidate(&bridgetypes.ClaimCandidateResponse{
			Bridge: &bridgetypes.BridgeResponse{Amount: "xx"},
		})
		require.Error(t, err)
	})

	t.Run("invalid metadata", func(t *testing.T) {
		_, err := toClaimCandidate(&bridgetypes.ClaimCandidateResponse{
			Bridge: &bridgetypes.BridgeResponse{Amount: "1", Metadata: "0xzz"},
		})
		require.Error(t, err)
	})

	t.Run("success", func(t *testing.T) {
		candidate, err := toClaimCandidate(&bridgetypes.ClaimCandidateResponse{
			Bridge: &bridgetypes.BridgeResponse{
				BlockNum:           5,
				BlockPos:           6,
				TxHash:             "0x01",
				BlockTimestamp:     7,
				LeafType:           1,
				OriginNetwork:      2,
				OriginAddress:      "0x02",
				DestinationNetwork: 3,
				DestinationAddress: "0x03",
				Amount:             "42",
				Metadata:           "0xbeef",
				DepositCount:       9,
				TxnSender:          "0x04",
				ToAddress:          "0x05",
			},
		})
		require.NoError(t, err)
		require.Equal(t, uint64(5), candidate.Bridge.BlockNum)
		require.Equal(t, []byte{0xbe, 0xef}, candidate.Bridge.Metadata)
	})
}

func TestParseAmount(t *testing.T) {
	t.Run("empty returns zero", func(t *testing.T) {
		amount, err := parseAmount("")
		require.NoError(t, err)
		require.Equal(t, int64(0), amount.Int64())
	})

	t.Run("valid", func(t *testing.T) {
		amount, err := parseAmount("123")
		require.NoError(t, err)
		require.Equal(t, int64(123), amount.Int64())
	})

	t.Run("invalid", func(t *testing.T) {
		_, err := parseAmount("not-a-number")
		require.Error(t, err)
	})
}

func TestParseHexBytes(t *testing.T) {
	t.Run("empty returns nil", func(t *testing.T) {
		decoded, err := parseHexBytes("")
		require.NoError(t, err)
		require.Nil(t, decoded)
	})

	t.Run("0x only returns nil", func(t *testing.T) {
		decoded, err := parseHexBytes("0x")
		require.NoError(t, err)
		require.Nil(t, decoded)
	})

	t.Run("valid with prefix", func(t *testing.T) {
		decoded, err := parseHexBytes("0xdead")
		require.NoError(t, err)
		require.Equal(t, []byte{0xde, 0xad}, decoded)
	})

	t.Run("valid without prefix", func(t *testing.T) {
		decoded, err := parseHexBytes("beef")
		require.NoError(t, err)
		require.Equal(t, []byte{0xbe, 0xef}, decoded)
	})

	t.Run("invalid hex", func(t *testing.T) {
		_, err := parseHexBytes("zz")
		require.Error(t, err)
	})
}
