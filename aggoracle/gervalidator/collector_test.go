package gervalidator

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const testTimeout = 5 * time.Second

// testValidator is a validator with its key
type testValidator struct {
	Validator
	key *ecdsa.PrivateKey
}

func newTestValidators(t *testing.T, n int) []testValidator {
	t.Helper()
	validators := make([]testValidator, n)
	for i := range validators {
		key := newKey(t)
		validators[i] = testValidator{
			Validator: Validator{
				Address: crypto.PubkeyToAddress(key.PublicKey),
				URL:     fmt.Sprintf("http://validator-%d.example.com", i),
			},
			key: key,
		}
	}
	return validators
}

func plain(validators []testValidator) []Validator {
	out := make([]Validator, len(validators))
	for i, v := range validators {
		out[i] = v.Validator
	}
	return out
}

// signedResponse is the response of a validator that signs the GER correctly
func (v testValidator) signedResponse(t *testing.T, ger common.Hash) *SignResponse {
	t.Helper()
	return &SignResponse{
		GER:       ger,
		Signature: withV27(signGER(t, v.key, ger)),
		Signer:    v.Address,
	}
}

func newCollector(t *testing.T, requestTimeout time.Duration) (*Collector, *ClientMock) {
	t.Helper()
	client := NewClientMock(t)
	return NewCollector(log.GetDefaultLogger(), client, requestTimeout), client
}

func requireValidResult(t *testing.T, result *Result, ger common.Hash, threshold int) {
	t.Helper()
	require.NotNil(t, result)
	require.Len(t, result.Signers, threshold)
	require.Len(t, result.Signatures, threshold*SignatureLength)
	for i, signer := range result.Signers {
		if i > 0 {
			require.Equal(t, -1, bytes.Compare(result.Signers[i-1].Bytes(), signer.Bytes()),
				"signers must be in strictly ascending order")
		}
		chunk := result.Signatures[i*SignatureLength : (i+1)*SignatureLength]
		require.Contains(t, []byte{27, 28}, chunk[vIndex], "v must be 27 or 28")
		_, recovered, err := RecoverSigner(ger, chunk)
		require.NoError(t, err)
		require.Equal(t, signer, recovered)
	}
}

func TestCollector_Collect_AllValidatorsSign(t *testing.T) {
	validators := newTestValidators(t, 4)
	collector, client := newCollector(t, testTimeout)
	for _, v := range validators {
		client.EXPECT().SignGER(mock.Anything, v.URL, testGER).Return(v.signedResponse(t, testGER), nil).Maybe()
	}

	result, err := collector.Collect(context.Background(), testGER, plain(validators), 3)
	require.NoError(t, err)
	requireValidResult(t, result, testGER, 3)
}

func TestCollector_Collect_ThresholdEqualsValidators(t *testing.T) {
	validators := newTestValidators(t, 3)
	collector, client := newCollector(t, testTimeout)
	for _, v := range validators {
		client.EXPECT().SignGER(mock.Anything, v.URL, testGER).Return(v.signedResponse(t, testGER), nil).Once()
	}

	result, err := collector.Collect(context.Background(), testGER, plain(validators), 3)
	require.NoError(t, err)
	requireValidResult(t, result, testGER, 3)
}

func TestCollector_Collect_SingleValidator(t *testing.T) {
	validators := newTestValidators(t, 1)
	collector, client := newCollector(t, testTimeout)
	client.EXPECT().SignGER(mock.Anything, validators[0].URL, testGER).
		Return(validators[0].signedResponse(t, testGER), nil).Once()

	result, err := collector.Collect(context.Background(), testGER, plain(validators), 1)
	require.NoError(t, err)
	requireValidResult(t, result, testGER, 1)
}

func TestCollector_Collect_SignaturesWithV0Or1AreNormalized(t *testing.T) {
	validators := newTestValidators(t, 1)
	collector, client := newCollector(t, testTimeout)
	client.EXPECT().SignGER(mock.Anything, validators[0].URL, testGER).Return(&SignResponse{
		GER:       testGER,
		Signature: signGER(t, validators[0].key, testGER), // v is 0 or 1
	}, nil).Once()

	result, err := collector.Collect(context.Background(), testGER, plain(validators), 1)
	require.NoError(t, err)
	requireValidResult(t, result, testGER, 1)
}

// Once the threshold is reached the pending requests are cancelled, and Collect doesn't wait for them
func TestCollector_Collect_StopsAtThreshold(t *testing.T) {
	validators := newTestValidators(t, 3)
	collector, client := newCollector(t, testTimeout)

	client.EXPECT().SignGER(mock.Anything, validators[0].URL, testGER).
		Return(validators[0].signedResponse(t, testGER), nil).Once()
	client.EXPECT().SignGER(mock.Anything, validators[1].URL, testGER).
		Return(validators[1].signedResponse(t, testGER), nil).Once()
	cancelled := make(chan struct{})
	client.EXPECT().SignGER(mock.Anything, validators[2].URL, testGER).
		RunAndReturn(func(ctx context.Context, _ string, _ common.Hash) (*SignResponse, error) {
			<-ctx.Done()
			close(cancelled)
			return nil, ctx.Err()
		}).Once()

	result, err := collector.Collect(context.Background(), testGER, plain(validators), 2)
	require.NoError(t, err)
	requireValidResult(t, result, testGER, 2)

	select {
	case <-cancelled:
	case <-time.After(testTimeout):
		require.Fail(t, "the request of the slow validator was not cancelled")
	}
}

func TestCollector_Collect_DuplicatedValidatorsAreAskedOnce(t *testing.T) {
	validators := newTestValidators(t, 2)
	collector, client := newCollector(t, testTimeout)
	for _, v := range validators {
		client.EXPECT().SignGER(mock.Anything, v.URL, testGER).Return(v.signedResponse(t, testGER), nil).Once()
	}

	input := append(plain(validators), validators[0].Validator)
	result, err := collector.Collect(context.Background(), testGER, input, 2)
	require.NoError(t, err)
	requireValidResult(t, result, testGER, 2)
}

func TestCollector_Collect_InvalidArguments(t *testing.T) {
	validators := plain(newTestValidators(t, 2))
	collector, _ := newCollector(t, testTimeout)

	_, err := collector.Collect(context.Background(), common.Hash{}, validators, 1)
	require.ErrorContains(t, err, "can't be zero")

	_, err = collector.Collect(context.Background(), testGER, validators, 0)
	require.ErrorContains(t, err, "invalid threshold 0")

	_, err = collector.Collect(context.Background(), testGER, validators, -1)
	require.ErrorContains(t, err, "invalid threshold -1")

	_, err = collector.Collect(context.Background(), testGER, validators, 3)
	require.ErrorContains(t, err, "greater than the number of validators (2)")

	_, err = collector.Collect(context.Background(), testGER, nil, 1)
	require.ErrorContains(t, err, "greater than the number of validators (0)")

	// Duplicated addresses don't count twice
	_, err = collector.Collect(context.Background(), testGER, []Validator{validators[0], validators[0]}, 2)
	require.ErrorContains(t, err, "greater than the number of validators (1)")
}

// requireThresholdNotReached checks the error and returns it
func requireThresholdNotReached(t *testing.T, err error, valid int) *ThresholdNotReachedError {
	t.Helper()
	require.ErrorIs(t, err, ErrThresholdNotReached)
	var notReached *ThresholdNotReachedError
	require.ErrorAs(t, err, &notReached)
	require.Equal(t, testGER, notReached.GER)
	require.Equal(t, valid, notReached.Valid)
	return notReached
}

func TestCollector_Collect_FailuresAreClassified(t *testing.T) {
	validators := newTestValidators(t, 4)
	collector, client := newCollector(t, testTimeout)

	otherGER := common.HexToHash("0x99")
	failures := []struct {
		validator testValidator
		resp      *SignResponse
		err       error
		kind      FailureKind
	}{
		{validator: validators[0], err: ErrGERNotFinal, kind: FailureTransient},
		{validator: validators[1], err: ErrGERUnknown, kind: FailureGERRejected},
		{validator: validators[2], resp: &SignResponse{GER: otherGER}, kind: FailureInvalid},
		{validator: validators[3], err: errors.New("connection refused"), kind: FailureTransient},
	}
	for _, f := range failures {
		client.EXPECT().SignGER(mock.Anything, f.validator.URL, testGER).Return(f.resp, f.err).Once()
	}

	result, err := collector.Collect(context.Background(), testGER, plain(validators), 2)
	require.Nil(t, result)
	notReached := requireThresholdNotReached(t, err, 0)
	require.Len(t, notReached.Failures, 4)

	kinds := map[common.Address]FailureKind{}
	for _, failure := range notReached.Failures {
		kinds[failure.Validator] = failure.Kind
	}
	for _, f := range failures {
		require.Equal(t, f.kind, kinds[f.validator.Address], "validator %s", f.validator.Address)
	}
	for i := 1; i < len(notReached.Failures); i++ {
		require.Equal(t, -1, bytes.Compare(notReached.Failures[i-1].Validator.Bytes(), notReached.Failures[i].Validator.Bytes()),
			"failures must be sorted by validator address")
	}
	require.ErrorContains(t, err, "2 transient, 1 ger-rejected, 1 invalid")
}

func TestCollector_Collect_Retryable(t *testing.T) {
	tests := []struct {
		name      string
		threshold int
		results   func(validators []testValidator) []error // one per validator, nil means signs
		valid     int
		retryable bool
	}{
		{
			name: "not final everywhere: retrying can reach the threshold", threshold: 2, valid: 0, retryable: true,
			results: func(v []testValidator) []error { return []error{ErrGERNotFinal, ErrGERNotFinal, ErrGERNotFinal} },
		},
		{
			name: "one signs, the others are not final", threshold: 2, valid: 1, retryable: true,
			results: func(v []testValidator) []error { return []error{nil, ErrGERNotFinal, ErrRateLimited} },
		},
		{
			name: "too many validators reject the GER", threshold: 2, valid: 1, retryable: false,
			results: func(v []testValidator) []error { return []error{nil, ErrGERUnknown, ErrGERNotCanonical} },
		},
		{
			name: "rejected by some, but the remaining can still reach the threshold", threshold: 2, valid: 0, retryable: true,
			results: func(v []testValidator) []error { return []error{ErrGERUnknown, ErrGERNotFinal, ErrRateLimited} },
		},
		{
			name: "invalid validators don't count", threshold: 3, valid: 1, retryable: false,
			results: func(v []testValidator) []error { return []error{nil, ErrInvalidResponse, ErrNoURL} },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			validators := newTestValidators(t, 3)
			collector, client := newCollector(t, testTimeout)
			for i, err := range tc.results(validators) {
				resp := (*SignResponse)(nil)
				if err == nil {
					resp = validators[i].signedResponse(t, testGER)
				}
				client.EXPECT().SignGER(mock.Anything, validators[i].URL, testGER).Return(resp, err).Once()
			}

			_, err := collector.Collect(context.Background(), testGER, plain(validators), tc.threshold)
			notReached := requireThresholdNotReached(t, err, tc.valid)
			require.Equal(t, tc.retryable, notReached.Retryable())
		})
	}
}

func TestCollector_Collect_RejectsInvalidSignatures(t *testing.T) {
	validators := newTestValidators(t, 5)
	collector, client := newCollector(t, testTimeout)
	other := newKey(t)

	// 0: a signature of another validator
	client.EXPECT().SignGER(mock.Anything, validators[0].URL, testGER).
		Return(validators[1].signedResponse(t, testGER), nil).Once()
	// 1: signs the GER with a key that is not its own, claiming to be validator 1
	client.EXPECT().SignGER(mock.Anything, validators[1].URL, testGER).Return(&SignResponse{
		GER: testGER, Signature: withV27(signGER(t, other, testGER)), Signer: validators[1].Address,
	}, nil).Once()
	// 2: signature too short
	client.EXPECT().SignGER(mock.Anything, validators[2].URL, testGER).
		Return(&SignResponse{GER: testGER, Signature: hexutil.Bytes{0x01, 0x02}}, nil).Once()
	// 3: signature of another GER
	client.EXPECT().SignGER(mock.Anything, validators[3].URL, testGER).
		Return(&SignResponse{GER: testGER, Signature: withV27(signGER(t, validators[3].key, common.HexToHash("0x99")))}, nil).Once()
	// 4: no response
	client.EXPECT().SignGER(mock.Anything, validators[4].URL, testGER).Return(nil, nil).Once()

	_, err := collector.Collect(context.Background(), testGER, plain(validators), 1)
	notReached := requireThresholdNotReached(t, err, 0)
	require.Len(t, notReached.Failures, 5)
	for _, failure := range notReached.Failures {
		require.Equal(t, FailureInvalid, failure.Kind, "validator %s: %s", failure.Validator, failure.Err)
		require.ErrorIs(t, failure.Err, ErrInvalidResponse)
	}
	require.False(t, notReached.Retryable())
}

func TestCollector_Collect_ValidatorWithoutURL(t *testing.T) {
	validators := newTestValidators(t, 2)
	validators[0].URL = ""
	collector, client := newCollector(t, testTimeout)
	// The validator without URL is never requested
	client.EXPECT().SignGER(mock.Anything, validators[1].URL, testGER).
		Return(validators[1].signedResponse(t, testGER), nil).Once()

	_, err := collector.Collect(context.Background(), testGER, plain(validators), 2)
	notReached := requireThresholdNotReached(t, err, 1)
	require.Len(t, notReached.Failures, 1)
	require.Equal(t, validators[0].Address, notReached.Failures[0].Validator)
	require.ErrorIs(t, notReached.Failures[0].Err, ErrNoURL)
	require.Equal(t, FailureInvalid, notReached.Failures[0].Kind)
}

func TestCollector_Collect_PanicIsolatedPerValidator(t *testing.T) {
	validators := newTestValidators(t, 3)
	collector, client := newCollector(t, testTimeout)
	client.EXPECT().SignGER(mock.Anything, validators[0].URL, testGER).
		RunAndReturn(func(context.Context, string, common.Hash) (*SignResponse, error) {
			panic("boom")
		}).Once()
	for _, v := range validators[1:] {
		client.EXPECT().SignGER(mock.Anything, v.URL, testGER).Return(v.signedResponse(t, testGER), nil).Once()
	}

	result, err := collector.Collect(context.Background(), testGER, plain(validators), 2)
	require.NoError(t, err)
	requireValidResult(t, result, testGER, 2)
	require.NotContains(t, result.Signers, validators[0].Address)
}

func TestCollector_Collect_RequestTimeout(t *testing.T) {
	validators := newTestValidators(t, 2)
	collector, client := newCollector(t, 50*time.Millisecond)
	client.EXPECT().SignGER(mock.Anything, validators[0].URL, testGER).
		RunAndReturn(func(ctx context.Context, _ string, _ common.Hash) (*SignResponse, error) {
			<-ctx.Done() // a validator that never answers
			return nil, ctx.Err()
		}).Once()
	client.EXPECT().SignGER(mock.Anything, validators[1].URL, testGER).
		Return(validators[1].signedResponse(t, testGER), nil).Once()

	start := time.Now()
	_, err := collector.Collect(context.Background(), testGER, plain(validators), 2)
	require.Less(t, time.Since(start), testTimeout)

	notReached := requireThresholdNotReached(t, err, 1)
	require.Len(t, notReached.Failures, 1)
	require.ErrorIs(t, notReached.Failures[0].Err, context.DeadlineExceeded)
	require.Equal(t, FailureTransient, notReached.Failures[0].Kind)
	require.True(t, notReached.Retryable())
}

func TestCollector_Collect_ParentContextCancelled(t *testing.T) {
	validators := newTestValidators(t, 2)
	collector, client := newCollector(t, testTimeout)
	for _, v := range validators {
		client.EXPECT().SignGER(mock.Anything, v.URL, testGER).
			RunAndReturn(func(ctx context.Context, _ string, _ common.Hash) (*SignResponse, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			}).Maybe()
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for range 20 { // the select between results and cancellation must always report the cancellation
		_, err := collector.Collect(ctx, testGER, plain(validators), 2)
		require.ErrorIs(t, err, context.Canceled)
		require.NotErrorIs(t, err, ErrThresholdNotReached)
	}
}

func TestThresholdNotReachedError_Message(t *testing.T) {
	err := &ThresholdNotReachedError{
		GER: testGER, Threshold: 3, Valid: 1,
		Failures: []ValidatorFailure{
			{Kind: FailureTransient}, {Kind: FailureGERRejected},
		},
	}
	require.ErrorIs(t, err, ErrThresholdNotReached)
	require.ErrorContains(t, err, "1 valid of threshold 3")
	require.ErrorContains(t, err, testGER.Hex())
}

func TestClassifyFailure(t *testing.T) {
	wrap := func(err error) error { return fmt.Errorf("context: %w", err) }
	tests := []struct {
		err  error
		kind FailureKind
	}{
		{ErrGERUnknown, FailureGERRejected},
		{wrap(ErrGERNotCanonical), FailureGERRejected},
		{ErrInvalidResponse, FailureInvalid},
		{wrap(ErrBadRequest), FailureInvalid},
		{ErrNoURL, FailureInvalid},
		{ErrGERNotFinal, FailureTransient},
		{ErrRateLimited, FailureTransient},
		{&APIError{StatusCode: 500}, FailureTransient},
		{context.DeadlineExceeded, FailureTransient},
		{errors.New("connection refused"), FailureTransient},
	}
	for _, tc := range tests {
		require.Equal(t, tc.kind, ClassifyFailure(tc.err), tc.err.Error())
	}
	require.Equal(t, "transient", FailureTransient.String())
	require.Equal(t, "ger-rejected", FailureGERRejected.String())
	require.Equal(t, "invalid", FailureInvalid.String())
	require.Equal(t, "unknown(9)", FailureKind(9).String())
}

func TestValidatorList(t *testing.T) {
	validators := []Validator{
		{Address: common.HexToAddress("0xa"), URL: "http://a"},
		{Address: common.HexToAddress("0xb")},
	}
	require.Equal(t,
		common.HexToAddress("0xa").Hex()+" (http://a), "+common.HexToAddress("0xb").Hex()+" (no URL)",
		validatorList(validators))
}

func TestDescribeAnswers(t *testing.T) {
	a, b, c := common.HexToAddress("0xa"), common.HexToAddress("0xb"), common.HexToAddress("0xc")
	validators := []Validator{{Address: a}, {Address: b}, {Address: c}}
	answers := map[common.Address]string{a: "signed", c: "transient (timeout)"}
	require.Equal(t,
		a.Hex()+": signed; "+b.Hex()+": not waited for; "+c.Hex()+": transient (timeout)",
		describeAnswers(validators, answers))
}
