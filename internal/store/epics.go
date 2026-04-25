package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bmf/links-issue-tracker/internal/model"
)

// PeekIssueType returns the issue_type of the row identified by id, or
// NotFoundError if no such row exists. Cheap (single-column lookup) and
// used by callers that need to dispatch between leaf and epic handling
// without paying for a full detail fetch up front.
func (s *Store) PeekIssueType(ctx context.Context, id string) (string, error) {
	var issueType string
	err := s.db.QueryRowContext(ctx, `SELECT issue_type FROM issues WHERE id = ?`, id).Scan(&issueType)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", NotFoundError{Entity: "record", ID: id}
		}
		return "", err
	}
	return issueType, nil
}

// epicSelectColumns lists the columns scanned into model.Epic. Epics share
// the issues table with leaf issues but do not project status, assignee, or
// closed_at — those fields don't exist on Epic by design.
const epicSelectColumns = `i.id, i.title, i.description, i.priority, i.topic, i.item_rank, i.created_at, i.updated_at, i.archived_at, i.deleted_at`

// ListEpicsFilter mirrors ListIssuesFilter but with epic-relevant fields only.
type ListEpicsFilter struct {
	Topics          []string
	IDs             []string
	IncludeArchived bool
	IncludeDeleted  bool
	SortBy          []SortSpec
	Limit           int
}

// GetEpic returns the epic identified by id. NotFoundError is returned if
// no row exists with that id, or if the row exists but is not an epic —
// from the consumer's perspective, "an epic with this id does not exist."
func (s *Store) GetEpic(ctx context.Context, id string) (model.Epic, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+epicSelectColumns+` FROM issues i WHERE id = ? AND issue_type = 'epic'`, id)
	epic, err := scanEpic(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Epic{}, NotFoundError{Entity: "epic", ID: id}
		}
		return model.Epic{}, err
	}
	labels, err := s.attachEpicLabels(ctx, []model.Epic{epic})
	if err != nil {
		return model.Epic{}, err
	}
	return labels[0], nil
}

// GetEpicDetail returns the epic plus its children, comments, history, and
// raw relations. Children are filtered to live (non-archived, non-deleted)
// non-epic issues — sub-epic relationships are intentionally not modeled
// here yet.
func (s *Store) GetEpicDetail(ctx context.Context, id string) (model.EpicDetail, error) {
	epic, err := s.GetEpic(ctx, id)
	if err != nil {
		return model.EpicDetail{}, err
	}
	relations, err := s.listRelations(ctx, id)
	if err != nil {
		return model.EpicDetail{}, err
	}
	comments, err := s.listComments(ctx, id)
	if err != nil {
		return model.EpicDetail{}, err
	}
	history, err := s.listHistory(ctx, id)
	if err != nil {
		return model.EpicDetail{}, err
	}
	detail := model.EpicDetail{
		Epic:      epic,
		Relations: relations,
		Comments:  comments,
		History:   history,
		Children:  []model.Issue{},
	}
	for _, rel := range relations {
		if rel.Type != "parent-child" || rel.DstID != id {
			continue
		}
		child, err := s.getIssueRaw(ctx, rel.SrcID)
		if err != nil {
			continue
		}
		// [LAW:dataflow-not-control-flow] Children list always flows through
		// the same filter; non-leaf or archived/deleted children produce no
		// entry rather than triggering a special-case branch.
		if child.IssueType == "epic" || child.ArchivedAt != nil || child.DeletedAt != nil {
			continue
		}
		detail.Children = append(detail.Children, child)
	}
	labeled, err := s.attachLabels(ctx, detail.Children)
	if err != nil {
		return model.EpicDetail{}, err
	}
	detail.Children = labeled
	sortIssuesByRank(detail.Children)
	return detail, nil
}

// ListEpics returns epics matching the filter, ordered by item_rank ASC.
func (s *Store) ListEpics(ctx context.Context, filter ListEpicsFilter) ([]model.Epic, error) {
	query := `SELECT ` + epicSelectColumns + ` FROM issues i`
	where := []string{"i.issue_type = 'epic'"}
	var args []any
	if !filter.IncludeArchived {
		where = append(where, "i.archived_at IS NULL")
	}
	if !filter.IncludeDeleted {
		where = append(where, "i.deleted_at IS NULL")
	}
	if len(filter.Topics) > 0 {
		placeholders := make([]string, 0, len(filter.Topics))
		for _, topic := range filter.Topics {
			trimmed := strings.TrimSpace(topic)
			if trimmed == "" {
				continue
			}
			placeholders = append(placeholders, "?")
			args = append(args, trimmed)
		}
		if len(placeholders) > 0 {
			where = append(where, "i.topic IN ("+strings.Join(placeholders, ",")+")")
		}
	}
	if len(filter.IDs) > 0 {
		placeholders := make([]string, 0, len(filter.IDs))
		for _, id := range filter.IDs {
			trimmed := strings.TrimSpace(id)
			if trimmed == "" {
				continue
			}
			placeholders = append(placeholders, "?")
			args = append(args, trimmed)
		}
		if len(placeholders) > 0 {
			where = append(where, "i.id IN ("+strings.Join(placeholders, ",")+")")
		}
	}
	query += " WHERE " + strings.Join(where, " AND ")
	orderClause, err := buildIssueOrderClause(filter.SortBy)
	if err != nil {
		return nil, err
	}
	query += " ORDER BY " + orderClause
	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", filter.Limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list epics: %w", err)
	}
	defer rows.Close()
	epics := []model.Epic{}
	for rows.Next() {
		epic, err := scanEpic(rows)
		if err != nil {
			return nil, err
		}
		epics = append(epics, epic)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return s.attachEpicLabels(ctx, epics)
}

func scanEpic(row issueScanner) (model.Epic, error) {
	var epic model.Epic
	var createdAt, updatedAt string
	var archivedAt, deletedAt sql.NullString
	if err := row.Scan(&epic.ID, &epic.Title, &epic.Description, &epic.Priority, &epic.Topic, &epic.Rank, &createdAt, &updatedAt, &archivedAt, &deletedAt); err != nil {
		return model.Epic{}, err
	}
	var err error
	epic.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return model.Epic{}, err
	}
	epic.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return model.Epic{}, err
	}
	if archivedAt.Valid {
		t, err := time.Parse(time.RFC3339Nano, archivedAt.String)
		if err != nil {
			return model.Epic{}, err
		}
		epic.ArchivedAt = &t
	}
	if deletedAt.Valid {
		t, err := time.Parse(time.RFC3339Nano, deletedAt.String)
		if err != nil {
			return model.Epic{}, err
		}
		epic.DeletedAt = &t
	}
	return epic, nil
}

// attachEpicLabels mirrors attachLabels for the Epic shape.
func (s *Store) attachEpicLabels(ctx context.Context, epics []model.Epic) ([]model.Epic, error) {
	if len(epics) == 0 {
		return epics, nil
	}
	ids := make([]string, len(epics))
	for i, epic := range epics {
		ids[i] = epic.ID
	}
	labelsByID, err := s.loadLabelsByIssueIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range epics {
		epics[i].Labels = labelsByID[epics[i].ID]
		if epics[i].Labels == nil {
			epics[i].Labels = []string{}
		}
	}
	return epics, nil
}
