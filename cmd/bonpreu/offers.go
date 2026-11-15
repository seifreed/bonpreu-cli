package main

import (
	"fmt"
	"regexp"
	"strconv"

	"github.com/seifreed/bonpreu-cli/internal/client"
)

// multibuyHints flag a promotion whose value depends on buying more (a second
// unit, a lot, an NxM deal) — worth a heads-up because the user can act on it,
// unlike a was-price or a flat % / loyalty-card discount that already applies.
// Bonpreu tags every promotion type "OFFER", so the distinction is text-only.
var (
	nxmOffer      = regexp.MustCompile(`(?i)\b(\d+)\s*[x×]\s*(\d+)\b`)
	unitOffer     = regexp.MustCompile(`(?i)\b(?:[2-9]\d*\s*(?:a|ª|º)|segona|segunda)\s+(?:unitat|unidad)\b`)
	multibuyWords = regexp.MustCompile(`(?i)\b(?:lot(?:e)?|emporta(?:['’]t)?|ll[eé]va(?:te)?)\b`)
)

// isMultibuy reports whether a promotion description is a quantity-dependent deal.
func isMultibuy(desc string) bool {
	if unitOffer.MatchString(desc) || multibuyWords.MatchString(desc) {
		return true
	}
	for _, match := range nxmOffer.FindAllStringSubmatchIndex(desc, -1) {
		if adjacentDecimal(desc, match[0], match[1]) {
			continue
		}
		buy, buyErr := strconv.Atoi(desc[match[2]:match[3]])
		get, getErr := strconv.Atoi(desc[match[4]:match[5]])
		if buyErr == nil && getErr == nil && buy > get {
			return true
		}
	}
	return false
}

func adjacentDecimal(s string, start, end int) bool {
	if start >= 2 && (s[start-1] == '.' || s[start-1] == ',') && isDigit(s[start-2]) {
		return true
	}
	return end+1 < len(s) && (s[end] == '.' || s[end] == ',') && isDigit(s[end+1])
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

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
