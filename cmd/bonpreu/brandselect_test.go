package main

import (
	"testing"

	"github.com/seifreed/bonpreu-cli/internal/client"
	"github.com/seifreed/bonpreu-cli/internal/config"
)

func TestNormBrand(t *testing.T) {
	cases := map[string]string{
		"Hellmann's":     "hellmanns",
		"HELLMANN'S":     "hellmanns",
		"Gallina Blanca": "gallinablanca",
		"L'Olivera":      "lolivera",
		"Açúcar Çñ":      "acucarcn",
	}
	for in, want := range cases {
		if got := normBrand(in); got != want {
			t.Errorf("normBrand(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPreferredFor(t *testing.T) {
	b := config.Brands{
		Preferred: []string{"Hellmann's"},
		Overrides: map[string][]string{"pastillas lavavajillas": {"Finish"}},
	}
	if list, ov := preferredFor("pastillas lavavajillas finish", b); !ov || list[0] != "Finish" {
		t.Errorf("override should win: %v %v", list, ov)
	}
	if list, ov := preferredFor("maionesa", b); ov || list[0] != "Hellmann's" {
		t.Errorf("global should apply: %v %v", list, ov)
	}
	// A term that normalizes to empty (symbol-only) has no brand intent: it must
	// fall through to Preferred, not spuriously match an override via Contains("").
	if list, ov := preferredFor("!!!", b); ov || list[0] != "Hellmann's" {
		t.Errorf("empty-normalized term must not match an override: %v %v", list, ov)
	}
}

func prod(id, brand, price string) client.Product {
	return client.Product{RetailerProductID: id, Brand: brand, Price: client.Money{Amount: price}}
}

func TestPickByBrand(t *testing.T) {
	prods := []client.Product{
		prod("a", "BONPREU", "1.50"),
		prod("b", "HELLMANN'S", "3.20"),
		prod("c", "Musa", "2.10"),
		{RetailerProductID: "ad", Brand: "HELLMANN'S", Price: client.Money{Amount: "0.99"}, ExternalAdvertID: "x"}, // sponsored, ignored
	}

	// cheapest_among: cheapest across preferred brands (Musa 2.10 < Hellmann's 3.20).
	got, ok := pickByBrand(prods, []string{"Hellmann's", "Musa"}, "cheapest_among")
	if !ok || got.RetailerProductID != "c" {
		t.Errorf("cheapest_among = %q (ok=%v), want c", got.RetailerProductID, ok)
	}

	// strict_priority: highest-priority brand present wins even if pricier.
	got, ok = pickByBrand(prods, []string{"Hellmann's", "Musa"}, "strict_priority")
	if !ok || got.RetailerProductID != "b" {
		t.Errorf("strict_priority = %q (ok=%v), want b", got.RetailerProductID, ok)
	}

	// sponsored Hellmann's must not be chosen.
	if got.RetailerProductID == "ad" {
		t.Error("sponsored product must not win")
	}

	// no preferred brand stocked → matched=false.
	if _, ok := pickByBrand(prods, []string{"Calvé"}, "cheapest_among"); ok {
		t.Error("expected matched=false when no preferred brand present")
	}
}

func TestResolveBrandHit(t *testing.T) {
	// Cheapest ("a") is deliberately NOT first, so the no-config case proves it
	// picks cheapest, not the relevance top hit.
	prods := []client.Product{prod("b", "HELLMANN'S", "3.20"), prod("a", "BONPREU", "1.50")}
	noCfg := config.Brands{}
	cfg := config.Brands{Preferred: []string{"Hellmann's"}}

	// no config → off, cheapest organic ("a"), even though it's listed second.
	if p, tag, _ := resolveBrandHit(prods, "maionesa", noCfg, nil, false); tag != "off" || p.RetailerProductID != "a" {
		t.Errorf("no-config: got %q tag=%q, want a/off", p.RetailerProductID, tag)
	}
	// config match → preferred.
	if p, tag, _ := resolveBrandHit(prods, "maionesa", cfg, nil, false); tag != "preferred" || p.RetailerProductID != "b" {
		t.Errorf("config match: got %q tag=%q, want b/preferred", p.RetailerProductID, tag)
	}
	// --brand override → override.
	if _, tag, _ := resolveBrandHit(prods, "maionesa", noCfg, []string{"Hellmann's"}, false); tag != "override" {
		t.Errorf("override tag = %q, want override", tag)
	}
	// --no-brands → off even with config.
	if _, tag, _ := resolveBrandHit(prods, "maionesa", cfg, nil, true); tag != "off" {
		t.Errorf("disabled tag = %q, want off", tag)
	}
	// global favourite absent for this product → quiet fallback (off), not a nag.
	if _, tag, _ := resolveBrandHit(prods, "maionesa", config.Brands{Preferred: []string{"Calvé"}}, nil, false); tag != "off" {
		t.Errorf("absent global-brand tag = %q, want off", tag)
	}
	// explicit per-term override absent → none (the skill should ask).
	ov := config.Brands{Overrides: map[string][]string{"maionesa": {"Calvé"}}}
	if _, tag, _ := resolveBrandHit(prods, "maionesa", ov, nil, false); tag != "none" {
		t.Errorf("absent override tag = %q, want none", tag)
	}
}
