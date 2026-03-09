package cli

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

const lkOutputEnv = "LK_OUTPUT"

type outputMode string

const (
	outputModeAuto outputMode = "auto"
	outputModeJSON outputMode = "json"
	outputModeText outputMode = "text"
)

func normalizeOutputModeArgs(args []string, stdout io.Writer) ([]string, error) {
	if len(args) == 0 || !commandSupportsOutputMode(args[0]) {
		return args, nil
	}
	trimmedArgs, cliOutputMode, cliOutputSet, cliJSONSet, cliJSONValue, err := consumeOutputModeFlags(args)
	if err != nil {
		return nil, err
	}
	mode, err := resolveRequestedOutputMode(cliOutputMode, cliOutputSet, cliJSONSet, cliJSONValue)
	if err != nil {
		return nil, err
	}
	return applyResolvedOutputMode(trimmedArgs, mode, stdout), nil
}

func commandSupportsOutputMode(command string) bool {
	switch command {
	case "init", "new", "ready", "ls", "show", "close", "open", "archive", "delete", "unarchive", "restore",
		"comment", "label", "parent", "children", "dep", "export", "beads", "workspace", "doctor", "fsck",
		"backup", "recover", "bulk", "sync", "hooks", "quickstart", "migrate":
		return true
	default:
		return false
	}
}

func consumeOutputModeFlags(args []string) ([]string, outputMode, bool, bool, bool, error) {
	trimmed := make([]string, 0, len(args))
	var cliOutputMode outputMode
	cliOutputSet := false
	cliJSONSet := false
	cliJSONValue := false
	pendingValue := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if pendingValue {
			trimmed = append(trimmed, arg)
			pendingValue = false
			continue
		}
		if arg == "--" {
			trimmed = append(trimmed, args[index:]...)
			break
		}
		if arg == "--json" {
			cliJSONSet = true
			cliJSONValue = true
			continue
		}
		if strings.HasPrefix(arg, "--json=") {
			parsed, err := parseJSONFlagValue(strings.TrimPrefix(arg, "--json="))
			if err != nil {
				return nil, "", false, false, false, err
			}
			cliJSONSet = true
			cliJSONValue = parsed
			continue
		}
		if strings.HasPrefix(arg, "--output=") {
			value := strings.TrimSpace(strings.TrimPrefix(arg, "--output="))
			mode, err := parseOutputMode(value)
			if err != nil {
				return nil, "", false, false, false, err
			}
			cliOutputMode = mode
			cliOutputSet = true
			continue
		}
		if arg == "--output" {
			if index+1 >= len(args) {
				return nil, "", false, false, false, fmt.Errorf("usage: missing value for --output (expected auto|json|text)")
			}
			value := strings.TrimSpace(args[index+1])
			mode, err := parseOutputMode(value)
			if err != nil {
				return nil, "", false, false, false, err
			}
			cliOutputMode = mode
			cliOutputSet = true
			index++
			continue
		}
		trimmed = append(trimmed, arg)
		pendingValue = tokenConsumesNextArg(arg)
	}
	return trimmed, cliOutputMode, cliOutputSet, cliJSONSet, cliJSONValue, nil
}

func parseOutputMode(value string) (outputMode, error) {
	mode := outputMode(strings.ToLower(strings.TrimSpace(value)))
	switch mode {
	case outputModeAuto, outputModeJSON, outputModeText:
		return mode, nil
	default:
		return "", fmt.Errorf("unsupported output mode %q (expected auto|json|text)", value)
	}
}

func parseJSONFlagValue(value string) (bool, error) {
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return false, fmt.Errorf("invalid --json value %q (expected true|false)", value)
	}
	return parsed, nil
}

func tokenConsumesNextArg(token string) bool {
	if !strings.HasPrefix(token, "-") || token == "--" || token == "-" {
		return false
	}
	if strings.HasPrefix(token, "--") {
		if strings.Contains(token, "=") {
			return false
		}
		name := strings.TrimPrefix(token, "--")
		if _, ok := knownBooleanFlags[name]; ok {
			return false
		}
		return true
	}
	// [LAW:dataflow-not-control-flow] Unknown short flags are treated as value-consuming to avoid reinterpreting following tokens.
	return true
}

var knownBooleanFlags = map[string]struct{}{
	"json":             {},
	"has-comments":     {},
	"include-archived": {},
	"include-deleted":  {},
	"clear-assignee":   {},
	"clear-labels":     {},
	"prune":            {},
	"set-upstream":     {},
	"force":            {},
	"repair":           {},
	"latest":           {},
	"latest-backup":    {},
	"skip-hooks":       {},
	"skip-agents":      {},
	"apply":            {},
}

func resolveRequestedOutputMode(cliOutputMode outputMode, cliOutputSet bool, cliJSONSet bool, cliJSONValue bool) (outputMode, error) {
	// [LAW:single-enforcer] Output mode precedence is enforced once here for every command path.
	if cliOutputSet {
		return cliOutputMode, nil
	}
	if cliJSONSet {
		if cliJSONValue {
			return outputModeJSON, nil
		}
		return outputModeText, nil
	}
	envValue := strings.TrimSpace(os.Getenv(lkOutputEnv))
	if envValue == "" {
		return outputModeAuto, nil
	}
	mode, err := parseOutputMode(envValue)
	if err != nil {
		return "", fmt.Errorf("invalid %s=%q: %w", lkOutputEnv, envValue, err)
	}
	return mode, nil
}

func applyResolvedOutputMode(args []string, mode outputMode, stdout io.Writer) []string {
	withoutJSON := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "--json" || strings.HasPrefix(arg, "--json=") {
			continue
		}
		withoutJSON = append(withoutJSON, arg)
	}
	if shouldRenderJSON(mode, stdout) {
		// [LAW:dataflow-not-control-flow] Commands keep their existing execution path; only normalized flag data varies.
		return append(withoutJSON, "--json")
	}
	return withoutJSON
}

func shouldRenderJSON(mode outputMode, stdout io.Writer) bool {
	switch mode {
	case outputModeJSON:
		return true
	case outputModeText:
		return false
	default:
		return !isTerminalWriter(stdout)
	}
}

func isTerminalWriter(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}
