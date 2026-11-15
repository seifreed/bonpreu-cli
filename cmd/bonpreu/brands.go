package main

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/seifreed/bonpreu-cli/internal/branddata"
	"github.com/seifreed/bonpreu-cli/internal/client"
)

// brandCount is one brand and how many products carry it across the crawl.
type brandCount struct {
	Brand string `json:"brand"`
	Count int    `json:"count"`
}

// cmdBrands lists Bonpreu's product brands (distinct names + product counts), the
// menu for picking favourites for the `[brands]` config. By default it serves the
// snapshot shipped with the binary (internal/branddata, instant, no network).
// `--update` re-crawls the live catalogue and prints the fresh JSON to stdout —
// redirect it back into internal/branddata/brands.json and rebuild to refresh the
// shipped copy. Output: table (default), `--json`/`--toon`, or `--csv`.
func cmdBrands(args []string) error {
	fs, cf := newCommonFlags("brands")
	update := fs.Bool("update", false, "re-crawl the live catalogue and print fresh JSON (else use the shipped snapshot)")
	depth := fs.Int("depth", 2, "with --update: category-tree depth to crawl")
	limit := fs.Int("limit", 300, "with --update: max products sampled per category")
	csv := fs.Bool("csv", false, "emit CSV (brand,count) instead of a table")
	parseFlags(fs, args)

	var ranked []brandCount
	if *update {
		cl, _ := newClient(cf)
		r, err := crawlBrands(cl, *depth, *limit)
		if err != nil {
			return err
		}
		ranked = r
		stderrInfo("catalog.brand_crawl.completed", "brand crawl completed", "operation", cf.operation, "brand_count", len(ranked))
	} else {
		if err := json.Unmarshal(branddata.JSON, &ranked); err != nil {
			return fmt.Errorf("read shipped brand snapshot: %w", err)
		}
	}

	if *csv {
		return writeBrandsCSV(os.Stdout, ranked)
	}
	if done, err := emitStructured(cf, ranked); done {
		return err
	}
	for _, b := range ranked {
		fmt.Printf("%5d  %s\n", b.Count, terminalText(b.Brand))
	}
	return nil
}

// writeBrandsCSV emits the tally as RFC 4180 CSV. Uses encoding/csv, not fmt %q:
// %q is Go string-literal quoting (escapes an embedded quote as \", renders
// newlines/tabs as literal \n\t), which a real CSV reader (Excel, encoding/csv)
// mis-parses. csv.Writer doubles quotes and wraps fields that contain a quote,
// comma, or newline — so a brand like `Say "Cheese", Inc.` survives a round-trip.
func writeBrandsCSV(out io.Writer, ranked []brandCount) error {
	w := csv.NewWriter(out)
	records := make([][]string, 0, len(ranked)+1)
	records = append(records, []string{"brand", "count"})
	for _, b := range ranked {
		records = append(records, []string{spreadsheetSafeBrand(b.Brand), strconv.Itoa(b.Count)})
	}
	return w.WriteAll(records)
}

// spreadsheetSafeBrand keeps catalog-controlled values from becoming spreadsheet formulas.
func spreadsheetSafeBrand(brand string) string {
	trimmed := strings.TrimLeftFunc(brand, unicode.IsSpace)
	r, _ := utf8.DecodeRuneInString(trimmed)
	if strings.HasPrefix(brand, "\t") || strings.HasPrefix(brand, "\r") || strings.HasPrefix(brand, "\n") || strings.ContainsRune("=+-@＝＋－＠", r) {
		return "\t" + brand
	}
	return brand
}

// crawlBrands walks the category tree to depth and aggregates distinct product
// brands. Anonymous reads (uTLS clears the WAF); slow and sequential — progress
// goes to stderr.
func crawlBrands(cl *client.Client, depth, limit int) ([]brandCount, error) {
	cats, err := cl.Categories(depth)
	if err != nil {
		return nil, err
	}
	var ids []string
	collectCategoryIDs(cats, &ids, map[string]bool{})
	if len(ids) == 0 {
		return nil, fmt.Errorf("no categories found (try a higher --depth)")
	}
	counts := map[string]int{}
	var categoryErrors []error
	for i, id := range ids {
		pp, perr := cl.CategoryProducts(id, limit)
		if perr != nil {
			stderrWarn("catalog.category.skipped", "category crawl failed; skipping it",
				"operation", "brands", "category_id", id, "error_type", fmt.Sprintf("%T", perr))
			categoryErrors = append(categoryErrors, fmt.Errorf("category %s: %w", id, perr))
			continue
		}
		tallyBrands(counts, pp.Products())
		stderrDebug("catalog.brand_crawl.progress", "brand crawl progress",
			"operation", "brands", "category_index", i+1, "category_total", len(ids), "brand_count", len(counts))
	}
	if len(categoryErrors) > 0 {
		return nil, errors.Join(categoryErrors...)
	}
	return sortedBrands(counts), nil
}

// collectCategoryIDs gathers every node's retailerCategoryId from the tree (deduped).
func collectCategoryIDs(cats []client.Category, out *[]string, seen map[string]bool) {
	for _, c := range cats {
		if c.RetailerCategoryID != "" && !seen[c.RetailerCategoryID] {
			seen[c.RetailerCategoryID] = true
			*out = append(*out, c.RetailerCategoryID)
		}
		collectCategoryIDs(c.ChildCategories, out, seen)
	}
}

// tallyBrands adds each product's (non-empty) brand to counts.
func tallyBrands(counts map[string]int, prods []client.Product) {
	for _, p := range prods {
		if p.Brand != "" {
			counts[p.Brand]++
		}
	}
}

// sortedBrands returns the tally ordered by count desc, then name for stable ties.
func sortedBrands(counts map[string]int) []brandCount {
	out := make([]brandCount, 0, len(counts))
	for b, n := range counts {
		out = append(out, brandCount{Brand: b, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Brand < out[j].Brand
	})
	return out
}
