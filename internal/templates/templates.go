package templates

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmf/links-issue-tracker/internal/config"
)

const (
	AgentsSectionTemplateName = "agents-section.md"
	PrePushHookTemplateName   = "pre-push-hook.sh"
)

var (
	//go:embed defaults/*
	defaultsFS embed.FS

	defaultTemplateNames = []string{
		AgentsSectionTemplateName,
		PrePushHookTemplateName,
	}
)

// Load resolves a managed template with project > global > embedded precedence.
func Load(name string, workspaceRoot string) (string, error) {
	projectPath := projectTemplatePath(workspaceRoot, name)
	globalPath := globalTemplatePath(name)

	// [LAW:dataflow-not-control-flow] Every source is always read in a fixed order; only values decide selection.
	projectContent, projectErr := readOptionalFile(projectPath)
	globalContent, globalErr := readOptionalFile(globalPath)
	embeddedContent, embeddedErr := readEmbedded(name)

	resolved := firstNonEmpty(projectContent, globalContent, embeddedContent)

	if projectErr != nil {
		return "", fmt.Errorf("load project template %s: %w", projectPath, projectErr)
	}
	if globalErr != nil {
		return "", fmt.Errorf("load global template %s: %w", globalPath, globalErr)
	}
	if embeddedErr != nil {
		return "", fmt.Errorf("load embedded template %s: %w", name, embeddedErr)
	}
	if resolved == "" {
		return "", fmt.Errorf("load template %s: no non-empty source", name)
	}
	return resolved, nil
}

// EmbeddedDefault returns the embedded template content for name.
func EmbeddedDefault(name string) string {
	content, err := readEmbedded(name)
	if err != nil {
		return ""
	}
	return content
}

// SeedGlobalDefaults writes embedded defaults into the global template directory.
func SeedGlobalDefaults() error {
	templatesDir := globalTemplatesDir()
	if strings.TrimSpace(templatesDir) == "" {
		return nil
	}
	if err := os.MkdirAll(templatesDir, 0o755); err != nil {
		return fmt.Errorf("create templates dir %s: %w", templatesDir, err)
	}
	for _, name := range defaultTemplateNames {
		content, err := readEmbedded(name)
		if err != nil {
			return fmt.Errorf("read embedded template %s: %w", name, err)
		}
		path := filepath.Join(templatesDir, name)
		if err := writeFileIfMissing(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("seed template %s: %w", path, err)
		}
	}
	return nil
}

func readOptionalFile(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return string(content), nil
}

func readEmbedded(name string) (string, error) {
	content, err := defaultsFS.ReadFile(filepath.Join("defaults", name))
	if err != nil {
		return "", err
	}
	return string(content), nil
}

func writeFileIfMissing(path string, content []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return err
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return nil
}

func projectTemplatePath(workspaceRoot string, name string) string {
	trimmedRoot := strings.TrimSpace(workspaceRoot)
	if trimmedRoot == "" {
		return ""
	}
	return filepath.Join(trimmedRoot, ".lit", "templates", name)
}

func globalTemplatesDir() string {
	// [LAW:one-source-of-truth] Global template storage reuses config.ConfigDir as the canonical root.
	root := strings.TrimSpace(config.ConfigDir())
	if root == "" {
		return ""
	}
	return filepath.Join(root, "templates")
}

func globalTemplatePath(name string) string {
	dir := globalTemplatesDir()
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	return filepath.Join(dir, name)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
