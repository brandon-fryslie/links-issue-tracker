package cli

import (
	"bytes"
	"reflect"
	"testing"
)

func TestNormalizeOutputModeArgs(t *testing.T) {
	nonTTY := &bytes.Buffer{}

	tests := []struct {
		name      string
		args      []string
		envOutput string
		want      []string
	}{
		{
			name: "auto defaults to json for non tty",
			args: []string{"ls"},
			want: []string{"ls", "--json"},
		},
		{
			name: "auto defaults to json for init command",
			args: []string{"init"},
			want: []string{"init", "--json"},
		},
		{
			name: "auto defaults to json for migrate command",
			args: []string{"migrate", "beads"},
			want: []string{"migrate", "beads", "--json"},
		},
		{
			name: "auto defaults to json for ready command",
			args: []string{"ready"},
			want: []string{"ready", "--json"},
		},
		{
			name:      "env text disables json in non tty",
			args:      []string{"ls"},
			envOutput: "text",
			want:      []string{"ls"},
		},
		{
			name:      "env json enables json in non tty",
			args:      []string{"ls"},
			envOutput: "json",
			want:      []string{"ls", "--json"},
		},
		{
			name:      "json shorthand beats env text",
			args:      []string{"ls", "--json"},
			envOutput: "text",
			want:      []string{"ls", "--json"},
		},
		{
			name:      "json false beats env json",
			args:      []string{"ls", "--json=false"},
			envOutput: "json",
			want:      []string{"ls"},
		},
		{
			name:      "output text beats json shorthand and env",
			args:      []string{"ls", "--output", "text", "--json"},
			envOutput: "json",
			want:      []string{"ls"},
		},
		{
			name:      "output json beats env text",
			args:      []string{"ls", "--output=json"},
			envOutput: "text",
			want:      []string{"ls", "--json"},
		},
		{
			name:      "output auto beats env text",
			args:      []string{"ls", "--output", "auto"},
			envOutput: "text",
			want:      []string{"ls", "--json"},
		},
		{
			name:      "completion command is not normalized",
			args:      []string{"completion", "zsh"},
			envOutput: "json",
			want:      []string{"completion", "zsh"},
		},
		{
			name: "title value that looks like output flag is preserved",
			args: []string{"new", "--title", "--output"},
			want: []string{"new", "--title", "--output", "--json"},
		},
		{
			name:      "double dash sentinel stops output parsing",
			args:      []string{"ls", "--", "--output", "text"},
			envOutput: "text",
			want:      []string{"ls", "--", "--output", "text"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if tc.envOutput == "" {
				t.Setenv(lkOutputEnv, "")
			} else {
				t.Setenv(lkOutputEnv, tc.envOutput)
			}
			got, err := normalizeOutputModeArgs(tc.args, nonTTY)
			if err != nil {
				t.Fatalf("normalizeOutputModeArgs() error = %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("normalizeOutputModeArgs() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNormalizeOutputModeArgsErrors(t *testing.T) {
	nonTTY := &bytes.Buffer{}

	t.Run("invalid cli output value", func(t *testing.T) {
		_, err := normalizeOutputModeArgs([]string{"ls", "--output", "nope"}, nonTTY)
		if err == nil {
			t.Fatalf("expected error for invalid --output")
		}
	})

	t.Run("invalid cli json value", func(t *testing.T) {
		_, err := normalizeOutputModeArgs([]string{"ls", "--json=nope"}, nonTTY)
		if err == nil {
			t.Fatalf("expected error for invalid --json")
		}
	})

	t.Run("missing cli output value", func(t *testing.T) {
		_, err := normalizeOutputModeArgs([]string{"ls", "--output"}, nonTTY)
		if err == nil {
			t.Fatalf("expected error for missing --output value")
		}
	})

	t.Run("invalid env output value", func(t *testing.T) {
		t.Setenv(lkOutputEnv, "wrong")
		_, err := normalizeOutputModeArgs([]string{"ls"}, nonTTY)
		if err == nil {
			t.Fatalf("expected error for invalid env output value")
		}
	})
}
