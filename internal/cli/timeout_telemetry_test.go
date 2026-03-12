package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmf/links-issue-tracker/internal/store"
)

func TestResolveOperationTimeoutDefaultsToFiveSeconds(t *testing.T) {
	t.Setenv(operationTimeoutEnvVar, "")

	timeout, err := resolveOperationTimeout()
	if err != nil {
		t.Fatalf("resolveOperationTimeout() error = %v", err)
	}
	if timeout != 5*time.Second {
		t.Fatalf("resolveOperationTimeout() = %s, want 5s", timeout)
	}
}

func TestOperationTimeoutErrorWritesTelemetry(t *testing.T) {
	repo := t.TempDir()
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("Chdir(repo) error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prevWD) })

	timeoutErr := operationTimeoutError([]string{"ls", "--json"}, 5*time.Second)
	if timeoutErr == nil {
		t.Fatal("operationTimeoutError() error = nil, want timeout error")
	}
	message := timeoutErr.Error()
	const marker = "wrote telemetry to "
	idx := strings.LastIndex(message, marker)
	if idx < 0 {
		t.Fatalf("operationTimeoutError() = %q, want telemetry marker", message)
	}
	telemetryPath := strings.TrimSpace(message[idx+len(marker):])
	if telemetryPath == "" {
		t.Fatalf("operationTimeoutError() = %q, parsed telemetry path is empty", message)
	}
	if !filepath.IsAbs(telemetryPath) {
		telemetryPath = filepath.Join(repo, telemetryPath)
	}
	payload, err := os.ReadFile(telemetryPath)
	if err != nil {
		t.Fatalf("ReadFile(telemetry) error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("Unmarshal(telemetry) error = %v", err)
	}
	if decoded["event"] != "operation_timeout" {
		t.Fatalf("telemetry event = %#v, want operation_timeout", decoded["event"])
	}
}

func TestHandleQueuedMutationErrorWritesJSON(t *testing.T) {
	var stdout bytes.Buffer

	err := handleQueuedMutationError(&stdout, true, store.MutationQueuedError{
		OperationID: "qop-123",
		Operation:   "create_issue",
	})
	if err != nil {
		t.Fatalf("handleQueuedMutationError() error = %v", err)
	}
	var payload map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("queued response should be json: %v", err)
	}
	if payload["status"] != "queued" {
		t.Fatalf("status = %q, want queued", payload["status"])
	}
	if payload["operation_id"] != "qop-123" {
		t.Fatalf("operation_id = %q, want qop-123", payload["operation_id"])
	}
	if payload["operation"] != "create_issue" {
		t.Fatalf("operation = %q, want create_issue", payload["operation"])
	}
}

func TestHandleQueuedMutationErrorPassesThroughNonQueued(t *testing.T) {
	var stdout bytes.Buffer
	original := errors.New("boom")

	err := handleQueuedMutationError(&stdout, true, original)
	if !errors.Is(err, original) {
		t.Fatalf("handleQueuedMutationError() = %v, want original error", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty output", stdout.String())
	}
}

func TestRunDoesNotEmitManagedTimeoutTelemetryForParentDeadline(t *testing.T) {
	t.Setenv(operationTimeoutEnvVar, "5s")
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-1*time.Second))
	defer cancel()

	var stdout bytes.Buffer
	err := Run(ctx, &stdout, &stdout, []string{"quickstart"})
	if err == nil {
		t.Fatal("Run() error = nil, want context deadline failure")
	}
	if strings.Contains(err.Error(), "wrote telemetry to") {
		t.Fatalf("Run() = %q, want parent deadline error without managed timeout telemetry", err.Error())
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want context.DeadlineExceeded", err)
	}
}
