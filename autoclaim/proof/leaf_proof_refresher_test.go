package proof

import (
	"context"
	"errors"
	"testing"

	bridgetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgeservicefinder"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

const (
	testSourceFiveURL = "http://source-5"
	testSourceOneURL  = "http://source-1"
	testSourceTwoURL  = "http://source-2"
)

// fakeURLResolver implements bridgeservicefinder.Finder for tests, returning a distinct URL (or
// error) per network. Start is a no-op; only GetURL is exercised.
type fakeURLResolver struct {
	urls map[uint32]string
	errs map[uint32]error
	// calls records every GetURL invocation, in order.
	calls []uint32
}

func (f *fakeURLResolver) Start(context.Context) error { return nil }

func (f *fakeURLResolver) GetURL(networkID uint32) (bridgeservicefinder.NetworkURLs, error) {
	f.calls = append(f.calls, networkID)
	if err, ok := f.errs[networkID]; ok {
		return bridgeservicefinder.NetworkURLs{}, err
	}
	return bridgeservicefinder.NetworkURLs{BridgeURL: f.urls[networkID]}, nil
}

// fakeClaimProofClient implements claimProofClient for tests, keyed by base URL.
type fakeClaimProofClient struct {
	baseURL string
	proofs  map[string]*bridgetypes.ClaimProof
	err     error
	// calls records every GetClaimProof invocation against this client instance.
	calls []claimProofCall
}

type claimProofCall struct {
	baseURL      string
	networkID    uint32
	leafIndex    uint32
	depositCount uint32
}

func (f *fakeClaimProofClient) GetClaimProof(
	_ context.Context, networkID, leafIndex, depositCount uint32,
) (*bridgetypes.ClaimProof, error) {
	f.calls = append(f.calls, claimProofCall{
		baseURL: f.baseURL, networkID: networkID, leafIndex: leafIndex, depositCount: depositCount,
	})
	if f.err != nil {
		return nil, f.err
	}
	return f.proofs[f.baseURL], nil
}

func newFakeRefresher(
	resolver *fakeURLResolver, clients map[string]*fakeClaimProofClient,
) *BridgeServiceLeafProofRefresher {
	return &BridgeServiceLeafProofRefresher{
		finder: resolver,
		newClient: func(baseURL string) claimProofClient {
			return clients[baseURL]
		},
	}
}

func testBridgeServiceProof(hexes ...string) bridgetypes.Proof {
	var proof bridgetypes.Proof
	for i, h := range hexes {
		proof[i] = bridgetypes.Hash(h)
	}
	return proof
}

func TestRefreshLeafProofHappyPath(t *testing.T) {
	resolver := &fakeURLResolver{urls: map[uint32]string{5: testSourceFiveURL}}
	client := &fakeClaimProofClient{
		baseURL: testSourceFiveURL,
		proofs: map[string]*bridgetypes.ClaimProof{
			testSourceFiveURL: {ProofLocalExitRoot: testBridgeServiceProof("0x01", "0x02")},
		},
	}
	refresher := newFakeRefresher(resolver, map[string]*fakeClaimProofClient{testSourceFiveURL: client})

	proof, err := refresher.RefreshLeafProof(context.Background(), 5, 10, 20)
	require.NoError(t, err)
	require.Equal(t, common.HexToHash("0x01"), proof[0])
	require.Equal(t, common.HexToHash("0x02"), proof[1])
	require.Equal(t, common.Hash{}, proof[2])

	require.Equal(t, []uint32{5}, resolver.calls)
	require.Equal(t, []claimProofCall{{baseURL: testSourceFiveURL, networkID: 5, leafIndex: 10, depositCount: 20}},
		client.calls)
}

func TestRefreshLeafProofResolvesURLPerCallNotAtConstruction(t *testing.T) {
	resolver := &fakeURLResolver{urls: map[uint32]string{
		1: testSourceOneURL,
		2: testSourceTwoURL,
	}}
	clientOne := &fakeClaimProofClient{
		baseURL: testSourceOneURL,
		proofs: map[string]*bridgetypes.ClaimProof{
			testSourceOneURL: {ProofLocalExitRoot: testBridgeServiceProof("0xaa")},
		},
	}
	clientTwo := &fakeClaimProofClient{
		baseURL: testSourceTwoURL,
		proofs: map[string]*bridgetypes.ClaimProof{
			testSourceTwoURL: {ProofLocalExitRoot: testBridgeServiceProof("0xbb")},
		},
	}
	refresher := newFakeRefresher(resolver, map[string]*fakeClaimProofClient{
		testSourceOneURL: clientOne,
		testSourceTwoURL: clientTwo,
	})

	proofOne, err := refresher.RefreshLeafProof(context.Background(), 1, 0, 0)
	require.NoError(t, err)
	require.Equal(t, common.HexToHash("0xaa"), proofOne[0])

	proofTwo, err := refresher.RefreshLeafProof(context.Background(), 2, 0, 0)
	require.NoError(t, err)
	require.Equal(t, common.HexToHash("0xbb"), proofTwo[0])

	require.Equal(t, []uint32{1, 2}, resolver.calls)
}

func TestRefreshLeafProofURLNotFound(t *testing.T) {
	resolveErr := errors.New("bridge service url not found for network")
	resolver := &fakeURLResolver{errs: map[uint32]error{3: resolveErr}}
	refresher := newFakeRefresher(resolver, nil)

	_, err := refresher.RefreshLeafProof(context.Background(), 3, 0, 0)
	require.ErrorIs(t, err, resolveErr)
	require.ErrorContains(t, err, "resolve bridge service url for source network 3")
}

func TestRefreshLeafProofClientError(t *testing.T) {
	resolver := &fakeURLResolver{urls: map[uint32]string{5: testSourceFiveURL}}
	clientErr := errors.New("not found")
	client := &fakeClaimProofClient{baseURL: testSourceFiveURL, err: clientErr}
	refresher := newFakeRefresher(resolver, map[string]*fakeClaimProofClient{testSourceFiveURL: client})

	_, err := refresher.RefreshLeafProof(context.Background(), 5, 0, 0)
	require.ErrorIs(t, err, clientErr)
	require.ErrorContains(t, err, "get claim proof from source network 5 bridge service")
}

func TestRefreshLeafProofNilResponse(t *testing.T) {
	resolver := &fakeURLResolver{urls: map[uint32]string{5: testSourceFiveURL}}
	client := &fakeClaimProofClient{baseURL: testSourceFiveURL}
	refresher := newFakeRefresher(resolver, map[string]*fakeClaimProofClient{testSourceFiveURL: client})

	_, err := refresher.RefreshLeafProof(context.Background(), 5, 0, 0)
	require.ErrorContains(t, err, "empty response")
}

func TestNewBridgeServiceLeafProofRefresherConstructsRealClient(t *testing.T) {
	refresher := NewBridgeServiceLeafProofRefresher(&fakeURLResolver{urls: map[uint32]string{1: "http://x"}})
	require.NotNil(t, refresher)
	require.NotNil(t, refresher.newClient("http://x"))
}
