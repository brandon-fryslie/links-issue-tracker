package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveCreatesSharedConfigInGitCommonDir(t *testing.T) {
	repo := t.TempDir()
	run(t, repo, "git", "init")
	info, err := Resolve(repo)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if info.WorkspaceID == "" {
		t.Fatal("expected workspace ID")
	}
	if _, err := os.Stat(info.ConfigPath); err != nil {
		t.Fatalf("config file missing: %v", err)
	}
	if _, err := os.Stat(info.StorageDir); err != nil {
		t.Fatalf("storage dir missing: %v", err)
	}
	common := strings.TrimSpace(runOutput(t, repo, "git", "rev-parse", "--git-common-dir"))
	wantStorageDir, err := filepath.EvalSymlinks(filepath.Join(repo, common, "links"))
	if err != nil {
		t.Fatalf("EvalSymlinks(wantStorageDir) error = %v", err)
	}
	gotStorageDir, err := filepath.EvalSymlinks(info.StorageDir)
	if err != nil {
		t.Fatalf("EvalSymlinks(gotStorageDir) error = %v", err)
	}
	if gotStorageDir != wantStorageDir {
		t.Fatalf("storage dir = %q, want %q", info.StorageDir, wantStorageDir)
	}
	info2, err := Resolve(repo)
	if err != nil {
		t.Fatalf("Resolve second call error = %v", err)
	}
	if info2.WorkspaceID != info.WorkspaceID {
		t.Fatalf("workspace ID changed: %q != %q", info2.WorkspaceID, info.WorkspaceID)
	}
}

func TestResolveFailsOutsideGit(t *testing.T) {
	_, err := Resolve(t.TempDir())
	if err == nil {
		t.Fatal("expected error")
	}
	if err != ErrNotGitRepo {
		t.Fatalf("err = %v, want %v", err, ErrNotGitRepo)
	}
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, string(out))
	}
}

func runOutput(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, string(out))
	}
	return strings.TrimSpace(string(out))
}
