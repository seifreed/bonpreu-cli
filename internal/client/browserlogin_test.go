//go:build browserlogin

package client

import (
	"context"
	"errors"
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
	navigateError     error
	navigateErrorText string
	cookiesError      error
	navigationURL     string
	cookieURLs        []string
}

func (e *browserLoginExecutor) Execute(_ context.Context, method string, params, result any) error {
	switch method {
	case network.CommandEnable:
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
	oldRun, oldNow, oldSleep := runBrowserActions, loginNow, loginSleep
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
		runBrowserActions, loginNow, loginSleep = oldRun, oldNow, oldSleep
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

	t.Run("browser closed", func(t *testing.T) {
		executor := &browserLoginExecutor{}
		useBrowserLoginFakes(t, executor)
		loginSleep = func(context.Context, time.Duration) error { return context.Canceled }
		_, err := LoginViaBrowser("https://www.bonpreuesclat.cat/")
		if err == nil || !strings.Contains(err.Error(), "login aborted") {
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
