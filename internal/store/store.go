package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
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

type CreateIssueInput struct {
	Title       string
	Description string
	IssueType   string
	Priority    int
	Assignee    string
}

type UpdateIssueInput struct {
	Title       *string
	Description *string
	IssueType   *string
	Status      *string
	Priority    *int
	Assignee    *string
}

type ListIssuesFilter struct {
	Status    string
	IssueType string
	Assignee  string
	Limit     int
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
		`CREATE INDEX IF NOT EXISTS idx_issues_status_priority ON issues(status, priority, updated_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_relations_src_type ON relations(src_id, type);`,
		`CREATE INDEX IF NOT EXISTS idx_relations_dst_type ON relations(dst_id, type);`,
		`CREATE INDEX IF NOT EXISTS idx_comments_issue_created ON comments(issue_id, created_at);`,
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
	issue := model.Issue{
		ID:          newIssueID(s.workspaceID),
		Title:       strings.TrimSpace(in.Title),
		Description: strings.TrimSpace(in.Description),
		Status:      "open",
		Priority:    priority,
		IssueType:   issueType,
		Assignee:    strings.TrimSpace(in.Assignee),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO issues(
		id, title, description, status, priority, issue_type, assignee, created_at, updated_at, closed_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
		issue.ID, issue.Title, issue.Description, issue.Status, issue.Priority, issue.IssueType,
		issue.Assignee, issue.CreatedAt.Format(time.RFC3339Nano), issue.UpdatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return model.Issue{}, fmt.Errorf("insert issue: %w", err)
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
	return issues, rows.Err()
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
	return issue, nil
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
	issue.UpdatedAt = time.Now().UTC()
	var closedAt any
	if issue.ClosedAt != nil {
		closedAt = issue.ClosedAt.Format(time.RFC3339Nano)
	}
	_, err = s.db.ExecContext(ctx, `UPDATE issues SET
		title = ?, description = ?, status = ?, priority = ?, issue_type = ?, assignee = ?, updated_at = ?, closed_at = ?
		WHERE id = ?`, issue.Title, issue.Description, issue.Status, issue.Priority, issue.IssueType, issue.Assignee, issue.UpdatedAt.Format(time.RFC3339Nano), closedAt, issue.ID)
	if err != nil {
		return model.Issue{}, fmt.Errorf("update issue: %w", err)
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
	_, err := s.db.ExecContext(ctx, `INSERT INTO comments(id, issue_id, body, created_at, created_by) VALUES (?, ?, ?, ?, ?)`, comment.ID, comment.IssueID, comment.Body, comment.CreatedAt.Format(time.RFC3339Nano), comment.CreatedBy)
	if err != nil {
		return model.Comment{}, fmt.Errorf("insert comment: %w", err)
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
	_, err := s.db.ExecContext(ctx, `INSERT INTO relations(src_id, dst_id, type, created_at, created_by) VALUES (?, ?, ?, ?, ?)`, rel.SrcID, rel.DstID, rel.Type, rel.CreatedAt.Format(time.RFC3339Nano), rel.CreatedBy)
	if err != nil {
		return model.Relation{}, fmt.Errorf("insert relation: %w", err)
	}
	return rel, nil
}

func (s *Store) RemoveRelation(ctx context.Context, srcID, dstID, relType string) error {
	if relType == "related-to" {
		ordered := []string{srcID, dstID}
		sort.Strings(ordered)
		srcID, dstID = ordered[0], ordered[1]
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM relations WHERE src_id = ? AND dst_id = ? AND type = ?`, srcID, dstID, relType)
	if err != nil {
		return fmt.Errorf("delete relation: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("relation not found")
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
	return model.Export{Version: 1, WorkspaceID: s.workspaceID, ExportedAt: time.Now().UTC(), Issues: issues, Relations: rels, Comments: comments}, nil
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
	_, err = s.db.ExecContext(ctx, `INSERT INTO issues(
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
	_, err := s.db.ExecContext(ctx, `INSERT INTO comments(id, issue_id, body, created_at, created_by)
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
	_, err := s.db.ExecContext(ctx, `INSERT INTO relations(src_id, dst_id, type, created_at, created_by)
	VALUES (?, ?, ?, ?, ?)
	ON CONFLICT(src_id, dst_id, type) DO UPDATE SET
		created_at = excluded.created_at,
		created_by = excluded.created_by`,
		srcID, dstID, relType, in.CreatedAt.Format(time.RFC3339Nano), createdBy)
	if err != nil {
		return fmt.Errorf("import relation: %w", err)
	}
	return nil
}

func (s *Store) listRelations(ctx context.Context, issueID string) ([]model.Relation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT src_id, dst_id, type, created_at, created_by FROM relations WHERE src_id = ? OR dst_id = ? ORDER BY created_at ASC`, issueID, issueID)
	if err != nil {
		return nil, fmt.Errorf("list relations: %w", err)
	}
	defer rows.Close()
	var rels []model.Relation
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

func (s *Store) listComments(ctx context.Context, issueID string) ([]model.Comment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, issue_id, body, created_at, created_by FROM comments WHERE issue_id = ? ORDER BY created_at ASC`, issueID)
	if err != nil {
		return nil, fmt.Errorf("list comments: %w", err)
	}
	defer rows.Close()
	var out []model.Comment
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
	var rels []model.Relation
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
	var out []model.Comment
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
	return issue, nil
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

func newIssueID(workspaceID string) string {
	prefix := strings.SplitN(workspaceID, "-", 2)[0]
	return fmt.Sprintf("lk-%s-%s", prefix, uuid.NewString()[:8])
}
