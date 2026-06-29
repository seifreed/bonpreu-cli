package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckoutCanValidate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"totals":{"itemPriceAfterPromos":{"amount":"40.00"}},"activeCheckoutGroup":{"canCheckout":true,"minimumCheckoutThreshold":{"amount":"35.00"}}}`))
	}))
	defer srv.Close()
	useServer(t, srv, true)
	out, err := capture(t, func() error { return cmdCheckout([]string{"status"}) })
	if err != nil || !strings.Contains(out, "es pot validar") {
		t.Fatalf("can-validate path: %q err %v", out, err)
	}
}

func TestCheckoutSlotsMissingAddress(t *testing.T) {
	srv := apiServer(t)
	defer srv.Close()
	useServer(t, srv, true)
	if _, err := capture(t, func() error { return cmdCheckout([]string{"slots"}) }); err == nil {
		t.Error("slots without --address should error")
	}
}

func TestCheckoutRawToonVariants(t *testing.T) {
	srv := apiServer(t)
	defer srv.Close()
	useServer(t, srv, true)
	if _, err := capture(t, func() error { return cmdCheckout([]string{"addresses", "--toon"}) }); err != nil {
		t.Errorf("addresses --toon: %v", err)
	}
	if _, err := capture(t, func() error { return cmdCheckout([]string{"slots", "--address", "a", "--toon"}) }); err != nil {
		t.Errorf("slots --toon: %v", err)
	}
}

func TestCheckoutDefaultSubcommand(t *testing.T) {
	srv := apiServer(t)
	defer srv.Close()
	useServer(t, srv, true)
	// args[0] is a flag, not a known subcommand → falls through to status.
	if _, err := capture(t, func() error { return cmdCheckout([]string{"--json"}) }); err != nil {
		t.Fatalf("checkout default→status: %v", err)
	}
}
