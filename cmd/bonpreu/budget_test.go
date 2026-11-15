package main

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveMaxPrecedence(t *testing.T) {
	// Isolate config to a temp dir so a real ~/.bonpreu doesn't leak in.
	dir := privateTestConfigDir(t)

	// flag wins over env.
	t.Setenv("BONPREU_MAX_EUR", "100")
	if v, _ := resolveMax(50, true); v != 50 {
		t.Errorf("flag should win: got %v want 50", v)
	}
	if v, _ := resolveMax(0, true); v != 0 {
		t.Errorf("explicit zero flag should disable the limit: got %v want 0", v)
	}

	// env wins when no flag.
	if v, _ := resolveMax(0, false); v != 100 {
		t.Errorf("env should win: got %v want 100", v)
	}

	// config used when no flag/env (empty env reads as unset by resolveMax).
	t.Setenv("BONPREU_MAX_EUR", "")
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[limits]\nmax_eur = 30\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if v, _ := resolveMax(0, false); v != 30 {
		t.Errorf("config should be used: got %v want 30", v)
	}

	// invalid env is an error.
	t.Setenv("BONPREU_MAX_EUR", "notanumber")
	if _, err := resolveMax(0, false); err == nil {
		t.Error("invalid BONPREU_MAX_EUR should error")
	}
	for _, invalid := range []float64{-1, math.NaN(), math.Inf(1)} {
		if _, err := resolveMax(invalid, true); err == nil {
			t.Errorf("invalid --max %v should error", invalid)
		}
	}
	for _, invalid := range []string{"NaN", "Inf", "-1"} {
		t.Setenv("BONPREU_MAX_EUR", invalid)
		if _, err := resolveMax(0, false); err == nil {
			t.Errorf("invalid BONPREU_MAX_EUR %q should error", invalid)
		}
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
	for _, invalid := range []float64{math.NaN(), math.Inf(1), -1} {
		if err := checkBudget(1, invalid, "cart"); err == nil {
			t.Errorf("invalid budget %v should error", invalid)
		}
		if err := checkBudget(invalid, 10, "cart"); err == nil {
			t.Errorf("invalid total %v should error", invalid)
		}
	}
}

func TestResolveMaxFailsOnMalformedConfig(t *testing.T) {
	dir := privateTestConfigDir(t)
	t.Setenv("BONPREU_MAX_EUR", "") // force the config path
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("not valid = toml ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveMax(0, false); err == nil {
		t.Fatal("a malformed config must surface an error, not silently disable the limit")
	}
}

func TestResolveMaxFailsOnMisspelledLimitKey(t *testing.T) {
	dir := privateTestConfigDir(t)
	t.Setenv("BONPREU_MAX_EUR", "")
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[limits]\nmax_eur_limt = 5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveMax(0, false); err == nil {
		t.Fatal("an unknown limit key must not silently disable the spending guard")
	}
}
