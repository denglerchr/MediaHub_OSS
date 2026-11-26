package audit

import (
	"context"
	"io"
	"log/slog"
	"os"
)

// AlStdout enforces the structured audit schema.
type AlStdout struct {
	logger *slog.Logger
}

// NewAlStdout constructs an audit logger writing Info-level JSON events to
// os.Stdout, ignoring the application logger's level filter.
func NewAlStdout(logger *slog.Logger) *AlStdout {
	_ = logger
	return NewAlStdoutWithWriter(os.Stdout)
}

// NewAlStdoutWithWriter constructs an AlStdout instance writing Info-level JSON
// audit events to w.
func NewAlStdoutWithWriter(w io.Writer) *AlStdout {
	if w == nil {
		w = os.Stdout
	}
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	return &AlStdout{
		logger: slog.New(handler),
	}
}

// Log records an audit event.
func (a *AlStdout) Log(ctx context.Context, action string, actor string, resource string, details map[string]any) {

	// Build the list of attributes
	attrs := make([]any, 0, 4)

	attrs = append(attrs, slog.String("audit_action", action))
	attrs = append(attrs, slog.String("audit_actor", actor))
	attrs = append(attrs, slog.String("audit_resource", resource))

	if len(details) > 0 {
		groupAttrs := make([]any, 0, len(details))
		for k, v := range details {
			groupAttrs = append(groupAttrs, slog.Any(k, v))
		}
		attrs = append(attrs, slog.Group("detail", groupAttrs...))
	}

	// We use InfoContext so the handler can use the Context
	a.logger.InfoContext(ctx, "AUDIT EVENT", attrs...)
}
