package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/agglayer/aggkit/tools/ger_validator_signer/cache"
	"github.com/agglayer/aggkit/tools/ger_validator_signer/finality"
	"github.com/agglayer/aggkit/tools/ger_validator_signer/l1"
	"github.com/agglayer/aggkit/tools/ger_validator_signer/service/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var (
	agglayerGER = common.HexToAddress("0x1111111111111111111111111111111111111111")
	validator   = common.HexToAddress("0x2222222222222222222222222222222222222222")
	testGER     = common.HexToHash("0x0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	parentHash  = common.HexToHash("0xaaaa000000000000000000000000000000000000000000000000000000000001")
	otherHash   = common.HexToHash("0xcccc000000000000000000000000000000000000000000000000000000000003")
	signature   = append(make([]byte, 64), 27)
	errL1       = errors.New("l1 rpc down")
	errKMS      = errors.New("kms unavailable")
)

// parentNumber is the number of the block previous to the one that inserted the GER. The insertion block is 101
const parentNumber = uint64(100)

type fixture struct {
	t       *testing.T
	l1      *mocks.L1Reader
	signer  *mocks.GERSigner
	service *Service
}

func newFixture(t *testing.T, rule finality.Rule, negativeTTL time.Duration) *fixture {
	t.Helper()
	f := &fixture{t: t, l1: mocks.NewL1Reader(t), signer: mocks.NewGERSigner(t)}
	f.signer.EXPECT().Address().Return(validator).Maybe()
	f.service = New(slog.New(slog.NewTextHandler(io.Discard, nil)), Config{
		AgglayerGER:    agglayerGER,
		Finality:       rule,
		NegativeTTL:    negativeTTL,
		RequestTimeout: 5 * time.Second,
	}, f.l1, f.signer, cache.New(100))
	return f
}

func finalized() finality.Rule { return finality.Rule{Tag: "finalized"} }

// expectInserted expects the GER to be in AgglayerGER, inserted in the block after parentNumber, which is canonical
func (f *fixture) expectInserted() {
	f.t.Helper()
	f.l1.EXPECT().GlobalExitRootMap(mock.Anything, agglayerGER, testGER).Return(parentHash, nil).Once()
	f.l1.EXPECT().BlockByHash(mock.Anything, parentHash).
		Return(&l1.Block{Number: parentNumber, Hash: parentHash}, nil).Once()
	f.l1.EXPECT().BlockByNumber(mock.Anything, parentNumber).
		Return(&l1.Block{Number: parentNumber, Hash: parentHash}, nil).Once()
}

func (f *fixture) expectTag(tag string, number uint64) {
	f.t.Helper()
	f.l1.EXPECT().BlockNumberByTag(mock.Anything, tag).Return(number, nil).Once()
}

func (f *fixture) expectSigning() {
	f.t.Helper()
	f.signer.EXPECT().SignGER(mock.Anything, testGER).Return(signature, nil).Once()
}

func TestService_SignGER(t *testing.T) {
	f := newFixture(t, finalized(), time.Minute)
	f.expectInserted()
	f.expectTag("finalized", 105)
	f.expectSigning()

	result, err := f.service.SignGER(context.Background(), testGER)
	require.NoError(t, err)
	require.Equal(t, &Result{GER: testGER, Signature: signature, Signer: validator}, result)
}

func TestService_SignGER_ZeroGER(t *testing.T) {
	f := newFixture(t, finalized(), time.Minute) // the mocks have no expectation: nothing is queried

	result, err := f.service.SignGER(context.Background(), common.Hash{})
	require.ErrorIs(t, err, ErrInvalidGER)
	require.Nil(t, result)
}

func TestService_SignGER_FinalityBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		rule      finality.Rule
		tagged    uint64
		wantSign  bool
		wantFinal uint64
		hasFinal  bool
	}{
		{name: "the insertion block is the tagged one", rule: finalized(), tagged: 101, wantSign: true},
		{name: "the tagged block is before the insertion block", rule: finalized(), tagged: 100, wantFinal: 100, hasFinal: true},
		{name: "head minus 6: the insertion block is exactly the final one", rule: finality.Rule{Tag: "latest", NumBlocks: 6}, tagged: 107, wantSign: true},
		{name: "head minus 6: one block too early", rule: finality.Rule{Tag: "latest", NumBlocks: 6}, tagged: 106, wantFinal: 100, hasFinal: true},
		{name: "head minus 6 in a very short chain", rule: finality.Rule{Tag: "latest", NumBlocks: 6}, tagged: 5, wantFinal: 0, hasFinal: false},
		{name: "latest without margin: always final", rule: finality.Rule{Tag: "latest"}, tagged: 101, wantSign: true},
		{name: "safe", rule: finality.Rule{Tag: "safe"}, tagged: 1000, wantSign: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, tc.rule, time.Minute)
			f.expectInserted()
			f.expectTag(tc.rule.Tag, tc.tagged)
			if tc.wantSign {
				f.expectSigning()
			}

			result, err := f.service.SignGER(context.Background(), testGER)
			if tc.wantSign {
				require.NoError(t, err)
				require.Equal(t, signature, result.Signature)
				return
			}
			require.ErrorIs(t, err, ErrGERNotFinal)
			var notFinal *NotFinalError
			require.ErrorAs(t, err, &notFinal)
			require.Equal(t, parentNumber+1, notFinal.InsertionBlock)
			require.Equal(t, tc.wantFinal, notFinal.FinalBlock)
			require.Equal(t, tc.hasFinal, notFinal.HasFinalBlock)
			require.Nil(t, result)
		})
	}
}

func TestNotFinalError_Message(t *testing.T) {
	withFinal := &NotFinalError{InsertionBlock: 101, FinalBlock: 100, HasFinalBlock: true}
	require.ErrorIs(t, withFinal, ErrGERNotFinal)
	require.ErrorContains(t, withFinal, "inserted in L1 block 101 and the final block is 100")

	noFinal := &NotFinalError{InsertionBlock: 101}
	require.ErrorContains(t, noFinal, "no block is final yet")
}

func TestService_SignGER_Rejections(t *testing.T) {
	t.Run("unknown GER", func(t *testing.T) {
		f := newFixture(t, finalized(), time.Minute)
		f.l1.EXPECT().GlobalExitRootMap(mock.Anything, agglayerGER, testGER).Return(common.Hash{}, nil).Once()

		_, err := f.service.SignGER(context.Background(), testGER)
		require.ErrorIs(t, err, ErrGERUnknown)
	})

	t.Run("the block of the hash is unknown to the node", func(t *testing.T) {
		f := newFixture(t, finalized(), time.Minute)
		f.l1.EXPECT().GlobalExitRootMap(mock.Anything, agglayerGER, testGER).Return(parentHash, nil).Once()
		f.l1.EXPECT().BlockByHash(mock.Anything, parentHash).Return(nil, l1.ErrBlockNotFound).Once()

		_, err := f.service.SignGER(context.Background(), testGER)
		require.ErrorIs(t, err, ErrGERBlockNotCanonical)
	})

	t.Run("the canonical block of that height is another one (an orphan was returned by hash)", func(t *testing.T) {
		f := newFixture(t, finalized(), time.Minute)
		f.l1.EXPECT().GlobalExitRootMap(mock.Anything, agglayerGER, testGER).Return(parentHash, nil).Once()
		f.l1.EXPECT().BlockByHash(mock.Anything, parentHash).
			Return(&l1.Block{Number: parentNumber, Hash: parentHash}, nil).Once()
		f.l1.EXPECT().BlockByNumber(mock.Anything, parentNumber).
			Return(&l1.Block{Number: parentNumber, Hash: otherHash}, nil).Once()

		_, err := f.service.SignGER(context.Background(), testGER)
		require.ErrorIs(t, err, ErrGERBlockNotCanonical)
		require.ErrorContains(t, err, otherHash.Hex())
	})

	t.Run("there is no canonical block of that height", func(t *testing.T) {
		f := newFixture(t, finalized(), time.Minute)
		f.l1.EXPECT().GlobalExitRootMap(mock.Anything, agglayerGER, testGER).Return(parentHash, nil).Once()
		f.l1.EXPECT().BlockByHash(mock.Anything, parentHash).
			Return(&l1.Block{Number: parentNumber, Hash: parentHash}, nil).Once()
		f.l1.EXPECT().BlockByNumber(mock.Anything, parentNumber).Return(nil, l1.ErrBlockNotFound).Once()

		_, err := f.service.SignGER(context.Background(), testGER)
		require.ErrorIs(t, err, ErrGERBlockNotCanonical)
	})

	t.Run("not canonical wins over not final", func(t *testing.T) {
		f := newFixture(t, finalized(), time.Minute)
		f.l1.EXPECT().GlobalExitRootMap(mock.Anything, agglayerGER, testGER).Return(parentHash, nil).Once()
		f.l1.EXPECT().BlockByHash(mock.Anything, parentHash).
			Return(&l1.Block{Number: parentNumber, Hash: parentHash}, nil).Once()
		f.l1.EXPECT().BlockByNumber(mock.Anything, parentNumber).
			Return(&l1.Block{Number: parentNumber, Hash: otherHash}, nil).Once()
		// BlockNumberByTag is not even asked

		_, err := f.service.SignGER(context.Background(), testGER)
		require.ErrorIs(t, err, ErrGERBlockNotCanonical)
		require.NotErrorIs(t, err, ErrGERNotFinal)
	})
}

func TestService_SignGER_FailuresAreNotRejections(t *testing.T) {
	steps := map[string]func(f *fixture){
		"reading the contract": func(f *fixture) {
			f.l1.EXPECT().GlobalExitRootMap(mock.Anything, agglayerGER, testGER).Return(common.Hash{}, errL1).Once()
		},
		"getting the block by hash": func(f *fixture) {
			f.l1.EXPECT().GlobalExitRootMap(mock.Anything, agglayerGER, testGER).Return(parentHash, nil).Once()
			f.l1.EXPECT().BlockByHash(mock.Anything, parentHash).Return(nil, errL1).Once()
		},
		"getting the block by number": func(f *fixture) {
			f.l1.EXPECT().GlobalExitRootMap(mock.Anything, agglayerGER, testGER).Return(parentHash, nil).Once()
			f.l1.EXPECT().BlockByHash(mock.Anything, parentHash).
				Return(&l1.Block{Number: parentNumber, Hash: parentHash}, nil).Once()
			f.l1.EXPECT().BlockByNumber(mock.Anything, parentNumber).Return(nil, errL1).Once()
		},
		"getting the tag": func(f *fixture) {
			f.expectInserted()
			f.l1.EXPECT().BlockNumberByTag(mock.Anything, "finalized").Return(0, errL1).Once()
		},
	}
	for step, setup := range steps {
		t.Run(step, func(t *testing.T) {
			f := newFixture(t, finalized(), time.Minute)
			setup(f)

			_, err := f.service.SignGER(context.Background(), testGER)
			require.ErrorIs(t, err, errL1)
			for _, rejection := range []error{ErrGERUnknown, ErrGERBlockNotCanonical, ErrGERNotFinal} {
				require.NotErrorIs(t, err, rejection)
			}

			// A failure is not cached: the next request asks L1 again, and it can succeed
			f.expectInserted()
			f.expectTag("finalized", 105)
			f.expectSigning()
			result, err := f.service.SignGER(context.Background(), testGER)
			require.NoError(t, err)
			require.Equal(t, signature, result.Signature)
		})
	}
}

func TestService_SignGER_SignerFailure(t *testing.T) {
	f := newFixture(t, finalized(), time.Minute)
	f.expectInserted()
	f.expectTag("finalized", 105)
	f.signer.EXPECT().SignGER(mock.Anything, testGER).Return(nil, errKMS).Once()

	result, err := f.service.SignGER(context.Background(), testGER)
	require.ErrorIs(t, err, errKMS)
	require.Nil(t, result)

	// It is not cached: when the signer works again the GER is signed
	f.expectInserted()
	f.expectTag("finalized", 105)
	f.expectSigning()
	result, err = f.service.SignGER(context.Background(), testGER)
	require.NoError(t, err)
	require.Equal(t, signature, result.Signature)
}

func TestService_SignGER_SignaturesAreCached(t *testing.T) {
	f := newFixture(t, finalized(), time.Minute)
	f.expectInserted() // every expectation is Once: a second query to L1 or the signer would fail the test
	f.expectTag("finalized", 105)
	f.expectSigning()

	for range 5 {
		result, err := f.service.SignGER(context.Background(), testGER)
		require.NoError(t, err)
		require.Equal(t, signature, result.Signature)
		require.Equal(t, validator, result.Signer)
	}
}

func TestService_SignGER_NegativeAnswersAreCached(t *testing.T) {
	t.Run("unknown GER, for the TTL", func(t *testing.T) {
		f := newFixture(t, finalized(), 100*time.Millisecond)
		f.l1.EXPECT().GlobalExitRootMap(mock.Anything, agglayerGER, testGER).Return(common.Hash{}, nil).Once()

		for range 3 {
			_, err := f.service.SignGER(context.Background(), testGER)
			require.ErrorIs(t, err, ErrGERUnknown)
		}

		// After the TTL L1 is asked again, and now the GER is there
		time.Sleep(150 * time.Millisecond)
		f.expectInserted()
		f.expectTag("finalized", 105)
		f.expectSigning()
		result, err := f.service.SignGER(context.Background(), testGER)
		require.NoError(t, err)
		require.Equal(t, signature, result.Signature)
	})

	t.Run("not final, with the detail", func(t *testing.T) {
		f := newFixture(t, finalized(), time.Minute)
		f.expectInserted()
		f.expectTag("finalized", 90)

		for range 3 {
			_, err := f.service.SignGER(context.Background(), testGER)
			var notFinal *NotFinalError
			require.ErrorAs(t, err, &notFinal)
			require.Equal(t, uint64(101), notFinal.InsertionBlock)
		}
	})

	t.Run("without TTL nothing negative is cached", func(t *testing.T) {
		f := newFixture(t, finalized(), 0)
		f.expectInserted()
		f.expectTag("finalized", 90)
		_, err := f.service.SignGER(context.Background(), testGER)
		require.ErrorIs(t, err, ErrGERNotFinal)

		// The block became final: the next request is not answered with the cached rejection
		f.expectInserted()
		f.expectTag("finalized", 105)
		f.expectSigning()
		_, err = f.service.SignGER(context.Background(), testGER)
		require.NoError(t, err)
	})
}

// Concurrent requests for the same GER share one query to L1 and one signature
func TestService_SignGER_ConcurrentRequestsShareTheWork(t *testing.T) {
	f := newFixture(t, finalized(), time.Minute)
	release := make(chan struct{})
	// Once: a second query would fail the test, whether it comes from a request that didn't join the flight
	// or from the cache
	f.l1.EXPECT().GlobalExitRootMap(mock.Anything, agglayerGER, testGER).
		RunAndReturn(func(context.Context, common.Address, common.Hash) (common.Hash, error) {
			<-release
			return parentHash, nil
		}).Once()
	f.l1.EXPECT().BlockByHash(mock.Anything, parentHash).
		Return(&l1.Block{Number: parentNumber, Hash: parentHash}, nil).Once()
	f.l1.EXPECT().BlockByNumber(mock.Anything, parentNumber).
		Return(&l1.Block{Number: parentNumber, Hash: parentHash}, nil).Once()
	f.expectTag("finalized", 105)
	f.expectSigning()

	const requests = 20
	var wg sync.WaitGroup
	results := make([]*Result, requests)
	errs := make([]error, requests)
	for i := range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = f.service.SignGER(context.Background(), testGER)
		}()
	}
	time.Sleep(100 * time.Millisecond) // the requests pile up behind the first one
	close(release)
	wg.Wait()

	for i := range requests {
		require.NoError(t, errs[i])
		require.Equal(t, signature, results[i].Signature)
	}
}

// The caller that gives up doesn't make the others fail: the work is not tied to its context
func TestService_SignGER_ACallerGivingUpDoesNotAffectTheOthers(t *testing.T) {
	f := newFixture(t, finalized(), time.Minute)
	started := make(chan struct{})
	release := make(chan struct{})
	f.l1.EXPECT().GlobalExitRootMap(mock.Anything, agglayerGER, testGER).
		RunAndReturn(func(ctx context.Context, _ common.Address, _ common.Hash) (common.Hash, error) {
			close(started)
			select {
			case <-release:
				return parentHash, nil
			case <-ctx.Done():
				return common.Hash{}, ctx.Err()
			}
		}).Once()
	f.l1.EXPECT().BlockByHash(mock.Anything, parentHash).
		Return(&l1.Block{Number: parentNumber, Hash: parentHash}, nil).Once()
	f.l1.EXPECT().BlockByNumber(mock.Anything, parentNumber).
		Return(&l1.Block{Number: parentNumber, Hash: parentHash}, nil).Once()
	f.expectTag("finalized", 105)
	f.expectSigning()

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() {
		_, err := f.service.SignGER(firstCtx, testGER)
		firstDone <- err
	}()
	<-started

	secondDone := make(chan *Result, 1)
	go func() {
		result, err := f.service.SignGER(context.Background(), testGER)
		require.NoError(t, err)
		secondDone <- result
	}()
	time.Sleep(50 * time.Millisecond) // the second one joins the flight

	cancelFirst()
	require.ErrorIs(t, <-firstDone, context.Canceled)

	close(release)
	select {
	case result := <-secondDone:
		require.Equal(t, signature, result.Signature)
	case <-time.After(5 * time.Second):
		require.Fail(t, "the second request didn't finish")
	}
}

func TestService_SignGER_RequestTimeout(t *testing.T) {
	f := newFixture(t, finalized(), time.Minute)
	f.service.cfg.RequestTimeout = 50 * time.Millisecond
	f.l1.EXPECT().GlobalExitRootMap(mock.Anything, agglayerGER, testGER).
		RunAndReturn(func(ctx context.Context, _ common.Address, _ common.Hash) (common.Hash, error) {
			<-ctx.Done() // an L1 that never answers
			return common.Hash{}, ctx.Err()
		}).Once()

	start := time.Now()
	_, err := f.service.SignGER(context.Background(), testGER)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), 5*time.Second)
}

func TestService_Info(t *testing.T) {
	f := newFixture(t, finality.Rule{Tag: "latest", NumBlocks: 6}, time.Minute)
	require.Equal(t, Info{
		Signer:          validator,
		AgglayerGER:     agglayerGER,
		FinalityTag:     "latest",
		FinalityNumBlks: 6,
	}, f.service.Info())
}
