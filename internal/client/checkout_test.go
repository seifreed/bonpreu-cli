package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestCheckoutStatusAndSlotsAndAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "carts/active"):
			w.Write([]byte(`{"totals":{"itemPriceAfterPromos":{"amount":"40.00"}},"activeCheckoutGroup":{"canCheckout":true,"minimumCheckoutThreshold":{"amount":"35.00"}}}`))
		default:
			w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer srv.Close()
	cl := testClient(srv)

	acg, total, raw, err := cl.CheckoutStatus()
	if err != nil || !acg.CanCheckout || total.Amount != "40.00" || len(raw) == 0 {
		t.Fatalf("CheckoutStatus = %+v %v %v", acg, total, err)
	}
	if _, err := cl.Addresses(); err != nil {
		t.Fatalf("Addresses: %v", err)
	}
	if _, err := cl.Slots("addr-1"); err != nil {
		t.Fatalf("Slots: %v", err)
	}
}

func TestCheckoutStatusRejectsMissingOrNullGroup(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	cl := testClient(srv)
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "missing", body: `{"totals":{"itemPriceAfterPromos":{"amount":"40.00"}}}`},
		{name: "null", body: `{"activeCheckoutGroup":null,"totals":{"itemPriceAfterPromos":{"amount":"40.00"}}}`},
		{name: "missing readiness", body: `{"activeCheckoutGroup":{},"totals":{"itemPriceAfterPromos":{"amount":"40.00"}}}`},
		{name: "null readiness", body: `{"activeCheckoutGroup":{"canCheckout":null},"totals":{"itemPriceAfterPromos":{"amount":"40.00"}}}`},
		{name: "wrong field type", body: `{"activeCheckoutGroup":{"canCheckout":"yes"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body = tc.body
			if _, _, _, err := cl.CheckoutStatus(); err == nil || !strings.Contains(err.Error(), "activeCheckoutGroup") {
				t.Fatalf("CheckoutStatus(%s) error = %v; want invalid group error", tc.name, err)
			}
		})
	}
}

func TestReserveSlotPostsSelectedSlotAndRegion(t *testing.T) {
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Write([]byte(`"session":{"csrf":{"token":"abcdef12-3456-7890-abcd-ef1234567890"}}`))
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/ecomslots/v1/slots/reservation" {
			t.Errorf("reservation request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-CSRF-TOKEN"); got != "abcdef12-3456-7890-abcd-ef1234567890" {
			t.Errorf("reservation CSRF token = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode reservation body: %v", err)
		}
		w.Write([]byte(`{"reserved":true}`))
	}))
	defer srv.Close()
	cl := testClient(srv)
	cl.RegionID = "region-1"
	raw, err := cl.ReserveSlot(cl.RegionID, "address-2", "slot-3", "Custom door")
	if err != nil || string(raw) != `{"reserved":true}` {
		t.Fatalf("ReserveSlot = %s, %v", raw, err)
	}
	want := map[string]string{
		"regionId": "region-1", "deliveryDestinationId": "address-2", "slotId": "slot-3", "externalAddress": "Custom door",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reservation body = %#v, want %#v", got, want)
	}
}

func TestReserveSlotRejectsMissingIDs(t *testing.T) {
	cl := &Client{BaseURL: "https://example.invalid"}
	for _, tc := range []struct {
		name                  string
		region, address, slot string
	}{
		{name: "region", address: "address-2", slot: "slot-3"},
		{name: "address", region: "region-1", slot: "slot-3"},
		{name: "slot", region: "region-1", address: "address-2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := cl.ReserveSlot(tc.region, tc.address, tc.slot, ""); err == nil {
				t.Fatalf("ReserveSlot should reject missing %s before making a request", tc.name)
			}
		})
	}
}

func TestReserveSlotWrapsUncertainPostError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Write([]byte(`"session":{"csrf":{"token":"abcdef12-3456-7890-abcd-ef1234567890"}}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	_, err := testClient(srv).ReserveSlot("region-1", "address-2", "slot-3", "")
	if err == nil || !strings.Contains(err.Error(), "outcome is uncertain") {
		t.Fatalf("ReserveSlot error = %v, want uncertain outcome", err)
	}
	if status, ok := HTTPStatus(err); !ok || status != http.StatusInternalServerError {
		t.Fatalf("HTTPStatus(%v) = %d, %v; want 500, true", err, status, ok)
	}
}

func TestReserveSlotReturnsCSRFError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			t.Errorf("reservation should not be sent without CSRF token, got %s", r.URL.Path)
		}
		w.Write([]byte(`<html>no session state</html>`))
	}))
	defer srv.Close()
	if _, err := testClient(srv).ReserveSlot("region-1", "address-2", "slot-3", ""); err == nil || !strings.Contains(err.Error(), "no CSRF token") {
		t.Fatalf("ReserveSlot without CSRF token error = %v", err)
	}
}
