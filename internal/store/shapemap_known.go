package store

import (
	"io/fs"
	"regexp"

	"github.com/bmf/links-issue-tracker/internal/store/migrations"
)

// This file holds the knowledge that changes as the schema's history grows:
// the correspondence between historical source-column names and domain fields,
// and the record of which columns past migrations intentionally removed.
// shapemap.go (the type and the applier) does not change when a new shape is
// learned; this file does. They are split by change-reason.

// DeterministicMap proposes a total ShapeMapping for a dump whose every column
// it recognizes, or returns ok=false when the dump carries a column or table
// outside its vocabulary — in which case the LLM mapper (wired in the loop
// ticket) takes over.
//
// [LAW:one-type-per-behavior] There are not separate "clean-ahead" and
// "pre-goose" mappers. They are the same fold over one correspondence table;
// the shapes differ only in which entries fire — clean-ahead needs no aliases,
// pre-goose exercises prompt/assignee aliases and the legacy-status transform.
// [LAW:dataflow-not-control-flow] The status column carries the legacy
// transform unconditionally because that transform is idempotent on canonical
// values, so no "is this pre-goose?" branch is needed.
func DeterministicMap(dump RawDump) (ShapeMapping, bool) {
	out := ShapeMapping{Columns: map[ColumnRef]Disposition{}}
	for _, table := range dump.Tables {
		rules, isDomain := knownSourceColumns[table.Name]
		_, isBookkeeping := bookkeepingTables[table.Name]
		for _, col := range table.Columns {
			ref := ColumnRef{Table: table.Name, Column: col}
			switch {
			case isBookkeeping:
				provenance, reason := classifyDrop(table.Name, col)
				out.Columns[ref] = Dropped{Provenance: provenance, Reason: reason}
			case isDomain:
				disp, known := rules[col]
				if !known {
					// A domain table carrying a column we don't recognize is
					// exactly the novel-shape case: decline so the loop's LLM
					// mapper can reason about it rather than guess here.
					return ShapeMapping{}, false
				}
				out.Columns[ref] = disp
			default:
				return ShapeMapping{}, false
			}
		}
	}
	return out, true
}

func mapTo(target TargetKey, t Transform) Disposition { return MappedTo{Target: target, Transform: t} }

// keep maps a source column straight onto a domain field, no value conversion.
func keep(target TargetKey) Disposition { return mapTo(target, TransformIdentity) }

// knownSourceColumns is the correspondence table: per domain source table, the
// disposition of each source column name the deterministic mapper recognizes.
// Aliases (a v1 name and its pre-goose predecessor) point at the same domain
// field; only one is present in any given dump, so they never collide.
var knownSourceColumns = map[string]map[string]Disposition{
	"issues": {
		"id":           keep("issues.id"),
		"title":        keep("issues.title"),
		"description":  keep("issues.description"),
		"agent_prompt": keep("issues.prompt"),                         // v1 name
		"prompt":       keep("issues.prompt"),                         // pre-goose, pre-rename
		"status":       mapTo("issues.status", TransformLegacyStatus), // legacy vocab → canonical (idempotent)
		"priority":     keep("issues.priority"),                       // legacy out-of-range clamped at the import boundary
		"issue_type":   keep("issues.issue_type"),
		"topic":        keep("issues.topic"),
		"assignee":     keep("issues.assignee"),
		"created_at":   keep("issues.created_at"),
		"updated_at":   keep("issues.updated_at"),
		"closed_at":    keep("issues.closed_at"),
		"archived_at":  keep("issues.archived_at"),
		"deleted_at":   keep("issues.deleted_at"),
		"item_rank":    keep("issues.rank"), // v1 name
	},
	"relations": {
		"src_id":     keep("relations.src_id"),
		"dst_id":     keep("relations.dst_id"),
		"type":       keep("relations.type"),
		"created_at": keep("relations.created_at"),
		"created_by": keep("relations.created_by"),
	},
	"comments": {
		"id":         keep("comments.id"),
		"issue_id":   keep("comments.issue_id"),
		"body":       keep("comments.body"),
		"created_at": keep("comments.created_at"),
		"created_by": keep("comments.created_by"),
	},
	"labels": {
		"issue_id":   keep("labels.issue_id"),
		"label":      keep("labels.name"),
		"created_at": keep("labels.created_at"),
		"created_by": keep("labels.created_by"),
	},
	"issue_events": {
		"id":         keep("events.id"),
		"issue_id":   keep("events.issue_id"),
		"action":     keep("events.action"),
		"reason":     keep("events.reason"),
		"actor":      keep("events.actor"), // v1 name
		"assignee":   keep("events.actor"), // pre-goose, pre-rename
		"created_at": keep("events.created_at"),
	},
	"issue_event_changes": {
		"event_id":   keep("event_changes.event_id"),
		"field":      keep("event_changes.field"),
		"from_value": keep("event_changes.from"),
		"to_value":   keep("event_changes.to"),
	},
}

// bookkeepingTables are the tables a dump carries that have no domain
// representation by design: their columns drop, intended, with the reason
// stated. They are infrastructure, not application data.
var bookkeepingTables = map[string]string{
	"goose_db_version":     "goose migration registry — schema bookkeeping, no domain field",
	"migration_quarantine": "migration quarantine ledger — schema bookkeeping, no domain field",
	"meta":                 "schema metadata table — no domain field",
}

// classifyDrop decides why a source column has no domain target, distinguishing
// the silent-pass case (intended) from the surface-to-human case (unexplained)
// from migration history. [LAW:single-enforcer] One definition of "why was this
// dropped" serves every mapper and the LLM path alike.
//
// A column is an intended drop when its table is bookkeeping (no domain field
// by design) or when a numbered migration explicitly removed it; otherwise the
// drop is unexplained.
func classifyDrop(table, column string) (DropProvenance, string) {
	if reason, ok := bookkeepingTables[table]; ok {
		return DropIntended, reason
	}
	if file, ok := migrationDroppedCols[ColumnRef{Table: table, Column: column}]; ok {
		return DropIntended, "removed by migration " + file
	}
	return DropUnexplained, ""
}

var dropColumnRE = regexp.MustCompile("(?is)ALTER\\s+TABLE\\s+`?(\\w+)`?[^;]*?DROP\\s+COLUMN\\s+(?:IF\\s+EXISTS\\s+)?`?(\\w+)`?")

// parseDroppedColumns extracts every (table, column) a chunk of migration SQL
// explicitly drops. It is a pure function so the "migration history" arm of
// classifyDrop is testable without depending on the current corpus happening to
// contain a drop.
func parseDroppedColumns(sqlText string) []ColumnRef {
	var refs []ColumnRef
	for _, m := range dropColumnRE.FindAllStringSubmatch(sqlText, -1) {
		refs = append(refs, ColumnRef{Table: m[1], Column: m[2]})
	}
	return refs
}

// migrationDroppedCols maps every column a numbered migration removed to the
// file that removed it. It is scanned once from the embedded goose corpus. The
// pre-goose renames (prompt→agent_prompt, assignee→actor) are not drops — the
// values survive under a new name, so the correspondence table maps them. A
// genuine future DROP COLUMN migration is the only way a domain column becomes
// an intended drop.
var migrationDroppedCols = scanMigrationDrops()

func scanMigrationDrops() map[ColumnRef]string {
	out := map[ColumnRef]string{}
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		// The corpus is embedded at build time; a read failure here is an
		// impossible state, not a runtime condition to recover from.
		panic("scan migration drops: read embedded registry: " + err.Error())
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := migrations.FS.ReadFile(entry.Name())
		if err != nil {
			panic("scan migration drops: read " + entry.Name() + ": " + err.Error())
		}
		for _, ref := range parseDroppedColumns(string(data)) {
			out[ref] = entry.Name()
		}
	}
	return out
}
