package client

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/browserutils/kooky"
)

type testBrowser string

type cookieItem struct {
	cookie *kooky.Cookie
	err    error
}

func (b testBrowser) Browser() string      { return string(b) }
func (testBrowser) Profile() string        { return "default" }
func (testBrowser) IsDefaultProfile() bool { return true }
func (testBrowser) FilePath() string       { return "" }

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
	return &kooky.Cookie{Browser: testBrowser(browser), Cookie: http.Cookie{Name: name, Value: value}}
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

func TestCookiesFromBrowserUsesIsolatedProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := CookiesFromBrowser("chrome"); err == nil || !strings.Contains(err.Error(), "no Bonpreu session found") {
		t.Fatalf("empty isolated browser profile error = %v", err)
	}
}
