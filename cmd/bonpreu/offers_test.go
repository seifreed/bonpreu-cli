package main

import (
	"testing"

	"github.com/seifreed/bonpreu-cli/internal/client"
)

func TestClassifyOffer(t *testing.T) {
	multibuy := func(d string) string { k, _ := classifyOffer([]client.Promotion{{Description: d}}); return k }
	for _, d := range []string{"2a unitat 50% de descompte", "Lot estalvi a 7,75 euros/lot. Clica aquí", "3x2", "2x1"} {
		if multibuy(d) != "multibuy" {
			t.Errorf("%q should be multibuy, got %q", d, multibuy(d))
		}
	}
	if multibuy("Abans 5,99€") != "price" {
		t.Errorf("was-price should be 'price', got %q", multibuy("Abans 5,99€"))
	}
	if k, _ := classifyOffer(nil); k != "" {
		t.Errorf("no promos → empty, got %q", k)
	}

	// multibuy wins over a co-listed price drop, and its label is returned.
	k, label := classifyOffer([]client.Promotion{{Description: "Abans 5,99€"}, {Description: "2a unitat 50% de descompte"}})
	if k != "multibuy" || label != "2a unitat 50% de descompte" {
		t.Errorf("multibuy should win: kind=%q label=%q", k, label)
	}
}

func TestResolveOffersMode(t *testing.T) {
	cases := []struct{ flag, cfg, want string }{
		{"", "", "alert"},              // default
		{"", "ignore", "ignore"},       // config
		{"accept", "ignore", "accept"}, // flag overrides config
		{"bogus", "", "alert"},         // invalid → default
	}
	for _, c := range cases {
		if got := resolveOffersMode(c.flag, c.cfg); got != c.want {
			t.Errorf("resolveOffersMode(%q,%q) = %q, want %q", c.flag, c.cfg, got, c.want)
		}
	}
}
