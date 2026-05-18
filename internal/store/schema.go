package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/bmf/links-issue-tracker/internal/model"
	"github.com/bmf/links-issue-tracker/internal/rank"
)

// CurrentSchemaVersion is the schema version this binary knows how to produce.
// Bumped exactly when a new migration step is appended to slowPathReconcile.
// Paired with the workspace's `meta.schema_version` row — equality routes
// Open() to the fast path; inequality routes to slow-path reconciliation.
// See design-docs/MIGRATION-ARCHITECTURE.md.
// [LAW:one-source-of-truth] Single integer the migration contract pivots on.
const CurrentSchemaVersion = 1

// schemaVersionAbsent is the sentinel readSchemaStamp returns when the meta
// table is missing (fresh database), no schema_version row exists, or the
// stored value does not parse as a non-negative integer. All three are
// equivalent to "stamp absent" for routing purposes — the slow path treats
// them identically.
const schemaVersionAbsent = -1

// priorityCheckClause is the schema-level encoding of the priority range
// invariant. Derived from the canonical model.Priority* constants and shared
// by the fresh-table CREATE (createIssuesTableStmt) and the upgrade-path
// ALTER (resetPrioritiesToNormal) so the two writers cannot drift.
// [LAW:one-source-of-truth]
var priorityCheckClause = fmt.Sprintf("priority >= %d AND priority <= %d", model.PriorityNormal, model.PriorityUrgent)

// canonicalStatusCheckClause encodes the invariant that container rows store
// NULL status (state is derived from children) and leaf rows carry one of the
// known states. Single source of truth used by both the fresh-table CREATE
// (via createIssuesTableStmt) and the upgrade-path ALTER (ensureStatusConstraint)
// so they cannot diverge. The leaf branch carries an explicit `status IS NOT
// NULL`: `IN (...)` against NULL evaluates to NULL, and MySQL/Dolt CHECK
// treats NULL as not-violated, so without this clause a leaf row with NULL
// status would slip through the very constraint it is supposed to forbid.
const canonicalStatusCheckClause = `(issue_type IN ('epic') AND status IS NULL) OR (issue_type NOT IN ('epic') AND status IS NOT NULL AND status IN ('open','in_progress','closed'))`

func createIssuesTableStmt() string {
	return fmt.Sprintf(`CREATE TABLE issues (
			id VARCHAR(191) PRIMARY KEY,
			title TEXT NOT NULL,
			description TEXT NOT NULL,
			agent_prompt TEXT,
			status VARCHAR(32) NULL,
			priority INT NOT NULL,
			issue_type VARCHAR(32) NOT NULL,
			topic VARCHAR(191) NOT NULL,
			assignee TEXT NOT NULL,
			created_at VARCHAR(64) NOT NULL,
			updated_at VARCHAR(64) NOT NULL,
			closed_at VARCHAR(64) NULL,
			archived_at VARCHAR(64) NULL,
			deleted_at VARCHAR(64) NULL,
			CHECK(%s),
			CHECK(%s),
			CHECK(issue_type IN ('task','feature','bug','chore','epic'))
		);`, canonicalStatusCheckClause, priorityCheckClause)
}

// MigrationQueryCount returns the number of SQL queries the most recent
// migrate() call issued. Exposed for the no-op-Open query-count assertion;
// callers outside tests should ignore this. See
// design-docs/MIGRATION-ARCHITECTURE.md.
func (s *Store) MigrationQueryCount() int {
	return s.migrationQueryCount
}

// migrate is the single entrypoint Open and OpenForRead use to bring the
// workspace into a state the binary can talk to. Two paths exist; the
// workspace stamp picks one:
//
//   - Fast path (stamp == CurrentSchemaVersion): three queries — read stamp,
//     ensure workspace_id, smoke probe. No DDL, no information_schema, no
//     reconciliation. This is the steady-state cost of every CLI invocation.
//   - Slow path (everything else): take a snapshot once r5v9.5 lands, run the
//     idempotent reconciliation block below, then write the stamp atomically
//     with the schema change.
//
// [LAW:types-are-the-program] The stamp is the discriminator; path selection
// follows from data, not probe results.
// [LAW:single-enforcer] Stamp comparison happens here exactly once — the rest
// of the store treats whichever path ran as authoritative.
func (s *Store) migrate(ctx context.Context) error {
	s.migrationQueryCount = 0
	stamp, err := s.readSchemaStamp(ctx)
	if err != nil {
		return err
	}
	if stamp == CurrentSchemaVersion {
		return s.fastPath(ctx)
	}
	return s.slowPathReconcile(ctx, stamp)
}

// readSchemaStamp returns the workspace's recorded schema version, or
// schemaVersionAbsent when the meta table or row is missing or the value is
// unparseable. Counted as one migration query.
func (s *Store) readSchemaStamp(ctx context.Context) (int, error) {
	s.migrationQueryCount++
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT meta_value FROM meta WHERE meta_key = ?`, "schema_version").Scan(&raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || isMissingMetaTableError(err) {
			return schemaVersionAbsent, nil
		}
		return schemaVersionAbsent, fmt.Errorf("read schema stamp: %w", err)
	}
	parsed, parseErr := strconv.Atoi(strings.TrimSpace(raw))
	if parseErr != nil || parsed < 0 {
		return schemaVersionAbsent, nil
	}
	return parsed, nil
}

// fastPath is the steady-state Open contract: workspace_id ensure + smoke
// probe. Two more queries on top of the stamp read, giving ≤3 total on a
// no-op Open. Issues a workspace_id write + commit only when the recorded
// value diverges from the configured workspaceID — rare in steady state.
//
// [LAW:dataflow-not-control-flow] Same two operations every fast path. The
// workspace_id mismatch case adds writes through the same commitWorkingSet
// path every other mutation uses; no fast-path-specific branching beyond the
// equality test inherent to ensure-style writes.
func (s *Store) fastPath(ctx context.Context) error {
	if err := s.fastPathEnsureWorkspaceID(ctx); err != nil {
		return err
	}
	return s.fastPathSmoke(ctx)
}

func (s *Store) fastPathEnsureWorkspaceID(ctx context.Context) error {
	s.migrationQueryCount++
	var current string
	err := s.db.QueryRowContext(ctx, `SELECT meta_value FROM meta WHERE meta_key = ?`, "workspace_id").Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("fast path read workspace_id: %w", err)
	}
	if current == s.workspaceID {
		return nil
	}
	s.migrationQueryCount++
	if _, err := s.db.ExecContext(ctx, `INSERT INTO meta(meta_key, meta_value) VALUES (?, ?)
			ON DUPLICATE KEY UPDATE meta_value = VALUES(meta_value)`, "workspace_id", s.workspaceID); err != nil {
		return fmt.Errorf("fast path write workspace_id: %w", err)
	}
	s.migrationQueryCount++
	return s.commitWorkingSet(ctx, "Refresh workspace_id")
}

// fastPathSmoke verifies that the canonical leaf table is reachable through
// the current connection. Caught here so a botched workspace fails on Open
// with a clear schema error rather than a downstream operation surfacing a
// cryptic "table doesn't exist." Single query; tolerates the empty-table
// case.
func (s *Store) fastPathSmoke(ctx context.Context) error {
	s.migrationQueryCount++
	var probe int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM issues LIMIT 1`).Scan(&probe)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("schema smoke: %w", err)
	}
	return nil
}

// isMissingMetaTableError is true for the Dolt/MySQL "table doesn't exist"
// error shape readSchemaStamp may receive on a fresh database. Tolerated
// because the slow path is the safety net: a missing meta table provably
// means migration is needed, so the fast path's role is to route there
// cleanly rather than surface the error.
func isMissingMetaTableError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "table not found") ||
		strings.Contains(msg, "doesn't exist") ||
		strings.Contains(msg, "does not exist")
}

// slowPathReconcile runs the full idempotent reconciliation. Entered when the
// workspace stamp is absent, lower than CurrentSchemaVersion, or — as a
// safety net — higher (a future r5v9.4 will turn the "higher" case into a
// hard refusal). Every step here must be safe to re-run; the stamp comparison
// already filtered the no-op case, so any redundancy hits this code in tests
// and at compat-boundary moments only.
func (s *Store) slowPathReconcile(ctx context.Context, currentStamp int) error {
	changed := false
	schema := []string{
		`CREATE TABLE meta (
			meta_key VARCHAR(191) PRIMARY KEY,
			meta_value TEXT NOT NULL
		);`,
		createIssuesTableStmt(),
		`CREATE TABLE relations (
			src_id VARCHAR(191) NOT NULL,
			dst_id VARCHAR(191) NOT NULL,
			type VARCHAR(32) NOT NULL,
			created_at VARCHAR(64) NOT NULL,
			created_by TEXT NOT NULL,
			PRIMARY KEY (src_id, dst_id, type),
			FOREIGN KEY (src_id) REFERENCES issues(id) ON DELETE CASCADE,
			FOREIGN KEY (dst_id) REFERENCES issues(id) ON DELETE CASCADE,
			CHECK(type IN ('blocks','parent-child','related-to'))
		);`,
		`CREATE TABLE comments (
			id VARCHAR(191) PRIMARY KEY,
			issue_id VARCHAR(191) NOT NULL,
			body TEXT NOT NULL,
			created_at VARCHAR(64) NOT NULL,
			created_by TEXT NOT NULL,
			FOREIGN KEY (issue_id) REFERENCES issues(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE labels (
			issue_id VARCHAR(191) NOT NULL,
			label VARCHAR(191) NOT NULL,
			created_at VARCHAR(64) NOT NULL,
			created_by TEXT NOT NULL,
			PRIMARY KEY (issue_id, label),
			FOREIGN KEY (issue_id) REFERENCES issues(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX idx_issues_status_priority ON issues(status, priority, updated_at);`,
		`CREATE INDEX idx_relations_src_type ON relations(src_id, type);`,
		`CREATE INDEX idx_relations_dst_type ON relations(dst_id, type);`,
		`CREATE INDEX idx_comments_issue_created ON comments(issue_id, created_at);`,
		`CREATE INDEX idx_labels_issue ON labels(issue_id, label);`,
		`CREATE INDEX idx_labels_name ON labels(label, issue_id);`,
		// [LAW:one-source-of-truth] issue_events is the canonical mutation log
		// for every issue field. The legacy issue_history schema (status-only,
		// from/to columns that lied for archive/delete) is dropped below.
		`CREATE TABLE issue_events (
			id VARCHAR(191) PRIMARY KEY,
			issue_id VARCHAR(191) NOT NULL,
			action VARCHAR(64) NULL,
			reason TEXT NOT NULL,
			actor TEXT NOT NULL,
			created_at VARCHAR(64) NOT NULL,
			FOREIGN KEY (issue_id) REFERENCES issues(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE issue_event_changes (
			event_id VARCHAR(191) NOT NULL,
			field VARCHAR(64) NOT NULL,
			from_value TEXT NULL,
			to_value TEXT NULL,
			PRIMARY KEY (event_id, field),
			FOREIGN KEY (event_id) REFERENCES issue_events(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX idx_issue_events_issue_created ON issue_events(issue_id, created_at);`,
	}
	for _, stmt := range schema {
		stmtChanged, err := execIgnoreAlreadyExists(ctx, s.db, stmt)
		if err != nil {
			return err
		}
		changed = changed || stmtChanged
	}
	// [LAW:one-source-of-truth] issue_history is superseded by
	// issue_events + issue_event_changes. Existing repos may still have it;
	// drop it (existing history rows are discarded — issues are untouched).
	if _, err := s.db.ExecContext(ctx, `DROP TABLE IF EXISTS issue_history`); err != nil {
		return fmt.Errorf("drop legacy issue_history table: %w", err)
	}
	// issue_events.assignee was renamed to actor. Probe-gated rename keeps the
	// migration idempotent across fresh / migrated databases.
	actorColumnChanged, err := s.execReconciliationUpdate(
		ctx,
		`SELECT 1 FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'issue_events' AND column_name = 'assignee' LIMIT 1`,
		`ALTER TABLE issue_events RENAME COLUMN assignee TO actor`,
		"rename issue_events.assignee to actor",
	)
	if err != nil {
		return err
	}
	changed = changed || actorColumnChanged
	rankColumnChanged, err := execIgnoreAlreadyExists(ctx, s.db, `ALTER TABLE issues ADD COLUMN item_rank TEXT NOT NULL DEFAULT ''`)
	if err != nil {
		return err
	}
	changed = changed || rankColumnChanged
	rankIndexChanged, err := execIgnoreAlreadyExists(ctx, s.db, `CREATE INDEX idx_issues_rank ON issues(item_rank(191))`)
	if err != nil {
		return err
	}
	changed = changed || rankIndexChanged
	topicColumnChanged, err := execIgnoreAlreadyExists(ctx, s.db, `ALTER TABLE issues ADD COLUMN topic VARCHAR(191) NOT NULL DEFAULT 'misc' AFTER issue_type`)
	if err != nil {
		return err
	}
	changed = changed || topicColumnChanged
	// Workspaces predating the rename still have the old `prompt` column.
	// Probe-gated rename keeps migration idempotent across fresh / migrated /
	// pre-rename workspace states. `prompt` is reserved in Dolt's MySQL parser,
	// so the source-side identifier is backtick-quoted; `agent_prompt` is not
	// reserved and needs no quoting.
	promptRenamedChanged, err := s.execReconciliationUpdate(
		ctx,
		`SELECT 1 FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'issues' AND column_name = 'prompt' LIMIT 1`,
		"ALTER TABLE issues RENAME COLUMN `prompt` TO agent_prompt",
		"rename prompt column to agent_prompt",
	)
	if err != nil {
		return err
	}
	changed = changed || promptRenamedChanged
	promptColumnChanged, err := execIgnoreAlreadyExists(ctx, s.db, "ALTER TABLE issues ADD COLUMN agent_prompt TEXT NULL AFTER `description`")
	if err != nil {
		return err
	}
	changed = changed || promptColumnChanged
	// Workspaces where the column was added before the NULL declaration took
	// effect still have it as NOT NULL, which makes `lit new` fail at the DB
	// layer when no --prompt is supplied. Relax to NULL the same way
	// ensureUnifiedStatusSchema relaxes status; the helper swallows the no-op
	// error when the column is already nullable.
	promptRelaxedChanged, err := execIgnoreAlreadyExists(ctx, s.db, "ALTER TABLE issues MODIFY agent_prompt TEXT NULL")
	if err != nil {
		return err
	}
	changed = changed || promptRelaxedChanged
	statusChanged, err := s.ensureUnifiedStatusSchema(ctx)
	if err != nil {
		return err
	}
	changed = changed || statusChanged
	topicChanged, err := s.ensureIssueTopics(ctx)
	if err != nil {
		return err
	}
	changed = changed || topicChanged
	rankChanged, err := s.ensureIssueRanks(ctx)
	if err != nil {
		return err
	}
	changed = changed || rankChanged
	priorityChanged, err := s.resetPrioritiesToNormal(ctx)
	if err != nil {
		return err
	}
	changed = changed || priorityChanged
	workspaceChanged, err := s.ensureMetaValue(ctx, "workspace_id", s.workspaceID)
	if err != nil {
		return err
	}
	changed = changed || workspaceChanged
	stampChanged, err := s.writeSchemaStampForward(ctx, currentStamp)
	if err != nil {
		return err
	}
	changed = changed || stampChanged
	if !changed {
		return nil
	}
	// [LAW:dataflow-not-control-flow] Slow-path reconciliation always runs the
	// same stages; the derived `changed` value alone decides whether a Dolt
	// commit is needed. The stamp write above is sequenced last so it enters
	// the same commitWorkingSet as the DDL — the atomicity guarantee in
	// design-docs/MIGRATION-ARCHITECTURE.md.
	if err := s.commitWorkingSet(ctx, "Initialize links schema"); err != nil {
		return err
	}
	return nil
}

// writeSchemaStampForward stamps the workspace at CurrentSchemaVersion when
// the workspace is at or below it; preserves higher stamps untouched. Higher
// stamps mean the workspace was last written by a newer binary — downgrading
// silently would be a forward-compat hazard, and r5v9.4 will turn this case
// into a hard refusal at the path-selection boundary. Returns whether a write
// happened (the slow path uses this to decide whether to commitWorkingSet).
// [LAW:no-defensive-null-guards] currentStamp == schemaVersionAbsent is the
// fresh-DB sentinel, handled explicitly rather than masked.
func (s *Store) writeSchemaStampForward(ctx context.Context, currentStamp int) (bool, error) {
	if currentStamp != schemaVersionAbsent && currentStamp > CurrentSchemaVersion {
		return false, nil
	}
	if currentStamp == CurrentSchemaVersion {
		return false, nil
	}
	if err := s.setMeta(ctx, nil, "schema_version", strconv.Itoa(CurrentSchemaVersion)); err != nil {
		return false, err
	}
	return true, nil
}

type issueCheckConstraint struct {
	name   string
	clause string
}

func (s *Store) ensureUnifiedStatusSchema(ctx context.Context) (bool, error) {
	// [LAW:one-source-of-truth] `status` is the canonical workflow state for
	// non-container issues. Containers derive state from children and store NULL.
	changed := false
	// Existing workspaces created before status was nullable still have the
	// column declared NOT NULL. Relax it before any backfill that needs to
	// write NULL. Dolt rejects MODIFY on a column that already matches, so
	// the helper swallows "already" errors via execIgnoreAlreadyExists.
	relaxedChanged, err := execIgnoreAlreadyExists(ctx, s.db, `ALTER TABLE issues MODIFY status VARCHAR(32) NULL`)
	if err != nil {
		return false, err
	}
	changed = changed || relaxedChanged
	legacyStatusUpdates := []struct {
		probe   string
		stmt    string
		context string
	}{
		{
			probe:   `SELECT 1 FROM issues WHERE status = 'in-progress' LIMIT 1`,
			stmt:    `UPDATE issues SET status = 'in_progress' WHERE status = 'in-progress'`,
			context: "normalize legacy in-progress status",
		},
		{
			probe:   `SELECT 1 FROM issues WHERE status = 'todo' LIMIT 1`,
			stmt:    `UPDATE issues SET status = 'open' WHERE status = 'todo'`,
			context: "normalize legacy todo status",
		},
		{
			probe:   `SELECT 1 FROM issues WHERE status = 'done' LIMIT 1`,
			stmt:    `UPDATE issues SET status = 'closed' WHERE status = 'done'`,
			context: "normalize legacy done status",
		},
		{
			probe:   `SELECT 1 FROM issues WHERE status NOT IN ('open','in_progress','closed') LIMIT 1`,
			stmt:    `UPDATE issues SET status = 'open' WHERE status NOT IN ('open','in_progress','closed')`,
			context: "normalize invalid status",
		},
		{
			probe:   `SELECT 1 FROM issues WHERE closed_at IS NOT NULL AND status <> 'closed' LIMIT 1`,
			stmt:    `UPDATE issues SET status = 'closed' WHERE closed_at IS NOT NULL AND status <> 'closed'`,
			context: "normalize closed_at status",
		},
		{
			probe:   `SELECT 1 FROM issues WHERE status <> 'closed' AND closed_at IS NOT NULL LIMIT 1`,
			stmt:    `UPDATE issues SET closed_at = NULL WHERE status <> 'closed'`,
			context: "normalize non-closed closed_at",
		},
		{
			// [LAW:one-source-of-truth] Containers derive state from children;
			// any persisted status on an epic row is dead data left over from
			// the pre-derivation schema. NULL it so the column stops lying and
			// future readers that touch i.status on an epic fail loudly.
			probe:   `SELECT 1 FROM issues WHERE issue_type IN ('epic') AND status IS NOT NULL LIMIT 1`,
			stmt:    `UPDATE issues SET status = NULL WHERE issue_type IN ('epic')`,
			context: "null out container status",
		},
	}
	for _, update := range legacyStatusUpdates {
		updateChanged, err := s.execReconciliationUpdate(ctx, update.probe, update.stmt, update.context)
		if err != nil {
			return false, err
		}
		changed = changed || updateChanged
	}
	constraintChanged, err := s.ensureStatusConstraint(ctx)
	if err != nil {
		return false, err
	}
	changed = changed || constraintChanged
	return changed, nil
}

func (s *Store) ensureIssueTopics(ctx context.Context) (bool, error) {
	// [LAW:single-enforcer] Legacy topic repair happens in one SQL reconciliation stage instead of a second Go defaulting path.
	return s.execReconciliationUpdate(
		ctx,
		`SELECT 1 FROM issues WHERE TRIM(COALESCE(topic, '')) = '' LIMIT 1`,
		`UPDATE issues SET topic = 'misc' WHERE TRIM(COALESCE(topic, '')) = ''`,
		"backfill legacy issue topics",
	)
}

func (s *Store) ensureIssueRanks(ctx context.Context) (bool, error) {
	// Assign ranks to any issues that don't have one yet, preserving the
	// previous default ordering (status, priority, updated_at, id) as the
	// initial rank sequence.
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM issues WHERE item_rank = '' ORDER BY status ASC, priority ASC, updated_at DESC, id ASC")
	if err != nil {
		return false, fmt.Errorf("ensureIssueRanks: query unranked: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return false, fmt.Errorf("ensureIssueRanks: scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("ensureIssueRanks: rows: %w", err)
	}
	if len(ids) == 0 {
		return false, nil
	}
	current := rank.Initial()
	for _, id := range ids {
		if _, err := s.db.ExecContext(ctx, "UPDATE issues SET item_rank = ? WHERE id = ?", current, id); err != nil {
			return false, fmt.Errorf("ensureIssueRanks: update %s: %w", id, err)
		}
		current = rank.After(current)
	}
	return true, nil
}

// resetPrioritiesToNormal performs the one-shot data migration described in
// links-priority-2r6: collapse the legacy 0..4 priority range to {normal=0,
// urgent=1} by resetting all existing priorities to normal, then install the
// canonical CHECK constraint. Gated by the CHECK constraint shape itself: a
// table whose only priority constraint is `priority >= 0 AND priority <= 1`
// is already on the canonical schema (fresh-create or post-migration), so
// the function returns without writing. Otherwise it resets all rows to 0
// before replacing the CHECK so the new constraint can never reject the
// existing data. [LAW:dataflow-not-control-flow] [LAW:single-enforcer]
func (s *Store) resetPrioritiesToNormal(ctx context.Context) (bool, error) {
	constraints, err := s.listIssuePriorityCheckConstraints(ctx)
	if err != nil {
		return false, err
	}
	if hasCanonicalPriorityConstraint(constraints) {
		return false, nil
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("UPDATE issues SET priority = %d", model.PriorityNormal)); err != nil {
		return false, fmt.Errorf("reset priorities to normal: %w", err)
	}
	for _, c := range constraints {
		if _, err := s.db.ExecContext(ctx, "ALTER TABLE issues DROP CHECK `"+strings.ReplaceAll(c.name, "`", "``")+"`"); err != nil {
			return false, fmt.Errorf("drop priority check %s: %w", c.name, err)
		}
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE issues ADD CONSTRAINT issues_priority_check CHECK (%s)", priorityCheckClause)); err != nil {
		return false, fmt.Errorf("add priority check: %w", err)
	}
	return true, nil
}

func (s *Store) listIssuePriorityCheckConstraints(ctx context.Context) ([]issueCheckConstraint, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT tc.constraint_name, cc.check_clause
		FROM information_schema.table_constraints tc
		JOIN information_schema.check_constraints cc
		  ON tc.constraint_schema = cc.constraint_schema
		 AND tc.constraint_name = cc.constraint_name
		WHERE tc.table_schema = DATABASE()
		  AND tc.table_name = 'issues'
		  AND tc.constraint_type = 'CHECK'`)
	if err != nil {
		return nil, fmt.Errorf("query issue check constraints: %w", err)
	}
	defer rows.Close()
	out := []issueCheckConstraint{}
	for rows.Next() {
		var c issueCheckConstraint
		if err := rows.Scan(&c.name, &c.clause); err != nil {
			return nil, fmt.Errorf("scan issue check constraint: %w", err)
		}
		if strings.Contains(normalizeConstraintClause(c.clause), "priority") {
			out = append(out, c)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate issue check constraints: %w", err)
	}
	return out, nil
}

func hasCanonicalPriorityConstraint(constraints []issueCheckConstraint) bool {
	if len(constraints) != 1 {
		return false
	}
	normalized := normalizeConstraintClause(constraints[0].clause)
	// [LAW:one-source-of-truth] Discriminator derived from PriorityUrgent — the
	// upper bound is what differs between the legacy (<=4) and canonical (<=1)
	// shapes.
	return strings.Contains(normalized, fmt.Sprintf("priority<=%d", model.PriorityUrgent))
}

func (s *Store) ensureStatusConstraint(ctx context.Context) (bool, error) {
	checks, err := s.listIssueStatusCheckConstraints(ctx)
	if err != nil {
		return false, err
	}
	if hasCanonicalStatusConstraint(checks) {
		return false, nil
	}
	for _, constraint := range checks {
		if _, err := s.db.ExecContext(ctx, "ALTER TABLE issues DROP CHECK `"+strings.ReplaceAll(constraint.name, "`", "``")+"`"); err != nil {
			return false, fmt.Errorf("drop status check %s: %w", constraint.name, err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE issues ADD CONSTRAINT issues_status_check CHECK (`+canonicalStatusCheckClause+`)`); err != nil {
		return false, fmt.Errorf("add canonical status check: %w", err)
	}
	return true, nil
}

func (s *Store) listIssueStatusCheckConstraints(ctx context.Context) ([]issueCheckConstraint, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT tc.constraint_name, cc.check_clause
		FROM information_schema.table_constraints tc
		JOIN information_schema.check_constraints cc
		  ON tc.constraint_schema = cc.constraint_schema
		 AND tc.constraint_name = cc.constraint_name
		WHERE tc.table_schema = DATABASE()
		  AND tc.table_name = 'issues'
		  AND tc.constraint_type = 'CHECK'`)
	if err != nil {
		return nil, fmt.Errorf("query issue check constraints: %w", err)
	}
	defer rows.Close()
	out := []issueCheckConstraint{}
	for rows.Next() {
		var constraint issueCheckConstraint
		if err := rows.Scan(&constraint.name, &constraint.clause); err != nil {
			return nil, fmt.Errorf("scan issue check constraint: %w", err)
		}
		normalized := normalizeConstraintClause(constraint.clause)
		if strings.Contains(normalized, "statusin(") {
			out = append(out, constraint)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate issue check constraints: %w", err)
	}
	return out, nil
}

func hasCanonicalStatusConstraint(constraints []issueCheckConstraint) bool {
	if len(constraints) != 1 {
		return false
	}
	// Dolt's information_schema.check_clauses may report the clause with or
	// without an outer wrapping pair of parentheses depending on how the
	// constraint was added. Tolerate either side wrapping the other so the
	// migration stays idempotent across normalization differences. Drift past
	// this is caught by TestMigrationIsIdempotentOnSecondOpen.
	actual := normalizeConstraintClause(constraints[0].clause)
	expected := normalizeConstraintClause(canonicalStatusCheckClause)
	return strings.Contains(actual, expected) || strings.Contains(expected, actual)
}

func normalizeConstraintClause(clause string) string {
	replacer := strings.NewReplacer(" ", "", "\t", "", "\n", "", "`", "")
	return strings.ToLower(replacer.Replace(clause))
}

func (s *Store) execReconciliationUpdate(ctx context.Context, probe string, stmt string, contextLabel string) (bool, error) {
	var matched int
	if err := s.db.QueryRowContext(ctx, probe).Scan(&matched); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("%s: probe rows: %w", contextLabel, err)
	}
	if _, err := s.db.ExecContext(ctx, stmt); err != nil {
		return false, fmt.Errorf("%s: %w", contextLabel, err)
	}
	return true, nil
}

func execIgnoreAlreadyExists(ctx context.Context, db *sql.DB, stmt string) (bool, error) {
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		normalized := strings.ToLower(err.Error())
		if strings.Contains(normalized, "already exists") || strings.Contains(normalized, "duplicate column") || strings.Contains(normalized, "duplicate key name") {
			return false, nil
		}
		return false, fmt.Errorf("migrate schema: %w", err)
	}
	return true, nil
}
