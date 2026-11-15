package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seifreed/bonpreu-cli/internal/client"
)

// apiServer returns an httptest server that answers every endpoint the commands
// hit with canned, well-formed responses.
func apiServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.Contains(p, "/product-pages/search"):
			w.Write([]byte(`{"productGroups":[{"decoratedProducts":[{"retailerProductId":"23597","name":"Llet","price":{"amount":"1.69","currency":"EUR"}}]}]}`))
		case strings.HasSuffix(p, "/product-pages"):
			w.Write([]byte(`{"productGroups":[{"decoratedProducts":[{"retailerProductId":"1","name":"Aigua","price":{"amount":"0.50","currency":"EUR"}}]}]}`))
		case strings.Contains(p, "/v1/categories"):
			w.Write([]byte(`[{"retailerCategoryId":"0301","name":"Begudes","childCategories":[{"retailerCategoryId":"0302","name":"Aigua"}]}]`))
		case strings.HasPrefix(p, "/products/"):
			id := strings.TrimPrefix(p, "/products/")
			_, _ = fmt.Fprintf(w, `<html>window.__URQL_DATA__ = {"p":{"retailerProductId":%q,"productId":"uuid-%s","name":"Llet","brand":"ATO","price":{"amount":"1.69","currency":"EUR"}}}</html>`, id, id)
		case strings.Contains(p, "/cart/v1/carts/active"):
			w.Write([]byte(`{"items":[{"productId":"uuid-23597","quantity":2,"finalPrice":{"amount":"1.69"},"totalPrices":{"finalPrice":{"amount":"3.38"}}}],"totals":{"itemPriceAfterPromos":{"amount":"3.38","currency":"EUR"}},"activeCheckoutGroup":{"canCheckout":false,"checkoutRestrictions":["NOT_REACHED_THRESHOLD"],"minimumCheckoutThreshold":{"amount":"35.00"}}}`))
		case p == "/":
			w.Write([]byte(`<html>"session":{"csrf":{"token":"abcdef12-3456-7890-abcd-ef1234567890"}}</html>`))
		case strings.Contains(p, "/apply-quantity"):
			w.Write([]byte(`{"items":[]}`))
		case strings.Contains(p, "/order/v6/orders"):
			w.WriteHeader(http.StatusOK) // authenticated
		case strings.Contains(p, "ecomdeliverydestinations"):
			w.Write([]byte(`{"deliveryAddresses":[{"id":"addr-1"}]}`))
		case strings.Contains(p, "ecomslots"):
			w.Write([]byte(`{"slots":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// useServer points the CLI at srv, isolates config/session to a fresh temp dir,
// and returns that dir so a test can write fixtures (e.g. a malformed config.toml)
// into it without re-reading the env (which gosec flags as a tainted path source).
func useServer(t *testing.T, srv *httptest.Server, authed bool) string {
	t.Helper()
	t.Setenv("BONPREU_BASE_URL", srv.URL)
	dir := t.TempDir()
	t.Setenv("BONPREU_CONFIG_DIR", dir)
	t.Setenv("BONPREU_MAX_EUR", "")
	if authed {
		if err := client.SaveSession(client.Session{Cookie: "global_sid=a; aws-waf-token=b", RegionID: "r-1"}); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// capture runs fn with os.Stdout redirected and returns what it wrote.
func capture(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	runErr := fn()
	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	return string(out), runErr
}

func TestCmdSearchVariants(t *testing.T) {
	srv := apiServer(t)
	defer srv.Close()
	useServer(t, srv, false)

	for _, args := range [][]string{
		{"llet"},
		{"--json", "llet"},
		{"--toon", "llet"},
		{"--fresh", "--limit", "5", "llet"},
	} {
		out, err := capture(t, func() error { return cmdSearch(args) })
		if err != nil {
			t.Fatalf("cmdSearch(%v): %v", args, err)
		}
		if !strings.Contains(out, "Llet") && !strings.Contains(out, "23597") {
			t.Errorf("cmdSearch(%v) output missing product: %q", args, out)
		}
	}
}

func TestCmdBatch(t *testing.T) {
	srv := apiServer(t)
	defer srv.Close()
	useServer(t, srv, false)
	out, err := capture(t, func() error { return cmdBatch([]string{"llet"}) })
	if err != nil {
		t.Fatalf("cmdBatch: %v", err)
	}
	if !strings.Contains(out, "Llet") {
		t.Errorf("cmdBatch output: %q", out)
	}
}

func TestCmdTotal(t *testing.T) {
	srv := apiServer(t)
	defer srv.Close()
	useServer(t, srv, false)
	out, err := capture(t, func() error { return cmdTotal([]string{"23597", "2"}) })
	if err != nil {
		t.Fatalf("cmdTotal: %v", err)
	}
	if !strings.Contains(out, "total:") {
		t.Errorf("cmdTotal output: %q", out)
	}
}

func TestCmdProduct(t *testing.T) {
	srv := apiServer(t)
	defer srv.Close()
	useServer(t, srv, false)
	out, err := capture(t, func() error { return cmdProduct([]string{"23597"}) })
	if err != nil {
		t.Fatalf("cmdProduct: %v", err)
	}
	if !strings.Contains(out, "Llet") {
		t.Errorf("cmdProduct output: %q", out)
	}
}

func TestCmdCategories(t *testing.T) {
	srv := apiServer(t)
	defer srv.Close()
	useServer(t, srv, false)

	out, err := capture(t, func() error { return cmdCategories(nil) })
	if err != nil || !strings.Contains(out, "Begudes") {
		t.Fatalf("categories tree: %q err %v", out, err)
	}
	out, err = capture(t, func() error { return cmdCategories([]string{"--id", "0301"}) })
	if err != nil || !strings.Contains(out, "Aigua") {
		t.Fatalf("categories by id: %q err %v", out, err)
	}
}

func TestCmdWhoami(t *testing.T) {
	srv := apiServer(t)
	defer srv.Close()
	useServer(t, srv, true)
	var events []string
	oldInfo := stderrInfo
	stderrInfo = func(event, _ string, _ ...any) { events = append(events, event) }
	defer func() { stderrInfo = oldInfo }()
	out, err := capture(t, func() error { return cmdWhoami(nil) })
	if err != nil || !strings.Contains(out, "authenticated") {
		t.Fatalf("whoami: %q err %v", out, err)
	}
	if len(events) != 1 || events[0] != "auth.session.verified" {
		t.Fatalf("whoami events = %v", events)
	}
}

func TestCmdCart(t *testing.T) {
	srv := apiServer(t)
	defer srv.Close()
	useServer(t, srv, true)
	var events []string
	oldInfo := stderrInfo
	stderrInfo = func(event, _ string, _ ...any) { events = append(events, event) }
	defer func() { stderrInfo = oldInfo }()

	if out, err := capture(t, func() error { return cmdCart([]string{"get"}) }); err != nil || !strings.Contains(out, "uuid-23597") {
		t.Fatalf("cart get: %q err %v", out, err)
	}
	if _, err := capture(t, func() error { return cmdCart([]string{"add", "23597", "1"}) }); err != nil {
		t.Fatalf("cart add: %v", err)
	}
	if _, err := capture(t, func() error { return cmdCart([]string{"set", "23597", "5"}) }); err != nil {
		t.Fatalf("cart set: %v", err)
	}
	if _, err := capture(t, func() error { return cmdCart([]string{"clear"}) }); err != nil {
		t.Fatalf("cart clear: %v", err)
	}
	for _, event := range events {
		if event == "cart.clear.completed" {
			return
		}
	}
	t.Fatalf("cart events missing clear completion: %v", events)
}

func TestCmdCheckout(t *testing.T) {
	srv := apiServer(t)
	defer srv.Close()
	useServer(t, srv, true)
	var events []string
	oldInfo := stderrInfo
	stderrInfo = func(event, _ string, _ ...any) { events = append(events, event) }
	defer func() { stderrInfo = oldInfo }()

	if out, err := capture(t, func() error { return cmdCheckout([]string{"status"}) }); err != nil || !strings.Contains(out, "mínim") {
		t.Fatalf("checkout status: %q err %v", out, err)
	}
	if len(events) != 1 || events[0] != "checkout.status.checked" {
		t.Fatalf("checkout status events = %v", events)
	}
	if _, err := capture(t, func() error { return cmdCheckout([]string{"addresses"}) }); err != nil {
		t.Fatalf("checkout addresses: %v", err)
	}
	if _, err := capture(t, func() error { return cmdCheckout([]string{"slots", "--address", "addr-1"}) }); err != nil {
		t.Fatalf("checkout slots: %v", err)
	}
}

func TestImportHarEndToEnd(t *testing.T) {
	t.Setenv("BONPREU_CONFIG_DIR", t.TempDir())
	har := `{"log":{"entries":[{"request":{"url":"https://compraonline.bonpreuesclat.cat/api/x?regionId=r-9","headers":[{"name":"cookie","value":"global_sid=a; aws-waf-token=b"}]}}]}}`
	path := filepath.Join(t.TempDir(), "s.har")
	if err := os.WriteFile(path, []byte(har), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cmdImportHar([]string{"--file", path}); err != nil {
		t.Fatalf("import-har: %v", err)
	}
	if s, err := client.LoadSession(); err != nil || s.RegionID != "r-9" {
		t.Errorf("session region = %q, want r-9", s.RegionID)
	}
}

func TestHandlerErrorPaths(t *testing.T) {
	srv := errorServer(t)
	defer srv.Close()
	useServer(t, srv, true)

	checks := []func() error{
		func() error { return cmdSearch([]string{"x"}) },
		func() error { return cmdBatch([]string{"x"}) },
		func() error { return cmdProduct([]string{"99"}) },
		func() error { return cmdCategories(nil) },
		func() error { return cmdCategories([]string{"--id", "0301"}) },
		func() error { return cmdCart([]string{"get"}) },
		func() error { return cmdCart([]string{"add", "99", "1"}) },
		func() error { return cmdCart([]string{"clear"}) },
		func() error { return cmdCheckout([]string{"status"}) },
		func() error { return cmdCheckout([]string{"addresses"}) },
		func() error { return cmdCheckout([]string{"slots", "--address", "a"}) },
		func() error { return cmdWhoami(nil) },
	}
	for i, fn := range checks {
		_, err := capture(t, fn)
		if err == nil {
			t.Errorf("check %d: expected an error from the 500 server", i)
		}
	}
}

func TestOutputVariants(t *testing.T) {
	srv := apiServer(t)
	defer srv.Close()
	useServer(t, srv, true)

	for _, args := range [][]string{
		{"--json"}, {"--toon"},
	} {
		for _, fn := range []func([]string) error{
			func(a []string) error { return cmdProduct(append([]string{"23597"}, a...)) },
			func(a []string) error { return cmdBatch(append([]string{"llet"}, a...)) },
			func(a []string) error { return cmdTotal(append([]string{"23597", "2"}, a...)) },
			func(a []string) error { return cmdCart(append([]string{"get"}, a...)) },
			func(a []string) error { return cmdCheckout(append([]string{"status"}, a...)) },
			func(a []string) error { return cmdCheckout(append([]string{"addresses"}, a...)) },
			cmdWhoami,
		} {
			if _, err := capture(t, func() error { return fn(args) }); err != nil {
				t.Errorf("%v variant: %v", args, err)
			}
		}
	}
}

func TestEmptyResultPaths(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "carts/active"):
			w.Write([]byte(`{"items":[],"totals":{},"activeCheckoutGroup":{}}`))
		case strings.Contains(r.URL.Path, "/order/"):
			w.WriteHeader(http.StatusOK)
		default:
			w.Write([]byte(`{"productGroups":[]}`)) // no products
		}
	}))
	defer srv.Close()
	useServer(t, srv, true)

	if out, _ := capture(t, func() error { return cmdSearch([]string{"zzz"}) }); !strings.Contains(out, "sense resultats") {
		t.Errorf("empty search should say 'sense resultats': %q", out)
	}
	if out, _ := capture(t, func() error { return cmdCart([]string{"get"}) }); !strings.Contains(out, "buida") {
		t.Errorf("empty cart should say 'buida': %q", out)
	}
	if _, err := capture(t, func() error { return cmdCart([]string{"clear"}) }); err != nil {
		t.Errorf("clear empty cart: %v", err)
	}
}
