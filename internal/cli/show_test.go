package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/bmf/links-issue-tracker/internal/store"
)

// [LAW:dataflow-not-control-flow] (links-agent-epic-model-uew.3)
// `lit show <leaf>` inlines its parent's description so an agent reading a
// leaf gets containing-epic context in one call instead of round-tripping
// through `lit show <epic>`. Test asserts content presence, not formatting.
func TestRunShowInlinesParentDescription(t *testing.T) {
	ctx := context.Background()
	ap := newTestCLIApp(t)

	parent, err := ap.Store.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "Containing epic",
		Topic:       "epic-topic",
		IssueType:   "epic",
		Description: "EPIC_DESCRIPTION_MARKER spans multiple\nlines and should appear\ninlined under the parent block.",
		Priority:    1,
	})
	if err != nil {
		t.Fatalf("CreateIssue(parent): %v", err)
	}
	leaf, err := ap.Store.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "Leaf task",
		Topic:       "epic-topic",
		IssueType:   "task",
		Description: "LEAF_DESCRIPTION_MARKER",
		ParentID:    parent.ID,
		Priority:    1,
	})
	if err != nil {
		t.Fatalf("CreateIssue(leaf): %v", err)
	}

	var stdout bytes.Buffer
	if err := runShow(ctx, &stdout, ap, []string{leaf.ID}); err != nil {
		t.Fatalf("runShow: %v", err)
	}
	out := stdout.String()

	if !strings.Contains(out, "EPIC_DESCRIPTION_MARKER") {
		t.Errorf("show output missing parent description marker; output:\n%s", out)
	}
	if !strings.Contains(out, "should appear") {
		t.Errorf("show output truncated parent description across lines; output:\n%s", out)
	}
	if !strings.Contains(out, "LEAF_DESCRIPTION_MARKER") {
		t.Errorf("show output missing leaf description; output:\n%s", out)
	}
	if !strings.Contains(out, parent.ID) {
		t.Errorf("show output missing parent id %q; output:\n%s", parent.ID, out)
	}

	// Parent block precedes the leaf description block so containing context
	// is read first.
	parentIdx := strings.Index(out, "EPIC_DESCRIPTION_MARKER")
	leafIdx := strings.Index(out, "LEAF_DESCRIPTION_MARKER")
	if parentIdx < 0 || leafIdx < 0 || parentIdx >= leafIdx {
		t.Errorf("expected parent description before leaf description; parentIdx=%d leafIdx=%d", parentIdx, leafIdx)
	}
}

func TestRunShowOmitsParentBlockWhenNoParent(t *testing.T) {
	ctx := context.Background()
	ap := newTestCLIApp(t)

	standalone, err := ap.Store.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "Standalone task",
		Topic:       "alpha-topic",
		IssueType:   "task",
		Description: "STANDALONE_DESCRIPTION",
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	var stdout bytes.Buffer
	if err := runShow(ctx, &stdout, ap, []string{standalone.ID}); err != nil {
		t.Fatalf("runShow: %v", err)
	}
	out := stdout.String()

	if strings.Contains(out, "\nparent:\n") {
		t.Errorf("show output included a parent block for orphan issue; output:\n%s", out)
	}
	if !strings.Contains(out, "STANDALONE_DESCRIPTION") {
		t.Errorf("show output missing standalone description; output:\n%s", out)
	}
}
