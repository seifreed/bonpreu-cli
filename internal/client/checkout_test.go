package client

import (
	"net/http"
	"net/http/httptest"
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
