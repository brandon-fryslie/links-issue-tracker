package beads

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/bmf/links-issue-tracker/internal/store"
)

func TestImportFromBeadsSQLite(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "links.db"), "workspace-test")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer st.Close()

	beadsDBPath := filepath.Join(t.TempDir(), "beads.db")
	beadsDB, err := sql.Open(driverName, beadsDBPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer beadsDB.Close()

	for _, stmt := range []string{
		`CREATE TABLE issues (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'open',
			priority INTEGER NOT NULL DEFAULT 2,
			issue_type TEXT NOT NULL DEFAULT 'task',
			assignee TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			closed_at DATETIME,
			deleted_at DATETIME
		);`,
		`CREATE TABLE dependencies (
			issue_id TEXT NOT NULL,
			depends_on_id TEXT NOT NULL,
			type TEXT NOT NULL DEFAULT 'blocks',
			created_at TIMESTAMP NOT NULL,
			created_by TEXT NOT NULL
		);`,
		`CREATE TABLE comments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			issue_id TEXT NOT NULL,
			author TEXT NOT NULL,
			text TEXT NOT NULL,
			created_at DATETIME NOT NULL
		);`,
	} {
		if _, err := beadsDB.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("ExecContext(%q) error = %v", stmt, err)
		}
	}

	for _, stmt := range []string{
		`INSERT INTO issues(id, title, description, status, priority, issue_type, assignee, created_at, updated_at, closed_at, deleted_at)
		 VALUES ('issue-epic', 'Renderer cleanup', 'epic desc', 'open', 1, 'epic', 'bmf', '2026-03-07T00:00:00Z', '2026-03-07T01:00:00Z', NULL, NULL)`,
		`INSERT INTO issues(id, title, description, status, priority, issue_type, assignee, created_at, updated_at, closed_at, deleted_at)
		 VALUES ('issue-task', 'Move pass validation', 'task desc', 'closed', 2, 'task', 'e-prawn', '2026-03-07T02:00:00Z', '2026-03-07T03:00:00Z', '2026-03-07T04:00:00Z', NULL)`,
		`INSERT INTO dependencies(issue_id, depends_on_id, type, created_at, created_by)
		 VALUES ('issue-task', 'issue-epic', 'parent-child', '2026-03-07T03:30:00Z', 'bmf')`,
		`INSERT INTO comments(issue_id, author, text, created_at)
		 VALUES ('issue-task', 'bmf', 'Need compiler contract first.', '2026-03-07T03:45:00Z')`,
	} {
		if _, err := beadsDB.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed beads db error = %v", err)
		}
	}

	summary, err := Import(ctx, st, beadsDBPath)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if summary.Issues != 2 || summary.Relations != 1 || summary.Comments != 1 {
		t.Fatalf("summary = %#v", summary)
	}

	detail, err := st.GetIssueDetail(ctx, "issue-task")
	if err != nil {
		t.Fatalf("GetIssueDetail() error = %v", err)
	}
	if detail.Parent == nil || detail.Parent.ID != "issue-epic" {
		t.Fatalf("detail.Parent = %#v", detail.Parent)
	}
	if len(detail.Comments) != 1 || detail.Comments[0].Body != "Need compiler contract first." {
		t.Fatalf("detail.Comments = %#v", detail.Comments)
	}
	if detail.Issue.Status != "closed" || detail.Issue.ClosedAt == nil {
		t.Fatalf("detail.Issue = %#v", detail.Issue)
	}
}

func TestExportToBeadsSQLite(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "links.db"), "workspace-test")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer st.Close()

	epic, err := st.CreateIssue(ctx, store.CreateIssueInput{Title: "Renderer cleanup", IssueType: "epic", Priority: 1, Assignee: "bmf"})
	if err != nil {
		t.Fatalf("CreateIssue epic error = %v", err)
	}
	task, err := st.CreateIssue(ctx, store.CreateIssueInput{Title: "Move pass validation", IssueType: "task", Priority: 2})
	if err != nil {
		t.Fatalf("CreateIssue task error = %v", err)
	}
	if _, err := st.AddRelation(ctx, store.AddRelationInput{SrcID: task.ID, DstID: epic.ID, Type: "parent-child", CreatedBy: "bmf"}); err != nil {
		t.Fatalf("AddRelation() error = %v", err)
	}
	if _, err := st.AddComment(ctx, store.AddCommentInput{IssueID: task.ID, Body: "Need compiler contract first.", CreatedBy: "bmf"}); err != nil {
		t.Fatalf("AddComment() error = %v", err)
	}

	beadsDBPath := filepath.Join(t.TempDir(), "exported-beads.db")
	summary, err := Export(ctx, st, beadsDBPath)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if summary.Issues != 2 || summary.Relations != 1 || summary.Comments != 1 {
		t.Fatalf("summary = %#v", summary)
	}

	beadsDB, err := sql.Open(driverName, beadsDBPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer beadsDB.Close()

	var issueCount, relationCount, commentCount int
	if err := beadsDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM issues`).Scan(&issueCount); err != nil {
		t.Fatalf("count issues error = %v", err)
	}
	if err := beadsDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM dependencies`).Scan(&relationCount); err != nil {
		t.Fatalf("count dependencies error = %v", err)
	}
	if err := beadsDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM comments`).Scan(&commentCount); err != nil {
		t.Fatalf("count comments error = %v", err)
	}
	if issueCount != 2 || relationCount != 1 || commentCount != 1 {
		t.Fatalf("counts = issues:%d relations:%d comments:%d", issueCount, relationCount, commentCount)
	}

	var exportedType, exportedTitle string
	if err := beadsDB.QueryRowContext(ctx, `SELECT issue_type, title FROM issues WHERE id = ?`, epic.ID).Scan(&exportedType, &exportedTitle); err != nil {
		t.Fatalf("read exported issue error = %v", err)
	}
	if exportedType != "epic" || exportedTitle != epic.Title {
		t.Fatalf("exported issue = type:%q title:%q", exportedType, exportedTitle)
	}
}
