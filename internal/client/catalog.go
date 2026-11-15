package client

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Money is the API's price shape: a decimal string + ISO currency.
type Money struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

// UnitPrice is the normalised €/kg, €/L, €/unit figure carried on every product —
// the basis for ranking a category by real value.
type UnitPrice struct {
	Price    Money  `json:"price"`
	Unit     string `json:"unit"`     // e.g. "fop.price.per.kg"
	UnitName string `json:"unitName"` // e.g. "PER_1KG", "EACH"
}

// Promotion flags a product on offer. Description is the human label the site
// shows, e.g. "Abans 2,89€" (was 2.89€) or a multi-buy.
type Promotion struct {
	PromoID             string `json:"promoId"`
	RetailerPromotionID string `json:"retailerPromotionId"`
	Description         string `json:"description"`
	Type                string `json:"type"`
}

// Image is the product photo; only Src is needed for output.
type Image struct {
	Src string `json:"src"`
}

// Product is one catalog item as returned by search and category listings.
type Product struct {
	ProductID           string      `json:"productId"`                  // stable UUID (cart uses this)
	RetailerProductID   string      `json:"retailerProductId"`          // the short human id shown on the site
	ExternalAdvertID    string      `json:"externalAdvertId,omitempty"` // set on sponsored/featured placements
	Name                string      `json:"name"`
	Brand               string      `json:"brand"`
	PackSizeDescription string      `json:"packSizeDescription"`
	Price               Money       `json:"price"`
	UnitPrice           UnitPrice   `json:"unitPrice"`
	Available           bool        `json:"available"`
	IsNew               bool        `json:"isNew"`
	Promotions          []Promotion `json:"promotions"`
	CategoryPath        []string    `json:"categoryPath"`
	Image               Image       `json:"image"`
}

// Sponsored reports whether the product is a paid/featured placement rather than
// an organic result.
func (p Product) Sponsored() bool { return p.ExternalAdvertID != "" }

// ProductGroup buckets results (e.g. "on_offer", "featured", "ungrouped"); only
// the decorated products are consumed, so the bucket label is not modelled.
type ProductGroup struct {
	DecoratedProducts []Product `json:"decoratedProducts"`
}

// ProductPage is the search / category-listing response envelope.
type ProductPage struct {
	ProductGroups []ProductGroup `json:"productGroups"`
}

// Products flattens every group into a single ordered slice — the common case for
// callers that don't care which bucket a hit came from.
func (pp *ProductPage) Products() []Product {
	var out []Product
	for _, g := range pp.ProductGroups {
		out = append(out, g.DecoratedProducts...)
	}
	return out
}

const (
	searchTermMax  = 50  // the SPA truncates the query to 50 chars
	defaultPageMax = 100 // products per call when the caller gives no limit
	apiMaxPageSize = 300 // the API rejects maxPageSize/maxProductsToDecorate > 300
)

// pageSize clamps a requested count to what the API accepts (1..300).
func pageSize(limit int) (int, error) {
	if limit < 0 {
		return 0, fmt.Errorf("limit must be non-negative, got %d", limit)
	}
	if limit == 0 {
		return defaultPageMax, nil
	}
	if limit > apiMaxPageSize {
		return apiMaxPageSize, nil
	}
	return limit, nil
}

// setListParams writes the listing knobs shared by search and category queries:
// web tag and the decorate/page-size caps (both set to n).
func setListParams(q url.Values, n int) {
	q.Set("tag", "web")
	q.Set("maxProductsToDecorate", strconv.Itoa(n))
	q.Set("maxPageSize", strconv.Itoa(n))
}

// Search runs a full-text product search. limit caps both the page size and how
// many products get fully decorated (priced); 0 uses the default. Counts above
// the API's 300 ceiling are clamped to 300.
func (c *Client) Search(term string, limit int) (*ProductPage, error) {
	n, err := pageSize(limit)
	if err != nil {
		return nil, err
	}
	// Truncate to searchTermMax *characters* (runes), not bytes — a byte cut would
	// split a multibyte rune (accented Catalan/Spanish terms) into invalid UTF-8.
	if r := []rune(term); len(r) > searchTermMax {
		term = string(r[:searchTermMax])
	}
	q := url.Values{}
	q.Set("q", term)
	setListParams(q, n)
	var pp ProductPage
	u := c.BaseURL + "/api/webproductpagews/v6/product-pages/search?" + q.Encode()
	if err := c.DoJSON("GET", u, nil, &pp); err != nil {
		return nil, err
	}
	return &pp, nil
}

// CategoryProducts lists a category's products. id may be either the short
// retailer category code from Categories (e.g. "0301") or the category UUID
// (the categoryId field) — UUIDs are detected and queried accordingly.
func (c *Client) CategoryProducts(id string, limit int) (*ProductPage, error) {
	n, err := pageSize(limit)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	if isCategoryUUID(id) {
		q.Set("category", id)
		q.Set("isRetailerCategoryId", "false")
	} else {
		q.Set("retailerCategoryId", id)
	}
	setListParams(q, n)
	var pp ProductPage
	u := c.BaseURL + "/api/webproductpagews/v6/product-pages?" + q.Encode()
	if err := c.DoJSON("GET", u, nil, &pp); err != nil {
		return nil, err
	}
	return &pp, nil
}

// isCategoryUUID reports whether id is a category UUID rather than a short
// retailer code (which is plain digits like "0301"). UUIDs carry hyphens.
func isCategoryUUID(id string) bool {
	return strings.Contains(id, "-")
}

// Category is a node in the catalog tree.
type Category struct {
	CategoryID         string     `json:"categoryId"`
	RetailerCategoryID string     `json:"retailerCategoryId"`
	Name               string     `json:"name"`
	ProductCount       int        `json:"productCount"`
	ChildCategories    []Category `json:"childCategories"`
}

// Categories returns the category tree to the given depth (1 = top level).
func (c *Client) Categories(depth int) ([]Category, error) {
	if depth < 0 {
		return nil, fmt.Errorf("category depth must be non-negative, got %d", depth)
	}
	if depth == 0 {
		depth = 1
	}
	var cats []Category
	u := fmt.Sprintf("%s/api/webproductpagews/v1/categories?decoration=false&categoryDepth=%d", c.BaseURL, depth)
	if err := c.DoJSON("GET", u, nil, &cats); err != nil {
		return nil, err
	}
	return cats, nil
}
