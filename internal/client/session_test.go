package client

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func privateClientConfigTestDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "config")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BONPREU_CONFIG_DIR", dir)
	return dir
}

func loadAuthResult(t *testing.T, writeSession []byte) (bool, string, error) {
	t.Helper()
	dir := privateClientConfigTestDir(t)
	if writeSession != nil {
		if err := os.WriteFile(filepath.Join(dir, sessionFile), writeSession, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var logs bytes.Buffer
	c := &Client{Logger: testLogger(&logs)}
	loaded, err := c.LoadAuth()
	return loaded, logs.String(), err
}

func TestLoadAuthReturnsCorruptSessionError(t *testing.T) {
	loaded, logs, err := loadAuthResult(t, []byte("{ not valid json"))
	if loaded || err == nil || !strings.Contains(err.Error(), "read cached session") {
		t.Fatalf("LoadAuth = %v, %v; want corrupt-session error", loaded, err)
	}
	if logs != "" {
		t.Fatalf("auth error should be logged at the command boundary, got duplicate library log %q", logs)
	}
}

func TestLoadAuthReturnsMalformedConfigError(t *testing.T) {
	dir := privateClientConfigTestDir(t)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("not valid = toml ["), 0o600); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	loaded, err := (&Client{Logger: testLogger(&logs)}).LoadAuth()
	if loaded || err == nil || !strings.Contains(err.Error(), "read authentication config") {
		t.Fatalf("LoadAuth = %v, %v; want config error", loaded, err)
	}
	if logs.Len() != 0 {
		t.Fatalf("auth error should be logged at the command boundary, got %q", logs.String())
	}
}

func TestLoadAuthSilentWhenNoSession(t *testing.T) {
	loaded, logs, err := loadAuthResult(t, nil)
	if loaded || err != nil || logs != "" {
		t.Errorf("missing session should be a normal unauthenticated state: %v, %v, logs=%q", loaded, err, logs)
	}
}

func TestLoadAuthWarnsOnForeignBaseURL(t *testing.T) {
	privateClientConfigTestDir(t)
	var logs bytes.Buffer
	c := &Client{
		BaseURL: "https://evil.example",
		Logger:  testLogger(&logs),
	}
	c.Cookie = "global_sid=x; aws-waf-token=y"
	if _, err := c.LoadAuth(); err != nil { // BaseURL stays foreign; cookie already set → must warn
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "security.cookie_external_host") {
		t.Errorf("expected a foreign-host cookie warning, got %q", logs.String())
	}

	// A real Bonpreu BaseURL must NOT warn.
	logs.Reset()
	c2 := &Client{
		BaseURL: BaseURL,
		Logger:  testLogger(&logs),
	}
	c2.Cookie = "global_sid=x; aws-waf-token=y"
	if _, err := c2.LoadAuth(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "security.cookie_external_host") {
		t.Errorf("Bonpreu BaseURL must not warn, got %q", logs.String())
	}

	logs.Reset()
	c3 := &Client{BaseURL: "https://attacker.bonpreuesclat.cat", Logger: testLogger(&logs)}
	c3.Cookie = "global_sid=x; aws-waf-token=y"
	if _, err := c3.LoadAuth(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "security.cookie_external_host") {
		t.Errorf("unrecognized Bonpreu subdomain must warn, got %q", logs.String())
	}
}

func TestSessionRoundTripAndHARSave(t *testing.T) {
	privateClientConfigTestDir(t)
	if err := SaveSession(Session{Cookie: "c", RegionID: "r"}); err != nil {
		t.Fatal(err)
	}
	if s, err := LoadSession(); err != nil || s.Cookie != "c" || s.RegionID != "r" {
		t.Fatalf("LoadSession = %+v", s)
	}
	if err := SaveHARSession(HARSession{Cookie: "c2", RegionID: "r2"}); err != nil {
		t.Fatal(err)
	}
	if s, err := LoadSession(); err != nil || s.Cookie != "c2" {
		t.Fatalf("after HAR save: %+v", s)
	}
}

func TestLoadAuthConfigFallback(t *testing.T) {
	dir := privateClientConfigTestDir(t)
	body := "[auth]\ncookie = \"global_sid=x\"\n[defaults]\nregion_id = \"r-7\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &Client{}
	if loaded, err := c.LoadAuth(); err != nil || !loaded {
		t.Fatal("LoadAuth should adopt the cookie from config")
	}
	if c.Cookie != "global_sid=x" || c.RegionID != "r-7" {
		t.Errorf("LoadAuth fallback: cookie=%q region=%q", c.Cookie, c.RegionID)
	}
}
