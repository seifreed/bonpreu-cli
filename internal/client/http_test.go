package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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
	if strings.Contains(err.Error(), `"error"`) {
		t.Fatalf("error string leaked response body: %v", err)
	}
}

func TestAPIErrorBodyRemainsCapped(t *testing.T) {
	body := strings.Repeat("x", maxBodyBytes+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()
	var ae *APIError
	if err := testClient(srv).DoJSON("GET", srv.URL+"/oversized", nil, nil); !errors.As(err, &ae) {
		t.Fatalf("DoJSON error = %v; want APIError", err)
	}
	if len(ae.Body) != maxBodyBytes {
		t.Fatalf("APIError body length = %d; want capped at %d", len(ae.Body), maxBodyBytes)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type brokenBody struct{}

func (brokenBody) Read([]byte) (int, error) { return 0, errors.New("stream broke") }
func (brokenBody) Close() error             { return nil }

type errorBody struct{ readErr, closeErr error }

func (b errorBody) Read([]byte) (int, error) { return 0, b.readErr }
func (b errorBody) Close() error             { return b.closeErr }

func TestDoReturnsSuccessfulResponseReadError(t *testing.T) {
	cl := New()
	cl.HTTP = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: brokenBody{}, Header: make(http.Header)}, nil
	})}
	req, err := http.NewRequest(http.MethodGet, "https://example.invalid/data?token=private-value", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cl.do(req); err == nil || !strings.Contains(err.Error(), "response body") {
		t.Fatalf("successful response read error = %v", err)
	} else if strings.Contains(err.Error(), "private-value") {
		t.Fatalf("response read error exposed URL query: %v", err)
	}
}

func TestDoPreservesErrorResponseReadAndCloseErrors(t *testing.T) {
	readErr := errors.New("response stream failed")
	closeErr := errors.New("response close failed")
	cl := New()
	cl.HTTP = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadGateway, Body: errorBody{readErr, closeErr}, Header: make(http.Header)}, nil
	})}
	req, err := http.NewRequest(http.MethodGet, "https://example.invalid/api", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = cl.do(req)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadGateway {
		t.Fatalf("Do error = %v, want APIError 502", err)
	}
	if !errors.Is(err, readErr) || !errors.Is(err, closeErr) {
		t.Fatalf("Do error = %v, want both body read and close causes", err)
	}
}

func TestDoRedactsURLFromTransportError(t *testing.T) {
	cl := New()
	cl.HTTP = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial failed")
	})}
	err := cl.DoJSON("GET", "https://private-value@example.invalid/search?token=private-value", nil, nil)
	if err == nil {
		t.Fatal("DoJSON error = nil; want transport error")
	}
	if strings.Contains(err.Error(), "private-value") {
		t.Fatalf("transport error exposed URL query: %v", err)
	}
}

func TestDoRedactsURLFromRequestConstructionError(t *testing.T) {
	err := New().DoJSON("GET", "https://private-value@example.invalid/%zz?token=private-value", nil, nil)
	if err == nil {
		t.Fatal("DoJSON error = nil; want malformed URL error")
	}
	if strings.Contains(err.Error(), "private-value") {
		t.Fatalf("request construction error exposed URL credentials: %v", err)
	}
}

func TestRedactURLErrorPreservesNonURLErrors(t *testing.T) {
	cause := errors.New("request setup failed")
	if got := redactURLError(cause); !errors.Is(got, cause) {
		t.Fatalf("redactURLError(%v) = %v; want original error", cause, got)
	}
}

func TestSafeRequestURLRedactsInvalidAndRelativeURLs(t *testing.T) {
	for _, raw := range []string{"https://[invalid", "/relative/path"} {
		if got := safeRequestURL(raw); got != "<redacted>" {
			t.Errorf("safeRequestURL(%q) = %q, want <redacted>", raw, got)
		}
	}
}

func TestHTTPDebugEventHasSafeRequestContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "private response payload")
	}))
	defer srv.Close()

	var logs bytes.Buffer
	cl := testClient(srv)
	cl.Logger = slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/search?term=private-query", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Cookie", "session=private-cookie")
	if _, err := cl.do(req); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"event_name":"http.response"`, `"http.request.method":"GET"`, `"http.response.status_code":200`, `"duration_ms":`, `"response_bytes":`, `"outcome":"success"`} {
		if !strings.Contains(logs.String(), field) {
			t.Errorf("HTTP event missing %s: %q", field, logs.String())
		}
	}
	for _, secret := range []string{"private-query", "private-cookie", "private response payload", srv.URL} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("HTTP event leaked %q: %q", secret, logs.String())
		}
	}
}

func TestHTTPTraceStartEventOmitsRequestData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()
	var logs bytes.Buffer
	cl := testClient(srv)
	cl.Logger = slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug - 4}))
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/search?term=private-query", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Cookie", "session=private-cookie")
	if _, err := cl.do(req); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), `"event_name":"http.request.start"`) || !strings.Contains(logs.String(), `"http.request.method":"GET"`) {
		t.Fatalf("trace start event missing: %q", logs.String())
	}
	for _, secret := range []string{"private-query", "private-cookie", "private response", srv.URL} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("trace event leaked %q: %q", secret, logs.String())
		}
	}
}

func TestHTTPDebugEventRedactsTransportError(t *testing.T) {
	var logs bytes.Buffer
	cl := New()
	cl.Logger = slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cl.HTTP = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("private transport detail")
	})}
	req, err := http.NewRequest(http.MethodGet, "https://example.invalid/path?secret=value", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cl.do(req); err == nil {
		t.Fatal("expected transport failure")
	}
	for _, field := range []string{`"event_name":"http.response"`, `"outcome":"error"`, `"error_type":"*url.Error"`} {
		if !strings.Contains(logs.String(), field) {
			t.Errorf("HTTP error event missing %s: %q", field, logs.String())
		}
	}
	for _, secret := range []string{"private transport detail", "secret=value", "example.invalid"} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("HTTP error event leaked %q: %q", secret, logs.String())
		}
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

func TestCheckAuthWithCookieUsesCandidateWithoutChangingClient(t *testing.T) {
	var gotCookie string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	cl := testClient(srv)
	cl.Cookie = "cached=old"
	got, err := cl.CheckAuthWithCookie("candidate=new")
	if err != nil || !got {
		t.Fatalf("CheckAuthWithCookie = %v, %v; want true, nil", got, err)
	}
	if gotCookie != "candidate=new" {
		t.Errorf("verification cookie = %q, want candidate cookie", gotCookie)
	}
	if cl.Cookie != "cached=old" {
		t.Errorf("candidate verification changed cached cookie to %q", cl.Cookie)
	}
}

func TestCheckAuthWithCookieRejectsRemoteHTTP(t *testing.T) {
	cl := &Client{BaseURL: "http://compraonline.bonpreuesclat.cat"}
	if _, err := cl.CheckAuthWithCookie("global_sid=secret"); err == nil || !strings.Contains(err.Error(), "non-HTTPS") {
		t.Fatalf("CheckAuthWithCookie error = %v, want non-HTTPS rejection", err)
	}
}

func TestNewReqRejectsRemoteHTTPCookie(t *testing.T) {
	cl := &Client{BaseURL: "http://compraonline.bonpreuesclat.cat", Cookie: "global_sid=secret"}
	if _, err := cl.newReq(http.MethodGet, cl.BaseURL+"/api", nil, nil); err == nil || !strings.Contains(err.Error(), "non-HTTPS") {
		t.Fatalf("newReq error = %v, want non-HTTPS rejection", err)
	}
}

func TestNewReqRejectsForeignHTTPSCookieByDefault(t *testing.T) {
	cl := &Client{BaseURL: "https://evil.example", Cookie: "global_sid=secret"}
	if _, err := cl.newReq(http.MethodGet, cl.BaseURL+"/api", nil, nil); err == nil || !strings.Contains(err.Error(), "non-Bonpreu") {
		t.Fatalf("newReq error = %v, want foreign-host rejection", err)
	}
}

func TestNewReqValidatesActualCookieTarget(t *testing.T) {
	cl := &Client{BaseURL: BaseURL, Cookie: "global_sid=secret"}
	if _, err := cl.newReq(http.MethodGet, "https://evil.example/api", nil, nil); err == nil || !strings.Contains(err.Error(), "non-Bonpreu") {
		t.Fatalf("newReq error = %v, want foreign request target rejection", err)
	}
}

func TestNewReqAllowsExplicitTrustedHTTPSHost(t *testing.T) {
	cl := &Client{BaseURL: "https://staging.example", Cookie: "global_sid=secret", AllowExternalCookieHost: true}
	req, err := cl.newReq(http.MethodGet, cl.BaseURL+"/api", nil, nil)
	if err != nil {
		t.Fatalf("newReq with explicit staging opt-in: %v", err)
	}
	if got := req.Header.Get("Cookie"); got != "global_sid=secret" {
		t.Fatalf("request Cookie = %q, want explicitly allowed session", got)
	}
}

func TestCheckAuthWithCookieRejectsForeignHTTPSByDefault(t *testing.T) {
	cl := &Client{BaseURL: "https://evil.example"}
	if _, err := cl.CheckAuthWithCookie("global_sid=secret"); err == nil || !strings.Contains(err.Error(), "non-Bonpreu") {
		t.Fatalf("CheckAuthWithCookie error = %v, want foreign-host rejection", err)
	}
}

func TestClientDoesNotForwardCookieOnHTTPSDowngradeRedirect(t *testing.T) {
	var destinationRequests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		destinationRequests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer destination.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", destination.URL)
		w.WriteHeader(http.StatusFound)
	}))
	defer source.Close()

	cl := New()
	cl.BaseURL = source.URL
	cl.HTTP.Transport = source.Client().Transport
	cl.Cookie = "global_sid=secret"
	if _, err := cl.GetText(source.URL); err == nil || !strings.Contains(err.Error(), "HTTPS-to-HTTP") {
		t.Fatalf("HTTPS downgrade error = %v", err)
	}
	if got := destinationRequests.Load(); got != 0 {
		t.Fatalf("HTTP downgrade destination received %d requests, want none", got)
	}
}

func TestCheckRedirectAllowsSafeRedirectAndEnforcesLimit(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://example.test/path", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkRedirect(req, []*http.Request{req}); err != nil {
		t.Fatalf("same-scheme redirect error = %v", err)
	}
	authenticated, err := http.NewRequest(http.MethodGet, "https://example.test/path", nil)
	if err != nil {
		t.Fatal(err)
	}
	authenticated.Header.Set("Cookie", "session=secret")
	redirect, err := http.NewRequest(http.MethodGet, "https://example.test/next", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkRedirect(redirect, []*http.Request{authenticated}); err == nil || !strings.Contains(err.Error(), "session cookie") {
		t.Fatalf("cookie-bearing redirect error = %v", err)
	}
	via := make([]*http.Request, 10)
	for i := range via {
		via[i] = req
	}
	if err := checkRedirect(req, via); err == nil || !strings.Contains(err.Error(), "10 redirects") {
		t.Fatalf("redirect limit error = %v", err)
	}
}

func TestValidateCookieTransport(t *testing.T) {
	for _, tc := range []struct {
		name, targetURL, cookie string
		allowExternal           bool
		wantErr                 bool
	}{
		{name: "empty cookie"},
		{name: "bad url", targetURL: "://bad", cookie: "secret", wantErr: true},
		{name: "bonpreu https", targetURL: "https://www.compraonline.bonpreuesclat.cat", cookie: "secret"},
		{name: "uppercase https", targetURL: "HTTPS://www.compraonline.bonpreuesclat.cat", cookie: "secret"},
		{name: "foreign https", targetURL: "https://example.test", cookie: "secret", wantErr: true},
		{name: "trusted foreign https", targetURL: "https://example.test", cookie: "secret", allowExternal: true},
		{name: "localhost", targetURL: "http://localhost:8080", cookie: "secret"},
		{name: "loopback ip", targetURL: "http://127.0.0.1:8080", cookie: "secret"},
		{name: "remote http", targetURL: "http://example.test", cookie: "secret", allowExternal: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cl := &Client{AllowExternalCookieHost: tc.allowExternal}
			err := cl.validateCookieTransport(tc.cookie, tc.targetURL)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateCookieTransport = %v, wantErr %v", err, tc.wantErr)
			}
		})
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

	var logs bytes.Buffer
	cl := testClient(srv)
	cl.Logger = testLogger(&logs)

	if err := cl.DoJSON("GET", srv.URL+"/x", nil, nil); err != nil {
		t.Fatalf("DoJSON: %v", err)
	}
	if strings.Count(logs.String(), "event_name=http.retry") != 1 {
		t.Fatalf("expected exactly one throttle event, got %q", logs.String())
	}
	if !strings.Contains(logs.String(), "retry scheduled") || !strings.Contains(logs.String(), "http.response.status_code=503") {
		t.Errorf("event missing throttle/status: %q", logs.String())
	}
}

func TestNoLogWhenNotThrottled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	var logs bytes.Buffer
	cl := testClient(srv)
	cl.Logger = testLogger(&logs)
	if err := cl.DoJSON("GET", srv.URL+"/x", nil, nil); err != nil {
		t.Fatalf("DoJSON: %v", err)
	}
	if logs.Len() != 0 {
		t.Errorf("expected no logs on success, got %q", logs.String())
	}
}

func TestTransportFallbackLogsOnce(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	var logs bytes.Buffer
	cl := testClient(srv)
	cl.transportErr = errors.New("uTLS boom") // simulate the Chrome transport failing to init
	cl.Logger = testLogger(&logs)

	for i := 0; i < 3; i++ {
		if err := cl.DoJSON("GET", srv.URL+"/x", nil, nil); err != nil {
			t.Fatalf("DoJSON: %v", err)
		}
	}
	if strings.Count(logs.String(), "event_name=transport.fallback") != 1 {
		t.Fatalf("transport fallback should log exactly once, got %q", logs.String())
	}
	if !strings.Contains(logs.String(), "standard transport") || !strings.Contains(logs.String(), "error_type=") || strings.Contains(logs.String(), "uTLS boom") {
		t.Errorf("event should identify fallback without exposing error text: %q", logs.String())
	}
}

func TestCartProjectionMismatchReturnsDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"items":"not-an-array"}`)) // valid JSON, wrong type for items
	}))
	defer srv.Close()

	cl := testClient(srv)
	cart, err := cl.GetCart()
	var shapeErr *json.UnmarshalTypeError
	if !errors.As(err, &shapeErr) {
		t.Fatalf("GetCart error = %v, want wrapped JSON type error", err)
	}
	if cart != nil {
		t.Fatalf("GetCart returned a partial cart on decode error: %+v", cart)
	}
}

func TestGetCartRejectsMissingOrNullItems(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"missing items", `{}`, "missing or null items"},
		{"null items", `{"items":null}`, "missing or null items"},
		{"missing product ID", `{"items":[{}]}`, "invalid item at index 0"},
		{"missing quantity", `{"items":[{"productId":"p1"}]}`, "invalid item at index 0"},
		{"zero quantity", `{"items":[{"productId":"p1","quantity":0}]}`, "invalid item at index 0"},
		{"negative quantity", `{"items":[{"productId":"p1","quantity":-1}]}`, "invalid item at index 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			cart, err := testClient(srv).GetCart()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("GetCart(%s) error = %v; want %q", tc.body, err, tc.want)
			}
			if cart != nil {
				t.Fatalf("GetCart(%s) returned a cart after schema failure: %+v", tc.body, cart)
			}
		})
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

func TestNewReqRejectsUnsupportedLanguage(t *testing.T) {
	cl := &Client{BaseURL: BaseURL, Lang: "fr"}
	if _, err := cl.newReq(http.MethodGet, cl.BaseURL+"/api", nil, nil); err == nil || !strings.Contains(err.Error(), `unsupported language "fr"`) {
		t.Fatalf("newReq error = %v, want unsupported-language validation", err)
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

func TestGetTextRejectsBodyOverLimit(t *testing.T) {
	body := strings.Repeat("x", maxBodyBytes+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()
	if got, err := testClient(srv).GetText(srv.URL + "/oversized"); err == nil || got != "" {
		t.Fatalf("GetText returned %d bytes, error %v; want size-limit error and no partial body", len(got), err)
	}
}

func TestCapWaitAndRetryAfter(t *testing.T) {
	if capWait(-1) != 0 {
		t.Error("negative wait should clamp to 0")
	}
	if capWait(time.Hour) != maxRetryWait {
		t.Error("huge wait should clamp to maxRetryWait")
	}
	if got := parseRetryAfter("Wed, 21 Oct 2015 07:28:00 GMT"); got != 0 {
		t.Errorf("past HTTP-date Retry-After = %v, want immediate retry", got)
	}
	future := time.Now().Add(2 * time.Second).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(future); got < 0 || got > 2*time.Second {
		t.Errorf("future HTTP-date Retry-After = %v, want between 0 and 2s", got)
	}
	if parseRetryAfter("not-a-date") != -1 {
		t.Error("invalid Retry-After should be treated as absent")
	}
	if parseRetryAfter("5") != 5*time.Second {
		t.Error("delta-seconds Retry-After")
	}
	if parseRetryAfter("9999999999999999999") != maxRetryWait {
		t.Error("oversized delta-seconds Retry-After should be capped")
	}
	if parseRetryAfter("999999999999999999999999999999999999") != maxRetryWait {
		t.Error("delta-seconds beyond uint64 should still be capped, not treated as absent")
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
	err := testClient(srv).DoJSON("GET", srv.URL+"/x?token=private-value", nil, &out)
	if err == nil {
		t.Error("invalid JSON should produce a decode error")
	} else if strings.Contains(err.Error(), "private-value") {
		t.Fatalf("decode error exposed URL query: %v", err)
	}
}

func TestDecodeEmptyJSONResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	var out struct {
		A int `json:"a"`
	}
	if err := testClient(srv).DoJSON("GET", srv.URL+"/x", nil, &out); err == nil {
		t.Fatal("an empty 2xx response should not decode as JSON success")
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
