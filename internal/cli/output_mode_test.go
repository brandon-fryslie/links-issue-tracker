package cli

import (
	"bytes"
	"context"
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

func TestRunQuickstartDefaultsToTextOnNonTTY(t *testing.T) {
	var stdout bytes.Buffer
	if err := Run(context.Background(), &stdout, &stdout, []string{"quickstart"}); err != nil {
		t.Fatalf("Run(quickstart) error = %v", err)
	}
	if strings.Contains(stdout.String(), "\"summary\"") {
		t.Fatalf("quickstart default output should be text: %q", stdout.String())
	}
	if strings.TrimSpace(stdout.String()) == "" {
		t.Fatal("quickstart text output is empty")
	}
}

func TestRunQuickstartRejectsJSONOutput(t *testing.T) {
	var stdout bytes.Buffer
	if err := Run(context.Background(), &stdout, &stdout, []string{"--json", "quickstart"}); err == nil {
		t.Fatal("Run(--json quickstart) unexpectedly succeeded")
	}
}

func TestRunQuickstartRejectsCommandLocalJSONFlag(t *testing.T) {
	var stdout bytes.Buffer
	if err := Run(context.Background(), &stdout, &stdout, []string{"quickstart", "--json"}); err == nil {
		t.Fatal("Run(quickstart --json) unexpectedly succeeded")
	}
}
