package cli

import (
	"strings"
	"testing"
)

func TestRenderLinksAgentsSectionOmitsTrivialIssueCreationAndJSONNoise(t *testing.T) {
	// [LAW:one-source-of-truth] The rendered template is the canonical AGENTS.md workflow text, so regressions are enforced here.
	section := renderLinksAgentsSection()

	if !strings.Contains(section, "Create or claim an issue only when the work needs tracking.") {
		t.Fatalf("rendered section missing tracked-work issue guidance: %q", section)
	}
	if !strings.Contains(section, "Do not create tickets for trivial drive-by edits like one-line doc fixes") {
		t.Fatalf("rendered section missing trivial-edit guidance: %q", section)
	}
	if strings.Contains(section, "If no issue exists for the task, create one with `lnks new ...`.") {
		t.Fatalf("rendered section still requires unconditional issue creation: %q", section)
	}
	if strings.Contains(section, "--json") {
		t.Fatalf("rendered section should not mention --json: %q", section)
	}
}
