package client

import (
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestApplyQuantityEmptyItemsFetchesCart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"items":[]}`))
	}))
	defer srv.Close()
	if _, err := testClient(srv).ApplyQuantity(nil); err != nil {
		t.Fatalf("empty ApplyQuantity should fetch the cart: %v", err)
	}
}

func TestApplyQuantityRejectsInvalidItemsBeforeNetwork(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	cl := testClient(srv)
	for _, tc := range []struct {
		name string
		item CartItem
	}{
		{name: "empty ID", item: CartItem{Quantity: 1}},
		{name: "zero delta", item: CartItem{ProductID: "p1"}},
		{name: "NaN delta", item: CartItem{ProductID: "p1", Quantity: math.NaN()}},
		{name: "infinite delta", item: CartItem{ProductID: "p1", Quantity: math.Inf(1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := cl.ApplyQuantity([]CartItem{tc.item}); err == nil {
				t.Fatal("ApplyQuantity should reject invalid item")
			}
		})
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("invalid items made %d network requests, want none", got)
	}
}

func TestCSRFTokenMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<html>no token</html>`))
	}))
	defer srv.Close()
	if _, err := testClient(srv).csrfToken(); err == nil {
		t.Error("expected error when CSRF token absent")
	}
}

func TestApplyQuantityPostError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" { // csrf fetch succeeds
			w.Write([]byte(`"csrf":{"token":"abcdef12-3456-7890-abcd-ef1234567890"}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError) // apply-quantity fails
	}))
	defer srv.Close()
	if _, err := testClient(srv).ApplyQuantity([]CartItem{{ProductID: "p", Quantity: 1}}); err == nil {
		t.Error("apply-quantity 500 should error")
	}
}

func TestApplyQuantity503IsNotRetried(t *testing.T) {
	var postCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			_, _ = w.Write([]byte(`"csrf":{"token":"abcdef12-3456-7890-abcd-ef1234567890"}`))
			return
		}
		postCount++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	if _, err := testClient(srv).ApplyQuantity([]CartItem{{ProductID: "p", Quantity: 1}}); err == nil || !strings.Contains(err.Error(), "inspect the cart before retrying") {
		t.Fatal("a 503 from cart mutation must be returned as an ambiguous outcome")
	}
	if postCount != 1 {
		t.Fatalf("cart mutation sent %d times after 503; want one attempt", postCount)
	}
}
