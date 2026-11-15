package main

import (
	"fmt"
	"strings"

	"github.com/seifreed/bonpreu-cli/internal/client"
)

// multibuyHints flag a promotion whose value depends on buying more (a second
// unit, a lot, an NxM deal) — worth a heads-up because the user can act on it,
// unlike a was-price or a flat % / loyalty-card discount that already applies.
// Bonpreu tags every promotion type "OFFER", so the distinction is text-only.
var multibuyHints = []string{"unitat", "unidad", "lot", "emporta", "leva", "2x", "3x", "x2", "x1"}

// isMultibuy reports whether a promotion description is a quantity-dependent deal.
func isMultibuy(desc string) bool {
	d := strings.ToLower(desc)
	for _, h := range multibuyHints {
		if strings.Contains(d, h) {
			return true
		}
	}
	return false
}

// classifyOffer inspects a product's promotions: "multibuy" (a quantity deal worth
// a heads-up) wins; else "price" (a was-price drop, informational); else "". label
// is the promo text to show.
func classifyOffer(promos []client.Promotion) (kind, label string) {
	var first string
	for _, p := range promos {
		if p.Description == "" {
			continue
		}
		if first == "" {
			first = p.Description
		}
		if isMultibuy(p.Description) {
			return "multibuy", p.Description
		}
	}
	if first != "" {
		return "price", first
	}
	return "", ""
}

// resolveOffersMode picks the offers behaviour: flag overrides config, else the
// default "alert" (surface actionable offers and ask before taking them).
func resolveOffersMode(flag, cfg string) (string, error) {
	valid := func(mode string) bool {
		return mode == "alert" || mode == "ignore" || mode == "accept"
	}
	if flag != "" && !valid(flag) {
		return "", fmt.Errorf("invalid --offers %q (want alert, ignore, or accept)", flag)
	}
	if cfg != "" && !valid(cfg) {
		return "", fmt.Errorf("invalid [offers].mode %q (want alert, ignore, or accept)", cfg)
	}
	if flag != "" {
		return flag, nil
	}
	if cfg != "" {
		return cfg, nil
	}
	return "alert", nil
}
