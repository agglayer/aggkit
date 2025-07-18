package common

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/log"
)

func TestExecute_SuccessFirstTry(t *testing.T) {
	r := &RetryDelays{
		Delays:      []types.Duration{{Duration: 10 * time.Millisecond}},
		MaxAttempts: 3,
	}
	logger := log.WithFields("module", "ut")
	ctx := context.Background()
	fn := func() (int, error) {
		return 42, nil
	}
	result, err := Execute(r, ctx, logger, "test-success", fn)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result != 42 {
		t.Fatalf("expected result 42, got %v", result)
	}
}

func TestExecute_RetryAndSuccess(t *testing.T) {
	r := &RetryDelays{
		Delays:      []types.Duration{{Duration: 10 * time.Millisecond}, {Duration: 10 * time.Millisecond}},
		MaxAttempts: 3,
	}
	logger := log.WithFields("module", "ut")
	ctx := context.Background()
	attempts := 0
	fn := func() (string, error) {
		attempts++
		if attempts < 2 {
			return "", errors.New("fail")
		}
		return "ok", nil
	}
	result, err := Execute(r, ctx, logger, "test-retry-success", fn)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result != "ok" {
		t.Fatalf("expected result 'ok', got %v", result)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
}

func TestExecute_ExceedMaxAttempts(t *testing.T) {
	r := &RetryDelays{
		Delays:      []types.Duration{{Duration: 5 * time.Millisecond}},
		MaxAttempts: 2,
	}
	logger := log.WithFields("module", "ut")
	ctx := context.Background()
	fn := func() (int, error) {
		return 0, errors.New("fail")
	}
	result, err := Execute(r, ctx, logger, "test-max-attempts", fn)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if result != 0 {
		t.Fatalf("expected zero result, got %v", result)
	}
}

func TestExecute_ContextCancelled(t *testing.T) {
	r := &RetryDelays{
		Delays:      []types.Duration{{Duration: 50 * time.Millisecond}},
		MaxAttempts: 3,
	}
	logger := log.WithFields("module", "ut")
	ctx, cancel := context.WithCancel(context.Background())
	fn := func() (int, error) {
		cancel() // cancel context on first call
		return 0, errors.New("fail")
	}
	result, err := Execute(r, ctx, logger, "test-context-cancel", fn)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled error, got %v", err)
	}
	if result != 0 {
		t.Fatalf("expected zero result, got %v", result)
	}
}

func TestExecute_NilRetryDelays(t *testing.T) {
	logger := log.WithFields("module", "ut")
	ctx := context.Background()
	fn := func() (int, error) {
		return 1, nil
	}
	result, err := Execute[int](nil, ctx, logger, "test-nil-retrydelays", fn)
	if err == nil {
		t.Fatalf("expected error for nil RetryDelays, got nil")
	}
	if result != 0 {
		t.Fatalf("expected zero result, got %v", result)
	}
}

func TestExecute_NilLogger(t *testing.T) {
	r := &RetryDelays{
		Delays:      []types.Duration{{Duration: 1 * time.Millisecond}},
		MaxAttempts: 1,
	}
	ctx := context.Background()
	fn := func() (string, error) {
		return "ok", nil
	}
	result, err := Execute(r, ctx, nil, "test-nil-logger", fn)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result != "ok" {
		t.Fatalf("expected result 'ok', got %v", result)
	}
}
