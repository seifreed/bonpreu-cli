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
	"math/rand"
	"net/http"
	"strconv"
	"strings"
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

	// Region scopes prices/availability. Bonpreu serves the anonymous catalog
	// without one, so it is optional for reads; cart/checkout resolve it from the
	// session. Empty = let the server use its default region.
	RegionID string

	// Cookie carries a browser session (global_sid + aws-waf-token) for the
	// authenticated leg (cart/checkout) and to unlock GraphQL product detail.
	Cookie string
}

// New returns a Client with web-app-like defaults (anonymous, Catalan). Its HTTP
// client presents Chrome's TLS (JA3) fingerprint via uTLS; if that fails to
// initialize it falls back to the stdlib transport.
func New() *Client {
	hc := &http.Client{Timeout: 30 * time.Second}
	if tr, err := newChromeTransport(); err == nil {
		hc.Transport = tr
	}
	return &Client{
		HTTP:      hc,
		BaseURL:   BaseURL,
		UserAgent: DefaultUA,
		Lang:      "ca",
	}
}

// APIError carries a non-2xx response so callers (and agents) can branch on it.
// RetryAfter is the parsed Retry-After header on a 429/503 (>=0 when present).
type APIError struct {
	Status     int
	Body       string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("bonpreu api: HTTP %d: %s", e.Status, truncate(e.Body, 300))
}

func (c *Client) newReq(method, url string, body any) (*http.Request, error) {
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
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	if c.Cookie != "" {
		req.Header.Set("cookie", c.Cookie)
	}
	return req, nil
}

// Automatic backoff on throttling (HTTP 429/503): retry up to maxRetries times,
// honouring Retry-After, else exponential backoff with jitter, each wait capped.
const (
	maxRetries       = 3
	defaultRetryBase = 500 * time.Millisecond
	maxRetryWait     = 10 * time.Second
)

func isRateLimited(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && (ae.Status == http.StatusTooManyRequests || ae.Status == http.StatusServiceUnavailable)
}

// parseRetryAfter reads the delta-seconds form of Retry-After; -1 when absent.
func parseRetryAfter(h string) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return -1
	}
	if secs, err := strconv.Atoi(h); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
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
	backoff := defaultRetryBase
	for attempt := 0; ; attempt++ {
		err := c.doOnce(method, url, body, out)
		if !isRateLimited(err) || attempt >= maxRetries {
			return err
		}
		time.Sleep(retryWait(err, backoff))
		backoff *= 2
	}
}

// doOnce performs a single API request against BaseURL, decoding a 2xx JSON body
// into out (if non-nil). Non-2xx responses become *APIError.
func (c *Client) doOnce(method, url string, body, out any) error {
	req, err := c.newReq(method, url, body)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{Status: resp.StatusCode, Body: string(data), RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode %s: %w", url, err)
		}
	}
	return nil
}

// GetText fetches a URL and returns the raw body (used to scrape SSR HTML for
// product detail, which the API only serves over WAF-gated GraphQL).
func (c *Client) GetText(url string) (string, error) {
	req, err := c.newReq("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("accept", "text/html,application/xhtml+xml")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &APIError{Status: resp.StatusCode, Body: string(data)}
	}
	return string(data), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
