package logging

import (
	"bytes"
	"strings"
	"testing"
)

func newTestLogger(level Level) (*Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return New(&buf, level), &buf
}

func TestLevelFiltering(t *testing.T) {
	log, buf := newTestLogger(LevelWarn)

	log.Debugf("debug %d", 1)
	log.Infof("info %d", 2)
	if buf.Len() != 0 {
		t.Errorf("records below the level must be dropped, got %q", buf.String())
	}

	log.Warnf("warn %d", 3)
	log.Errorf("error %d", 4)
	for _, want := range []string{"WRN", "ERR"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("output %q missing level tag %s", buf.String(), want)
		}
	}
}

func TestStructuredFieldsAndOperation(t *testing.T) {
	log, buf := newTestLogger(LevelInfo)

	log.With("kind", "router", "name", "grafana").WithOperation("apply-42").Errorf("failed to stage item: %v", "cycle")

	line := buf.String()
	for _, want := range []string{"op=apply-42", "kind=router", "name=grafana", "ERR", "failed to stage item: cycle"} {
		if !strings.Contains(line, want) {
			t.Errorf("record %q missing %q", line, want)
		}
	}
	if !strings.HasPrefix(line, "20") {
		t.Errorf("record must start with an RFC3339 timestamp, got %q", line)
	}
	if !strings.HasSuffix(line, "\n") {
		t.Error("record must end with a newline")
	}
}

func TestWithDoesNotMutateParent(t *testing.T) {
	log, buf := newTestLogger(LevelInfo)

	child := log.With("kind", "router")
	log.Infof("parent line")
	if strings.Contains(buf.String(), "kind=router") {
		t.Error("parent logger must not inherit child fields")
	}
	child.Infof("child line")
	if !strings.Contains(buf.String(), "kind=router") {
		t.Error("child logger must carry its fields")
	}
}

func TestFormatAddsMissingNewline(t *testing.T) {
	log, buf := newTestLogger(LevelInfo)
	log.Infof("no explicit newline")
	if !strings.HasSuffix(buf.String(), "\n") {
		t.Errorf("newline must be appended, got %q", buf.String())
	}
}

func TestParseLevel(t *testing.T) {
	cases := map[string]Level{
		"debug":   LevelDebug,
		"INFO":    LevelInfo,
		" warn ":  LevelWarn,
		"warning": LevelWarn,
		"error":   LevelError,
		"":        LevelInfo,
		"bogus":   LevelInfo,
	}
	for in, want := range cases {
		if got := ParseLevel(in); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestRedact(t *testing.T) {
	if got := Redact("abc"); got != "****" {
		t.Errorf("Redact(short) = %q, want ****", got)
	}
	if got := Redact("super-secret-token"); got != "****oken" {
		t.Errorf("Redact(long) = %q, want ****oken", got)
	}
	if strings.Contains(Redact("super-secret-token"), "super") {
		t.Error("Redact must never expose the secret prefix")
	}
}
