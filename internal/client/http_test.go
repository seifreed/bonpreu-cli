package client

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// testClient returns a Client pointed at srv with a plain HTTP client (the uTLS
// transport is only for real HTTPS; these tests exercise request/response logic).
func testClient(srv *httptest.Server) *Client {
	c := New()
	c.BaseURL = srv.URL
	c.HTTP = srv.Client()
	return c
}

func TestSearchDecodesProducts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/product-pages/search") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("q") != "llet" {
			t.Errorf("q = %q", r.URL.Query().Get("q"))
		}
		w.Write([]byte(`{"productGroups":[{"type":"ungrouped","decoratedProducts":[
			{"retailerProductId":"23597","name":"Llet","price":{"amount":"5.76","currency":"EUR"}}]}]}`))
	}))
	defer srv.Close()

	pp, err := testClient(srv).Search("llet", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	got := pp.Products()
	if len(got) != 1 || got[0].RetailerProductID != "23597" || got[0].Price.Amount != "5.76" {
		t.Fatalf("decoded wrong: %+v", got)
	}
}

func TestAPIErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(`{"error":"nope"}`))
	}))
	defer srv.Close()

	err := testClient(srv).DoJSON("GET", srv.URL+"/x", nil, nil)
	ae, ok := err.(*APIError)
	if !ok || ae.Status != 404 {
		t.Fatalf("want APIError 404, got %v", err)
	}
}

func TestRetryOnRateLimit(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		if hits == 1 {
			w.Header().Set("Retry-After", "0") // retry immediately, no slow sleep
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	if err := testClient(srv).DoJSON("GET", srv.URL+"/x", nil, nil); err != nil {
		t.Fatalf("expected success after retry: %v", err)
	}
	if hits != 2 {
		t.Fatalf("expected 1 retry (2 hits), got %d", hits)
	}
}

func TestCheckAuth(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   bool
	}{{200, true}, {401, false}, {403, false}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
		}))
		got, err := testClient(srv).CheckAuth()
		srv.Close()
		if tc.status == 200 && err != nil {
			t.Errorf("status 200: unexpected err %v", err)
		}
		if got != tc.want {
			t.Errorf("status %d: CheckAuth = %v, want %v", tc.status, got, tc.want)
		}
	}
}

func TestApplyQuantityFetchesCSRF(t *testing.T) {
	var gotCSRF, gotSource string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/":
			w.Write([]byte(`<html>...,"session":{"csrf":{"token":"abcdef12-3456-7890-abcd-ef1234567890"}},...</html>`))
		case strings.Contains(r.URL.Path, "/apply-quantity"):
			gotCSRF = r.Header.Get("X-CSRF-TOKEN")
			gotSource = r.Header.Get("ecom-request-source")
			b, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read body: %v", err)
			}
			gotBody = b
			w.Write([]byte(`{"cartId":"c1"}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	_, err := testClient(srv).ApplyQuantity([]CartItem{{ProductID: "p1", Quantity: 2}})
	if err != nil {
		t.Fatalf("ApplyQuantity: %v", err)
	}
	if gotCSRF != "abcdef12-3456-7890-abcd-ef1234567890" {
		t.Errorf("CSRF header = %q", gotCSRF)
	}
	if gotSource != "web" {
		t.Errorf("ecom-request-source = %q", gotSource)
	}
	if !strings.Contains(string(gotBody), `"productId":"p1"`) || !strings.Contains(string(gotBody), `"quantity":2`) {
		t.Errorf("body = %s", gotBody)
	}
}

func TestGetCartProjection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"cartId":"c1","items":[{"productId":"p1","quantity":3,
			"finalPrice":{"amount":"6.00"},"totalPrices":{"finalPrice":{"amount":"18.00"}}}],
			"totals":{"itemPriceAfterPromos":{"amount":"18.00","currency":"EUR"}}}`))
	}))
	defer srv.Close()

	cart, err := testClient(srv).GetCart()
	if err != nil {
		t.Fatalf("GetCart: %v", err)
	}
	if len(cart.Lines) != 1 || cart.Lines[0].Quantity != 3 || cart.Total.Amount != "18.00" {
		t.Fatalf("projection wrong: %+v", cart)
	}
}

func TestHTTPStatus(t *testing.T) {
	plain := &APIError{Status: 404}
	if got, ok := HTTPStatus(plain); !ok || got != 404 {
		t.Errorf("plain: got (%d,%v), want (404,true)", got, ok)
	}
	wrapped := fmt.Errorf("resolve product 99: %w", &APIError{Status: 401})
	if got, ok := HTTPStatus(wrapped); !ok || got != 401 {
		t.Errorf("wrapped: got (%d,%v), want (401,true)", got, ok)
	}
	if got, ok := HTTPStatus(errors.New("boom")); ok || got != 0 {
		t.Errorf("non-api: got (%d,%v), want (0,false)", got, ok)
	}
}

func TestTruncateRuneSafe(t *testing.T) {
	// 10 'é' = 20 bytes; truncate to 5 must yield 5 whole runes + ellipsis,
	// never a split byte.
	out := truncate(strings.Repeat("é", 10), 5)
	if !utf8.ValidString(out) {
		t.Fatalf("not valid UTF-8: %q", out)
	}
	if want := strings.Repeat("é", 5) + "…"; out != want {
		t.Errorf("truncate = %q, want %q", out, want)
	}
	if s := "short"; truncate(s, 300) != s {
		t.Errorf("short string altered: %q", truncate(s, 300))
	}
}

func TestRetryLogsThrottle(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		if hits == 1 {
			w.Header().Set("Retry-After", "0") // retry immediately, no slow sleep
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	var logs []string
	cl := testClient(srv)
	cl.Logf = func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }

	if err := cl.DoJSON("GET", srv.URL+"/x", nil, nil); err != nil {
		t.Fatalf("DoJSON: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected exactly one throttle log, got %d: %v", len(logs), logs)
	}
	if !strings.Contains(logs[0], "throttled") || !strings.Contains(logs[0], "503") {
		t.Errorf("log missing throttle/status: %q", logs[0])
	}
}

func TestNoLogWhenNotThrottled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	var logs []string
	cl := testClient(srv)
	cl.Logf = func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }
	if err := cl.DoJSON("GET", srv.URL+"/x", nil, nil); err != nil {
		t.Fatalf("DoJSON: %v", err)
	}
	if len(logs) != 0 {
		t.Errorf("expected no logs on success, got %v", logs)
	}
}

func TestTransportFallbackLogsOnce(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	var logs []string
	cl := testClient(srv)
	cl.transportErr = errors.New("uTLS boom") // simulate the Chrome transport failing to init
	cl.Logf = func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }

	for i := 0; i < 3; i++ {
		if err := cl.DoJSON("GET", srv.URL+"/x", nil, nil); err != nil {
			t.Fatalf("DoJSON: %v", err)
		}
	}
	if len(logs) != 1 {
		t.Fatalf("transport fallback should log exactly once across requests, got %d: %v", len(logs), logs)
	}
	if !strings.Contains(logs[0], "stdlib transport") || !strings.Contains(logs[0], "uTLS boom") {
		t.Errorf("log missing detail: %q", logs[0])
	}
}

func TestCartProjectionMismatchLogs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"items":"not-an-array"}`)) // valid JSON, wrong type for items
	}))
	defer srv.Close()

	var logs []string
	cl := testClient(srv)
	cl.Logf = func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }

	cart, err := cl.GetCart()
	if err != nil {
		t.Fatalf("GetCart should stay best-effort, got err: %v", err)
	}
	if len(cart.Lines) != 0 {
		t.Errorf("expected empty projection, got %+v", cart.Lines)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "did not match") {
		t.Fatalf("expected one shape-mismatch log, got %v", logs)
	}
}

// Every method that fronts an HTTP call must propagate a transport/5xx error.
func TestMethodErrorBranches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	cl := testClient(srv)

	if _, err := cl.Search("x", 0); err == nil {
		t.Error("Search")
	}
	if _, err := cl.CategoryProducts("0301", 0); err == nil {
		t.Error("CategoryProducts")
	}
	if _, err := cl.Categories(1); err == nil {
		t.Error("Categories")
	}
	if _, _, _, err := cl.CheckoutStatus(); err == nil {
		t.Error("CheckoutStatus")
	}
	if _, err := cl.Addresses(); err == nil {
		t.Error("Addresses")
	}
	if _, err := cl.Slots("a"); err == nil {
		t.Error("Slots")
	}
	if _, err := cl.GetCart(); err == nil {
		t.Error("GetCart")
	}
	if _, err := cl.Product("x"); err == nil {
		t.Error("Product")
	}
	if _, err := cl.ApplyQuantity([]CartItem{{ProductID: "p", Quantity: 1}}); err == nil {
		t.Error("ApplyQuantity (csrf fetch fails)")
	}
	if ok, err := cl.CheckAuth(); ok || err == nil {
		t.Error("CheckAuth on 500 → (false, err)")
	}
}

// newReq sets es accept-language, the cookie, and content-type on a body.
func TestRequestHeaderBranches(t *testing.T) {
	var lang, cookie, ct string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang = r.Header.Get("accept-language")
		cookie = r.Header.Get("cookie")
		ct = r.Header.Get("content-type")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	cl := testClient(srv)
	cl.Lang = "es"
	cl.Cookie = "global_sid=a"

	var out any
	if err := cl.DoJSON("POST", srv.URL+"/x", map[string]string{"k": "v"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(lang, "es") {
		t.Errorf("accept-language = %q, want es-first", lang)
	}
	if cookie != "global_sid=a" {
		t.Errorf("cookie = %q", cookie)
	}
	if ct != "application/json" {
		t.Errorf("content-type = %q (body should set it)", ct)
	}
}

// A 429 without Retry-After exercises the exponential-backoff+jitter wait path.
func TestRetryBackoffJitterPath(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		if hits == 1 {
			w.WriteHeader(http.StatusTooManyRequests) // no Retry-After → backoff path
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	if err := testClient(srv).DoJSON("GET", srv.URL+"/x", nil, nil); err != nil {
		t.Fatalf("expected success after backoff retry: %v", err)
	}
}

func TestGetTextNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if _, err := testClient(srv).GetText(srv.URL + "/x"); err == nil {
		t.Error("expected error on 500")
	}
}

func TestCapWaitAndRetryAfter(t *testing.T) {
	if capWait(-1) != 0 {
		t.Error("negative wait should clamp to 0")
	}
	if capWait(time.Hour) != maxRetryWait {
		t.Error("huge wait should clamp to maxRetryWait")
	}
	if parseRetryAfter("Wed, 21 Oct 2099 07:28:00 GMT") != -1 {
		t.Error("HTTP-date Retry-After is unsupported → -1")
	}
	if parseRetryAfter("5") != 5*time.Second {
		t.Error("delta-seconds Retry-After")
	}
}

func TestRequestBuildError(t *testing.T) {
	cl := New()
	// A channel cannot be JSON-marshalled → newReq fails before any network I/O.
	if err := cl.DoJSON("POST", "http://example.invalid/x", make(chan int), nil); err == nil {
		t.Error("unmarshalable body should error during request build")
	}
}

func TestDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("notjson"))
	}))
	defer srv.Close()
	var out struct {
		A int `json:"a"`
	}
	if err := testClient(srv).DoJSON("GET", srv.URL+"/x", nil, &out); err == nil {
		t.Error("invalid JSON should produce a decode error")
	}
}

func TestTruncateFewRunesManyBytes(t *testing.T) {
	// 6 bytes but only 3 runes; n=5 → the rune count fits, returns unchanged.
	if got := truncate("ééé", 5); got != "ééé" {
		t.Errorf("truncate = %q, want unchanged", got)
	}
}

func TestGetTextBadURL(t *testing.T) {
	if _, err := New().GetText("://bad"); err == nil {
		t.Error("malformed URL should error at request build")
	}
}

func TestDoTransportError(t *testing.T) {
	// Port 0 is not connectable → HTTP.Do fails, exercising do()'s error branch.
	if err := New().DoJSON("GET", "http://127.0.0.1:0/x", nil, nil); err == nil {
		t.Error("a connection failure should surface")
	}
}
