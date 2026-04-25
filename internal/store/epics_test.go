package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bmf/links-issue-tracker/internal/model"
)

// [LAW:one-type-per-behavior] (links-agent-epic-model-uew.5)
// Epics and leaf issues are distinct types with distinct accessors. Asking
// for an epic via GetIssue, or a leaf via GetEpic, returns NotFoundError —
// the row may exist, but not as the requested kind.
func TestGetIssueRejectsEpicID(t *testing.T) {
	ctx := context.Background()
	st := openIssueStore(t, ctx)

	epic, err := st.CreateIssue(ctx, CreateIssueInput{Title: "Epic X", Topic: "alpha-topic", IssueType: "epic"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	if _, err := st.GetIssue(ctx, epic.ID); err == nil {
		t.Fatalf("GetIssue(%q) returned nil error; want NotFoundError because id is an epic", epic.ID)
	} else {
		var notFound NotFoundError
		if !errors.As(err, &notFound) {
			t.Errorf("GetIssue(%q) error = %v, want NotFoundError", epic.ID, err)
		}
	}
}

func TestGetEpicRejectsLeafID(t *testing.T) {
	ctx := context.Background()
	st := openIssueStore(t, ctx)

	leaf, err := st.CreateIssue(ctx, CreateIssueInput{Title: "Leaf X", Topic: "alpha-topic", IssueType: "task"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	if _, err := st.GetEpic(ctx, leaf.ID); err == nil {
		t.Fatalf("GetEpic(%q) returned nil; want NotFoundError because id is a leaf", leaf.ID)
	} else {
		var notFound NotFoundError
		if !errors.As(err, &notFound) {
			t.Errorf("GetEpic(%q) error = %v, want NotFoundError", leaf.ID, err)
		}
	}
}

func TestListIssuesExcludesEpics(t *testing.T) {
	ctx := context.Background()
	st := openIssueStore(t, ctx)

	epic, err := st.CreateIssue(ctx, CreateIssueInput{Title: "Epic X", Topic: "alpha-topic", IssueType: "epic"})
	if err != nil {
		t.Fatalf("CreateIssue epic: %v", err)
	}
	leaf, err := st.CreateIssue(ctx, CreateIssueInput{Title: "Leaf X", Topic: "alpha-topic", IssueType: "task"})
	if err != nil {
		t.Fatalf("CreateIssue leaf: %v", err)
	}

	issues, err := st.ListIssues(ctx, ListIssuesFilter{})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	for _, issue := range issues {
		if issue.ID == epic.ID {
			t.Errorf("ListIssues returned epic %q; want excluded", epic.ID)
		}
		if issue.IssueType == "epic" {
			t.Errorf("ListIssues returned issue_type=epic id=%q", issue.ID)
		}
	}
	found := false
	for _, issue := range issues {
		if issue.ID == leaf.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("ListIssues missing leaf %q", leaf.ID)
	}
}

func TestGetEpicDetailReturnsLeafChildrenOnly(t *testing.T) {
	ctx := context.Background()
	st := openIssueStore(t, ctx)

	epic, err := st.CreateIssue(ctx, CreateIssueInput{Title: "Container", Topic: "alpha-topic", IssueType: "epic"})
	if err != nil {
		t.Fatalf("CreateIssue epic: %v", err)
	}
	openLeaf, err := st.CreateIssue(ctx, CreateIssueInput{Title: "Open leaf", Topic: "alpha-topic", IssueType: "task", ParentID: epic.ID})
	if err != nil {
		t.Fatalf("CreateIssue openLeaf: %v", err)
	}
	doneLeaf, err := st.CreateIssue(ctx, CreateIssueInput{Title: "Done leaf", Topic: "alpha-topic", IssueType: "task", ParentID: epic.ID})
	if err != nil {
		t.Fatalf("CreateIssue doneLeaf: %v", err)
	}
	if _, err := st.TransitionIssue(ctx, TransitionIssueInput{IssueID: doneLeaf.ID, Action: "start", CreatedBy: "tester"}); err != nil {
		t.Fatalf("start doneLeaf: %v", err)
	}
	if _, err := st.TransitionIssue(ctx, TransitionIssueInput{IssueID: doneLeaf.ID, Action: "done", CreatedBy: "tester"}); err != nil {
		t.Fatalf("done doneLeaf: %v", err)
	}

	detail, err := st.GetEpicDetail(ctx, epic.ID)
	if err != nil {
		t.Fatalf("GetEpicDetail: %v", err)
	}
	if detail.Epic.ID != epic.ID {
		t.Errorf("Epic.ID = %q, want %q", detail.Epic.ID, epic.ID)
	}
	if len(detail.Children) != 2 {
		t.Fatalf("Children len = %d, want 2; got %#v", len(detail.Children), detail.Children)
	}

	progress := detail.Progress()
	if progress.Total != 2 {
		t.Errorf("Progress.Total = %d, want 2", progress.Total)
	}
	if progress.Closed != 1 {
		t.Errorf("Progress.Closed = %d, want 1", progress.Closed)
	}
	if progress.Open != 1 {
		t.Errorf("Progress.Open = %d, want 1", progress.Open)
	}

	// Sanity: verify that the open child is the one we left open.
	for _, child := range detail.Children {
		if child.ID == openLeaf.ID && child.Status != "open" {
			t.Errorf("openLeaf status = %q, want open", child.Status)
		}
	}
}

// [LAW:one-type-per-behavior]
// Status transitions on epics emit a clear, type-aware error rather than
// "issue not found" or silently mutating a meaningless column.
func TestTransitionRejectsEpic(t *testing.T) {
	ctx := context.Background()
	st := openIssueStore(t, ctx)

	epic, err := st.CreateIssue(ctx, CreateIssueInput{Title: "Container", Topic: "alpha-topic", IssueType: "epic"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	for _, action := range []string{"start", "done", "close", "reopen"} {
		_, err := st.TransitionIssue(ctx, TransitionIssueInput{IssueID: epic.ID, Action: action, CreatedBy: "tester"})
		if err == nil {
			t.Errorf("TransitionIssue(%q) on epic returned nil error; want rejection", action)
			continue
		}
		if !strings.Contains(err.Error(), "epic") {
			t.Errorf("TransitionIssue(%q) error = %q, want message mentioning epic", action, err.Error())
		}
	}
}

// IssueDetail.Parent is now *Epic. A leaf with an epic parent has Parent
// populated; a parentless leaf has Parent=nil.
func TestGetIssueDetailParentIsEpic(t *testing.T) {
	ctx := context.Background()
	st := openIssueStore(t, ctx)

	epic, err := st.CreateIssue(ctx, CreateIssueInput{Title: "Epic", Topic: "alpha-topic", IssueType: "epic"})
	if err != nil {
		t.Fatalf("CreateIssue epic: %v", err)
	}
	leaf, err := st.CreateIssue(ctx, CreateIssueInput{Title: "Leaf", Topic: "alpha-topic", IssueType: "task", ParentID: epic.ID})
	if err != nil {
		t.Fatalf("CreateIssue leaf: %v", err)
	}

	detail, err := st.GetIssueDetail(ctx, leaf.ID)
	if err != nil {
		t.Fatalf("GetIssueDetail: %v", err)
	}
	if detail.Parent == nil {
		t.Fatalf("Parent is nil; want *Epic for %q", epic.ID)
	}
	if detail.Parent.ID != epic.ID {
		t.Errorf("Parent.ID = %q, want %q", detail.Parent.ID, epic.ID)
	}
	// Type-correctness: detail.Parent is *model.Epic, has no Status field.
	// This is a compile-time guarantee; the test exists to lock the type.
	var _ *model.Epic = detail.Parent
}
