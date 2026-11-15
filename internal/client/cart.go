package client

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
)

// CartItem is one element of an apply-quantity request: the product's stable UUID
// (productId, not the short retailer id) and a quantity DELTA — positive adds,
// negative removes. The endpoint is additive, not absolute.
type CartItem struct {
	ProductID string  `json:"productId"`
	Quantity  float64 `json:"quantity"`
}

// CartLine is a line in a fetched cart. The cart API carries productId + quantity
// + prices, but no product name (resolve via the catalog if you need one).
type CartLine struct {
	ProductID   string  `json:"productId"`
	Quantity    float64 `json:"quantity"`
	FinalPrice  Money   `json:"finalPrice"`
	TotalPrices struct {
		FinalPrice Money `json:"finalPrice"`
	} `json:"totalPrices"`
}

// Cart is a fetched cart. Lines/Total are projections; Raw preserves the full
// response for --json since the cart schema is broad.
type Cart struct {
	Lines []CartLine      `json:"lines"`
	Total Money           `json:"total"`
	Raw   json.RawMessage `json:"-"`
}

const (
	cartActive    = "/api/cart/v1/carts/active"
	applyQuantity = cartActive + "/apply-quantity?cartProductSorting=CATEGORIES"
)

// cartShape projects the active-cart endpoint. Both the cart view (lines/total)
// and the checkout view (activeCheckoutGroup) read from this one response.
type cartShape struct {
	Items  []CartLine `json:"items"`
	Totals struct {
		ItemPriceAfterPromos Money `json:"itemPriceAfterPromos"`
	} `json:"totals"`
	ACG CheckoutGroup `json:"activeCheckoutGroup"`
}

// activeCart fetches the active-cart endpoint once and projects the fields the
// cart and checkout views share. Raw is preserved for --json; malformed JSON is
// returned as an error so callers cannot treat a broken response as an empty cart.
func (c *Client) activeCart() (json.RawMessage, cartShape, error) {
	raw, err := c.getRaw(c.BaseURL + cartActive)
	if err != nil {
		return nil, cartShape{}, err
	}
	var s cartShape
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, cartShape{}, fmt.Errorf("decode active cart response: %w", err)
	}
	return raw, s, nil
}

// GetCart fetches the active cart. Requires a session cookie (set via LoadAuth).
func (c *Client) GetCart() (*Cart, error) {
	raw, s, err := c.activeCart()
	if err != nil {
		return nil, err
	}
	return &Cart{
		Lines: s.Items,
		Total: s.Totals.ItemPriceAfterPromos,
		Raw:   raw,
	}, nil
}

// CheckAuth reports whether the current cookie is an authenticated session, by
// hitting an endpoint that returns 401 for anonymous callers (the orders list).
func (c *Client) CheckAuth() (bool, error) {
	return c.checkAuth(nil)
}

// CheckAuthWithCookie verifies a candidate cookie without mutating the client's
// cached cookie or persisting it.
func (c *Client) CheckAuthWithCookie(cookie string) (bool, error) {
	if err := c.validateCookieTransport(cookie, c.BaseURL); err != nil {
		return false, err
	}
	c.warnExternalCookieHost(cookie)
	return c.checkAuth(map[string]string{"cookie": cookie})
}

func (c *Client) checkAuth(extra map[string]string) (bool, error) {
	err := c.doJSONHeaders("GET", c.BaseURL+"/api/order/v6/orders", nil, nil, extra)
	if err == nil {
		return true, nil
	}
	if status, ok := HTTPStatus(err); ok && (status == 401 || status == 403) {
		return false, nil
	}
	return false, err
}

var csrfRE = regexp.MustCompile(`"csrf":\{"token":"([0-9a-fA-F-]{8,})"`)

// csrfToken fetches a CSRF token for cart writes. The web app embeds it in the
// authenticated homepage SSR state at session.csrf.token; with the session
// cookie we read it the same way (no separate endpoint exists).
func (c *Client) csrfToken() (string, error) {
	html, err := c.GetText(c.BaseURL + "/")
	if err != nil {
		return "", err
	}
	m := csrfRE.FindStringSubmatch(html)
	if m == nil {
		return "", fmt.Errorf("no CSRF token in page — the session may not be authenticated")
	}
	return m[1], nil
}

// ApplyQuantity applies quantity DELTAS to the active cart (+adds, -removes) in a
// single write, the same call the web app's add/remove buttons make. It fetches a
// CSRF token first and sends the ecom-request-source header the API requires.
func (c *Client) ApplyQuantity(items []CartItem) (*Cart, error) {
	if len(items) == 0 {
		return c.GetCart()
	}
	for _, item := range items {
		if strings.TrimSpace(item.ProductID) == "" {
			return nil, fmt.Errorf("cart product ID is required")
		}
		if item.Quantity == 0 || math.IsNaN(item.Quantity) || math.IsInf(item.Quantity, 0) {
			return nil, fmt.Errorf("cart quantity delta must be finite and non-zero")
		}
	}
	csrf, err := c.csrfToken()
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	err = c.doJSONHeaders("POST", c.BaseURL+applyQuantity, items, &raw, map[string]string{
		"x-csrf-token":        csrf,
		"ecom-request-source": "web",
	})
	if err != nil {
		return nil, fmt.Errorf("cart quantity update outcome is uncertain; inspect the cart before retrying: %w", err)
	}
	return &Cart{Raw: raw}, nil
}
