package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func privateConfigTestDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "config")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BONPREU_CONFIG_DIR", dir)
	return dir
}

func TestLoadConfigParsesTOML(t *testing.T) {
	dir := privateConfigTestDir(t)

	const body = `[auth]
cookie = "global_sid=abc; aws-waf-token=def"
[defaults]
region_id = "d57f6327-ce67-44d5-bdd5-dc63e98460a6"
lang = "es"
[limits]
max_eur = 75.5
[brands]
preferred = ["Hellmann's", "Asturiana"]
mode = "strict_priority"
on_missing = "ask"
[brands.overrides]
"pastillas lavavajillas" = ["Finish"]
`
	if err := os.WriteFile(filepath.Join(dir, configFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if got.Auth.Cookie != "global_sid=abc; aws-waf-token=def" ||
		got.Defaults.RegionID != "d57f6327-ce67-44d5-bdd5-dc63e98460a6" ||
		got.Defaults.Lang != "es" ||
		got.Limits.MaxEUR != 75.5 {
		t.Errorf("LoadConfig core fields mismatch: %+v", got)
	}
	if len(got.Brands.Preferred) != 2 || got.Brands.Preferred[0] != "Hellmann's" ||
		got.Brands.Mode != "strict_priority" || got.Brands.OnMissing != "ask" ||
		got.Brands.Overrides["pastillas lavavajillas"][0] != "Finish" {
		t.Errorf("brands section mismatch: %+v", got.Brands)
	}
	if !got.Brands.Configured() {
		t.Error("Configured() should be true")
	}
}

func TestLoadConfigMissingIsEmpty(t *testing.T) {
	privateConfigTestDir(t)
	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("missing config should not error: %v", err)
	}
	if c.Auth.Cookie != "" || c.Defaults.RegionID != "" || c.Limits.MaxEUR != 0 || c.Brands.Configured() {
		t.Errorf("missing config should be zero value, got %+v", c)
	}
}

func TestLoadConfigRejectsUnknownKeys(t *testing.T) {
	dir := privateConfigTestDir(t)
	if err := os.WriteFile(filepath.Join(dir, configFile), []byte("[limits]\nmax_eur_limit = 5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "limits.max_eur_limit") {
		t.Fatalf("LoadConfig error = %v, want unknown key path", err)
	}
}

func TestLoadConfigRejectsPostalCodeWithoutRegionResolver(t *testing.T) {
	dir := privateConfigTestDir(t)
	if err := os.WriteFile(filepath.Join(dir, configFile), []byte("[defaults]\npostal_code = \"08001\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "defaults.postal_code") {
		t.Fatalf("LoadConfig error = %v, want unsupported postal_code key", err)
	}
}

func TestLoadConfigReturnsMalformedTOMLError(t *testing.T) {
	dir := privateConfigTestDir(t)
	if err := os.WriteFile(filepath.Join(dir, configFile), []byte("[limits\nmax_eur = 5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig accepted malformed TOML")
	}
}

func TestConfigReadsRejectPermissiveExistingDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ACLs are not represented by Go permission bits")
	}
	for _, read := range []struct {
		name string
		call func() error
	}{
		{name: "toml", call: func() error { _, err := LoadConfig(); return err }},
		{name: "session", call: func() error { var state map[string]string; return Load("session.json", &state) }},
	} {
		t.Run(read.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "bonpreu")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, configFile), []byte("[auth]\ncookie = \"secret\"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte(`{"cookie":"secret"}`), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Setenv("BONPREU_CONFIG_DIR", dir)

			if err := read.call(); err == nil || !strings.Contains(err.Error(), "must have mode 0700") {
				t.Fatalf("read config error = %v, want private-directory error", err)
			}
			info, err := os.Stat(dir)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != 0o755 {
				t.Fatalf("config directory permissions = %04o, want unchanged 0755", got)
			}
		})
	}
}

func TestSaveRejectsPermissiveExistingDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ACLs are not represented by Go permission bits")
	}
	dir := filepath.Join(t.TempDir(), "bonpreu")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BONPREU_CONFIG_DIR", dir)

	if err := Save("session.json", map[string]string{"cookie": "secret"}); err == nil || !strings.Contains(err.Error(), "must have mode 0700") {
		t.Fatalf("Save error = %v, want private-directory error", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "session.json")); !os.IsNotExist(err) {
		t.Fatalf("session file stat error = %v, want no secret written", err)
	}
}

func TestLoadConfigStatErrorSurfaces(t *testing.T) {
	// A stat error that is NOT "file missing" must be returned, not swallowed as an
	// empty config — otherwise the spending guard would silently disable. Point the
	// config dir under a regular file so stat'ing config.toml fails with ENOTDIR.
	dir := t.TempDir()
	notADir := filepath.Join(dir, "afile")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BONPREU_CONFIG_DIR", filepath.Join(notADir, "nested"))
	if _, err := LoadConfig(); err == nil {
		t.Error("LoadConfig should return a non-IsNotExist stat error, not swallow it")
	}
}

func TestSaveLoadJSONState(t *testing.T) {
	privateConfigTestDir(t)
	type state struct {
		Cookie string `json:"cookie"`
		Region string `json:"region"`
	}
	in := state{Cookie: "c", Region: "r"}
	if err := Save("session.json", in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(filepath.Join(Dir(), "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("saved session permissions = %04o; want 0600", got)
	}
	var out state
	if err := Load("session.json", &out); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out != in {
		t.Errorf("JSON round-trip: got %+v want %+v", out, in)
	}
}

func TestDirEnvOverride(t *testing.T) {
	t.Setenv("BONPREU_CONFIG_DIR", "/tmp/custom-bonpreu")
	if Dir() != "/tmp/custom-bonpreu" {
		t.Errorf("Dir() = %q, want the env override", Dir())
	}
}
