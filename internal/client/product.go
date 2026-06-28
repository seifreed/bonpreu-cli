package client

import (
	"encoding/json"
	"fmt"
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
	html, err := c.GetText(c.BaseURL + "/products/" + retailerProductID)
	if err != nil {
		return nil, err
	}
	node := findProductNode(html, retailerProductID)
	if node == nil {
		return nil, fmt.Errorf("product %s: not found in page (id may be invalid or out of catalog)", retailerProductID)
	}
	b, _ := json.Marshal(node)
	var pd ProductDetail
	if err := json.Unmarshal(b, &pd.Product); err != nil {
		return nil, fmt.Errorf("decode product %s: %w", retailerProductID, err)
	}
	pd.Details = collectDetails(html)
	return &pd, nil
}

var stateVarRE = regexp.MustCompile(`window\.(__URQL_DATA__|__INITIAL_STATE__|__QUERY_INITIAL_STATE__)\s*=\s*`)

// findProductNode walks every embedded state blob (and the double-encoded `data`
// strings inside URQL) for the richest object whose retailerProductId matches.
func findProductNode(html, id string) map[string]any {
	var best map[string]any
	bestLen := -1
	consider := func(m map[string]any) {
		if v, _ := m["retailerProductId"].(string); v == id {
			if _, ok := m["name"]; ok {
				if n := len(fmt.Sprint(m)); n > bestLen {
					best, bestLen = m, n
				}
			}
		}
	}
	for _, blob := range embeddedStates(html) {
		walkMaps(blob, consider)
	}
	return best
}

// collectDetails gathers every {title, content} info block on the page, in order,
// de-duplicated by title. These hold ingredients / nutrition / allergens text.
func collectDetails(html string) []Detail {
	var out []Detail
	seen := map[string]bool{}
	for _, blob := range embeddedStates(html) {
		walkMaps(blob, func(m map[string]any) {
			t, okT := m["title"].(string)
			ct, okC := m["content"].(string)
			if t == "brand" { // already shown as the product's marca
				return
			}
			if okT && okC && t != "" && ct != "" && !seen[t] {
				seen[t] = true
				out = append(out, Detail{Title: t, Content: stripHTML(ct)})
			}
		})
	}
	return out
}

// embeddedStates returns each window.__…__ JSON blob, plus the JSON decoded from
// any nested double-encoded `data` strings (URQL stores query results that way).
func embeddedStates(html string) []any {
	var blobs []any
	for _, loc := range stateVarRE.FindAllStringIndex(html, -1) {
		obj, ok := braceObject(html, loc[1])
		if !ok {
			continue
		}
		var v any
		if json.Unmarshal([]byte(obj), &v) == nil {
			blobs = append(blobs, v)
		}
	}
	// Expand nested `data` JSON strings one level (URQL's dehydrated results).
	var extra []any
	for _, b := range blobs {
		walkMaps(b, func(m map[string]any) {
			if s, ok := m["data"].(string); ok && strings.HasPrefix(s, "{") {
				var v any
				if json.Unmarshal([]byte(s), &v) == nil {
					extra = append(extra, v)
				}
			}
		})
	}
	return append(blobs, extra...)
}

// braceObject extracts a balanced {...} starting at i, respecting strings/escapes.
func braceObject(s string, i int) (string, bool) {
	if i >= len(s) || s[i] != '{' {
		return "", false
	}
	depth, inStr, esc := 0, false, false
	for j := i; j < len(s); j++ {
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
				return s[i : j+1], true
			}
		}
	}
	return "", false
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

var tagRE = regexp.MustCompile(`<[^>]+>`)

// stripHTML turns the light HTML in detail content into readable plain text.
func stripHTML(s string) string {
	s = strings.ReplaceAll(s, "<br />", "\n")
	s = strings.ReplaceAll(s, "<br/>", "\n")
	s = strings.ReplaceAll(s, "<br>", "\n")
	s = tagRE.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}
