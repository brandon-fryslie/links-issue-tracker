package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bmf/links-issue-tracker/internal/model"
)

type transitionCallResult struct {
	issue model.Issue
	err   error
}

type fakeTransitionCall struct {
	results []transitionCallResult
	calls   int
}

func (f *fakeTransitionCall) run(_ context.Context, _ TransitionIssueInput) (model.Issue, error) {
	f.calls++
	if len(f.results) == 0 {
		return model.Issue{}, errors.New("unexpected call")
	}
	current := f.results[0]
	f.results = f.results[1:]
	return current.issue, current.err
}

func TestTransitionIssueWithRetryRetriesTransientError(t *testing.T) {
	call := &fakeTransitionCall{
		results: []transitionCallResult{
			{err: transientManifestReadOnlyError{err: errors.New("transient manifest read only")}},
			{issue: model.Issue{ID: "lit-test-1"}},
		},
	}

	issue, err := transitionIssueWithRetry(
		context.Background(),
		TransitionIssueInput{IssueID: "lit-test-1"},
		call.run,
		func(int) time.Duration { return 0 },
		func(context.Context, time.Duration) error { return nil },
	)
	if err != nil {
		t.Fatalf("transitionIssueWithRetry() error = %v", err)
	}
	if issue.ID != "lit-test-1" {
		t.Fatalf("issue.ID = %q, want lit-test-1", issue.ID)
	}
	if call.calls != 2 {
		t.Fatalf("call.calls = %d, want 2", call.calls)
	}
}

func TestTransitionIssueWithRetryReturnsLastErrorAfterExhaustion(t *testing.T) {
	lastErr := transientManifestReadOnlyError{err: errors.New("transient 3")}
	call := &fakeTransitionCall{
		results: []transitionCallResult{
			{err: transientManifestReadOnlyError{err: errors.New("transient 1")}},
			{err: transientManifestReadOnlyError{err: errors.New("transient 2")}},
			{err: lastErr},
		},
	}

	_, err := transitionIssueWithRetry(
		context.Background(),
		TransitionIssueInput{IssueID: "lit-test-2"},
		call.run,
		func(int) time.Duration { return 0 },
		func(context.Context, time.Duration) error { return nil },
	)
	if err == nil {
		t.Fatal("transitionIssueWithRetry() error = nil, want non-nil")
	}
	if !errors.Is(err, ErrTransientManifestReadOnly) {
		t.Fatalf("error = %v, want ErrTransientManifestReadOnly", err)
	}
	if err.Error() != lastErr.Error() {
		t.Fatalf("error = %q, want %q", err.Error(), lastErr.Error())
	}
	if call.calls != transitionIssueRetryMaxAttempts {
		t.Fatalf("call.calls = %d, want %d", call.calls, transitionIssueRetryMaxAttempts)
	}
}

func TestTransitionIssueWithRetryDoesNotRetryNonTransientError(t *testing.T) {
	call := &fakeTransitionCall{
		results: []transitionCallResult{
			{err: errors.New("some other storage failure")},
			{issue: model.Issue{ID: "lit-test-3"}},
		},
	}

	_, err := transitionIssueWithRetry(
		context.Background(),
		TransitionIssueInput{IssueID: "lit-test-3"},
		call.run,
		func(int) time.Duration { return 0 },
		func(context.Context, time.Duration) error { return nil },
	)
	if err == nil {
		t.Fatal("transitionIssueWithRetry() error = nil, want non-nil")
	}
	if call.calls != 1 {
		t.Fatalf("call.calls = %d, want 1", call.calls)
	}
}

func TestTransitionIssueWithRetryHonorsContextTimeoutDuringBackoff(t *testing.T) {
	call := &fakeTransitionCall{
		results: []transitionCallResult{
			{err: transientManifestReadOnlyError{err: errors.New("transient timeout")}},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()

	_, err := transitionIssueWithRetry(
		ctx,
		TransitionIssueInput{IssueID: "lit-test-4"},
		call.run,
		func(int) time.Duration { return 50 * time.Millisecond },
		waitWithContext,
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("transitionIssueWithRetry() error = %v, want context.DeadlineExceeded", err)
	}
	if call.calls != 1 {
		t.Fatalf("call.calls = %d, want 1", call.calls)
	}
}

func TestTransitionIssueRetryDelayIsBounded(t *testing.T) {
	for attempt := 1; attempt <= 10; attempt++ {
		delay := transitionIssueRetryDelay(attempt)
		if delay < transitionIssueRetryBaseDelay {
			t.Fatalf("delay(%d) = %v, want >= %v", attempt, delay, transitionIssueRetryBaseDelay)
		}
		max := transitionIssueRetryMaxDelay + transitionIssueRetryJitter
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
