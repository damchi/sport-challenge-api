// Package logging builds the structured logger shared by the API and the worker.
package logging

import (
	"context"
	"io"
	"log/slog"
)

// errorEventType marks a log line as an error for Error Reporting, which then
// groups it without needing a stack trace.
const errorEventType = "type.googleapis.com/google.devtools.clouderrorreporting.v1beta1.ReportedErrorEvent"

// New returns a logger that writes JSON lines that Cloud Logging understands: "severity"
// (DEBUG, INFO, WARNING, ERROR), "message" and "time" (in UTC). Lines at ERROR
// also carry what Error Reporting needs, with service as the name of the
// program ("api", "worker", "migrate").
func New(w io.Writer, level slog.Level, service string) *slog.Logger {
	base := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) > 0 {
				return a
			}
			switch a.Key {
			case slog.TimeKey:
				a.Value = slog.TimeValue(a.Value.Time().UTC())
			case slog.LevelKey:
				a.Key = "severity"
				if lvl, ok := a.Value.Any().(slog.Level); ok && lvl == slog.LevelWarn {
					a.Value = slog.StringValue("WARNING")
				}
			case slog.MessageKey:
				a.Key = "message"
			}
			return a
		},
	})
	// The fields are given to the handler before any group can be opened, so
	// that they stay at the root of the line.
	reported := base.WithAttrs([]slog.Attr{
		slog.String("@type", errorEventType),
		slog.Group("serviceContext", slog.String("service", service)),
	})
	return slog.New(errorReporting{plain: base, reported: reported})
}

// errorReporting writes the lines at ERROR with the Error Reporting fields and
// the others without. Both handlers always get the same attributes and groups.
type errorReporting struct {
	plain, reported slog.Handler
}

func (h errorReporting) Enabled(ctx context.Context, level slog.Level) bool {
	return h.plain.Enabled(ctx, level)
}

func (h errorReporting) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelError {
		return h.reported.Handle(ctx, r)
	}
	return h.plain.Handle(ctx, r)
}

func (h errorReporting) WithAttrs(attrs []slog.Attr) slog.Handler {
	return errorReporting{plain: h.plain.WithAttrs(attrs), reported: h.reported.WithAttrs(attrs)}
}

func (h errorReporting) WithGroup(name string) slog.Handler {
	return errorReporting{plain: h.plain.WithGroup(name), reported: h.reported.WithGroup(name)}
}
