package store

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sync/errgroup"
)

// TestConcurrentMutationsCreateIssues verifies that N goroutines performing
// mutations against the same Store all succeed without data corruption, and
// that the commit lock is not held after all operations complete.
//
// This exercises the processCommitMutex + file-based commit lock to ensure
// concurrent mutations are serialized correctly: each CreateIssue goes through
// withMutation, which acquires the commit lock, begins a tx, runs the mutation,
// commits the tx, runs commitWorkingSet (re-entrant), and releases the lock.
func TestConcurrentMutationsCreateIssues(t *testing.T) {
	ctx := context.Background()
	st := openIssueStore(t, ctx)

	const goroutines = 10
	var mu sync.Mutex
	createdIDs := make([]string, 0, goroutines)

	eg, egCtx := errgroup.WithContext(ctx)

	for i := range goroutines {
		i := i
		eg.Go(func() error {
			issue, err := st.CreateIssue(egCtx, CreateIssueInput{
				Title:     fmt.Sprintf("Concurrent issue %d", i),
				Topic:     "concurrent",
				IssueType: "task",
				Priority:  i % 5,
				Labels:    []string{"concurrent-test"},
			})
			if err != nil {
				return fmt.Errorf("goroutine %d: CreateIssue error: %w", i, err)
			}
			mu.Lock()
			createdIDs = append(createdIDs, issue.ID)
			mu.Unlock()
			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		t.Fatalf("concurrent mutations failed: %v", err)
	}

	// All goroutines must have produced a unique issue ID.
	if len(createdIDs) != goroutines {
		t.Fatalf("created %d issues, want %d", len(createdIDs), goroutines)
	}

	// Every issue must be readable with correct data.
	for _, id := range createdIDs {
		issue, err := st.GetIssue(ctx, id)
		if err != nil {
			t.Fatalf("GetIssue(%s) error = %v", id, err)
		}
		if !strings.HasPrefix(issue.ID, "test-concurrent-") {
			t.Fatalf("issue.ID = %q, want test-concurrent- prefix", issue.ID)
		}
		if issue.Topic != "concurrent" {
			t.Fatalf("issue.Topic = %q, want concurrent", issue.Topic)
		}
		if issue.IssueType != "task" {
			t.Fatalf("issue.IssueType = %q, want task", issue.IssueType)
		}
	}

	// List must return exactly goroutines issues with the concurrent-test label.
	all, err := st.ListIssues(ctx, ListIssuesFilter{
		LabelsAll: []string{"concurrent-test"},
	})
	if err != nil {
		t.Fatalf("ListIssues() error = %v", err)
	}
	if len(all) != goroutines {
		t.Fatalf("ListIssues() returned %d issues, want %d", len(all), goroutines)
	}

	// Lock must not be held.
	if _, err := os.Stat(st.commitLockPath); !os.IsNotExist(err) {
		t.Fatalf("lock file exists after concurrent mutations: stat err = %v", err)
	}
}

// TestConcurrentMutationsMixedOperations runs N goroutines performing different
// mutation types (create, update, comment, transition) against the same Store.
// After completion, verifies data integrity and lock release.
func TestConcurrentMutationsMixedOperations(t *testing.T) {
	ctx := context.Background()
	st := openIssueStore(t, ctx)

	// Pre-create issues for update/comment/transition goroutines.
	const preCreateCount = 10
	issues := make([]string, preCreateCount)
	for i := range issues {
		issue, err := st.CreateIssue(ctx, CreateIssueInput{
			Title:     fmt.Sprintf("Pre-create %d", i),
			Topic:     "mixed",
			IssueType: "task",
			Priority:  2,
			Labels:    []string{"mixed-test"},
		})
		if err != nil {
			t.Fatalf("pre-create issue %d error = %v", i, err)
		}
		issues[i] = issue.ID
	}

	eg, egCtx := errgroup.WithContext(ctx)

	// Goroutine batch 1: create new issues with the mixed-test label.
	const newCount = 5
	for i := range newCount {
		eg.Go(func() error {
			_, err := st.CreateIssue(egCtx, CreateIssueInput{
				Title:     fmt.Sprintf("New issue %d", i),
				Topic:     "mixed",
				IssueType: "task",
				Priority:  1,
				Labels:    []string{"mixed-test"},
			})
			return err
		})
	}

	// Goroutine batch 2: add comments to pre-created issues.
	for i, id := range issues[:5] {
		i, id := i, id
		eg.Go(func() error {
			_, err := st.AddComment(egCtx, AddCommentInput{
				IssueID:   id,
				Body:      fmt.Sprintf("Concurrent comment %d", i),
				CreatedBy: "concurrent-tester",
			})
			return err
		})
	}

	// Goroutine batch 3: update pre-created issues.
	for i, id := range issues[5:] {
		i, id := i, id
		eg.Go(func() error {
			newPriority := (i + 1) % 5
			_, err := st.UpdateIssue(egCtx, id, UpdateIssueInput{
				Priority: &newPriority,
			})
			return err
		})
	}

	// Goroutine batch 4: transition pre-created issues.
	for i, id := range issues[:3] {
		i, id := i, id
		eg.Go(func() error {
			action := "start"
			if i%2 == 0 {
				action = "close"
			}
			_, err := st.TransitionIssue(egCtx, TransitionIssueInput{
				IssueID:   id,
				Action:    action,
				Reason:    "concurrent test",
				CreatedBy: "concurrent-tester",
			})
			return err
		})
	}

	if err := eg.Wait(); err != nil {
		t.Fatalf("concurrent mixed mutations failed: %v", err)
	}

	// Verify all pre-created issues are still readable.
	for _, id := range issues {
		_, err := st.GetIssue(ctx, id)
		if err != nil {
			t.Fatalf("GetIssue(%s) after concurrent ops error = %v", id, err)
		}
	}

	// Verify the total count: preCreateCount + newCount, all carrying mixed-test label.
	all, err := st.ListIssues(ctx, ListIssuesFilter{
		LabelsAll: []string{"mixed-test"},
	})
	if err != nil {
		t.Fatalf("ListIssues() error = %v", err)
	}
	wantTotal := preCreateCount + newCount
	if len(all) != wantTotal {
		t.Fatalf("ListIssues() returned %d issues, want %d", len(all), wantTotal)
	}

	// Lock must not be held.
	if _, err := os.Stat(st.commitLockPath); !os.IsNotExist(err) {
		t.Fatalf("lock file exists after concurrent mixed ops: stat err = %v", err)
	}
}
