package main

import (
	"flag"
	"fmt"
	"math"
	"strings"

	"github.com/seifreed/bonpreu-cli/internal/client"
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
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	cf := addCommon(fs)
	limit := fs.Int("limit", 0, "max products to return (0 = server default ~100)")
	fresh := fs.Bool("fresh", false, "drop frozen (Congelats) + canned (Conserves) hits")
	_ = fs.Parse(reorderArgs(fs, args))

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
	if cf.jsonOut {
		return emitJSON(prods)
	}
	if len(prods) == 0 {
		fmt.Println("(sense resultats)")
		return nil
	}
	for _, p := range prods {
		fmt.Println("  " + productLine(p))
	}
	return nil
}

// topHit picks the first organic result, skipping sponsored/featured placements
// so a term like "llet" returns actual milk, not a paid coffee-capsule ad.
func topHit(prods []client.Product) client.Product {
	for _, p := range prods {
		if !p.Sponsored() {
			return p
		}
	}
	return prods[0]
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

// cmdBatch: search many terms, print the top hit per term — one request each, but
// a single command. Bridges plain-word lists to ids+prices.
func cmdBatch(args []string) error {
	fs := flag.NewFlagSet("batch", flag.ExitOnError)
	cf := addCommon(fs)
	file := fs.String("f", "", "file with one search term per line ('-' for stdin); else terms are positional")
	fresh := fs.Bool("fresh", false, "drop frozen + canned before picking the top hit")
	_ = fs.Parse(reorderArgs(fs, args))

	terms, err := collectLines(*file, fs.Args())
	if err != nil {
		return err
	}
	if len(terms) == 0 {
		return fmt.Errorf("no terms (use -f file, stdin, or '<term>...' args)")
	}
	cl := newClient(cf)

	type hit struct {
		Term    string          `json:"term"`
		Product *client.Product `json:"product"`
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
		p := topHit(prods)
		out = append(out, hit{Term: t, Product: &p})
	}

	if cf.jsonOut {
		return emitJSON(out)
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
		fmt.Printf("• %-*s → %s\n", w, h.Term, productLine(*h.Product))
	}
	if missing > 0 {
		return fmt.Errorf("%d of %d terms returned no product", missing, len(terms))
	}
	return nil
}

// cmdTotal: deterministic basket total from '<id> [qty]' lines — the CLI fetches
// each product's current price and sums price×qty in integer cents, exact and
// reproducible. Pre-cart estimate; the cart/checkout API stays authoritative.
func cmdTotal(args []string) error {
	fs := flag.NewFlagSet("total", flag.ExitOnError)
	cf := addCommon(fs)
	file := fs.String("f", "", "file with one '<id> [qty]' per line ('-' for stdin); else ids are positional (qty 1)")
	_ = fs.Parse(reorderArgs(fs, args))

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
		sub := int64(math.Round(float64(cents) * bl.qty))
		totalCents += sub
		results = append(results, lineResult{
			ID: bl.id, Name: pd.Name, Qty: bl.qty,
			Price: pd.Price.Amount, Subtotal: centsStr(sub),
		})
	}

	if cf.jsonOut {
		return emitJSON(map[string]any{
			"lines":    results,
			"total":    centsStr(totalCents),
			"count":    len(lines),
			"complete": failed == 0,
		})
	}
	for _, r := range results {
		if r.Error != "" {
			fmt.Printf("  [%s] %s  ERROR: %s\n", r.ID, orDefault(r.Name, "?"), r.Error)
			continue
		}
		fmt.Printf("  [%s] %s — %s × %s€ = %s€\n", r.ID, r.Name, fmtQty(r.Qty), r.Price, r.Subtotal)
	}
	fmt.Printf("  total: %s€  (%d línies)\n", centsStr(totalCents), len(lines))
	if failed > 0 {
		return fmt.Errorf("%d of %d lines could not be priced (excluded from the total) — check the ids exist", failed, len(lines))
	}
	return nil
}

// cmdProduct: product detail (price, brand, format, ingredients/nutrition text
// when the item carries it).
func cmdProduct(args []string) error {
	fs := flag.NewFlagSet("product", flag.ExitOnError)
	cf := addCommon(fs)
	_ = fs.Parse(reorderArgs(fs, args))
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: bonpreu product <id>")
	}
	cl := newClient(cf)
	pd, err := cl.Product(fs.Arg(0))
	if err != nil {
		return err
	}
	if cf.jsonOut {
		return emitJSON(pd)
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
	fs := flag.NewFlagSet("categories", flag.ExitOnError)
	cf := addCommon(fs)
	id := fs.String("id", "", "retailer category id (e.g. 0301) → list that category's products")
	depth := fs.Int("depth", 1, "tree depth when listing the tree (1 = top level)")
	limit := fs.Int("limit", 0, "max products when using --id")
	_ = fs.Parse(reorderArgs(fs, args))
	cl := newClient(cf)

	if *id != "" {
		pp, err := cl.CategoryProducts(*id, *limit)
		if err != nil {
			return err
		}
		if cf.jsonOut {
			return emitJSON(pp.Products())
		}
		for _, p := range pp.Products() {
			fmt.Println("  " + productLine(p))
		}
		return nil
	}

	cats, err := cl.Categories(*depth)
	if err != nil {
		return err
	}
	if cf.jsonOut {
		return emitJSON(cats)
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
