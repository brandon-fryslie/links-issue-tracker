package model

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/bmf/links-issue-tracker/internal/lifecycle"
)

type State = lifecycle.State
type Progress = lifecycle.Progress
type ActionName = lifecycle.ActionName

const (
	StateOpen       = lifecycle.Open
	StateInProgress = lifecycle.InProgress
	StateClosed     = lifecycle.Closed

	ActionStart  = lifecycle.ActionStart
	ActionDone   = lifecycle.ActionDone
	ActionClose  = lifecycle.ActionClose
	ActionReopen = lifecycle.ActionReopen
)

// [LAW:one-type-per-behavior] Issues and epics are one record type; lifecycle
// capability data carries the behavior distinction without splitting shared
// issue behavior across duplicate types.
type Issue struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Priority    int        `json:"priority"`
	IssueType   string     `json:"issue_type"`
	Topic       string     `json:"topic"`
	Rank        string     `json:"rank"`
	Labels      []string   `json:"labels"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ArchivedAt  *time.Time `json:"archived_at,omitempty"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`

	lifecycle lifecycle.Lifecycle
}

func (i Issue) State() State {
	return State(i.lifecycleOrDefault().State())
}

func (i Issue) Progress() Progress {
	return i.lifecycleOrDefault().Progress()
}

func (i Issue) Capabilities() Capabilities {
	return capabilitiesFrom(i.lifecycle)
}

func (i Issue) AvailableActions() []ActionName {
	var out []ActionName
	for _, actionable := range lifecycle.Actionables(i.lifecycle) {
		out = append(out, actionable.AvailableActions()...)
	}
	return out
}

func (i Issue) Apply(action ActionName, actor string, reason string) (Issue, error) {
	actionables := lifecycle.Actionables(i.lifecycle)
	var matches []lifecycle.Actionable
	for _, actionable := range actionables {
		for _, available := range actionable.AvailableActions() {
			if available == lifecycle.ActionName(action) {
				matches = append(matches, actionable)
			}
		}
	}
	if len(matches) == 0 {
		return Issue{}, fmt.Errorf("no %s action available on this issue", action)
	}
	if len(matches) > 1 {
		return Issue{}, fmt.Errorf("ambiguous %s action available on this issue", action)
	}
	next, err := matches[0].Apply(lifecycle.ActionName(action), actor, reason)
	if err != nil {
		return Issue{}, err
	}
	i.SetLifecycle(next)
	return i, nil
}

// SetLifecycle is the store/model boundary for synthesized lifecycle data.
// [LAW:single-enforcer] Store hydration is the only caller that turns persisted
// columns and relations into an Issue lifecycle expression.
func (i *Issue) SetLifecycle(l lifecycle.Lifecycle) {
	i.lifecycle = l
}

func (i Issue) Lifecycle() lifecycle.Lifecycle {
	return i.lifecycleOrDefault()
}

func (i Issue) StatusValue() string {
	status := i.Capabilities().Status
	if status == nil {
		return ""
	}
	return string(status.Value)
}

func (i Issue) AssigneeValue() string {
	status := i.Capabilities().Status
	if status == nil {
		return ""
	}
	return status.Assignee
}

func (i Issue) ClosedAtValue() *time.Time {
	status := i.Capabilities().Status
	if status == nil {
		return nil
	}
	return cloneTime(status.ClosedAt)
}

func (i Issue) WithAssignee(assignee string) Issue {
	status := i.Capabilities().Status
	if status == nil {
		return i
	}
	i.SetLifecycle(lifecycle.OwnedStatus{
		Value:    lifecycle.State(status.Value),
		Assignee: assignee,
		ClosedAt: cloneTime(status.ClosedAt),
	})
	return i
}

func (i Issue) WithStatus(value State, assignee string, closedAt *time.Time) Issue {
	i.SetLifecycle(lifecycle.OwnedStatus{
		Value:    lifecycle.State(value),
		Assignee: assignee,
		ClosedAt: cloneTime(closedAt),
	})
	return i
}

func (i Issue) lifecycleOrDefault() lifecycle.Lifecycle {
	if i.lifecycle != nil {
		return i.lifecycle
	}
	return lifecycle.OwnedStatus{Value: lifecycle.Open}
}

type issueJSON struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Status      *State     `json:"status,omitempty"`
	Priority    int        `json:"priority"`
	IssueType   string     `json:"issue_type"`
	Topic       string     `json:"topic"`
	Assignee    string     `json:"assignee,omitempty"`
	Rank        string     `json:"rank"`
	Labels      []string   `json:"labels"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ClosedAt    *time.Time `json:"closed_at,omitempty"`
	ArchivedAt  *time.Time `json:"archived_at,omitempty"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
	Progress    Progress   `json:"progress"`
}

func (i Issue) MarshalJSON() ([]byte, error) {
	caps := i.Capabilities()
	var statusValue *State
	var assignee string
	var closedAt *time.Time
	if caps.Status != nil {
		value := caps.Status.Value
		statusValue = &value
		assignee = caps.Status.Assignee
		closedAt = cloneTime(caps.Status.ClosedAt)
	}
	return json.Marshal(issueJSON{
		ID:          i.ID,
		Title:       i.Title,
		Description: i.Description,
		Status:      statusValue,
		Priority:    i.Priority,
		IssueType:   i.IssueType,
		Topic:       i.Topic,
		Assignee:    assignee,
		Rank:        i.Rank,
		Labels:      i.Labels,
		CreatedAt:   i.CreatedAt,
		UpdatedAt:   i.UpdatedAt,
		ClosedAt:    closedAt,
		ArchivedAt:  i.ArchivedAt,
		DeletedAt:   i.DeletedAt,
		Progress:    i.Progress(),
	})
}

func (i *Issue) UnmarshalJSON(data []byte) error {
	var payload issueJSON
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	*i = Issue{
		ID:          payload.ID,
		Title:       payload.Title,
		Description: payload.Description,
		Priority:    payload.Priority,
		IssueType:   payload.IssueType,
		Topic:       payload.Topic,
		Rank:        payload.Rank,
		Labels:      payload.Labels,
		CreatedAt:   payload.CreatedAt,
		UpdatedAt:   payload.UpdatedAt,
		ArchivedAt:  payload.ArchivedAt,
		DeletedAt:   payload.DeletedAt,
	}
	if payload.Status != nil {
		i.SetLifecycle(lifecycle.OwnedStatus{
			Value:    lifecycle.State(*payload.Status),
			Assignee: payload.Assignee,
			ClosedAt: cloneTime(payload.ClosedAt),
		})
	}
	return nil
}

type Relation struct {
	SrcID     string    `json:"src_id"`
	DstID     string    `json:"dst_id"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by"`
}

type Comment struct {
	ID        string    `json:"id"`
	IssueID   string    `json:"issue_id"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by"`
}

type Label struct {
	IssueID   string    `json:"issue_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by"`
}

type IssueHistory struct {
	ID         string    `json:"id"`
	IssueID    string    `json:"issue_id"`
	Action     string    `json:"action"`
	Reason     string    `json:"reason"`
	FromStatus string    `json:"from_status"`
	ToStatus   string    `json:"to_status"`
	CreatedAt  time.Time `json:"created_at"`
	CreatedBy  string    `json:"created_by"`
}

type IssueDetail struct {
	Issue     Issue          `json:"issue"`
	Relations []Relation     `json:"relations"`
	Comments  []Comment      `json:"comments"`
	Children  []Issue        `json:"children"`
	DependsOn []Issue        `json:"depends_on"`
	Related   []Issue        `json:"related"`
	Blocks    []Issue        `json:"blocks"`
	Parent    *Issue         `json:"parent,omitempty"`
	History   []IssueHistory `json:"history"`
}

type Export struct {
	Version     int            `json:"version"`
	WorkspaceID string         `json:"workspace_id"`
	ExportedAt  time.Time      `json:"exported_at"`
	Issues      []Issue        `json:"issues"`
	Relations   []Relation     `json:"relations"`
	Comments    []Comment      `json:"comments"`
	Labels      []Label        `json:"labels"`
	History     []IssueHistory `json:"history"`
}
