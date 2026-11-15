package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seifreed/bonpreu-cli/internal/client"
)

func TestGuardLineAddWarnsWhenUnpriceable(t *testing.T) {
	var logs []string
	old := stderrWarn
	stderrWarn = func(event, message string, attrs ...any) { logs = append(logs, event+" "+message+fmt.Sprint(attrs...)) }
	defer func() { stderrWarn = old }()

	pd := &client.ProductDetail{}
	pd.Name = "Mystery item"
	pd.Price.Amount = "" // unparseable → priceCents fails

	// flagMax > 0 so a limit is in force; the line is unpriceable, so the guard is
	// skipped — and must warn that --max is not enforced here.
	if err := guardLineAdd(50, pd, 2); err != nil {
		t.Fatalf("unpriceable line must not block: %v", err)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "cart.guard.price_unavailable") || !strings.Contains(logs[0], "not enforced") || strings.Contains(logs[0], pd.Name) {
		t.Fatalf("expected one '--max not enforced' warning, got %v", logs)
	}
}

func TestGuardLineAddSilentWithoutLimit(t *testing.T) {
	t.Setenv("BONPREU_MAX_EUR", "")             // no limit from env
	t.Setenv("BONPREU_CONFIG_DIR", t.TempDir()) // no limit from a real config.toml
	var logs []string
	old := stderrWarn
	stderrWarn = func(event, message string, attrs ...any) { logs = append(logs, event+" "+message+fmt.Sprint(attrs...)) }
	defer func() { stderrWarn = old }()

	pd := &client.ProductDetail{}
	pd.Name = "Mystery item"
	if err := guardLineAdd(0, pd, 2); err != nil { // no limit set
		t.Fatalf("no limit: %v", err)
	}
	if len(logs) != 0 {
		t.Errorf("no limit set → no warning expected, got %v", logs)
	}
}

func testCartClient(srv *httptest.Server) *client.Client {
	cl := client.New()
	cl.BaseURL = srv.URL
	cl.HTTP = srv.Client()
	return cl
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
	srv := apiServer(t)
	defer srv.Close()
	dir := useServer(t, srv, true)
	// Malformed config.toml in the (already session-seeded) dir → resolveMax fails
	// inside guardLineAdd when no --max flag is given.
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("bad = ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := capture(t, func() error { return cmdCart([]string{"add", "23597", "1"}) }); err == nil {
		t.Error("a malformed config should fail the spending-guard resolution")
	}
}

func TestCartWriteBranches(t *testing.T) {
	srv := apiServer(t)
	defer srv.Close()
	useServer(t, srv, true)

	// `set` to the quantity already in the cart (2) → "already at target", no write.
	if out, err := capture(t, func() error { return cmdCart([]string{"set", "23597", "2"}) }); err != nil {
		t.Fatalf("set same qty: %v", err)
	} else {
		_ = out
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
