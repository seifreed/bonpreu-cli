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
	if _, err := cl.ReserveSlot("region-1", "address-2", " ", ""); err == nil {
		t.Fatal("ReserveSlot should reject a missing slot before making a request")
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
