package service

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"math/big"
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
	rule    finality.Rule
	l1      *mocks.L1Reader
	signer  *mocks.GERSigner
	service *Service
	// logs has what the service logged, at debug level
	logs *bytes.Buffer
}

func newFixture(t *testing.T, rule finality.Rule, negativeTTL time.Duration) *fixture {
	t.Helper()
	f := &fixture{t: t, rule: rule, l1: mocks.NewL1Reader(t), signer: mocks.NewGERSigner(t), logs: &bytes.Buffer{}}
	f.signer.EXPECT().Address().Return(validator).Maybe()
	logger := slog.New(slog.NewTextHandler(f.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	f.service = New(logger, Config{
		AgglayerGER:    agglayerGER,
		L1ChainID:      11155111,
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

// finalBlockHash is the hash of the final block number in the tests
func finalBlockHash(number uint64) common.Hash {
	return common.BigToHash(new(big.Int).SetUint64(0xf1a1<<32 | number))
}

// expectFinalState expects the GER to be read in the state of the final block, pinned by its hash, and to have
// there the parent block parentAtFinal
func (f *fixture) expectFinalState(finalBlock uint64, parentAtFinal common.Hash) {
	f.t.Helper()
	f.l1.EXPECT().BlockByNumber(mock.Anything, finalBlock).
		Return(&l1.Block{Number: finalBlock, Hash: finalBlockHash(finalBlock)}, nil).Once()
	f.l1.EXPECT().GlobalExitRootMapAt(mock.Anything, agglayerGER, testGER, finalBlockHash(finalBlock)).
		Return(parentAtFinal, nil).Once()
}

// expectFinal expects the block of the tag, that makes the insertion block final, and the GER in the state of the
// final block (the same parent block)
func (f *fixture) expectFinal(tag string, tagged uint64) {
	f.t.Helper()
	f.expectTag(tag, tagged)
	f.expectFinalState(tagged-f.rule.NumBlocks, parentHash)
}

func (f *fixture) expectSigning() {
	f.t.Helper()
	f.signer.EXPECT().SignGER(mock.Anything, testGER).Return(signature, nil).Once()
}

func TestService_SignGER(t *testing.T) {
	f := newFixture(t, finalized(), time.Minute)
	f.expectInserted()
	f.expectFinal("finalized", 105)
	f.expectSigning()

	result, err := f.service.SignGER(context.Background(), testGER)
	require.NoError(t, err)
	require.Equal(t, &Result{GER: testGER, Signature: signature, Signer: validator}, result)
}

// TestService_SignGER_DebugLogsTheChecks checks that the debug logs show each check on the GER and its data
func TestService_SignGER_DebugLogsTheChecks(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(f *fixture)
		expect []string
	}{
		{
			name: "signed",
			setup: func(f *fixture) {
				f.expectInserted()
				f.expectFinal("latest", 107)
				f.expectSigning()
			},
			expect: []string{
				`"GER check 1/4 (in L1?): yes`, "agglayerGER=" + agglayerGER.Hex(), "parentBlockHash=" + parentHash.Hex(),
				`"GER check 2/4 (canonical?): yes`, "parentBlock=100", "insertionBlock=101",
				`"GER check 3/4 (final?): yes`, "tag=latest", "taggedBlock=107", "numBlocks=6", "finalBlock=101",
				`"GER check 4/4 (in the final state?): yes`, "finalBlockHash=" + finalBlockHash(101).Hex(),
			},
		},
		{
			name: "not final",
			setup: func(f *fixture) {
				f.expectInserted()
				f.expectTag("latest", 104)
			},
			expect: []string{
				`"GER check 3/4 (final?): no: the insertion block is after the final block"`,
				"blockFinality=LatestBlock/-6", "taggedBlock=104", "latestBlock=104", "finalBlock=98",
				"blocksToFinal=3",
			},
		},
		{
			name: "not in L1",
			setup: func(f *fixture) {
				f.l1.EXPECT().GlobalExitRootMap(mock.Anything, agglayerGER, testGER).Return(common.Hash{}, nil).Once()
			},
			expect: []string{`"GER check 1/4 (in L1?): no`, "l1ChainID=11155111"},
		},
		{
			name: "reorganized",
			setup: func(f *fixture) {
				f.expectInserted()
				f.expectTag("latest", 107)
				f.expectFinalState(101, common.Hash{})
			},
			expect: []string{`"GER check 4/4 (in the final state?): no`, "finalBlock=101"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, finality.Rule{Tag: "latest", NumBlocks: 6}, time.Minute)
			tc.setup(f)

			_, _ = f.service.SignGER(context.Background(), testGER)
			for _, want := range tc.expect {
				require.Contains(t, f.logs.String(), want)
			}
		})
	}
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
			if !tc.wantSign && tc.rule.Tag != "latest" {
				f.expectTag("latest", tc.tagged+10)
			}
			if tc.wantSign {
				f.expectFinalState(tc.tagged-tc.rule.NumBlocks, parentHash)
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
			require.Equal(t, tc.rule, notFinal.Finality)
			require.Equal(t, tc.tagged, notFinal.TaggedBlock)
			finalBlock, hasFinal := notFinal.FinalBlock()
			require.Equal(t, tc.wantFinal, finalBlock)
			require.Equal(t, tc.hasFinal, hasFinal)
			require.Nil(t, result)
		})
	}
}

func TestNotFinalError_Message(t *testing.T) {
	tests := []struct {
		name string
		err  *NotFinalError
		want string
	}{
		{
			name: "finalized",
			err: &NotFinalError{InsertionBlock: 4305, Finality: finality.Rule{Tag: "finalized"},
				TaggedBlock: 4290, LatestBlock: 4310},
			want: "it was inserted in L1 block 4305 and, with BlockFinality FinalizedBlock, the final block is 4290 " +
				"(finalized block 4290, latest block 4310): 15 blocks to go",
		},
		{
			name: "safe with an offset",
			err: &NotFinalError{InsertionBlock: 4305, Finality: finality.Rule{Tag: "safe", NumBlocks: 2},
				TaggedBlock: 4300, LatestBlock: 4310},
			want: "it was inserted in L1 block 4305 and, with BlockFinality SafeBlock/-2, the final block is 4298 " +
				"(safe block 4300 - 2, latest block 4310): 7 blocks to go",
		},
		{
			name: "latest with an offset",
			err: &NotFinalError{InsertionBlock: 4305, Finality: finality.Rule{Tag: "latest", NumBlocks: 12},
				TaggedBlock: 4310, LatestBlock: 4310},
			want: "it was inserted in L1 block 4305 and, with BlockFinality LatestBlock/-12, the final block is " +
				"4298 (latest block 4310 - 12): 7 blocks to go",
		},
		{
			name: "no block is final yet",
			err: &NotFinalError{InsertionBlock: 3, Finality: finality.Rule{Tag: "latest", NumBlocks: 12},
				TaggedBlock: 5, LatestBlock: 5},
			want: "it was inserted in L1 block 3 and, with BlockFinality LatestBlock/-12, no block is final yet " +
				"(latest block 5 - 12): 10 blocks to go",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorIs(t, tc.err, ErrGERNotFinal)
			require.EqualError(t, tc.err, ErrGERNotFinal.Error()+": "+tc.want)
		})
	}
}

func TestService_SignGER_Rejections(t *testing.T) {
	t.Run("unknown GER", func(t *testing.T) {
		f := newFixture(t, finalized(), time.Minute)
		f.l1.EXPECT().GlobalExitRootMap(mock.Anything, agglayerGER, testGER).Return(common.Hash{}, nil).Once()

		_, err := f.service.SignGER(context.Background(), testGER)
		require.ErrorIs(t, err, ErrGERUnknown)
		require.EqualError(t, err, "global exit root not found in L1: it has no entry in globalExitRootMap of "+
			"the L1 contract AgglayerGER "+agglayerGER.Hex()+" (L1 chain 11155111)",
			"it says that it is the contract of L1, and which one")
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

// A reorg between the reads of L1 could make the height of the insertion block final while the block that inserted
// the GER is an orphan: the GER must be in the state of the final block, with the same parent block
func TestService_SignGER_ReorganizedInsertionIsNotSigned(t *testing.T) {
	tests := map[string]common.Hash{
		"the GER is not in the final state":                     {},
		"the GER was inserted after another block in the final": otherHash,
	}
	for name, parentAtFinal := range tests {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, finalized(), time.Minute)
			f.expectInserted()
			f.expectTag("finalized", 105)
			f.expectFinalState(105, parentAtFinal)
			// No signing expected: calling the signer fails the test

			result, err := f.service.SignGER(context.Background(), testGER)
			require.ErrorIs(t, err, ErrGERBlockNotCanonical)
			require.Nil(t, result)
		})
	}
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
		"getting the final block": func(f *fixture) {
			f.expectInserted()
			f.expectTag("finalized", 105)
			f.l1.EXPECT().BlockByNumber(mock.Anything, uint64(105)).Return(nil, errL1).Once()
		},
		// Also when the node refuses it because the final block stopped being canonical (requireCanonical)
		"reading the contract in the final block": func(f *fixture) {
			f.expectInserted()
			f.expectTag("finalized", 105)
			f.l1.EXPECT().BlockByNumber(mock.Anything, uint64(105)).
				Return(&l1.Block{Number: 105, Hash: finalBlockHash(105)}, nil).Once()
			f.l1.EXPECT().GlobalExitRootMapAt(mock.Anything, agglayerGER, testGER, finalBlockHash(105)).
				Return(common.Hash{}, errL1).Once()
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
			f.expectFinal("finalized", 105)
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
	f.expectFinal("finalized", 105)
	f.signer.EXPECT().SignGER(mock.Anything, testGER).Return(nil, errKMS).Once()

	result, err := f.service.SignGER(context.Background(), testGER)
	require.ErrorIs(t, err, errKMS)
	require.Nil(t, result)

	// It is not cached: when the signer works again the GER is signed
	f.expectInserted()
	f.expectFinal("finalized", 105)
	f.expectSigning()
	result, err = f.service.SignGER(context.Background(), testGER)
	require.NoError(t, err)
	require.Equal(t, signature, result.Signature)
}

func TestService_SignGER_SignaturesAreCached(t *testing.T) {
	f := newFixture(t, finalized(), time.Minute)
	f.expectInserted() // every expectation is Once: a second query to L1 or the signer would fail the test
	f.expectFinal("finalized", 105)
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
		f.expectFinal("finalized", 105)
		f.expectSigning()
		result, err := f.service.SignGER(context.Background(), testGER)
		require.NoError(t, err)
		require.Equal(t, signature, result.Signature)
	})

	t.Run("not final, with the detail", func(t *testing.T) {
		f := newFixture(t, finalized(), time.Minute)
		f.expectInserted()
		f.expectTag("finalized", 90)
		f.expectTag("latest", 120)

		for range 3 {
			_, err := f.service.SignGER(context.Background(), testGER)
			var notFinal *NotFinalError
			require.ErrorAs(t, err, &notFinal)
			require.Equal(t, uint64(101), notFinal.InsertionBlock)
			require.Equal(t, uint64(120), notFinal.LatestBlock)
			require.Equal(t, uint64(11), notFinal.BlocksToFinal())
		}
	})

	t.Run("without TTL nothing negative is cached", func(t *testing.T) {
		f := newFixture(t, finalized(), 0)
		f.expectInserted()
		f.expectTag("finalized", 90)
		f.expectTag("latest", 120)
		_, err := f.service.SignGER(context.Background(), testGER)
		require.ErrorIs(t, err, ErrGERNotFinal)

		// The block became final: the next request is not answered with the cached rejection
		f.expectInserted()
		f.expectFinal("finalized", 105)
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
	f.expectFinal("finalized", 105)
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
	f.expectFinal("finalized", 105)
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
