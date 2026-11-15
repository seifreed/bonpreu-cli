package main

import (
	"os"
	"testing"
)

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
