package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmf/links-issue-tracker/internal/annotation"
	"github.com/bmf/links-issue-tracker/internal/app"
	"github.com/bmf/links-issue-tracker/internal/store"
	"github.com/bmf/links-issue-tracker/internal/workspace"
)

func newTestCLIApp(t *testing.T) *app.App {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("LIT_CONFIG_GLOBAL_PATH", "")
	t.Setenv("LIT_CONFIG_PROJECT_PATH", "")
	ctx := context.Background()
	workspaceRoot := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(workspaceRoot, "dolt"), "test-workspace-id")
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	return &app.App{
		Workspace: workspace.Info{
			RootDir: workspaceRoot,
		},
		Store: st,
	}
}

func TestRunReadyAnnotatesBlockedIssues(t *testing.T) {
	ctx := context.Background()
	ap := newTestCLIApp(t)

	openA, err := ap.Store.CreateIssue(ctx, store.CreateIssueInput{
		Title:     "Open issue A",
		IssueType: "task",
		Priority:  2,
		Assignee:  "alice",
	})
	if err != nil {
		t.Fatalf("CreateIssue(openA) error = %v", err)
	}
	openB, err := ap.Store.CreateIssue(ctx, store.CreateIssueInput{
		Title:     "Open issue B",
		IssueType: "bug",
		Priority:  1,
		Assignee:  "bob",
	})
	if err != nil {
		t.Fatalf("CreateIssue(openB) error = %v", err)
	}
	if _, err := ap.Store.AddRelation(ctx, store.AddRelationInput{
		SrcID:     openB.ID,
		DstID:     openA.ID,
		Type:      "blocks",
		CreatedBy: "agent",
	}); err != nil {
		t.Fatalf("AddRelation(blocks) error = %v", err)
	}

	closed, err := ap.Store.CreateIssue(ctx, store.CreateIssueInput{
		Title:     "Already done",
		IssueType: "task",
		Priority:  0,
	})
	if err != nil {
		t.Fatalf("CreateIssue(closed) error = %v", err)
	}
	if _, err := ap.Store.TransitionIssue(ctx, store.TransitionIssueInput{
		IssueID:   closed.ID,
		Action:    "close",
		Reason:    "not ready work",
		CreatedBy: "tester",
	}); err != nil {
		t.Fatalf("TransitionIssue(close) error = %v", err)
	}

	var stdout bytes.Buffer
	if err := runReady(ctx, &stdout, ap, []string{"--json"}); err != nil {
		t.Fatalf("runReady(--json) error = %v", err)
	}

	var got []annotation.AnnotatedIssue
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal(ready output) error = %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2; got=%#v", len(got), got)
	}
	// First issue should not be blocked (sorted by readiness: unblocked first)
	if isReadyBlocked(got[0].Annotations) {
		t.Fatalf("got[0] should not be blocked, annotations=%#v", got[0].Annotations)
	}
	if got[0].ID != openA.ID {
		t.Fatalf("got[0].ID = %q, want %q", got[0].ID, openA.ID)
	}
	// Second issue should be blocked
	if !isReadyBlocked(got[1].Annotations) {
		t.Fatalf("got[1] should be blocked, annotations=%#v", got[1].Annotations)
	}
	if got[1].ID != openB.ID {
		t.Fatalf("got[1].ID = %q, want %q", got[1].ID, openB.ID)
	}
	if got[1].Annotations[0].Kind.String() != "blocked_by" {
		t.Fatalf("got[1].Annotations[0].Kind = %q, want blocked_by", got[1].Annotations[0].Kind.String())
	}
	if got[1].Annotations[0].Message != openA.ID {
		t.Fatalf("got[1].Annotations[0].Message = %q, want %q", got[1].Annotations[0].Message, openA.ID)
	}
}

func TestRunReadySupportsAssigneeAndLimit(t *testing.T) {
	ctx := context.Background()
	ap := newTestCLIApp(t)

	if _, err := ap.Store.CreateIssue(ctx, store.CreateIssueInput{
		Title:     "Alice old",
		IssueType: "task",
		Priority:  1,
		Assignee:  "alice",
	}); err != nil {
		t.Fatalf("CreateIssue(alice old) error = %v", err)
	}
	if _, err := ap.Store.CreateIssue(ctx, store.CreateIssueInput{
		Title:     "Bob task",
		IssueType: "task",
		Priority:  0,
		Assignee:  "bob",
	}); err != nil {
		t.Fatalf("CreateIssue(bob) error = %v", err)
	}

	var stdout bytes.Buffer
	if err := runReady(ctx, &stdout, ap, []string{"--assignee", "alice", "--limit", "1", "--json"}); err != nil {
		t.Fatalf("runReady(--assignee --limit --json) error = %v", err)
	}

	var got []annotation.AnnotatedIssue
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal(ready output) error = %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1; got=%#v", len(got), got)
	}
	if got[0].Assignee != "alice" {
		t.Fatalf("got[0].Assignee = %q, want alice", got[0].Assignee)
	}
}

func TestRunReadyAnnotatesMissingRequiredField(t *testing.T) {
	ctx := context.Background()
	ap := newTestCLIApp(t)

	configDir := filepath.Join(ap.Workspace.RootDir, ".lit")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(configDir) error = %v", err)
	}
	configContent := "[ready]\nrequired_fields = [\"description\"]\n"
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(configContent), 0o644); err != nil {
		t.Fatalf("WriteFile(config.toml) error = %v", err)
	}

	issue, err := ap.Store.CreateIssue(ctx, store.CreateIssueInput{
		Title:     "Needs description",
		IssueType: "task",
		Priority:  1,
		Assignee:  "alice",
	})
	if err != nil {
		t.Fatalf("CreateIssue(issue) error = %v", err)
	}

	var stdout bytes.Buffer
	if err := runReady(ctx, &stdout, ap, []string{"--json"}); err != nil {
		t.Fatalf("runReady(--json) error = %v", err)
	}

	var got []annotation.AnnotatedIssue
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal(ready output) error = %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].ID != issue.ID {
		t.Fatalf("got[0].ID = %q, want %q", got[0].ID, issue.ID)
	}
	if !isReadyBlocked(got[0].Annotations) {
		t.Fatal("issue with missing required field should be blocked")
	}
	if len(got[0].Annotations) != 1 {
		t.Fatalf("len(got[0].Annotations) = %d, want 1", len(got[0].Annotations))
	}
	if got[0].Annotations[0].Kind.String() != "missing_field" {
		t.Fatalf("got[0].Annotations[0].Kind = %q, want missing_field", got[0].Annotations[0].Kind.String())
	}
	if got[0].Annotations[0].Message != "description" {
		t.Fatalf("got[0].Annotations[0].Message = %q, want description", got[0].Annotations[0].Message)
	}
}

func TestRunReadyErrorsOnInvalidRequiredField(t *testing.T) {
	ctx := context.Background()
	ap := newTestCLIApp(t)

	configDir := filepath.Join(ap.Workspace.RootDir, ".lit")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(configDir) error = %v", err)
	}
	configContent := "[ready]\nrequired_fields = [\"made_up_field\"]\n"
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(configContent), 0o644); err != nil {
		t.Fatalf("WriteFile(config.toml) error = %v", err)
	}

	var stdout bytes.Buffer
	err := runReady(ctx, &stdout, ap, []string{"--json"})
	if err == nil {
		t.Fatal("runReady expected error for invalid required field")
	}
	if !strings.Contains(err.Error(), "made_up_field") {
		t.Fatalf("error = %q, want mention of made_up_field", err.Error())
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("error = %q, want 'does not exist' context", err.Error())
	}
}

func TestRunReadyTextOutputIncludesNotReadySectionAndReason(t *testing.T) {
	ctx := context.Background()
	ap := newTestCLIApp(t)

	configDir := filepath.Join(ap.Workspace.RootDir, ".lit")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(configDir) error = %v", err)
	}
	configContent := "[ready]\nrequired_fields = [\"description\"]\n"
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(configContent), 0o644); err != nil {
		t.Fatalf("WriteFile(config.toml) error = %v", err)
	}

	if _, err := ap.Store.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "Ready ticket",
		IssueType:   "task",
		Priority:    1,
		Description: "ship it",
	}); err != nil {
		t.Fatalf("CreateIssue(ready) error = %v", err)
	}
	if _, err := ap.Store.CreateIssue(ctx, store.CreateIssueInput{
		Title:     "Missing description",
		IssueType: "task",
		Priority:  2,
	}); err != nil {
		t.Fatalf("CreateIssue(not ready) error = %v", err)
	}

	var stdout bytes.Buffer
	if err := runReady(ctx, &stdout, ap, nil); err != nil {
		t.Fatalf("runReady() error = %v", err)
	}
	text := stdout.String()
	if !strings.Contains(text, "Ready\n") {
		t.Fatalf("ready output missing Ready section header: %q", text)
	}
	if !strings.Contains(text, "\nNot Ready\n") {
		t.Fatalf("ready output missing Not Ready section header: %q", text)
	}
	if !strings.Contains(text, "Field description not set") {
		t.Fatalf("ready output missing not-ready reason: %q", text)
	}
}

func TestRunReadyReturnsConfigErrorForInvalidProjectConfig(t *testing.T) {
	ctx := context.Background()
	ap := newTestCLIApp(t)

	configDir := filepath.Join(ap.Workspace.RootDir, ".lit")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(configDir) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte("[ready\nrequired_fields = [\"description\"]"), 0o644); err != nil {
		t.Fatalf("WriteFile(config.toml) error = %v", err)
	}

	var stdout bytes.Buffer
	err := runReady(ctx, &stdout, ap, []string{"--json"})
	if err == nil {
		t.Fatal("runReady expected config parse error")
	}
	if !strings.Contains(err.Error(), "parse config") {
		t.Fatalf("runReady error = %q, want parse config context", err.Error())
	}
}
