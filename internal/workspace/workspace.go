package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrNotGitRepo = errors.New("links requires a git repository/worktree")

type Config struct {
	WorkspaceID string    `json:"workspace_id"`
	CreatedAt   time.Time `json:"created_at"`
	Version     int       `json:"schema_version"`
}

type Info struct {
	RootDir      string
	GitCommonDir string
	StorageDir   string
	ConfigPath   string
	DatabasePath string
	DoltRepoPath string
	WorkspaceID  string
}

type GitRemote struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

func Resolve(cwd string) (Info, error) {
	rootDir, err := gitOutput(cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return Info{}, ErrNotGitRepo
	}
	gitCommonDirRaw, err := gitOutput(cwd, "rev-parse", "--git-common-dir")
	if err != nil {
		return Info{}, ErrNotGitRepo
	}
	gitCommonDir := gitCommonDirRaw
	if !filepath.IsAbs(gitCommonDir) {
		gitCommonDir = filepath.Join(rootDir, gitCommonDirRaw)
	}
	storageDir := filepath.Join(filepath.Clean(gitCommonDir), "links")
	configPath := filepath.Join(storageDir, "config.json")
	databasePath := filepath.Join(storageDir, "dolt")
	doltRepoPath := filepath.Join(databasePath, "links")
	if err := os.MkdirAll(storageDir, 0o755); err != nil {
		return Info{}, fmt.Errorf("create storage dir: %w", err)
	}
	cfg, err := loadOrCreateConfig(configPath)
	if err != nil {
		return Info{}, err
	}
	return Info{
		RootDir:      rootDir,
		GitCommonDir: filepath.Clean(gitCommonDir),
		StorageDir:   storageDir,
		ConfigPath:   configPath,
		DatabasePath: databasePath,
		DoltRepoPath: doltRepoPath,
		WorkspaceID:  cfg.WorkspaceID,
	}, nil
}

func gitOutput(cwd string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func GitRemotes(cwd string) ([]GitRemote, error) {
	output, err := gitOutput(cwd, "remote", "-v")
	if err != nil {
		return nil, err
	}
	entries := strings.Split(strings.TrimSpace(output), "\n")
	byName := map[string]string{}
	for _, entry := range entries {
		line := strings.TrimSpace(entry)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		name := fields[0]
		url := fields[1]
		scope := strings.Trim(fields[2], "()")
		if scope != "fetch" {
			continue
		}
		byName[name] = url
	}
	remotes := make([]GitRemote, 0, len(byName))
	for name, url := range byName {
		remotes = append(remotes, GitRemote{Name: name, URL: url})
	}
	sort.Slice(remotes, func(i, j int) bool { return remotes[i].Name < remotes[j].Name })
	return remotes, nil
}

func loadOrCreateConfig(path string) (Config, error) {
	payload, err := os.ReadFile(path)
	if err == nil {
		var cfg Config
		if err := json.Unmarshal(payload, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse workspace config: %w", err)
		}
		if cfg.WorkspaceID == "" {
			return Config{}, errors.New("workspace config missing workspace_id")
		}
		return cfg, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("read workspace config: %w", err)
	}
	cfg := Config{
		WorkspaceID: uuid.NewString(),
		CreatedAt:   time.Now().UTC(),
		Version:     1,
	}
	payload, err = json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return Config{}, fmt.Errorf("marshal workspace config: %w", err)
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		return Config{}, fmt.Errorf("write workspace config: %w", err)
	}
	return cfg, nil
}
