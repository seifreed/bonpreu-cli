package main

import (
	"testing"

	"github.com/seifreed/bonpreu-cli/internal/client"
)

func TestClassifyOffer(t *testing.T) {
	multibuy := func(d string) string { k, _ := classifyOffer([]client.Promotion{{Description: d}}); return k }
	for _, d := range []string{"2a unitat 50% de descompte", "Lot estalvi a 7,75 euros/lot. Clica aquí", "3x2", "2x1", "4x3", "4 x 3", "4×3"} {
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
	if k, _ := classifyOffer([]client.Promotion{{}, {Description: "Abans 5,99€"}}); k != "price" {
		t.Errorf("empty promo should be skipped, got %q", k)
	}

	// multibuy wins over a co-listed price drop, and its label is returned.
	k, label := classifyOffer([]client.Promotion{{Description: "Abans 5,99€"}, {Description: "2a unitat 50% de descompte"}})
	if k != "multibuy" || label != "2a unitat 50% de descompte" {
		t.Errorf("multibuy should win: kind=%q label=%q", k, label)
	}
}

func TestResolveOffersMode(t *testing.T) {
	cases := []struct {
		flag, cfg, want string
		wantErr         bool
	}{
		{flag: "", cfg: "", want: "alert"},              // default
		{flag: "", cfg: "ignore", want: "ignore"},       // config
		{flag: "accept", cfg: "ignore", want: "accept"}, // flag overrides config
		{flag: "bogus", cfg: "", wantErr: true},         // invalid flag is an error
		{flag: "", cfg: "bogus", wantErr: true},         // invalid config is an error
		{flag: "bogus", cfg: "ignore", wantErr: true},   // don't hide a bad flag
	}
	for _, c := range cases {
		got, err := resolveOffersMode(c.flag, c.cfg)
		if (err != nil) != c.wantErr {
			t.Errorf("resolveOffersMode(%q,%q) error = %v, wantErr %v", c.flag, c.cfg, err, c.wantErr)
		}
		if !c.wantErr && got != c.want {
			t.Errorf("resolveOffersMode(%q,%q) = %q, want %q", c.flag, c.cfg, got, c.want)
		}
	}
}
