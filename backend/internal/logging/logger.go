// Package logging is the console's structured logger: levelled key=value
// lines with operation IDs and secret redaction, written to an injectable
// writer. It ONLY logs — control flow (exit, return, retry) stays at the
// call site, one explicit statement per failure case, so readers and
// analyzers can always see what happens next. Write failures are
// deliberately swallowed — logging must never take the application down —
// which is also why callers never need to handle errors from this package,
// unlike fmt.Fprintf.
package logging

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// Level orders severity; records below the logger's level are dropped.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DBG"
	case LevelInfo:
		return "INF"
	case LevelWarn:
		return "WRN"
	case LevelError:
		return "ERR"
	default:
		return "???"
	}
}

// ParseLevel maps common level names to a Level, falling back to LevelInfo.
func ParseLevel(name string) Level {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return LevelDebug
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}

// Logger writes single-line records:
//
//	2026-11-05T15:04:05Z ERR op=apply-42 kind=router failed to stage item
//
// It is safe for concurrent use; loggers derived with With/WithOperation
// share the parent's writer and lock.
type Logger struct {
	mu     *sync.Mutex
	out    io.Writer
	level  Level
	op     string
	fields []string
}

func New(w io.Writer, level Level) *Logger {
	return &Logger{mu: &sync.Mutex{}, out: w, level: level}
}

// With returns a child logger carrying additional key=value fields. Keys
// without a value pair are dropped. Never log secret values — pass them
// through Redact first.
func (l *Logger) With(kv ...string) *Logger {
	child := *l
	child.fields = append(append([]string(nil), l.fields...), formatFields(kv...)...)
	return &child
}

// WithOperation returns a child logger tagged with an operation ID that ties
// related log lines together.
func (l *Logger) WithOperation(op string) *Logger {
	child := *l
	child.op = op
	return &child
}

func (l *Logger) Debugf(format string, args ...any) { l.logf(LevelDebug, format, args...) }
func (l *Logger) Infof(format string, args ...any)  { l.logf(LevelInfo, format, args...) }
func (l *Logger) Warnf(format string, args ...any)  { l.logf(LevelWarn, format, args...) }
func (l *Logger) Errorf(format string, args ...any) { l.logf(LevelError, format, args...) }

func (l *Logger) logf(level Level, format string, args ...any) {
	if level < l.level {
		return
	}

	var b strings.Builder
	b.WriteString(time.Now().UTC().Format(time.RFC3339))
	b.WriteByte(' ')
	b.WriteString(level.String())
	if l.op != "" {
		b.WriteString(" op=")
		b.WriteString(l.op)
	}
	for _, f := range l.fields {
		b.WriteByte(' ')
		b.WriteString(f)
	}
	b.WriteByte(' ')
	fmt.Fprintf(&b, format, args...) // strings.Builder writes never fail

	msg := b.String()
	if !strings.HasSuffix(msg, "\n") {
		msg += "\n"
	}
	l.mu.Lock()
	_, _ = io.WriteString(l.out, msg) // logging failures must never crash the app
	l.mu.Unlock()
}

func formatFields(kv ...string) []string {
	var out []string
	for i := 0; i+1 < len(kv); i += 2 {
		out = append(out, kv[i]+"="+kv[i+1])
	}
	return out
}

// Redact masks a secret for logging, keeping only the last 4 characters so
// operators can correlate values without ever exposing them.
func Redact(secret string) string {
	if len(secret) <= 4 {
		return "****"
	}

	return "****" + secret[len(secret)-4:]
}
