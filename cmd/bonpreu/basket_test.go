package main

import "testing"

func TestPriceCents(t *testing.T) {
	cases := map[string]int64{"1.69": 169, "8.55": 855, "0.96": 96, "10": 1000, "0": 0}
	for in, want := range cases {
		got, err := priceCents(in)
		if err != nil || got != want {
			t.Errorf("priceCents(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	if _, err := priceCents("abc"); err == nil {
		t.Error("expected error for non-numeric price")
	}
}

func TestCentsStr(t *testing.T) {
	cases := map[int64]string{169: "1.69", 5: "0.05", 1000: "10.00", 0: "0.00"}
	for in, want := range cases {
		if got := centsStr(in); got != want {
			t.Errorf("centsStr(%d) = %q; want %q", in, got, want)
		}
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
}

func TestStripComment(t *testing.T) {
	if got := stripComment("00944 1  # Tomàquet"); got != "00944 1" {
		t.Errorf("stripComment = %q", got)
	}
	if got := stripComment("# whole line"); got != "" {
		t.Errorf("whole-line comment should vanish, got %q", got)
	}
}
