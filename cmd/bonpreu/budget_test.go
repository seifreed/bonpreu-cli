package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveMaxPrecedence(t *testing.T) {
	// Isolate config to a temp dir so a real ~/.bonpreu doesn't leak in.
	dir := t.TempDir()
	t.Setenv("BONPREU_CONFIG_DIR", dir)

	// flag wins over env.
	t.Setenv("BONPREU_MAX_EUR", "100")
	if v, _ := resolveMax(50); v != 50 {
		t.Errorf("flag should win: got %v want 50", v)
	}

	// env wins when no flag.
	if v, _ := resolveMax(0); v != 100 {
		t.Errorf("env should win: got %v want 100", v)
	}

	// config used when no flag/env (empty env reads as unset by resolveMax).
	t.Setenv("BONPREU_MAX_EUR", "")
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[limits]\nmax_eur = 30\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if v, _ := resolveMax(0); v != 30 {
		t.Errorf("config should be used: got %v want 30", v)
	}

	// invalid env is an error.
	t.Setenv("BONPREU_MAX_EUR", "notanumber")
	if _, err := resolveMax(0); err == nil {
		t.Error("invalid BONPREU_MAX_EUR should error")
	}
}

func TestCheckBudget(t *testing.T) {
	if err := checkBudget(40, 50, "cart"); err != nil {
		t.Errorf("under cap should pass: %v", err)
	}
	if err := checkBudget(60, 50, "cart"); err == nil {
		t.Error("over cap should fail")
	}
	if err := checkBudget(999, 0, "cart"); err != nil {
		t.Errorf("cap 0 disables the guard: %v", err)
	}
}

func TestResolveMaxFailsOnMalformedConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BONPREU_CONFIG_DIR", dir)
	t.Setenv("BONPREU_MAX_EUR", "") // force the config path
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("not valid = toml ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveMax(0); err == nil {
		t.Fatal("a malformed config must surface an error, not silently disable the limit")
	}
}
