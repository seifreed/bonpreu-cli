package main

import (
	"sort"
	"strings"

	"github.com/seifreed/bonpreu-cli/internal/client"
	"github.com/seifreed/bonpreu-cli/internal/config"
)

// normBrand canonicalises a brand for comparison: lowercase, accents folded, and
// everything but letters/digits dropped — so "Hellmann's" matches "HELLMANN'S"
// and "Gallina Blanca" matches "gallina blanca". Avoids an x/text dependency with
// a small fold table covering Catalan/Spanish.
func normBrand(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch r {
		case 'à', 'á', 'â', 'ä', 'a':
			b.WriteRune('a')
		case 'è', 'é', 'ê', 'ë':
			b.WriteRune('e')
		case 'í', 'ì', 'î', 'ï':
			b.WriteRune('i')
		case 'ò', 'ó', 'ô', 'ö':
			b.WriteRune('o')
		case 'ú', 'ù', 'û', 'ü':
			b.WriteRune('u')
		case 'ç':
			b.WriteRune('c')
		case 'ñ':
			b.WriteRune('n')
		default:
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// preferredFor returns the brand list whose normalized override key most
// specifically matches a search term (substring either direction); otherwise it
// returns the global Preferred list. Equal-length matches use the key text as a
// stable tie-breaker. isOverride reports whether an override matched.
func preferredFor(term string, b config.Brands) (brands []string, isOverride bool) {
	nt := normBrand(term)
	bestKey, bestNorm := "", ""
	var bestList []string
	for key, list := range b.Overrides {
		nk := normBrand(key)
		// Both sides must be non-empty because strings.Contains(x, "") is always true.
		if nk != "" && nt != "" && (strings.Contains(nt, nk) || strings.Contains(nk, nt)) {
			if len(nk) > len(bestNorm) || len(nk) == len(bestNorm) && key < bestKey {
				bestKey, bestNorm, bestList = key, nk, list
			}
		}
	}
	if bestKey != "" {
		return bestList, true
	}
	return b.Preferred, false
}

// pickByBrand chooses, among organic results, the cheapest product whose brand is
// in prefs (mode "cheapest_among"), or the cheapest of the highest-priority brand
// present (mode "strict_priority"). matched is false when no preferred brand is
// stocked — the caller decides whether to ask or fall back to cheapest.
func pickByBrand(prods []client.Product, prefs []string, mode string) (chosen client.Product, matched bool) {
	wanted := make([]string, 0, len(prefs))
	for _, p := range prefs {
		if n := normBrand(p); n != "" {
			wanted = append(wanted, n)
		}
	}
	if len(wanted) == 0 {
		return client.Product{}, false
	}

	if mode == "strict_priority" {
		for _, w := range wanted {
			if hits := brandMatches(prods, []string{w}); len(hits) > 0 {
				return cheapestOf(hits), true
			}
		}
		return client.Product{}, false
	}
	// cheapest_among (default): cheapest across any preferred brand.
	hits := brandMatches(prods, wanted)
	if len(hits) == 0 {
		return client.Product{}, false
	}
	return cheapestOf(hits), true
}

// organic returns the non-sponsored products, preserving order. The shop ranks
// and picks among organic results only — paid placements never win.
func organic(prods []client.Product) []client.Product {
	out := make([]client.Product, 0, len(prods))
	for _, p := range prods {
		if !p.Sponsored() {
			out = append(out, p)
		}
	}
	return out
}

func availableProducts(prods []client.Product) []client.Product {
	out := make([]client.Product, 0, len(prods))
	for _, p := range prods {
		if p.Available {
			out = append(out, p)
		}
	}
	return out
}

// brandMatches returns the organic products whose brand normalizes to one of the
// wanted (already-normalized) brands.
func brandMatches(prods []client.Product, wanted []string) []client.Product {
	var out []client.Product
	for _, p := range organic(prods) {
		nb := normBrand(p.Brand)
		for _, w := range wanted {
			if nb == w {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// cheapestOrganic is the baseline pick when no brand preference applies: the
// cheapest available, non-sponsored product. Falls back to the first product if
// every available result is sponsored.
func cheapestOrganic(prods []client.Product) client.Product {
	org := organic(prods)
	if len(org) == 0 {
		return prods[0]
	}
	return cheapestOf(org)
}

// cheapestOf returns the lowest shelf-price product (ties → first, stable). "La
// más barata" is the absolute price; €/unit value ranking is the separate
// --cheapest feature.
func cheapestOf(prods []client.Product) client.Product {
	sorted := append([]client.Product(nil), prods...)
	sort.SliceStable(sorted, func(i, j int) bool {
		ci, ei := priceCents(sorted[i].Price.Amount)
		cj, ej := priceCents(sorted[j].Price.Amount)
		if ei != nil || ej != nil { // unparseable prices sink
			return ei == nil && ej != nil
		}
		return ci < cj
	})
	return sorted[0]
}
