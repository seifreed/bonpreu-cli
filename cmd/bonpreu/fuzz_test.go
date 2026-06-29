package main

import "testing"

// Fuzz the parsers that consume user-supplied list files (-f basket/term files) and
// API price strings. None may panic on adversarial input — a crash would be a DoS
// on `total`/`batch`/`cart`. Seed corpus also runs under plain `go test`.

func FuzzParseBasketLine(f *testing.F) {
	for _, s := range []string{"", "00944", "00944 2", "x NaN", "a b c", "  ", "1 -0", "9 1e400", "id 0.5"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		bl, err := parseBasketLine(s)
		if err == nil && (bl.qty < 0 || bl.qty != bl.qty) { // NaN != NaN
			t.Fatalf("parseBasketLine(%q) accepted a bad qty %v", s, bl.qty)
		}
	})
}

func FuzzPriceCents(f *testing.F) {
	for _, s := range []string{"", "0", "1.69", "-1.05", "abc", "1e308", "NaN", "  10  ", "1,69"} {
		f.Add(s)
	}
	f.Fuzz(func(_ *testing.T, s string) {
		_, _ = priceCents(s) // may error, must not panic
	})
}

func FuzzStripComment(f *testing.F) {
	for _, s := range []string{"", "#", "a # b", "00944 1 # name", "####"} {
		f.Add(s)
	}
	f.Fuzz(func(_ *testing.T, s string) {
		_ = stripComment(s)
	})
}
