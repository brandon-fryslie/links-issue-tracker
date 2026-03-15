package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bmf/links-issue-tracker/internal/store"
	"github.com/bmf/links-issue-tracker/internal/workspace"
)

func TestResolveAppBootstrapPolicy(t *testing.T) {
	testCases := []struct {
		name string
		args []string
		want appBootstrapPolicy
	}{
		{
			name: "ls is read only",
			args: []string{"ls"},
			want: appBootstrapPolicy{accessMode: appAccessRead, drainQueue: false},
		},
		{
			name: "dep ls is read only",
			args: []string{"dep", "ls", "lit-123"},
			want: appBootstrapPolicy{accessMode: appAccessRead, drainQueue: false},
		},
		{
			name: "backup create is read only",
			args: []string{"backup", "create"},
			want: appBootstrapPolicy{accessMode: appAccessRead, drainQueue: false},
		},
		{
			name: "fsck without repair is read only",
			args: []string{"fsck"},
			want: appBootstrapPolicy{accessMode: appAccessRead, drainQueue: false},
		},
		{
			name: "fsck repair is writable",
			args: []string{"fsck", "--repair"},
			want: appBootstrapPolicy{accessMode: appAccessWrite, drainQueue: true},
		},
		{
			name: "new is writable",
			args: []string{"new", "--title", "test"},
			want: appBootstrapPolicy{accessMode: appAccessWrite, drainQueue: true},
		},
	}

	for _, tc := range testCases {
		got := resolveAppBootstrapPolicy(tc.args)
		if got != tc.want {
			t.Fatalf("%s policy = %#v, want %#v", tc.name, got, tc.want)
		}
	}
}

func TestReadCommandSkipsMutationQueueDrain(t *testing.T) {
	repo, ws := initBootstrapTestRepo(t)
	writeQueuedCreateIssue(t, ws, "queued issue")

	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("Chdir(repo) error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prevWD) })

	var stdout bytes.Buffer
	if err := Run(context.Background(), &stdout, &stdout, []string{"ls", "--json"}); err != nil {
		t.Fatalf("Run(ls --json) error = %v", err)
	}

	var issues []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &issues); err != nil {
		t.Fatalf("json.Unmarshal(ls output) error = %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("ls issues = %#v, want queue to remain unapplied", issues)
	}

	if _, err := os.Stat(filepath.Join(ws.DatabasePath, ".links-mutation-queue.offset")); !os.IsNotExist(err) {
		t.Fatalf("queue offset stat error = %v, want not exist", err)
	}
}

func TestWriteCommandDrainsMutationQueueBeforeMutating(t *testing.T) {
	repo, ws := initBootstrapTestRepo(t)
	writeQueuedCreateIssue(t, ws, "queued issue")

	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("Chdir(repo) error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prevWD) })

	var newOut bytes.Buffer
	if err := Run(context.Background(), &newOut, &newOut, []string{"new", "--title", "direct issue", "--type", "task", "--priority", "2", "--json"}); err != nil {
		t.Fatalf("Run(new --json) error = %v", err)
	}

	var listOut bytes.Buffer
	if err := Run(context.Background(), &listOut, &listOut, []string{"ls", "--json"}); err != nil {
		t.Fatalf("Run(ls --json) error = %v", err)
	}

	var issues []struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(listOut.Bytes(), &issues); err != nil {
		t.Fatalf("json.Unmarshal(ls output) error = %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("len(issues) = %d, want 2; issues=%#v", len(issues), issues)
	}

	titles := map[string]bool{}
	for _, issue := range issues {
		titles[issue.Title] = true
	}
	if !titles["queued issue"] || !titles["direct issue"] {
		t.Fatalf("titles = %#v, want queued and direct issues", titles)
	}

	offsetPayload, err := os.ReadFile(filepath.Join(ws.DatabasePath, ".links-mutation-queue.offset"))
	if err != nil {
		t.Fatalf("ReadFile(queue offset) error = %v", err)
	}
	if string(bytes.TrimSpace(offsetPayload)) == "0" || len(bytes.TrimSpace(offsetPayload)) == 0 {
		t.Fatalf("queue offset payload = %q, want advanced offset", string(offsetPayload))
	}
}

func initBootstrapTestRepo(t *testing.T) (string, workspace.Info) {
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

	var stdout bytes.Buffer
	if err := Run(context.Background(), &stdout, &stdout, []string{"init", "--skip-hooks", "--skip-agents", "--json"}); err != nil {
		t.Fatalf("Run(init --skip-hooks --skip-agents --json) error = %v", err)
	}

	ws, err := workspace.Resolve(repo)
	if err != nil {
		t.Fatalf("workspace.Resolve() error = %v", err)
	}
	st, err := store.Open(context.Background(), ws.DatabasePath, ws.WorkspaceID)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("store.Close() error = %v", err)
	}
	return repo, ws
}

func writeQueuedCreateIssue(t *testing.T, ws workspace.Info, title string) {
	t.Helper()

	payload, err := json.Marshal(store.CreateIssueInput{
		Title:     title,
		IssueType: "task",
		Priority:  2,
	})
	if err != nil {
		t.Fatalf("json.Marshal(payload) error = %v", err)
	}
	entry, err := json.Marshal(map[string]any{
		"id":              "queued-create-" + title,
		"operation":       "create_issue",
		"payload":         json.RawMessage(payload),
		"enqueued_at":     time.Now().UTC(),
		"enqueued_by_pid": os.Getpid(),
	})
	if err != nil {
		t.Fatalf("json.Marshal(entry) error = %v", err)
	}

	queuePath := filepath.Join(ws.DatabasePath, ".links-mutation-queue.jsonl")
	if err := os.WriteFile(queuePath, append(entry, '\n'), 0o600); err != nil {
		t.Fatalf("WriteFile(queue) error = %v", err)
	}
	if err := os.Remove(filepath.Join(ws.DatabasePath, ".links-mutation-queue.offset")); err != nil && !os.IsNotExist(err) {
		t.Fatalf("Remove(queue offset) error = %v", err)
	}
}
