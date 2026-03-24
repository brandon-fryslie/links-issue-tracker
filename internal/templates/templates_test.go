package templates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadEmbeddedFallback(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	workspaceRoot := t.TempDir()
	content, err := Load(AgentsSectionTemplateName, workspaceRoot)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !strings.Contains(content, "BEGIN LINKS INTEGRATION") {
		t.Fatalf("embedded fallback missing marker: %q", content)
	}
}

func TestLoadGlobalOverride(t *testing.T) {
	xdgRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgRoot)

	globalTemplates := filepath.Join(xdgRoot, "links-issue-tracker", "templates")
	if err := os.MkdirAll(globalTemplates, 0o755); err != nil {
		t.Fatalf("MkdirAll(global templates) error = %v", err)
	}
	want := "custom global template\n"
	if err := os.WriteFile(filepath.Join(globalTemplates, AgentsSectionTemplateName), []byte(want), 0o644); err != nil {
		t.Fatalf("WriteFile(global template) error = %v", err)
	}

	got, err := Load(AgentsSectionTemplateName, t.TempDir())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got != want {
		t.Fatalf("Load() = %q, want %q", got, want)
	}
}

func TestLoadProjectOverrideWins(t *testing.T) {
	xdgRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgRoot)

	globalTemplates := filepath.Join(xdgRoot, "links-issue-tracker", "templates")
	if err := os.MkdirAll(globalTemplates, 0o755); err != nil {
		t.Fatalf("MkdirAll(global templates) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(globalTemplates, AgentsSectionTemplateName), []byte("global\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(global template) error = %v", err)
	}

	workspaceRoot := t.TempDir()
	projectTemplates := filepath.Join(workspaceRoot, ".lit", "templates")
	if err := os.MkdirAll(projectTemplates, 0o755); err != nil {
		t.Fatalf("MkdirAll(project templates) error = %v", err)
	}
	want := "project\n"
	if err := os.WriteFile(filepath.Join(projectTemplates, AgentsSectionTemplateName), []byte(want), 0o644); err != nil {
		t.Fatalf("WriteFile(project template) error = %v", err)
	}

	got, err := Load(AgentsSectionTemplateName, workspaceRoot)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got != want {
		t.Fatalf("Load() = %q, want %q", got, want)
	}
}

func TestLoadPropagatesFilesystemError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	workspaceRoot := t.TempDir()
	projectTemplatePath := filepath.Join(workspaceRoot, ".lit", "templates", AgentsSectionTemplateName)
	if err := os.MkdirAll(projectTemplatePath, 0o755); err != nil {
		t.Fatalf("MkdirAll(project template path as dir) error = %v", err)
	}

	_, err := Load(AgentsSectionTemplateName, workspaceRoot)
	if err == nil {
		t.Fatal("Load() expected filesystem error, got nil")
	}
}

func TestSeedGlobalDefaultsIdempotent(t *testing.T) {
	xdgRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgRoot)

	if err := SeedGlobalDefaults(); err != nil {
		t.Fatalf("SeedGlobalDefaults(first) error = %v", err)
	}

	templatesDir := filepath.Join(xdgRoot, "links-issue-tracker", "templates")
	hookPath := filepath.Join(templatesDir, PrePushHookTemplateName)
	custom := "custom hook\n"
	if err := os.WriteFile(hookPath, []byte(custom), 0o644); err != nil {
		t.Fatalf("WriteFile(custom hook) error = %v", err)
	}

	if err := SeedGlobalDefaults(); err != nil {
		t.Fatalf("SeedGlobalDefaults(second) error = %v", err)
	}

	content, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("ReadFile(hook template) error = %v", err)
	}
	if string(content) != custom {
		t.Fatalf("seed overwrote existing file: got %q, want %q", string(content), custom)
	}
}

func TestSeedGlobalDefaultsCreatesDirAndFiles(t *testing.T) {
	xdgRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgRoot)

	templatesDir := filepath.Join(xdgRoot, "links-issue-tracker", "templates")
	if _, err := os.Stat(templatesDir); !os.IsNotExist(err) {
		t.Fatalf("templates dir precondition failed: err=%v", err)
	}

	if err := SeedGlobalDefaults(); err != nil {
		t.Fatalf("SeedGlobalDefaults() error = %v", err)
	}

	for _, name := range []string{AgentsSectionTemplateName, PrePushHookTemplateName} {
		path := filepath.Join(templatesDir, name)
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", path, err)
		}
		if len(content) == 0 {
			t.Fatalf("seeded template %s is empty", path)
		}
	}
}
