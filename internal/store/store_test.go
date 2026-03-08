package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestStoreCreateEpicAndRelations(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "links.db"), "test-workspace-id")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer st.Close()
	epic, err := st.CreateIssue(ctx, CreateIssueInput{Title: "Renderer cleanup", IssueType: "epic", Priority: 1})
	if err != nil {
		t.Fatalf("CreateIssue epic error = %v", err)
	}
	child, err := st.CreateIssue(ctx, CreateIssueInput{Title: "Move pass validation", IssueType: "task", Priority: 2})
	if err != nil {
		t.Fatalf("CreateIssue child error = %v", err)
	}
	related, err := st.CreateIssue(ctx, CreateIssueInput{Title: "Guard shared buffers", IssueType: "feature", Priority: 2})
	if err != nil {
		t.Fatalf("CreateIssue related error = %v", err)
	}
	if _, err := st.AddRelation(ctx, AddRelationInput{SrcID: child.ID, DstID: epic.ID, Type: "parent-child", CreatedBy: "tester"}); err != nil {
		t.Fatalf("AddRelation parent-child error = %v", err)
	}
	if _, err := st.AddRelation(ctx, AddRelationInput{SrcID: child.ID, DstID: related.ID, Type: "blocks", CreatedBy: "tester"}); err != nil {
		t.Fatalf("AddRelation blocks error = %v", err)
	}
	if _, err := st.AddRelation(ctx, AddRelationInput{SrcID: child.ID, DstID: related.ID, Type: "related-to", CreatedBy: "tester"}); err != nil {
		t.Fatalf("AddRelation related-to error = %v", err)
	}
	if _, err := st.AddComment(ctx, AddCommentInput{IssueID: child.ID, Body: "Need compile boundary first.", CreatedBy: "tester"}); err != nil {
		t.Fatalf("AddComment error = %v", err)
	}
	detail, err := st.GetIssueDetail(ctx, child.ID)
	if err != nil {
		t.Fatalf("GetIssueDetail error = %v", err)
	}
	if detail.Parent == nil || detail.Parent.ID != epic.ID {
		t.Fatalf("parent = %#v, want %s", detail.Parent, epic.ID)
	}
	if len(detail.DependsOn) != 1 || detail.DependsOn[0].ID != related.ID {
		t.Fatalf("depends_on = %#v, want %s", detail.DependsOn, related.ID)
	}
	if len(detail.Related) != 1 || detail.Related[0].ID != related.ID {
		t.Fatalf("related = %#v, want %s", detail.Related, related.ID)
	}
	if len(detail.Comments) != 1 {
		t.Fatalf("comments len = %d, want 1", len(detail.Comments))
	}
	export, err := st.Export(ctx)
	if err != nil {
		t.Fatalf("Export error = %v", err)
	}
	if export.WorkspaceID != "test-workspace-id" {
		t.Fatalf("workspace_id = %q", export.WorkspaceID)
	}
	if len(export.Issues) != 3 {
		t.Fatalf("issues len = %d, want 3", len(export.Issues))
	}
}

func TestStoreRejectsInvalidIssueType(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "links.db"), "test-workspace-id")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer st.Close()
	if _, err := st.CreateIssue(ctx, CreateIssueInput{Title: "Bad", IssueType: "weird", Priority: 2}); err == nil {
		t.Fatal("expected invalid issue type error")
	}
}
