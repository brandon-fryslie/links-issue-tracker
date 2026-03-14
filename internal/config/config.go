package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// Config holds user-level settings loaded from ~/.config/links-issue-tracker/config.toml.
type Config struct {
	Logging   LoggingConfig   `mapstructure:"logging"`
	Init      InitConfig      `mapstructure:"init"`
	Migration MigrationConfig `mapstructure:"migration"`
	Ready     ReadyConfig     `mapstructure:"ready"`
}

type LoggingConfig struct {
	Verbose bool   `mapstructure:"verbose"`
	File    string `mapstructure:"file"`
}

type InitConfig struct {
	InstallHooks  bool `mapstructure:"install_hooks"`
	InstallAgents bool `mapstructure:"install_agents"`
}

type MigrationConfig struct {
	AutoApply bool `mapstructure:"auto_apply"`
}

type ReadyConfig struct {
	RequiredFields []string `mapstructure:"required_fields"`
}

const (
	globalConfigPathEnv  = "LIT_CONFIG_GLOBAL_PATH"
	projectConfigPathEnv = "LIT_CONFIG_PROJECT_PATH"
)

// configDir returns the directory where the config file lives.
func configDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "links-issue-tracker")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "links-issue-tracker")
}

// Load reads config from ~/.config/links-issue-tracker/config.toml and
// optionally from <workspace>/.lit/config.toml when a workspace root is given.
// A missing file is not an error; defaults are returned.
func Load(workspaceRoot ...string) Config {
	v := viper.New()

	v.SetDefault("logging.verbose", false)
	v.SetDefault("logging.file", "")
	v.SetDefault("init.install_hooks", true)
	v.SetDefault("init.install_agents", true)
	v.SetDefault("migration.auto_apply", false)
	v.SetDefault("ready.required_fields", []string{})

	// [LAW:single-enforcer] Global/project config precedence is resolved once at load time.
	globalRequired := mergeConfigFile(v, globalConfigPath())
	projectRequired := []string{}
	if root := strings.TrimSpace(first(workspaceRoot)); root != "" {
		projectRequired = mergeConfigFile(v, projectConfigPath(root))
	}

	var cfg Config
	_ = v.Unmarshal(&cfg)
	mergedRequired := append(globalRequired, projectRequired...)
	if len(mergedRequired) == 0 {
		mergedRequired = cfg.Ready.RequiredFields
	}
	// [LAW:one-source-of-truth] Required fields are normalized once into a single canonical list.
	cfg.Ready.RequiredFields = normalizeRequiredFields(mergedRequired)
	return cfg
}

func globalConfigPath() string {
	if override := strings.TrimSpace(os.Getenv(globalConfigPathEnv)); override != "" {
		return override
	}
	dir := configDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "config.toml")
}

func projectConfigPath(workspaceRoot string) string {
	if override := strings.TrimSpace(os.Getenv(projectConfigPathEnv)); override != "" {
		return override
	}
	return filepath.Join(workspaceRoot, ".lit", "config.toml")
}

func mergeConfigFile(v *viper.Viper, path string) []string {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return nil
	}
	fileConfig := viper.New()
	fileConfig.SetConfigFile(trimmedPath)
	if err := fileConfig.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if errors.As(err, &notFound) || os.IsNotExist(err) {
			return nil
		}
		return nil
	}
	_ = v.MergeConfigMap(fileConfig.AllSettings())
	required := fileConfig.GetStringSlice("ready.required_fields")
	required = append(required, fileConfig.GetStringSlice("required_fields")...)
	return required
}

func normalizeRequiredFields(fields []string) []string {
	seen := make(map[string]struct{}, len(fields))
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		normalized := strings.ToLower(strings.TrimSpace(field))
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
