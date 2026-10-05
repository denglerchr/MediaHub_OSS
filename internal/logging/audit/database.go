package audit

import (
	"context"
	"log/slog"
	"mediahub_oss/internal/repository"
)

type AlDatabase struct {
	Repo repository.Repository
}

func NewAlDatabase(repo repository.Repository) *AlDatabase {
	return &AlDatabase{Repo: repo}
}

func (a *AlDatabase) Log(ctx context.Context, action string, actor string, resource string, details map[string]any) {
	log := repository.AuditLog{
		Action:   action,
		Actor:    actor,
		Resource: resource,
		Details:  details,
	}
	// An audit-write failure must never fail the request, but it must not be
	// silently swallowed either. AlDatabase has no injected logger, so fall back
	// to the default slog logger.
	if err := a.Repo.LogAudit(ctx, log); err != nil {
		slog.Error("Failed to write audit log entry",
			"error", err,
			"action", action,
			"actor", actor,
			"resource", resource,
		)
	}
}
