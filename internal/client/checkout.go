package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
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
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(s.ACG, &fields); err != nil || fields == nil {
		if err == nil {
			err = fmt.Errorf("expected an object")
		}
		return nil, Money{}, nil, fmt.Errorf("decode active cart response: invalid activeCheckoutGroup: %w", err)
	}
	canCheckout, ok := fields["canCheckout"]
	if !ok || bytes.Equal(bytes.TrimSpace(canCheckout), []byte("null")) {
		return nil, Money{}, nil, fmt.Errorf("decode active cart response: invalid activeCheckoutGroup: missing or null canCheckout")
	}
	var group CheckoutGroup
	if err := json.Unmarshal(s.ACG, &group); err != nil {
		return nil, Money{}, nil, fmt.Errorf("decode active cart response: invalid activeCheckoutGroup: %w", err)
	}
	return &group, s.Totals.ItemPriceAfterPromos, raw, nil
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

// ReserveSlot selects a delivery slot for the active cart. It does not place an
// order; the website performs payment authentication and final validation later.
func (c *Client) ReserveSlot(regionID, deliveryDestinationID, slotID, externalAddress string) (json.RawMessage, error) {
	if strings.TrimSpace(regionID) == "" || strings.TrimSpace(deliveryDestinationID) == "" || strings.TrimSpace(slotID) == "" {
		return nil, fmt.Errorf("region, delivery destination, and slot IDs are required")
	}
	body := struct {
		RegionID              string `json:"regionId"`
		SlotID                string `json:"slotId"`
		DeliveryDestinationID string `json:"deliveryDestinationId"`
		ExternalAddress       string `json:"externalAddress,omitempty"`
	}{regionID, slotID, deliveryDestinationID, externalAddress}
	csrf, err := c.csrfToken()
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	err = c.doJSONHeaders(http.MethodPost, c.BaseURL+"/api/ecomslots/v1/slots/reservation", body, &raw, map[string]string{
		"x-csrf-token": csrf,
	})
	if err != nil {
		return nil, fmt.Errorf("delivery slot reservation outcome is uncertain; inspect checkout before retrying: %w", err)
	}
	return raw, nil
}
