// Package logging builds the structured logger shared by the API and the worker.
package logging

import (
	"io"
	"log/slog"
)

// New returns a logger that writes JSON lines that Cloud Logging understands: "severity"
// (DEBUG, INFO, WARNING, ERROR), "message" and "time" (in UTC).
func New(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{
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
	}))
}
