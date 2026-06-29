package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// importHarWith runs cmdImportHar against a HAR file holding the given cookie and
// (optional) regionId query param, capturing stderr diagnostics.
func importHarWith(t *testing.T, cookie, regionURL string) []string {
	t.Helper()
	t.Setenv("BONPREU_CONFIG_DIR", t.TempDir())

	har := fmt.Sprintf(`{"log":{"entries":[{"request":{"url":%q,"headers":[{"name":"cookie","value":%q}]}}]}}`, regionURL, cookie)
	path := filepath.Join(t.TempDir(), "session.har")
	if err := os.WriteFile(path, []byte(har), 0o600); err != nil {
		t.Fatal(err)
	}

	var logs []string
	old := stderrLogf
	stderrLogf = func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }
	defer func() { stderrLogf = old }()

	if err := cmdImportHar([]string{"--file", path}); err != nil {
		t.Fatalf("cmdImportHar: %v", err)
	}
	return logs
}

func TestImportHarWarnsWhenNoRegion(t *testing.T) {
	logs := importHarWith(t,
		"global_sid=a; aws-waf-token=b",
		"https://compraonline.bonpreuesclat.cat/api/x") // no regionId param
	if len(logs) != 1 || !strings.Contains(logs[0], "no region") {
		t.Fatalf("expected a no-region warning, got %v", logs)
	}
}

func TestImportHarSilentWhenRegionPresent(t *testing.T) {
	logs := importHarWith(t,
		"global_sid=a; aws-waf-token=b",
		"https://compraonline.bonpreuesclat.cat/api/x?regionId=r-123")
	if len(logs) != 0 {
		t.Errorf("region present → no warning expected, got %v", logs)
	}
}

func TestSetCookieWarnsOnAnonymousCookie(t *testing.T) {
	t.Setenv("BONPREU_CONFIG_DIR", t.TempDir())
	var logs []string
	old := stderrLogf
	stderrLogf = func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }
	defer func() { stderrLogf = old }()

	if err := cmdSetCookie([]string{"sessionid=x; other=y"}); err != nil {
		t.Fatalf("cmdSetCookie: %v", err)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "anonymous") {
		t.Fatalf("expected an anonymous-cookie warning, got %v", logs)
	}
}

func TestSetCookieSilentOnAuthedCookie(t *testing.T) {
	t.Setenv("BONPREU_CONFIG_DIR", t.TempDir())
	var logs []string
	old := stderrLogf
	stderrLogf = func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }
	defer func() { stderrLogf = old }()

	if err := cmdSetCookie([]string{"global_sid=a; aws-waf-token=b"}); err != nil {
		t.Fatalf("cmdSetCookie: %v", err)
	}
	if len(logs) != 0 {
		t.Errorf("authed cookie → no warning, got %v", logs)
	}
}

func TestSetCookieSaveError(t *testing.T) {
	t.Setenv("BONPREU_CONFIG_DIR", unwritableConfigDir(t))
	if err := cmdSetCookie([]string{"global_sid=a; aws-waf-token=b"}); err == nil {
		t.Error("set-cookie should fail when the session cannot be saved")
	}
}

func TestImportHarSaveError(t *testing.T) {
	t.Setenv("BONPREU_CONFIG_DIR", unwritableConfigDir(t))
	har := `{"log":{"entries":[{"request":{"url":"https://compraonline.bonpreuesclat.cat/x","headers":[{"name":"cookie","value":"global_sid=a; aws-waf-token=b"}]}}]}}`
	path := filepath.Join(t.TempDir(), "s.har")
	if err := os.WriteFile(path, []byte(har), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cmdImportHar([]string{"--file", path}); err == nil {
		t.Error("import-har should fail when the session cannot be saved")
	}
}

func TestReadFileOrStdin(t *testing.T) {
	withStdin(t, "hello", func() {
		b, err := readFileOrStdin("-")
		if err != nil || string(b) != "hello" {
			t.Fatalf("stdin read = %q, err %v", b, err)
		}
	})
}

func TestCmdSetCookieStdinAndEmpty(t *testing.T) {
	t.Setenv("BONPREU_CONFIG_DIR", t.TempDir())

	withStdin(t, "global_sid=a; aws-waf-token=b\n", func() {
		if err := cmdSetCookie([]string{"--stdin"}); err != nil {
			t.Fatalf("set-cookie --stdin: %v", err)
		}
	})
	withStdin(t, "   \n", func() {
		if err := cmdSetCookie([]string{"--stdin"}); err == nil {
			t.Error("empty cookie should error")
		}
	})
}

func TestCmdImportHarUsageError(t *testing.T) {
	if err := cmdImportHar(nil); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("import-har with no --file should show usage, got %v", err)
	}
}

func TestImportHarParseError(t *testing.T) {
	t.Setenv("BONPREU_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "anon.har")
	// bonpreu entry but the cookie is anonymous → ParseHAR errors.
	har := `{"log":{"entries":[{"request":{"url":"https://compraonline.bonpreuesclat.cat/x","headers":[{"name":"cookie","value":"visitorId=1"}]}}]}}`
	if err := os.WriteFile(path, []byte(har), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cmdImportHar([]string{"--file", path}); err == nil {
		t.Error("import-har with no authed cookie should error")
	}
}

func TestAuthErrorsWithoutSession(t *testing.T) {
	srv := apiServer(t)
	defer srv.Close()
	useServer(t, srv, false) // no session → authedClient fails in each handler

	checks := []func() error{
		func() error { return cmdCart([]string{"get"}) },
		func() error { return cmdCart([]string{"clear"}) },
		func() error { return cmdCart([]string{"add", "23597", "1"}) },
		func() error { return cmdCheckout([]string{"status"}) },
		func() error { return cmdCheckout([]string{"addresses"}) },
		func() error { return cmdCheckout([]string{"slots", "--address", "a"}) },
	}
	for i, fn := range checks {
		if _, err := capture(t, fn); err == nil {
			t.Errorf("auth check %d: expected not-authenticated error", i)
		}
	}
}

func TestWhoamiCheckAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError) // orders endpoint 500 → CheckAuth error
	}))
	defer srv.Close()
	useServer(t, srv, true)
	if _, err := capture(t, func() error { return cmdWhoami(nil) }); err == nil {
		t.Error("whoami should surface a CheckAuth transport error")
	}
}

func TestImportHarFileMissing(t *testing.T) {
	t.Setenv("BONPREU_CONFIG_DIR", t.TempDir())
	if err := cmdImportHar([]string{"--file", filepath.Join(t.TempDir(), "nope.har")}); err == nil {
		t.Error("import-har with a missing file should error")
	}
}

func TestWhoamiWithoutSession(t *testing.T) {
	srv := apiServer(t)
	defer srv.Close()
	useServer(t, srv, false) // no session → authedClient fails
	if _, err := capture(t, func() error { return cmdWhoami(nil) }); err == nil {
		t.Error("whoami without a session should error")
	}
}

func TestSetCookieStdinReadError(t *testing.T) {
	t.Setenv("BONPREU_CONFIG_DIR", t.TempDir())
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	r.Close() // closed → io.ReadAll(os.Stdin) errors
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old }()
	if err := cmdSetCookie([]string{"--stdin"}); err == nil {
		t.Error("a stdin read failure should surface")
	}
}

func TestWhoamiNotAuthenticated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized) // cookie present but rejected
	}))
	defer srv.Close()
	useServer(t, srv, true)
	if _, err := capture(t, func() error { return cmdWhoami(nil) }); err == nil {
		t.Error("expected 'cookie present but NOT authenticated' error")
	}
}
