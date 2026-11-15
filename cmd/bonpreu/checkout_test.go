package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/seifreed/bonpreu-cli/internal/client"
)

const checkoutCSRFHTML = `"session":{"csrf":{"token":"abcdef12-3456-7890-abcd-ef1234567890"}}`

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

func TestCheckoutBrowserUsesAuthenticatedSessionAndReportsHandoffOnly(t *testing.T) {
	srv := apiServer(t)
	defer srv.Close()
	useServer(t, srv, true)
	oldOpen := openCheckoutBrowser
	openCheckoutBrowser = func(baseURL, cookie string) error {
		if baseURL != srv.URL || cookie != "global_sid=a; aws-waf-token=b" {
			t.Errorf("checkout browser args = (%q, %q)", baseURL, cookie)
		}
		return nil
	}
	t.Cleanup(func() { openCheckoutBrowser = oldOpen })
	oldInfo := stderrInfo
	var events []string
	stderrInfo = func(event, _ string, _ ...any) { events = append(events, event) }
	t.Cleanup(func() { stderrInfo = oldInfo })

	out, err := capture(t, func() error { return cmdCheckout([]string{"browser", "--json"}) })
	if err != nil || !strings.Contains(out, `"browser_closed": true`) || !strings.Contains(out, `"order_placed": false`) {
		t.Fatalf("checkout browser output = %q, err %v", out, err)
	}
	if len(events) != 2 || events[0] != "checkout.browser.started" || events[1] != "checkout.browser.closed" {
		t.Fatalf("checkout browser events = %v", events)
	}
}

func TestCheckoutBrowserHumanOutput(t *testing.T) {
	srv := apiServer(t)
	defer srv.Close()
	useServer(t, srv, true)
	oldOpen := openCheckoutBrowser
	openCheckoutBrowser = func(string, string) error { return nil }
	t.Cleanup(func() { openCheckoutBrowser = oldOpen })
	out, err := capture(t, func() error { return cmdCheckout([]string{"browser"}) })
	if err != nil || !strings.Contains(out, "no ha confirmado si se realizó la compra") {
		t.Fatalf("checkout browser output = %q, err %v", out, err)
	}
}

func TestCheckoutBrowserPropagatesHandoffError(t *testing.T) {
	srv := apiServer(t)
	defer srv.Close()
	useServer(t, srv, true)
	oldOpen := openCheckoutBrowser
	openCheckoutBrowser = func(string, string) error { return fmt.Errorf("browser launch failed") }
	t.Cleanup(func() { openCheckoutBrowser = oldOpen })
	_, err := capture(t, func() error { return cmdCheckout([]string{"browser"}) })
	if err == nil || err.Error() != "browser launch failed" {
		t.Fatalf("checkout browser error = %v", err)
	}
}

func TestCheckoutBrowserRequiresAuthentication(t *testing.T) {
	srv := apiServer(t)
	defer srv.Close()
	useServer(t, srv, false)
	_, err := capture(t, func() error { return cmdCheckout([]string{"browser"}) })
	if err == nil || !strings.Contains(err.Error(), "not authenticated") {
		t.Fatalf("checkout browser without auth error = %v", err)
	}
}

func TestCheckoutReserveSlot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Write([]byte(checkoutCSRFHTML))
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/ecomslots/v1/slots/reservation" {
			t.Errorf("reservation request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-CSRF-TOKEN"); got != "abcdef12-3456-7890-abcd-ef1234567890" {
			t.Errorf("reservation CSRF token = %q", got)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode reservation body: %v", err)
		}
		if body["regionId"] != "r-1" || body["deliveryDestinationId"] != "address-1" || body["slotId"] != "slot-1" {
			t.Errorf("reservation body = %#v", body)
		}
		w.Write([]byte(`{"reserved":true}`))
	}))
	defer srv.Close()
	useServer(t, srv, true)
	out, err := capture(t, func() error {
		return cmdCheckout([]string{"reserve", "--address", "address-1", "--slot", "slot-1"})
	})
	if err != nil || !strings.Contains(out, "slot-1 reservada") {
		t.Fatalf("checkout reserve output = %q, err %v", out, err)
	}
}

func TestCheckoutReserveRequiresAddressAndSlot(t *testing.T) {
	for _, args := range [][]string{{"reserve"}, {"reserve", "--address", "address-1"}, {"reserve", "--slot", "slot-1"}} {
		if _, err := capture(t, func() error { return cmdCheckout(args) }); err == nil {
			t.Errorf("cmdCheckout(%v) should require both address and slot", args)
		}
	}
}

func TestCheckoutReserveRequiresAuthentication(t *testing.T) {
	srv := apiServer(t)
	defer srv.Close()
	useServer(t, srv, false)
	_, err := capture(t, func() error {
		return cmdCheckout([]string{"reserve", "--address", "address-1", "--slot", "slot-1"})
	})
	if err == nil || !strings.Contains(err.Error(), "not authenticated") {
		t.Fatalf("reserve without auth error = %v", err)
	}
}

func TestCheckoutReserveRequiresRegion(t *testing.T) {
	srv := apiServer(t)
	defer srv.Close()
	useServer(t, srv, true)
	if err := client.SaveSession(client.Session{Cookie: "global_sid=a; aws-waf-token=b"}); err != nil {
		t.Fatal(err)
	}
	_, err := capture(t, func() error {
		return cmdCheckout([]string{"reserve", "--address", "address-1", "--slot", "slot-1"})
	})
	if err == nil || !strings.Contains(err.Error(), "missing delivery region") {
		t.Fatalf("reserve without region error = %v", err)
	}
}

func TestCheckoutReserveAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Write([]byte(checkoutCSRFHTML))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	useServer(t, srv, true)
	_, err := capture(t, func() error {
		return cmdCheckout([]string{"reserve", "--address", "address-1", "--slot", "slot-1"})
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("reserve API error = %v", err)
	}
}

func TestCheckoutReserveStructuredOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Write([]byte(checkoutCSRFHTML))
			return
		}
		if got := r.Header.Get("X-CSRF-TOKEN"); got != "abcdef12-3456-7890-abcd-ef1234567890" {
			t.Errorf("reservation CSRF token = %q", got)
		}
		w.Write([]byte(`{"reserved":true}`))
	}))
	defer srv.Close()
	useServer(t, srv, true)
	out, err := capture(t, func() error {
		return cmdCheckout([]string{"reserve", "--address", "address-1", "--slot", "slot-1", "--json"})
	})
	if err != nil || !strings.Contains(out, `"reserved": true`) || !strings.Contains(out, `"slot_id": "slot-1"`) {
		t.Fatalf("structured reserve output = %q, err %v", out, err)
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

func TestCheckoutSlotsServiceErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		want   string
	}{
		{name: "no slots offered", status: http.StatusNotFound, want: "no slots offered"},
		{name: "other service error", status: http.StatusInternalServerError, want: "HTTP 500"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()
			useServer(t, srv, true)
			_, err := capture(t, func() error { return cmdCheckout([]string{"slots", "--address", "address-1"}) })
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("checkout slots error = %v, want %q", err, tc.want)
			}
		})
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

func TestCheckoutRejectsUnknownSubcommand(t *testing.T) {
	srv := apiServer(t)
	defer srv.Close()
	useServer(t, srv, false)
	if _, err := capture(t, func() error { return cmdCheckout([]string{"stats"}) }); err == nil || !strings.Contains(err.Error(), "unknown checkout subcommand") {
		t.Fatalf("unknown checkout subcommand error = %v", err)
	}
}
