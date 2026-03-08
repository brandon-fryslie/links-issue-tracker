package doltcli

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

const MinSupportedVersion = "1.81.10"

type Version struct {
	Major int
	Minor int
	Patch int
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

func (v Version) LessThan(other Version) bool {
	if v.Major != other.Major {
		return v.Major < other.Major
	}
	if v.Minor != other.Minor {
		return v.Minor < other.Minor
	}
	return v.Patch < other.Patch
}

var versionPattern = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

func ParseVersion(value string) (Version, error) {
	matches := versionPattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(matches) != 4 {
		return Version{}, fmt.Errorf("parse dolt version from %q", value)
	}
	major, err := strconv.Atoi(matches[1])
	if err != nil {
		return Version{}, err
	}
	minor, err := strconv.Atoi(matches[2])
	if err != nil {
		return Version{}, err
	}
	patch, err := strconv.Atoi(matches[3])
	if err != nil {
		return Version{}, err
	}
	return Version{Major: major, Minor: minor, Patch: patch}, nil
}

func InstalledVersion(ctx context.Context, cwd string) (Version, error) {
	output, err := runCommand(ctx, cwd, "version")
	if err != nil {
		return Version{}, err
	}
	return ParseVersion(output)
}

func RequireMinimumVersion(ctx context.Context, cwd string, minRequired string) (Version, error) {
	minVersion, err := ParseVersion(minRequired)
	if err != nil {
		return Version{}, fmt.Errorf("parse min required version: %w", err)
	}
	installed, err := InstalledVersion(ctx, cwd)
	if err != nil {
		return Version{}, err
	}
	if installed.LessThan(minVersion) {
		return Version{}, fmt.Errorf("dolt %s+ is required, found %s", minVersion.String(), installed.String())
	}
	return installed, nil
}

func Run(ctx context.Context, cwd string, args ...string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("dolt args are required")
	}
	return runCommand(ctx, cwd, args...)
}

func runCommand(ctx context.Context, cwd string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "dolt", args...)
	cmd.Dir = cwd
	output, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(output))
		if trimmed == "" {
			trimmed = err.Error()
		}
		return "", fmt.Errorf("dolt %s: %s", strings.Join(args, " "), trimmed)
	}
	return strings.TrimSpace(string(output)), nil
}
