package audit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"testing"

	"mediahub_oss/internal/logging/audit"
)

func TestAlStdout_IgnoresAppLoggerLevelAndEmitsStructuredJSON(t *testing.T) {
	var appBuf bytes.Buffer
	appLogger := slog.New(slog.NewJSONHandler(&appBuf, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	// Redirect os.Stdout to verify NewAlStdout writes directly to stdout regardless of appLogger level.
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create stdout pipe: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() {
		os.Stdout = origStdout
		_ = r.Close()
		_ = w.Close()
	})

	al := audit.NewAlStdout(appLogger)
	al.Log(context.Background(), "USER_LOGIN", "alice", "auth/token", map[string]any{
		"ip":     "127.0.0.1",
		"method": "password",
	})

	if err := w.Close(); err != nil {
		t.Fatalf("failed to close write pipe: %v", err)
	}
	var stdoutBuf bytes.Buffer
	if _, err := io.Copy(&stdoutBuf, r); err != nil {
		t.Fatalf("failed to read from stdout pipe: %v", err)
	}

	if appBuf.Len() != 0 {
		t.Fatalf("expected app logger buffer to stay empty, got %q", appBuf.String())
	}

	var parsed map[string]any
	if err := json.Unmarshal(stdoutBuf.Bytes(), &parsed); err != nil {
		t.Fatalf("expected valid JSON output from AlStdout, got %q: %v", stdoutBuf.String(), err)
	}
	if parsed["level"] != "INFO" {
		t.Errorf("expected level INFO, got %v", parsed["level"])
	}
	if parsed["msg"] != "AUDIT EVENT" {
		t.Errorf("expected msg 'AUDIT EVENT', got %v", parsed["msg"])
	}
	if parsed["audit_action"] != "USER_LOGIN" {
		t.Errorf("expected audit_action USER_LOGIN, got %v", parsed["audit_action"])
	}
	if parsed["audit_actor"] != "alice" {
		t.Errorf("expected audit_actor alice, got %v", parsed["audit_actor"])
	}
	if parsed["audit_resource"] != "auth/token" {
		t.Errorf("expected audit_resource auth/token, got %v", parsed["audit_resource"])
	}
	detail, ok := parsed["detail"].(map[string]any)
	if !ok {
		t.Fatalf("expected detail object in JSON output, got %T (%v)", parsed["detail"], parsed["detail"])
	}
	if detail["ip"] != "127.0.0.1" || detail["method"] != "password" {
		t.Errorf("unexpected detail map: %+v", detail)
	}
}

func TestNewAlStdoutWithWriter_EmitsJSON(t *testing.T) {
	var buf bytes.Buffer
	al := audit.NewAlStdoutWithWriter(&buf)

	al.Log(context.Background(), "ENTRY_CREATE", "bob", "db/1", map[string]any{"entry_id": 42})

	var parsed map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("expected valid JSON, got %q: %v", buf.String(), err)
	}
	if parsed["audit_action"] != "ENTRY_CREATE" || parsed["audit_actor"] != "bob" || parsed["audit_resource"] != "db/1" {
		t.Errorf("unexpected audit fields: %+v", parsed)
	}
	detail, ok := parsed["detail"].(map[string]any)
	if !ok || detail["entry_id"] != float64(42) {
		t.Errorf("unexpected detail field: %v", parsed["detail"])
	}
}
