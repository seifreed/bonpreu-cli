package client

import (
	"fmt"
	"net/url"
	"strconv"
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

// OnOffer reports whether the product carries any promotion.
func (p Product) OnOffer() bool { return len(p.Promotions) > 0 }

// Sponsored reports whether the product is a paid/featured placement rather than
// an organic result.
func (p Product) Sponsored() bool { return p.ExternalAdvertID != "" }

// ProductGroup buckets results (e.g. "on_offer", "featured", "ungrouped").
type ProductGroup struct {
	Type              string    `json:"type"`
	DecoratedProducts []Product `json:"decoratedProducts"`
}

// ProductPage is the search / category-listing response envelope.
type ProductPage struct {
	ProductGroups      []ProductGroup `json:"productGroups"`
	AdditionalPageInfo struct {
		NextPageToken string `json:"nextPageToken"`
		TotalCount    int    `json:"totalCount"`
	} `json:"additionalPageInfo"`
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
	defaultPageMax = 100 // products per call; the site uses up to 300
)

// Search runs a full-text product search. max caps both the page size and how
// many products get fully decorated (priced); 0 uses the default.
func (c *Client) Search(term string, max int) (*ProductPage, error) {
	if max <= 0 {
		max = defaultPageMax
	}
	if len(term) > searchTermMax {
		term = term[:searchTermMax]
	}
	q := url.Values{}
	q.Set("q", term)
	q.Set("tag", "web")
	q.Set("includeAdditionalPageInfo", "true")
	q.Set("maxProductsToDecorate", strconv.Itoa(max))
	q.Set("maxPageSize", strconv.Itoa(max))
	var pp ProductPage
	u := c.BaseURL + "/api/webproductpagews/v6/product-pages/search?" + q.Encode()
	if err := c.DoJSON("GET", u, nil, &pp); err != nil {
		return nil, err
	}
	return &pp, nil
}

// CategoryProducts lists a category's products by its retailer category id
// (the short code from Categories, e.g. "0301").
func (c *Client) CategoryProducts(retailerCategoryID string, max int) (*ProductPage, error) {
	if max <= 0 {
		max = defaultPageMax
	}
	q := url.Values{}
	q.Set("retailerCategoryId", retailerCategoryID)
	q.Set("tag", "web")
	q.Set("includeAdditionalPageInfo", "true")
	q.Set("maxProductsToDecorate", strconv.Itoa(max))
	q.Set("maxPageSize", strconv.Itoa(max))
	var pp ProductPage
	u := c.BaseURL + "/api/webproductpagews/v6/product-pages?" + q.Encode()
	if err := c.DoJSON("GET", u, nil, &pp); err != nil {
		return nil, err
	}
	return &pp, nil
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
	if depth <= 0 {
		depth = 1
	}
	var cats []Category
	u := fmt.Sprintf("%s/api/webproductpagews/v1/categories?decoration=false&categoryDepth=%d", c.BaseURL, depth)
	if err := c.DoJSON("GET", u, nil, &cats); err != nil {
		return nil, err
	}
	return cats, nil
}
