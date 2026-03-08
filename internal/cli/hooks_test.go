package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmf/links-issue-tracker/internal/workspace"
)

func TestHooksInstallWritesPrePushHook(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	ws, err := workspace.Resolve(repo)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	var stdout bytes.Buffer
	if err := runHooks(&stdout, ws, []string{"install"}); err != nil {
		t.Fatalf("runHooks(install) error = %v", err)
	}

	hookPath := filepath.Join(ws.GitCommonDir, "hooks", "pre-push")
	content, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("ReadFile(pre-push) error = %v", err)
	}
	text := string(content)
	if !strings.Contains(text, linksPrePushHookMarker) {
		t.Fatalf("hook missing marker: %q", text)
	}
	if !strings.Contains(text, "warning: db sync failed") {
		t.Fatalf("hook missing warning output: %q", text)
	}
	if !strings.Contains(text, "exit 0") {
		t.Fatalf("hook must never block push: %q", text)
	}
}

func TestHooksInstallPreservesExistingPrePushHook(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	ws, err := workspace.Resolve(repo)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	hooksDir := filepath.Join(ws.GitCommonDir, "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(hooks) error = %v", err)
	}
	original := "#!/usr/bin/env bash\necho custom-pre-push\n"
	originalPath := filepath.Join(hooksDir, "pre-push")
	if err := os.WriteFile(originalPath, []byte(original), 0o755); err != nil {
		t.Fatalf("WriteFile(pre-push) error = %v", err)
	}

	if err := runHooksInstall(new(bytes.Buffer), ws, nil); err != nil {
		t.Fatalf("runHooksInstall() error = %v", err)
	}

	legacyPath := filepath.Join(hooksDir, "pre-push.links.user")
	legacy, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatalf("ReadFile(pre-push.links.user) error = %v", err)
	}
	if string(legacy) != original {
		t.Fatalf("legacy hook mismatch: got %q want %q", string(legacy), original)
	}

	newHook, err := os.ReadFile(originalPath)
	if err != nil {
		t.Fatalf("ReadFile(new pre-push) error = %v", err)
	}
	if !strings.Contains(string(newHook), `"${legacy_hook}" "$@"`) {
		t.Fatalf("new hook does not chain legacy hook: %q", string(newHook))
	}
}

func TestRunHooksViaCLI(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("Chdir(repo) error = %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prevWD)
	})

	var stdout bytes.Buffer
	if err := Run(context.Background(), &stdout, &stdout, []string{"hooks", "install", "--json"}); err != nil {
		t.Fatalf("Run(hooks install --json) error = %v", err)
	}
	if !strings.Contains(stdout.String(), `"status": "installed"`) {
		t.Fatalf("unexpected hooks install output: %q", stdout.String())
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}
