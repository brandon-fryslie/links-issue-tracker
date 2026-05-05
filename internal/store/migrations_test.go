package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmf/links-issue-tracker/internal/doltcli"
)

// withActiveMigrationsForTest swaps the live migration set for the duration of
// a test. Restores the production set on cleanup.
func withActiveMigrationsForTest(t *testing.T, override []migration) {
	t.Helper()
	prev := activeMigrations
	activeMigrations = func() []migration { return override }
	t.Cleanup(func() { activeMigrations = prev })
}

func countMigrationLogRows(t *testing.T, ctx context.Context, s *Store, version int) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM migration_log WHERE version = ?`, version).Scan(&n); err != nil {
		t.Fatalf("count migration_log v=%d: %v", version, err)
	}
	return n
}

func readSchemaVersionForTest(t *testing.T, ctx context.Context, s *Store) string {
	t.Helper()
	v, err := s.getMeta(ctx, nil, "schema_version")
	if err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	return v
}

// regressSchemaVersionToBaseline rolls schema_version back to "1" and persists
// the change to a Dolt commit so a subsequent Open observes a pending v2.
func regressSchemaVersionToBaseline(t *testing.T, ctx context.Context, s *Store) {
	t.Helper()
	if err := s.ExecRawForTest(ctx, `UPDATE meta SET meta_value = '1' WHERE meta_key = 'schema_version'`); err != nil {
		t.Fatalf("regress schema_version: %v", err)
	}
	if err := s.commitWorkingSet(ctx, "test: regress schema_version to baseline"); err != nil {
		t.Fatalf("commit regression: %v", err)
	}
}

func TestVersionedMigrationsRunOnExistingDB(t *testing.T) {
	ctx := context.Background()
	doltRoot := filepath.Join(t.TempDir(), "dolt")
	first, err := Open(ctx, doltRoot, "ws-1")
	if err != nil {
		t.Fatalf("Open(first): %v", err)
	}
	if err := first.EnsureIssuePrefix(ctx, "test"); err != nil {
		t.Fatalf("EnsureIssuePrefix: %v", err)
	}
	issue, err := first.CreateIssue(ctx, CreateIssueInput{Title: "high pri", Topic: "schema", IssueType: "task", Priority: 0})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	// Bypass the Store's priority validator by hitting the DB directly. The
	// scenario under test is "an existing v1 DB has rows at non-zero priority"
	// — those rows predate the framework, so the value need not pass current
	// validation.
	if err := first.ExecRawForTest(ctx, `UPDATE issues SET priority = 2 WHERE id = ?`, issue.ID); err != nil {
		t.Fatalf("seed legacy priority: %v", err)
	}
	regressSchemaVersionToBaseline(t, ctx, first)
	if err := first.Close(); err != nil {
		t.Fatalf("Close(first): %v", err)
	}

	second, err := Open(ctx, doltRoot, "ws-1")
	if err != nil {
		t.Fatalf("Open(second): %v", err)
	}
	defer second.Close()

	got, err := second.GetIssue(ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.Priority != 0 {
		t.Fatalf("priority after migrate = %d, want 0", got.Priority)
	}
	if v := readSchemaVersionForTest(t, ctx, second); v != "2" {
		t.Fatalf("schema_version = %q, want \"2\"", v)
	}
	var status string
	if err := second.db.QueryRowContext(ctx,
		`SELECT status FROM migration_log WHERE version = 2 ORDER BY id DESC LIMIT 1`).Scan(&status); err != nil {
		t.Fatalf("query migration_log: %v", err)
	}
	if status != migrationStatusSuccess {
		t.Fatalf("migration_log status = %q, want %q", status, migrationStatusSuccess)
	}
}

func TestVersionedMigrationsSkipOnUpToDate(t *testing.T) {
	ctx := context.Background()
	doltRoot := filepath.Join(t.TempDir(), "dolt")
	first, err := Open(ctx, doltRoot, "ws-1")
	if err != nil {
		t.Fatalf("Open(first): %v", err)
	}
	beforeRows := countMigrationLogRows(t, ctx, first, 2)
	if err := first.Close(); err != nil {
		t.Fatalf("Close(first): %v", err)
	}
	commitsBefore, err := doltcli.Run(ctx, filepath.Join(doltRoot, "links"), "log", "--oneline")
	if err != nil {
		t.Fatalf("dolt log before: %v", err)
	}

	second, err := Open(ctx, doltRoot, "ws-1")
	if err != nil {
		t.Fatalf("Open(second): %v", err)
	}
	defer second.Close()
	afterRows := countMigrationLogRows(t, ctx, second, 2)
	commitsAfter, err := doltcli.Run(ctx, filepath.Join(doltRoot, "links"), "log", "--oneline")
	if err != nil {
		t.Fatalf("dolt log after: %v", err)
	}
	if afterRows != beforeRows {
		t.Fatalf("migration_log v=2 rows changed across reopen: before=%d after=%d", beforeRows, afterRows)
	}
	if countNonEmptyLines(commitsAfter) != countNonEmptyLines(commitsBefore) {
		t.Fatalf("dolt commits changed across reopen:\nbefore:\n%s\nafter:\n%s", commitsBefore, commitsAfter)
	}
}

func TestVersionedMigrationsAreStrictlyAscending(t *testing.T) {
	expected := 2
	for _, m := range versionedMigrations {
		if m.version != expected {
			t.Fatalf("versionedMigrations not strictly ascending: got version %d at expected %d", m.version, expected)
		}
		expected++
	}
}

func TestResetPrioritiesToNormalRunsOnce(t *testing.T) {
	ctx := context.Background()
	doltRoot := filepath.Join(t.TempDir(), "dolt")
	first, err := Open(ctx, doltRoot, "ws-1")
	if err != nil {
		t.Fatalf("Open(first): %v", err)
	}
	if err := first.EnsureIssuePrefix(ctx, "test"); err != nil {
		t.Fatalf("EnsureIssuePrefix: %v", err)
	}
	issue, err := first.CreateIssue(ctx, CreateIssueInput{Title: "after migrate", Topic: "schema", IssueType: "task", Priority: 0})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	// After v2 has run, post-migration writes must survive subsequent opens.
	if err := first.ExecRawForTest(ctx, `UPDATE issues SET priority = 3 WHERE id = ?`, issue.ID); err != nil {
		t.Fatalf("post-migrate priority bump: %v", err)
	}
	if err := first.commitWorkingSet(ctx, "test: post-migrate priority"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second, err := Open(ctx, doltRoot, "ws-1")
	if err != nil {
		t.Fatalf("Open(second): %v", err)
	}
	defer second.Close()
	got, err := second.GetIssue(ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.Priority != 3 {
		t.Fatalf("priority = %d after second open, want 3 (migration must not re-run)", got.Priority)
	}
}

func TestSafetyBranchCreatedBeforeVersionedMigrations(t *testing.T) {
	ctx := context.Background()
	doltRoot := filepath.Join(t.TempDir(), "dolt")
	first, err := Open(ctx, doltRoot, "ws-1")
	if err != nil {
		t.Fatalf("Open(first): %v", err)
	}
	regressSchemaVersionToBaseline(t, ctx, first)
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second, err := Open(ctx, doltRoot, "ws-1")
	if err != nil {
		t.Fatalf("Open(second): %v", err)
	}
	defer second.Close()

	rows, err := second.db.QueryContext(ctx, `SELECT name FROM dolt_branches`)
	if err != nil {
		t.Fatalf("query dolt_branches: %v", err)
	}
	defer rows.Close()
	var branches []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan branch: %v", err)
		}
		branches = append(branches, name)
	}
	found := false
	for _, b := range branches {
		if strings.HasPrefix(b, "pre-migrate-v2-") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected pre-migrate-v2-* branch, got %v", branches)
	}
}

func TestPerMigrationCommitMessageFormat(t *testing.T) {
	ctx := context.Background()
	doltRoot := filepath.Join(t.TempDir(), "dolt")
	first, err := Open(ctx, doltRoot, "ws-1")
	if err != nil {
		t.Fatalf("Open(first): %v", err)
	}
	if err := first.EnsureIssuePrefix(ctx, "test"); err != nil {
		t.Fatalf("EnsureIssuePrefix: %v", err)
	}
	issue, err := first.CreateIssue(ctx, CreateIssueInput{Title: "row", Topic: "schema", IssueType: "task", Priority: 0})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := first.ExecRawForTest(ctx, `UPDATE issues SET priority = 4 WHERE id = ?`, issue.ID); err != nil {
		t.Fatalf("seed priority: %v", err)
	}
	regressSchemaVersionToBaseline(t, ctx, first)
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second, err := Open(ctx, doltRoot, "ws-1")
	if err != nil {
		t.Fatalf("Open(second): %v", err)
	}
	defer second.Close()

	logOut, err := doltcli.Run(ctx, filepath.Join(doltRoot, "links"), "log", "--oneline")
	if err != nil {
		t.Fatalf("dolt log: %v", err)
	}
	if !strings.Contains(logOut, "migrate v2: reset_priorities_to_normal (rows=") {
		t.Fatalf("dolt log missing per-migration commit message:\n%s", logOut)
	}
}

func TestMigrationFailureLogsAndAborts(t *testing.T) {
	ctx := context.Background()
	doltRoot := filepath.Join(t.TempDir(), "dolt")
	first, err := Open(ctx, doltRoot, "ws-1")
	if err != nil {
		t.Fatalf("Open(first): %v", err)
	}
	regressSchemaVersionToBaseline(t, ctx, first)
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	failingErr := errors.New("synthetic body failure")
	prevActive := activeMigrations
	activeMigrations = func() []migration {
		return []migration{
			{version: 2, name: "synthetic_failure", up: func(ctx context.Context, s *Store, tx *sql.Tx) (int64, error) {
				return 0, failingErr
			}},
		}
	}
	second, err := Open(ctx, doltRoot, "ws-1")
	if err == nil {
		_ = second.Close()
		activeMigrations = prevActive
		t.Fatal("Open succeeded; expected migration failure to abort startup")
	}
	if !strings.Contains(err.Error(), "synthetic body failure") {
		activeMigrations = prevActive
		t.Fatalf("error = %v, want it to wrap synthetic body failure", err)
	}
	activeMigrations = prevActive

	// Reopen with the production migration set so we can inspect the log row.
	// The schema_version was at 1 when the failure aborted; the production v2
	// runs cleanly here and advances it to 2. The synthetic failure row from
	// the previous attempt must still be present.
	probe, err := Open(ctx, doltRoot, "ws-1")
	if err != nil {
		t.Fatalf("Open(probe) after failure: %v", err)
	}
	defer probe.Close()
	// schema_version stays at 1 because the production migration also ran on
	// this Open and advanced it to 2 — but the failure row from the previous
	// attempt must still exist.
	var status, errText string
	if err := probe.db.QueryRowContext(ctx,
		`SELECT status, COALESCE(error,'') FROM migration_log WHERE name = 'synthetic_failure' ORDER BY id DESC LIMIT 1`).Scan(&status, &errText); err != nil {
		t.Fatalf("query migration_log: %v", err)
	}
	if status != migrationStatusFailure {
		t.Fatalf("status = %q, want %q", status, migrationStatusFailure)
	}
	if !strings.Contains(errText, "synthetic body failure") {
		t.Fatalf("error text = %q, want it to contain synthetic body failure", errText)
	}
}

func TestDryRunMode(t *testing.T) {
	ctx := context.Background()
	doltRoot := filepath.Join(t.TempDir(), "dolt")
	first, err := Open(ctx, doltRoot, "ws-1")
	if err != nil {
		t.Fatalf("Open(first): %v", err)
	}
	if err := first.EnsureIssuePrefix(ctx, "test"); err != nil {
		t.Fatalf("EnsureIssuePrefix: %v", err)
	}
	issue, err := first.CreateIssue(ctx, CreateIssueInput{Title: "stay urgent", Topic: "schema", IssueType: "task", Priority: 0})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := first.ExecRawForTest(ctx, `UPDATE issues SET priority = 2 WHERE id = ?`, issue.ID); err != nil {
		t.Fatalf("seed priority: %v", err)
	}
	regressSchemaVersionToBaseline(t, ctx, first)
	beforeRows := countMigrationLogRows(t, ctx, first, 2)
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	commitsBefore, err := doltcli.Run(ctx, filepath.Join(doltRoot, "links"), "log", "--oneline")
	if err != nil {
		t.Fatalf("dolt log before: %v", err)
	}

	t.Setenv(dryRunEnvVar, "1")
	second, err := Open(ctx, doltRoot, "ws-1")
	if err != nil {
		t.Fatalf("Open dry-run: %v", err)
	}
	stderr := &bytes.Buffer{}
	second.SetLoggerForTest(stderr)
	// Re-run the migration runner with the test logger attached so we can
	// capture the dry-run lines (the Open above already ran it once with the
	// production logger). Schema state is unchanged because dry-run rolls back.
	if err := second.runVersionedMigrations(ctx); err != nil {
		t.Fatalf("runVersionedMigrations dry-run: %v", err)
	}

	got, err := second.GetIssue(ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.Priority != 2 {
		t.Fatalf("priority = %d after dry-run, want 2 (no mutation)", got.Priority)
	}
	if v := readSchemaVersionForTest(t, ctx, second); v != "1" {
		t.Fatalf("schema_version = %q after dry-run, want \"1\"", v)
	}
	afterRows := countMigrationLogRows(t, ctx, second, 2)
	if afterRows != beforeRows {
		t.Fatalf("migration_log v=2 rows changed under dry-run: before=%d after=%d", beforeRows, afterRows)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	commitsAfter, err := doltcli.Run(ctx, filepath.Join(doltRoot, "links"), "log", "--oneline")
	if err != nil {
		t.Fatalf("dolt log after: %v", err)
	}
	if countNonEmptyLines(commitsAfter) != countNonEmptyLines(commitsBefore) {
		t.Fatalf("dry-run produced new Dolt commits:\nbefore:\n%s\nafter:\n%s", commitsBefore, commitsAfter)
	}
	if !strings.Contains(stderr.String(), fmt.Sprintf("status=%s", migrationStatusDryRun)) {
		t.Fatalf("stderr missing status=dry_run line:\n%s", stderr.String())
	}
}

func TestDryRunModeReportsErrors(t *testing.T) {
	ctx := context.Background()
	doltRoot := filepath.Join(t.TempDir(), "dolt")
	first, err := Open(ctx, doltRoot, "ws-1")
	if err != nil {
		t.Fatalf("Open(first): %v", err)
	}
	regressSchemaVersionToBaseline(t, ctx, first)
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	t.Setenv(dryRunEnvVar, "1")
	withActiveMigrationsForTest(t, []migration{
		{version: 2, name: "dry_run_failure", up: func(ctx context.Context, s *Store, tx *sql.Tx) (int64, error) {
			return 0, errors.New("synthetic dry-run body error")
		}},
	})
	second, err := Open(ctx, doltRoot, "ws-1")
	if err == nil {
		_ = second.Close()
		t.Fatal("dry-run Open succeeded; expected body error to surface")
	}
	if !strings.Contains(err.Error(), "synthetic dry-run body error") {
		t.Fatalf("error = %v, want it to wrap synthetic dry-run body error", err)
	}
}
