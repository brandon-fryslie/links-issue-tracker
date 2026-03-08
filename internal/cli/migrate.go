package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bmf/links-issue-tracker/internal/workspace"
)

var beadsWordRegex = regexp.MustCompile(`(?i)\bbeads\b`)

type migrateBeadsReport struct {
	Mode               string   `json:"mode"`
	Applied            bool     `json:"applied"`
	HooksDir           string   `json:"hooks_dir"`
	HookFilesScanned   int      `json:"hook_files_scanned"`
	BeadsHookFiles     []string `json:"beads_hook_files"`
	HookFilesModified  []string `json:"hook_files_modified"`
	HookFilesRemoved   []string `json:"hook_files_removed"`
	AgentsPath         string   `json:"agents_path"`
	AgentsFound        bool     `json:"agents_found"`
	AgentsBefore       int      `json:"agents_mentions_before"`
	AgentsAfter        int      `json:"agents_mentions_after"`
	AgentsUpdated      bool     `json:"agents_updated"`
	LitHookInstalled   bool     `json:"lit_hook_installed"`
	LitHookPath        string   `json:"lit_hook_path,omitempty"`
	LitLegacyChainPath string   `json:"lit_legacy_chain_path,omitempty"`
	Notes              []string `json:"notes,omitempty"`
}

func runMigrate(stdout io.Writer, ws workspace.Info, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: lit migrate beads [--apply] [--json]")
	}
	switch args[0] {
	case "beads":
		return runMigrateBeads(stdout, ws, args[1:])
	default:
		return errors.New("usage: lit migrate beads [--apply] [--json]")
	}
}

func runMigrateBeads(stdout io.Writer, ws workspace.Info, args []string) error {
	fs := flag.NewFlagSet("migrate beads", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	applyChanges := fs.Bool("apply", false, "Apply migration changes (default: dry-run)")
	jsonOut := fs.Bool("json", false, "Output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: lit migrate beads [--apply] [--json]")
	}

	report, err := migrateBeads(ws, *applyChanges)
	if err != nil {
		return err
	}
	return printValue(stdout, report, *jsonOut, func(w io.Writer, v any) error {
		r := v.(migrateBeadsReport)
		_, printErr := fmt.Fprintf(
			w,
			"mode=%s scanned=%d beads_hooks=%d modified=%d removed=%d agents_updated=%t lit_hook_installed=%t\n",
			r.Mode,
			r.HookFilesScanned,
			len(r.BeadsHookFiles),
			len(r.HookFilesModified),
			len(r.HookFilesRemoved),
			r.AgentsUpdated,
			r.LitHookInstalled,
		)
		return printErr
	})
}

func migrateBeads(ws workspace.Info, applyChanges bool) (migrateBeadsReport, error) {
	mode := "dry-run"
	if applyChanges {
		mode = "apply"
	}
	report := migrateBeadsReport{
		Mode:     mode,
		Applied:  applyChanges,
		HooksDir: filepath.Join(ws.GitCommonDir, "hooks"),
	}

	entries, err := os.ReadDir(report.HooksDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			report.Notes = append(report.Notes, "hooks directory does not exist yet")
		} else {
			return report, fmt.Errorf("read hooks dir: %w", err)
		}
	} else {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			path := filepath.Join(report.HooksDir, entry.Name())
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				return report, fmt.Errorf("read hook %s: %w", path, readErr)
			}
			report.HookFilesScanned++
			if !beadsWordRegex.Match(content) {
				continue
			}
			report.BeadsHookFiles = append(report.BeadsHookFiles, path)
			cleaned, removeFile, changed := stripBeadsHookMentions(content)
			if !changed {
				continue
			}
			if removeFile {
				report.HookFilesRemoved = append(report.HookFilesRemoved, path)
				if applyChanges {
					if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
						return report, fmt.Errorf("remove hook %s: %w", path, removeErr)
					}
				}
				continue
			}
			report.HookFilesModified = append(report.HookFilesModified, path)
			if applyChanges {
				info, statErr := os.Stat(path)
				if statErr != nil {
					return report, fmt.Errorf("stat hook %s: %w", path, statErr)
				}
				if writeErr := os.WriteFile(path, cleaned, info.Mode().Perm()); writeErr != nil {
					return report, fmt.Errorf("write hook %s: %w", path, writeErr)
				}
			}
		}
	}

	// [LAW:one-source-of-truth] AGENTS.md in repo root is the single migration target for agent instructions.
	report.AgentsPath = filepath.Join(ws.RootDir, "AGENTS.md")
	agentsContent, agentsErr := os.ReadFile(report.AgentsPath)
	if agentsErr == nil {
		report.AgentsFound = true
		report.AgentsBefore = countRegexMatches(beadsWordRegex, agentsContent)
		updated := replaceBeadsWords(string(agentsContent))
		report.AgentsAfter = countRegexMatches(beadsWordRegex, []byte(updated))
		report.AgentsUpdated = updated != string(agentsContent)
		if report.AgentsUpdated && applyChanges {
			if writeErr := os.WriteFile(report.AgentsPath, []byte(updated), 0o644); writeErr != nil {
				return report, fmt.Errorf("write AGENTS.md: %w", writeErr)
			}
		}
	} else if errors.Is(agentsErr, os.ErrNotExist) {
		report.Notes = append(report.Notes, "AGENTS.md not found in repo root")
	} else {
		return report, fmt.Errorf("read AGENTS.md: %w", agentsErr)
	}

	if applyChanges {
		// [LAW:single-enforcer] Migration reuses installHooks to keep hook installation logic owned in one place.
		hookResult, hookErr := installHooks(ws)
		if hookErr != nil {
			return report, hookErr
		}
		report.LitHookInstalled = true
		report.LitHookPath = hookResult.HookPath
		report.LitLegacyChainPath = hookResult.LegacyPath
	} else {
		report.Notes = append(report.Notes, "dry-run: lit hooks not installed; rerun with --apply")
	}

	return report, nil
}

func stripBeadsHookMentions(content []byte) ([]byte, bool, bool) {
	original := string(content)
	lines := strings.Split(original, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if beadsWordRegex.MatchString(line) {
			continue
		}
		kept = append(kept, line)
	}
	normalized := normalizeBlankLines(strings.Join(kept, "\n"))
	changed := normalized != original
	if !changed {
		return content, false, false
	}
	if !hasHookLogic(normalized) {
		return []byte{}, true, true
	}
	if !strings.HasSuffix(normalized, "\n") {
		normalized += "\n"
	}
	return []byte(normalized), false, true
}

func normalizeBlankLines(input string) string {
	lines := strings.Split(input, "\n")
	out := make([]string, 0, len(lines))
	previousBlank := false
	for _, line := range lines {
		blank := strings.TrimSpace(line) == ""
		if blank && previousBlank {
			continue
		}
		out = append(out, line)
		previousBlank = blank
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

func hasHookLogic(input string) bool {
	lines := strings.Split(input, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#!") || strings.HasPrefix(trimmed, "#") {
			continue
		}
		return true
	}
	return false
}

func replaceBeadsWords(input string) string {
	return beadsWordRegex.ReplaceAllStringFunc(input, func(token string) string {
		switch token {
		case strings.ToUpper(token):
			return "LIT"
		case strings.ToLower(token):
			return "lit"
		case capitalize(strings.ToLower(token)):
			return "Lit"
		default:
			return "lit"
		}
	})
}

func countRegexMatches(pattern *regexp.Regexp, content []byte) int {
	return len(pattern.FindAll(content, -1))
}

func capitalize(input string) string {
	if input == "" {
		return input
	}
	return strings.ToUpper(input[:1]) + input[1:]
}
