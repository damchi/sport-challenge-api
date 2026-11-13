// Package logging builds the structured logger shared by the API and the worker.
package logging

import (
	"context"
	"io"
	"log/slog"
	"runtime"
	"slices"
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
	return slog.New(errorReporting{plain: base, base: base, service: service})
}

// errorReporting writes the lines at ERROR with the fields Error Reporting
// needs: the marker, the program, and the place of the call (it takes the
// place of a stack trace). They must sit at the root of the line, whatever
// groups the logger has opened, so a line at ERROR is written by a handler
// rebuilt from the root: the attributes and groups of the logger are replayed
// on top of the root fields. The lines below ERROR use plain.
type errorReporting struct {
	plain   slog.Handler // base with the attributes and groups so far
	base    slog.Handler // the JSON handler, as it was created
	service string
	ops     []func(slog.Handler) slog.Handler // the attributes and groups so far
}

func (h errorReporting) Enabled(ctx context.Context, level slog.Level) bool {
	return h.plain.Enabled(ctx, level)
}

func (h errorReporting) Handle(ctx context.Context, r slog.Record) error {
	if r.Level < slog.LevelError {
		return h.plain.Handle(ctx, r)
	}
	frame, _ := runtime.CallersFrames([]uintptr{r.PC}).Next()
	reported := h.base.WithAttrs([]slog.Attr{
		slog.String("@type", errorEventType),
		slog.Group("serviceContext", slog.String("service", h.service)),
		slog.Group("context", slog.Group("reportLocation",
			slog.String("filePath", frame.File),
			slog.Int("lineNumber", frame.Line),
			slog.String("functionName", frame.Function))),
	})
	for _, op := range h.ops {
		reported = op(reported)
	}
	return reported.Handle(ctx, r)
}

func (h errorReporting) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h.with(func(handler slog.Handler) slog.Handler { return handler.WithAttrs(attrs) })
}

func (h errorReporting) WithGroup(name string) slog.Handler {
	return h.with(func(handler slog.Handler) slog.Handler { return handler.WithGroup(name) })
}

func (h errorReporting) with(op func(slog.Handler) slog.Handler) slog.Handler {
	h.plain = op(h.plain)
	h.ops = append(slices.Clone(h.ops), op)
	return h
}
