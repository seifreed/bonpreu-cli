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

// preferredFor returns the brand list that applies to a search term: the first
// override whose key matches the term (normalized substring, either direction)
// wins; otherwise the global Preferred list. isOverride reports which it was.
func preferredFor(term string, b config.Brands) (brands []string, isOverride bool) {
	nt := normBrand(term)
	for key, list := range b.Overrides {
		nk := normBrand(key)
		// Both sides must be non-empty: strings.Contains(x, "") is always true, so
		// without the nt!="" guard a term that normalizes to empty (a symbol-only
		// term like "!!!", or a stray bullet line in a -f file) would spuriously
		// match the first override visited — and map order is random, so the pick
		// would be nondeterministic. Such a term has no brand intent; fall through.
		if nk != "" && nt != "" && (strings.Contains(nt, nk) || strings.Contains(nk, nt)) {
			return list, true
		}
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
// cheapest non-sponsored product (the user's "si no hay marca preferida, la más
// barata"). Falls back to the first product if every result is sponsored.
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
