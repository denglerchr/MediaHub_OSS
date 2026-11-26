package sqlite_test

import (
	"context"
	"testing"
	"time"

	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/repository/sqlite"
	"mediahub_oss/internal/shared"
)

func TestGetLogs_TimeFilterEpochAndHistorical(t *testing.T) {
	ctx := context.Background()

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	if err := r.MigrateUp(context.Background()); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	t1950 := time.Date(1950, 1, 1, 12, 0, 0, 0, time.UTC)
	t1970 := time.Unix(0, 0).UTC()
	t2025 := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	// Insert audit logs directly with distinct timestamps
	_, err = r.DB.ExecContext(ctx,
		`INSERT INTO audit_logs (timestamp, action, actor, resource, details) VALUES (?, ?, ?, ?, ?)`,
		t1950.UnixMilli(), "CREATE", "admin", "db1", `{"msg":"historical 1950 log"}`,
	)
	if err != nil {
		t.Fatalf("failed to insert 1950 log: %v", err)
	}

	_, err = r.DB.ExecContext(ctx,
		`INSERT INTO audit_logs (timestamp, action, actor, resource, details) VALUES (?, ?, ?, ?, ?)`,
		t1970.UnixMilli(), "CREATE", "admin", "db1", `{"msg":"epoch 1970 log"}`,
	)
	if err != nil {
		t.Fatalf("failed to insert 1970 log: %v", err)
	}

	_, err = r.DB.ExecContext(ctx,
		`INSERT INTO audit_logs (timestamp, action, actor, resource, details) VALUES (?, ?, ?, ?, ?)`,
		t2025.UnixMilli(), "CREATE", "admin", "db1", `{"msg":"future 2025 log"}`,
	)
	if err != nil {
		t.Fatalf("failed to insert 2025 log: %v", err)
	}

	// 1. Filter with TStart = 1940 and TEnd = 1970 -> Should return 1950 and 1970 logs
	t1940 := time.Date(1940, 1, 1, 0, 0, 0, 0, time.UTC)
	logs, err := r.GetLogs(ctx, repo.QueryOptions{
		TStart: t1940,
		TEnd:   t1970,
	})
	if err != nil {
		t.Fatalf("GetLogs failed: %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("expected 2 logs for range [1940, 1970], got %d", len(logs))
	}

	byMsg := make(map[string]repo.AuditLog, len(logs))
	for _, logEntry := range logs {
		if logEntry.Timestamp.IsZero() || logEntry.Timestamp.Year() == 1 {
			t.Fatalf("expected non-Year-1 timestamp for log %v, got %v", logEntry.Details, logEntry.Timestamp)
		}
		if msg, ok := logEntry.Details["msg"].(string); ok {
			byMsg[msg] = logEntry
		}
	}
	log1950, ok := byMsg["historical 1950 log"]
	if !ok {
		t.Fatalf("expected historical 1950 log in range [1940, 1970], got %v", logs)
	}
	if !log1950.Timestamp.Equal(t1950) {
		t.Errorf("expected 1950 log timestamp %v, got %v", t1950, log1950.Timestamp)
	}
	log1970, ok := byMsg["epoch 1970 log"]
	if !ok {
		t.Fatalf("expected epoch 1970 log in range [1940, 1970], got %v", logs)
	}
	if !log1970.Timestamp.Equal(t1970) {
		t.Errorf("expected 1970 log timestamp %v, got %v", t1970, log1970.Timestamp)
	}

	// 2. Filter with exact epoch timestamp TStart = 1970, TEnd = 1970 -> Should return only 1970 log
	logsEpoch, err := r.GetLogs(ctx, repo.QueryOptions{
		TStart: t1970,
		TEnd:   t1970,
	})
	if err != nil {
		t.Fatalf("GetLogs for epoch failed: %v", err)
	}
	if len(logsEpoch) != 1 || logsEpoch[0].Details["msg"] != "epoch 1970 log" {
		t.Fatalf("expected only epoch 1970 log, got %v", logsEpoch)
	}
	if logsEpoch[0].Timestamp.IsZero() || logsEpoch[0].Timestamp.Year() == 1 {
		t.Fatalf("expected non-Year-1 timestamp for epoch log, got %v", logsEpoch[0].Timestamp)
	}
	if !logsEpoch[0].Timestamp.Equal(t1970) {
		t.Errorf("expected epoch log timestamp %v, got %v", t1970, logsEpoch[0].Timestamp)
	}

	// 3. Log row stored with shared.MissingTimestamp sentinel scans as zero time.Time{}
	if _, err := r.DB.ExecContext(ctx,
		`INSERT INTO audit_logs (timestamp, action, actor, resource, details) VALUES (?, ?, ?, ?, ?)`,
		shared.MissingTimestamp, "CREATE", "admin", "db1", `{"msg":"missing sentinel log"}`,
	); err != nil {
		t.Fatalf("failed to insert MissingTimestamp log: %v", err)
	}
	allLogs, err := r.GetLogs(ctx, repo.QueryOptions{Order: "asc"})
	if err != nil {
		t.Fatalf("GetLogs (all) failed: %v", err)
	}
	var foundSentinel bool
	for _, entry := range allLogs {
		if entry.Details["msg"] == "missing sentinel log" {
			foundSentinel = true
			if !entry.Timestamp.IsZero() {
				t.Errorf("expected IsZero() == true for MissingTimestamp log, got %v", entry.Timestamp)
			}
		}
	}
	if !foundSentinel {
		t.Fatalf("expected missing sentinel log in GetLogs output")
	}
}

func TestLogAudit_SuccessAndError(t *testing.T) {
	ctx := context.Background()

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}

	// Before migrations, audit_logs table does not exist. LogAudit must return an error.
	err = r.LogAudit(ctx, repo.AuditLog{
		Action:   "TEST_ACTION",
		Actor:    "admin",
		Resource: "system",
		Details:  map[string]any{"test": "val"},
	})
	if err == nil {
		t.Fatalf("expected LogAudit to return error when audit_logs table does not exist, got nil")
	}

	// Apply migrations
	if err := r.MigrateUp(context.Background()); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	// After migrations, LogAudit must succeed
	err = r.LogAudit(ctx, repo.AuditLog{
		Action:   "TEST_ACTION",
		Actor:    "admin",
		Resource: "system",
		Details:  map[string]any{"test": "val"},
	})
	if err != nil {
		t.Fatalf("expected LogAudit to succeed, got: %v", err)
	}

	// Close repo DB. Subsequent LogAudit must return error.
	r.Close()
	err = r.LogAudit(ctx, repo.AuditLog{
		Action:   "TEST_ACTION",
		Actor:    "admin",
		Resource: "system",
		Details:  map[string]any{"test": "val"},
	})
	if err == nil {
		t.Fatalf("expected LogAudit to return error on closed DB, got nil")
	}
}

func TestSQLite_GetLogs_IDRange(t *testing.T) {
	ctx := context.Background()

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	if err := r.MigrateUp(ctx); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	for i := 1; i <= 5; i++ {
		if err := r.LogAudit(ctx, repo.AuditLog{
			Action:   "TEST_ACTION",
			Actor:    "admin",
			Resource: "system",
		}); err != nil {
			t.Fatalf("LogAudit %d failed: %v", i, err)
		}
	}

	logs, err := r.GetLogs(ctx, repo.QueryOptions{Order: "asc", IDStart: 2, IDEnd: 4})
	if err != nil {
		t.Fatalf("GetLogs ID range failed: %v", err)
	}
	if len(logs) != 3 {
		t.Fatalf("expected 3 logs, got %d", len(logs))
	}
	for i, wantID := range []int64{2, 3, 4} {
		if logs[i].ID != wantID {
			t.Errorf("logs[%d].ID = %d, want %d", i, logs[i].ID, wantID)
		}
	}
}
