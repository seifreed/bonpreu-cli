package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirHomeFallback(t *testing.T) {
	t.Setenv("BONPREU_CONFIG_DIR", "") // force the home-dir path
	t.Setenv("HOME", "/home/tester")
	if got := Dir(); got != filepath.Join("/home/tester", ".bonpreu") {
		t.Errorf("Dir() = %q, want ~/.bonpreu", got)
	}
}

func TestDirNoHome(t *testing.T) {
	t.Setenv("BONPREU_CONFIG_DIR", "")
	t.Setenv("HOME", "") // UserHomeDir fails → bare .bonpreu fallback
	if got := Dir(); got != ".bonpreu" {
		t.Errorf("Dir() = %q, want .bonpreu fallback", got)
	}
}

func TestLoadMissingFileErrors(t *testing.T) {
	t.Setenv("BONPREU_CONFIG_DIR", t.TempDir())
	var v map[string]any
	if err := Load("does-not-exist.json", &v); err == nil {
		t.Error("Load of a missing file should error")
	}
}

func TestSaveMkdirFailure(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Config dir would have to be created *under* a regular file → MkdirAll fails.
	t.Setenv("BONPREU_CONFIG_DIR", filepath.Join(blocker, "sub"))
	if err := Save("session.json", map[string]any{"a": 1}); err == nil {
		t.Error("Save should fail when the config dir cannot be created")
	}
}

func TestSaveEncodeError(t *testing.T) {
	t.Setenv("BONPREU_CONFIG_DIR", t.TempDir())
	if err := Save("x.json", make(chan int)); err == nil {
		t.Error("an unmarshalable value should fail to save")
	}
}

func TestSaveCreateTempAndRenameFailures(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BONPREU_CONFIG_DIR", dir)
	if err := Save("nested/name.json", map[string]int{"a": 1}); err == nil {
		t.Fatal("CreateTemp should reject a name containing a path separator")
	}

	if err := os.Mkdir(filepath.Join(dir, "existing"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Save("existing", map[string]int{"a": 1}); err == nil {
		t.Fatal("rename over a directory should fail")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "existing.tmp-") {
			t.Fatalf("temporary file was not cleaned up after rename failure: %s", entry.Name())
		}
	}
}
