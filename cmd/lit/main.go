package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/bmf/links-issue-tracker/internal/cli"
)

func main() {
	if err := cli.Run(context.Background(), os.Stdout, os.Stderr, os.Args[1:]); err != nil {
		exitCode := cli.ExitCode(err)
		if os.Getenv("LIT_ERROR_JSON") == "1" {
			payload := cli.ErrorPayload(err)
			payload["error"].(map[string]any)["exit_code"] = exitCode
			_ = json.NewEncoder(os.Stderr).Encode(payload)
		} else {
			fmt.Fprintf(os.Stderr, "error (code=%d): %v\n", exitCode, err)
		}
		os.Exit(exitCode)
	}
}
