package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmf/links-issue-tracker/internal/doltcli"
)

func TestOpenSyncDoesNotCreateStartupCommitWhenSchemaIsCurrent(t *testing.T) {
	ctx := context.Background()
	doltRoot := filepath.Join(t.TempDir(), "dolt")

	st, err := Open(ctx, doltRoot, "test-workspace-id")
	if err != nil {
		t.Fatalf("Open() initial error = %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close() initial error = %v", err)
	}

	repoPath := filepath.Join(doltRoot, "links")
	beforeLog, err := doltcli.Run(ctx, repoPath, "log", "--oneline")
	if err != nil {
		t.Fatalf("dolt log before sync open error = %v", err)
	}

	syncStore, err := OpenSync(ctx, doltRoot, "test-workspace-id")
	if err != nil {
		t.Fatalf("OpenSync() error = %v", err)
	}
	if err := syncStore.Close(); err != nil {
		t.Fatalf("Close() sync error = %v", err)
	}

	afterLog, err := doltcli.Run(ctx, repoPath, "log", "--oneline")
	if err != nil {
		t.Fatalf("dolt log after sync open error = %v", err)
	}

	if countNonEmptyLines(afterLog) != countNonEmptyLines(beforeLog) {
		t.Fatalf("OpenSync() created extra commit:\nbefore:\n%s\nafter:\n%s", beforeLog, afterLog)
	}
}

func TestSyncRemoteLifecycle(t *testing.T) {
	ctx := context.Background()
	doltRoot := filepath.Join(t.TempDir(), "dolt")

	st, err := Open(ctx, doltRoot, "test-workspace-id")
	if err != nil {
		t.Fatalf("Open() initial error = %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close() initial error = %v", err)
	}

	syncStore, err := OpenSync(ctx, doltRoot, "test-workspace-id")
	if err != nil {
		t.Fatalf("OpenSync() error = %v", err)
	}
	defer syncStore.Close()

	if err := syncStore.SyncAddRemote(ctx, "origin", "https://example.com/repo.git"); err != nil {
		t.Fatalf("SyncAddRemote() error = %v", err)
	}

	remotes, err := syncStore.SyncListRemotes(ctx)
	if err != nil {
		t.Fatalf("SyncListRemotes() after add error = %v", err)
	}
	if len(remotes) != 1 || remotes[0].Name != "origin" {
		t.Fatalf("remotes after add = %#v", remotes)
	}
	if remotes[0].URL == "" {
		t.Fatalf("remotes after add missing URL: %#v", remotes)
	}

	if err := syncStore.SyncRemoveRemote(ctx, "origin"); err != nil {
		t.Fatalf("SyncRemoveRemote() error = %v", err)
	}

	remotes, err = syncStore.SyncListRemotes(ctx)
	if err != nil {
		t.Fatalf("SyncListRemotes() after remove error = %v", err)
	}
	if len(remotes) != 0 {
		t.Fatalf("remotes after remove = %#v, want empty", remotes)
	}
}

func TestValidateEmbeddedSyncSupportAcceptsRequiredVersions(t *testing.T) {
	err := validateEmbeddedSyncSupport(map[string]string{
		"github.com/dolthub/dolt/go": minEmbeddedDoltVersion,
		"github.com/dolthub/driver":  minEmbeddedDriverVersion,
	})
	if err != nil {
		t.Fatalf("validateEmbeddedSyncSupport() error = %v", err)
	}
}

func TestValidateEmbeddedSyncSupportRejectsOlderVersions(t *testing.T) {
	err := validateEmbeddedSyncSupport(map[string]string{
		"github.com/dolthub/dolt/go": "v0.40.5-0.20240702155756-bcf4dd5f5cc1",
		"github.com/dolthub/driver":  "v0.2.0",
	})
	if err == nil {
		t.Fatal("validateEmbeddedSyncSupport() error = nil, want version failure")
	}
	if !strings.Contains(err.Error(), "embedded sync requires") {
		t.Fatalf("validateEmbeddedSyncSupport() error = %v, want embedded sync guidance", err)
	}
}
