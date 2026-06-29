package client

import (
	"encoding/json"
	"net/url"
)

// CheckoutGroup is the active cart's checkout-readiness view: whether the basket
// can be checked out and, if not, why (below the minimum, no slot booked, empty).
// Only the fields the text view consumes are modelled; --json forwards the raw
// response, so unused API fields are left out of the type.
type CheckoutGroup struct {
	CanCheckout              bool     `json:"canCheckout"`
	CheckoutRestrictions     []string `json:"checkoutRestrictions"`
	MinimumCheckoutThreshold Money    `json:"minimumCheckoutThreshold"`
}

// CheckoutStatus reads the active cart and returns its checkout group plus the
// current total — the verifiable, non-mutating checkout view.
func (c *Client) CheckoutStatus() (*CheckoutGroup, Money, json.RawMessage, error) {
	raw, s, err := c.activeCart()
	if err != nil {
		return nil, Money{}, nil, err
	}
	return &s.ACG, s.Totals.ItemPriceAfterPromos, raw, nil
}

// Addresses lists the customer's saved delivery destinations. The slot endpoints
// hang off a destination id, so this is the first step toward booking a slot.
func (c *Client) Addresses() (json.RawMessage, error) {
	return c.getRaw(c.BaseURL + "/api/ecomdeliverydestinations/v4/delivery-addresses?deliveryMethod=HOME_DELIVERY")
}

// Slots lists the delivery slots offered for a delivery destination. The id is
// user-supplied (checkout slots --address <id>), so it is query-escaped rather
// than concatenated raw — an id with &, #, or a space would otherwise build a
// malformed request URL.
func (c *Client) Slots(deliveryDestinationID string) (json.RawMessage, error) {
	return c.getRaw(c.BaseURL + "/api/ecomslots/v1/web/slots-offering?deliveryDestinationId=" + url.QueryEscape(deliveryDestinationID))
}
