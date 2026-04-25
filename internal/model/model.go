package model

import "time"

// Issue is a leaf work item — something an agent can claim, work, and
// transition. IssueType discriminates *kinds* of leaves (task, feature,
// bug, chore). Epics live in their own type; see model.Epic.
//
// [LAW:one-type-per-behavior] Leaf issues all have identical lifecycle
// operations (status transitions, assignee, close), so they are one type
// with a kind discriminator. Epics, which lack those operations entirely,
// are a separate type rather than a discriminator branch on Issue.
type Issue struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Status      string     `json:"status"`
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
	// Parent is the containing epic, if any. Leaf-on-leaf parent relations
	// (rare and pathological) are not surfaced here.
	// (links-agent-epic-model-uew.5)
	Parent  *Epic          `json:"parent,omitempty"`
	History []IssueHistory `json:"history"`
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
