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
	return slog.New(errorReporting{service: service, Handler: slog.NewJSONHandler(w, &slog.HandlerOptions{
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
	})})
}

// errorReporting adds the Error Reporting fields to the lines at ERROR.
type errorReporting struct {
	slog.Handler
	service string
}

func (h errorReporting) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelError {
		r.AddAttrs(slog.String("@type", errorEventType),
			slog.Group("serviceContext", slog.String("service", h.service)))
	}
	return h.Handler.Handle(ctx, r)
}

func (h errorReporting) WithAttrs(attrs []slog.Attr) slog.Handler {
	return errorReporting{service: h.service, Handler: h.Handler.WithAttrs(attrs)}
}

func (h errorReporting) WithGroup(name string) slog.Handler {
	return errorReporting{service: h.service, Handler: h.Handler.WithGroup(name)}
}
