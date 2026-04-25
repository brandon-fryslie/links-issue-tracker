package model

import "time"

// Epic is a container for related work. Epics are not workable items: they
// have no status, no assignee, no closed_at, and never transition through
// open/in_progress/closed. Whether the work an epic represents is "done" is
// derivable from its children's statuses, not stored on the epic itself.
//
// [LAW:one-type-per-behavior] Epics and Issues have genuinely different
// operations (transition, claim, close vs. group, browse, navigate), so they
// are distinct types rather than one Issue type with an issue_type
// discriminator branching on every behavior. (links-agent-epic-model-uew.5)
type Epic struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Topic       string     `json:"topic"`
	Priority    int        `json:"priority"`
	Rank        string     `json:"rank"`
	Labels      []string   `json:"labels"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ArchivedAt  *time.Time `json:"archived_at,omitempty"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
}

// EpicDetail is an epic with its associated children, comments, history,
// and raw relations. Children are leaf Issues (parent-child relations from
// other epics are not surfaced here — sub-epic semantics are deferred).
type EpicDetail struct {
	Epic      Epic           `json:"epic"`
	Relations []Relation     `json:"relations"`
	Comments  []Comment      `json:"comments"`
	Children  []Issue        `json:"children"`
	History   []IssueHistory `json:"history"`
}

// EpicProgress summarizes children completion. Used in place of "status" on
// epic display, since epics do not have lifecycle status.
type EpicProgress struct {
	Open       int `json:"open"`
	InProgress int `json:"in_progress"`
	Closed     int `json:"closed"`
	Total      int `json:"total"`
}

// Progress derives a children-completion summary from the epic's live
// children (archived/deleted children are excluded by the store before this
// runs).
func (d EpicDetail) Progress() EpicProgress {
	var p EpicProgress
	for _, child := range d.Children {
		p.Total++
		switch child.Status {
		case "open":
			p.Open++
		case "in_progress":
			p.InProgress++
		case "closed":
			p.Closed++
		}
	}
	return p
}
