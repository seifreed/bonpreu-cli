package client

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/browserutils/kooky"
)

type testBrowser string

type testProfileBrowser struct {
	browser string
	profile string
	path    string
}

type cookieItem struct {
	cookie *kooky.Cookie
	err    error
}

func (b testBrowser) Browser() string             { return string(b) }
func (testBrowser) Profile() string               { return "default" }
func (testBrowser) IsDefaultProfile() bool        { return true }
func (testBrowser) FilePath() string              { return "" }
func (b testProfileBrowser) Browser() string      { return b.browser }
func (b testProfileBrowser) Profile() string      { return b.profile }
func (testProfileBrowser) IsDefaultProfile() bool { return false }
func (b testProfileBrowser) FilePath() string     { return b.path }

func cookieSequence(items ...cookieItem) kooky.CookieSeq {
	return func(yield func(*kooky.Cookie, error) bool) {
		for _, item := range items {
			if !yield(item.cookie, item.err) {
				return
			}
		}
	}
}

func browserCookie(browser, name, value string) *kooky.Cookie {
	return browserCookieForDomain(browser, name, value, "."+cookieDomain)
}

func browserCookieForDomain(browser, name, value, domain string) *kooky.Cookie {
	return &kooky.Cookie{Browser: testBrowser(browser), Cookie: http.Cookie{Name: name, Value: value, Domain: domain}}
}

func TestCookiesFromSequenceGroupsByBrowserAndFilters(t *testing.T) {
	seq := cookieSequence(
		cookieItem{nil, errors.New("store unreadable")},
		cookieItem{browserCookie("chrome", "global_sid", "old"), nil},
		cookieItem{browserCookie("chrome", "global_sid", "new"), nil},
		cookieItem{browserCookie("firefox", "aws-waf-token", "other-browser"), nil},
		cookieItem{browserCookie("chrome", "aws-waf-token", "token"), nil},
		cookieItem{browserCookie("chrome", "empty", ""), nil},
		cookieItem{&kooky.Cookie{Browser: testBrowser("chrome"), Cookie: http.Cookie{Value: "value"}}, nil},
		cookieItem{&kooky.Cookie{Cookie: http.Cookie{Name: "orphan", Value: "value"}}, nil},
	)

	got, err := cookiesFromSequence(" CHROME ", seq)
	if err != nil || got.Cookie != "aws-waf-token=token; global_sid=new" {
		t.Fatalf("filtered session = %+v, err %v", got, err)
	}
	got, err = cookiesFromSequence("", seq)
	if err != nil || got.Cookie != "aws-waf-token=token; global_sid=new" {
		t.Fatalf("unfiltered session = %+v, err %v", got, err)
	}
}

func TestCookiesFromSequenceDoesNotCombineProfiles(t *testing.T) {
	profileA := testProfileBrowser{browser: "chrome", profile: "Default", path: "/chrome/Default/Cookies"}
	profileB := testProfileBrowser{browser: "chrome", profile: "Profile 1", path: "/chrome/Profile 1/Cookies"}
	seq := cookieSequence(
		cookieItem{&kooky.Cookie{Browser: profileA, Cookie: http.Cookie{Name: "global_sid", Value: "session-a", Domain: "." + cookieDomain}}, nil},
		cookieItem{&kooky.Cookie{Browser: profileB, Cookie: http.Cookie{Name: "aws-waf-token", Value: "token-b", Domain: "." + cookieDomain}}, nil},
	)

	if got, err := cookiesFromSequence("chrome", seq); err == nil || got.Cookie != "" {
		t.Fatalf("cookies from separate profiles formed a session: %+v, err %v", got, err)
	}
}

func TestCookiesFromSequenceIgnoresDomainsThatOnlyContainBonpreuName(t *testing.T) {
	seq := cookieSequence(
		cookieItem{browserCookie("chrome", "global_sid", "legit-session"), nil},
		cookieItem{browserCookieForDomain("chrome", "aws-waf-token", "legit-token", ".www.compraonline.bonpreuesclat.cat"), nil},
		cookieItem{browserCookieForDomain("chrome", "global_sid", "spoof-session", "evilbonpreuesclat.cat"), nil},
		cookieItem{browserCookieForDomain("chrome", "aws-waf-token", "spoof-token", "bonpreuesclat.cat.evil.example"), nil},
	)

	got, err := cookiesFromSequence("chrome", seq)
	if err != nil || got.Cookie != "aws-waf-token=legit-token; global_sid=legit-session" {
		t.Fatalf("session from domains containing Bonpreu's name = %+v, %v; want only exact Bonpreu domains", got, err)
	}
}

func TestCookiesFromSequenceExplainsMissingSession(t *testing.T) {
	for _, browser := range []string{"", " safari "} {
		_, err := cookiesFromSequence(browser, cookieSequence(
			cookieItem{browserCookie("chrome", "global_sid", "only-half"), nil},
		))
		if err == nil || !strings.Contains(err.Error(), "no Bonpreu session found") {
			t.Fatalf("browser %q error = %v", browser, err)
		}
		if browser == "safari" && !strings.Contains(err.Error(), "safari") {
			t.Fatalf("filtered error should identify browser: %v", err)
		}
	}
}

func TestCookiesFromSequenceReturnsStoreErrorsWhenNoSession(t *testing.T) {
	_, err := cookiesFromSequence("chrome", cookieSequence(
		cookieItem{nil, errors.New("profile decryption failed")},
		cookieItem{browserCookie("chrome", "global_sid", "half-session"), nil},
	))
	if err == nil || !strings.Contains(err.Error(), "profile decryption failed") {
		t.Fatalf("browser cookie error = %v; want underlying profile-read failure", err)
	}
}

func TestCookiesFromSequenceOnlySkipsKnownUnsupportedStoreErrors(t *testing.T) {
	for _, message := range []string{"not implemented", "DPAPI method not implemented on this platform"} {
		_, err := cookiesFromSequence("chrome", cookieSequence(
			cookieItem{nil, fmt.Errorf("cookie store: %w", errors.New(message))},
			cookieItem{browserCookie("chrome", "global_sid", "half-session"), nil},
		))
		if err == nil || !strings.Contains(err.Error(), "no Bonpreu session found") || !strings.Contains(err.Error(), "unsupported on this platform") {
			t.Errorf("known unsupported error %q should be reported when no session is found, got %v", message, err)
		}
	}

	got, err := cookiesFromSequence("chrome", cookieSequence(
		cookieItem{nil, errors.New("not implemented")},
		cookieItem{browserCookie("chrome", "global_sid", "session"), nil},
		cookieItem{browserCookie("chrome", "aws-waf-token", "token"), nil},
	))
	if err != nil || !CookieLooksAuthed(got.Cookie) {
		t.Errorf("unsupported unrelated store should not block a valid session: %+v, %v", got, err)
	}

	_, err = cookiesFromSequence("chrome", cookieSequence(
		cookieItem{nil, errors.New("profile not implemented: decryption failed")},
	))
	if err == nil || !strings.Contains(err.Error(), "profile not implemented: decryption failed") {
		t.Errorf("unrelated store error containing unsupported wording must be preserved, got %v", err)
	}
}

func TestCookiesFromBrowserUsesIsolatedProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := CookiesFromBrowser("chrome"); err == nil || !strings.Contains(err.Error(), "no Bonpreu session found") {
		t.Fatalf("empty isolated browser profile error = %v", err)
	}
}
