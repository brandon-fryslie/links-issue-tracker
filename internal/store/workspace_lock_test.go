package store

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestWorkspaceLockSharedHoldersCoexist pins the contract that multiple Stores
// open against the same workspace each take their own shared (LOCK_SH) hold and
// none of them block another. Without this invariant, two concurrent readers
// (e.g. agent A running lit ls while agent B runs lit show) would serialize on
// startup or, worse, the second would error with workspace-busy.
func TestWorkspaceLockSharedHoldersCoexist(t *testing.T) {
	ctx := context.Background()
	doltRoot := filepath.Join(t.TempDir(), "dolt")

	first, err := Open(ctx, doltRoot, "test-workspace-id")
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	defer first.Close()

	// Second Open against the same workspace must succeed without waiting on
	// the first's shared hold to release.
	done := make(chan error, 1)
	go func() {
		s, err := Open(ctx, doltRoot, "test-workspace-id")
		if err != nil {
			done <- err
			return
		}
		_ = s.Close()
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("second Open() error = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("second Open() did not complete within 10s — shared holds must coexist")
	}
}

// TestWorkspaceLockExclusiveRefusesWhileSharedHeld pins the headline acceptance
// criterion: while a Store holds a shared workspace lock, an attempt to take
// the exclusive hold (i.e. lit snapshots restore) refuses immediately with a
// "workspace busy" error — not a query error, not a silent corruption, not a
// hang.
//
// [LAW:single-enforcer] One exclusive holder at a time; this test pins that
// the refusal contract is owned by LockWorkspaceExclusive.
func TestWorkspaceLockExclusiveRefusesWhileSharedHeld(t *testing.T) {
	ctx := context.Background()
	doltRoot := filepath.Join(t.TempDir(), "dolt")

	st, err := Open(ctx, doltRoot, "test-workspace-id")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer st.Close()

	release, err := LockWorkspaceExclusive(ctx, doltRoot)
	if err == nil {
		_ = release()
		t.Fatal("LockWorkspaceExclusive succeeded while a Store held a shared hold; expected refusal")
	}
	if !strings.Contains(err.Error(), "workspace busy") {
		t.Fatalf("error %q must name the workspace-busy condition so the operator knows what to do", err.Error())
	}
}

// TestWorkspaceLockSharedRefusesWhileExclusiveHeld pins the reverse direction:
// while a holder has the exclusive lock (i.e. mid-restore), an attempt to open
// a Store must not succeed. The shared-side acquisition retries briefly and
// then refuses with a workspace-busy error naming the likely cause.
func TestWorkspaceLockSharedRefusesWhileExclusiveHeld(t *testing.T) {
	ctx := context.Background()
	doltRoot := filepath.Join(t.TempDir(), "dolt")

	// Bootstrap the database so Open's only failure mode here is the lock.
	bootstrap, err := Open(ctx, doltRoot, "test-workspace-id")
	if err != nil {
		t.Fatalf("bootstrap Open() error = %v", err)
	}
	if err := bootstrap.Close(); err != nil {
		t.Fatalf("bootstrap Close() error = %v", err)
	}

	release, err := LockWorkspaceExclusive(ctx, doltRoot)
	if err != nil {
		t.Fatalf("LockWorkspaceExclusive() error = %v", err)
	}
	defer release()

	// Constrain the shared-side wait so this test stays bounded.
	shortCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	_, err = acquireWorkspaceShared(shortCtx, doltRoot)
	if err == nil {
		t.Fatal("acquireWorkspaceShared succeeded while exclusive held; expected refusal or context deadline")
	}
	// Either of two errors is acceptable: the context deadline fires before
	// the retry budget, or the retry budget elapses and the shared helper
	// returns its workspace-busy message. Both are observable refusals.
	if !strings.Contains(err.Error(), "workspace busy") && !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("unexpected error from acquireWorkspaceShared: %v", err)
	}
}

// TestWorkspaceLockExclusiveReleasedAfterClose pins that Close releases the
// shared hold, so a subsequent restore can take the exclusive hold without
// any explicit quiesce step beyond closing the Store.
func TestWorkspaceLockExclusiveReleasedAfterClose(t *testing.T) {
	ctx := context.Background()
	doltRoot := filepath.Join(t.TempDir(), "dolt")

	st, err := Open(ctx, doltRoot, "test-workspace-id")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	release, err := LockWorkspaceExclusive(ctx, doltRoot)
	if err != nil {
		t.Fatalf("LockWorkspaceExclusive after Close error = %v", err)
	}
	if err := release(); err != nil {
		t.Fatalf("release exclusive error = %v", err)
	}
}

// TestOpenSyncHoldsWorkspaceLock pins the contract that lit sync's long-lived
// Store also acquires the shared workspace lock — the same way Open and
// OpenForRead do. Without this, `lit sync ...` could hold an open Dolt
// connection while `lit snapshots restore` rotates the directory.
//
// [LAW:single-enforcer] All Store constructors that open long-lived Dolt
// connections route through the same workspace-lock contract; OpenSync is no
// exception even though it serves a different higher-level purpose.
func TestOpenSyncHoldsWorkspaceLock(t *testing.T) {
	ctx := context.Background()
	doltRoot := filepath.Join(t.TempDir(), "dolt")

	st, err := OpenSync(ctx, doltRoot, "test-workspace-id")
	if err != nil {
		t.Fatalf("OpenSync() error = %v", err)
	}
	defer st.Close()

	// While the sync Store is open, exclusive acquire must refuse.
	release, err := LockWorkspaceExclusive(ctx, doltRoot)
	if err == nil {
		_ = release()
		t.Fatal("LockWorkspaceExclusive succeeded while OpenSync Store held shared; expected refusal")
	}
	if !strings.Contains(err.Error(), "workspace busy") {
		t.Fatalf("error %q must name workspace-busy", err.Error())
	}
}

// TestWorkspaceLockExclusiveSerializes pins that two concurrent exclusive
// acquisitions cannot both succeed — one wins, the other refuses immediately.
// Without this invariant, two concurrent lit snapshots restore commands could
// both rotate the database directory and lose data.
func TestWorkspaceLockExclusiveSerializes(t *testing.T) {
	ctx := context.Background()
	doltRoot := filepath.Join(t.TempDir(), "dolt")

	// Initialize the parent dir by opening and closing a Store once. The lock
	// file lives in the workspace storage dir, so its parent must exist.
	bootstrap, err := Open(ctx, doltRoot, "test-workspace-id")
	if err != nil {
		t.Fatalf("bootstrap Open() error = %v", err)
	}
	if err := bootstrap.Close(); err != nil {
		t.Fatalf("bootstrap Close() error = %v", err)
	}

	first, err := LockWorkspaceExclusive(ctx, doltRoot)
	if err != nil {
		t.Fatalf("first exclusive acquire error = %v", err)
	}
	defer first()

	var (
		secondErr error
		wg        sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		release, err := LockWorkspaceExclusive(ctx, doltRoot)
		if err == nil {
			_ = release()
			secondErr = nil
			return
		}
		secondErr = err
	}()
	wg.Wait()
	if secondErr == nil {
		t.Fatal("second exclusive acquire succeeded while first held; expected refusal")
	}
	if !strings.Contains(secondErr.Error(), "workspace busy") {
		t.Fatalf("second error %q must name workspace-busy", secondErr.Error())
	}
}
