package proof

import (
	"context"
	"errors"
	"testing"

	"github.com/agglayer/aggkit/bridgeservice/client"
	bridgetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/stretchr/testify/require"
)

const testDestNetwork = uint32(5)

// fakeInjectedLeafClient implements injectedLeafClient for tests, keyed by base URL.
type fakeInjectedLeafClient struct {
	baseURL   string
	responses map[string]*bridgetypes.L1InfoTreeLeafResponse
	err       error
	// calls records every GetInjectedL1InfoLeaf invocation against this client instance.
	calls []injectedLeafCall
}

type injectedLeafCall struct {
	baseURL   string
	networkID int
	leafIndex int
}

func (f *fakeInjectedLeafClient) GetInjectedL1InfoLeaf(
	_ context.Context, networkID, leafIndex int,
) (*bridgetypes.L1InfoTreeLeafResponse, error) {
	f.calls = append(f.calls, injectedLeafCall{baseURL: f.baseURL, networkID: networkID, leafIndex: leafIndex})
	if f.err != nil {
		return nil, f.err
	}
	return f.responses[f.baseURL], nil
}

func newFakeGate(
	resolver *fakeURLResolver, clients map[string]*fakeInjectedLeafClient,
) *BridgeServiceGERGate {
	return &BridgeServiceGERGate{
		finder: resolver,
		newClient: func(baseURL string) injectedLeafClient {
			return clients[baseURL]
		},
		networkID: testDestNetwork,
	}
}

func TestGERGateResolveURLError(t *testing.T) {
	resolveErr := errors.New("bridge service url not found for network")
	resolver := &fakeURLResolver{errs: map[uint32]error{testDestNetwork: resolveErr}}
	gate := newFakeGate(resolver, nil)

	_, err := gate.GetFirstGERAfterL1InfoTreeIndex(context.Background(), 10)
	require.ErrorIs(t, err, resolveErr)
	require.ErrorContains(t, err, "resolve bridge service url for destination network 5")
}

func TestGERGateNotFoundMapsToErrGERNotInjected(t *testing.T) {
	resolver := &fakeURLResolver{urls: map[uint32]string{testDestNetwork: testSourceFiveURL}}
	c := &fakeInjectedLeafClient{baseURL: testSourceFiveURL, err: client.ErrNotFound}
	gate := newFakeGate(resolver, map[string]*fakeInjectedLeafClient{testSourceFiveURL: c})

	_, err := gate.GetFirstGERAfterL1InfoTreeIndex(context.Background(), 10)
	require.ErrorIs(t, err, ErrGERNotInjected)
	require.Equal(t, []injectedLeafCall{{baseURL: testSourceFiveURL, networkID: 5, leafIndex: 10}}, c.calls)
}

func TestGERGateOtherClientErrorIsHardError(t *testing.T) {
	resolver := &fakeURLResolver{urls: map[uint32]string{testDestNetwork: testSourceFiveURL}}
	clientErr := errors.New("rpc failure")
	c := &fakeInjectedLeafClient{baseURL: testSourceFiveURL, err: clientErr}
	gate := newFakeGate(resolver, map[string]*fakeInjectedLeafClient{testSourceFiveURL: c})

	_, err := gate.GetFirstGERAfterL1InfoTreeIndex(context.Background(), 10)
	require.ErrorIs(t, err, clientErr)
	require.NotErrorIs(t, err, ErrGERNotInjected)
	require.ErrorContains(t, err, "get injected l1 info leaf from destination network 5 bridge service")
}

func TestGERGateNilResponseIsError(t *testing.T) {
	resolver := &fakeURLResolver{urls: map[uint32]string{testDestNetwork: testSourceFiveURL}}
	c := &fakeInjectedLeafClient{baseURL: testSourceFiveURL}
	gate := newFakeGate(resolver, map[string]*fakeInjectedLeafClient{testSourceFiveURL: c})

	_, err := gate.GetFirstGERAfterL1InfoTreeIndex(context.Background(), 10)
	require.ErrorContains(t, err, "empty response")
}

func TestGERGateSuccessReturnsL1InfoTreeIndex(t *testing.T) {
	resolver := &fakeURLResolver{urls: map[uint32]string{testDestNetwork: testSourceFiveURL}}
	c := &fakeInjectedLeafClient{
		baseURL: testSourceFiveURL,
		responses: map[string]*bridgetypes.L1InfoTreeLeafResponse{
			testSourceFiveURL: {L1InfoTreeIndex: 42},
		},
	}
	gate := newFakeGate(resolver, map[string]*fakeInjectedLeafClient{testSourceFiveURL: c})

	idx, err := gate.GetFirstGERAfterL1InfoTreeIndex(context.Background(), 10)
	require.NoError(t, err)
	require.Equal(t, uint32(42), idx)
	require.Equal(t, []uint32{testDestNetwork}, resolver.calls)
	require.Equal(t, []injectedLeafCall{{baseURL: testSourceFiveURL, networkID: 5, leafIndex: 10}}, c.calls)
}

func TestNewBridgeServiceGERGateConstructsRealClient(t *testing.T) {
	gate := NewBridgeServiceGERGate(&fakeURLResolver{urls: map[uint32]string{testDestNetwork: "http://x"}}, testDestNetwork)
	require.NotNil(t, gate)
	require.Equal(t, testDestNetwork, gate.networkID)
	require.NotNil(t, gate.newClient("http://x"))
}
