package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/bmf/links-issue-tracker/internal/app"
	"github.com/bmf/links-issue-tracker/internal/beads"
	"github.com/bmf/links-issue-tracker/internal/model"
	"github.com/bmf/links-issue-tracker/internal/query"
	"github.com/bmf/links-issue-tracker/internal/store"
	"github.com/bmf/links-issue-tracker/internal/workspace"
)

func Run(ctx context.Context, stdout io.Writer, stderr io.Writer, args []string) error {
	if len(args) == 0 {
		printUsage(stderr)
		return nil
	}
	ap, err := app.OpenFromWD(ctx)
	if err != nil {
		if errors.Is(err, workspace.ErrNotGitRepo) {
			return fmt.Errorf("links requires running inside a git repository/worktree")
		}
		return err
	}
	defer ap.Close()

	switch args[0] {
	case "new":
		return runNew(ctx, stdout, ap, args[1:])
	case "ls":
		return runList(ctx, stdout, ap, args[1:])
	case "show":
		return runShow(ctx, stdout, ap, args[1:])
	case "edit":
		return runEdit(ctx, stdout, ap, args[1:])
	case "close":
		return runStatus(ctx, stdout, ap, args[1:], "closed")
	case "open":
		return runStatus(ctx, stdout, ap, args[1:], "open")
	case "comment":
		return runComment(ctx, stdout, ap, args[1:])
	case "dep":
		return runDep(ctx, stdout, ap, args[1:])
	case "export":
		return runExport(ctx, stdout, ap, args[1:])
	case "beads":
		return runBeads(ctx, stdout, ap, args[1:])
	case "workspace":
		return runWorkspace(stdout, ap, args[1:])
	case "help", "-h", "--help":
		printUsage(stdout)
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runNew(ctx context.Context, stdout io.Writer, ap *app.App, args []string) error {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	title := fs.String("title", "", "Issue title")
	description := fs.String("description", "", "Issue description")
	issueType := fs.String("type", "task", "Issue type: task|feature|bug|chore|epic")
	priority := fs.Int("priority", 2, "Priority 0..4 (lower is more important)")
	assignee := fs.String("assignee", "", "Assignee")
	jsonOut := fs.Bool("json", false, "Output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	issue, err := ap.Store.CreateIssue(ctx, store.CreateIssueInput{
		Title: *title, Description: *description, IssueType: *issueType, Priority: *priority, Assignee: *assignee,
	})
	if err != nil {
		return err
	}
	return printValue(stdout, issue, *jsonOut, printIssueSummary)
}

func runList(ctx context.Context, stdout io.Writer, ap *app.App, args []string) error {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	status := fs.String("status", "", "Filter by status")
	issueType := fs.String("type", "", "Filter by issue type")
	assignee := fs.String("assignee", "", "Filter by assignee")
	priorityMin := fs.Int("priority-min", -1, "Minimum priority 0..4")
	priorityMax := fs.Int("priority-max", -1, "Maximum priority 0..4")
	search := fs.String("search", "", "Search title and description text")
	ids := fs.String("ids", "", "Comma-separated issue IDs")
	hasComments := fs.Bool("has-comments", false, "Only include issues with comments")
	updatedAfter := fs.String("updated-after", "", "Only include issues updated at or after RFC3339 timestamp")
	updatedBefore := fs.String("updated-before", "", "Only include issues updated at or before RFC3339 timestamp")
	queryExpr := fs.String("query", "", "Query language: status:open type:task priority<=2 has:comments text")
	limit := fs.Int("limit", 0, "Limit results")
	jsonOut := fs.Bool("json", false, "Output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	visited := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { visited[f.Name] = true })
	filter := store.ListIssuesFilter{
		Status:    strings.TrimSpace(*status),
		IssueType: strings.TrimSpace(*issueType),
		Assignee:  strings.TrimSpace(*assignee),
		Limit:     *limit,
	}
	if visited["priority-min"] {
		value := *priorityMin
		filter.PriorityMin = &value
	}
	if visited["priority-max"] {
		value := *priorityMax
		filter.PriorityMax = &value
	}
	if visited["search"] {
		filter.SearchTerms = append(filter.SearchTerms, strings.TrimSpace(*search))
	}
	if visited["ids"] {
		filter.IDs = splitCSV(*ids)
	}
	if visited["has-comments"] {
		value := *hasComments
		filter.HasComments = &value
	}
	if visited["updated-after"] {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*updatedAfter))
		if err != nil {
			return fmt.Errorf("parse --updated-after: %w", err)
		}
		filter.UpdatedAfter = &parsed
	}
	if visited["updated-before"] {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*updatedBefore))
		if err != nil {
			return fmt.Errorf("parse --updated-before: %w", err)
		}
		filter.UpdatedBefore = &parsed
	}
	if strings.TrimSpace(*queryExpr) != "" {
		parsed, err := query.Parse(*queryExpr)
		if err != nil {
			return err
		}
		filter, err = query.Merge(filter, parsed.Filter)
		if err != nil {
			return err
		}
	}
	issues, err := ap.Store.ListIssues(ctx, filter)
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeJSON(stdout, issues)
	}
	return printIssueTable(stdout, issues)
}

func runShow(ctx context.Context, stdout io.Writer, ap *app.App, args []string) error {
	positional, flagArgs := splitArgs(args, 1)
	if len(positional) != 1 {
		return errors.New("usage: lk show <id>")
	}
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "Output JSON")
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: lk show <id>")
	}
	detail, err := ap.Store.GetIssueDetail(ctx, positional[0])
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeJSON(stdout, detail)
	}
	return printIssueDetail(stdout, detail)
}

func runEdit(ctx context.Context, stdout io.Writer, ap *app.App, args []string) error {
	positional, flagArgs := splitArgs(args, 1)
	if len(positional) != 1 {
		return errors.New("usage: lk edit <id> [flags]")
	}
	fs := flag.NewFlagSet("edit", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	title := fs.String("title", "", "New title")
	description := fs.String("description", "", "New description")
	issueType := fs.String("type", "", "New issue type")
	status := fs.String("status", "", "New status")
	priority := fs.Int("priority", -1, "New priority")
	assignee := fs.String("assignee", "", "New assignee")
	clearAssignee := fs.Bool("clear-assignee", false, "Clear assignee")
	jsonOut := fs.Bool("json", false, "Output JSON")
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: lk edit <id> [flags]")
	}
	visited := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { visited[f.Name] = true })
	var in store.UpdateIssueInput
	if visited["title"] {
		in.Title = title
	}
	if visited["description"] {
		in.Description = description
	}
	if visited["type"] {
		in.IssueType = issueType
	}
	if visited["status"] {
		in.Status = status
	}
	if visited["priority"] {
		in.Priority = priority
	}
	if *clearAssignee {
		empty := ""
		in.Assignee = &empty
	} else if visited["assignee"] {
		in.Assignee = assignee
	}
	issue, err := ap.Store.UpdateIssue(ctx, positional[0], in)
	if err != nil {
		return err
	}
	return printValue(stdout, issue, *jsonOut, printIssueSummary)
}

func runStatus(ctx context.Context, stdout io.Writer, ap *app.App, args []string, status string) error {
	positional, flagArgs := splitArgs(args, 1)
	if len(positional) != 1 {
		return fmt.Errorf("usage: lk %s <id>", status)
	}
	fs := flag.NewFlagSet(status, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "Output JSON")
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: lk %s <id>", status)
	}
	issue, err := ap.Store.UpdateIssue(ctx, positional[0], store.UpdateIssueInput{Status: &status})
	if err != nil {
		return err
	}
	return printValue(stdout, issue, *jsonOut, printIssueSummary)
}

func runComment(ctx context.Context, stdout io.Writer, ap *app.App, args []string) error {
	if len(args) == 0 || args[0] != "add" {
		return errors.New("usage: lk comment add <id> --body <text>")
	}
	positional, flagArgs := splitArgs(args[1:], 1)
	if len(positional) != 1 {
		return errors.New("usage: lk comment add <id> --body <text>")
	}
	fs := flag.NewFlagSet("comment add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	body := fs.String("body", "", "Comment body")
	by := fs.String("by", os.Getenv("USER"), "Comment author")
	jsonOut := fs.Bool("json", false, "Output JSON")
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: lk comment add <id> --body <text>")
	}
	comment, err := ap.Store.AddComment(ctx, store.AddCommentInput{IssueID: positional[0], Body: *body, CreatedBy: *by})
	if err != nil {
		return err
	}
	return printValue(stdout, comment, *jsonOut, func(w io.Writer, v any) error {
		c := v.(model.Comment)
		_, err := fmt.Fprintf(w, "%s %s\n", c.IssueID, c.ID)
		return err
	})
}

func runDep(ctx context.Context, stdout io.Writer, ap *app.App, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: lk dep <add|rm> ...")
	}
	switch args[0] {
	case "add":
		positional, flagArgs := splitArgs(args[1:], 2)
		if len(positional) != 2 {
			return errors.New("usage: lk dep add <src-id> <dst-id> [--type blocks|parent-child|related-to]")
		}
		fs := flag.NewFlagSet("dep add", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		relType := fs.String("type", "blocks", "Relation type: blocks|parent-child|related-to")
		by := fs.String("by", os.Getenv("USER"), "Relation creator")
		jsonOut := fs.Bool("json", false, "Output JSON")
		if err := fs.Parse(flagArgs); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("usage: lk dep add <src-id> <dst-id> [--type blocks|parent-child|related-to]")
		}
		rel, err := ap.Store.AddRelation(ctx, store.AddRelationInput{SrcID: positional[0], DstID: positional[1], Type: *relType, CreatedBy: *by})
		if err != nil {
			return err
		}
		return printValue(stdout, rel, *jsonOut, func(w io.Writer, v any) error {
			r := v.(model.Relation)
			_, err := fmt.Fprintf(w, "%s --%s--> %s\n", r.SrcID, r.Type, r.DstID)
			return err
		})
	case "rm":
		positional, flagArgs := splitArgs(args[1:], 2)
		if len(positional) != 2 {
			return errors.New("usage: lk dep rm <src-id> <dst-id> [--type ...]")
		}
		fs := flag.NewFlagSet("dep rm", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		relType := fs.String("type", "blocks", "Relation type: blocks|parent-child|related-to")
		if err := fs.Parse(flagArgs); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("usage: lk dep rm <src-id> <dst-id> [--type ...]")
		}
		if err := ap.Store.RemoveRelation(ctx, positional[0], positional[1], *relType); err != nil {
			return err
		}
		_, err := fmt.Fprintln(stdout, "ok")
		return err
	default:
		return errors.New("usage: lk dep <add|rm> ...")
	}
}

func runExport(ctx context.Context, stdout io.Writer, ap *app.App, args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return err
	}
	export, err := ap.Store.Export(ctx)
	if err != nil {
		return err
	}
	return writeJSON(stdout, export)
}

func runBeads(ctx context.Context, stdout io.Writer, ap *app.App, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: lk beads <import|export> --db <path> [--json]")
	}
	switch args[0] {
	case "import":
		fs := flag.NewFlagSet("beads import", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		dbPath := fs.String("db", "", "Path to beads sqlite database")
		jsonOut := fs.Bool("json", false, "Output JSON")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(*dbPath) == "" {
			return errors.New("usage: lk beads import --db <path> [--json]")
		}
		summary, err := beads.Import(ctx, ap.Store, *dbPath)
		if err != nil {
			return err
		}
		return printValue(stdout, summary, *jsonOut, func(w io.Writer, v any) error {
			s := v.(beads.Summary)
			_, err := fmt.Fprintf(w, "imported issues=%d relations=%d comments=%d\n", s.Issues, s.Relations, s.Comments)
			return err
		})
	case "export":
		fs := flag.NewFlagSet("beads export", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		dbPath := fs.String("db", "", "Path to beads sqlite database")
		jsonOut := fs.Bool("json", false, "Output JSON")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(*dbPath) == "" {
			return errors.New("usage: lk beads export --db <path> [--json]")
		}
		summary, err := beads.Export(ctx, ap.Store, *dbPath)
		if err != nil {
			return err
		}
		return printValue(stdout, summary, *jsonOut, func(w io.Writer, v any) error {
			s := v.(beads.Summary)
			_, err := fmt.Fprintf(w, "exported issues=%d relations=%d comments=%d\n", s.Issues, s.Relations, s.Comments)
			return err
		})
	default:
		return errors.New("usage: lk beads <import|export> --db <path> [--json]")
	}
}

func runWorkspace(stdout io.Writer, ap *app.App, args []string) error {
	jsonOut := len(args) > 0 && args[0] == "--json"
	payload := map[string]string{
		"workspace_id":   ap.Workspace.WorkspaceID,
		"git_common_dir": ap.Workspace.GitCommonDir,
		"storage_dir":    ap.Workspace.StorageDir,
		"database_path":  ap.Workspace.DatabasePath,
	}
	if jsonOut {
		return writeJSON(stdout, payload)
	}
	for _, key := range []string{"workspace_id", "git_common_dir", "storage_dir", "database_path"} {
		if _, err := fmt.Fprintf(stdout, "%s: %s\n", key, payload[key]); err != nil {
			return err
		}
	}
	return nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func printValue(w io.Writer, v any, jsonOut bool, textFn func(io.Writer, any) error) error {
	if jsonOut {
		return writeJSON(w, v)
	}
	return textFn(w, v)
}

func printIssueSummary(w io.Writer, v any) error {
	issue := v.(model.Issue)
	_, err := fmt.Fprintf(w, "%s [%s/%s/P%d] %s\n", issue.ID, issue.Status, issue.IssueType, issue.Priority, issue.Title)
	return err
}

func printIssueTable(w io.Writer, issues []model.Issue) error {
	tw := tabwriter.NewWriter(w, 2, 2, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "ID\tSTATUS\tTYPE\tP\tASSIGNEE\tTITLE"); err != nil {
		return err
	}
	for _, issue := range issues {
		assignee := issue.Assignee
		if assignee == "" {
			assignee = "-"
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n", issue.ID, issue.Status, issue.IssueType, issue.Priority, assignee, issue.Title); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func printIssueDetail(w io.Writer, detail model.IssueDetail) error {
	issue := detail.Issue
	if _, err := fmt.Fprintf(w, "%s\n%s\n\nstatus: %s\ntype: %s\npriority: %d\nassignee: %s\n", issue.ID, issue.Title, issue.Status, issue.IssueType, issue.Priority, emptyDash(issue.Assignee)); err != nil {
		return err
	}
	if issue.Description != "" {
		if _, err := fmt.Fprintf(w, "\ndescription:\n%s\n", issue.Description); err != nil {
			return err
		}
	}
	if detail.Parent != nil {
		if _, err := fmt.Fprintf(w, "\nparent:\n- %s %s\n", detail.Parent.ID, detail.Parent.Title); err != nil {
			return err
		}
	}
	if err := printIssueGroup(w, "children", detail.Children); err != nil {
		return err
	}
	if err := printIssueGroup(w, "depends_on", detail.DependsOn); err != nil {
		return err
	}
	if err := printIssueGroup(w, "blocked_by", detail.BlockedBy); err != nil {
		return err
	}
	if err := printIssueGroup(w, "related", detail.Related); err != nil {
		return err
	}
	if len(detail.Comments) > 0 {
		if _, err := fmt.Fprintln(w, "\ncomments:"); err != nil {
			return err
		}
		for _, c := range detail.Comments {
			if _, err := fmt.Fprintf(w, "- [%s] %s\n", c.CreatedBy, strings.ReplaceAll(c.Body, "\n", "\\n")); err != nil {
				return err
			}
		}
	}
	return nil
}

func printIssueGroup(w io.Writer, label string, issues []model.Issue) error {
	if len(issues) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(w, "\n%s:\n", label); err != nil {
		return err
	}
	for _, issue := range issues {
		if _, err := fmt.Fprintf(w, "- %s %s\n", issue.ID, issue.Title); err != nil {
			return err
		}
	}
	return nil
}

func emptyDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func splitCSV(input string) []string {
	if strings.TrimSpace(input) == "" {
		return nil
	}
	parts := strings.Split(input, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `links / lk

Usage:
  lk new --title <title> [--description <text>] [--type task|feature|bug|chore|epic] [--priority 0-4] [--assignee <user>] [--json]
  lk ls [--status open|closed] [--type <type>] [--assignee <user>] [--priority-min N] [--priority-max N] [--search <text>] [--ids a,b] [--has-comments] [--updated-after RFC3339] [--updated-before RFC3339] [--query <expr>] [--limit N] [--json]
  lk show <id> [--json]
  lk edit <id> [--title ...] [--description ...] [--type ...] [--status ...] [--priority ...] [--assignee ...|--clear-assignee] [--json]
  lk close <id> [--json]
  lk open <id> [--json]
  lk comment add <id> --body <text> [--by <user>] [--json]
  lk dep add <src-id> <dst-id> [--type blocks|parent-child|related-to] [--by <user>] [--json]
  lk dep rm <src-id> <dst-id> [--type blocks|parent-child|related-to]
  lk export
  lk beads import --db <path> [--json]
  lk beads export --db <path> [--json]
  lk workspace [--json]
`)
}

func splitArgs(args []string, positionalCount int) ([]string, []string) {
	positionals := make([]string, 0, positionalCount)
	flags := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			if !strings.Contains(arg, "=") && index+1 < len(args) && !strings.HasPrefix(args[index+1], "-") {
				flags = append(flags, args[index+1])
				index++
			}
			continue
		}
		if len(positionals) < positionalCount {
			positionals = append(positionals, arg)
			continue
		}
		flags = append(flags, arg)
	}
	return positionals, flags
}
