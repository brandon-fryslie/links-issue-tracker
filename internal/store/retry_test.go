package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeRetryOperation struct {
	results []error
	calls   int
}

func (f *fakeRetryOperation) run(_ context.Context) error {
	f.calls++
	if len(f.results) == 0 {
		return errors.New("unexpected call")
	}
	current := f.results[0]
	f.results = f.results[1:]
	return current
}

func TestRetryTransientManifestReadOnlyRetriesTransientError(t *testing.T) {
	op := &fakeRetryOperation{
		results: []error{
			transientManifestReadOnlyError{err: errors.New("transient manifest read only")},
			nil,
		},
	}

	err := retryTransientManifestReadOnly(
		context.Background(),
		op.run,
		func(int) time.Duration { return 0 },
		func(context.Context, time.Duration) error { return nil },
	)
	if err != nil {
		t.Fatalf("retryTransientManifestReadOnly() error = %v", err)
	}
	if op.calls != 2 {
		t.Fatalf("op.calls = %d, want 2", op.calls)
	}
}

func TestRetryTransientManifestReadOnlyReturnsLastErrorAfterExhaustion(t *testing.T) {
	lastErr := transientManifestReadOnlyError{err: errors.New("transient 3")}
	op := &fakeRetryOperation{
		results: []error{
			transientManifestReadOnlyError{err: errors.New("transient 1")},
			transientManifestReadOnlyError{err: errors.New("transient 2")},
			lastErr,
		},
	}

	err := retryTransientManifestReadOnly(
		context.Background(),
		op.run,
		func(int) time.Duration { return 0 },
		func(context.Context, time.Duration) error { return nil },
	)
	if err == nil {
		t.Fatal("retryTransientManifestReadOnly() error = nil, want non-nil")
	}
	if !errors.Is(err, ErrTransientManifestReadOnly) {
		t.Fatalf("error = %v, want ErrTransientManifestReadOnly", err)
	}
	if err.Error() != lastErr.Error() {
		t.Fatalf("error = %q, want %q", err.Error(), lastErr.Error())
	}
	if op.calls != transientManifestRetryMaxAttempts {
		t.Fatalf("op.calls = %d, want %d", op.calls, transientManifestRetryMaxAttempts)
	}
}

func TestRetryTransientManifestReadOnlyDoesNotRetryNonTransientError(t *testing.T) {
	op := &fakeRetryOperation{
		results: []error{
			errors.New("some other storage failure"),
			nil,
		},
	}

	err := retryTransientManifestReadOnly(
		context.Background(),
		op.run,
		func(int) time.Duration { return 0 },
		func(context.Context, time.Duration) error { return nil },
	)
	if err == nil {
		t.Fatal("retryTransientManifestReadOnly() error = nil, want non-nil")
	}
	if op.calls != 1 {
		t.Fatalf("op.calls = %d, want 1", op.calls)
	}
}

func TestRetryTransientManifestReadOnlyHonorsContextTimeoutDuringBackoff(t *testing.T) {
	op := &fakeRetryOperation{
		results: []error{
			transientManifestReadOnlyError{err: errors.New("transient timeout")},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()

	err := retryTransientManifestReadOnly(
		ctx,
		op.run,
		func(int) time.Duration { return 50 * time.Millisecond },
		waitWithContext,
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("retryTransientManifestReadOnly() error = %v, want context.DeadlineExceeded", err)
	}
	if op.calls != 1 {
		t.Fatalf("op.calls = %d, want 1", op.calls)
	}
}

func TestTransientManifestRetryDelayIsBounded(t *testing.T) {
	for attempt := 1; attempt <= 10; attempt++ {
		delay := transientManifestRetryDelay(attempt)
		if delay < transientManifestRetryBaseDelay {
			t.Fatalf("delay(%d) = %v, want >= %v", attempt, delay, transientManifestRetryBaseDelay)
		}
		max := transientManifestRetryMaxDelay + transientManifestRetryJitter
		if delay > max {
			t.Fatalf("delay(%d) = %v, want <= %v", attempt, delay, max)
		}
	}
}

func TestWrapCommitWorkingSetErrorMarksTransientManifestReadOnly(t *testing.T) {
	err := wrapCommitWorkingSetError(errors.New("Error 1105: cannot update manifest: database is read only"))
	if !errors.Is(err, ErrTransientManifestReadOnly) {
		t.Fatalf("errors.Is(err, ErrTransientManifestReadOnly) = false, err=%v", err)
	}
	if !strings.Contains(err.Error(), "dolt commit working set") || !strings.Contains(err.Error(), "cannot update manifest") {
		t.Fatalf("unexpected wrapped error text: %q", err.Error())
	}
}

func TestWrapCommitWorkingSetErrorLeavesNonTransientUnmarked(t *testing.T) {
	err := wrapCommitWorkingSetError(errors.New("permission denied"))
	if errors.Is(err, ErrTransientManifestReadOnly) {
		t.Fatalf("errors.Is(err, ErrTransientManifestReadOnly) = true, err=%v", err)
	}
	if got, want := err.Error(), "dolt commit working set: permission denied"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}
