package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestDiagnosticLoggerJSONAndLevel(t *testing.T) {
	t.Setenv("BONPREU_LOG_FORMAT", "json")
	t.Setenv("BONPREU_LOG_LEVEL", "warn")
	var out bytes.Buffer
	logger := newLogger(&out)
	logger.Warn("request failed\nforged", "service", "bonpreu", "component", "cli")
	if !strings.Contains(out.String(), `"level":"WARN"`) || !strings.Contains(out.String(), `\nforged`) {
		t.Fatalf("expected structured, escaped warning, got %q", out.String())
	}
	out.Reset()
	logger.Info("routine")
	if out.Len() != 0 {
		t.Fatalf("info should be filtered at warn level: %q", out.String())
	}
}
