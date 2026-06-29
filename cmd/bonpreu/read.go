package main

import (
	"fmt"
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
	parseFlags(fs, args)

	term := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if term == "" {
		return fmt.Errorf("usage: bonpreu search <term...>")
	}
	cl := newClient(cf)
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
		fmt.Println("(sense resultats)")
		return nil
	}
	printProductLines(prods, winners, *cheapest)
	return nil
}

// printProductLines renders one product per line. With cheapest set, each unit
// group's winner (per winners) gets a ★ and a "← millor /kg" tag, and the rest are
// indented to align under it.
func printProductLines(prods []client.Product, winners map[string]bool, cheapest bool) {
	for _, p := range prods {
		if winners[p.RetailerProductID] {
			fmt.Println("  ★ " + productLine(p) + "  ← millor " + unitSuffix(p.UnitPrice))
			continue
		}
		indent := "  "
		if cheapest {
			indent = "    " // align under the ★ winners
		}
		fmt.Println(indent + productLine(p))
	}
}

// rankByUnit ranks search results by real value: products with a comparable unit
// price (parseable amount + a known €/kg | €/L | €/u suffix) are stable-sorted
// cheapest-first ahead of the rest, which keep their original order. winners holds
// each unit group's cheapest (by RetailerProductID; ties all win), for marking.
// Sponsored placements never win — the ★ "millor preu" badge marks the cheapest
// organic item (see organic()), mirroring how batch skips paid results.
func rankByUnit(prods []client.Product) (sorted []client.Product, winners map[string]bool) {
	cents := func(p client.Product) (int64, bool) {
		if unitSuffix(p.UnitPrice) == "" {
			return 0, false
		}
		c, err := priceCents(p.UnitPrice.Price.Amount)
		return c, err == nil
	}

	org := organic(prods)          // rank/mark among non-sponsored results only
	minCents := map[string]int64{} // cheapest cents seen per unit suffix (not the builtin min)
	for _, p := range org {
		if c, ok := cents(p); ok {
			suf := unitSuffix(p.UnitPrice)
			if m, seen := minCents[suf]; !seen || c < m {
				minCents[suf] = c
			}
		}
	}
	winners = map[string]bool{}
	for _, p := range org {
		if c, ok := cents(p); ok && c == minCents[unitSuffix(p.UnitPrice)] {
			winners[p.RetailerProductID] = true
		}
	}

	sorted = append([]client.Product(nil), prods...)
	sort.SliceStable(sorted, func(i, j int) bool {
		ci, oki := cents(sorted[i])
		cj, okj := cents(sorted[j])
		if oki != okj {
			return oki // comparable products sort ahead of non-comparable
		}
		if !oki {
			return false // both non-comparable: keep original order
		}
		return ci < cj
	})
	return sorted, winners
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

// cmdBatch: search many terms, print one hit per term — the preferred brand when
// configured, else the cheapest organic hit. One request each, single command.
// Bridges plain-word lists to ids+prices.
func cmdBatch(args []string) error {
	fs, cf := newCommonFlags("batch")
	file := fs.String("f", "", "file with one search term per line ('-' for stdin); else terms are positional")
	fresh := fs.Bool("fresh", false, "drop frozen + canned before picking the top hit")
	brand := fs.String("brand", "", "prefer these brands (comma-separated), overriding config for this run")
	noBrands := fs.Bool("no-brands", false, "ignore configured brand preferences (pick cheapest/organic)")
	offers := fs.String("offers", "", "actionable offers: alert (default) | ignore | accept (overrides [offers] mode)")
	parseFlags(fs, args)

	terms, err := collectLines(*file, fs.Args())
	if err != nil {
		return err
	}
	if len(terms) == 0 {
		return fmt.Errorf("no terms (use -f file, stdin, or '<term>...' args)")
	}
	cl := newClient(cf)
	cfg, _ := config.LoadConfig()
	override := splitBrands(*brand)
	offersMode := resolveOffersMode(*offers, cfg.Offers.Mode)

	type hit struct {
		Term       string          `json:"term"`
		Product    *client.Product `json:"product"`
		BrandMatch string          `json:"brandMatch,omitempty"` // preferred | override | none | off
		Considered []string        `json:"consideredBrands,omitempty"`
		Offer      string          `json:"offer,omitempty"`      // multibuy | price
		OfferLabel string          `json:"offerLabel,omitempty"` // the promo text (e.g. "2a unitat 50%")
	}
	out := make([]hit, 0, len(terms))
	missing := 0
	for _, t := range terms {
		pp, serr := cl.Search(t, 10)
		if serr != nil {
			out = append(out, hit{Term: t})
			missing++
			continue
		}
		prods := filterFresh(pp.Products(), *fresh)
		if len(prods) == 0 {
			out = append(out, hit{Term: t})
			missing++
			continue
		}
		p, match, considered := resolveBrandHit(prods, t, cfg.Brands, override, *noBrands)
		h := hit{Term: t, Product: &p, BrandMatch: match, Considered: considered}
		if offersMode != "ignore" {
			h.Offer, h.OfferLabel = classifyOffer(p.Promotions)
		}
		out = append(out, h)
	}

	if done, err := emitStructured(cf, out); done {
		return err
	}
	w := 0
	for _, h := range out {
		if len(h.Term) > w {
			w = len(h.Term)
		}
	}
	for _, h := range out {
		if h.Product == nil {
			fmt.Printf("• %-*s → (sense resultats)\n", w, h.Term)
			continue
		}
		line := productLine(*h.Product)
		if h.BrandMatch == "none" {
			line += "  ⚠ marca preferida no disponible"
		}
		if h.Offer == "multibuy" {
			line += "  💡 oferta: aprofitable per quantitat"
		}
		fmt.Printf("• %-*s → %s\n", w, h.Term, line)
	}
	if missing > 0 {
		return fmt.Errorf("%d of %d terms returned no product", missing, len(terms))
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
// with no preference (or --no-brands) it falls back to the cheapest organic hit
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
	parseFlags(fs, args)

	lines, err := collectBasket(*file, fs.Args())
	if err != nil {
		return err
	}
	if len(lines) == 0 {
		return fmt.Errorf("no basket lines (use -f file, stdin, or '<id> [<id>...]' args)")
	}
	cl := newClient(cf)

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
		cents, cerr := priceCents(pd.Price.Amount)
		if cerr != nil {
			results = append(results, lineResult{ID: bl.id, Name: pd.Name, Qty: bl.qty, Error: cerr.Error()})
			failed++
			continue
		}
		sub := lineCents(cents, bl.qty)
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
		return err
	}
	for _, r := range results {
		if r.Error != "" {
			fmt.Printf("  [%s] %s  ERROR: %s\n", r.ID, firstNonEmpty(r.Name, "?"), r.Error)
			continue
		}
		fmt.Printf("  [%s] %s — %s × %s€ = %s€\n", r.ID, r.Name, fmtQty(r.Qty), r.Price, r.Subtotal)
	}
	fmt.Printf("  total: %s€  (%s)\n", centsStr(totalCents), plural(len(lines), "línia", "línies"))
	if failed > 0 {
		return fmt.Errorf("%d of %d lines could not be priced (excluded from the total) — check the ids exist", failed, len(lines))
	}
	return nil
}

// cmdProduct: product detail (price, brand, format, ingredients/nutrition text
// when the item carries it).
func cmdProduct(args []string) error {
	fs, cf := newCommonFlags("product")
	parseFlags(fs, args)
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: bonpreu product <id>")
	}
	cl := newClient(cf)
	pd, err := cl.Product(fs.Arg(0))
	if err != nil {
		return err
	}
	if done, err := emitStructured(cf, pd); done {
		return err
	}
	fmt.Printf("[%s] %s\n", pd.RetailerProductID, pd.Name)
	if pd.Brand != "" {
		fmt.Printf("  marca: %s\n", pd.Brand)
	}
	fmt.Printf("  preu: %s", money(pd.Price))
	if pd.UnitPrice.Price.Amount != "" {
		fmt.Printf("  (%s%s)", money(pd.UnitPrice.Price), unitSuffix(pd.UnitPrice))
	}
	fmt.Println()
	if pd.PackSizeDescription != "" {
		fmt.Printf("  format: %s\n", pd.PackSizeDescription)
	}
	for _, pr := range pd.Promotions {
		if pr.Description != "" {
			fmt.Printf("  oferta: %s\n", pr.Description)
		}
	}
	fmt.Printf("  url: %s/products/%s\n", cl.BaseURL, pd.RetailerProductID)
	for _, d := range pd.Details {
		fmt.Printf("  %s:\n", d.Title)
		for _, ln := range strings.Split(d.Content, "\n") {
			if s := strings.TrimSpace(ln); s != "" {
				fmt.Printf("    %s\n", s)
			}
		}
	}
	return nil
}

// cmdCategories: the category tree, or one category's products with --id.
func cmdCategories(args []string) error {
	fs, cf := newCommonFlags("categories")
	id := fs.String("id", "", "category id — short code (0301) or UUID → list that category's products")
	depth := fs.Int("depth", 1, "tree depth when listing the tree (1 = top level)")
	limit := fs.Int("limit", 0, "max products when using --id")
	cheapest := fs.Bool("cheapest", false, "with --id: rank by unit price (€/kg, €/L, €/u); mark the cheapest per unit")
	parseFlags(fs, args)
	cl := newClient(cf)

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
		printProductLines(prods, winners, *cheapest)
		return nil
	}

	cats, err := cl.Categories(*depth)
	if err != nil {
		return err
	}
	if done, err := emitStructured(cf, cats); done {
		return err
	}
	printCats(cats, 0)
	return nil
}

func printCats(cats []client.Category, indent int) {
	pad := strings.Repeat("  ", indent)
	for _, c := range cats {
		fmt.Printf("%s%s  %s\n", pad, c.RetailerCategoryID, c.Name)
		printCats(c.ChildCategories, indent+1)
	}
}
