package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/seifreed/bonpreu-cli/internal/client"
)

// reorderArgs must hoist flags ahead of positionals but keep negative-number
// tokens (e.g. a quantity like -1) positional — no flag here starts with a digit.
func TestReorderArgs(t *testing.T) {
	newFS := func() *flag.FlagSet {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.Bool("json", false, "")
		fs.Float64("max", 0, "")
		return fs
	}

	// `cart add 23597 2 --max 50` → flags hoisted, ids positional.
	got := reorderArgs(newFS(), []string{"23597", "2", "--max", "50"})
	want := []string{"--max", "50", "--", "23597", "2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("flag hoist: got %v want %v", got, want)
	}

	// `cart add 23597 -1` → -1 stays positional, not treated as a flag.
	got = reorderArgs(newFS(), []string{"23597", "-1"})
	want = []string{"--", "23597", "-1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("negative number: got %v want %v", got, want)
	}

	// bool flag takes no value.
	got = reorderArgs(newFS(), []string{"pa", "--json"})
	want = []string{"--json", "--", "pa"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("bool flag: got %v want %v", got, want)
	}
}

func TestNewClientWarnsOnMalformedConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BONPREU_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("bad = toml ["), 0o600); err != nil {
		t.Fatal(err)
	}
	var logs []string
	old := stderrLogf
	stderrLogf = func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }
	defer func() { stderrLogf = old }()

	_ = newClient(&common{})
	if len(logs) != 1 || !strings.Contains(logs[0], "config.toml could not be read") {
		t.Fatalf("expected a malformed-config warning, got %v", logs)
	}
}

func TestNewClientSilentWithoutConfig(t *testing.T) {
	t.Setenv("BONPREU_CONFIG_DIR", t.TempDir()) // no config.toml present
	var logs []string
	old := stderrLogf
	stderrLogf = func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }
	defer func() { stderrLogf = old }()

	_ = newClient(&common{})
	if len(logs) != 0 {
		t.Errorf("missing config is normal → no warning, got %v", logs)
	}
}

func TestPureHelperEdges(t *testing.T) {
	if money(client.Money{}) != "?" {
		t.Error("empty money → ?")
	}
	if fmtQty(1.5) != "1.5" {
		t.Error("fractional qty")
	}
	if plural(1, "línia", "línies") != "1 línia" {
		t.Error("singular plural")
	}
	if isFresh(client.Product{CategoryPath: []string{"Congelats"}}) {
		t.Error("frozen category is not fresh")
	}
	if !isFresh(client.Product{CategoryPath: []string{"Lactis"}}) {
		t.Error("normal category is fresh")
	}
	if restrictionsCa(nil) != "(cap)" {
		t.Error("no restrictions → (cap)")
	}
	if !strings.Contains(restrictionsCa([]string{"WAT"}), "WAT") {
		t.Error("unknown code passes through")
	}
	all := []client.Product{{ExternalAdvertID: "ad", Name: "Sponsored"}}
	if len(organic(all)) != 0 {
		t.Error("all-sponsored → no organic results")
	}
	if cheapestOrganic(all).Name != "Sponsored" {
		t.Error("all-sponsored → first element fallback")
	}
	if _, err := parseBasketLine("a b c"); err == nil {
		t.Error("3 fields → error")
	}
	if _, err := parsePositiveQty("0", false); err == nil {
		t.Error("qty 0 for add → error")
	}
}

func TestReorderArgsTerminator(t *testing.T) {
	fs, _ := newCommonFlags("t")
	out := reorderArgs(fs, []string{"a", "--", "-x", "b"})
	// everything after -- is positional, including -x
	joined := strings.Join(out, " ")
	if !strings.Contains(joined, "-- ") || !strings.Contains(joined, "-x") {
		t.Errorf("terminator handling: %v", out)
	}
}

func TestEmitRawUnparseable(t *testing.T) {
	out, err := capture(t, func() error { return emitRaw([]byte("not json")) })
	if err != nil || !strings.Contains(out, "not json") {
		t.Fatalf("emitRaw fallback: %q err %v", out, err)
	}
}

func TestEmitWriteErrorsSurface(t *testing.T) {
	srv := apiServer(t)
	defer srv.Close()
	useServer(t, srv, true)

	handlers := map[string]func() error{
		"search --json":     func() error { return cmdSearch([]string{"--json", "llet"}) },
		"search --toon":     func() error { return cmdSearch([]string{"--toon", "llet"}) },
		"batch --json":      func() error { return cmdBatch([]string{"--json", "llet"}) },
		"total --json":      func() error { return cmdTotal([]string{"--json", "23597", "2"}) },
		"product --json":    func() error { return cmdProduct([]string{"--json", "23597"}) },
		"product --toon":    func() error { return cmdProduct([]string{"--toon", "23597"}) },
		"categories --json": func() error { return cmdCategories([]string{"--json"}) },
		"category --json":   func() error { return cmdCategories([]string{"--json", "--id", "0301"}) },
		"cart get --json":   func() error { return cmdCart([]string{"get", "--json"}) },
		"cart get --toon":   func() error { return cmdCart([]string{"get", "--toon"}) },
		"checkout --json":   func() error { return cmdCheckout([]string{"status", "--json"}) },
		"addresses --json":  func() error { return cmdCheckout([]string{"addresses", "--json"}) },
		"addresses --toon":  func() error { return cmdCheckout([]string{"addresses", "--toon"}) },
		"whoami --json":     func() error { return cmdWhoami([]string{"--json"}) },
	}
	for name, h := range handlers {
		if err := withFailingStdout(t, h); err == nil {
			t.Errorf("%s: a stdout write failure should surface as an error", name)
		}
	}
}

func TestNewClientConfigDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BONPREU_CONFIG_DIR", dir)
	t.Setenv("BONPREU_BASE_URL", "")
	body := "[defaults]\nlang = \"es\"\nregion_id = \"r-1\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cl := newClient(&common{})
	if cl.Lang != "es" || cl.RegionID != "r-1" {
		t.Errorf("newClient defaults: lang=%q region=%q", cl.Lang, cl.RegionID)
	}
}

func TestReorderArgsFlagEquals(t *testing.T) {
	fs, _ := newCommonFlags("t")
	out := reorderArgs(fs, []string{"--lang=es", "term"})
	if out[0] != "--lang=es" {
		t.Errorf("--flag=value should be kept intact: %v", out)
	}
}

func TestEmitRawEmpty(t *testing.T) {
	if err := emitRaw(nil); err != nil {
		t.Errorf("emitRaw(nil) should be a no-op: %v", err)
	}
}
