package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/seifreed/bonpreu-cli/internal/client"
)

func TestGuardResultingLineRejectsUnpriceableWhenLimitSet(t *testing.T) {
	pd := &client.ProductDetail{}
	pd.Name = "Mystery item"
	pd.Price.Amount = "" // unparseable → priceCents fails

	if err := guardResultingLine(50, pd, 2); err == nil || !strings.Contains(err.Error(), "cannot enforce") {
		t.Fatalf("unpriceable line must fail closed when --max is set, got %v", err)
	}
}

func TestGuardResultingLineRejectsOverflowWhenLimitSet(t *testing.T) {
	pd := &client.ProductDetail{}
	pd.Price.Amount = "1.00"
	if err := guardResultingLine(50, pd, 1e300); err == nil || !strings.Contains(err.Error(), "cannot enforce") {
		t.Fatalf("unrepresentable line subtotal must fail closed, got %v", err)
	}
}

func TestGuardResultingLineUsesExactUnitPriceBeforeRounding(t *testing.T) {
	pd := &client.ProductDetail{}
	pd.Price.Amount = "0.334"
	if err := guardResultingLine(0.99, pd, 3); err == nil || !strings.Contains(err.Error(), "1.00€") {
		t.Fatalf("exact line value should exceed 0.99€ as 1.00€, got %v", err)
	}
	if err := guardResultingLine(1, pd, 3); err != nil {
		t.Fatalf("exact line value should fit 1.00€: %v", err)
	}
}

func TestGuardResultingLineSilentWithoutLimit(t *testing.T) {
	t.Setenv("BONPREU_MAX_EUR", "") // no limit from env
	privateTestConfigDir(t)         // no limit from a real config.toml

	pd := &client.ProductDetail{}
	pd.Name = "Mystery item"
	if err := guardResultingLine(0, pd, 2); err != nil { // no limit set
		t.Fatalf("no limit: %v", err)
	}
}

func TestCartLineTotalCents(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lines []client.CartLine
		want  int64
		fail  bool
	}{
		{
			name: "sums duplicate lines",
			lines: []client.CartLine{
				{ProductID: "p", TotalPrices: struct {
					FinalPrice client.Money `json:"finalPrice"`
				}{FinalPrice: client.Money{Amount: "1.25"}}},
				{ProductID: "p", TotalPrices: struct {
					FinalPrice client.Money `json:"finalPrice"`
				}{FinalPrice: client.Money{Amount: "2.00"}}},
			},
			want: 325,
		},
		{name: "missing product", lines: []client.CartLine{{ProductID: "other"}}, fail: true},
		{name: "missing total", lines: []client.CartLine{{ProductID: "p"}}, fail: true},
		{
			name: "overflow",
			lines: []client.CartLine{
				{ProductID: "p", TotalPrices: struct {
					FinalPrice client.Money `json:"finalPrice"`
				}{FinalPrice: client.Money{Amount: "92233720368547758.07"}}},
				{ProductID: "p", TotalPrices: struct {
					FinalPrice client.Money `json:"finalPrice"`
				}{FinalPrice: client.Money{Amount: "0.01"}}},
			},
			fail: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := cartLineTotalCents(tc.lines, "p")
			if tc.fail && err == nil {
				t.Fatalf("cartLineTotalCents = %d, nil; want error", got)
			}
			if !tc.fail && (err != nil || got != tc.want) {
				t.Fatalf("cartLineTotalCents = %d, %v; want %d", got, err, tc.want)
			}
		})
	}
}

func testCartClient(srv *httptest.Server) *client.Client {
	cl := client.New()
	cl.BaseURL = srv.URL
	cl.HTTP = srv.Client()
	return cl
}

func statefulCartServer(t *testing.T, initialQty float64) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	quantities := map[string]float64{"uuid-23597": initialQty}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/products/"):
			id := strings.TrimPrefix(r.URL.Path, "/products/")
			_, _ = fmt.Fprintf(w, `<html>window.__URQL_DATA__ = {"p":{"retailerProductId":%q,"productId":"uuid-%s","name":"Llet","price":{"amount":"1.69"}}}</html>`, id, id)
		case r.URL.Path == "/":
			_, _ = w.Write([]byte(`"csrf":{"token":"abcdef12-3456-7890-abcd-ef1234567890"}`))
		case r.URL.Path == "/api/cart/v1/carts/active":
			mu.Lock()
			items := make([]client.CartLine, 0, len(quantities))
			for productID, qty := range quantities {
				if qty > 0 {
					items = append(items, client.CartLine{ProductID: productID, Quantity: qty})
				}
			}
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(struct {
				Items []client.CartLine `json:"items"`
			}{Items: items})
		case r.URL.Path == "/api/cart/v1/carts/active/apply-quantity":
			var items []client.CartItem
			if err := json.NewDecoder(r.Body).Decode(&items); err != nil {
				http.Error(w, "invalid cart update", http.StatusBadRequest)
				return
			}
			mu.Lock()
			for _, item := range items {
				quantities[item.ProductID] += item.Quantity
				if quantities[item.ProductID] <= 0 {
					delete(quantities, item.ProductID)
				}
			}
			mu.Unlock()
			_, _ = w.Write([]byte(`{"items":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestCartAddDoesNotCheckoutOrPlaceOrder(t *testing.T) {
	var cartWrites, forbiddenRequests, cartReads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.ToLower(r.URL.Path)
		if strings.Contains(path, "checkout") || strings.Contains(path, "order") ||
			(r.Method == http.MethodPost && r.URL.Path != "/api/cart/v1/carts/active/apply-quantity") {
			forbiddenRequests.Add(1)
		}
		if r.Method == http.MethodPost {
			if r.URL.Path == "/api/cart/v1/carts/active/apply-quantity" {
				cartWrites.Add(1)
			}
		}
		switch r.URL.Path {
		case "/products/23597":
			_, _ = w.Write([]byte(`<html>window.__URQL_DATA__ = {"p":{"retailerProductId":"23597","productId":"uuid-23597","name":"Llet","price":{"amount":"1.69"}}}</html>`))
		case "/":
			_, _ = w.Write([]byte(`"csrf":{"token":"abcdef12-3456-7890-abcd-ef1234567890"}`))
		case "/api/cart/v1/carts/active":
			if cartReads.Add(1) == 1 {
				_, _ = w.Write([]byte(`{"items":[]}`))
			} else {
				_, _ = w.Write([]byte(`{"items":[{"productId":"uuid-23597","quantity":1,"totalPrices":{"finalPrice":{"amount":"1.69"}}}]}`))
			}
		case "/api/cart/v1/carts/active/apply-quantity":
			_, _ = w.Write([]byte(`{"items":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	useServer(t, srv, true)

	if _, err := capture(t, func() error {
		return cmdCart([]string{"add", "23597", "1", "--max", "50"})
	}); err != nil {
		t.Fatalf("cart add: %v", err)
	}
	if got := cartWrites.Load(); got != 1 {
		t.Fatalf("cart quantity writes = %d, want 1", got)
	}
	if got := forbiddenRequests.Load(); got != 0 {
		t.Fatalf("checkout/order requests or non-cart writes = %d, want 0", got)
	}
}

func TestCartAddSurfacesOutputFailureAfterCartWrite(t *testing.T) {
	var cartWrites, cartReads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/products/23597":
			_, _ = w.Write([]byte(`<html>window.__URQL_DATA__ = {"p":{"retailerProductId":"23597","productId":"uuid-23597","name":"Llet","price":{"amount":"1.69"}}}</html>`))
		case "/":
			_, _ = w.Write([]byte(`"csrf":{"token":"abcdef12-3456-7890-abcd-ef1234567890"}`))
		case "/api/cart/v1/carts/active":
			if cartReads.Add(1)%2 == 1 {
				_, _ = w.Write([]byte(`{"items":[]}`))
			} else {
				_, _ = w.Write([]byte(`{"items":[{"productId":"uuid-23597","quantity":1}]}`))
			}
		case "/api/cart/v1/carts/active/apply-quantity":
			cartWrites.Add(1)
			_, _ = w.Write([]byte(`{"items":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	useServer(t, srv, true)

	err := withFailingStdout(t, func() error {
		return cmdCart([]string{"add", "23597", "1"})
	})
	if err == nil || !strings.Contains(err.Error(), "inspect the cart before retrying") {
		t.Fatalf("cart add error = %v, want output failure with retry guidance", err)
	}
	if got := cartWrites.Load(); got != 1 {
		t.Fatalf("cart writes = %d, want one successful cart update", got)
	}
	err = withFailingStdout(t, func() error {
		return cmdCart([]string{"add", "23597", "1", "--json"})
	})
	if err == nil || !strings.Contains(err.Error(), "inspect the cart before retrying") {
		t.Fatalf("JSON cart add error = %v, want output failure with retry guidance", err)
	}
	if got := cartWrites.Load(); got != 2 {
		t.Fatalf("cart writes = %d, want two successful cart updates", got)
	}
}

func TestCartAddReportsServerQuantityLimit(t *testing.T) {
	var cartWrites atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/products/23597":
			_, _ = w.Write([]byte(`<html>window.__URQL_DATA__ = {"p":{"retailerProductId":"23597","productId":"uuid-23597","name":"Llet","price":{"amount":"1.69"}}}</html>`))
		case "/":
			_, _ = w.Write([]byte(`"csrf":{"token":"abcdef12-3456-7890-abcd-ef1234567890"}`))
		case "/api/cart/v1/carts/active":
			_, _ = w.Write([]byte(`{"items":[]}`))
		case "/api/cart/v1/carts/active/apply-quantity":
			cartWrites.Add(1)
			_, _ = w.Write([]byte(`{"limitedItems":[{"productId":"uuid-23597","reason":"EXCEEDED_MAX_QUANTITY_SINGLE","expectedAmount":2,"actualAmount":1}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	useServer(t, srv, true)

	out, err := capture(t, func() error {
		return cmdCart([]string{"add", "23597", "2"})
	})
	if err == nil || !strings.Contains(err.Error(), "EXCEEDED_MAX_QUANTITY_SINGLE") {
		t.Fatalf("cart add error = %v, want quantity-limit failure", err)
	}
	if strings.Contains(out, "ok — add") {
		t.Fatalf("cart add reported full success despite a server limit: %q", out)
	}
	if got := cartWrites.Load(); got != 1 {
		t.Fatalf("cart writes = %d, want one cart update", got)
	}
	out, err = capture(t, func() error {
		return cmdCart([]string{"add", "23597", "2", "--json"})
	})
	if err == nil || !strings.Contains(err.Error(), "EXCEEDED_MAX_QUANTITY_SINGLE") || !strings.Contains(out, `"limitedItems"`) {
		t.Fatalf("JSON cart add output=%q error=%v, want raw limited result and failure", out, err)
	}
	if got := cartWrites.Load(); got != 2 {
		t.Fatalf("cart writes = %d, want two cart updates", got)
	}
}

func TestCartUpdateLimitErrorHandlesMalformedResponse(t *testing.T) {
	for _, raw := range []string{`not-json`, `null`, `{}`, `{"other":true}`, `{"items":"bad"}`, `{"items":null}`, `{"limitedItems":{"productId":"uuid-23597"}}`} {
		err := cartUpdateLimitError(json.RawMessage(raw), "uuid-23597")
		if err == nil || !strings.Contains(err.Error(), "check the cart before retrying") {
			t.Errorf("malformed cart update %s error = %v, want read-back guidance", raw, err)
		}
	}
}

func TestCartUpdateLimitErrorRejectsUnidentifiableResultItems(t *testing.T) {
	for _, raw := range []string{
		`{"limitedItems":[{}]}`,
		`{"limitedItems":[{"productId":"  "}]}`,
		`{"limitedItems":null}`,
		`{"unavailableData":[{}]}`,
		`{"unavailableData":[{"productId":"  "}]}`,
		`{"unavailableData":null}`,
	} {
		t.Run(raw, func(t *testing.T) {
			err := cartUpdateLimitError(json.RawMessage(raw), "uuid-23597")
			if err == nil || !strings.Contains(err.Error(), "check the cart before retrying") {
				t.Fatalf("cart update result %s error = %v; want uncertain-result error", raw, err)
			}
		})
	}
}

func TestCartUpdateLimitErrorReportsUnavailableProduct(t *testing.T) {
	err := cartUpdateLimitError(json.RawMessage(`{"unavailableData":[{"productId":"uuid-23597","reason":"OUT_OF_STOCK"}]}`), "uuid-23597")
	if err == nil || !strings.Contains(err.Error(), "OUT_OF_STOCK") {
		t.Fatalf("unavailable cart update error = %v, want product availability failure", err)
	}
	if err := cartUpdateLimitError(json.RawMessage(`{"unavailableData":[{"productId":"other","reason":"OUT_OF_STOCK"}]}`), "uuid-23597"); err != nil {
		t.Fatalf("unavailable data for another product should not fail update: %v", err)
	}
}

func TestCartAddVerifiesServerAppliedQuantityWithoutBudget(t *testing.T) {
	var cartReads, cartWrites atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/products/23597":
			_, _ = w.Write([]byte(`<html>window.__URQL_DATA__ = {"p":{"retailerProductId":"23597","productId":"uuid-23597","name":"Llet","price":{"amount":"1.69"}}}</html>`))
		case "/":
			_, _ = w.Write([]byte(`"csrf":{"token":"abcdef12-3456-7890-abcd-ef1234567890"}`))
		case "/api/cart/v1/carts/active":
			cartReads.Add(1)
			_, _ = w.Write([]byte(`{"items":[]}`))
		case "/api/cart/v1/carts/active/apply-quantity":
			cartWrites.Add(1)
			_, _ = w.Write([]byte(`{"items":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	useServer(t, srv, true)

	out, err := capture(t, func() error { return cmdCart([]string{"add", "23597", "1"}) })
	if err == nil || !strings.Contains(err.Error(), "resulting quantity is 0") || strings.Contains(out, "ok — add") {
		t.Fatalf("cart add output=%q error=%v; want an unverified no-op error", out, err)
	}
	if got := cartReads.Load(); got != 2 {
		t.Fatalf("cart reads = %d, want preflight and post-write verification", got)
	}
	if got := cartWrites.Load(); got != 1 {
		t.Fatalf("cart writes = %d, want one cart-only mutation", got)
	}
}

func TestCartAddAcceptsExactFractionalReadback(t *testing.T) {
	var cartReads, cartWrites atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/products/23597":
			_, _ = w.Write([]byte(`<html>window.__URQL_DATA__ = {"p":{"retailerProductId":"23597","productId":"uuid-23597","name":"Llet","price":{"amount":"1.69"}}}</html>`))
		case "/":
			_, _ = w.Write([]byte(`"csrf":{"token":"abcdef12-3456-7890-abcd-ef1234567890"}`))
		case "/api/cart/v1/carts/active":
			if cartReads.Add(1) == 1 {
				_, _ = w.Write([]byte(`{"items":[{"productId":"uuid-23597","quantity":0.1}]}`))
			} else {
				_, _ = w.Write([]byte(`{"items":[{"productId":"uuid-23597","quantity":0.3}]}`))
			}
		case "/api/cart/v1/carts/active/apply-quantity":
			cartWrites.Add(1)
			_, _ = w.Write([]byte(`{"items":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	useServer(t, srv, true)

	out, err := capture(t, func() error { return cmdCart([]string{"add", "23597", "0.2"}) })
	if err != nil || !strings.Contains(out, "ok — add") {
		t.Fatalf("cart add output=%q error=%v; want the exact 0.3 quantity to verify", out, err)
	}
	if got := cartWrites.Load(); got != 1 {
		t.Fatalf("cart writes = %d, want one cart-only mutation", got)
	}
}

func TestCartAddRefusesUnrepresentableCurrentQuantityBeforeWrite(t *testing.T) {
	var writes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/products/23597":
			_, _ = w.Write([]byte(`<html>window.__URQL_DATA__ = {"p":{"retailerProductId":"23597","productId":"uuid-23597","name":"Llet","price":{"amount":"1.69"}}}</html>`))
		case "/":
			_, _ = w.Write([]byte(`"csrf":{"token":"abcdef12-3456-7890-abcd-ef1234567890"}`))
		case "/api/cart/v1/carts/active":
			_, _ = w.Write([]byte(`{"items":[{"productId":"uuid-23597","quantity":1e308},{"productId":"uuid-23597","quantity":1e308}]}`))
		case "/api/cart/v1/carts/active/apply-quantity":
			writes.Add(1)
			_, _ = w.Write([]byte(`{"items":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	useServer(t, srv, true)

	if _, err := capture(t, func() error { return cmdCart([]string{"add", "23597", "1"}) }); err == nil || !strings.Contains(err.Error(), "exceeds the supported range") {
		t.Fatalf("cart add with unrepresentable existing quantity error = %v", err)
	}
	if got := writes.Load(); got != 0 {
		t.Fatalf("cart writes = %d, want no write when preflight quantity cannot be represented", got)
	}
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
		if strings.Contains(r.URL.Path, "/cart/v1/carts/active") {
			_, _ = w.Write([]byte(`{"items":[]}`))
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

func TestCartWriteLimitAppliesToResultingLine(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "add", args: []string{"add", "23597", "1", "--max", "5"}},
		{name: "set", args: []string{"set", "23597", "3", "--max", "5"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var writes atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasPrefix(r.URL.Path, "/products/"):
					w.Write([]byte(`<html>window.__URQL_DATA__ = {"p":{"retailerProductId":"23597","productId":"uuid-23597","name":"Llet","price":{"amount":"1.69"}}}</html>`))
				case strings.Contains(r.URL.Path, "/cart/v1/carts/active"):
					w.Write([]byte(`{"items":[{"productId":"uuid-23597","quantity":2}]}`))
				case strings.Contains(r.URL.Path, "/apply-quantity"):
					writes.Add(1)
					w.Write([]byte(`{"items":[]}`))
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer srv.Close()
			useServer(t, srv, true)

			if _, err := capture(t, func() error { return cmdCart(tc.args) }); err == nil || !strings.Contains(err.Error(), "BUDGET EXCEEDED") {
				t.Fatalf("cart %s error = %v, want resulting line over limit", tc.name, err)
			}
			if got := writes.Load(); got != 0 {
				t.Fatalf("cart %s sent %d writes despite resulting line exceeding limit", tc.name, got)
			}
		})
	}
}

func TestCartAddSubcentPriceDoesNotWritePastMax(t *testing.T) {
	var writes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/products/"):
			_, _ = w.Write([]byte(`<html>window.__URQL_DATA__ = {"p":{"retailerProductId":"23597","productId":"uuid-23597","name":"Product","price":{"amount":"0.334"}}}</html>`))
		case r.URL.Path == "/api/cart/v1/carts/active":
			_, _ = w.Write([]byte(`{"items":[]}`))
		case strings.Contains(r.URL.Path, "/apply-quantity"):
			writes.Add(1)
			_, _ = w.Write([]byte(`{"items":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	useServer(t, srv, true)

	if _, err := capture(t, func() error {
		return cmdCart([]string{"add", "23597", "3", "--max", "0.99"})
	}); err == nil || !strings.Contains(err.Error(), "1.00€") {
		t.Fatalf("cart add error = %v, want preflight rejection at 1.00€", err)
	}
	if got := writes.Load(); got != 0 {
		t.Fatalf("cart add sent %d writes despite exceeding --max", got)
	}
}

func TestCartSetDecreaseStillEnforcesResultingLineLimit(t *testing.T) {
	var writes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/":
			w.Write([]byte(`"csrf":{"token":"abcdef12-3456-7890-abcd-ef1234567890"}`))
		case strings.HasPrefix(r.URL.Path, "/products/"):
			w.Write([]byte(`<html>window.__URQL_DATA__ = {"p":{"retailerProductId":"23597","productId":"uuid-23597","name":"Llet","price":{"amount":"1.69"}}}</html>`))
		case r.URL.Path == "/api/cart/v1/carts/active":
			w.Write([]byte(`{"items":[{"productId":"uuid-23597","quantity":2}]}`))
		case strings.Contains(r.URL.Path, "/apply-quantity"):
			writes.Add(1)
			w.Write([]byte(`{"items":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	useServer(t, srv, true)

	if _, err := capture(t, func() error { return cmdCart([]string{"set", "23597", "1", "--max", "1"}) }); err == nil || !strings.Contains(err.Error(), "BUDGET EXCEEDED") {
		t.Fatalf("cart set error = %v, want resulting line over limit", err)
	}
	if got := writes.Load(); got != 0 {
		t.Fatalf("cart set sent %d writes despite resulting line exceeding the limit", got)
	}
}

func TestCartSetRejectsInvalidLimitBeforeReadingCart(t *testing.T) {
	var cartReads, writes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/products/"):
			w.Write([]byte(`<html>window.__URQL_DATA__ = {"p":{"retailerProductId":"23597","productId":"uuid-23597","name":"Llet","price":{"amount":"1.69"}}}</html>`))
		case r.URL.Path == "/api/cart/v1/carts/active":
			cartReads.Add(1)
			w.Write([]byte(`{"items":[{"productId":"uuid-23597","quantity":2}]}`))
		case strings.Contains(r.URL.Path, "/apply-quantity"):
			writes.Add(1)
			w.Write([]byte(`{"items":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	useServer(t, srv, true)

	if _, err := capture(t, func() error { return cmdCart([]string{"set", "23597", "1", "--max", "NaN"}) }); err == nil || !strings.Contains(err.Error(), "invalid --max") {
		t.Fatalf("cart set error = %v, want invalid limit error", err)
	}
	if got := cartReads.Load(); got != 0 {
		t.Fatalf("cart set read the cart %d times despite an invalid limit", got)
	}
	if got := writes.Load(); got != 0 {
		t.Fatalf("cart set sent %d writes despite an invalid limit", got)
	}
}

func TestCartSetReportsConcurrentQuantityChange(t *testing.T) {
	var cartReads, writes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/products/"):
			w.Write([]byte(`<html>window.__URQL_DATA__ = {"p":{"retailerProductId":"23597","productId":"uuid-23597","name":"Llet","price":{"amount":"1.69"}}}</html>`))
		case r.URL.Path == "/api/cart/v1/carts/active":
			if cartReads.Add(1) == 1 {
				w.Write([]byte(`{"items":[{"productId":"uuid-23597","quantity":2}]}`))
			} else {
				// Simulate another client changing the cart between our read and delta POST.
				w.Write([]byte(`{"items":[{"productId":"uuid-23597","quantity":4}]}`))
			}
		case r.URL.Path == "/":
			w.Write([]byte(`"csrf":{"token":"abcdef12-3456-7890-abcd-ef1234567890"}`))
		case strings.Contains(r.URL.Path, "/apply-quantity"):
			writes.Add(1)
			w.Write([]byte(`{"items":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	useServer(t, srv, true)

	out, err := capture(t, func() error { return cmdCart([]string{"set", "23597", "3"}) })
	if err == nil || !strings.Contains(err.Error(), "resulting quantity is 4, not requested 3") {
		t.Fatalf("cart set output %q, error = %v; want concurrent change diagnostic", out, err)
	}
	if got := writes.Load(); got != 1 {
		t.Fatalf("cart set sent %d writes, want one delta write", got)
	}
}

func TestCartSetReportsReadbackErrorAfterAcceptedWrite(t *testing.T) {
	var cartReads, writes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/products/"):
			w.Write([]byte(`<html>window.__URQL_DATA__ = {"p":{"retailerProductId":"23597","productId":"uuid-23597","name":"Llet","price":{"amount":"1.69"}}}</html>`))
		case r.URL.Path == "/api/cart/v1/carts/active":
			if cartReads.Add(1) > 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Write([]byte(`{"items":[{"productId":"uuid-23597","quantity":2}]}`))
		case r.URL.Path == "/":
			w.Write([]byte(`"csrf":{"token":"abcdef12-3456-7890-abcd-ef1234567890"}`))
		case strings.Contains(r.URL.Path, "/apply-quantity"):
			writes.Add(1)
			w.Write([]byte(`{"items":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	useServer(t, srv, true)

	_, err := capture(t, func() error { return cmdCart([]string{"set", "23597", "3"}) })
	if err == nil || !strings.Contains(err.Error(), "accepted but the result could not be verified") || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("cart set error = %v; want accepted-write readback error", err)
	}
	if got := writes.Load(); got != 1 {
		t.Fatalf("cart set sent %d writes, want one", got)
	}
}

func TestCartAddMaxVerifiesResultAfterAcceptedWrite(t *testing.T) {
	for _, tc := range []struct {
		name             string
		readback500      bool
		overflowQuantity bool
		want             string
	}{
		{name: "concurrent addition exceeds limit", want: "accepted but the resulting line exceeds --max"},
		{name: "readback fails", readback500: true, want: "accepted but the result could not be verified"},
		{name: "result cannot be priced", overflowQuantity: true, want: "accepted but the spending limit could not be checked after the write"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cartReads, writes atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasPrefix(r.URL.Path, "/products/"):
					w.Write([]byte(`<html>window.__URQL_DATA__ = {"p":{"retailerProductId":"23597","productId":"uuid-23597","name":"Llet","price":{"amount":"1.69"}}}</html>`))
				case r.URL.Path == "/api/cart/v1/carts/active":
					switch {
					case cartReads.Add(1) == 1:
						w.Write([]byte(`{"items":[{"productId":"uuid-23597","quantity":1}]}`))
					case tc.readback500:
						w.WriteHeader(http.StatusInternalServerError)
					case tc.overflowQuantity:
						w.Write([]byte(`{"items":[{"productId":"uuid-23597","quantity":1e300}]}`))
					default:
						// A concurrent writer adds lines after the preflight read.
						w.Write([]byte(`{"items":[{"productId":"uuid-23597","quantity":4,"totalPrices":{"finalPrice":{"amount":"6.76"}}}]}`))
					}
				case r.URL.Path == "/":
					w.Write([]byte(`"csrf":{"token":"abcdef12-3456-7890-abcd-ef1234567890"}`))
				case strings.Contains(r.URL.Path, "/apply-quantity"):
					writes.Add(1)
					w.Write([]byte(`{"items":[]}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			useServer(t, srv, true)

			out, err := capture(t, func() error { return cmdCart([]string{"add", "23597", "1", "--max", "5"}) })
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("cart add output %q, error = %v; want %q", out, err, tc.want)
			}
			if strings.Contains(out, "ok — add") {
				t.Fatalf("cart add reported success: %q", out)
			}
			if got := writes.Load(); got != 1 {
				t.Fatalf("cart add sent %d writes, want one accepted write", got)
			}
		})
	}
}

func TestCartWriteMaxChecksActualCartLinePrice(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		before string
		after  string
		want   string
	}{
		{
			name:   "add",
			args:   []string{"add", "23597", "1", "--max", "5"},
			before: `{"items":[]}`,
			after:  `{"items":[{"productId":"uuid-23597","quantity":1,"totalPrices":{"finalPrice":{"amount":"6.00"}}}]}`,
			want:   "resulting line exceeds --max",
		},
		{
			name:   "set",
			args:   []string{"set", "23597", "2", "--max", "5"},
			before: `{"items":[{"productId":"uuid-23597","quantity":1,"totalPrices":{"finalPrice":{"amount":"1.69"}}}]}`,
			after:  `{"items":[{"productId":"uuid-23597","quantity":2,"totalPrices":{"finalPrice":{"amount":"12.00"}}}]}`,
			want:   "resulting line exceeds --max",
		},
		{
			name:   "missing actual cart price",
			args:   []string{"add", "23597", "1", "--max", "5"},
			before: `{"items":[]}`,
			after:  `{"items":[{"productId":"uuid-23597","quantity":1}]}`,
			want:   "spending limit could not be checked after the write",
		},
		{
			name:   "accepted write missing from readback",
			args:   []string{"add", "23597", "1", "--max", "5"},
			before: `{"items":[]}`,
			after:  `{"items":[]}`,
			want:   "below the requested increase",
		},
		{
			name:   "unrepresentable combined quantity",
			args:   []string{"add", "23597", "1", "--max", "5"},
			before: `{"items":[]}`,
			after:  `{"items":[{"productId":"uuid-23597","quantity":1e308},{"productId":"uuid-23597","quantity":1e308}]}`,
			want:   "result could not be verified",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cartReads, writes atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasPrefix(r.URL.Path, "/products/"):
					_, _ = w.Write([]byte(`<html>window.__URQL_DATA__ = {"p":{"retailerProductId":"23597","productId":"uuid-23597","name":"Llet","price":{"amount":"1.69"}}}</html>`))
				case r.URL.Path == "/api/cart/v1/carts/active":
					if cartReads.Add(1) == 1 {
						_, _ = w.Write([]byte(tc.before))
					} else {
						_, _ = w.Write([]byte(tc.after))
					}
				case r.URL.Path == "/":
					_, _ = w.Write([]byte(`"csrf":{"token":"abcdef12-3456-7890-abcd-ef1234567890"}`))
				case strings.Contains(r.URL.Path, "/apply-quantity"):
					writes.Add(1)
					_, _ = w.Write([]byte(`{"items":[]}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			useServer(t, srv, true)

			out, err := capture(t, func() error { return cmdCart(tc.args) })
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("cart %s output %q error = %v; want actual line price to exceed cap", tc.name, out, err)
			}
			if strings.Contains(out, "ok —") {
				t.Fatalf("cart %s reported success above the cap: %q", tc.name, out)
			}
			if got := writes.Load(); got != 1 {
				t.Fatalf("cart %s writes = %d; want one accepted write", tc.name, got)
			}
		})
	}
}

func TestCartAddMaxSumsDuplicateExistingLines(t *testing.T) {
	var writes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/products/"):
			w.Write([]byte(`<html>window.__URQL_DATA__ = {"p":{"retailerProductId":"23597","productId":"uuid-23597","name":"Llet","price":{"amount":"1.69"}}}</html>`))
		case strings.Contains(r.URL.Path, "/apply-quantity"):
			writes.Add(1)
			w.Write([]byte(`{"items":[]}`))
		case r.URL.Path == "/api/cart/v1/carts/active":
			w.Write([]byte(`{"items":[{"productId":"uuid-23597","quantity":1},{"productId":"uuid-23597","quantity":2}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	useServer(t, srv, true)

	if _, err := capture(t, func() error { return cmdCart([]string{"add", "23597", "1", "--max", "5"}) }); err == nil || !strings.Contains(err.Error(), "BUDGET EXCEEDED") {
		t.Fatalf("cart add with duplicate existing lines error = %v, want budget exceeded", err)
	}
	if got := writes.Load(); got != 0 {
		t.Fatalf("cart add sent %d writes despite duplicate lines exceeding the limit", got)
	}
}

func TestCartAddLimitFailsClosedWhenCurrentCartCannotBeRead(t *testing.T) {
	var writes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/products/"):
			w.Write([]byte(`<html>window.__URQL_DATA__ = {"p":{"retailerProductId":"23597","productId":"uuid-23597","name":"Llet","price":{"amount":"1.69"}}}</html>`))
		case strings.Contains(r.URL.Path, "/cart/v1/carts/active"):
			w.WriteHeader(http.StatusInternalServerError)
		case strings.Contains(r.URL.Path, "/apply-quantity"):
			writes.Add(1)
			w.Write([]byte(`{"items":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	useServer(t, srv, true)

	if _, err := capture(t, func() error { return cmdCart([]string{"add", "23597", "1", "--max", "5"}) }); err == nil || !strings.Contains(err.Error(), "read current cart quantity") {
		t.Fatalf("cart add error = %v, want current-cart read error", err)
	}
	if got := writes.Load(); got != 0 {
		t.Fatalf("cart add sent %d writes when the line limit could not be checked", got)
	}
}

func TestCartAddDoesNotWriteWithoutProductUUID(t *testing.T) {
	var writes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			writes.Add(1)
			w.Write([]byte(`{"items":[]}`))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/products/") {
			w.Write([]byte(`<html>window.__URQL_DATA__ = {"p":{"retailerProductId":"23597","name":"Mystery item","price":{"amount":"1.69"}}}</html>`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	useServer(t, srv, true)

	if _, err := capture(t, func() error { return cmdCart([]string{"add", "23597", "1"}) }); err == nil || !strings.Contains(err.Error(), "no cart product ID") {
		t.Fatalf("cart add without product UUID error = %v", err)
	}
	if got := writes.Load(); got != 0 {
		t.Fatalf("cart add sent %d write requests without a product UUID, want none", got)
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

func TestCurrentQtySumsDuplicateProductLines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"items":[{"productId":"p1","quantity":2},{"productId":"p1","quantity":3}]}`))
	}))
	defer srv.Close()
	q, err := currentQty(testCartClient(srv), "p1")
	if err != nil || q != 5 {
		t.Fatalf("currentQty for repeated lines = %v, %v; want 5, nil", q, err)
	}
}

func TestDecimalQtySumRejectsInvalidAndOverflow(t *testing.T) {
	if _, err := decimalQtySum([]client.CartLine{{ProductID: "p", Quantity: math.NaN()}}, "p"); err == nil {
		t.Fatal("decimal quantity sum should reject an invalid number")
	}
	if _, err := decimalQtySum([]client.CartLine{{ProductID: "p", Quantity: math.MaxFloat64}, {ProductID: "p", Quantity: math.MaxFloat64}}, "p"); err == nil {
		t.Fatal("decimal quantity sum should reject an unrepresentable total")
	}
}

func TestCartSetFractionalNoopDoesNotWrite(t *testing.T) {
	var cartWrites atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/products/23597":
			_, _ = w.Write([]byte(`<html>window.__URQL_DATA__ = {"p":{"retailerProductId":"23597","productId":"uuid-23597","name":"Llet","price":{"amount":"1.69"}}}</html>`))
		case "/api/cart/v1/carts/active":
			_, _ = w.Write([]byte(`{"items":[{"productId":"uuid-23597","quantity":0.1},{"productId":"uuid-23597","quantity":0.2}]}`))
		case "/":
			_, _ = w.Write([]byte(`"csrf":{"token":"abcdef12-3456-7890-abcd-ef1234567890"}`))
		case "/api/cart/v1/carts/active/apply-quantity":
			cartWrites.Add(1)
			_, _ = w.Write([]byte(`{"limitedItems":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	useServer(t, srv, true)

	if _, err := capture(t, func() error { return cmdCart([]string{"set", "23597", "0.3"}) }); err != nil {
		t.Fatalf("setting the existing fractional quantity: %v", err)
	}
	if got := cartWrites.Load(); got != 0 {
		t.Fatalf("cart writes = %d, want no write for an unchanged quantity", got)
	}
}

func TestCartAddJSON(t *testing.T) {
	srv := statefulCartServer(t, 0)
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

func TestCartClearReportsServerQuantityLimit(t *testing.T) {
	var cartWrites atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`"csrf":{"token":"abcdef12-3456-7890-abcd-ef1234567890"}`))
		case "/api/cart/v1/carts/active":
			_, _ = w.Write([]byte(`{"items":[{"productId":"uuid-23597","quantity":1}]}`))
		case "/api/cart/v1/carts/active/apply-quantity":
			cartWrites.Add(1)
			_, _ = w.Write([]byte(`{"limitedItems":[{"productId":"uuid-23597","reason":"QUANTITY_UNAVAILABLE"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	useServer(t, srv, true)

	out, err := capture(t, func() error { return cmdCart([]string{"clear"}) })
	if err == nil || !strings.Contains(err.Error(), "QUANTITY_UNAVAILABLE") {
		t.Fatalf("cart clear error = %v, want server quantity-limit failure", err)
	}
	if strings.Contains(out, "ok — removed") {
		t.Fatalf("cart clear reported success despite a server limit: %q", out)
	}
	if got := cartWrites.Load(); got != 1 {
		t.Fatalf("cart writes = %d, want one cart update", got)
	}
}

func TestCartClearVerifiesResultAfterAcceptedWrite(t *testing.T) {
	for _, tc := range []struct {
		name        string
		readback500 bool
		want        string
	}{
		{name: "concurrent line remains", want: "accepted but 1 cart line(s) remain"},
		{name: "readback fails", readback500: true, want: "accepted but the result could not be verified"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cartReads, writes atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/":
					w.Write([]byte(`"csrf":{"token":"abcdef12-3456-7890-abcd-ef1234567890"}`))
				case "/api/cart/v1/carts/active":
					if cartReads.Add(1) > 1 && tc.readback500 {
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					w.Write([]byte(`{"items":[{"productId":"p1","quantity":1}]}`))
				case "/api/cart/v1/carts/active/apply-quantity":
					writes.Add(1)
					w.Write([]byte(`{"items":[]}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			useServer(t, srv, true)

			out, err := capture(t, func() error { return cmdCart([]string{"clear"}) })
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("cart clear output %q, error = %v; want %q", out, err, tc.want)
			}
			if strings.Contains(out, "ok — removed") {
				t.Fatalf("cart clear reported success: %q", out)
			}
			if got := writes.Load(); got != 1 {
				t.Fatalf("cart clear sent %d writes, want one accepted write", got)
			}
		})
	}
}

func TestCartClearOutputFailureAfterCartWrite(t *testing.T) {
	srv := statefulCartServer(t, 1)
	defer srv.Close()
	useServer(t, srv, true)

	err := withFailingStdout(t, func() error { return cmdCart([]string{"clear"}) })
	if err == nil || !strings.Contains(err.Error(), "inspect the cart before retrying") {
		t.Fatalf("cart clear output error = %v, want read-back guidance after write", err)
	}
}

func TestCartClearJSONOutput(t *testing.T) {
	srv := statefulCartServer(t, 1)
	defer srv.Close()
	useServer(t, srv, true)

	out, err := capture(t, func() error { return cmdCart([]string{"clear", "--json"}) })
	if err != nil || !strings.Contains(out, `"items": []`) {
		t.Fatalf("cart clear --json output = %q, error = %v", out, err)
	}
}

func TestCartClearJSONOutputFailureAfterWrite(t *testing.T) {
	srv := statefulCartServer(t, 1)
	defer srv.Close()
	useServer(t, srv, true)

	err := withFailingStdout(t, func() error { return cmdCart([]string{"clear", "--json"}) })
	if err == nil || !strings.Contains(err.Error(), "inspect the cart before retrying") {
		t.Fatalf("cart clear JSON output error = %v, want read-back guidance after write", err)
	}
}

func TestCartClearEmptyJSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/cart/v1/carts/active" {
			_, _ = w.Write([]byte(`{"items":[],"totals":{"itemPriceAfterPromos":{"amount":"0.00"}}}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	useServer(t, srv, true)

	out, err := capture(t, func() error { return cmdCart([]string{"clear", "--json"}) })
	if err != nil || !strings.Contains(out, `"items": []`) {
		t.Fatalf("empty cart clear --json output = %q, error = %v", out, err)
	}
}

// `set` for a product not currently in the cart exercises currentQty's
// not-found return.
func TestCartSetNewProduct(t *testing.T) {
	srv := statefulCartServer(t, 0)
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
		case r.URL.Path == "/api/cart/v1/carts/active":
			w.Write([]byte(`{"items":[]}`))
		case r.URL.Path == "/api/cart/v1/carts/active/apply-quantity":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	useServer(t, srv, true)

	if _, err := capture(t, func() error { return cmdCart([]string{"add", "23597"}) }); err == nil {
		t.Error("add with one arg should error (need id + qty)")
	}
	if _, err := capture(t, func() error { return cmdCart([]string{"add", "23597", "1", "unexpected"}) }); err == nil {
		t.Error("add with extra args should error instead of mutating the cart")
	}
	if _, err := capture(t, func() error { return cmdCart([]string{"add", "23597", "1"}) }); err == nil {
		t.Error("add should surface the apply-quantity error")
	}
}

func TestCartMutationsRejectSurplusArgumentsBeforeRequests(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	useServer(t, srv, true)

	for _, args := range [][]string{
		{"clear", "23597"},
		{"add", "23597", "1", "unexpected"},
		{"set", "23597", "2", "unexpected"},
	} {
		requests.Store(0)
		_, err := capture(t, func() error { return cmdCart(args) })
		if err == nil || !strings.Contains(err.Error(), "usage:") {
			t.Errorf("cart %v error = %v; want usage error", args, err)
		}
		if got := requests.Load(); got != 0 {
			t.Errorf("cart %v made %d requests before rejecting malformed args", args, got)
		}
	}
}

func TestCartAddRejectsInvalidBudgetBeforeWrite(t *testing.T) {
	var writes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/products/") {
			w.Write([]byte(`<html>window.__URQL_DATA__ = {"p":{"retailerProductId":"23597","productId":"uuid-23597","name":"Llet","price":{"amount":"1.69"}}}</html>`))
			return
		}
		if strings.Contains(r.URL.Path, "/apply-quantity") {
			writes.Add(1)
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	useServer(t, srv, true)
	t.Setenv("BONPREU_MAX_EUR", "not-a-number")
	if _, err := capture(t, func() error { return cmdCart([]string{"add", "23597", "1"}) }); err == nil || !strings.Contains(err.Error(), "invalid BONPREU_MAX_EUR") {
		t.Errorf("cart add error = %v, want invalid spending-limit error", err)
	}
	if got := writes.Load(); got != 0 {
		t.Fatalf("cart add sent %d writes despite an invalid spending limit", got)
	}
}

func TestCartWriteBranches(t *testing.T) {
	srv := statefulCartServer(t, 2)
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
	t.Setenv("BONPREU_MAX_EUR", "0.10")
	if _, err := capture(t, func() error { return cmdCart([]string{"add", "23597", "1", "--max", "0"}) }); err != nil {
		t.Fatalf("explicit --max 0 should disable the inherited cap: %v", err)
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
			dir := privateTestConfigDir(t)
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
