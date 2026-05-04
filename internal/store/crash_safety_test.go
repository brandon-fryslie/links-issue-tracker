package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestPanicDuringMutationReleasesLock verifies that withMutation's deferred
// rollback and lock release fire even when the mutation function panics.
// Without defer, a panic would leave the lock file on disk, blocking all
// future mutations.
func TestPanicDuringMutationReleasesLock(t *testing.T) {
	st := openIssueStore(t, context.Background())
	lockPath := st.commitLockPath

	// withMutation panics inside the mutation fn.
	func() {
		defer func() {
			_ = recover()
		}()
		_ = st.withMutation(context.Background(), "panic-test", func(ctx context.Context, tx *sql.Tx) error {
			panic("simulated mutation panic")
		})
	}()

	// Lock file must not exist after the panic is recovered.
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("lock file still exists after panic: stat err = %v", err)
	}

	// A subsequent mutation must succeed, proving the lock was released.
	_, err := st.CreateIssue(context.Background(), CreateIssueInput{
		Title:     "Post-panic issue",
		Topic:     "crash",
		IssueType: "task",
		Priority:  2,
	})
	if err != nil {
		t.Fatalf("CreateIssue after panic error = %v", err)
	}
}

// TestPanicDuringWithCommitLockReleasesLock verifies that withCommitLock's
// defer release() fires even when the enclosed operation panics.
func TestPanicDuringWithCommitLockReleasesLock(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), ".links-commit.lock")
	s := &Store{commitLockPath: lockPath}

	func() {
		defer func() {
			_ = recover()
		}()
		_ = s.withCommitLock(context.Background(), func(ctx context.Context) error {
			panic("simulated operation panic")
		})
	}()

	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("lock file still exists after withCommitLock panic: stat err = %v", err)
	}
}

// TestStaleLockFromDeadPIDIsReclaimed creates a lock file containing a PID
// that does not correspond to a running process and verifies that
// acquireCommitLock reclaims it.
func TestStaleLockFromDeadPIDIsReclaimed(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), ".links-commit.lock")
	// PID 999999 is extremely unlikely to be running.
	if err := os.WriteFile(lockPath, []byte("999999\n"), 0o600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	s := &Store{commitLockPath: lockPath}

	// Override PID probe so the test is deterministic regardless of platform.
	originalProbe := commitLockPIDRunning
	commitLockPIDRunning = func(pid int) (bool, error) {
		return false, nil
	}
	t.Cleanup(func() { commitLockPIDRunning = originalProbe })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	lockedCtx, release, err := s.acquireCommitLock(ctx)
	if err != nil {
		t.Fatalf("acquireCommitLock() error = %v", err)
	}
	if lockedCtx.Value(commitLockContextKey{}) != true {
		t.Fatalf("context does not carry commit lock marker")
	}
	release()

	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("lock file still exists after release: stat err = %v", err)
	}
}

// TestStaleLockFromAgeIsReclaimed creates a lock file with an old mtime and
// verifies that removeStaleCommitLock removes it even when the owner PID is
// unreadable (malformed content).
func TestStaleLockFromAgeIsReclaimed(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), ".links-commit.lock")
	if err := os.WriteFile(lockPath, []byte("garbage\n"), 0o600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	// Make the lock older than the stale threshold.
	staleTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(lockPath, staleTime, staleTime); err != nil {
		t.Fatalf("Chtimes error = %v", err)
	}

	if err := removeStaleCommitLock(lockPath, time.Minute); err != nil {
		t.Fatalf("removeStaleCommitLock() error = %v", err)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("stale lock file was not removed: stat err = %v", err)
	}
}

// TestCommitWorkingSetRetryAfterTxCommit verifies that withMutation calls
// commitWorkingSet (which re-enters withCommitLock) after the transaction is
// committed. This exercises the re-entrant path where the context already
// carries the lock marker.
func TestCommitWorkingSetRetryAfterTxCommit(t *testing.T) {
	st := openIssueStore(t, context.Background())

	// CreateIssue goes through withMutation, which:
	// 1. acquires commit lock
	// 2. begins tx, runs fn, commits tx
	// 3. calls commitWorkingSet (which re-enters withCommitLock — short-circuits)
	// If any step fails, CreateIssue returns an error.
	issue, err := st.CreateIssue(context.Background(), CreateIssueInput{
		Title:     "Commit path exercise",
		Topic:     "crash",
		IssueType: "task",
		Priority:  1,
	})
	if err != nil {
		t.Fatalf("CreateIssue() error = %v", err)
	}
	if issue.ID == "" {
		t.Fatal("CreateIssue() returned empty ID")
	}

	// Verify the issue is readable.
	got, err := st.GetIssue(context.Background(), issue.ID)
	if err != nil {
		t.Fatalf("GetIssue() error = %v", err)
	}
	if got.Title != issue.Title {
		t.Fatalf("GetIssue() title = %q, want %q", got.Title, issue.Title)
	}

	// Lock must not be held.
	if _, err := os.Stat(st.commitLockPath); !os.IsNotExist(err) {
		t.Fatalf("lock file exists after CreateIssue: stat err = %v", err)
	}
}

// TestLockFileContainsCurrentPID verifies that tryAcquireFileLock writes the
// current process PID to the lock file.
func TestLockFileContainsCurrentPID(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), ".links-commit.lock")

	locked, err := tryAcquireFileLock(lockPath)
	if err != nil {
		t.Fatalf("tryAcquireFileLock() error = %v", err)
	}
	if !locked {
		t.Fatal("tryAcquireFileLock() returned locked=false")
	}

	content, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("ReadFile(lock) error = %v", err)
	}
	expected := fmt.Sprintf("%d\n", os.Getpid())
	if string(content) != expected {
		t.Fatalf("lock content = %q, want %q", string(content), expected)
	}
	_ = os.Remove(lockPath)
}

// TestTryAcquireFileLockFailsIfExists verifies atomicity: a second
// tryAcquireFileLock on the same path fails with os.ErrExist.
func TestTryAcquireFileLockFailsIfExists(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), ".links-commit.lock")

	locked, err := tryAcquireFileLock(lockPath)
	if err != nil {
		t.Fatalf("first tryAcquireFileLock() error = %v", err)
	}
	if !locked {
		t.Fatal("first tryAcquireFileLock() returned locked=false")
	}

	locked2, err2 := tryAcquireFileLock(lockPath)
	if err2 == nil {
		t.Fatal("second tryAcquireFileLock() error = nil, want os.ErrExist")
	}
	if !errors.Is(err2, os.ErrExist) {
		t.Fatalf("second tryAcquireFileLock() error = %v, want os.ErrExist", err2)
	}
	if locked2 {
		t.Fatal("second tryAcquireFileLock() returned locked=true")
	}
	_ = os.Remove(lockPath)
}

// TestReentrantWithCommitLockShortCircuits verifies that calling withCommitLock
// from within a held lock is a no-op acquisition (the context already carries
// the lock marker and the release is a no-op).
func TestReentrantWithCommitLockShortCircuits(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), ".links-commit.lock")
	s := &Store{commitLockPath: lockPath}

	err := s.withCommitLock(context.Background(), func(ctx context.Context) error {
		// Nested call should short-circuit: no deadlock, no second lock file.
		return s.withCommitLock(ctx, func(ctx context.Context) error {
			// Verify the context still carries the marker.
			if ctx.Value(commitLockContextKey{}) != true {
				return errors.New("nested context missing commit lock marker")
			}
			return nil
		})
	})
	if err != nil {
		t.Fatalf("nested withCommitLock() error = %v", err)
	}

	// Lock file must be cleaned up.
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("lock file still exists after re-entrant withCommitLock: stat err = %v", err)
	}
}

// TestAcquireCommitLockContextCancellation verifies that a cancelled context
// prevents lock acquisition rather than blocking indefinitely.
func TestAcquireCommitLockContextCancellation(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), ".links-commit.lock")
	s := &Store{commitLockPath: lockPath}

	// Hold the lock externally with current PID (live owner).
	if err := os.WriteFile(lockPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	// Cancel the context immediately.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := s.acquireCommitLock(ctx)
	if err == nil {
		t.Fatal("acquireCommitLock() with cancelled context succeeded, want error")
	}
}

