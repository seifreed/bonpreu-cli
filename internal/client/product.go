package client

import (
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
)

// Detail is a titled information block from the product page (ingredients,
// nutrition text, allergens, cooking guidelines, storage…). The site renders
// these from a details array; content may carry light HTML.
type Detail struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

// ProductDetail is a single product's full view: the catalog Product plus the
// titled info blocks (when the item carries them — a minority do).
type ProductDetail struct {
	Product
	Details []Detail `json:"details,omitempty"`
}

// Product fetches one product by its retailer id. The detail API is GraphQL
// behind the WAF JS-challenge, so this scrapes the server-rendered product page
// and pulls the embedded product node (and any info blocks) out of it — which
// works anonymously, the same uTLS path as the rest of the read core.
func (c *Client) Product(retailerProductID string) (*ProductDetail, error) {
	// PathEscape the user-supplied id: it's the user's own request to the same host
	// (Go rejects CRLF, so no header smuggling), but escaping keeps a stray /, ?, or
	// # from reshaping the path/query — consistent with the escaped query params elsewhere.
	html, err := c.GetText(c.BaseURL + "/products/" + url.PathEscape(retailerProductID))
	if err != nil {
		if status, _ := HTTPStatus(err); status == 404 {
			return nil, fmt.Errorf("product %s: not found (no such id)", retailerProductID)
		}
		return nil, err
	}
	blobs, stateErr := embeddedStatesWithinBudget(html, 2*maxBodyBytes)
	node := findProductNode(blobs, retailerProductID)
	if node == nil {
		if stateErr != nil {
			return nil, fmt.Errorf("product %s: parse embedded state: %w", retailerProductID, stateErr)
		}
		return nil, fmt.Errorf("product %s: not found in page (id may be invalid or out of catalog)", retailerProductID)
	}
	product, err := productFromNode(node, retailerProductID)
	if err != nil {
		return nil, err
	}
	pd := &ProductDetail{Product: product}
	pd.Details = collectDetails(blobs, retailerProductID)
	if stateErr != nil && c.Logger != nil {
		c.Logger.Warn("product page contained malformed embedded data",
			"component", "client", "event_name", "product.state_parse_failed",
			"operation", "product", "product_id", retailerProductID, "error", stateErr)
	}
	return pd, nil
}

func productFromNode(node map[string]any, retailerProductID string) (Product, error) {
	b, err := json.Marshal(node)
	if err != nil {
		return Product{}, fmt.Errorf("encode product %s: %w", retailerProductID, err)
	}
	var product Product
	if err := json.Unmarshal(b, &product); err != nil {
		return Product{}, fmt.Errorf("decode product %s: %w", retailerProductID, err)
	}
	return product, nil
}

var stateVarRE = regexp.MustCompile(`window\.(__URQL_DATA__|__INITIAL_STATE__|__QUERY_INITIAL_STATE__)\s*=\s*`)

// findProductNode walks every embedded state blob (and the double-encoded `data`
// strings inside URQL) for the richest object whose retailerProductId matches.
func findProductNode(blobs []any, id string) map[string]any {
	var best map[string]any
	bestValid := false
	bestLen := -1
	bestRepresentation := ""
	consider := func(m map[string]any) {
		if v, _ := m["retailerProductId"].(string); v == id {
			if _, ok := m["name"]; ok {
				_, validErr := productFromNode(m, id)
				valid := validErr == nil
				// Map traversal is unordered, so use stable text to break equal-size ties.
				representation := fmt.Sprint(m)
				if n := len(representation); valid && !bestValid || valid == bestValid && (n > bestLen || n == bestLen && representation < bestRepresentation) {
					best, bestValid, bestLen, bestRepresentation = m, valid, n, representation
				}
			}
		}
	}
	for _, blob := range blobs {
		walkMaps(blob, consider)
	}
	return best
}

// collectDetails gathers {title, content} blocks from state containing the selected
// product, skipping nested data for other products. These hold ingredients / nutrition / allergens.
func collectDetails(blobs []any, productID string) []Detail {
	var out []Detail
	seen := map[string]bool{}
	appendDetail := func(m map[string]any) {
		t, okT := m["title"].(string)
		ct, okC := m["content"].(string)
		if t == "brand" { // already shown as the product's marca
			return
		}
		if okT && okC && t != "" && !seen[t] {
			if content := stripHTML(ct); content != "" { // skip blocks that strip to nothing
				seen[t] = true
				out = append(out, Detail{Title: t, Content: content})
			}
		}
	}
	for _, blob := range blobs {
		containsProduct := false
		walkMaps(blob, func(m map[string]any) {
			if id, _ := m["retailerProductId"].(string); id == productID {
				containsProduct = true
			}
		})
		if containsProduct {
			walkProductTree(blob, productID, appendDetail)
		}
	}
	return out
}

func walkProductTree(v any, productID string, fn func(map[string]any)) {
	switch x := v.(type) {
	case map[string]any:
		if id, ok := x["retailerProductId"].(string); ok && id != productID {
			return
		}
		fn(x)
		for _, child := range x {
			walkProductTree(child, productID, fn)
		}
	case []any:
		for _, child := range x {
			walkProductTree(child, productID, fn)
		}
	}
}

// embeddedStatesWithinBudget returns valid state blobs and the first parse failure.
func embeddedStatesWithinBudget(html string, scanBudget int) ([]any, error) {
	var blobs []any
	var parseErr error
	for _, loc := range stateVarRE.FindAllStringIndex(html, -1) {
		if scanBudget <= 0 {
			if parseErr == nil {
				parseErr = fmt.Errorf("embedded state scan budget exceeded")
			}
			break
		}
		remainingBudget := scanBudget
		obj, ok, scanned := braceObjectWithinBudget(html, loc[1], scanBudget)
		scanBudget -= scanned
		if !ok {
			if parseErr == nil {
				switch {
				case scanned == len(html)-loc[1]:
					parseErr = fmt.Errorf("unterminated state object at byte %d", loc[1])
				case scanned == remainingBudget && remainingBudget < len(html)-loc[1]:
					parseErr = fmt.Errorf("embedded state scan budget exceeded at byte %d", loc[1]+scanned)
				}
			}
			continue
		}
		var blob any
		if err := json.Unmarshal([]byte(obj), &blob); err != nil {
			if parseErr == nil {
				parseErr = fmt.Errorf("invalid state object at byte %d: %w", loc[1], err)
			}
			continue
		}
		blobs = append(blobs, blob)
	}
	// Expand nested `data` JSON strings one level (URQL's dehydrated results).
	var extra []any
	for _, b := range blobs {
		walkMaps(b, func(m map[string]any) {
			if s, ok := m["data"].(string); ok && strings.HasPrefix(strings.TrimSpace(s), "{") {
				s = strings.TrimSpace(s)
				var nested any
				if err := json.Unmarshal([]byte(s), &nested); err != nil {
					if parseErr == nil {
						parseErr = fmt.Errorf("invalid nested state data: %w", err)
					}
					return
				}
				extra = append(extra, nested)
			}
		})
	}
	return append(blobs, extra...), parseErr
}

// braceObject extracts a balanced {...} starting at i, respecting strings/escapes.
func braceObject(s string, i int) (string, bool) {
	obj, ok, _ := braceObjectWithinBudget(s, i, len(s))
	return obj, ok
}

func braceObjectWithinBudget(s string, i, scanBudget int) (string, bool, int) {
	if i < 0 || i >= len(s) || s[i] != '{' || scanBudget <= 0 {
		return "", false, 0
	}
	end := len(s)
	if scanBudget < end-i {
		end = i + scanBudget
	}
	depth, inStr, esc := 0, false, false
	for j := i; j < end; j++ {
		ch := s[j]
		switch {
		case esc:
			esc = false
		case ch == '\\':
			esc = true
		case ch == '"':
			inStr = !inStr
		case inStr:
		case ch == '{':
			depth++
		case ch == '}':
			depth--
			if depth == 0 {
				return s[i : j+1], true, j + 1 - i
			}
		}
	}
	return "", false, end - i
}

// walkMaps invokes fn on every map encountered in an arbitrary JSON value tree.
func walkMaps(v any, fn func(map[string]any)) {
	switch x := v.(type) {
	case map[string]any:
		fn(x)
		for _, vv := range x {
			walkMaps(vv, fn)
		}
	case []any:
		for _, vv := range x {
			walkMaps(vv, fn)
		}
	}
}

var (
	tagRE       = regexp.MustCompile(`<[^>]+>`)
	rowBreakRE  = regexp.MustCompile(`(?i)<br\s*/?>|</(tr|p|li|div|h[1-6])>`)
	cellBreakRE = regexp.MustCompile(`(?i)</(td|th)>`)
	wsRunRE     = regexp.MustCompile(`[ \t]{2,}`)
)

// stripHTML turns the light HTML in detail content (descriptions, ingredients,
// and the nutrition <table>) into readable plain text: table rows and list items
// become lines, cells become space-separated columns, then tags are removed,
// HTML entities are decoded, and whitespace tidied so a nutrition table reads as
// one fact per line.
func stripHTML(s string) string {
	s = rowBreakRE.ReplaceAllString(s, "\n")
	s = cellBreakRE.ReplaceAllString(s, "\t")
	s = tagRE.ReplaceAllString(s, "")
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		cols := make([]string, 0, 4)
		for _, c := range strings.Split(line, "\t") {
			// Decode entities AFTER tag removal so real markup is already gone and we
			// only turn text-level entities into characters: "Sucre &amp; sal" → "Sucre
			// & sal", "&oacute;" → "ó", "&nbsp;" → a (TrimSpace-able) space. Without
			// this the scraped ingredient/nutrition text shows raw &amp;/&nbsp;/&oacute;.
			c = html.UnescapeString(c)
			if c = strings.TrimSpace(c); c != "" {
				cols = append(cols, c)
			}
		}
		if len(cols) == 0 {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(wsRunRE.ReplaceAllString(strings.Join(cols, "  "), " "))
	}
	return b.String()
}
