package client

import (
	"fmt"
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
	var logs []string
	c := &Client{Logf: func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }}
	c.LoadAuth()
	return logs
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
	var logs []string
	c := &Client{
		BaseURL: "https://evil.example",
		Logf:    func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) },
	}
	c.Cookie = "global_sid=x; aws-waf-token=y"
	c.LoadAuth() // BaseURL stays foreign; cookie already set → must warn
	var warned bool
	for _, l := range logs {
		if strings.Contains(l, "non-Bonpreu host") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("expected a foreign-host cookie warning, got %v", logs)
	}

	// A real Bonpreu BaseURL must NOT warn.
	logs = nil
	c2 := &Client{
		BaseURL: BaseURL,
		Logf:    func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) },
	}
	c2.Cookie = "global_sid=x; aws-waf-token=y"
	c2.LoadAuth()
	for _, l := range logs {
		if strings.Contains(l, "non-Bonpreu host") {
			t.Errorf("Bonpreu BaseURL must not warn, got %v", logs)
		}
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
