package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
)

type failedLogWriter struct{}

func (failedLogWriter) Write([]byte) (int, error) { return 0, errors.New("sink unavailable") }

func TestLoggerSinkFailureDoesNotPanic(_ *testing.T) {
	newLogger(failedLogWriter{}).Error("diagnostic event")
}

func TestLoggerConcurrentWrites(t *testing.T) {
	t.Setenv("BONPREU_LOG_FORMAT", "json")
	var out bytes.Buffer
	logger := newLogger(&out)
	const count = 32
	var wg sync.WaitGroup
	wg.Add(count)
	for i := 0; i < count; i++ {
		go func() {
			defer wg.Done()
			logger.Info("concurrent event", "component", "test")
		}()
	}
	wg.Wait()
	if got := strings.Count(out.String(), "\n"); got != count {
		t.Fatalf("concurrent log records = %d, want %d", got, count)
	}
}

func TestLoggerCustomTraceAndFatalLevels(t *testing.T) {
	t.Setenv("BONPREU_LOG_FORMAT", "json")
	var out bytes.Buffer
	t.Setenv("BONPREU_LOG_LEVEL", "trace")
	newLogger(&out).Log(context.Background(), traceLevel, "request detail")
	if !strings.Contains(out.String(), `"level":"TRACE"`) {
		t.Fatalf("custom trace level = %q", out.String())
	}
	out.Reset()
	t.Setenv("BONPREU_LOG_LEVEL", "fatal")
	logger := newLogger(&out)
	logger.Error("filtered error")
	logger.Log(context.Background(), fatalLevel, "process failure")
	if strings.Contains(out.String(), "filtered error") || !strings.Contains(out.String(), `"level":"FATAL"`) {
		t.Fatalf("fatal level selection = %q", out.String())
	}
}

func TestCLIInfoAndWarningEventsAreStructured(t *testing.T) {
	t.Setenv("BONPREU_LOG_FORMAT", "json")
	t.Setenv("BONPREU_LOG_LEVEL", "debug")
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	stderrDebug("test.debug", "details", "operation", "test")
	stderrInfo("test.info", "started", "operation", "test")
	stderrWarn("test.warning", "recovered")
	_ = w.Close()
	os.Stderr = old
	data, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"level":"DEBUG"`, `"event_name":"test.debug"`, `"level":"INFO"`, `"event_name":"test.info"`, `"operation":"test"`, `"level":"WARN"`, `"event_name":"test.warning"`} {
		if !strings.Contains(string(data), field) {
			t.Errorf("CLI log output missing %s: %s", field, data)
		}
	}
}

func TestDiagnosticLoggerJSONAndLevel(t *testing.T) {
	t.Setenv("BONPREU_LOG_FORMAT", "json")
	t.Setenv("BONPREU_LOG_LEVEL", "warn")
	var out bytes.Buffer
	logger := newLogger(&out)
	logger.Warn("request failed\nforged", "service", "bonpreu", "component", "cli")
	if !strings.Contains(out.String(), `"level":"WARN"`) || !strings.Contains(out.String(), `\nforged`) || !strings.Contains(out.String(), `Z"`) {
		t.Fatalf("expected structured, escaped warning, got %q", out.String())
	}
	out.Reset()
	logger.Info("routine")
	if out.Len() != 0 {
		t.Fatalf("info should be filtered at warn level: %q", out.String())
	}
}

func TestDiagnosticLoggerLevelsAndTextFormat(t *testing.T) {
	for _, level := range []string{"debug", "info", "warning", "error", "unknown"} {
		t.Run(level, func(t *testing.T) {
			t.Setenv("BONPREU_LOG_LEVEL", level)
			t.Setenv("BONPREU_LOG_FORMAT", "text")
			var out bytes.Buffer
			newLogger(&out).Info("startup", "service", "bonpreu", "component", "cli")
			if level == "error" || level == "warning" {
				if out.Len() != 0 {
					t.Fatalf("info was not filtered at %s: %q", level, out.String())
				}
				return
			}
			if !strings.Contains(out.String(), `msg=startup`) || !strings.Contains(out.String(), `service=bonpreu`) {
				t.Fatalf("expected text fields, got %q", out.String())
			}
		})
	}
}

func TestClientLoggerAddsOperationContext(t *testing.T) {
	t.Setenv("BONPREU_LOG_FORMAT", "json")
	t.Setenv("BONPREU_LOG_LEVEL", "info")
	var out bytes.Buffer
	clientLogger(&out, "checkout slots").Info("request complete", "event_name", "http.complete")
	for _, field := range []string{`"service":"bonpreu"`, `"operation":"checkout slots"`, `"process.pid":`, `"event_name":"http.complete"`} {
		if !strings.Contains(out.String(), field) {
			t.Fatalf("logger output missing %s: %q", field, out.String())
		}
	}
}
