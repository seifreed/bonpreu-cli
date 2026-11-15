package main

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/seifreed/bonpreu-cli/internal/client"
)

// money formats the API's "8.55"/"EUR" pair as "8.55€".
func money(m client.Money) string {
	if m.Amount == "" {
		return "?"
	}
	return terminalText(m.Amount) + "€"
}

// terminalText makes control characters in remote text visible instead of letting
// them alter the terminal display.
func terminalText(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsPrint(r) {
			b.WriteRune(r)
			continue
		}
		quoted := strconv.QuoteRune(r)
		b.WriteString(quoted[1 : len(quoted)-1])
	}
	return b.String()
}

// unitSuffix turns the API unit ("fop.price.per.kg", "PER_1KG", "EACH") into a
// short label: /kg, /L, /u.
func unitSuffix(u client.UnitPrice) string {
	s := strings.ToLower(u.Unit + " " + u.UnitName)
	switch {
	case strings.Contains(s, "kg") || strings.Contains(s, "1kg"):
		return "/kg"
	case strings.Contains(s, "per.l") || strings.Contains(s, "1l") || strings.Contains(s, "litre"):
		return "/L"
	case strings.Contains(s, "each"):
		return "/u"
	default:
		return ""
	}
}

// productLine renders a one-line summary: "[49637] Name — 8.55€ (0.28€/u)  ⟨offer⟩".
func productLine(p client.Product) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %s — %s", terminalText(p.RetailerProductID), terminalText(strings.TrimSpace(p.Name)), money(p.Price))
	if p.UnitPrice.Price.Amount != "" {
		fmt.Fprintf(&b, " (%s%s)", money(p.UnitPrice.Price), unitSuffix(p.UnitPrice))
	}
	if p.Sponsored() {
		b.WriteString("  (patrocinat)")
	}
	if !p.Available {
		b.WriteString("  [no disponible]")
	}
	for _, pr := range p.Promotions {
		if pr.Description != "" {
			fmt.Fprintf(&b, "  ⟨%s⟩", terminalText(pr.Description))
		}
	}
	return b.String()
}
