package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/bmf/links-issue-tracker/internal/model"
	"github.com/bmf/links-issue-tracker/internal/store"
)

type transitionCallResult struct {
	issue model.Issue
	err   error
}

type fakeTransitionIssueService struct {
	results []transitionCallResult
	calls   int
}

func (f *fakeTransitionIssueService) TransitionIssue(_ context.Context, _ store.TransitionIssueInput) (model.Issue, error) {
	f.calls++
	if len(f.results) == 0 {
		return model.Issue{}, errors.New("unexpected call")
	}
	current := f.results[0]
	f.results = f.results[1:]
	return current.issue, current.err
}

func TestTransitionIssueWithRetryRetriesTransientError(t *testing.T) {
	service := &fakeTransitionIssueService{
		results: []transitionCallResult{
			{err: errors.New("dolt commit working set: Error 1105: cannot update manifest: database is read only")},
			{issue: model.Issue{ID: "lit-test-1"}},
		},
	}

	issue, err := transitionIssueWithRetry(context.Background(), service, store.TransitionIssueInput{IssueID: "lit-test-1"})
	if err != nil {
		t.Fatalf("transitionIssueWithRetry() error = %v", err)
	}
	if issue.ID != "lit-test-1" {
		t.Fatalf("issue.ID = %q, want lit-test-1", issue.ID)
	}
	if service.calls != 2 {
		t.Fatalf("service.calls = %d, want 2", service.calls)
	}
}

func TestTransitionIssueWithRetryDoesNotRetryNonTransientError(t *testing.T) {
	service := &fakeTransitionIssueService{
		results: []transitionCallResult{
			{err: errors.New("some other storage failure")},
			{issue: model.Issue{ID: "lit-test-2"}},
		},
	}

	_, err := transitionIssueWithRetry(context.Background(), service, store.TransitionIssueInput{IssueID: "lit-test-2"})
	if err == nil {
		t.Fatal("transitionIssueWithRetry() error = nil, want non-nil")
	}
	if service.calls != 1 {
		t.Fatalf("service.calls = %d, want 1", service.calls)
	}
}

func TestTransitionIssueWithRetryHonorsContextCancellation(t *testing.T) {
	service := &fakeTransitionIssueService{
		results: []transitionCallResult{
			{err: errors.New("Error 1105: cannot update manifest: database is read only")},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := transitionIssueWithRetry(ctx, service, store.TransitionIssueInput{IssueID: "lit-test-3"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("transitionIssueWithRetry() error = %v, want context.Canceled", err)
	}
	if service.calls != 1 {
		t.Fatalf("service.calls = %d, want 1", service.calls)
	}
}

func TestIsTransientManifestReadOnlyError(t *testing.T) {
	if isTransientManifestReadOnlyError(nil) {
		t.Fatal("isTransientManifestReadOnlyError(nil) = true, want false")
	}
	if !isTransientManifestReadOnlyError(errors.New("Error 1105: cannot update manifest: database is read only")) {
		t.Fatal("expected transient manifest read-only error to match")
	}
	if !isTransientManifestReadOnlyError(errors.New("cannot update manifest while set is read only")) {
		t.Fatal("expected substring-based manifest read-only error to match")
	}
	if isTransientManifestReadOnlyError(errors.New("permission denied")) {
		t.Fatal("unexpected match for non-transient error")
	}
}
