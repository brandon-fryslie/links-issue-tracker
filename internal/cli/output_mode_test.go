package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestParseGlobalOutputMode(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantArgs []string
		wantMode outputMode
	}{
		{
			name:     "default mode is text",
			args:     []string{"quickstart"},
			wantArgs: []string{"quickstart"},
			wantMode: outputModeText,
		},
		{
			name:     "exact global json flag enables json mode",
			args:     []string{"--json", "quickstart"},
			wantArgs: []string{"quickstart"},
			wantMode: outputModeJSON,
		},
		{
			name:     "command args that look like removed output flags are preserved",
			args:     []string{"new", "--title", "--output"},
			wantArgs: []string{"new", "--title", "--output"},
			wantMode: outputModeText,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			gotArgs, gotMode, err := parseGlobalOutputMode(tc.args, &bytes.Buffer{})
			if err != nil {
				t.Fatalf("parseGlobalOutputMode() error = %v", err)
			}
			if gotMode != tc.wantMode {
				t.Fatalf("mode = %q, want %q", gotMode, tc.wantMode)
			}
			if !reflect.DeepEqual(gotArgs, tc.wantArgs) {
				t.Fatalf("args = %#v, want %#v", gotArgs, tc.wantArgs)
			}
		})
	}
}

func TestParseGlobalOutputModeErrors(t *testing.T) {
	t.Run("removed output flag is rejected", func(t *testing.T) {
		_, _, err := parseGlobalOutputMode([]string{"--output", "json", "quickstart"}, &bytes.Buffer{})
		if err == nil {
			t.Fatalf("expected error for removed --output")
		}
		if !strings.Contains(err.Error(), "--output is no longer supported") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("json flag values are rejected", func(t *testing.T) {
		_, _, err := parseGlobalOutputMode([]string{"--json=false", "quickstart"}, &bytes.Buffer{})
		if err == nil {
			t.Fatalf("expected error for unsupported --json value")
		}
		if !strings.Contains(err.Error(), "--json does not accept a value") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestRunQuickstartDefaultsToTextOnNonTTY(t *testing.T) {
	var stdout bytes.Buffer
	if err := Run(context.Background(), &stdout, &stdout, []string{"quickstart"}); err != nil {
		t.Fatalf("Run(quickstart) error = %v", err)
	}
	if strings.Contains(stdout.String(), "\"summary\"") {
		t.Fatalf("quickstart default output should be text: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Agent quickstart for links issue tracking") {
		t.Fatalf("quickstart text output missing summary: %q", stdout.String())
	}
}

func TestRunQuickstartJSONFlagEnablesJSON(t *testing.T) {
	var stdout bytes.Buffer
	if err := Run(context.Background(), &stdout, &stdout, []string{"--json", "quickstart"}); err != nil {
		t.Fatalf("Run(--json quickstart) error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected json output when --json is set: %v", err)
	}
}

func TestRunRejectsCommandLocalJSONFalse(t *testing.T) {
	var stdout bytes.Buffer
	err := Run(context.Background(), &stdout, &stdout, []string{"quickstart", "--json=false"})
	if err == nil {
		t.Fatal("expected error for command-local --json=false")
	}
	if !strings.Contains(err.Error(), "--json does not accept a value") {
		t.Fatalf("unexpected error: %v", err)
	}
}
