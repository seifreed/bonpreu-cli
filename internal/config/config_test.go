package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigParsesTOML(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BONPREU_CONFIG_DIR", dir)

	const body = `[auth]
cookie = "global_sid=abc; aws-waf-token=def"
[defaults]
region_id = "d57f6327-ce67-44d5-bdd5-dc63e98460a6"
lang = "es"
postal_code = "08001"
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
		got.Defaults.Lang != "es" || got.Defaults.PostalCode != "08001" ||
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
	t.Setenv("BONPREU_CONFIG_DIR", t.TempDir())
	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("missing config should not error: %v", err)
	}
	if c.Auth.Cookie != "" || c.Defaults.RegionID != "" || c.Limits.MaxEUR != 0 || c.Brands.Configured() {
		t.Errorf("missing config should be zero value, got %+v", c)
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
	t.Setenv("BONPREU_CONFIG_DIR", t.TempDir())
	type state struct {
		Cookie string `json:"cookie"`
		Region string `json:"region"`
	}
	in := state{Cookie: "c", Region: "r"}
	if err := Save("session.json", in); err != nil {
		t.Fatalf("Save: %v", err)
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
