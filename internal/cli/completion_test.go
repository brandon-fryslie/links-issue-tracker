package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestCompletionScriptsRender(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		var stdout bytes.Buffer
		if err := runCompletion(&stdout, []string{shell}); err != nil {
			t.Fatalf("runCompletion(%q) error = %v", shell, err)
		}
		if !strings.Contains(stdout.String(), "lk") {
			t.Fatalf("completion output for %q missing lk command name: %q", shell, stdout.String())
		}
	}
}

func TestRunHelpIncludesCompletion(t *testing.T) {
	var stdout bytes.Buffer
	if err := Run(context.Background(), &stdout, &stdout, []string{"help"}); err != nil {
		t.Fatalf("Run(help) error = %v", err)
	}
	if !strings.Contains(stdout.String(), "lk completion <bash|zsh|fish>") {
		t.Fatalf("help output missing completion command: %q", stdout.String())
	}
}
