package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/bmf/links-issue-tracker/internal/dbsnapshot"
	"github.com/pressly/goose/v3"
)

// migrationDownForTest, if non-nil, replaces provider.Down(ctx) inside
// applyDownMigrations. Tests use this to drive the loop without needing a
// multi-migration registry or a real failing Down. Parallels
// migrationUpByOneForTest on the forward path.
var migrationDownForTest func(ctx context.Context, provider *goose.Provider) (*goose.MigrationResult, error)

// downgradeSnapshotLabel is the label prefix every downgrade-recovery snapshot
// carries. Distinct from migrationSnapshotLabel so retention budgets for the
// forward (Open) and reverse (user-invoked) paths can evolve independently.
//
// [LAW:single-enforcer] migrate() owns forward convergence at Open and stamps
// snapshots with migrationSnapshotLabel; Downgrade() owns user-invoked reverse
// and stamps with downgradeSnapshotLabel. The IsMigrationSnapshotName predicate
// matches only the former, so migrate()'s prune sweep cannot collect downgrade
// snapshots and vice versa.
const downgradeSnapshotLabel = "lit-downgrade"

// downgradeMigrationFailedError carries the underlying goose error from a Down
// step. It exists so DowngradeRollbackError.Unwrap can reach the original cause
// while preserving the formatted message that names the failing version.
type downgradeMigrationFailedError struct {
	Version int64
	Cause   error
}

func (e *downgradeMigrationFailedError) Error() string {
	return fmt.Sprintf("down-migrate v%d: %v", e.Version, e.Cause)
}

func (e *downgradeMigrationFailedError) Unwrap() error { return e.Cause }

// DowngradeTargetAheadError reports that the requested target is at or above
// the workspace's current version — there is nothing to roll back. The forward
// upgrade path lives on Open; downgrade refuses to impersonate it.
//
// [LAW:types-are-the-program] Distinct refusal causes are distinct types, not
// a kind field on a generic DowngradeError.
type DowngradeTargetAheadError struct {
	Current int64
	Target  int64
}

func (e *DowngradeTargetAheadError) Error() string {
	return fmt.Sprintf(
		"cannot downgrade to v%d: workspace is already at v%d (use the normal forward upgrade path)",
		e.Target, e.Current,
	)
}

// DowngradeBelowBaselineError reports that the requested target sits below the
// embedded baseline. Running Down past baseline drops every table; Downgrade
// refuses before invoking goose so the destructive baseline Down is unreachable
// from this entry point. Recovery is dbsnapshot restore.
type DowngradeBelowBaselineError struct {
	Target int64
}

func (e *DowngradeBelowBaselineError) Error() string {
	return fmt.Sprintf(
		"cannot downgrade past baseline (v%d) — this would destroy the workspace; "+
			"restore from a `dbsnapshot` instead",
		baselineVersion,
	)
}

// DowngradeRollbackError wraps a downgrade failure that occurred after the
// recovery snapshot was taken. Parallel in shape and intent to
// MigrationRollbackError — the operator-facing recovery instruction is the
// same: `lit snapshots restore <name>`.
//
// [LAW:single-enforcer] The recovery-instruction format lives here so every
// downgrade caller sees the same words, mirroring the migrate side.
type DowngradeRollbackError struct {
	Snapshot dbsnapshot.Snapshot
	Cause    error
}

func (e *DowngradeRollbackError) Error() string {
	return fmt.Sprintf(
		"downgrade: %v\n\nthe workspace state before this downgrade is preserved at:\n  %s\n\nto restore, run:\n  lit snapshots restore %s",
		e.Cause, e.Snapshot.Path, e.Snapshot.Name,
	)
}

func (e *DowngradeRollbackError) Unwrap() error { return e.Cause }

// Downgrade reverses migrations to bring the workspace to targetSchemaVersion,
// taking a recovery snapshot first and committing one Dolt commit per reversed
// migration. It is invoked only by the future `lit downgrade` command (ticket
// .4); no Open-path code reaches it.
//
// Refusals (no snapshot taken):
//   - target == current applied: no-op, returns nil.
//   - target > current applied: DowngradeTargetAheadError.
//   - target < baselineVersion: DowngradeBelowBaselineError.
//   - workspace not in phaseManaged: a plain error (no goose log to reverse).
//
// On a down-migration failure after the snapshot is taken, the returned error
// is a DowngradeRollbackError carrying the snapshot name and the literal
// recovery command.
//
// [LAW:single-enforcer] This is the sole reverse-migration boundary; migrate()
// remains untouched and owns only forward convergence. They share primitives
// (newGooseProvider, the snapshotGuard type) but never share control flow.
// [LAW:dataflow-not-control-flow] The same sequence — classify → refuse-or-
// snapshot → loop-Down-and-commit — runs every invocation. Variability lives
// in targetSchemaVersion and the recorded version, not in which stages execute.
// [LAW:one-source-of-truth] goose_db_version remains the applied-state
// authority for both directions; this loop reads it via recordedMigrationVersion
// and lets goose mutate it the same way Up does.
func (s *Store) Downgrade(ctx context.Context, targetSchemaVersion int64) error {
	state, err := s.classifyMigrationState(ctx)
	if err != nil {
		return err
	}
	if state.phase != phaseManaged {
		return fmt.Errorf(
			"downgrade: workspace is not goose-managed (no goose_db_version table); run Open first to adopt or initialize",
		)
	}
	if targetSchemaVersion == state.appliedVersion {
		return nil
	}
	if targetSchemaVersion > state.appliedVersion {
		return &DowngradeTargetAheadError{Current: state.appliedVersion, Target: targetSchemaVersion}
	}
	if targetSchemaVersion < baselineVersion {
		return &DowngradeBelowBaselineError{Target: targetSchemaVersion}
	}

	guard := newSnapshotGuard(
		s.doltRootDir,
		migrationSnapshotsDir(s.doltRootDir),
		formatDowngradeSnapshotLabel(time.Now()),
	)
	snap, err := guard.ensure()
	if err != nil {
		return fmt.Errorf("downgrade: %w", err)
	}

	if err := s.applyDownMigrations(ctx, targetSchemaVersion); err != nil {
		return &DowngradeRollbackError{Snapshot: snap, Cause: err}
	}
	return nil
}

// applyDownMigrations steps the workspace from its recorded version down to
// target, one migration at a time, with one Dolt commit per reversed step.
// Symmetric with applyPendingMigrations: a single goose provider drives the
// loop and commitWorkingSet records each step under the commit lock.
//
// [LAW:single-enforcer] commitWorkingSet acquires the commit lock per step,
// matching the forward path; no second lock-acquisition pattern is introduced.
func (s *Store) applyDownMigrations(ctx context.Context, target int64) error {
	provider, err := newGooseProvider(s.db)
	if err != nil {
		return fmt.Errorf("construct downgrade provider: %w", err)
	}
	for {
		current, err := s.recordedMigrationVersion(ctx)
		if err != nil {
			return err
		}
		if current <= target {
			return nil
		}
		downOne := provider.Down
		if hook := migrationDownForTest; hook != nil {
			downOne = func(ctx context.Context) (*goose.MigrationResult, error) {
				return hook(ctx, provider)
			}
		}
		result, err := downOne(ctx)
		if err != nil {
			if errors.Is(err, goose.ErrNoNextVersion) {
				return nil
			}
			return &downgradeMigrationFailedError{Version: current, Cause: err}
		}
		if result == nil || result.Source == nil {
			return fmt.Errorf("down-migrate v%d: goose returned nil result", current)
		}
		if err := s.commitWorkingSet(ctx, downgradeCommitMessage(result)); err != nil {
			return fmt.Errorf("commit downgrade revert of v%d: %w", result.Source.Version, err)
		}
	}
}

// downgradeCommitMessage is the one-line Dolt commit message for a reversed
// migration: `downgrade: revert v<N> <file>`, symmetric with
// migrationCommitMessage's `migrate: v<N> <file>` shape.
func downgradeCommitMessage(result *goose.MigrationResult) string {
	return fmt.Sprintf("downgrade: revert v%d %s", result.Source.Version, filepath.Base(result.Source.Path))
}

// formatDowngradeSnapshotLabel returns the label used for downgrade-recovery
// snapshots, mirroring formatMigrationSnapshotLabel's shape. The trailing
// timestamp is cosmetic — dbsnapshot.Take encodes take-time in the directory
// name — but makes the label legible in operator listings.
func formatDowngradeSnapshotLabel(t time.Time) string {
	return fmt.Sprintf("%s-%d", downgradeSnapshotLabel, t.UTC().UnixNano())
}
