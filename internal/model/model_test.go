package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func hydratedIssue(t *testing.T, issue Issue, status State) Issue {
	t.Helper()
	hydrated, err := HydrateOwnedStatus(issue, StatusView{Value: status})
	if err != nil {
		t.Fatalf("HydrateOwnedStatus() error = %v", err)
	}
	return hydrated
}

func TestApplyRefusesContainerEvenWhenMembersAreActionable(t *testing.T) {
	childA := hydratedIssue(t, Issue{ID: "a", IssueType: "task"}, StateOpen)
	childB := hydratedIssue(t, Issue{ID: "b", IssueType: "task"}, StateOpen)
	container, err := HydrateAllOf(Issue{ID: "epic", IssueType: "epic"}, []Issue{childA, childB})
	if err != nil {
		t.Fatalf("HydrateAllOf() error = %v", err)
	}
	_, err = container.Apply(ActionStart, "tester", "")
	if err == nil || err.Error() != "no start action available on this issue" {
		t.Fatalf("Apply(start) error = %v, want no start action available", err)
	}
}

func TestIssueJSONRoundTripEpicProducesContainer(t *testing.T) {
	epic, err := HydrateAllOf(Issue{
		ID:        "epic-1",
		Title:     "Container",
		IssueType: "epic",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}, nil)
	if err != nil {
		t.Fatalf("HydrateAllOf() error = %v", err)
	}
	data, err := json.Marshal(epic)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded Issue
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded.IssueType != "epic" {
		t.Fatalf("IssueType = %q, want epic", decoded.IssueType)
	}
	if decoded.Capabilities().Status != nil {
		t.Fatalf("Capabilities().Status = %#v, want nil", decoded.Capabilities().Status)
	}
}

func TestIssueJSONRoundTripLeafPreservesStatusFields(t *testing.T) {
	closedAt := time.Now().UTC()
	leaf, err := HydrateOwnedStatus(Issue{
		ID:        "task-1",
		Title:     "Leaf",
		IssueType: "task",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}, StatusView{Value: StateClosed, Assignee: "dev", ClosedAt: &closedAt})
	if err != nil {
		t.Fatalf("HydrateOwnedStatus() error = %v", err)
	}
	data, err := json.Marshal(leaf)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded Issue
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded.StatusValue() != string(StateClosed) {
		t.Fatalf("StatusValue() = %q, want closed", decoded.StatusValue())
	}
	if decoded.AssigneeValue() != "dev" {
		t.Fatalf("AssigneeValue() = %q, want dev", decoded.AssigneeValue())
	}
	if decoded.ClosedAtValue() == nil || !decoded.ClosedAtValue().Equal(closedAt) {
		t.Fatalf("ClosedAtValue() = %#v, want %s", decoded.ClosedAtValue(), closedAt)
	}
}

func TestIssueJSONRejectsLeafWithoutStatus(t *testing.T) {
	payload := `{"id":"task-1","title":"Leaf","issue_type":"task","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","progress":{"total":1}}`
	var issue Issue
	err := json.Unmarshal([]byte(payload), &issue)
	if err == nil || !strings.Contains(err.Error(), "missing status field on non-epic") {
		t.Fatalf("Unmarshal() error = %v, want missing status field error", err)
	}
}

func TestUnhydratedIssueLifecycleMethodsReturnZeroValues(t *testing.T) {
	issue := Issue{ID: "task-1", IssueType: "task"}
	if issue.State() != "" {
		t.Fatalf("State() = %q, want zero state", issue.State())
	}
	if issue.Progress() != (Progress{}) {
		t.Fatalf("Progress() = %#v, want zero progress", issue.Progress())
	}
	if issue.Capabilities() != (Capabilities{}) {
		t.Fatalf("Capabilities() = %#v, want empty capabilities", issue.Capabilities())
	}
	if actions := issue.AvailableActions(); actions != nil {
		t.Fatalf("AvailableActions() = %#v, want nil", actions)
	}
	if _, err := issue.Apply(ActionStart, "tester", ""); err == nil || !strings.Contains(err.Error(), "has no hydrated lifecycle") {
		t.Fatalf("Apply() error = %v, want hydration error", err)
	}
}

func TestUnhydratedIssueMarshalJSONReturnsError(t *testing.T) {
	_, err := json.Marshal(Issue{ID: "task-1", IssueType: "task"})
	if err == nil || !strings.Contains(err.Error(), "has no hydrated lifecycle") {
		t.Fatalf("Marshal() error = %v, want hydration error", err)
	}
}
