//go:build browserlogin

package client

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

type browserLoginExecutor struct {
	cookies           []*network.Cookie
	setCookies        []*network.SetCookieParams
	navigateError     error
	navigateErrorText string
	setCookieError    error
	cookiesError      error
	navigationURL     string
	cookieURLs        []string
}

type blockingCheckoutReader struct {
	started chan struct{}
	release chan struct{}
}

func (r *blockingCheckoutReader) Read([]byte) (int, error) {
	close(r.started)
	<-r.release
	return 0, io.EOF
}

func (e *browserLoginExecutor) Execute(_ context.Context, method string, params, result any) error {
	switch method {
	case network.CommandEnable:
		return nil
	case network.CommandSetCookie:
		if e.setCookieError != nil {
			return e.setCookieError
		}
		e.setCookies = append(e.setCookies, params.(*network.SetCookieParams))
		return nil
	case page.CommandNavigate:
		e.navigationURL = params.(*page.NavigateParams).URL
		if e.navigateError != nil {
			return e.navigateError
		}
		result.(*page.NavigateReturns).ErrorText = e.navigateErrorText
		return nil
	case network.CommandGetCookies:
		if e.cookiesError != nil {
			return e.cookiesError
		}
		e.cookieURLs = params.(*network.GetCookiesParams).URLs
		result.(*network.GetCookiesReturns).Cookies = e.cookies
		return nil
	default:
		return errors.New("unexpected CDP method: " + method)
	}
}

func useBrowserLoginFakes(t *testing.T, executor cdp.Executor) {
	t.Helper()
	oldRun, oldNow, oldSleep, oldCheckoutWait := runBrowserActions, loginNow, loginSleep, checkoutWait
	runBrowserActions = func(ctx context.Context, actions ...chromedp.Action) error {
		ctx = cdp.WithExecutor(ctx, executor)
		for _, action := range actions {
			if err := action.Do(ctx); err != nil {
				return err
			}
		}
		return nil
	}
	loginNow = time.Now
	loginSleep = sleepCtx
	t.Cleanup(func() {
		runBrowserActions, loginNow, loginSleep, checkoutWait = oldRun, oldNow, oldSleep, oldCheckoutWait
	})
}

func TestLoginViaBrowserCapturesBonpreuCookies(t *testing.T) {
	executor := &browserLoginExecutor{cookies: []*network.Cookie{
		nil,
		{Name: "", Value: "ignored", Domain: ".bonpreuesclat.cat"},
		{Name: "empty", Domain: ".bonpreuesclat.cat"},
		{Name: "unrelated", Value: "ignored", Domain: ".other.example"},
		{Name: "global_sid", Value: "old", Domain: ".bonpreuesclat.cat"},
		{Name: "aws-waf-token", Value: "waf", Domain: "www.bonpreuesclat.cat"},
		{Name: "global_sid", Value: "session", Domain: ".bonpreuesclat.cat"},
		{Name: "global_sid", Value: "spoof", Domain: "evilbonpreuesclat.cat"},
	}}
	useBrowserLoginFakes(t, executor)
	session, err := LoginViaBrowser("https://www.bonpreuesclat.cat/")
	if err != nil {
		t.Fatal(err)
	}
	if session.Cookie != "aws-waf-token=waf; global_sid=session" {
		t.Fatalf("captured cookie = %q", session.Cookie)
	}
	if executor.navigationURL != "https://www.bonpreuesclat.cat/" || len(executor.cookieURLs) != 1 || executor.cookieURLs[0] != executor.navigationURL {
		t.Fatalf("browser URLs: navigate=%q cookies=%v", executor.navigationURL, executor.cookieURLs)
	}
}

func TestLoginViaBrowserReturnsPromptWriteErrorBeforeLaunching(t *testing.T) {
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeEnd.Close(); err != nil {
		t.Fatal(err)
	}
	oldStderr, oldRun := os.Stderr, runBrowserActions
	os.Stderr = writeEnd
	var runCount int
	runBrowserActions = func(context.Context, ...chromedp.Action) error {
		runCount++
		return nil
	}
	t.Cleanup(func() {
		os.Stderr = oldStderr
		runBrowserActions = oldRun
		_ = readEnd.Close()
		_ = writeEnd.Close()
	})

	_, err = LoginViaBrowser("https://www.bonpreuesclat.cat/")
	if err == nil || !strings.Contains(err.Error(), "write browser login prompt") {
		t.Fatalf("LoginViaBrowser error = %v; want prompt write error", err)
	}
	if runCount != 0 {
		t.Fatalf("browser launched %d times after the prompt failed; want none", runCount)
	}
}

func TestLoginViaBrowserRejectsUntrustedURLsBeforeLaunching(t *testing.T) {
	for _, loginURL := range []string{
		"http://www.bonpreuesclat.cat/",
		"https://attacker.example/",
		"https://user@www.bonpreuesclat.cat/",
		"https://www.bonpreuesclat.cat:8443/",
	} {
		t.Run(loginURL, func(t *testing.T) {
			executor := &browserLoginExecutor{}
			useBrowserLoginFakes(t, executor)
			loginSleep = func(context.Context, time.Duration) error { return context.Canceled }
			if _, err := LoginViaBrowser(loginURL); err == nil || !strings.Contains(err.Error(), "HTTPS Bonpreu host") {
				t.Fatalf("LoginViaBrowser(%q) error = %v; want untrusted URL rejection", loginURL, err)
			}
			if executor.navigationURL != "" {
				t.Fatalf("untrusted URL reached browser navigation: %q", executor.navigationURL)
			}
		})
	}
}

func TestLoginViaBrowserNavigationErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*browserLoginExecutor)
		want string
	}{
		{"CDP failure", func(e *browserLoginExecutor) { e.navigateError = errors.New("cdp failure") }, "open browser (is Chrome installed?): cdp failure"},
		{"navigation failure", func(e *browserLoginExecutor) { e.navigateErrorText = "net::ERR_FAILED" }, "navigate: net::ERR_FAILED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executor := &browserLoginExecutor{}
			tc.set(executor)
			useBrowserLoginFakes(t, executor)
			_, err := LoginViaBrowser("https://www.bonpreuesclat.cat/")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("LoginViaBrowser error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestLoginViaBrowserTimeoutAndCancellation(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		executor := &browserLoginExecutor{}
		useBrowserLoginFakes(t, executor)
		base := time.Unix(1, 0)
		calls := 0
		loginNow = func() time.Time {
			calls++
			if calls == 1 {
				return base
			}
			return base.Add(loginTimeout + time.Second)
		}
		_, err := LoginViaBrowser("https://www.bonpreuesclat.cat/")
		if err == nil || !strings.Contains(err.Error(), "timed out waiting for login") {
			t.Fatalf("LoginViaBrowser error = %v, want timeout", err)
		}
	})
	t.Run("cookie read failure is preserved on timeout", func(t *testing.T) {
		executor := &browserLoginExecutor{cookiesError: errors.New("cookie query failed")}
		useBrowserLoginFakes(t, executor)
		base := time.Unix(1, 0)
		calls := 0
		loginNow = func() time.Time {
			calls++
			if calls == 1 {
				return base
			}
			return base.Add(loginTimeout + time.Second)
		}
		_, err := LoginViaBrowser("https://www.bonpreuesclat.cat/")
		if err == nil || !strings.Contains(err.Error(), "cookie query failed") {
			t.Fatalf("LoginViaBrowser error = %v, want cookie read failure", err)
		}
	})
	t.Run("transient cookie read failure is preserved on timeout", func(t *testing.T) {
		executor := &browserLoginExecutor{cookiesError: errors.New("cookie query failed")}
		useBrowserLoginFakes(t, executor)
		base := time.Unix(1, 0)
		calls := 0
		loginNow = func() time.Time {
			calls++
			if calls == 1 {
				return base
			}
			if calls == 2 {
				return base.Add(time.Second)
			}
			return base.Add(loginTimeout + time.Second)
		}
		loginSleep = func(context.Context, time.Duration) error {
			executor.cookiesError = nil
			return nil
		}
		_, err := LoginViaBrowser("https://www.bonpreuesclat.cat/")
		if err == nil || !strings.Contains(err.Error(), "cookie query failed") {
			t.Fatalf("LoginViaBrowser error = %v, want transient cookie read failure", err)
		}
	})

	t.Run("browser closed", func(t *testing.T) {
		executor := &browserLoginExecutor{}
		useBrowserLoginFakes(t, executor)
		loginSleep = func(context.Context, time.Duration) error { return context.Canceled }
		_, err := LoginViaBrowser("https://www.bonpreuesclat.cat/")
		if err == nil || !strings.Contains(err.Error(), "login aborted") || !errors.Is(err, context.Canceled) {
			t.Fatalf("LoginViaBrowser error = %v, want cancellation", err)
		}
	})
}

func TestReadBonpreuCookiesReturnsCDPError(t *testing.T) {
	executor := &browserLoginExecutor{cookiesError: errors.New("cookie query failed")}
	useBrowserLoginFakes(t, executor)
	cookie, err := readBonpreuCookies(context.Background(), "https://www.bonpreuesclat.cat/")
	if cookie != "" || err == nil || err.Error() != "cookie query failed" {
		t.Fatalf("readBonpreuCookies() = %q, %v", cookie, err)
	}
}

func TestSleepCtxCompletionAndCancellation(t *testing.T) {
	if err := sleepCtx(context.Background(), 0); err != nil {
		t.Fatalf("sleepCtx completed with error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepCtx(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("sleepCtx cancellation = %v", err)
	}
}

func TestCheckoutPageURLRequiresBonpreuHTTPSHost(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
		ok    bool
	}{
		{"https://www.compraonline.bonpreuesclat.cat/path?token=ignored#fragment", "https://www.compraonline.bonpreuesclat.cat/checkout", true},
		{"http://www.bonpreuesclat.cat/", "", false},
		{"https://bonpreuesclat.cat.attacker.example/", "", false},
		{"https://example.com/", "", false},
	} {
		got, err := checkoutPageURL(tc.input)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("checkoutPageURL(%q) = %q, %v; want %q, ok=%v", tc.input, got, err, tc.want, tc.ok)
		}
	}
}

func TestCheckoutInBrowserRejectsUnauthenticatedCookieBeforeLaunching(t *testing.T) {
	for _, tc := range []struct{ cookie, want string }{
		{"global_sid=anonymous", "authenticated"},
		{"global_sid=; aws-waf-token=", "authenticated"},
		{"global_sid=x\naws-waf-token=y", "could not be parsed"},
	} {
		if err := CheckoutInBrowser("https://www.bonpreuesclat.cat", tc.cookie); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("CheckoutInBrowser with invalid cookie %q error = %v", tc.cookie, err)
		}
	}
}

func TestCheckoutInBrowserRejectsUntrustedBaseURL(t *testing.T) {
	if err := CheckoutInBrowser("http://example.com", "global_sid=session; aws-waf-token=waf"); err == nil || !strings.Contains(err.Error(), "HTTPS Bonpreu host") {
		t.Fatalf("CheckoutInBrowser with untrusted base URL error = %v", err)
	}
}

func TestCheckoutInBrowserReturnsPromptWriteErrorBeforeLaunching(t *testing.T) {
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeEnd.Close(); err != nil {
		t.Fatal(err)
	}
	oldStderr, oldRun := os.Stderr, runBrowserActions
	os.Stderr = writeEnd
	var runCount int
	runBrowserActions = func(context.Context, ...chromedp.Action) error {
		runCount++
		return nil
	}
	t.Cleanup(func() {
		os.Stderr = oldStderr
		runBrowserActions = oldRun
		_ = readEnd.Close()
		_ = writeEnd.Close()
	})

	err = CheckoutInBrowser("https://www.bonpreuesclat.cat", "global_sid=session; aws-waf-token=waf")
	if err == nil || !strings.Contains(err.Error(), "write checkout browser prompt") {
		t.Fatalf("CheckoutInBrowser error = %v; want prompt write error", err)
	}
	if runCount != 0 {
		t.Fatalf("browser launched %d times after the prompt failed; want none", runCount)
	}
}

func TestCheckoutInBrowserSetsSessionCookiesAndNavigates(t *testing.T) {
	executor := &browserLoginExecutor{}
	useBrowserLoginFakes(t, executor)
	checkoutWait = func(context.Context) error { return nil }
	if err := CheckoutInBrowser("https://www.compraonline.bonpreuesclat.cat/ignored", "global_sid=session; aws-waf-token=waf"); err != nil {
		t.Fatal(err)
	}
	if executor.navigationURL != "https://www.compraonline.bonpreuesclat.cat/checkout" {
		t.Fatalf("checkout navigation URL = %q", executor.navigationURL)
	}
	if len(executor.setCookies) != 2 {
		t.Fatalf("set cookies = %#v", executor.setCookies)
	}
	seen := map[string]string{}
	for _, cookie := range executor.setCookies {
		seen[cookie.Name] = cookie.Value
		if cookie.URL != executor.navigationURL || !cookie.Secure || !cookie.HTTPOnly {
			t.Errorf("cookie params = %#v", cookie)
		}
	}
	if seen["global_sid"] != "session" || seen["aws-waf-token"] != "waf" {
		t.Fatalf("set cookie values = %#v", seen)
	}
}

func TestCheckoutInBrowserReturnsNavigationCookieAndWaitErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		setup   func(*browserLoginExecutor)
		waitErr error
		want    string
	}{
		{"cookie setup", func(e *browserLoginExecutor) { e.setCookieError = errors.New("cookie setup failed") }, nil, "open checkout browser: cookie setup failed"},
		{"navigation command", func(e *browserLoginExecutor) { e.navigateError = errors.New("navigation failed") }, nil, "open checkout browser: navigation failed"},
		{"navigation response", func(e *browserLoginExecutor) { e.navigateErrorText = "net::ERR_FAILED" }, nil, "navigate: net::ERR_FAILED"},
		{"wait", func(*browserLoginExecutor) {}, errors.New("stdin failed"), "wait for checkout confirmation: stdin failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executor := &browserLoginExecutor{}
			tc.setup(executor)
			useBrowserLoginFakes(t, executor)
			checkoutWait = func(context.Context) error { return tc.waitErr }
			err := CheckoutInBrowser("https://www.bonpreuesclat.cat", "global_sid=session; aws-waf-token=waf")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("CheckoutInBrowser error = %v; want %q", err, tc.want)
			}
		})
	}
}

func TestWaitForCheckoutReadsLineAndStopsOnCancellation(t *testing.T) {
	if err := waitForCheckout(context.Background(), strings.NewReader("\n")); err != nil {
		t.Fatalf("waitForCheckout with Enter = %v", err)
	}
	preCanceled, preCancel := context.WithCancel(context.Background())
	preCancel()
	if err := waitForCheckout(preCanceled, strings.NewReader("\n")); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForCheckout with canceled context = %v", err)
	}

	reader := &blockingCheckoutReader{started: make(chan struct{}), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- waitForCheckout(ctx, reader) }()
	select {
	case <-reader.started:
	case <-time.After(time.Second):
		t.Fatal("waitForCheckout did not start reading")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waitForCheckout cancellation = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waitForCheckout did not return after cancellation")
	}
	close(reader.release)
}

func TestDefaultCheckoutWaitReadsStdin(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := writer.WriteString("\n"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	stdin := os.Stdin
	os.Stdin = reader
	t.Cleanup(func() { os.Stdin = stdin })

	if err := checkoutWait(context.Background()); err != nil {
		t.Fatalf("default checkout wait = %v", err)
	}
}
