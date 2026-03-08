package model

import "time"

// [LAW:one-type-per-behavior] Issues and epics are one record type with
// issue_type carrying the behavior distinction.
type Issue struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Status      string     `json:"status"`
	Priority    int        `json:"priority"`
	IssueType   string     `json:"issue_type"`
	Assignee    string     `json:"assignee,omitempty"`
	Labels      []string   `json:"labels"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ClosedAt    *time.Time `json:"closed_at,omitempty"`
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

type IssueDetail struct {
	Issue     Issue      `json:"issue"`
	Relations []Relation `json:"relations"`
	Comments  []Comment  `json:"comments"`
	Children  []Issue    `json:"children"`
	DependsOn []Issue    `json:"depends_on"`
	Related   []Issue    `json:"related"`
	BlockedBy []Issue    `json:"blocked_by"`
	Parent    *Issue     `json:"parent,omitempty"`
}

type Export struct {
	Version     int        `json:"version"`
	WorkspaceID string     `json:"workspace_id"`
	ExportedAt  time.Time  `json:"exported_at"`
	Issues      []Issue    `json:"issues"`
	Relations   []Relation `json:"relations"`
	Comments    []Comment  `json:"comments"`
	Labels      []Label    `json:"labels"`
}
