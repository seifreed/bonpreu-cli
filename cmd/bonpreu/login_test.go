package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seifreed/bonpreu-cli/internal/client"
)

func TestSaveAndVerify(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		cookie     string
		json       bool
		wantErr    string
		wantOutput string
	}{
		{name: "authenticated JSON", status: http.StatusOK, cookie: "global_sid=s; aws-waf-token=t", json: true, wantOutput: `"authenticated": true`},
		{name: "unauthenticated JSON cookie", status: http.StatusUnauthorized, cookie: "global_sid=s; aws-waf-token=t", json: true, wantErr: "NOT authenticated"},
		{name: "verification failure", status: http.StatusInternalServerError, cookie: "global_sid=s; aws-waf-token=t", wantErr: "could not verify session"},
		{name: "missing cookie", status: http.StatusOK, wantErr: "carried no cookie"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/order/v6/orders") {
					w.WriteHeader(tc.status)
					return
				}
				http.NotFound(w, r)
			}))
			defer srv.Close()
			useServer(t, srv, false)
			cf := &common{jsonOut: tc.json}
			var events []string
			oldInfo := stderrInfo
			stderrInfo = func(event, _ string, _ ...any) { events = append(events, event) }
			defer func() { stderrInfo = oldInfo }()
			previous := client.Session{Cookie: "previous=still-valid", RegionID: "old-region"}
			if tc.wantErr != "" {
				if err := client.SaveSession(previous); err != nil {
					t.Fatal(err)
				}
			}
			out, err := capture(t, func() error {
				return saveAndVerify(cf, client.Session{Cookie: tc.cookie, RegionID: "r1"})
			})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
				}
				if len(events) != 0 {
					t.Errorf("failed verification emitted success events: %v", events)
				}
				got, loadErr := client.LoadSession()
				if loadErr != nil || got != previous {
					t.Errorf("failed login changed cached session = %+v, err %v; want %+v", got, loadErr, previous)
				}
				return
			}
			if err != nil || !strings.Contains(out, tc.wantOutput) {
				t.Fatalf("output = %q, err %v", out, err)
			}
			if len(events) != 1 || events[0] != "auth.session.verified" {
				t.Errorf("successful verification events = %v", events)
			}
		})
	}
}

func TestSaveAndVerifyPreservesOrReplacesRegion(t *testing.T) {
	for _, tc := range []struct {
		name       string
		captured   string
		wantRegion string
	}{
		{name: "preserves existing region when capture has none", wantRegion: "old-region"},
		{name: "uses region from capture", captured: "new-region", wantRegion: "new-region"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			privateTestConfigDir(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/order/v6/orders") {
					w.WriteHeader(http.StatusOK)
					return
				}
				http.NotFound(w, r)
			}))
			defer srv.Close()
			useServer(t, srv, false)
			if err := client.SaveSession(client.Session{Cookie: "previous=still-valid", RegionID: "old-region"}); err != nil {
				t.Fatal(err)
			}

			out, err := capture(t, func() error {
				return saveAndVerify(&common{operation: "login"}, client.Session{
					Cookie: "global_sid=new; aws-waf-token=new-token", RegionID: tc.captured,
				})
			})
			if err != nil || !strings.Contains(out, "region "+tc.wantRegion) {
				t.Fatalf("login output = %q, error %v; want region %q", out, err, tc.wantRegion)
			}
			got, err := client.LoadSession()
			if err != nil || got.Cookie != "global_sid=new; aws-waf-token=new-token" || got.RegionID != tc.wantRegion {
				t.Fatalf("saved session = %+v, error %v; want new cookie and region %q", got, err, tc.wantRegion)
			}
		})
	}
}

func TestSaveAndVerifyReturnsPreviousSessionReadError(t *testing.T) {
	dir := privateTestConfigDir(t)
	badSession := []byte("{broken session")
	if err := os.WriteFile(filepath.Join(dir, "session.json"), badSession, 0o600); err != nil {
		t.Fatal(err)
	}
	err := saveAndVerify(&common{operation: "login"}, client.Session{Cookie: "global_sid=new; aws-waf-token=new-token"})
	if err == nil || !strings.Contains(err.Error(), "read previous session region") {
		t.Fatalf("saveAndVerify error = %v; want prior-session read error", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "session.json"))
	if err != nil || string(got) != string(badSession) {
		t.Fatalf("corrupt session changed to %q, error %v; want it preserved", got, err)
	}
}

func TestSaveAndVerifyReturnsUnreadableConfigError(t *testing.T) {
	dir := privateTestConfigDir(t)
	t.Setenv("BONPREU_MAX_EUR", "")
	previous := client.Session{Cookie: "previous=still-valid", RegionID: "old-region"}
	if err := client.SaveSession(previous); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("not valid = toml ["), 0o600); err != nil {
		t.Fatal(err)
	}
	err := saveAndVerify(&common{operation: "login"}, client.Session{Cookie: "global_sid=a; aws-waf-token=b"})
	if err == nil || !strings.Contains(err.Error(), "read authentication config") {
		t.Fatalf("saveAndVerify error = %v; want config error", err)
	}
	got, loadErr := client.LoadSession()
	if loadErr != nil || got != previous {
		t.Fatalf("invalid config changed cached session = %+v, err %v; want %+v", got, loadErr, previous)
	}
}

func TestSaveAndVerifyReturnsSessionWriteError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/order/v6/orders") {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	useServer(t, srv, false)
	oldPersist := persistSession
	persistSession = func(client.Session) error { return errors.New("disk write failed") }
	t.Cleanup(func() { persistSession = oldPersist })
	err := saveAndVerify(&common{operation: "login"}, client.Session{Cookie: "global_sid=s; aws-waf-token=t"})
	if err == nil || !strings.Contains(err.Error(), "disk write failed") {
		t.Fatalf("saveAndVerify error = %v, want persistence failure", err)
	}
}

func TestSaveAndVerifySurfacesOutputFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/order/v6/orders") {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	useServer(t, srv, false)
	for _, jsonOut := range []bool{false, true} {
		cf := &common{operation: "login", jsonOut: jsonOut}
		err := withFailingStdout(t, func() error {
			return saveAndVerify(cf, client.Session{Cookie: "global_sid=s; aws-waf-token=t"})
		})
		if err == nil || !strings.Contains(err.Error(), "session saved, but confirmation output failed") {
			t.Fatalf("login output error = %v; want saved-session context", err)
		}
		got, loadErr := client.LoadSession()
		if loadErr != nil || got.Cookie != "global_sid=s; aws-waf-token=t" {
			t.Fatalf("session after output failure = %+v, %v; want persisted login session", got, loadErr)
		}
	}
}

func TestCmdLoginSelectsBrowserSource(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/order/v6/orders") {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	useServer(t, srv, false)
	var events []string
	oldInfo := stderrInfo
	stderrInfo = func(event, _ string, _ ...any) { events = append(events, event) }
	defer func() { stderrInfo = oldInfo }()

	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "existing browser", args: []string{"--from-browser", "firefox"}, want: "cookie from browser"},
		{name: "open browser", args: nil, want: "cookie from login"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events = nil
			fromCalled, openCalled := false, false
			oldRead, oldOpen := readLoginBrowser, openLoginBrowser
			readLoginBrowser = func(browser string) (client.Session, error) {
				fromCalled = true
				if browser != "firefox" {
					t.Errorf("browser = %q, want firefox", browser)
				}
				return client.Session{Cookie: tc.want, RegionID: "r1"}, nil
			}
			openLoginBrowser = func(url string) (client.Session, error) {
				openCalled = true
				if url != srv.URL+"/" {
					t.Errorf("login URL = %q", url)
				}
				return client.Session{Cookie: tc.want, RegionID: "r1"}, nil
			}
			t.Cleanup(func() { readLoginBrowser, openLoginBrowser = oldRead, oldOpen })
			out, err := capture(t, func() error {
				return cmdLogin(tc.args)
			})
			if err != nil || !strings.Contains(out, "authenticated") {
				t.Fatalf("login output = %q, err %v", out, err)
			}
			if fromCalled != (tc.args != nil) || openCalled == fromCalled {
				t.Fatalf("source calls: from-browser=%v open-browser=%v", fromCalled, openCalled)
			}
			if len(events) != 2 || events[0] != "auth.login.started" || events[1] != "auth.session.verified" {
				t.Fatalf("login events = %v", events)
			}
			session, err := client.LoadSession()
			if err != nil {
				t.Fatal(err)
			}
			if got := session.Cookie; got != tc.want {
				t.Fatalf("saved session cookie = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCmdLoginRejectsUnexpectedArgumentsBeforeBrowserAccess(t *testing.T) {
	readCalled, openCalled := false, false
	err := cmdLoginWith([]string{"unexpected"}, func(string) (client.Session, error) {
		readCalled = true
		return client.Session{}, nil
	}, func(string) (client.Session, error) {
		openCalled = true
		return client.Session{}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "usage: bonpreu login") {
		t.Fatalf("cmdLoginWith error = %v; want usage error", err)
	}
	if readCalled || openCalled {
		t.Fatalf("unexpected argument reached browser access: read=%v open=%v", readCalled, openCalled)
	}
}

func TestRunLoginLogsBrowserOpenFailure(t *testing.T) {
	t.Setenv("BONPREU_BASE_URL", "https://login.example")
	privateTestConfigDir(t)
	t.Setenv("BONPREU_LOG_FORMAT", "json")
	oldOpen := openLoginBrowser
	defer func() { openLoginBrowser = oldOpen }()
	called := false
	openLoginBrowser = func(url string) (client.Session, error) {
		called = true
		if url != "https://login.example/" {
			t.Errorf("login URL = %q", url)
		}
		return client.Session{}, errors.New("browser unavailable")
	}
	code, logs := runWithStderr(t, []string{"login"})
	if !called || code != 1 || !strings.Contains(logs, `"event_name":"command.failed"`) || !strings.Contains(logs, `"operation":"login"`) {
		t.Fatalf("login open failure called=%v exit=%d logs=%q", called, code, logs)
	}
}
