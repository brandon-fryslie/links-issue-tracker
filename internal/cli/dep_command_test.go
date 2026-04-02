package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmf/links-issue-tracker/internal/model"
)

func TestDepAddBlocksUsesBlockerBlockedOrder(t *testing.T) {
	setupDepCommandTestRepo(t)

	blockerID := mustCreateIssue(t, "grooming gate")
	blockedID := mustCreateIssue(t, "deliver task")

	addOutput := mustRunLitText(t, []string{"dep", "add", blockerID, blockedID, "--type", "blocks"})
	wantLine := blockerID + " --blocks--> " + blockedID
	if strings.TrimSpace(addOutput) != wantLine {
		t.Fatalf("dep add output = %q, want %q", strings.TrimSpace(addOutput), wantLine)
	}

	blocker := mustShowIssueDetail(t, blockerID)
	if len(blocker.DependsOn) != 0 {
		t.Fatalf("blocker depends_on = %#v, want none", blocker.DependsOn)
	}
	if len(blocker.Blocks) != 1 || blocker.Blocks[0].ID != blockedID {
		t.Fatalf("blocker blocks = %#v, want [%s]", blocker.Blocks, blockedID)
	}

	blocked := mustShowIssueDetail(t, blockedID)
	if len(blocked.Blocks) != 0 {
		t.Fatalf("blocked blocks = %#v, want none", blocked.Blocks)
	}
	if len(blocked.DependsOn) != 1 || blocked.DependsOn[0].ID != blockerID {
		t.Fatalf("blocked depends_on = %#v, want [%s]", blocked.DependsOn, blockerID)
	}

	lsOutput := mustRunLitText(t, []string{"dep", "ls", blockerID, "--type", "blocks"})
	if strings.TrimSpace(lsOutput) != wantLine {
		t.Fatalf("dep ls output = %q, want %q", strings.TrimSpace(lsOutput), wantLine)
	}
}

func TestDepRmBlocksUsesBlockerBlockedOrder(t *testing.T) {
	setupDepCommandTestRepo(t)

	blockerID := mustCreateIssue(t, "grooming gate")
	blockedID := mustCreateIssue(t, "deliver task")
	mustRunLitText(t, []string{"dep", "add", blockerID, blockedID, "--type", "blocks"})

	rmOutput := mustRunLitText(t, []string{"dep", "rm", blockerID, blockedID, "--type", "blocks"})
	if strings.TrimSpace(rmOutput) != "ok" {
		t.Fatalf("dep rm output = %q, want ok", strings.TrimSpace(rmOutput))
	}

	blocker := mustShowIssueDetail(t, blockerID)
	if len(blocker.Blocks) != 0 || len(blocker.DependsOn) != 0 {
		t.Fatalf("blocker relations after rm = blocks:%#v depends_on:%#v, want none", blocker.Blocks, blocker.DependsOn)
	}
	blocked := mustShowIssueDetail(t, blockedID)
	if len(blocked.Blocks) != 0 || len(blocked.DependsOn) != 0 {
		t.Fatalf("blocked relations after rm = blocks:%#v depends_on:%#v, want none", blocked.Blocks, blocked.DependsOn)
	}

	lsOutput := mustRunLitText(t, []string{"dep", "ls", blockerID, "--type", "blocks"})
	if strings.TrimSpace(lsOutput) != "" {
		t.Fatalf("dep ls output after rm = %q, want empty", strings.TrimSpace(lsOutput))
	}
}

func setupDepCommandTestRepo(t *testing.T) {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init")

	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("Chdir(repo) error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prevWD) })

	t.Setenv("HOME", repo)
	t.Setenv("CODEX_HOME", filepath.Join(repo, ".codex-home"))

	if _, err := runLitCommand([]string{"init", "--skip-hooks", "--skip-agents", "--json"}); err != nil {
		t.Fatalf("Run(init --skip-hooks --skip-agents --json) error = %v", err)
	}
}

func mustCreateIssue(t *testing.T, title string) string {
	t.Helper()
	out, err := runLitCommand([]string{"new", "--title", title, "--topic", "deps", "--type", "task", "--json"})
	if err != nil {
		t.Fatalf("Run(new --json) error = %v", err)
	}
	var issue model.Issue
	if err := json.Unmarshal([]byte(out), &issue); err != nil {
		t.Fatalf("new output is not valid issue json: %v", err)
	}
	if strings.TrimSpace(issue.ID) == "" {
		t.Fatalf("new output missing issue id: %s", out)
	}
	return issue.ID
}

func mustShowIssueDetail(t *testing.T, issueID string) model.IssueDetail {
	t.Helper()
	out, err := runLitCommand([]string{"show", issueID, "--json"})
	if err != nil {
		t.Fatalf("Run(show --json) error = %v", err)
	}
	var detail model.IssueDetail
	if err := json.Unmarshal([]byte(out), &detail); err != nil {
		t.Fatalf("show output is not valid detail json: %v", err)
	}
	return detail
}

func mustRunLitText(t *testing.T, args []string) string {
	t.Helper()
	out, err := runLitCommand(args)
	if err != nil {
		t.Fatalf("Run(%v) error = %v", args, err)
	}
	return out
}

func runLitCommand(args []string) (string, error) {
	var stdout bytes.Buffer
	err := Run(context.Background(), &stdout, &stdout, args)
	return stdout.String(), err
}
