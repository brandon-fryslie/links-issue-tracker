# Migration architecture

The contract every `lit` migration follows. Owned by `internal/store/schema.go`. Read this before adding a new migration; the rules here are tighter than the surrounding code.

## Motivation

Pre-r5v9.2, every `Open()` ran the full reconciliation pass: ~25 SQL queries' worth of `CREATE TABLE IF NOT EXISTS`, probe-gated `ALTER`s, and `execReconciliationUpdate` calls. Every CLI invocation paid this cost — even when nothing needed to change. PR #119 made it worse (30-50 queries plus a goose Provider construction); the rewrite documented in epic `links-schema-rebuild-r5v9` reverted that and started over from first principles. This document captures the resulting contract.

The whole point is one sentence: **the workspace records its schema version, the binary declares the version it knows, and the comparison decides whether anything needs to happen.** Reconciliation is a slow path entered by exception, not a fast path entered by default.

## The stamp

Two stakes name the system's state:

| Stake | Where | What it says |
|---|---|---|
| `meta.schema_version` row | Workspace's dolt database | "This workspace's schema is at version N" |
| `store.CurrentSchemaVersion` | Compile-time constant in the binary | "This binary knows how to produce version N" |

Both stakes are unsigned integers. The workspace stake lives in the existing `meta` table — single source of truth for workspace-level metadata. The binary stake is a Go constant edited only when a new migration lands.

## Path selection

`migrate()` reads the workspace stamp and compares it to `CurrentSchemaVersion`. The comparison is the only branch in the system; everything downstream is data-driven from there.

| Workspace stamp | Binary stake | Path |
|---|---|---|
| Equal to `CurrentSchemaVersion` | — | Fast path |
| Empty (fresh database) | — | Slow path → stamp written = `CurrentSchemaVersion` |
| Less than `CurrentSchemaVersion` | — | Slow path (forward migration) → stamp bumped = `CurrentSchemaVersion` |
| Greater than `CurrentSchemaVersion` | — | Workspace was written by a newer binary. Slow path runs idempotent reconciliation as a safety net but does **not** touch the stamp. Ticket r5v9.4 will turn this into a hard refusal once compat-window enforcement lands. |

The comparison itself is `int` against `int`. The stamp string is parsed with `strconv.Atoi`; an unparseable stamp is treated as "absent" and routed to the slow path (which will then write a clean stamp).

## Fast path

The fast path is the steady-state cost of `Open()`. It runs exactly three SQL queries:

1. **`SELECT meta_value FROM meta WHERE meta_key = 'schema_version'`** — read the stamp.
2. **`SELECT meta_value FROM meta WHERE meta_key = 'workspace_id'`** — confirm `workspace_id` (the helper writes only when the value differs, so steady-state is read-only).
3. **`SELECT 1 FROM issues LIMIT 1`** — the smoke probe. Mechanically verifies that the canonical leaf table exists and is reachable through the current connection. Fails loudly with a clear schema error rather than letting a downstream `lit ls` blow up with a cryptic table-missing error.

The fast path runs no DDL. It does not touch `information_schema`. It does not call `execReconciliationUpdate`. If a future smoke turns out to need more, lift it into a single batched query, never into a probe loop.

## Slow path

The slow path is where reconciliation lives. It is entered when the stamp tells us reconciliation is needed (empty, lower, or — for the safety-net case — higher).

Sequence:

1. *(Reserved for r5v9.5)* — take a `dbsnapshot.Take` snapshot **before** any mutation. The commit lock is already held by the caller (`Open` acquires it before invoking `migrate`), so snapshot capture is coherent.
2. **Apply reconciliation.** Every step is idempotent: `CREATE TABLE IF NOT EXISTS`, probe-gated `ALTER`, `execReconciliationUpdate`. Re-running on a workspace already at the target version is a no-op at the storage layer.
3. **Write the stamp.** `setMeta('schema_version', CurrentSchemaVersion)` — but only when the stamp is empty or lower than the binary stake. A higher stamp is left alone (binary is older than the workspace; preserve forward-compat until r5v9.4 lands).
4. **`commitWorkingSet`.** All DDL and the stamp write enter the same Dolt commit. Either both are durable or both stay in the working set for the next `Open` to retry.

## Atomicity guarantees

DDL in MySQL/Dolt implicitly commits any open SQL transaction, so true SQL-level atomicity across schema changes is impossible. Atomicity is instead enforced at the Dolt commit boundary:

- **Successful slow path** → all DDL + stamp write reach one Dolt commit (`commitWorkingSet`).
- **Failure before stamp write** → DDL changes sit in the working set, stamp unchanged. Next `Open` re-enters the slow path (stamp still mismatched) and reconciliation re-runs idempotently.
- **Failure after stamp write but before `commitWorkingSet`** → working set carries both the DDL and the stamp; queries see the new stamp, but the Dolt commit is missing. Ticket r5v9.5 closes this window by taking a snapshot at slow-path entry and restoring on any post-stamp failure. Until then, this window exists.

The stamp write is the **last** write before `commitWorkingSet` precisely so the failure window is minimal.

## Adding a new migration

A new migration is exactly four steps:

1. **Bump `CurrentSchemaVersion`.** Change the integer constant in `internal/store/schema.go`. This is the only edit that says "this binary now requires version N."
2. **Add reconciliation logic.** Append the DDL/data-fixup steps to the slow path's reconciliation block. Use `execIgnoreAlreadyExists` for idempotent DDL and `execReconciliationUpdate` for data fixups. Existing workspaces stamped at the previous version will pick up the new step on next `Open`.
3. **Verify idempotence.** The reconciliation must be safe to re-run on a workspace already at the new version. The existing tests (`TestMigrationIsIdempotentOnSecondOpen` and the schema-rebuild query-count tests) guard this.
4. **Update or extend tests.** If the new migration adds a column, table, or constraint, add a test that observes the new shape after `Open`. The query-count test does not need to change — fast-path cost is invariant to the number of historical migrations.

Do **not** introduce per-version migration files, registries, or numbered changeset directories. The slow path is a single function; new logic appends to it. Idempotence is the only contract.

## What this design does not do

- Does not re-introduce goose, changeset registries, plugin systems, or any external migration framework.
- Does not maintain a migration audit log table inside the workspace database. (`issue_events` exists for issue-level mutations only.)
- Does not take a snapshot on every `Open` — only on slow-path entry, and only once r5v9.5 lands.
- Does not enforce binary/workspace compat-window. r5v9.4 owns that and inserts a check between the stamp read and path selection.
- Does not restore from snapshot on migration failure. r5v9.5 owns that.
- Does not auto-revert via Dolt branching or quarantine tables. Snapshot-and-restore is the recovery model.

## Forward path

- **r5v9.3** — Canonical schema reconcile as one idempotent pass. Folds the existing reconciliation steps into a single coherent block that the slow path calls.
- **r5v9.4** — Compat-window enforcement. Refuses `Open` when the workspace stamp is outside `[MinSupportedSchemaVersion, CurrentSchemaVersion]`.
- **r5v9.5** — Forward migration application + snapshot-based recovery. Wires `dbsnapshot.Take` at slow-path entry and `dbsnapshot.Restore` (operator-invoked, with workspace-exclusivity caveats from r5v9.7) for failure recovery.
- **r5v9.6** — Optional file-based migration audit trail external to the workspace database.
- **r5v9.7** — Workspace-exclusivity lock for `dbsnapshot.Restore` concurrent-reader safety.

## References

- PR #119 — the migration system this epic replaced (reverted).
- PR #120 — the never-merged "heal" attempt that documented why the goose-based design failed.
- PR #121 (r5v9.1) — `internal/dbsnapshot` (`Take`/`List`/`Restore`/`Prune`). Slow-path entry will call `Take`; operator-invoked restore exists but is not called by the slow path.
- `internal/store/commit_lock.go` — the writer-serialization mechanism the slow path runs under.
