package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/google/uuid"

	"github.com/bmf/links-issue-tracker/internal/model"
)

const sqliteDriverName = "sqlite"

type Store struct {
	db          *sql.DB
	workspaceID string
}

type SyncState struct {
	Path              string
	ContentHash       string
	WorkspaceRevision int64
}

type ImportIssue struct {
	ID          string
	Title       string
	Description string
	Status      string
	Priority    int
	IssueType   string
	Assignee    string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ClosedAt    *time.Time
	Labels      []string
}

type ImportComment struct {
	ID        string
	IssueID   string
	Body      string
	CreatedAt time.Time
	CreatedBy string
}

type ImportRelation struct {
	SrcID     string
	DstID     string
	Type      string
	CreatedAt time.Time
	CreatedBy string
}

type ImportLabel struct {
	IssueID   string
	Name      string
	CreatedAt time.Time
	CreatedBy string
}

type CreateIssueInput struct {
	Title       string
	Description string
	IssueType   string
	Priority    int
	Assignee    string
	Labels      []string
}

type UpdateIssueInput struct {
	Title       *string
	Description *string
	IssueType   *string
	Status      *string
	Priority    *int
	Assignee    *string
	Labels      *[]string
}

type ListIssuesFilter struct {
	Status        string
	IssueType     string
	Assignee      string
	PriorityMin   *int
	PriorityMax   *int
	SearchTerms   []string
	IDs           []string
	HasComments   *bool
	LabelsAll     []string
	UpdatedAfter  *time.Time
	UpdatedBefore *time.Time
	Limit         int
}

type AddCommentInput struct {
	IssueID   string
	Body      string
	CreatedBy string
}

type AddRelationInput struct {
	SrcID     string
	DstID     string
	Type      string
	CreatedBy string
}

type AddLabelInput struct {
	IssueID   string
	Name      string
	CreatedBy string
}

func Open(ctx context.Context, dbPath string, workspaceID string) (*Store, error) {
	db, err := sql.Open(sqliteDriverName, dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA foreign_keys=ON;",
		"PRAGMA busy_timeout=5000;",
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("apply pragma %q: %w", pragma, err)
		}
	}
	s := &Store{db: db, workspaceID: workspaceID}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	schema := []string{
		`CREATE TABLE IF NOT EXISTS meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS issues (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			priority INTEGER NOT NULL,
			issue_type TEXT NOT NULL,
			assignee TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			closed_at TEXT,
			CHECK(status IN ('open','closed')),
			CHECK(priority >= 0 AND priority <= 4),
			CHECK(issue_type IN ('task','feature','bug','chore','epic'))
		);`,
		`CREATE TABLE IF NOT EXISTS relations (
			src_id TEXT NOT NULL,
			dst_id TEXT NOT NULL,
			type TEXT NOT NULL,
			created_at TEXT NOT NULL,
			created_by TEXT NOT NULL,
			PRIMARY KEY (src_id, dst_id, type),
			FOREIGN KEY (src_id) REFERENCES issues(id) ON DELETE CASCADE,
			FOREIGN KEY (dst_id) REFERENCES issues(id) ON DELETE CASCADE,
			CHECK(type IN ('blocks','parent-child','related-to'))
		);`,
		`CREATE TABLE IF NOT EXISTS comments (
			id TEXT PRIMARY KEY,
			issue_id TEXT NOT NULL,
			body TEXT NOT NULL,
			created_at TEXT NOT NULL,
			created_by TEXT NOT NULL,
			FOREIGN KEY (issue_id) REFERENCES issues(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS labels (
			issue_id TEXT NOT NULL,
			label TEXT NOT NULL,
			created_at TEXT NOT NULL,
			created_by TEXT NOT NULL,
			PRIMARY KEY (issue_id, label),
			FOREIGN KEY (issue_id) REFERENCES issues(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_issues_status_priority ON issues(status, priority, updated_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_relations_src_type ON relations(src_id, type);`,
		`CREATE INDEX IF NOT EXISTS idx_relations_dst_type ON relations(dst_id, type);`,
		`CREATE INDEX IF NOT EXISTS idx_comments_issue_created ON comments(issue_id, created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_labels_issue ON labels(issue_id, label);`,
		`CREATE INDEX IF NOT EXISTS idx_labels_name ON labels(label, issue_id);`,
	}
	for _, stmt := range schema {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate schema: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO meta(key, value) VALUES ('workspace_id', ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, s.workspaceID); err != nil {
		return fmt.Errorf("store workspace_id: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO meta(key, value) VALUES ('schema_version', '1')
		 ON CONFLICT(key) DO NOTHING`); err != nil {
		return fmt.Errorf("store schema_version: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO meta(key, value) VALUES ('workspace_revision', '0')
		 ON CONFLICT(key) DO NOTHING`); err != nil {
		return fmt.Errorf("store workspace_revision: %w", err)
	}
	return nil
}

func (s *Store) GetWorkspaceRevision(ctx context.Context) (int64, error) {
	return s.getWorkspaceRevision(ctx, nil)
}

func (s *Store) GetSyncState(ctx context.Context) (SyncState, error) {
	state := SyncState{}
	var err error
	state.Path, err = s.getMeta(ctx, nil, "last_sync_path")
	if err != nil {
		return SyncState{}, err
	}
	state.ContentHash, err = s.getMeta(ctx, nil, "last_sync_hash")
	if err != nil {
		return SyncState{}, err
	}
	revisionValue, err := s.getMeta(ctx, nil, "last_sync_workspace_revision")
	if err != nil {
		return SyncState{}, err
	}
	if strings.TrimSpace(revisionValue) != "" {
		state.WorkspaceRevision, err = strconv.ParseInt(revisionValue, 10, 64)
		if err != nil {
			return SyncState{}, fmt.Errorf("parse last_sync_workspace_revision: %w", err)
		}
	}
	return state, nil
}

func (s *Store) RecordSyncState(ctx context.Context, state SyncState) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin record sync state tx: %w", err)
	}
	defer tx.Rollback()
	for key, value := range map[string]string{
		"last_sync_path":               strings.TrimSpace(state.Path),
		"last_sync_hash":               strings.TrimSpace(state.ContentHash),
		"last_sync_workspace_revision": strconv.FormatInt(state.WorkspaceRevision, 10),
	} {
		if err := s.setMeta(ctx, tx, key, value); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit record sync state: %w", err)
	}
	return nil
}

func (s *Store) CreateIssue(ctx context.Context, in CreateIssueInput) (model.Issue, error) {
	if strings.TrimSpace(in.Title) == "" {
		return model.Issue{}, errors.New("title is required")
	}
	issueType, err := validateIssueType(in.IssueType)
	if err != nil {
		return model.Issue{}, err
	}
	priority := in.Priority
	if err := validatePriority(priority); err != nil {
		return model.Issue{}, err
	}
	now := time.Now().UTC()
	labels, err := canonicalizeLabels(in.Labels)
	if err != nil {
		return model.Issue{}, err
	}
	issue := model.Issue{
		ID:          newIssueID(s.workspaceID),
		Title:       strings.TrimSpace(in.Title),
		Description: strings.TrimSpace(in.Description),
		Status:      "open",
		Priority:    priority,
		IssueType:   issueType,
		Assignee:    strings.TrimSpace(in.Assignee),
		Labels:      labels,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Issue{}, fmt.Errorf("begin create issue tx: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO issues(
		id, title, description, status, priority, issue_type, assignee, created_at, updated_at, closed_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
		issue.ID, issue.Title, issue.Description, issue.Status, issue.Priority, issue.IssueType,
		issue.Assignee, issue.CreatedAt.Format(time.RFC3339Nano), issue.UpdatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return model.Issue{}, fmt.Errorf("insert issue: %w", err)
	}
	if err := s.replaceLabelsTx(ctx, tx, issue.ID, issue.Labels, "links"); err != nil {
		return model.Issue{}, err
	}
	if _, err := s.bumpWorkspaceRevisionTx(ctx, tx); err != nil {
		return model.Issue{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.Issue{}, fmt.Errorf("commit create issue: %w", err)
	}
	return issue, nil
}

func (s *Store) ListIssues(ctx context.Context, filter ListIssuesFilter) ([]model.Issue, error) {
	query := `SELECT id, title, description, status, priority, issue_type, assignee, created_at, updated_at, closed_at FROM issues`
	var where []string
	var args []any
	if filter.Status != "" {
		where = append(where, "status = ?")
		args = append(args, filter.Status)
	}
	if filter.IssueType != "" {
		where = append(where, "issue_type = ?")
		args = append(args, filter.IssueType)
	}
	if filter.Assignee != "" {
		where = append(where, "assignee = ?")
		args = append(args, filter.Assignee)
	}
	if filter.PriorityMin != nil {
		where = append(where, "priority >= ?")
		args = append(args, *filter.PriorityMin)
	}
	if filter.PriorityMax != nil {
		where = append(where, "priority <= ?")
		args = append(args, *filter.PriorityMax)
	}
	if filter.UpdatedAfter != nil {
		where = append(where, "updated_at >= ?")
		args = append(args, filter.UpdatedAfter.UTC().Format(time.RFC3339Nano))
	}
	if filter.UpdatedBefore != nil {
		where = append(where, "updated_at <= ?")
		args = append(args, filter.UpdatedBefore.UTC().Format(time.RFC3339Nano))
	}
	if filter.HasComments != nil {
		where = append(where, "EXISTS (SELECT 1 FROM comments WHERE comments.issue_id = issues.id) = ?")
		args = append(args, boolToInt(*filter.HasComments))
	}
	if len(filter.LabelsAll) > 0 {
		labels, err := canonicalizeLabels(filter.LabelsAll)
		if err != nil {
			return nil, err
		}
		for _, label := range labels {
			where = append(where, "EXISTS (SELECT 1 FROM labels WHERE labels.issue_id = issues.id AND labels.label = ?)")
			args = append(args, label)
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
			where = append(where, "id IN ("+strings.Join(placeholders, ", ")+")")
		}
	}
	for _, term := range filter.SearchTerms {
		trimmed := strings.ToLower(strings.TrimSpace(term))
		if trimmed == "" {
			continue
		}
		where = append(where, "(LOWER(title) LIKE ? OR LOWER(description) LIKE ?)")
		like := "%" + trimmed + "%"
		args = append(args, like, like)
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY status ASC, priority ASC, updated_at DESC, id ASC"
	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", filter.Limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list issues: %w", err)
	}
	defer rows.Close()
	var issues []model.Issue
	for rows.Next() {
		issue, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}
		issues = append(issues, issue)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return s.attachLabels(ctx, issues)
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *Store) GetIssueDetail(ctx context.Context, id string) (model.IssueDetail, error) {
	issue, err := s.GetIssue(ctx, id)
	if err != nil {
		return model.IssueDetail{}, err
	}
	relations, err := s.listRelations(ctx, id)
	if err != nil {
		return model.IssueDetail{}, err
	}
	comments, err := s.listComments(ctx, id)
	if err != nil {
		return model.IssueDetail{}, err
	}
	detail := model.IssueDetail{
		Issue:     issue,
		Relations: relations,
		Comments:  comments,
		Children:  []model.Issue{},
		DependsOn: []model.Issue{},
		Related:   []model.Issue{},
		BlockedBy: []model.Issue{},
	}
	for _, rel := range relations {
		switch rel.Type {
		case "blocks":
			if rel.SrcID == id {
				dep, err := s.GetIssue(ctx, rel.DstID)
				if err == nil {
					detail.DependsOn = append(detail.DependsOn, dep)
				}
			}
			if rel.DstID == id {
				blocked, err := s.GetIssue(ctx, rel.SrcID)
				if err == nil {
					detail.BlockedBy = append(detail.BlockedBy, blocked)
				}
			}
		case "parent-child":
			if rel.SrcID == id {
				parent, err := s.GetIssue(ctx, rel.DstID)
				if err == nil {
					detail.Parent = &parent
				}
			}
			if rel.DstID == id {
				child, err := s.GetIssue(ctx, rel.SrcID)
				if err == nil {
					detail.Children = append(detail.Children, child)
				}
			}
		case "related-to":
			otherID := rel.SrcID
			if otherID == id {
				otherID = rel.DstID
			}
			related, err := s.GetIssue(ctx, otherID)
			if err == nil {
				detail.Related = append(detail.Related, related)
			}
		}
	}
	labeled, err := s.attachLabels(ctx, detail.Children)
	if err != nil {
		return model.IssueDetail{}, err
	}
	detail.Children = labeled
	labeled, err = s.attachLabels(ctx, detail.DependsOn)
	if err != nil {
		return model.IssueDetail{}, err
	}
	detail.DependsOn = labeled
	labeled, err = s.attachLabels(ctx, detail.Related)
	if err != nil {
		return model.IssueDetail{}, err
	}
	detail.Related = labeled
	labeled, err = s.attachLabels(ctx, detail.BlockedBy)
	if err != nil {
		return model.IssueDetail{}, err
	}
	detail.BlockedBy = labeled
	if detail.Parent != nil {
		parentIssues, err := s.attachLabels(ctx, []model.Issue{*detail.Parent})
		if err != nil {
			return model.IssueDetail{}, err
		}
		detail.Parent = &parentIssues[0]
	}
	return detail, nil
}

func (s *Store) GetIssue(ctx context.Context, id string) (model.Issue, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, title, description, status, priority, issue_type, assignee, created_at, updated_at, closed_at FROM issues WHERE id = ?`, id)
	issue, err := scanIssue(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Issue{}, fmt.Errorf("issue %q not found", id)
		}
		return model.Issue{}, err
	}
	labeled, err := s.attachLabels(ctx, []model.Issue{issue})
	if err != nil {
		return model.Issue{}, err
	}
	return labeled[0], nil
}

func (s *Store) UpdateIssue(ctx context.Context, id string, in UpdateIssueInput) (model.Issue, error) {
	issue, err := s.GetIssue(ctx, id)
	if err != nil {
		return model.Issue{}, err
	}
	if in.Title != nil {
		issue.Title = strings.TrimSpace(*in.Title)
		if issue.Title == "" {
			return model.Issue{}, errors.New("title cannot be empty")
		}
	}
	if in.Description != nil {
		issue.Description = strings.TrimSpace(*in.Description)
	}
	if in.IssueType != nil {
		issueType, err := validateIssueType(*in.IssueType)
		if err != nil {
			return model.Issue{}, err
		}
		issue.IssueType = issueType
	}
	if in.Status != nil {
		status := strings.TrimSpace(*in.Status)
		if status != "open" && status != "closed" {
			return model.Issue{}, errors.New("status must be open or closed")
		}
		issue.Status = status
		if status == "closed" {
			now := time.Now().UTC()
			issue.ClosedAt = &now
		} else {
			issue.ClosedAt = nil
		}
	}
	if in.Priority != nil {
		if err := validatePriority(*in.Priority); err != nil {
			return model.Issue{}, err
		}
		issue.Priority = *in.Priority
	}
	if in.Assignee != nil {
		issue.Assignee = strings.TrimSpace(*in.Assignee)
	}
	if in.Labels != nil {
		labels, err := canonicalizeLabels(*in.Labels)
		if err != nil {
			return model.Issue{}, err
		}
		issue.Labels = labels
	}
	issue.UpdatedAt = time.Now().UTC()
	var closedAt any
	if issue.ClosedAt != nil {
		closedAt = issue.ClosedAt.Format(time.RFC3339Nano)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Issue{}, fmt.Errorf("begin update issue tx: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `UPDATE issues SET
		title = ?, description = ?, status = ?, priority = ?, issue_type = ?, assignee = ?, updated_at = ?, closed_at = ?
		WHERE id = ?`, issue.Title, issue.Description, issue.Status, issue.Priority, issue.IssueType, issue.Assignee, issue.UpdatedAt.Format(time.RFC3339Nano), closedAt, issue.ID)
	if err != nil {
		return model.Issue{}, fmt.Errorf("update issue: %w", err)
	}
	if in.Labels != nil {
		if err := s.replaceLabelsTx(ctx, tx, issue.ID, issue.Labels, "links"); err != nil {
			return model.Issue{}, err
		}
	}
	if _, err := s.bumpWorkspaceRevisionTx(ctx, tx); err != nil {
		return model.Issue{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.Issue{}, fmt.Errorf("commit update issue: %w", err)
	}
	return issue, nil
}

func (s *Store) AddComment(ctx context.Context, in AddCommentInput) (model.Comment, error) {
	if _, err := s.GetIssue(ctx, in.IssueID); err != nil {
		return model.Comment{}, err
	}
	body := strings.TrimSpace(in.Body)
	if body == "" {
		return model.Comment{}, errors.New("comment body is required")
	}
	now := time.Now().UTC()
	comment := model.Comment{ID: "cmt-" + uuid.NewString(), IssueID: in.IssueID, Body: body, CreatedAt: now, CreatedBy: strings.TrimSpace(in.CreatedBy)}
	if comment.CreatedBy == "" {
		comment.CreatedBy = "unknown"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Comment{}, fmt.Errorf("begin add comment tx: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO comments(id, issue_id, body, created_at, created_by) VALUES (?, ?, ?, ?, ?)`, comment.ID, comment.IssueID, comment.Body, comment.CreatedAt.Format(time.RFC3339Nano), comment.CreatedBy); err != nil {
		return model.Comment{}, fmt.Errorf("insert comment: %w", err)
	}
	if _, err := s.bumpWorkspaceRevisionTx(ctx, tx); err != nil {
		return model.Comment{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.Comment{}, fmt.Errorf("commit add comment: %w", err)
	}
	return comment, nil
}

func (s *Store) AddRelation(ctx context.Context, in AddRelationInput) (model.Relation, error) {
	if _, err := s.GetIssue(ctx, in.SrcID); err != nil {
		return model.Relation{}, err
	}
	if _, err := s.GetIssue(ctx, in.DstID); err != nil {
		return model.Relation{}, err
	}
	relType := strings.TrimSpace(in.Type)
	if relType != "blocks" && relType != "parent-child" && relType != "related-to" {
		return model.Relation{}, errors.New("relation type must be blocks, parent-child, or related-to")
	}
	srcID, dstID := in.SrcID, in.DstID
	if relType == "related-to" {
		if srcID == dstID {
			return model.Relation{}, errors.New("related-to cannot target itself")
		}
		ordered := []string{srcID, dstID}
		sort.Strings(ordered)
		srcID, dstID = ordered[0], ordered[1]
	}
	now := time.Now().UTC()
	rel := model.Relation{SrcID: srcID, DstID: dstID, Type: relType, CreatedAt: now, CreatedBy: strings.TrimSpace(in.CreatedBy)}
	if rel.CreatedBy == "" {
		rel.CreatedBy = "unknown"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Relation{}, fmt.Errorf("begin add relation tx: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO relations(src_id, dst_id, type, created_at, created_by) VALUES (?, ?, ?, ?, ?)`, rel.SrcID, rel.DstID, rel.Type, rel.CreatedAt.Format(time.RFC3339Nano), rel.CreatedBy); err != nil {
		return model.Relation{}, fmt.Errorf("insert relation: %w", err)
	}
	if _, err := s.bumpWorkspaceRevisionTx(ctx, tx); err != nil {
		return model.Relation{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.Relation{}, fmt.Errorf("commit add relation: %w", err)
	}
	return rel, nil
}

func (s *Store) RemoveRelation(ctx context.Context, srcID, dstID, relType string) error {
	if relType == "related-to" {
		ordered := []string{srcID, dstID}
		sort.Strings(ordered)
		srcID, dstID = ordered[0], ordered[1]
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin remove relation tx: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM relations WHERE src_id = ? AND dst_id = ? AND type = ?`, srcID, dstID, relType)
	if err != nil {
		return fmt.Errorf("delete relation: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("relation not found")
	}
	if _, err := s.bumpWorkspaceRevisionTx(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit remove relation: %w", err)
	}
	return nil
}

func (s *Store) Export(ctx context.Context) (model.Export, error) {
	issues, err := s.ListIssues(ctx, ListIssuesFilter{Limit: 0})
	if err != nil {
		return model.Export{}, err
	}
	rels, err := s.listAllRelations(ctx)
	if err != nil {
		return model.Export{}, err
	}
	comments, err := s.listAllComments(ctx)
	if err != nil {
		return model.Export{}, err
	}
	labels, err := s.listAllLabels(ctx)
	if err != nil {
		return model.Export{}, err
	}
	workspaceRevision, err := s.GetWorkspaceRevision(ctx)
	if err != nil {
		return model.Export{}, err
	}
	return model.Export{Version: 1, WorkspaceID: s.workspaceID, WorkspaceRevision: workspaceRevision, ExportedAt: time.Now().UTC(), Issues: issues, Relations: rels, Comments: comments, Labels: labels}, nil
}

func (s *Store) ImportIssue(ctx context.Context, in ImportIssue) error {
	issueType, err := validateIssueType(in.IssueType)
	if err != nil {
		return err
	}
	if err := validatePriority(in.Priority); err != nil {
		return err
	}
	status := strings.TrimSpace(in.Status)
	if status != "open" && status != "closed" {
		return errors.New("status must be open or closed")
	}
	if strings.TrimSpace(in.ID) == "" {
		return errors.New("issue id is required")
	}
	if strings.TrimSpace(in.Title) == "" {
		return errors.New("title is required")
	}
	var closedAt any
	if in.ClosedAt != nil {
		closedAt = in.ClosedAt.Format(time.RFC3339Nano)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin import issue tx: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO issues(
		id, title, description, status, priority, issue_type, assignee, created_at, updated_at, closed_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		title = excluded.title,
		description = excluded.description,
		status = excluded.status,
		priority = excluded.priority,
		issue_type = excluded.issue_type,
		assignee = excluded.assignee,
		created_at = excluded.created_at,
		updated_at = excluded.updated_at,
		closed_at = excluded.closed_at`,
		in.ID,
		strings.TrimSpace(in.Title),
		strings.TrimSpace(in.Description),
		status,
		in.Priority,
		issueType,
		strings.TrimSpace(in.Assignee),
		in.CreatedAt.Format(time.RFC3339Nano),
		in.UpdatedAt.Format(time.RFC3339Nano),
		closedAt,
	)
	if err != nil {
		return fmt.Errorf("import issue: %w", err)
	}
	labels, err := canonicalizeLabels(in.Labels)
	if err != nil {
		return err
	}
	if err := s.replaceLabelsTx(ctx, tx, in.ID, labels, "import"); err != nil {
		return err
	}
	if _, err := s.bumpWorkspaceRevisionTx(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit import issue: %w", err)
	}
	return nil
}

func (s *Store) ImportComment(ctx context.Context, in ImportComment) error {
	if _, err := s.GetIssue(ctx, in.IssueID); err != nil {
		return err
	}
	if strings.TrimSpace(in.ID) == "" {
		return errors.New("comment id is required")
	}
	if strings.TrimSpace(in.Body) == "" {
		return errors.New("comment body is required")
	}
	createdBy := strings.TrimSpace(in.CreatedBy)
	if createdBy == "" {
		createdBy = "unknown"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin import comment tx: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO comments(id, issue_id, body, created_at, created_by)
	VALUES (?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		issue_id = excluded.issue_id,
		body = excluded.body,
		created_at = excluded.created_at,
		created_by = excluded.created_by`,
		in.ID, in.IssueID, strings.TrimSpace(in.Body), in.CreatedAt.Format(time.RFC3339Nano), createdBy)
	if err != nil {
		return fmt.Errorf("import comment: %w", err)
	}
	if _, err := s.bumpWorkspaceRevisionTx(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit import comment: %w", err)
	}
	return nil
}

func (s *Store) ImportRelation(ctx context.Context, in ImportRelation) error {
	if _, err := s.GetIssue(ctx, in.SrcID); err != nil {
		return err
	}
	if _, err := s.GetIssue(ctx, in.DstID); err != nil {
		return err
	}
	relType := strings.TrimSpace(in.Type)
	if relType != "blocks" && relType != "parent-child" && relType != "related-to" {
		return errors.New("relation type must be blocks, parent-child, or related-to")
	}
	srcID, dstID := in.SrcID, in.DstID
	if relType == "related-to" {
		ordered := []string{srcID, dstID}
		sort.Strings(ordered)
		srcID, dstID = ordered[0], ordered[1]
	}
	createdBy := strings.TrimSpace(in.CreatedBy)
	if createdBy == "" {
		createdBy = "unknown"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin import relation tx: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO relations(src_id, dst_id, type, created_at, created_by)
	VALUES (?, ?, ?, ?, ?)
	ON CONFLICT(src_id, dst_id, type) DO UPDATE SET
		created_at = excluded.created_at,
		created_by = excluded.created_by`,
		srcID, dstID, relType, in.CreatedAt.Format(time.RFC3339Nano), createdBy)
	if err != nil {
		return fmt.Errorf("import relation: %w", err)
	}
	if _, err := s.bumpWorkspaceRevisionTx(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit import relation: %w", err)
	}
	return nil
}

func (s *Store) ImportLabel(ctx context.Context, in ImportLabel) error {
	if _, err := s.GetIssue(ctx, in.IssueID); err != nil {
		return err
	}
	label, err := normalizeLabel(in.Name)
	if err != nil {
		return err
	}
	createdBy := strings.TrimSpace(in.CreatedBy)
	if createdBy == "" {
		createdBy = "unknown"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin import label tx: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO labels(issue_id, label, created_at, created_by)
	VALUES (?, ?, ?, ?)
	ON CONFLICT(issue_id, label) DO UPDATE SET
		created_at = excluded.created_at,
		created_by = excluded.created_by`, in.IssueID, label, in.CreatedAt.Format(time.RFC3339Nano), createdBy)
	if err != nil {
		return fmt.Errorf("import label: %w", err)
	}
	if _, err := s.bumpWorkspaceRevisionTx(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit import label: %w", err)
	}
	return nil
}

func (s *Store) AddLabel(ctx context.Context, in AddLabelInput) ([]string, error) {
	if _, err := s.GetIssue(ctx, in.IssueID); err != nil {
		return nil, err
	}
	label, err := normalizeLabel(in.Name)
	if err != nil {
		return nil, err
	}
	createdBy := strings.TrimSpace(in.CreatedBy)
	if createdBy == "" {
		createdBy = "unknown"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin add label tx: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO labels(issue_id, label, created_at, created_by)
	VALUES (?, ?, ?, ?)
	ON CONFLICT(issue_id, label) DO NOTHING`, in.IssueID, label, time.Now().UTC().Format(time.RFC3339Nano), createdBy)
	if err != nil {
		return nil, fmt.Errorf("insert label: %w", err)
	}
	if _, err := s.bumpWorkspaceRevisionTx(ctx, tx); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit add label: %w", err)
	}
	return s.ListLabels(ctx, in.IssueID)
}

func (s *Store) RemoveLabel(ctx context.Context, issueID, labelName string) ([]string, error) {
	if _, err := s.GetIssue(ctx, issueID); err != nil {
		return nil, err
	}
	label, err := normalizeLabel(labelName)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin remove label tx: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM labels WHERE issue_id = ? AND label = ?`, issueID, label)
	if err != nil {
		return nil, fmt.Errorf("delete label: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, fmt.Errorf("label %q not found on issue %q", label, issueID)
	}
	if _, err := s.bumpWorkspaceRevisionTx(ctx, tx); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit remove label: %w", err)
	}
	return s.ListLabels(ctx, issueID)
}

func (s *Store) ReplaceLabels(ctx context.Context, issueID string, labels []string, createdBy string) error {
	if _, err := s.GetIssue(ctx, issueID); err != nil {
		return err
	}
	normalized, err := canonicalizeLabels(labels)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin replace labels tx: %w", err)
	}
	defer tx.Rollback()
	if err := s.replaceLabelsTx(ctx, tx, issueID, normalized, createdBy); err != nil {
		return err
	}
	if _, err := s.bumpWorkspaceRevisionTx(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit replace labels: %w", err)
	}
	return nil
}

func (s *Store) ListLabels(ctx context.Context, issueID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT label FROM labels WHERE issue_id = ? ORDER BY label ASC`, issueID)
	if err != nil {
		return nil, fmt.Errorf("list labels: %w", err)
	}
	defer rows.Close()
	var labels []string
	for rows.Next() {
		var label string
		if err := rows.Scan(&label); err != nil {
			return nil, err
		}
		labels = append(labels, label)
	}
	return labels, rows.Err()
}

func (s *Store) ReplaceFromExport(ctx context.Context, export model.Export) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin replace from export tx: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"labels", "comments", "relations", "issues"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("clear %s: %w", table, err)
		}
	}
	for _, issue := range export.Issues {
		var closedAt any
		if issue.ClosedAt != nil {
			closedAt = issue.ClosedAt.Format(time.RFC3339Nano)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO issues(id, title, description, status, priority, issue_type, assignee, created_at, updated_at, closed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			issue.ID, issue.Title, issue.Description, issue.Status, issue.Priority, issue.IssueType, issue.Assignee, issue.CreatedAt.Format(time.RFC3339Nano), issue.UpdatedAt.Format(time.RFC3339Nano), closedAt); err != nil {
			return fmt.Errorf("restore issue %s: %w", issue.ID, err)
		}
	}
	for _, relation := range export.Relations {
		if _, err := tx.ExecContext(ctx, `INSERT INTO relations(src_id, dst_id, type, created_at, created_by) VALUES (?, ?, ?, ?, ?)`,
			relation.SrcID, relation.DstID, relation.Type, relation.CreatedAt.Format(time.RFC3339Nano), relation.CreatedBy); err != nil {
			return fmt.Errorf("restore relation %s->%s: %w", relation.SrcID, relation.DstID, err)
		}
	}
	for _, comment := range export.Comments {
		if _, err := tx.ExecContext(ctx, `INSERT INTO comments(id, issue_id, body, created_at, created_by) VALUES (?, ?, ?, ?, ?)`,
			comment.ID, comment.IssueID, comment.Body, comment.CreatedAt.Format(time.RFC3339Nano), comment.CreatedBy); err != nil {
			return fmt.Errorf("restore comment %s: %w", comment.ID, err)
		}
	}
	for _, label := range export.Labels {
		if _, err := tx.ExecContext(ctx, `INSERT INTO labels(issue_id, label, created_at, created_by) VALUES (?, ?, ?, ?)`,
			label.IssueID, label.Name, label.CreatedAt.Format(time.RFC3339Nano), label.CreatedBy); err != nil {
			return fmt.Errorf("restore label %s:%s: %w", label.IssueID, label.Name, err)
		}
	}
	workspaceRevision := export.WorkspaceRevision
	if workspaceRevision < 0 {
		workspaceRevision = 0
	}
	if err := s.setMeta(ctx, tx, "workspace_revision", strconv.FormatInt(workspaceRevision, 10)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit replace from export: %w", err)
	}
	return nil
}

func (s *Store) listRelations(ctx context.Context, issueID string) ([]model.Relation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT src_id, dst_id, type, created_at, created_by FROM relations WHERE src_id = ? OR dst_id = ? ORDER BY created_at ASC`, issueID, issueID)
	if err != nil {
		return nil, fmt.Errorf("list relations: %w", err)
	}
	defer rows.Close()
	rels := []model.Relation{}
	for rows.Next() {
		var rel model.Relation
		var createdAt string
		if err := rows.Scan(&rel.SrcID, &rel.DstID, &rel.Type, &createdAt, &rel.CreatedBy); err != nil {
			return nil, err
		}
		t, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, err
		}
		rel.CreatedAt = t
		rels = append(rels, rel)
	}
	return rels, rows.Err()
}

func (s *Store) getWorkspaceRevision(ctx context.Context, tx *sql.Tx) (int64, error) {
	value, err := s.getMeta(ctx, tx, "workspace_revision")
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	revision, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse workspace_revision: %w", err)
	}
	return revision, nil
}

func (s *Store) bumpWorkspaceRevisionTx(ctx context.Context, tx *sql.Tx) (int64, error) {
	current, err := s.getWorkspaceRevision(ctx, tx)
	if err != nil {
		return 0, err
	}
	next := current + 1
	if err := s.setMeta(ctx, tx, "workspace_revision", strconv.FormatInt(next, 10)); err != nil {
		return 0, err
	}
	return next, nil
}

func (s *Store) getMeta(ctx context.Context, tx *sql.Tx, key string) (string, error) {
	var row *sql.Row
	if tx != nil {
		row = tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key)
	} else {
		row = s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key)
	}
	var value string
	if err := row.Scan(&value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("get meta %q: %w", key, err)
	}
	return value, nil
}

func (s *Store) setMeta(ctx context.Context, tx *sql.Tx, key, value string) error {
	var execer interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	}
	if tx != nil {
		execer = tx
	} else {
		execer = s.db
	}
	if _, err := execer.ExecContext(ctx, `INSERT INTO meta(key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value); err != nil {
		return fmt.Errorf("set meta %q: %w", key, err)
	}
	return nil
}

func (s *Store) listAllLabels(ctx context.Context) ([]model.Label, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT issue_id, label, created_at, created_by FROM labels ORDER BY issue_id ASC, label ASC`)
	if err != nil {
		return nil, fmt.Errorf("list all labels: %w", err)
	}
	defer rows.Close()
	out := []model.Label{}
	for rows.Next() {
		var label model.Label
		var createdAt string
		if err := rows.Scan(&label.IssueID, &label.Name, &createdAt, &label.CreatedBy); err != nil {
			return nil, err
		}
		t, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, err
		}
		label.CreatedAt = t
		out = append(out, label)
	}
	return out, rows.Err()
}

func (s *Store) listComments(ctx context.Context, issueID string) ([]model.Comment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, issue_id, body, created_at, created_by FROM comments WHERE issue_id = ? ORDER BY created_at ASC`, issueID)
	if err != nil {
		return nil, fmt.Errorf("list comments: %w", err)
	}
	defer rows.Close()
	out := []model.Comment{}
	for rows.Next() {
		var c model.Comment
		var createdAt string
		if err := rows.Scan(&c.ID, &c.IssueID, &c.Body, &createdAt, &c.CreatedBy); err != nil {
			return nil, err
		}
		t, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, err
		}
		c.CreatedAt = t
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) listAllRelations(ctx context.Context) ([]model.Relation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT src_id, dst_id, type, created_at, created_by FROM relations ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("list all relations: %w", err)
	}
	defer rows.Close()
	rels := []model.Relation{}
	for rows.Next() {
		var rel model.Relation
		var createdAt string
		if err := rows.Scan(&rel.SrcID, &rel.DstID, &rel.Type, &createdAt, &rel.CreatedBy); err != nil {
			return nil, err
		}
		t, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, err
		}
		rel.CreatedAt = t
		rels = append(rels, rel)
	}
	return rels, rows.Err()
}

func (s *Store) listAllComments(ctx context.Context) ([]model.Comment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, issue_id, body, created_at, created_by FROM comments ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("list all comments: %w", err)
	}
	defer rows.Close()
	out := []model.Comment{}
	for rows.Next() {
		var c model.Comment
		var createdAt string
		if err := rows.Scan(&c.ID, &c.IssueID, &c.Body, &createdAt, &c.CreatedBy); err != nil {
			return nil, err
		}
		t, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, err
		}
		c.CreatedAt = t
		out = append(out, c)
	}
	return out, rows.Err()
}

type issueScanner interface{ Scan(dest ...any) error }

func scanIssue(row issueScanner) (model.Issue, error) {
	var issue model.Issue
	var createdAt, updatedAt string
	var closedAt sql.NullString
	if err := row.Scan(&issue.ID, &issue.Title, &issue.Description, &issue.Status, &issue.Priority, &issue.IssueType, &issue.Assignee, &createdAt, &updatedAt, &closedAt); err != nil {
		return model.Issue{}, err
	}
	var err error
	issue.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return model.Issue{}, err
	}
	issue.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return model.Issue{}, err
	}
	if closedAt.Valid {
		t, err := time.Parse(time.RFC3339Nano, closedAt.String)
		if err != nil {
			return model.Issue{}, err
		}
		issue.ClosedAt = &t
	}
	issue.Labels = []string{}
	return issue, nil
}

func (s *Store) attachLabels(ctx context.Context, issues []model.Issue) ([]model.Issue, error) {
	if len(issues) == 0 {
		return issues, nil
	}
	issueIDs := make([]string, 0, len(issues))
	for _, issue := range issues {
		issueIDs = append(issueIDs, issue.ID)
	}
	labelMap, err := s.loadLabelsByIssueIDs(ctx, issueIDs)
	if err != nil {
		return nil, err
	}
	for index := range issues {
		issues[index].Labels = labelMap[issues[index].ID]
		if issues[index].Labels == nil {
			issues[index].Labels = []string{}
		}
	}
	return issues, nil
}

func (s *Store) loadLabelsByIssueIDs(ctx context.Context, issueIDs []string) (map[string][]string, error) {
	placeholders := make([]string, 0, len(issueIDs))
	args := make([]any, 0, len(issueIDs))
	for _, issueID := range issueIDs {
		placeholders = append(placeholders, "?")
		args = append(args, issueID)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT issue_id, label FROM labels WHERE issue_id IN (`+strings.Join(placeholders, ", ")+`) ORDER BY label ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("load labels by issue ids: %w", err)
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var issueID, label string
		if err := rows.Scan(&issueID, &label); err != nil {
			return nil, err
		}
		out[issueID] = append(out[issueID], label)
	}
	return out, rows.Err()
}

func (s *Store) replaceLabelsTx(ctx context.Context, tx *sql.Tx, issueID string, labels []string, createdBy string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM labels WHERE issue_id = ?`, issueID); err != nil {
		return fmt.Errorf("clear labels: %w", err)
	}
	author := strings.TrimSpace(createdBy)
	if author == "" {
		author = "unknown"
	}
	timestamp := time.Now().UTC().Format(time.RFC3339Nano)
	for _, label := range labels {
		if _, err := tx.ExecContext(ctx, `INSERT INTO labels(issue_id, label, created_at, created_by) VALUES (?, ?, ?, ?)`, issueID, label, timestamp, author); err != nil {
			return fmt.Errorf("insert label %q: %w", label, err)
		}
	}
	return nil
}

func validateIssueType(issueType string) (string, error) {
	issueType = strings.TrimSpace(strings.ToLower(issueType))
	switch issueType {
	case "", "task", "feature", "bug", "chore", "epic":
		if issueType == "" {
			return "task", nil
		}
		return issueType, nil
	default:
		return "", errors.New("issue type must be task, feature, bug, chore, or epic")
	}
}

func validatePriority(priority int) error {
	if priority < 0 || priority > 4 {
		return errors.New("priority must be between 0 and 4")
	}
	return nil
}

func canonicalizeLabels(labels []string) ([]string, error) {
	out := make([]string, 0, len(labels))
	seen := map[string]struct{}{}
	for _, label := range labels {
		normalized, err := normalizeLabel(label)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	sort.Strings(out)
	return out, nil
}

func normalizeLabel(label string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(label))
	if normalized == "" {
		return "", errors.New("label is required")
	}
	if strings.Contains(normalized, ",") {
		return "", errors.New("label cannot contain commas")
	}
	return normalized, nil
}

func newIssueID(workspaceID string) string {
	prefix := strings.SplitN(workspaceID, "-", 2)[0]
	return fmt.Sprintf("lk-%s-%s", prefix, uuid.NewString()[:8])
}
