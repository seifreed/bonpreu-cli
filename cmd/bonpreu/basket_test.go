package main

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPriceCents(t *testing.T) {
	cases := map[string]int64{
		"1.69": 169, "8.55": 855, "0.96": 96, "10": 1000, "0": 0,
		"1.005": 101, "9.995": 1000, "1e-2": 1, "0x1p+0": 100,
		"1_000.00": 100000, "92233720368547758.07": math.MaxInt64,
	}
	for in, want := range cases {
		got, err := priceCents(in)
		if err != nil || got != want {
			t.Errorf("priceCents(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	if _, err := priceCents("abc"); err == nil {
		t.Error("expected error for non-numeric price")
	}
	for _, bad := range []string{"NaN", "Inf", "+Inf", "-Inf", "-1.00", "1e308", "1e999999", "92233720368547760"} {
		if cents, err := priceCents(bad); err == nil {
			t.Errorf("priceCents(%q) = %d, want invalid-price error", bad, cents)
		}
	}
}

func TestRatFromDecimalRejectsInvalidInput(t *testing.T) {
	if _, err := ratFromDecimal("not a number"); err == nil {
		t.Fatal("ratFromDecimal should report malformed decimal input")
	}
}

func TestLineCentsRejectsOverflow(t *testing.T) {
	if got, err := lineCents(169, 2); err != nil || got != 338 {
		t.Fatalf("lineCents(169, 2) = %d, %v; want 338, nil", got, err)
	}
	if got, err := lineCents(25, 0.580); err != nil || got != 15 {
		t.Fatalf("lineCents(25, 0.580) = %d, %v; want 15, nil", got, err)
	}
	if _, err := lineCents(169, 1e300); err == nil {
		t.Fatal("lineCents must reject a subtotal that overflows int64")
	}
	for _, input := range []struct {
		cents int64
		qty   float64
	}{{-1, 1}, {169, -1}, {169, math.NaN()}, {169, math.Inf(1)}} {
		if _, err := lineCents(input.cents, input.qty); err == nil {
			t.Errorf("lineCents(%d, %v) must reject invalid input", input.cents, input.qty)
		}
	}
}

func TestCentsStr(t *testing.T) {
	cases := map[int64]string{
		169: "1.69", 5: "0.05", 1000: "10.00", 0: "0.00",
		-105: "-1.05", -5: "-0.05", -1000: "-10.00", // negatives keep a single leading sign
		math.MinInt64: "-92233720368547758.08",
	}
	for in, want := range cases {
		if got := centsStr(in); got != want {
			t.Errorf("centsStr(%d) = %q; want %q", in, got, want)
		}
	}
}

func TestFmtQtyLargeInteger(t *testing.T) {
	if got := fmtQty(1e20); got != "1e+20" {
		t.Fatalf("fmtQty(1e20) = %q; want exact finite representation", got)
	}
}

func TestParseBasketLine(t *testing.T) {
	bl, err := parseBasketLine("00944 2")
	if err != nil || bl.id != "00944" || bl.qty != 2 {
		t.Fatalf("got %+v, %v", bl, err)
	}
	bl, err = parseBasketLine("49637")
	if err != nil || bl.qty != 1 {
		t.Fatalf("bare id should default qty 1: %+v %v", bl, err)
	}
	if _, err := parseBasketLine("a b c"); err == nil {
		t.Error("expected error on malformed line")
	}
	// Non-finite quantities must be rejected: ParseFloat accepts them but they
	// silently corrupt totals (Inf) or drop a line (NaN) and bypass the --max guard.
	for _, bad := range []string{"00944 NaN", "00944 Inf", "00944 +Inf", "00944 -Inf", "00944 nan"} {
		if _, err := parseBasketLine(bad); err == nil {
			t.Errorf("parseBasketLine(%q) should reject a non-finite qty", bad)
		}
	}
}

func TestStripComment(t *testing.T) {
	if got := stripComment("00944 1  # Tomàquet"); got != "00944 1" {
		t.Errorf("stripComment = %q", got)
	}
	if got := stripComment("# whole line"); got != "" {
		t.Errorf("whole-line comment should vanish, got %q", got)
	}
}

func TestCollectFromFileAndStdin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "basket.txt")
	body := "# comment\n23597 2\n\n00944  # trailing comment\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	lines, err := collectBasket(path, nil)
	if err != nil || len(lines) != 2 || lines[0].id != "23597" || lines[0].qty != 2 || lines[1].qty != 1 {
		t.Fatalf("collectBasket(file) = %+v, err %v", lines, err)
	}

	terms, err := collectLines(path, nil)
	if err != nil || len(terms) != 2 {
		t.Fatalf("collectLines(file) = %v, err %v", terms, err)
	}

	// stdin ("-")
	withStdin(t, "11111 3\n", func() {
		lines, err := collectBasket("-", nil)
		if err != nil || len(lines) != 1 || lines[0].qty != 3 {
			t.Fatalf("collectBasket(stdin) = %+v, err %v", lines, err)
		}
	})

	// malformed line surfaces a line-numbered error
	bad := filepath.Join(dir, "bad.txt")
	if err := os.WriteFile(bad, []byte("ok 1\nx y z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := collectBasket(bad, nil); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("expected line-2 error, got %v", err)
	}
}

func TestScanSourceLineTooLong(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.txt")
	huge := strings.Repeat("a", 2*1024*1024) // exceeds the 1 MB scanner buffer
	if err := os.WriteFile(p, []byte(huge+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := collectBasket(p, nil); err == nil {
		t.Error("an over-long line should surface a scanner error")
	}
}
