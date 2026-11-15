package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seifreed/bonpreu-cli/internal/client"
)

func TestGuardLineAddRejectsUnpriceableWhenLimitSet(t *testing.T) {
	pd := &client.ProductDetail{}
	pd.Name = "Mystery item"
	pd.Price.Amount = "" // unparseable → priceCents fails

	if err := guardLineAdd(50, pd, 2); err == nil || !strings.Contains(err.Error(), "cannot enforce") {
		t.Fatalf("unpriceable line must fail closed when --max is set, got %v", err)
	}
}

func TestGuardLineAddRejectsOverflowWhenLimitSet(t *testing.T) {
	pd := &client.ProductDetail{}
	pd.Price.Amount = "1.00"
	if err := guardLineAdd(50, pd, 1e300); err == nil || !strings.Contains(err.Error(), "cannot enforce") {
		t.Fatalf("unrepresentable line subtotal must fail closed, got %v", err)
	}
}

func TestGuardLineAddSilentWithoutLimit(t *testing.T) {
	t.Setenv("BONPREU_MAX_EUR", "")             // no limit from env
	t.Setenv("BONPREU_CONFIG_DIR", t.TempDir()) // no limit from a real config.toml

	pd := &client.ProductDetail{}
	pd.Name = "Mystery item"
	if err := guardLineAdd(0, pd, 2); err != nil { // no limit set
		t.Fatalf("no limit: %v", err)
	}
}

func testCartClient(srv *httptest.Server) *client.Client {
	cl := client.New()
	cl.BaseURL = srv.URL
	cl.HTTP = srv.Client()
	return cl
}

func TestCartAddDoesNotWriteWhenLimitCannotBeEnforced(t *testing.T) {
	var cartWrite bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			cartWrite = true
		}
		if strings.HasPrefix(r.URL.Path, "/products/") {
			_, _ = w.Write([]byte(`<html>window.__URQL_DATA__ = {"p":{"retailerProductId":"23597","productId":"uuid-23597","name":"Mystery item","price":{"amount":"NaN"}}}</html>`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	useServer(t, srv, true)

	if _, err := capture(t, func() error { return cmdCart([]string{"add", "23597", "1", "--max", "50"}) }); err == nil || !strings.Contains(err.Error(), "cannot enforce") {
		t.Fatalf("cart add error = %v, want unpriceable price-limit error", err)
	}
	if cartWrite {
		t.Fatal("cart add wrote despite an active limit and unknown product price")
	}
}

func TestCurrentQtyPropagatesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if _, err := currentQty(testCartClient(srv), "p1"); err == nil {
		t.Fatal("expected error when the cart read fails, got nil (would over-add on `cart set`)")
	}
}

func TestCurrentQtyReturnsQuantity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"cartId":"c1","items":[{"productId":"p1","quantity":4}]}`))
	}))
	defer srv.Close()
	q, err := currentQty(testCartClient(srv), "p1")
	if err != nil || q != 4 {
		t.Fatalf("currentQty = %v, %v; want 4, nil", q, err)
	}
}

func TestCartAddJSON(t *testing.T) {
	srv := apiServer(t)
	defer srv.Close()
	useServer(t, srv, true)
	if _, err := capture(t, func() error { return cmdCart([]string{"add", "23597", "1", "--json"}) }); err != nil {
		t.Fatalf("cart add --json: %v", err)
	}
}

func TestCartSetCurrentQtyError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/products/"):
			w.Write([]byte(`<html>window.__URQL_DATA__ = {"p":{"retailerProductId":"23597","productId":"u","name":"Llet","price":{"amount":"1.69"}}}</html>`))
		default: // active-cart read fails → currentQty error on `set`
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	useServer(t, srv, true)
	if _, err := capture(t, func() error { return cmdCart([]string{"set", "23597", "3"}) }); err == nil {
		t.Error("set with an unreadable cart should surface the read error")
	}
}

func TestCartClearApplyError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "apply-quantity"):
			w.WriteHeader(http.StatusInternalServerError)
		case r.URL.Path == "/":
			w.Write([]byte(`"csrf":{"token":"abcdef12-3456-7890-abcd-ef1234567890"}`))
		default: // active cart has a line to clear
			w.Write([]byte(`{"items":[{"productId":"p","quantity":1}]}`))
		}
	}))
	defer srv.Close()
	useServer(t, srv, true)
	if _, err := capture(t, func() error { return cmdCart([]string{"clear"}) }); err == nil {
		t.Error("clear should surface the apply-quantity error")
	}
}

// `set` for a product not currently in the cart exercises currentQty's
// not-found return.
func TestCartSetNewProduct(t *testing.T) {
	srv := apiServer(t)
	defer srv.Close()
	useServer(t, srv, true)
	if _, err := capture(t, func() error { return cmdCart([]string{"set", "00944", "2"}) }); err != nil {
		t.Fatalf("set new product: %v", err)
	}
}

func TestCartAddArgErrorsAndApplyError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/":
			w.Write([]byte(`"csrf":{"token":"abcdef12-3456-7890-abcd-ef1234567890"}`))
		case strings.HasPrefix(r.URL.Path, "/products/"):
			w.Write([]byte(`<html>window.__URQL_DATA__ = {"p":{"retailerProductId":"23597","productId":"u","name":"Llet","price":{"amount":"1.69"}}}</html>`))
		default: // apply-quantity / cart reads fail
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	useServer(t, srv, true)

	if _, err := capture(t, func() error { return cmdCart([]string{"add", "23597"}) }); err == nil {
		t.Error("add with one arg should error (need id + qty)")
	}
	if _, err := capture(t, func() error { return cmdCart([]string{"add", "23597", "1"}) }); err == nil {
		t.Error("add should surface the apply-quantity error")
	}
}

func TestGuardResolveMaxErrorOnAdd(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BONPREU_CONFIG_DIR", dir)
	t.Setenv("BONPREU_MAX_EUR", "")
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("bad = ["), 0o600); err != nil {
		t.Fatal(err)
	}
	pd := &client.ProductDetail{}
	pd.Price.Amount = "1.69"
	if err := guardLineAdd(0, pd, 1); err == nil {
		t.Error("a malformed config should fail the spending-guard resolution")
	}
}

func TestCartWriteBranches(t *testing.T) {
	srv := apiServer(t)
	defer srv.Close()
	useServer(t, srv, true)
	var events []string
	oldInfo := stderrInfo
	stderrInfo = func(event, _ string, _ ...any) { events = append(events, event) }
	defer func() { stderrInfo = oldInfo }()

	// `set` to the quantity already in the cart (2) → "already at target", no write.
	if out, err := capture(t, func() error { return cmdCart([]string{"set", "23597", "2"}) }); err != nil {
		t.Fatalf("set same qty: %v", err)
	} else {
		_ = out
	}
	if len(events) != 1 || events[0] != "cart.quantity.no_change" {
		t.Fatalf("no-change event = %v", events)
	}
	if _, err := capture(t, func() error { return cmdCart([]string{"add", "23597", "1"}) }); err != nil {
		t.Fatalf("add with no limit: %v", err)
	}
	if len(events) != 2 || events[1] != "cart.quantity.changed" {
		t.Fatalf("cart write events = %v", events)
	}
	// --max exceeded: price 1.69 × 100 > 1€ → refused.
	if _, err := capture(t, func() error { return cmdCart([]string{"add", "23597", "100", "--max", "1"}) }); err == nil {
		t.Error("expected --max to refuse the line")
	}
	// bad quantity
	if _, err := capture(t, func() error { return cmdCart([]string{"add", "23597", "notnum"}) }); err == nil {
		t.Error("expected bad-qty error")
	}
	// unknown subcommand
	if _, err := capture(t, func() error { return cmdCart([]string{"frobnicate"}) }); err == nil {
		t.Error("expected unknown-subcommand error")
	}
}

func TestAuthedClientWithoutSession(t *testing.T) {
	srv := apiServer(t)
	defer srv.Close()
	useServer(t, srv, false) // no session seeded
	if _, err := authedClient(&common{}); err == nil {
		t.Error("authedClient should fail without a session")
	}
}

func TestAuthedClientSurfacesUnreadableAuthState(t *testing.T) {
	for _, tt := range []struct {
		name string
		file string
		body string
	}{
		{name: "corrupt session", file: "session.json", body: "{ invalid"},
		{name: "malformed config", file: "config.toml", body: "not valid = toml ["},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("BONPREU_CONFIG_DIR", dir)
			t.Setenv("BONPREU_MAX_EUR", "")
			if err := os.WriteFile(filepath.Join(dir, tt.file), []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := authedClient(&common{operation: "cart get"}); err == nil || strings.Contains(err.Error(), "not authenticated") {
				t.Fatalf("authedClient error = %v; want the underlying auth-state error", err)
			}
		})
	}
}

func TestAuthedClientRejectsAnonymousCookie(t *testing.T) {
	srv := apiServer(t)
	defer srv.Close()
	useServer(t, srv, false)
	// A stored cookie with no session markers must be rejected with a clear message
	// rather than passing the gate and letting cart/checkout hit the API for a 4xx.
	if err := client.SaveSession(client.Session{Cookie: "VISITORID=anon"}); err != nil {
		t.Fatal(err)
	}
	_, err := authedClient(&common{})
	if err == nil || !strings.Contains(err.Error(), "anonymous") {
		t.Errorf("anonymous cookie should be rejected as such, got %v", err)
	}
}
