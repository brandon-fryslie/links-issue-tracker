package main

import (
	"context"
	"fmt"
	"os"

	"github.com/bmf/links-issue-tracker/internal/cli"
)

func main() {
	if err := cli.Run(context.Background(), os.Stdout, os.Stderr, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
