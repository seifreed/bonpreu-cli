package main

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"

	"github.com/seifreed/bonpreu-cli/internal/client"
	"github.com/seifreed/bonpreu-cli/internal/config"
)

// freshDrop are the category-name fragments --fresh removes (frozen/canned), so
// a bare search surfaces the fresh item instead of its frozen/tinned sibling.
var freshDrop = []string{"congel", "conserv"}

func isFresh(p client.Product) bool {
	for _, c := range p.CategoryPath {
		n := strings.ToLower(c)
		for _, d := range freshDrop {
			if strings.Contains(n, d) {
				return false
			}
		}
	}
	return true
}

// cmdSearch: full-text product search. Anonymous, no login.
func cmdSearch(args []string) error {
	fs, cf := newCommonFlags("search")
	limit := fs.Int("limit", 0, "max products to return (0 = server default ~100)")
	fresh := fs.Bool("fresh", false, "drop frozen (Congelats) + canned (Conserves) hits")
	cheapest := fs.Bool("cheapest", false, "rank by unit price (€/kg, €/L, €/u); mark the cheapest per unit")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	term := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if term == "" {
		return invalidCommandf("usage: bonpreu search <term...>")
	}
	cl, _ := newClient(cf)
	pp, err := cl.Search(term, *limit)
	if err != nil {
		return err
	}
	prods := filterFresh(pp.Products(), *fresh)
	if *limit > 0 && len(prods) > *limit {
		prods = prods[:*limit]
	}
	var winners map[string]bool
	if *cheapest {
		prods, winners = rankByUnit(prods)
	}
	if done, err := emitStructured(cf, prods); done {
		return err
	}
	if len(prods) == 0 {
		return writeStdoutln("(sense resultats)")
	}
	return printProductLines(prods, winners, *cheapest)
}

// printProductLines renders one product per line. With cheapest set, each unit
// group's winner (per winners) gets a ★ and a "← millor /kg" tag, and the rest are
// indented to align under it.
func printProductLines(prods []client.Product, winners map[string]bool, cheapest bool) error {
	var output strings.Builder
	for _, p := range prods {
		if cheapest && p.Available && !p.Sponsored() {
			if unit, amount, ok := unitPriceAmount(p); ok && winners[unitWinnerKey(unit, amount)] {
				fmt.Fprintln(&output, "  ★ "+productLine(p)+"  ← millor "+unit)
				continue
			}
		}
		indent := "  "
		if cheapest {
			indent = "    " // align under the ★ winners
		}
		fmt.Fprintln(&output, indent+productLine(p))
	}
	return writeStdout("%s", output.String())
}

// rankByUnit ranks search results by real value: products with a parseable unit
// price are grouped by unit in first-seen order, then sorted cheapest-first within
// each group; non-comparable products keep their original order at the end.
// winners holds the cheapest exact unit-price key in each unit group; ties all win.
// Sponsored and unavailable placements never win — the ★ "millor preu" badge marks
// the cheapest available organic item, mirroring how batch skips unusable hits.
func rankByUnit(prods []client.Product) (sorted []client.Product, winners map[string]bool) {
	org := organic(availableProducts(prods)) // only available, non-sponsored products can win
	minAmounts := map[string]*big.Rat{}
	for _, p := range org {
		if suf, amount, ok := unitPriceAmount(p); ok {
			if m, seen := minAmounts[suf]; !seen || amount.Cmp(m) < 0 {
				minAmounts[suf] = amount
			}
		}
	}
	unitOrder := make(map[string]int)
	for _, p := range prods {
		suf, _, ok := unitPriceAmount(p)
		if !ok {
			continue
		}
		if _, seen := unitOrder[suf]; !seen {
			unitOrder[suf] = len(unitOrder)
		}
	}
	winners = map[string]bool{}
	for _, p := range org {
		if unit, amount, ok := unitPriceAmount(p); ok && amount.Cmp(minAmounts[unit]) == 0 {
			winners[unitWinnerKey(unit, amount)] = true
		}
	}

	sorted = append([]client.Product(nil), prods...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Available != sorted[j].Available {
			return sorted[i].Available
		}
		suffixI, ci, oki := unitPriceAmount(sorted[i])
		suffixJ, cj, okj := unitPriceAmount(sorted[j])
		if oki != okj {
			return oki // comparable products sort ahead of non-comparable
		}
		if !oki {
			return false // both non-comparable: keep original order
		}
		if suffixI != suffixJ {
			return unitOrder[suffixI] < unitOrder[suffixJ]
		}
		return ci.Cmp(cj) < 0
	})
	return sorted, winners
}

func unitPriceAmount(p client.Product) (string, *big.Rat, bool) {
	unit := unitSuffix(p.UnitPrice)
	if unit == "" {
		return "", nil, false
	}
	if _, err := priceCents(p.UnitPrice.Price.Amount); err != nil {
		return "", nil, false
	}
	amount, err := ratFromDecimal(strings.TrimSpace(p.UnitPrice.Price.Amount))
	if err != nil {
		return "", nil, false
	}
	return unit, amount, true
}

func unitWinnerKey(unit string, amount *big.Rat) string {
	return unit + "\x00" + amount.RatString()
}

func filterFresh(in []client.Product, fresh bool) []client.Product {
	if !fresh {
		return in
	}
	out := in[:0:0]
	for _, p := range in {
		if isFresh(p) {
			out = append(out, p)
		}
	}
	return out
}

// cmdBatch: search many terms, print one available hit per term — the preferred
// brand when configured, else the cheapest organic hit. One request each, single command.
// Bridges plain-word lists to ids+prices.
func cmdBatch(args []string) error {
	fs, cf := newCommonFlags("batch")
	file := fs.String("f", "", "file with one search term per line ('-' for stdin); else terms are positional")
	fresh := fs.Bool("fresh", false, "drop frozen + canned before picking the top hit")
	brand := fs.String("brand", "", "prefer these brands (comma-separated), overriding config for this run")
	noBrands := fs.Bool("no-brands", false, "ignore configured brand preferences (pick cheapest/organic)")
	offers := fs.String("offers", "", "actionable offers: alert (default) | ignore | accept (overrides [offers] mode)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	terms, err := collectLines(*file, fs.Args())
	if err != nil {
		return err
	}
	if len(terms) == 0 {
		return invalidCommandf("no terms (use -f file, stdin, or '<term>...' args)")
	}
	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("read batch preferences: %w", err)
	}
	cl := newClientWithConfig(cf, cfg)
	if mode := cfg.Brands.Mode; mode != "" && mode != "cheapest_among" && mode != "strict_priority" {
		return fmt.Errorf("invalid [brands].mode %q (want cheapest_among or strict_priority)", mode)
	}
	override := splitBrands(*brand)
	offersMode, err := resolveOffersMode(*offers, cfg.Offers.Mode)
	if err != nil {
		return err
	}

	type hit struct {
		Term       string          `json:"term"`
		Product    *client.Product `json:"product"`
		BrandMatch string          `json:"brandMatch,omitempty"` // preferred | override | none | off
		Considered []string        `json:"consideredBrands,omitempty"`
		MissPolicy string          `json:"brandMissPolicy,omitempty"` // ask | cheapest, only when an explicit brand is unavailable
		Offer      string          `json:"offer,omitempty"`           // multibuy | price
		OfferLabel string          `json:"offerLabel,omitempty"`      // the promo text (e.g. "2a unitat 50%")
		OfferMode  string          `json:"offerMode,omitempty"`       // resolved alert | ignore | accept policy
		Error      string          `json:"error,omitempty"`
	}
	out := make([]hit, 0, len(terms))
	missing := 0
	var searchErrors []error
	for _, t := range terms {
		pp, serr := cl.Search(t, 0)
		if serr != nil {
			out = append(out, hit{Term: t, Error: serr.Error()})
			searchErrors = append(searchErrors, serr)
			continue
		}
		prods := availableProducts(filterFresh(pp.Products(), *fresh))
		if len(prods) == 0 {
			out = append(out, hit{Term: t})
			missing++
			continue
		}
		p, match, considered := resolveBrandHit(prods, t, cfg.Brands, override, *noBrands)
		missPolicy := ""
		if match == "none" {
			missPolicy = cfg.Brands.OnMissing
			if missPolicy != "cheapest" {
				missPolicy = "ask"
			}
		}
		h := hit{Term: t, Product: &p, BrandMatch: match, Considered: considered, MissPolicy: missPolicy, OfferMode: offersMode}
		if offersMode != "ignore" {
			h.Offer, h.OfferLabel = classifyOffer(p.Promotions)
		}
		out = append(out, h)
	}

	structured, emitErr := emitStructured(cf, out)
	if structured && emitErr != nil {
		return emitErr
	}
	if !structured {
		var output strings.Builder
		w := 0
		for _, h := range out {
			if len(h.Term) > w {
				w = len(h.Term)
			}
		}
		for _, h := range out {
			if h.Product == nil {
				if h.Error != "" {
					fmt.Fprintf(&output, "• %-*s → ERROR: %s\n", w, terminalText(h.Term), terminalText(h.Error))
					continue
				}
				fmt.Fprintf(&output, "• %-*s → (sense resultats)\n", w, terminalText(h.Term))
				continue
			}
			line := productLine(*h.Product)
			if h.BrandMatch == "none" {
				line += "  ⚠ marca preferida no disponible"
			}
			if h.Offer == "multibuy" {
				line += "  💡 oferta: aprofitable per quantitat"
			}
			fmt.Fprintf(&output, "• %-*s → %s\n", w, terminalText(h.Term), line)
		}
		if output.Len() > 0 {
			if err := writeStdout("%s", output.String()); err != nil {
				return err
			}
		}
	}
	if len(searchErrors) > 0 {
		return fmt.Errorf("%d of %d term searches failed: %w", len(searchErrors), len(terms), errors.Join(searchErrors...))
	}
	if missing > 0 {
		return fmt.Errorf("%d of %d terms returned no eligible product", missing, len(terms))
	}
	return nil
}

// splitBrands parses a comma-separated --brand value into a trimmed, non-empty list.
func splitBrands(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// resolveBrandHit picks the product for a term honouring brand preferences:
// --brand override wins, else the configured brands (global or per-term override);
// with no preference (or --no-brands) it falls back to the cheapest available organic hit
// ("si no hay marca preferida, la más barata"). The returned tag
// (preferred|override|none|off) lets callers/the skill know whether to ask before
// substituting when a preferred brand wasn't stocked.
func resolveBrandHit(prods []client.Product, term string, brands config.Brands, override []string, disabled bool) (client.Product, string, []string) {
	if disabled {
		return cheapestOrganic(prods), "off", nil
	}

	// An explicit request — a --brand flag or a per-term config override — is a
	// deliberate "I want THIS brand": match → "override", miss → "none" so the
	// skill asks before substituting. A global favourites list is aspirational
	// across categories (Hellmann's doesn't make milk), so when none of those
	// brands applies to this product, fall back quietly ("off").
	explicit := override
	if len(explicit) == 0 {
		if list, isOv := preferredFor(term, brands); isOv {
			explicit = list
		}
	}
	if len(explicit) > 0 {
		if chosen, ok := pickByBrand(prods, explicit, brands.Mode); ok {
			return chosen, "override", explicit
		}
		return cheapestOrganic(prods), "none", explicit
	}

	if len(brands.Preferred) == 0 {
		return cheapestOrganic(prods), "off", nil
	}
	if chosen, ok := pickByBrand(prods, brands.Preferred, brands.Mode); ok {
		return chosen, "preferred", brands.Preferred
	}
	return cheapestOrganic(prods), "off", nil
}

// cmdTotal: deterministic basket total from '<id> [qty]' lines — the CLI fetches
// each product's current price and sums price×qty in integer cents, exact and
// reproducible. Pre-cart estimate; the cart/checkout API stays authoritative.
func cmdTotal(args []string) error {
	fs, cf := newCommonFlags("total")
	file := fs.String("f", "", "file with one '<id> [qty]' per line ('-' for stdin); else ids are positional (qty 1)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	lines, err := collectBasket(*file, fs.Args())
	if err != nil {
		return err
	}
	if len(lines) == 0 {
		return invalidCommandf("no basket lines (use -f file, stdin, or '<id> [<id>...]' args)")
	}
	cl, _ := newClient(cf)

	type lineResult struct {
		ID       string  `json:"id"`
		Name     string  `json:"name,omitempty"`
		Qty      float64 `json:"qty"`
		Price    string  `json:"price,omitempty"`
		Subtotal string  `json:"subtotal,omitempty"`
		Error    string  `json:"error,omitempty"`
	}
	results := make([]lineResult, 0, len(lines))
	var totalCents int64
	failed := 0
	for _, bl := range lines {
		pd, perr := cl.Product(bl.id)
		if perr != nil {
			results = append(results, lineResult{ID: bl.id, Qty: bl.qty, Error: perr.Error()})
			failed++
			continue
		}
		sub, serr := lineAmountCents(pd.Price.Amount, bl.qty)
		if serr != nil || sub > math.MaxInt64-totalCents {
			if serr == nil {
				serr = fmt.Errorf("basket total exceeds the supported range")
			}
			results = append(results, lineResult{ID: bl.id, Name: pd.Name, Qty: bl.qty, Error: serr.Error()})
			failed++
			continue
		}
		totalCents += sub
		results = append(results, lineResult{
			ID: bl.id, Name: pd.Name, Qty: bl.qty,
			Price: pd.Price.Amount, Subtotal: centsStr(sub),
		})
	}

	if done, err := emitStructured(cf, map[string]any{
		"lines":    results,
		"total":    centsStr(totalCents),
		"count":    len(lines),
		"complete": failed == 0,
	}); done {
		if err != nil {
			return err
		}
		if failed > 0 {
			return fmt.Errorf("%d of %d lines could not be priced (excluded from the total) — check the ids exist", failed, len(lines))
		}
		return nil
	}
	var output strings.Builder
	for _, r := range results {
		if r.Error != "" {
			fmt.Fprintf(&output, "  [%s] %s  ERROR: %s\n", terminalText(r.ID), terminalText(firstNonEmpty(r.Name, "?")), terminalText(r.Error))
			continue
		}
		fmt.Fprintf(&output, "  [%s] %s — %s × %s€ = %s€\n", terminalText(r.ID), terminalText(r.Name), fmtQty(r.Qty), terminalText(r.Price), terminalText(r.Subtotal))
	}
	fmt.Fprintf(&output, "  total: %s€  (%s)\n", centsStr(totalCents), plural(len(lines), "línia", "línies"))
	if err := writeStdout("%s", output.String()); err != nil {
		return err
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d lines could not be priced (excluded from the total) — check the ids exist", failed, len(lines))
	}
	return nil
}

// cmdProduct: product detail (price, brand, format, ingredients/nutrition text
// when the item carries it).
func cmdProduct(args []string) error {
	fs, cf := newCommonFlags("product")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return invalidCommandf("usage: bonpreu product <id>")
	}
	cl, _ := newClient(cf)
	pd, err := cl.Product(fs.Arg(0))
	if err != nil {
		return err
	}
	if done, err := emitStructured(cf, pd); done {
		return err
	}
	var output strings.Builder
	fmt.Fprintf(&output, "[%s] %s\n", terminalText(pd.RetailerProductID), terminalText(pd.Name))
	if pd.Brand != "" {
		fmt.Fprintf(&output, "  marca: %s\n", terminalText(pd.Brand))
	}
	fmt.Fprintf(&output, "  preu: %s", money(pd.Price))
	if pd.UnitPrice.Price.Amount != "" {
		fmt.Fprintf(&output, "  (%s%s)", money(pd.UnitPrice.Price), unitSuffix(pd.UnitPrice))
	}
	fmt.Fprintln(&output)
	if pd.PackSizeDescription != "" {
		fmt.Fprintf(&output, "  format: %s\n", terminalText(pd.PackSizeDescription))
	}
	for _, pr := range pd.Promotions {
		if pr.Description != "" {
			fmt.Fprintf(&output, "  oferta: %s\n", terminalText(pr.Description))
		}
	}
	fmt.Fprintf(&output, "  url: %s/products/%s\n", terminalText(cl.BaseURL), terminalText(pd.RetailerProductID))
	for _, d := range pd.Details {
		fmt.Fprintf(&output, "  %s:\n", terminalText(d.Title))
		for _, ln := range strings.Split(d.Content, "\n") {
			if s := strings.TrimSpace(ln); s != "" {
				fmt.Fprintf(&output, "    %s\n", terminalText(s))
			}
		}
	}
	return writeStdout("%s", output.String())
}

// cmdCategories: the category tree, or one category's products with --id.
func cmdCategories(args []string) error {
	fs, cf := newCommonFlags("categories")
	id := fs.String("id", "", "category id — short code (0301) or UUID → list that category's products")
	depth := fs.Int("depth", 1, "tree depth when listing the tree (1 = top level)")
	limit := fs.Int("limit", 0, "max products when using --id")
	cheapest := fs.Bool("cheapest", false, "with --id: rank by unit price (€/kg, €/L, €/u); mark the cheapest per unit")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return invalidCommandf("usage: bonpreu categories [--id <id>] [--depth N] [--limit N] [--cheapest]")
	}
	cl, _ := newClient(cf)

	if *id != "" {
		pp, err := cl.CategoryProducts(*id, *limit)
		if err != nil {
			if status, _ := client.HTTPStatus(err); status == 404 {
				return fmt.Errorf("category %s: not found (see `bonpreu categories` for valid ids)", *id)
			}
			return err
		}
		prods := pp.Products()
		var winners map[string]bool
		if *cheapest {
			prods, winners = rankByUnit(prods)
		}
		if done, err := emitStructured(cf, prods); done {
			return err
		}
		return printProductLines(prods, winners, *cheapest)
	}

	cats, err := cl.Categories(*depth)
	if err != nil {
		return err
	}
	if done, err := emitStructured(cf, cats); done {
		return err
	}
	return printCats(cats, 0)
}

func printCats(cats []client.Category, indent int) error {
	var output strings.Builder
	printCatsTo(&output, cats, indent)
	return writeStdout("%s", output.String())
}

func printCatsTo(output *strings.Builder, cats []client.Category, indent int) {
	pad := strings.Repeat("  ", indent)
	for _, c := range cats {
		fmt.Fprintf(output, "%s%s  %s\n", pad, terminalText(c.RetailerCategoryID), terminalText(c.Name))
		printCatsTo(output, c.ChildCategories, indent+1)
	}
}
