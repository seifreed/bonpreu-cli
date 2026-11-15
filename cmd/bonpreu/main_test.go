package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func runWithStderr(t *testing.T, args []string) (int, string) {
	return captureStderr(t, func() int { return run(args) })
}

func captureStderr(t *testing.T, invoke func() int) (int, string) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	old := os.Stderr
	os.Stderr = f
	t.Cleanup(func() { os.Stderr = old })
	code := invoke()
	os.Stderr = old
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	return code, string(b)
}

func TestMainPassesCommandExitCodeToProcess(t *testing.T) {
	oldArgs, oldExit := os.Args, exitProcess
	t.Cleanup(func() { os.Args, exitProcess = oldArgs, oldExit })
	os.Args = []string{"bonpreu", "version"}
	exitCode := -1
	exitProcess = func(code int) { exitCode = code }
	if _, err := capture(t, func() error { main(); return nil }); err != nil {
		t.Fatal(err)
	}
	if exitCode != 0 {
		t.Fatalf("main exit code = %d, want 0", exitCode)
	}
}

func TestRecoverCommandLogsFatalWithoutPanicValue(t *testing.T) {
	t.Setenv("BONPREU_LOG_FORMAT", "json")
	t.Setenv("BONPREU_LOG_LEVEL", "trace")
	const secret = "cookie=do-not-log"
	code, logs := captureStderr(t, func() int {
		return recoverCommand("cart write", func() int { panic(secret) })
	})
	if code != 2 || !strings.Contains(logs, `"level":"FATAL"`) || !strings.Contains(logs, `"event_name":"command.panicked"`) || !strings.Contains(logs, `"operation":"cart write"`) || !strings.Contains(logs, `"stack_trace"`) {
		t.Fatalf("panic exit=%d logs=%q", code, logs)
	}
	if strings.Contains(logs, secret) {
		t.Fatalf("panic value leaked to logs: %q", logs)
	}
}

func TestRunSafelyNoArgsShowsUsage(t *testing.T) {
	t.Setenv("BONPREU_LOG_FORMAT", "json")
	code, output := captureStderr(t, func() int { return runSafely(nil) })
	if code != 2 || !strings.Contains(output, "USAGE:") || !strings.Contains(output, `"event_name":"command.invalid"`) || !strings.Contains(output, `"operation":"startup"`) {
		t.Fatalf("runSafely without args exit=%d output=%q", code, output)
	}
}

func TestRunDispatchesBrandsCommand(t *testing.T) {
	srv := apiServer(t)
	defer srv.Close()
	useServer(t, srv, false)
	var code int
	_, err := captureFile(t, func() error { code = run([]string{"brands", "--json"}); return nil })
	if err != nil || code != 0 {
		t.Fatalf("brands dispatch exit=%d, err=%v", code, err)
	}
}

func TestRunLogsCommandCompletionAndSafeFailure(t *testing.T) {
	t.Setenv("BONPREU_LOG_FORMAT", "json")
	t.Setenv("BONPREU_LOG_LEVEL", "debug")
	successServer := apiServer(t)
	defer successServer.Close()
	useServer(t, successServer, false)
	code, logs := runWithStderr(t, []string{"search", "llet"})
	if code != 0 || !strings.Contains(logs, `"event_name":"command.completed"`) || !strings.Contains(logs, `"operation":"search"`) || !strings.Contains(logs, `"outcome":"success"`) || !strings.Contains(logs, `"duration_ms"`) {
		t.Fatalf("success exit=%d logs=%q", code, logs)
	}

	failureServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "private remote response", http.StatusInternalServerError)
	}))
	defer failureServer.Close()
	useServer(t, failureServer, false)
	code, logs = runWithStderr(t, []string{"categories", "--id", "missing"})
	if code != 1 || !strings.Contains(logs, `"level":"ERROR"`) || !strings.Contains(logs, `"event_name":"command.failed"`) || !strings.Contains(logs, `"operation":"categories"`) || !strings.Contains(logs, `"error_code":500`) {
		t.Fatalf("failure exit=%d logs=%q", code, logs)
	}
	if strings.Contains(logs, "private remote response") {
		t.Fatalf("remote body leaked to logs: %q", logs)
	}
	t.Logf("captured failure log sample: %s", logs)
}

func TestCommandOperationNames(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"search"}, "search"},
		{[]string{"cart"}, "cart"},
		{[]string{"cart", "add"}, "cart write"},
		{[]string{"cart", "set"}, "cart write"},
		{[]string{"cart", "clear"}, "cart clear"},
		{[]string{"checkout", "addresses"}, "checkout addresses"},
		{[]string{"checkout", "slots"}, "checkout slots"},
		{[]string{"checkout", "status"}, "checkout"},
		{[]string{"brands", "--json"}, "brands"},
		{[]string{"not-a-command\nsecret"}, "unknown"},
		{[]string{"checkout", "private-address-id"}, "checkout"},
		{[]string{"cart", "private-cookie"}, "cart"},
		{nil, "startup"},
	} {
		if got := commandOperation(tc.args); got != tc.want {
			t.Errorf("commandOperation(%v) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestRunDispatch(t *testing.T) {
	srv := apiServer(t)
	defer srv.Close()
	useServer(t, srv, true)

	cases := map[string]int{
		"":        2, // no args → usage, exit 2
		"nope":    2, // unknown command
		"version": 0,
		"help":    0,
		"search":  1, // missing term → error
	}
	for arg, want := range cases {
		var args []string
		if arg != "" {
			args = []string{arg}
		}
		var got int
		_, _ = capture(t, func() error { got = run(args); return nil })
		if got != want {
			t.Errorf("run(%q) = %d, want %d", arg, got, want)
		}
	}
	for _, alias := range []string{"--version", "-v", "-h", "--help"} {
		want := 0
		var got int
		_, _ = capture(t, func() error { got = run([]string{alias}); return nil })
		if got != want {
			t.Errorf("run(%q) = %d, want %d", alias, got, want)
		}
	}

	// Every command name dispatches without panicking (exit 0 or 1 both fine).
	for _, name := range []string{"search", "batch", "total", "product", "categories", "import-har", "set-cookie", "whoami", "cart", "checkout"} {
		_, _ = capture(t, func() error { run([]string{name}); return nil })
	}
	// A successful full run.
	var ok int
	_, _ = capture(t, func() error { ok = run([]string{"search", "llet"}); return nil })
	if ok != 0 {
		t.Errorf("run search llet = %d, want 0", ok)
	}
}

func TestRunVersionOutputFailure(t *testing.T) {
	if err := withFailingStdout(t, func() error {
		if run([]string{"version"}) != 1 {
			return io.ErrUnexpectedEOF
		}
		return nil
	}); err != nil {
		t.Fatalf("run(version) should fail when stdout cannot be written: %v", err)
	}
}

func TestRunUnknownCommandLogsWithoutInput(t *testing.T) {
	t.Setenv("BONPREU_LOG_FORMAT", "json")
	const input = "unknown\nprivate-token"
	code, logs := runWithStderr(t, []string{input})
	if code != 2 || !strings.Contains(logs, `"event_name":"command.invalid"`) || !strings.Contains(logs, `"operation":"unknown"`) {
		t.Fatalf("unknown command exit=%d logs=%q", code, logs)
	}
	event, _, _ := strings.Cut(logs, "\n")
	if strings.Contains(event, input) || strings.Contains(event, "private-token") {
		t.Fatalf("invalid command input leaked into structured event: %q", event)
	}
}

func TestVersionString(t *testing.T) {
	ov, oc, od := version, commit, date
	defer func() { version, commit, date = ov, oc, od }()

	version, commit, date = "1.2.3", "", ""
	if got := versionString(); got != "1.2.3" {
		t.Errorf("no commit → %q", got)
	}
	commit = "abc"
	if got := versionString(); got != "1.2.3 (abc)" {
		t.Errorf("commit only → %q", got)
	}
	date = "2026"
	if got := versionString(); got != "1.2.3 (abc, 2026)" {
		t.Errorf("commit+date → %q", got)
	}
}
