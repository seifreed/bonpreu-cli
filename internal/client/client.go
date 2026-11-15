// Package client is a thin, dependency-free Go client for the (unofficial)
// compraonline.bonpreuesclat.cat API. It speaks the same HTTP shapes the web app
// does and presents Chrome's TLS fingerprint (uTLS) so AWS WAF keeps it out of the
// JS-challenge path — the anonymous catalog reads work from any IP.
package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// BaseURL is the web/app shared host. Every API path lives under /api/… on it.
	BaseURL = "https://www.compraonline.bonpreuesclat.cat"
	// DefaultUA mirrors a current desktop Chrome so requests look like the web app.
	DefaultUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"
	// ClientName is the apollographql-client-name the SPA sends; mirrored for parity.
	ClientName = "ecom-web"
)

// Client is the API entrypoint. The zero value is not usable — use New.
type Client struct {
	HTTP      *http.Client
	BaseURL   string
	UserAgent string
	Lang      string // "ca" or "es"; sent via accept-language

	// AllowExternalCookieHost explicitly permits sending the session cookie to a
	// remote HTTPS host other than Bonpreu (for trusted staging environments).
	AllowExternalCookieHost bool

	// Region scopes prices/availability. Bonpreu serves the anonymous catalog
	// without one, so it is optional for reads; cart/checkout resolve it from the
	// session. Empty = let the server use its default region.
	RegionID string

	// Cookie carries a browser session (global_sid + aws-waf-token) for the
	// authenticated leg (cart/checkout) and to unlock GraphQL product detail.
	Cookie string

	// Logger receives structured diagnostics when provided. Nil disables logging;
	// the client never writes to process output directly.
	Logger *slog.Logger

	// transportErr records why the uTLS Chrome transport could not initialise (nil
	// when it did). New cannot log it — Logger is set by the caller afterwards — so it
	// is surfaced lazily on the first request.
	transportErr error
	warnOnce     sync.Once
}

// New returns a Client with web-app-like defaults (anonymous, Catalan). Its HTTP
// client presents Chrome's TLS (JA3) fingerprint via uTLS; if that fails to
// initialize it falls back to the stdlib transport.
func New() *Client {
	hc := &http.Client{Timeout: 30 * time.Second, CheckRedirect: checkRedirect}
	c := &Client{
		HTTP:      hc,
		BaseURL:   BaseURL,
		UserAgent: DefaultUA,
		Lang:      "ca",
	}
	if tr, err := newChromeTransport(); err == nil {
		hc.Transport = tr
	} else {
		c.transportErr = err
	}
	return c
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > 0 {
		previous := via[len(via)-1]
		if strings.EqualFold(previous.URL.Scheme, "https") && !strings.EqualFold(req.URL.Scheme, "https") {
			return errors.New("refusing HTTPS-to-HTTP redirect")
		}
		if previous.Header.Get("Cookie") != "" {
			return errors.New("refusing redirect carrying a session cookie")
		}
	}
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	return nil
}

// APIError carries a non-2xx response so callers (and agents) can branch on it.
// RetryAfter is the parsed Retry-After header on a 429/503 (>=0 when present).
type APIError struct {
	Status     int
	Body       string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	// Response bodies are controlled by the remote service and can contain echoed
	// credentials or personal data; keep them available via Body, never in logs.
	return fmt.Sprintf("bonpreu api: HTTP %d", e.Status)
}

// HTTPStatus reports the HTTP status carried by err when it is (or wraps) an
// *APIError. ok is false for any other error. Callers branch on a status (404,
// 401…) without reaching into the error type, and it unwraps wrapped errors.
func HTTPStatus(err error) (status int, ok bool) {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Status, true
	}
	return 0, false
}

// newReq builds an API request with the shared headers (accept, language,
// user-agent, origin/referer, cookie, and content-type for a JSON body) plus any
// extra headers the endpoint needs (e.g. the X-CSRF-TOKEN cart writes require).
// Pass extra=nil when none.
func (c *Client) newReq(method, url string, body any, extra map[string]string) (*http.Request, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("accept", "application/json")
	if c.Lang == "es" {
		req.Header.Set("accept-language", "es-ES,es;q=0.9,ca;q=0.8,en;q=0.7")
	} else {
		req.Header.Set("accept-language", "ca-ES,ca;q=0.9,es;q=0.8,en;q=0.7")
	}
	req.Header.Set("user-agent", c.UserAgent)
	req.Header.Set("referer", c.BaseURL+"/")
	req.Header.Set("origin", c.BaseURL)
	req.Header.Set("apollographql-client-name", ClientName)
	req.Header.Set("ecom-request-source", "web")
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	if c.Cookie != "" {
		if err := c.validateCookieTransport(c.Cookie, url); err != nil {
			return nil, err
		}
		req.Header.Set("cookie", c.Cookie)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	return req, nil
}

// Read-only GET/HEAD requests retry throttling (HTTP 429/503) up to maxRetries
// times. Mutations are never retried without an idempotency key.
const (
	maxRetries       = 3
	defaultRetryBase = 500 * time.Millisecond
	maxRetryWait     = 10 * time.Second
)

func isRateLimited(err error) bool {
	status, ok := HTTPStatus(err)
	return ok && (status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable)
}

// parseRetryAfter reads either Retry-After form; -1 when absent or invalid.
func parseRetryAfter(h string) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return -1
	}
	secs, err := strconv.ParseUint(h, 10, 64)
	if err == nil {
		if secs > uint64(maxRetryWait/time.Second) {
			return maxRetryWait
		}
		return time.Duration(secs) * time.Second
	}
	if errors.Is(err, strconv.ErrRange) {
		return maxRetryWait
	}
	if at, err := http.ParseTime(h); err == nil {
		wait := time.Until(at)
		if wait < 0 {
			return 0
		}
		return capWait(wait)
	}
	return -1
}

func retryWait(err error, backoff time.Duration) time.Duration {
	var ae *APIError
	if errors.As(err, &ae) && ae.RetryAfter >= 0 {
		return capWait(ae.RetryAfter)
	}
	return capWait(backoff + time.Duration(rand.Int63n(int64(250*time.Millisecond))))
}

func capWait(d time.Duration) time.Duration {
	if d > maxRetryWait {
		return maxRetryWait
	}
	if d < 0 {
		return 0
	}
	return d
}

// DoJSON issues a request, retrying transient throttling (429/503) so a burst of
// calls — pricing a basket, many searches during a shop — degrades gracefully
// instead of failing. Non-throttle errors return immediately.
func (c *Client) DoJSON(method, url string, body, out any) error {
	return c.doJSONHeaders(method, url, body, out, nil)
}

// doJSONHeaders is DoJSON with extra request headers (e.g. the X-CSRF-TOKEN and
// ecom-request-source the cart write endpoints require).
func (c *Client) doJSONHeaders(method, url string, body, out any, extra map[string]string) error {
	backoff := defaultRetryBase
	for attempt := 0; ; attempt++ {
		err := c.doOnce(method, url, body, out, extra)
		if (method != http.MethodGet && method != http.MethodHead) || !isRateLimited(err) || attempt >= maxRetries {
			return err
		}
		wait := retryWait(err, backoff)
		status, _ := HTTPStatus(err)
		if c.Logger != nil {
			c.Logger.Warn("HTTP request throttled; retry scheduled",
				"event_name", "http.retry", "component", "client", "http.request.method", method,
				"http.response.status_code", status, "retry.attempt", attempt+1,
				"retry.max", maxRetries, "duration_ms", wait.Milliseconds())
		}
		time.Sleep(wait)
		backoff *= 2
	}
}

// maxBodyBytes caps how much of any response body we read into memory.
const maxBodyBytes = 16 << 20

// do sends req, reads the (capped) response body, and turns a non-2xx status
// into an *APIError carrying that body and any Retry-After. The single place
// requests are executed and HTTP errors are mapped.
func (c *Client) do(req *http.Request) ([]byte, error) {
	c.warnOnce.Do(func() {
		if c.transportErr != nil {
			if c.Logger != nil {
				c.Logger.Warn("Chrome TLS transport unavailable; using standard transport",
					"event_name", "transport.fallback", "component", "client",
					"error_type", fmt.Sprintf("%T", c.transportErr))
			}
		}
	})
	if c.Logger != nil {
		c.Logger.Log(req.Context(), slog.LevelDebug-4, "HTTP request started",
			"event_name", "http.request.start", "component", "client", "http.request.method", req.Method)
	}
	started := time.Now()
	resp, err := c.HTTP.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			safeErr := *urlErr
			safeErr.URL = safeRequestURL(urlErr.URL)
			err = &safeErr
		}
		c.logHTTPResult(req, 0, 0, started, err)
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	closeErr := resp.Body.Close()
	bodyErr := errors.Join(readErr, closeErr)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if len(data) > maxBodyBytes {
			data = data[:maxBodyBytes]
		}
		// Keep the HTTP status actionable while preserving any body I/O failures.
		apiErr := &APIError{
			Status:     resp.StatusCode,
			Body:       string(data),
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		}
		if bodyErr != nil {
			err = errors.Join(apiErr, fmt.Errorf("read error response body: %w", bodyErr))
		} else {
			err = apiErr
		}
		c.logHTTPResult(req, resp.StatusCode, len(data), started, err)
		return nil, err
	}
	if len(data) > maxBodyBytes {
		err := fmt.Errorf("response body exceeds %d-byte limit", maxBodyBytes)
		err = errors.Join(err, bodyErr)
		c.logHTTPResult(req, resp.StatusCode, len(data), started, err)
		return nil, err
	}
	// A 2xx whose body read or close failed must not pass as success: the JSON
	// decoders would mostly catch truncation, but GetText has no decode step.
	if bodyErr != nil {
		c.logHTTPResult(req, resp.StatusCode, len(data), started, bodyErr)
		return nil, fmt.Errorf("response body read/close failed: %w", bodyErr)
	}
	c.logHTTPResult(req, resp.StatusCode, len(data), started, nil)
	return data, nil
}

func safeRequestURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "<redacted>"
	}
	return u.Scheme + "://" + u.Host
}

func (c *Client) logHTTPResult(req *http.Request, status, responseBytes int, started time.Time, err error) {
	if c.Logger == nil {
		return
	}
	attrs := []any{
		"event_name", "http.response", "component", "client",
		"http.request.method", req.Method, "duration_ms", time.Since(started).Milliseconds(),
		"response_bytes", responseBytes,
	}
	if status != 0 {
		attrs = append(attrs, "http.response.status_code", status)
	}
	if err != nil {
		attrs = append(attrs, "outcome", "error", "error_type", fmt.Sprintf("%T", err))
	} else {
		attrs = append(attrs, "outcome", "success")
	}
	c.Logger.Debug("HTTP request completed", attrs...)
}

// doOnce performs a single API request against BaseURL, decoding a 2xx JSON body
// into out (if non-nil). Non-2xx responses become *APIError.
func (c *Client) doOnce(method, url string, body, out any, extra map[string]string) error {
	req, err := c.newReq(method, url, body, extra)
	if err != nil {
		return err
	}
	data, err := c.do(req)
	if err != nil {
		return err
	}
	if out != nil {
		requestURL := safeRequestURL(url)
		if len(data) == 0 {
			return fmt.Errorf("decode response from %s: empty JSON body", requestURL)
		}
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode response from %s: %w", requestURL, err)
		}
	}
	return nil
}

// getRaw issues a GET and returns the response body as raw JSON (validated by
// the decoder). For endpoints whose shape is broad and forwarded verbatim to
// --json, or projected by the caller.
func (c *Client) getRaw(url string) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.DoJSON("GET", url, nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// GetText fetches a URL and returns the raw body (used to scrape SSR HTML for
// product detail, which the API only serves over WAF-gated GraphQL).
func (c *Client) GetText(url string) (string, error) {
	req, err := c.newReq("GET", url, nil, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("accept", "text/html,application/xhtml+xml")
	data, err := c.do(req)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// truncate caps s to n runes (with an ellipsis), never splitting a multibyte
// rune — error bodies are UTF-8 JSON/HTML and a byte cut would emit invalid UTF-8.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
