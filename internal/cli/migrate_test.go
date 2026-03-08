package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateBeadsDryRunDoesNotModifyFiles(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	hookPath := filepath.Join(repo, ".git", "hooks", "pre-push")
	if err := os.WriteFile(hookPath, []byte("#!/usr/bin/env bash\nbeads sync push\necho keep\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(pre-push) error = %v", err)
	}
	agentsPath := filepath.Join(repo, "AGENTS.md")
	if err := os.WriteFile(agentsPath, []byte("Use beads everywhere.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(AGENTS.md) error = %v", err)
	}

	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("Chdir(repo) error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prevWD) })

	var stdout bytes.Buffer
	if err := Run(context.Background(), &stdout, &stdout, []string{"migrate", "beads", "--json"}); err != nil {
		t.Fatalf("Run(migrate beads --json) error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if payload["mode"] != "dry-run" {
		t.Fatalf("mode = %v, want dry-run", payload["mode"])
	}

	hookContent, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("ReadFile(pre-push) error = %v", err)
	}
	if !strings.Contains(string(hookContent), "beads") {
		t.Fatalf("dry-run unexpectedly changed hook: %q", string(hookContent))
	}
	agentsContent, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatalf("ReadFile(AGENTS.md) error = %v", err)
	}
	if !strings.Contains(string(agentsContent), "beads") {
		t.Fatalf("dry-run unexpectedly changed AGENTS.md: %q", string(agentsContent))
	}
}

func TestMigrateBeadsApplyRewritesAgentsAndInstallsLitHook(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	hookPath := filepath.Join(repo, ".git", "hooks", "pre-push")
	if err := os.WriteFile(hookPath, []byte("#!/usr/bin/env bash\nbeads sync push\necho keep\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(pre-push) error = %v", err)
	}
	agentsPath := filepath.Join(repo, "AGENTS.md")
	if err := os.WriteFile(agentsPath, []byte("Use Beads and beads.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(AGENTS.md) error = %v", err)
	}

	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("Chdir(repo) error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prevWD) })

	var stdout bytes.Buffer
	if err := Run(context.Background(), &stdout, &stdout, []string{"migrate", "beads", "--apply", "--json"}); err != nil {
		t.Fatalf("Run(migrate beads --apply --json) error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if payload["mode"] != "apply" {
		t.Fatalf("mode = %v, want apply", payload["mode"])
	}
	if payload["lit_hook_installed"] != true {
		t.Fatalf("lit_hook_installed = %v, want true", payload["lit_hook_installed"])
	}

	newHookContent, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("ReadFile(pre-push) error = %v", err)
	}
	if !strings.Contains(string(newHookContent), linksPrePushHookMarker) {
		t.Fatalf("expected lit pre-push hook marker, got: %q", string(newHookContent))
	}
	if strings.Contains(strings.ToLower(string(newHookContent)), "beads") {
		t.Fatalf("new hook still contains beads: %q", string(newHookContent))
	}
	agentsContent, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatalf("ReadFile(AGENTS.md) error = %v", err)
	}
	if strings.Contains(strings.ToLower(string(agentsContent)), "beads") {
		t.Fatalf("AGENTS.md still contains beads: %q", string(agentsContent))
	}
	if !strings.Contains(string(agentsContent), "Lit") && !strings.Contains(string(agentsContent), "lit") {
		t.Fatalf("AGENTS.md missing lit replacement: %q", string(agentsContent))
	}
}
