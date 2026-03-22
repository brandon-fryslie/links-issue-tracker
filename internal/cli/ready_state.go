package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/bmf/links-issue-tracker/internal/annotation"
	"github.com/bmf/links-issue-tracker/internal/app"
	"github.com/bmf/links-issue-tracker/internal/model"
	"github.com/bmf/links-issue-tracker/internal/store"
)

// readyBlockingKinds defines which annotation kinds block readiness.
// [LAW:one-source-of-truth] Single definition of what "blocks readiness" for the ready command.
var readyBlockingKinds = []annotation.Kind{
	annotation.MissingField,
	annotation.BlockedBy,
}

func isReadyBlocked(annotations []annotation.Annotation) bool {
	return annotation.HasAny(annotations, readyBlockingKinds...)
}

// newFieldAnnotator validates requiredFields against model.Issue JSON fields,
// then returns an annotator that checks those fields on each issue.
func newFieldAnnotator(requiredFields []string) (annotation.Annotator, error) {
	validFields := issueJSONFieldNames()
	for _, field := range requiredFields {
		if _, ok := validFields[field]; !ok {
			return nil, fmt.Errorf("required field %q does not exist on issue", field)
		}
	}
	return func(_ context.Context, issue model.Issue) ([]annotation.Annotation, error) {
		fields, err := issueFieldValues(issue)
		if err != nil {
			return nil, err
		}
		var annotations []annotation.Annotation
		for _, field := range requiredFields {
			if !isRequiredFieldSet(fields[field]) {
				annotations = append(annotations, annotation.Annotation{
					Kind:    annotation.MissingField,
					Message: field,
				})
			}
		}
		return annotations, nil
	}, nil
}

// newBlockerAnnotator returns an annotator that checks open dependency blockers
// and flags priority inversions where a blocker has worse priority than the dependent.
func newBlockerAnnotator(st *store.Store) annotation.Annotator {
	// [LAW:dataflow-not-control-flow] Dependency lookup runs for every issue;
	// empty blockers list means no annotations, not a skipped operation.
	return func(ctx context.Context, issue model.Issue) ([]annotation.Annotation, error) {
		detail, err := st.GetIssueDetail(ctx, issue.ID)
		if err != nil {
			return nil, err
		}
		var annotations []annotation.Annotation
		for _, dep := range detail.DependsOn {
			if dep.Status == "closed" {
				continue
			}
			annotations = append(annotations, annotation.Annotation{
				Kind:    annotation.BlockedBy,
				Message: dep.ID,
			})
			if dep.Priority > issue.Priority {
				annotations = append(annotations, annotation.Annotation{
					Kind:    annotation.PriorityInversion,
					Message: fmt.Sprintf("%s (priority %d)", dep.ID, dep.Priority),
				})
			}
		}
		return annotations, nil
	}
}

func issueJSONFieldNames() map[string]struct{} {
	// [LAW:one-source-of-truth] model.Issue JSON tags are the canonical ready-field schema.
	issueType := reflect.TypeOf(model.Issue{})
	fields := make(map[string]struct{}, issueType.NumField())
	for i := 0; i < issueType.NumField(); i++ {
		field := issueType.Field(i)
		if !field.IsExported() {
			continue
		}
		name := issueJSONFieldName(field)
		if name == "" {
			continue
		}
		fields[name] = struct{}{}
	}
	return fields
}

func issueJSONFieldName(field reflect.StructField) string {
	tag, ok := field.Tag.Lookup("json")
	if !ok {
		return field.Name
	}
	name, _, _ := strings.Cut(tag, ",")
	switch name {
	case "":
		return field.Name
	case "-":
		return ""
	default:
		return name
	}
}

func issueFieldValues(issue model.Issue) (map[string]any, error) {
	payload, err := json.Marshal(issue)
	if err != nil {
		return nil, fmt.Errorf("marshal issue fields: %w", err)
	}
	values := map[string]any{}
	if err := json.Unmarshal(payload, &values); err != nil {
		return nil, fmt.Errorf("unmarshal issue fields: %w", err)
	}
	return values, nil
}

func isRequiredFieldSet(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(typed) != ""
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	default:
		return true
	}
}

// sortByReadiness places issues without blocking annotations first,
// preserving the original store ordering within each group.
func sortByReadiness(issues []annotation.AnnotatedIssue) {
	sort.SliceStable(issues, func(i, j int) bool {
		iBlocked := isReadyBlocked(issues[i].Annotations)
		jBlocked := isReadyBlocked(issues[j].Annotations)
		return !iBlocked && jBlocked
	})
}

func applyLimit(issues []annotation.AnnotatedIssue, limit int) []annotation.AnnotatedIssue {
	if limit <= 0 || len(issues) <= limit {
		return issues
	}
	return issues[:limit]
}

// readyBlockReason formats blocking annotations as a human-readable reason string.
// Only includes annotations whose Kind is in readyBlockingKinds.
func readyBlockReason(annotations []annotation.Annotation) string {
	reasons := make([]string, 0, len(annotations))
	for _, a := range annotations {
		if !annotation.HasAny([]annotation.Annotation{a}, readyBlockingKinds...) {
			continue
		}
		switch a.Kind {
		case annotation.MissingField:
			reasons = append(reasons, fmt.Sprintf("Field %s not set", a.Message))
		case annotation.BlockedBy:
			reasons = append(reasons, fmt.Sprintf("Blocked by ticket %s", a.Message))
		}
	}
	return strings.Join(reasons, "; ")
}

// printReadyOutput prints annotated issues in Ready / Not Ready sections.
// The partition is derived from the isReadyBlocked predicate at the render boundary.
func printReadyOutput(w io.Writer, format string, columns []string, issues []annotation.AnnotatedIssue) error {
	resolved := resolveColumns(columns)
	var ready, blocked []annotation.AnnotatedIssue
	for i := range issues {
		if isReadyBlocked(issues[i].Annotations) {
			blocked = append(blocked, issues[i])
		} else {
			ready = append(ready, issues[i])
		}
	}

	if _, err := fmt.Fprintln(w, "Ready"); err != nil {
		return err
	}
	if err := printAnnotatedRows(w, format, resolved, ready); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "Not Ready"); err != nil {
		return err
	}
	if err := printAnnotatedRows(w, format, resolved, blocked); err != nil {
		return err
	}
	return printPriorityInversions(w, issues)
}

func printAnnotatedRows(w io.Writer, format string, columns []string, issues []annotation.AnnotatedIssue) error {
	if len(issues) == 0 {
		_, err := fmt.Fprintln(w, "(none)")
		return err
	}
	hasReasons := false
	for _, issue := range issues {
		if readyBlockReason(issue.Annotations) != "" {
			hasReasons = true
			break
		}
	}
	switch format {
	case "", "lines":
		return printAnnotatedLines(w, issues, columns, hasReasons)
	case "table":
		return printAnnotatedTable(w, issues, columns, hasReasons)
	default:
		return fmt.Errorf("unsupported --format %q", format)
	}
}

func printAnnotatedLines(w io.Writer, issues []annotation.AnnotatedIssue, columns []string, hasReasons bool) error {
	for _, entry := range issues {
		line := formatIssueColumns(entry.Issue, columns, " | ")
		if hasReasons {
			if reason := readyBlockReason(entry.Annotations); reason != "" {
				line += " | " + reason
			}
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

func printAnnotatedTable(w io.Writer, issues []annotation.AnnotatedIssue, columns []string, hasReasons bool) error {
	headers := append([]string{}, columns...)
	if hasReasons {
		headers = append(headers, "reason")
	}
	tw := tabwriter.NewWriter(w, 2, 2, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, strings.ToUpper(strings.Join(headers, "\t"))); err != nil {
		return err
	}
	for _, entry := range issues {
		line := formatIssueColumns(entry.Issue, columns, "\t")
		if hasReasons {
			if reason := readyBlockReason(entry.Annotations); reason != "" {
				line += "\t" + reason
			}
		}
		if _, err := fmt.Fprintln(tw, line); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// printPriorityInversions prints a summary of priority inversions found across all issues.
func printPriorityInversions(w io.Writer, issues []annotation.AnnotatedIssue) error {
	type inversion struct {
		issueID   string
		issuePri  int
		blockerID string
	}
	var inversions []inversion
	for _, issue := range issues {
		for _, a := range issue.Annotations {
			if a.Kind != annotation.PriorityInversion {
				continue
			}
			inversions = append(inversions, inversion{
				issueID:   issue.ID,
				issuePri:  issue.Priority,
				blockerID: a.Message,
			})
		}
	}
	if len(inversions) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Priority inversions (%d):\n", len(inversions)); err != nil {
		return err
	}
	for _, inv := range inversions {
		if _, err := fmt.Fprintf(w, "  %s (P%d) blocked by %s — blocker is lower priority\n", inv.issueID, inv.issuePri, inv.blockerID); err != nil {
			return err
		}
	}
	return nil
}

func runFixPriority(ctx context.Context, stdout io.Writer, ap *app.App, args []string) error {
	positional, flagArgs := splitArgs(args, 1)
	fs := newCobraFlagSet("fix-priority")
	pullForward := fs.Bool("pull-forward", false, "Promote blockers to match this issue's priority")
	pushBack := fs.Bool("push-back", false, "Demote this issue to match its worst blocker's priority")
	jsonOut := fs.Bool("json", false, "Output JSON")
	if err := parseFlagSet(fs, flagArgs, stdout); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: lit fix-priority <issue-id> --pull-forward | --push-back")
	}
	if len(positional) != 1 {
		return fmt.Errorf("usage: lit fix-priority <issue-id> --pull-forward | --push-back")
	}
	if *pullForward == *pushBack {
		return fmt.Errorf("specify exactly one of --pull-forward or --push-back")
	}

	issue, err := ap.Store.GetIssue(ctx, positional[0])
	if err != nil {
		return err
	}

	writeJSON := shouldWriteJSON(stdout, *jsonOut)
	if *pushBack {
		return fixPriorityPushBack(ctx, stdout, ap, issue, writeJSON)
	}
	return fixPriorityPullForward(ctx, stdout, ap, issue, writeJSON)
}

type priorityChange struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	OldPriority int    `json:"old_priority"`
	NewPriority int    `json:"new_priority"`
}

// fixPriorityPushBack demotes the issue to match its worst direct blocker's priority.
func fixPriorityPushBack(ctx context.Context, stdout io.Writer, ap *app.App, issue model.Issue, jsonOut bool) error {
	detail, err := ap.Store.GetIssueDetail(ctx, issue.ID)
	if err != nil {
		return err
	}
	worstPri := issue.Priority
	for _, dep := range detail.DependsOn {
		if dep.Status == "closed" {
			continue
		}
		if dep.Priority > worstPri {
			worstPri = dep.Priority
		}
	}
	if worstPri == issue.Priority {
		return printValue(stdout, []priorityChange{}, jsonOut, printPriorityChangesAny)
	}
	updated, err := ap.Store.UpdateIssue(ctx, issue.ID, store.UpdateIssueInput{Priority: &worstPri})
	if err != nil {
		return err
	}
	changes := []priorityChange{{
		ID:          updated.ID,
		Title:       updated.Title,
		OldPriority: issue.Priority,
		NewPriority: worstPri,
	}}
	return printValue(stdout, changes, jsonOut, printPriorityChangesAny)
}

// fixPriorityPullForward promotes all blockers in the dependency chain
// to at least the issue's priority.
func fixPriorityPullForward(ctx context.Context, stdout io.Writer, ap *app.App, issue model.Issue, jsonOut bool) error {
	targetPri := issue.Priority
	var changes []priorityChange
	// [LAW:dataflow-not-control-flow] BFS walks the full dependency graph;
	// nodes that already meet the priority threshold produce no updates.
	queue := []string{issue.ID}
	visited := map[string]bool{issue.ID: true}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		detail, err := ap.Store.GetIssueDetail(ctx, current)
		if err != nil {
			return err
		}
		for _, dep := range detail.DependsOn {
			if dep.Status == "closed" || visited[dep.ID] {
				continue
			}
			visited[dep.ID] = true
			queue = append(queue, dep.ID)
			if dep.Priority <= targetPri {
				continue
			}
			updated, err := ap.Store.UpdateIssue(ctx, dep.ID, store.UpdateIssueInput{Priority: &targetPri})
			if err != nil {
				return err
			}
			changes = append(changes, priorityChange{
				ID:          updated.ID,
				Title:       updated.Title,
				OldPriority: dep.Priority,
				NewPriority: targetPri,
			})
		}
	}
	return printValue(stdout, changes, jsonOut, printPriorityChangesAny)
}

func printPriorityChangesAny(w io.Writer, v any) error {
	changes := v.([]priorityChange)
	if len(changes) == 0 {
		_, err := fmt.Fprintln(w, "No priority inversion found.")
		return err
	}
	for _, c := range changes {
		if _, err := fmt.Fprintf(w, "%s %q: P%d → P%d\n", c.ID, c.Title, c.OldPriority, c.NewPriority); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w, "%d issue(s) updated.\n", len(changes))
	return err
}
