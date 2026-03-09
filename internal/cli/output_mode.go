package cli

import (
	"fmt"
	"io"
	"os"
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
	trimmedArgs, cliOutputMode, cliOutputSet, cliJSONShorthand, err := consumeOutputModeFlags(args)
	if err != nil {
		return nil, err
	}
	mode, err := resolveRequestedOutputMode(cliOutputMode, cliOutputSet, cliJSONShorthand)
	if err != nil {
		return nil, err
	}
	return applyResolvedOutputMode(trimmedArgs, mode, stdout), nil
}

func commandSupportsOutputMode(command string) bool {
	switch command {
	case "new", "ls", "show", "edit", "close", "open", "archive", "delete", "unarchive", "restore",
		"comment", "label", "parent", "children", "dep", "export", "beads", "workspace", "doctor", "fsck",
		"backup", "recover", "bulk", "sync", "hooks", "quickstart":
		return true
	default:
		return false
	}
}

func consumeOutputModeFlags(args []string) ([]string, outputMode, bool, bool, error) {
	trimmed := make([]string, 0, len(args))
	var cliOutputMode outputMode
	cliOutputSet := false
	cliJSONShorthand := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--json" {
			cliJSONShorthand = true
			trimmed = append(trimmed, arg)
			continue
		}
		if strings.HasPrefix(arg, "--output=") {
			value := strings.TrimSpace(strings.TrimPrefix(arg, "--output="))
			mode, err := parseOutputMode(value)
			if err != nil {
				return nil, "", false, false, err
			}
			cliOutputMode = mode
			cliOutputSet = true
			continue
		}
		if arg == "--output" {
			if index+1 >= len(args) {
				return nil, "", false, false, fmt.Errorf("usage: missing value for --output (expected auto|json|text)")
			}
			value := strings.TrimSpace(args[index+1])
			mode, err := parseOutputMode(value)
			if err != nil {
				return nil, "", false, false, err
			}
			cliOutputMode = mode
			cliOutputSet = true
			index++
			continue
		}
		trimmed = append(trimmed, arg)
	}
	return trimmed, cliOutputMode, cliOutputSet, cliJSONShorthand, nil
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

func resolveRequestedOutputMode(cliOutputMode outputMode, cliOutputSet bool, cliJSONShorthand bool) (outputMode, error) {
	// [LAW:single-enforcer] Output mode precedence is enforced once here for every command path.
	if cliOutputSet {
		return cliOutputMode, nil
	}
	if cliJSONShorthand {
		return outputModeJSON, nil
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
		if arg == "--json" {
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
