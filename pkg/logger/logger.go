// Package logger provides structured logging using slog.
package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"runtime"
	"time"
)

// contextKey is a custom type for context keys to avoid collisions.
type contextKey string

const (
	// RequestIDKey is the context key for request ID.
	RequestIDKey contextKey = "request_id"
	// UserIDKey is the context key for user ID.
	UserIDKey contextKey = "user_id"
)

// Config holds logger configuration.
type Config struct {
	Level      string `env:"LOG_LEVEL" default:"info"`
	Format     string `env:"LOG_FORMAT" default:"json"` // json or text
	AddSource  bool   `env:"LOG_ADD_SOURCE" default:"false"`
	TimeFormat string `env:"LOG_TIME_FORMAT" default:"2006-01-02T15:04:05.000Z07:00"`
}

// Logger wraps slog.Logger with additional functionality.
type Logger struct {
	*slog.Logger
}

// New creates a new Logger instance.
func New(cfg Config) *Logger {
	var level slog.Level
	switch cfg.Level {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level:     level,
		AddSource: cfg.AddSource,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Customize time format
			if a.Key == slog.TimeKey {
				if t, ok := a.Value.Any().(time.Time); ok {
					a.Value = slog.StringValue(t.Format(cfg.TimeFormat))
				}
			}
			// Mask sensitive fields
			if a.Key == "password" || a.Key == "api_key" || a.Key == "token" || a.Key == "secret" {
				a.Value = slog.StringValue("***REDACTED***")
			}
			return a
		},
	}

	var handler slog.Handler
	var output io.Writer = os.Stdout

	if cfg.Format == "text" {
		handler = slog.NewTextHandler(output, opts)
	} else {
		handler = slog.NewJSONHandler(output, opts)
	}

	return &Logger{
		Logger: slog.New(handler),
	}
}

// Default returns a default logger instance.
func Default() *Logger {
	return New(Config{
		Level:     "info",
		Format:    "json",
		AddSource: false,
	})
}

// WithContext returns a logger with context values extracted.
func (l *Logger) WithContext(ctx context.Context) *Logger {
	logger := l.Logger

	if requestID, ok := ctx.Value(RequestIDKey).(string); ok && requestID != "" {
		logger = logger.With("request_id", requestID)
	}
	if userID, ok := ctx.Value(UserIDKey).(string); ok && userID != "" {
		logger = logger.With("user_id", userID)
	}

	return &Logger{Logger: logger}
}

// WithComponent returns a logger with component name.
func (l *Logger) WithComponent(name string) *Logger {
	return &Logger{
		Logger: l.Logger.With("component", name),
	}
}

// WithError returns a logger with error field.
func (l *Logger) WithError(err error) *Logger {
	return &Logger{
		Logger: l.Logger.With("error", err.Error()),
	}
}

// WithFields returns a logger with additional fields.
func (l *Logger) WithFields(fields map[string]any) *Logger {
	args := make([]any, 0, len(fields)*2)
	for k, v := range fields {
		args = append(args, k, v)
	}
	return &Logger{
		Logger: l.Logger.With(args...),
	}
}

// LogPanic logs panic information with stack trace.
func (l *Logger) LogPanic(r any) {
	buf := make([]byte, 4096)
	n := runtime.Stack(buf, false)
	l.Error("panic recovered",
		"panic", r,
		"stack", string(buf[:n]),
	)
}

// SetDefault sets this logger as the default slog logger.
func (l *Logger) SetDefault() {
	slog.SetDefault(l.Logger)
}
