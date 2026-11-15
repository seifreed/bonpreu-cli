//go:build browserlogin

package client

import (
	"context"
	"fmt"
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

// LoginViaBrowser opens a visible Chrome window at loginURL, lets the user log in
// by hand (captcha / SSO and all), and captures the session cookie the moment it
// appears — no DevTools, no HAR. Compiled only with `-tags browserlogin`; needs
// Chrome installed at runtime.
func LoginViaBrowser(loginURL string) (Session, error) {
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
	err := chromedp.Run(ctx, network.Enable(), chromedp.ActionFunc(func(ctx context.Context) error {
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
	deadline := time.Now().Add(loginTimeout)
	for {
		cookie, err := readBonpreuCookies(ctx, loginURL)
		if err == nil && CookieLooksAuthed(cookie) {
			return Session{Cookie: cookie}, nil
		}
		if time.Now().After(deadline) {
			return Session{}, fmt.Errorf("timed out waiting for login (%s) — the session cookie never appeared", loginTimeout)
		}
		if err := sleepCtx(ctx, loginPollEvery); err != nil {
			return Session{}, fmt.Errorf("login aborted (Ctrl-C or browser closed) before the session appeared")
		}
	}
}

// readBonpreuCookies pulls the current cookies for the site and joins the
// Bonpreu-domain ones into a Cookie header (reusing buildCookieHeader).
func readBonpreuCookies(ctx context.Context, url string) (string, error) {
	var cks []*network.Cookie
	err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
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
		if c == nil || c.Name == "" || c.Value == "" || !strings.Contains(c.Domain, cookieDomain) {
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
