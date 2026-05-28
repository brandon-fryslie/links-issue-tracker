package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmf/links-issue-tracker/internal/store/migrations"
)

// TestEveryMigrationDownIsExercised proves that every migration in the embedded
// registry has a Down section that not only exists (TestEveryMigrationHasDownSection's
// job, in the migrations package) but actually runs against a real workspace. An
// unexercised Down is worse than no Down — the registry claims invertibility the
// runtime cannot deliver.
//
// For each migration vN in the registry:
//  1. Open a fresh workspace (this applies every Up through registry max).
//  2. Construct a goose provider and DownTo(N-1), exercising vN's Down.
//  3. Assert the schema actually unwound (no error from goose, and for the
//     baseline case, the baseline tables are gone after DownTo(0)).
//
// [LAW:single-enforcer] One runtime gate proves every Down section is
// executable. There is no per-migration test to forget when a new file lands.
//
// [LAW:dataflow-not-control-flow] The same sequence (open → DownTo(version-1)
// → verify) runs for every migration. Variability lives in N and in the
// verification predicate, not in which stages execute.
func TestEveryMigrationDownIsExercised(t *testing.T) {
	registryMax, err := migrations.MaxVersion()
	if err != nil {
		t.Fatalf("MaxVersion() error = %v", err)
	}

	for v := registryMax; v >= migrations.Baseline; v-- {
		v := v
		t.Run(versionTestName(v), func(t *testing.T) {
			ctx := context.Background()
			doltRoot := filepath.Join(t.TempDir(), "dolt")

			st, err := Open(ctx, doltRoot, "test-workspace-id")
			if err != nil {
				t.Fatalf("Open() error = %v", err)
			}
			t.Cleanup(func() { _ = st.Close() })

			provider, err := newGooseProvider(st.db)
			if err != nil {
				t.Fatalf("newGooseProvider() error = %v", err)
			}

			results, err := provider.DownTo(ctx, v-1)
			if err != nil {
				t.Fatalf("DownTo(%d) for migration v%d failed — its `+goose Down` "+
					"section is present (TestEveryMigrationHasDownSection passed) but does not "+
					"actually run against a real workspace: %v", v-1, v, err)
			}
			// At least one migration must have been rolled back — DownTo is a
			// no-op when there is nothing to revert, and a no-op proves nothing
			// about the Down section.
			if len(results) == 0 {
				t.Fatalf("DownTo(%d) for migration v%d returned no results — "+
					"the Down section was not exercised", v-1, v)
			}

			// Baseline case: after DownTo(0) the baseline tables MUST be gone.
			// This is the only place we can verify shape without hard-coding
			// per-migration expectations; for non-baseline migrations the
			// "did Down work" check is "DownTo did not error" plus the next
			// iteration's fresh-open succeeding.
			if v == migrations.Baseline {
				assertBaselineTablesAbsent(t, ctx, st)
			}
		})
	}
}

// assertBaselineTablesAbsent verifies that the baseline tables (parsed from
// the embedded baseline file, the same oracle adoption uses) are no longer
// present after the baseline Down has run. Anchoring against the parsed
// baseline rather than a hand-maintained list keeps this aligned with
// 00001_baseline.sql automatically.
func assertBaselineTablesAbsent(t *testing.T, ctx context.Context, st *Store) {
	t.Helper()
	schema, err := baselineSchema()
	if err != nil {
		t.Fatalf("baselineSchema() error = %v", err)
	}
	var stillPresent []string
	for table := range schema {
		exists, err := st.tableExists(ctx, table)
		if err != nil {
			t.Fatalf("tableExists(%q) error = %v", table, err)
		}
		if exists {
			stillPresent = append(stillPresent, table)
		}
	}
	if len(stillPresent) > 0 {
		t.Fatalf("baseline Down ran without error but these tables survived: %s\n"+
			"00001_baseline.sql's `+goose Down` is incomplete — every CREATE TABLE "+
			"in the Up must have a matching DROP TABLE in the Down.",
			strings.Join(stillPresent, ", "))
	}
}

func versionTestName(v int64) string {
	entries, err := migrations.FS.ReadDir(".")
	if err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
				continue
			}
			if pv, ok := migrations.ParseVersion(e.Name()); ok && pv == v {
				return e.Name()
			}
		}
	}
	return fmt.Sprintf("v%d", v)
}
