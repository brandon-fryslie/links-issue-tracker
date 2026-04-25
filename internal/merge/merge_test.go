package merge

import (
	"testing"
	"time"

	"github.com/bmf/links-issue-tracker/internal/model"
)

func TestThreeWayDetectsPerIssueConflict(t *testing.T) {
	base := model.Export{Issues: []model.Issue{model.Issue{ID: "i1", Title: "issue", Priority: 2, IssueType: "task", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}.WithStatus(model.StateOpen, "", nil)}}
	local := model.Export{Issues: append([]model.Issue(nil), base.Issues...)}
	remote := model.Export{Issues: append([]model.Issue(nil), base.Issues...)}
	local.Issues[0].Title = "local-change"
	remote.Issues[0].Title = "remote-change"

	result := ThreeWay(base, local, remote)
	if len(result.Conflicts) != 1 {
		t.Fatalf("conflicts = %#v", result.Conflicts)
	}
	if result.Conflicts[0].IssueID != "i1" {
		t.Fatalf("issue id = %q", result.Conflicts[0].IssueID)
	}
}

func TestThreeWayMergesNonConflictingIssueChanges(t *testing.T) {
	now := time.Now().UTC()
	base := model.Export{
		WorkspaceID: "ws",
		Issues: []model.Issue{
			model.Issue{ID: "i1", Title: "one", Priority: 2, IssueType: "task", CreatedAt: now, UpdatedAt: now}.WithStatus(model.StateOpen, "", nil),
			model.Issue{ID: "i2", Title: "two", Priority: 2, IssueType: "task", CreatedAt: now, UpdatedAt: now}.WithStatus(model.StateOpen, "", nil),
		},
	}
	local := model.Export{WorkspaceID: base.WorkspaceID, Issues: append([]model.Issue(nil), base.Issues...)}
	local.Issues[0].Title = "local i1"
	remote := model.Export{WorkspaceID: base.WorkspaceID, Issues: append([]model.Issue(nil), base.Issues...)}
	remote.Issues[1].Title = "remote i2"

	result := ThreeWay(base, local, remote)
	if len(result.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts = %#v", result.Conflicts)
	}
	if len(result.Export.Issues) != 2 {
		t.Fatalf("issues = %#v", result.Export.Issues)
	}
	merged := map[string]string{}
	for _, issue := range result.Export.Issues {
		merged[issue.ID] = issue.Title
	}
	if merged["i1"] != "local i1" || merged["i2"] != "remote i2" {
		t.Fatalf("merged titles = %#v", merged)
	}
}
