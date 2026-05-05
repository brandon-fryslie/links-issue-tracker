package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// [LAW:one-source-of-truth] versionedMigrations is the only registry of
// post-baseline schema migrations; meta.schema_version is the only ran-once
// marker. Forward-only — corrective work lands as a new higher version, never
// as a "down" body. New entries append; never reorder, never renumber, never
// reuse a version.
//
// [LAW:no-mode-explosion] No flag or option gates a migration body — the only
// variability is the version number, carried in data.

// dryRunEnvVar names the environment variable that switches every code path
// calling Store.Open into dry-run mode. Per [LAW:single-enforcer] the runner is
// the only consumer of this knob; nothing else in the codebase should branch on
// it.
const dryRunEnvVar = "LIT_MIGRATE_DRY_RUN"

// migrationLogStatus enumerates the durable statuses a migration_log row may
// hold. dry_run rows are written to stderr only and never persist.
const (
	migrationStatusSuccess = "success"
	migrationStatusFailure = "failure"
	migrationStatusDryRun  = "dry_run"
	migrationStatusRunning = "running"
)

type migration struct {
	version int
	name    string
	up      func(ctx context.Context, s *Store, tx *sql.Tx) (rows int64, err error)
}

var versionedMigrations = []migration{
	{version: 2, name: "reset_priorities_to_normal", up: resetPrioritiesToNormal},
}

// activeMigrations returns the migration set the runner consults. Production
// callers see versionedMigrations; tests swap this via withMigrationsForTest to
// inject failures or alternative bodies. [LAW:single-enforcer] All migration
// dispatch reads through this single accessor.
var activeMigrations = func() []migration { return versionedMigrations }

func init() {
	// [LAW:single-enforcer] Strict-ascending invariant is checked once, at
	// startup, so no migration body needs to defensively re-validate ordering.
	expected := 2
	for _, m := range versionedMigrations {
		if m.version != expected {
			panic(fmt.Sprintf("versionedMigrations must be strictly ascending starting at 2: got version %d at expected %d", m.version, expected))
		}
		if strings.TrimSpace(m.name) == "" {
			panic(fmt.Sprintf("versionedMigrations[v=%d] missing name", m.version))
		}
		if m.up == nil {
			panic(fmt.Sprintf("versionedMigrations[v=%d] missing up func", m.version))
		}
		expected++
	}
}

// runVersionedMigrations is the [LAW:single-enforcer] gate for forward-only
// schema migrations. It reads meta.schema_version, runs every registered
// migration whose version exceeds the current marker, and advances the marker
// after each body's Dolt commit.
//
// [LAW:dataflow-not-control-flow] The same code path runs every Store.Open;
// the version value (not an `if` around the runner) decides which migration
// bodies execute.
func (s *Store) runVersionedMigrations(ctx context.Context) error {
	current, err := s.readSchemaVersion(ctx)
	if err != nil {
		return err
	}
	pending := pendingMigrations(activeMigrations(), current)
	if len(pending) == 0 {
		return nil
	}
	if isDryRunMode() {
		return s.runVersionedMigrationsDryRun(ctx, pending)
	}
	return s.runVersionedMigrationsApply(ctx, pending)
}

func pendingMigrations(all []migration, current int) []migration {
	pending := make([]migration, 0, len(all))
	for _, m := range all {
		if m.version > current {
			pending = append(pending, m)
		}
	}
	return pending
}

func isDryRunMode() bool {
	v := strings.TrimSpace(os.Getenv(dryRunEnvVar))
	return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
}

func (s *Store) readSchemaVersion(ctx context.Context) (int, error) {
	raw, err := s.getMeta(ctx, nil, "schema_version")
	if err != nil {
		return 0, fmt.Errorf("read schema_version: %w", err)
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 1, nil
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, fmt.Errorf("parse schema_version %q: %w", raw, err)
	}
	if n < 1 {
		return 1, nil
	}
	return n, nil
}

func (s *Store) runVersionedMigrationsApply(ctx context.Context, pending []migration) error {
	if err := s.createSafetyBranch(ctx, pending[0].version); err != nil {
		return err
	}
	for _, m := range pending {
		if err := s.applyMigration(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

// applyMigration runs a single migration's body inside its own SQL tx and
// records the durable migration_log row. The schema_version bump and the body
// share the same tx so a partial advance is impossible — a crash mid-body
// rolls back both, and the next Open re-runs the same version.
func (s *Store) applyMigration(ctx context.Context, m migration) error {
	startedAt := time.Now().UTC().Format(time.RFC3339Nano)
	logID, err := s.insertMigrationLogStart(ctx, m, startedAt)
	if err != nil {
		return err
	}
	s.logMigrationLine(m, migrationStatusRunning, 0, 0, "")

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migrate v%d tx: %w", m.version, err)
	}
	rows, bodyErr := m.up(ctx, s, tx)
	if bodyErr == nil {
		if err := s.setMeta(ctx, tx, "schema_version", strconv.Itoa(m.version)); err != nil {
			bodyErr = err
		}
	}
	if bodyErr != nil {
		_ = tx.Rollback()
		finishedAt := time.Now().UTC().Format(time.RFC3339Nano)
		dur := durationMillis(startedAt, finishedAt)
		if err := s.markMigrationLogFailure(ctx, logID, finishedAt, bodyErr); err != nil {
			s.logMigrationLine(m, migrationStatusFailure, 0, dur, fmt.Sprintf("%v (also failed to record migration_log: %v)", bodyErr, err))
		} else {
			s.logMigrationLine(m, migrationStatusFailure, 0, dur, bodyErr.Error())
		}
		// Persist the failure log row so operators can inspect it after the
		// startup abort. [LAW:single-enforcer] commitWorkingSet is the only
		// path to a Dolt commit; failure path is no exception.
		if commitErr := s.commitWorkingSet(ctx, fmt.Sprintf("migrate v%d: %s failed", m.version, m.name)); commitErr != nil {
			return fmt.Errorf("migrate v%d %s: %w (commit failure log: %v)", m.version, m.name, bodyErr, commitErr)
		}
		return fmt.Errorf("migrate v%d %s: %w", m.version, m.name, bodyErr)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migrate v%d tx: %w", m.version, err)
	}
	finishedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if err := s.markMigrationLogSuccess(ctx, logID, finishedAt, rows); err != nil {
		return fmt.Errorf("record migration_log success v%d: %w", m.version, err)
	}
	dur := durationMillis(startedAt, finishedAt)
	s.logMigrationLine(m, migrationStatusSuccess, rows, dur, "")
	if err := s.commitWorkingSet(ctx, fmt.Sprintf("migrate v%d: %s (rows=%d)", m.version, m.name, rows)); err != nil {
		return err
	}
	return nil
}

func (s *Store) runVersionedMigrationsDryRun(ctx context.Context, pending []migration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin dry-run tx: %w", err)
	}
	// [LAW:dataflow-not-control-flow] All bodies run inside the same tx;
	// rollback at the end is unconditional, so dry-run never mutates persisted
	// state regardless of which bodies executed.
	for _, m := range pending {
		startedAt := time.Now().UTC().Format(time.RFC3339Nano)
		s.logMigrationLine(m, migrationStatusRunning, 0, 0, "")
		rows, bodyErr := m.up(ctx, s, tx)
		finishedAt := time.Now().UTC().Format(time.RFC3339Nano)
		dur := durationMillis(startedAt, finishedAt)
		if bodyErr != nil {
			_ = tx.Rollback()
			s.logMigrationLine(m, migrationStatusFailure, rows, dur, bodyErr.Error())
			return fmt.Errorf("dry-run migrate v%d %s: %w", m.version, m.name, bodyErr)
		}
		s.logMigrationLine(m, migrationStatusDryRun, rows, dur, "")
	}
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return fmt.Errorf("rollback dry-run tx: %w", err)
	}
	return nil
}

// createSafetyBranch records a Dolt branch pointing at master HEAD before any
// versioned migration runs. Recovery from a botched migration is then `dolt
// reset --hard <branch>` from outside the app. The framework never deletes the
// branch — it is the operator's safety net.
func (s *Store) createSafetyBranch(ctx context.Context, firstPendingVersion int) error {
	branch := fmt.Sprintf("pre-migrate-v%d-%d", firstPendingVersion, time.Now().Unix())
	// DOLT_BRANCH('<name>', 'master') creates <name> at master's HEAD. If a
	// branch with the exact name already exists (improbable: the unix-second
	// timestamp differs each call) we treat the collision as no-op; the
	// existing branch points at the same HEAD this call would have created.
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("CALL DOLT_BRANCH('%s', 'master')", strings.ReplaceAll(branch, "'", "''"))); err != nil {
		normalized := strings.ToLower(err.Error())
		if strings.Contains(normalized, "already exists") {
			return nil
		}
		return fmt.Errorf("create safety branch %s: %w", branch, err)
	}
	return nil
}

func (s *Store) insertMigrationLogStart(ctx context.Context, m migration, startedAt string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO migration_log(version, name, started_at, status, rows_affected) VALUES (?, ?, ?, ?, 0)`,
		m.version, m.name, startedAt, migrationStatusRunning)
	if err != nil {
		return 0, fmt.Errorf("insert migration_log start v%d: %w", m.version, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("migration_log last insert id v%d: %w", m.version, err)
	}
	return id, nil
}

func (s *Store) markMigrationLogSuccess(ctx context.Context, id int64, finishedAt string, rows int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE migration_log SET status = ?, finished_at = ?, rows_affected = ? WHERE id = ?`,
		migrationStatusSuccess, finishedAt, rows, id)
	if err != nil {
		return fmt.Errorf("update migration_log success: %w", err)
	}
	return nil
}

func (s *Store) markMigrationLogFailure(ctx context.Context, id int64, finishedAt string, bodyErr error) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE migration_log SET status = ?, finished_at = ?, error = ? WHERE id = ?`,
		migrationStatusFailure, finishedAt, bodyErr.Error(), id)
	if err != nil {
		return fmt.Errorf("update migration_log failure: %w", err)
	}
	return nil
}

// logMigrationLine writes the canonical structured event line. Single source of
// phrasing across stderr; [LAW:one-source-of-truth] no caller assembles its own
// variant of this format.
func (s *Store) logMigrationLine(m migration, status string, rows int64, durMs int64, errMsg string) {
	w := s.logger
	if w == nil {
		return
	}
	line := fmt.Sprintf("lit migrate: v=%d name=%s status=%s rows=%d dur=%dms", m.version, m.name, status, rows, durMs)
	if errMsg != "" {
		line += fmt.Sprintf(" err=%s", strings.ReplaceAll(errMsg, "\n", " "))
	}
	_, _ = fmt.Fprintln(w, line)
}

func durationMillis(startedAt, finishedAt string) int64 {
	start, err := time.Parse(time.RFC3339Nano, startedAt)
	if err != nil {
		return 0
	}
	end, err := time.Parse(time.RFC3339Nano, finishedAt)
	if err != nil {
		return 0
	}
	return end.Sub(start).Milliseconds()
}

// resetPrioritiesToNormal is the v2 migration body. It rewrites every issue's
// priority to the canonical "normal" value (0). Idempotent within its own
// version: running it twice yields the same final state. After it runs once,
// schema_version is bumped to 2 and the body never executes again — so user
// edits made after the migration are preserved.
//
// [LAW:single-enforcer] The runner is the only gate; this body does not
// re-check schema_version itself.
func resetPrioritiesToNormal(ctx context.Context, s *Store, tx *sql.Tx) (int64, error) {
	const priorityNormal = 0
	res, err := tx.ExecContext(ctx, `UPDATE issues SET priority = ? WHERE priority <> ?`, priorityNormal, priorityNormal)
	if err != nil {
		return 0, fmt.Errorf("reset priorities to normal: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("reset priorities rows affected: %w", err)
	}
	return rows, nil
}
