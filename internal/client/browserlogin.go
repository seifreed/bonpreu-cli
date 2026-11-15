//go:build browserlogin

package client

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// loginPollEvery / loginTimeout bound the wait for the user to finish logging in.
const (
	loginPollEvery = 2 * time.Second
	loginTimeout   = 5 * time.Minute
)

var (
	runBrowserActions = chromedp.Run
	loginNow          = time.Now
	loginSleep        = sleepCtx
	checkoutWait      = func(ctx context.Context) error { return waitForCheckout(ctx, os.Stdin) }
)

// LoginViaBrowser opens a visible Chrome window at loginURL, lets the user log in
// by hand (captcha / SSO and all), and captures the session cookie the moment it
// appears — no DevTools, no HAR. Compiled only with `-tags browserlogin`; needs
// Chrome installed at runtime.
func LoginViaBrowser(loginURL string) (Session, error) {
	u, err := url.Parse(loginURL)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || !isBonpreuHost(u.Hostname()) ||
		u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return Session{}, fmt.Errorf("browser login requires an HTTPS Bonpreu host")
	}

	// Tie everything to a signal-aware context: Ctrl-C cancels it, which tears the
	// chromedp browser down cleanly instead of leaking an orphaned Chrome (Go's
	// default signal exit skips the deferred cancels).
	baseCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.Flag("headless", false))
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(baseCtx, opts...)
	defer cancelAlloc()
	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	// Tell the user what to do *before* navigating: the Bonpreu SPA behind the AWS
	// WAF challenge can take a long time to fire `load`, and page.Navigate (unlike
	// chromedp.Navigate) returns as soon as navigation starts rather than blocking
	// on it — so the prompt and the window appear right away.
	fmt.Fprintln(os.Stderr, "→ obrint el navegador… inicia sessió a la finestra; capturaré la sessió automàticament")
	err = runBrowserActions(ctx, network.Enable(), chromedp.ActionFunc(func(ctx context.Context) error {
		_, _, errText, _, err := page.Navigate(loginURL).Do(ctx)
		if err != nil {
			return err
		}
		if errText != "" {
			return fmt.Errorf("navigate: %s", errText)
		}
		return nil
	}))
	if err != nil {
		return Session{}, fmt.Errorf("open browser (is Chrome installed?): %w", err)
	}
	deadline := loginNow().Add(loginTimeout)
	var lastReadErr error
	for {
		cookie, err := readBonpreuCookies(ctx, loginURL)
		if err == nil && CookieLooksAuthed(cookie) {
			return Session{Cookie: cookie}, nil
		}
		if err != nil {
			lastReadErr = err
		}
		if loginNow().After(deadline) {
			if lastReadErr != nil {
				return Session{}, fmt.Errorf("timed out waiting for login (%s) — the session cookie never appeared: %w", loginTimeout, lastReadErr)
			}
			return Session{}, fmt.Errorf("timed out waiting for login (%s) — the session cookie never appeared", loginTimeout)
		}
		if err := loginSleep(ctx, loginPollEvery); err != nil {
			return Session{}, fmt.Errorf("login aborted (Ctrl-C or browser closed) before the session appeared: %w", err)
		}
	}
}

// CheckoutInBrowser opens the official checkout with the saved session cookies.
// The user completes delivery, terms acceptance, and any bank authentication in
// Bonpreu's own UI; the CLI never submits or authorizes a payment itself.
func CheckoutInBrowser(baseURL, cookieHeader string) error {
	checkoutURL, err := checkoutPageURL(baseURL)
	if err != nil {
		return err
	}
	req := &http.Request{Header: http.Header{"Cookie": []string{cookieHeader}}}
	cookies := req.Cookies()
	if len(cookies) == 0 {
		return fmt.Errorf("stored Bonpreu session cookie could not be parsed")
	}
	markers := map[string]bool{"global_sid": false, "aws-waf-token": false}
	for _, cookie := range cookies {
		if _, required := markers[cookie.Name]; required && cookie.Value != "" {
			markers[cookie.Name] = true
		}
	}
	if !markers["global_sid"] || !markers["aws-waf-token"] {
		return fmt.Errorf("stored cookie does not contain an authenticated Bonpreu session")
	}

	baseCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.Flag("headless", false))
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(baseCtx, opts...)
	defer cancelAlloc()
	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	fmt.Fprintln(os.Stderr, "→ obrint el checkout oficial; completa y confirma la compra en Chrome y pulsa Enter para cerrar")
	actions := []chromedp.Action{network.Enable()}
	for _, cookie := range cookies {
		cookie := cookie
		actions = append(actions, chromedp.ActionFunc(func(ctx context.Context) error {
			return network.SetCookie(cookie.Name, cookie.Value).
				WithURL(checkoutURL).
				WithSecure(true).
				WithHTTPOnly(true).
				Do(ctx)
		}))
	}
	actions = append(actions, chromedp.ActionFunc(func(ctx context.Context) error {
		_, _, errText, _, err := page.Navigate(checkoutURL).Do(ctx)
		if err != nil {
			return err
		}
		if errText != "" {
			return fmt.Errorf("navigate: %s", errText)
		}
		return nil
	}))
	if err := runBrowserActions(ctx, actions...); err != nil {
		return fmt.Errorf("open checkout browser: %w", err)
	}
	if err := checkoutWait(baseCtx); err != nil {
		return fmt.Errorf("wait for checkout confirmation: %w", err)
	}
	return nil
}

func waitForCheckout(ctx context.Context, input io.Reader) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	readDone := make(chan error, 1)
	go func() {
		_, err := bufio.NewReader(input).ReadString('\n')
		readDone <- err
	}()
	select {
	case err := <-readDone:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func checkoutPageURL(baseURL string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme != "https" || !isBonpreuHost(u.Hostname()) {
		return "", fmt.Errorf("checkout browser requires an HTTPS Bonpreu host")
	}
	u.Path = "/checkout"
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

// readBonpreuCookies pulls the current cookies for the site and joins the
// Bonpreu-domain ones into a Cookie header (reusing buildCookieHeader).
func readBonpreuCookies(ctx context.Context, url string) (string, error) {
	var cks []*network.Cookie
	err := runBrowserActions(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		c, err := network.GetCookies().WithURLs([]string{url}).Do(ctx)
		cks = c
		return err
	}))
	if err != nil {
		return "", err
	}
	val := map[string]string{}
	var order []string
	for _, c := range cks {
		if c == nil || c.Name == "" || c.Value == "" || !isBonpreuCookieDomain(c.Domain) {
			continue
		}
		if _, ok := val[c.Name]; !ok {
			order = append(order, c.Name)
		}
		val[c.Name] = c.Value
	}
	pairs := make([]nameVal, 0, len(order))
	for _, n := range order {
		pairs = append(pairs, nameVal{n, val[n]})
	}
	return buildCookieHeader(pairs), nil
}

// sleepCtx waits d, returning early (with an error) if the browser context dies —
// so closing the window stops the loop instead of hanging until the timeout.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
