package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderLinksAgentsSectionAgentNativeWorkflow(t *testing.T) {
	section := renderLinksAgentsSection()
	expectedSnippets := []string{
		linksAgentsBeginMarker,
		linksAgentsEndMarker,
		"Run `lit quickstart --json`.",
		"Check current ready work with `lit ready --json`.",
		"If no issue exists for the task, create one with `lit new ... --json`.",
		"Create a git commit for the completed work",
		"`git push` triggers hook-driven `lit sync push` attempts",
	}
	for _, snippet := range expectedSnippets {
		if !strings.Contains(section, snippet) {
			t.Fatalf("renderLinksAgentsSection() missing snippet %q", snippet)
		}
	}
}

func TestEnsureLinksAgentsSectionCreatesAndUpserts(t *testing.T) {
	root := t.TempDir()

	created, err := ensureLinksAgentsSection(root)
	if err != nil {
		t.Fatalf("ensureLinksAgentsSection(create) error = %v", err)
	}
	if !created.Created || !created.Changed {
		t.Fatalf("create result = %#v, want Created=true Changed=true", created)
	}
	initialBytes, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatalf("ReadFile(initial AGENTS.md) error = %v", err)
	}
	initial := string(initialBytes)
	if !strings.Contains(initial, "links Agent-Native Workflow") {
		t.Fatalf("initial AGENTS.md missing links section: %q", initial)
	}

	manual := strings.TrimSpace(`
# AGENTS

Project-specific guidance.

<!-- BEGIN LINKS INTEGRATION -->
legacy text
<!-- END LINKS INTEGRATION -->

Do not remove this footer.
`) + "\n"
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(manual), 0o644); err != nil {
		t.Fatalf("WriteFile(manual AGENTS.md) error = %v", err)
	}

	updated, err := ensureLinksAgentsSection(root)
	if err != nil {
		t.Fatalf("ensureLinksAgentsSection(update) error = %v", err)
	}
	if updated.Created || !updated.Changed {
		t.Fatalf("update result = %#v, want Created=false Changed=true", updated)
	}
	updatedBytes, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatalf("ReadFile(updated AGENTS.md) error = %v", err)
	}
	updatedText := string(updatedBytes)
	if strings.Contains(updatedText, "legacy text") {
		t.Fatalf("updated AGENTS.md should replace legacy managed section: %q", updatedText)
	}
	if !strings.Contains(updatedText, "Project-specific guidance.") || !strings.Contains(updatedText, "Do not remove this footer.") {
		t.Fatalf("updated AGENTS.md should preserve user content outside managed section: %q", updatedText)
	}

	unchanged, err := ensureLinksAgentsSection(root)
	if err != nil {
		t.Fatalf("ensureLinksAgentsSection(idempotent) error = %v", err)
	}
	if unchanged.Created || unchanged.Changed {
		t.Fatalf("idempotent result = %#v, want Created=false Changed=false", unchanged)
	}
}
