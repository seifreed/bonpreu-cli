package client

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadAuthLogs(t *testing.T, writeSession []byte) []string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("BONPREU_CONFIG_DIR", dir)
	if writeSession != nil {
		if err := os.WriteFile(filepath.Join(dir, sessionFile), writeSession, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var logs bytes.Buffer
	c := &Client{Logger: testLogger(&logs)}
	c.LoadAuth()
	if logs.Len() == 0 {
		return nil
	}
	return strings.Split(strings.TrimSpace(logs.String()), "\n")
}

func TestLoadAuthWarnsOnCorruptSession(t *testing.T) {
	logs := loadAuthLogs(t, []byte("{ not valid json"))
	if len(logs) != 1 || !strings.Contains(logs[0], "session cache could not be read") {
		t.Fatalf("expected a corrupt-session warning, got %v", logs)
	}
}

func TestLoadAuthSilentWhenNoSession(t *testing.T) {
	if logs := loadAuthLogs(t, nil); len(logs) != 0 {
		t.Errorf("missing session is normal → no warning, got %v", logs)
	}
}

func TestLoadAuthWarnsOnForeignBaseURL(t *testing.T) {
	t.Setenv("BONPREU_CONFIG_DIR", t.TempDir())
	var logs bytes.Buffer
	c := &Client{
		BaseURL: "https://evil.example",
		Logger:  testLogger(&logs),
	}
	c.Cookie = "global_sid=x; aws-waf-token=y"
	c.LoadAuth() // BaseURL stays foreign; cookie already set → must warn
	var warned bool
	warned = strings.Contains(logs.String(), "security.cookie_external_host")
	if !warned {
		t.Errorf("expected a foreign-host cookie warning, got %q", logs.String())
	}

	// A real Bonpreu BaseURL must NOT warn.
	logs.Reset()
	c2 := &Client{
		BaseURL: BaseURL,
		Logger:  testLogger(&logs),
	}
	c2.Cookie = "global_sid=x; aws-waf-token=y"
	c2.LoadAuth()
	if strings.Contains(logs.String(), "security.cookie_external_host") {
		t.Errorf("Bonpreu BaseURL must not warn, got %q", logs.String())
	}
}

func TestSessionRoundTripAndHARSave(t *testing.T) {
	t.Setenv("BONPREU_CONFIG_DIR", t.TempDir())
	if err := SaveSession(Session{Cookie: "c", RegionID: "r"}); err != nil {
		t.Fatal(err)
	}
	if s := LoadSession(); s.Cookie != "c" || s.RegionID != "r" {
		t.Fatalf("LoadSession = %+v", s)
	}
	if err := SaveHARSession(HARSession{Cookie: "c2", RegionID: "r2"}); err != nil {
		t.Fatal(err)
	}
	if s := LoadSession(); s.Cookie != "c2" {
		t.Fatalf("after HAR save: %+v", s)
	}
}

func TestLoadAuthConfigFallback(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BONPREU_CONFIG_DIR", dir)
	body := "[auth]\ncookie = \"global_sid=x\"\n[defaults]\nregion_id = \"r-7\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &Client{}
	if !c.LoadAuth() {
		t.Fatal("LoadAuth should adopt the cookie from config")
	}
	if c.Cookie != "global_sid=x" || c.RegionID != "r-7" {
		t.Errorf("LoadAuth fallback: cookie=%q region=%q", c.Cookie, c.RegionID)
	}
}
