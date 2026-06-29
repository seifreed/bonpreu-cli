package main

import "testing"

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
