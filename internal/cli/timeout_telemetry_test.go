package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/bmf/links-issue-tracker/internal/store"
)

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
