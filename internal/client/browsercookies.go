package client

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/browserutils/kooky"
	_ "github.com/browserutils/kooky/browser/all" // register Chrome/Firefox/Safari/Edge/Brave finders
)

// cookieDomain is the registrable domain shared by Bonpreu's site cookies.
const cookieDomain = "bonpreuesclat.cat"
const cookieTargetHost = "www.compraonline.bonpreuesclat.cat"

type nameVal struct{ name, val string }

// CookiesFromBrowser reads the Bonpreu session straight out of a browser's
// cookie store — the easy path: the user stays logged in in their everyday
// browser and the CLI lifts the cookie (HttpOnly included, since this reads the
// decrypted store, not page JS). browser filters to one of chrome|chromium|
// firefox|safari|edge|brave; "" reads every installed browser.
func CookiesFromBrowser(browser string) (Session, error) {
	return cookiesFromSequence(browser, cookiesFromStores(browser, kooky.TraverseCookieStores(context.Background())))
}

func cookiesFromStores(browser string, stores kooky.CookieStoreSeq) kooky.CookieSeq {
	browser = strings.ToLower(strings.TrimSpace(browser))
	type item struct {
		cookie *kooky.Cookie
		err    error
	}
	var items []item
	for store, err := range stores {
		if err != nil {
			items = append(items, item{err: err})
			continue
		}
		if store == nil || browser != "" && !strings.EqualFold(strings.TrimSpace(store.Browser()), browser) {
			continue
		}
		for cookie, err := range store.TraverseCookies(kooky.DomainContains(cookieDomain)) {
			items = append(items, item{cookie: cookie, err: err})
		}
		if err := store.Close(); err != nil {
			items = append(items, item{err: fmt.Errorf("close %s cookie store: %w", store.Browser(), err)})
		}
	}
	return func(yield func(*kooky.Cookie, error) bool) {
		for _, item := range items {
			if !yield(item.cookie, item.err) {
				return
			}
		}
	}
}

func cookiesFromSequence(browser string, cookies kooky.CookieSeq) (Session, error) {
	browser = strings.ToLower(strings.TrimSpace(browser))

	// Keep cookies within their browser profile and container; combining stores
	// can splice a session ID and WAF token into an invalid authenticated session.
	type store struct {
		val                   map[string]string
		order                 []string
		conflictingAuthCookie bool
	}
	type storeKey struct {
		browser, profile, path, container string
		defaultProfile                    bool
	}
	stores := map[storeKey]*store{}
	var storeOrder []storeKey
	var storeErrors []error
	unsupportedStoreCount := 0
	now := time.Now()
	for c, err := range cookies {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if isUnsupportedCookieStoreError(err) {
				unsupportedStoreCount++
			} else {
				storeErrors = append(storeErrors, err)
			}
			continue
		}
		if c == nil || c.Browser == nil || c.Name == "" || !cookieAppliesToShopHome(c, now) {
			continue
		}
		if c.Value == "" && !isAuthCookieName(c.Name) {
			continue
		}
		key := storeKey{
			browser:        strings.ToLower(c.Browser.Browser()),
			profile:        c.Browser.Profile(),
			path:           c.Browser.FilePath(),
			container:      c.Container,
			defaultProfile: c.Browser.IsDefaultProfile(),
		}
		if browser != "" && key.browser != browser {
			continue
		}
		s := stores[key]
		if s == nil {
			s = &store{val: map[string]string{}}
			stores[key] = s
			storeOrder = append(storeOrder, key)
		}
		if value, ok := s.val[c.Name]; ok && value != c.Value {
			if isAuthCookieName(c.Name) {
				s.conflictingAuthCookie = true
			}
			continue
		}
		if _, ok := s.val[c.Name]; !ok {
			s.order = append(s.order, c.Name)
		}
		s.val[c.Name] = c.Value
	}

	where := "your browser"
	if browser != "" {
		where = browser
	}
	var authenticatedCookie string
	conflictingAuthCookie := false
	ambiguityHint := "pass --from-browser <name>"
	if browser != "" {
		ambiguityHint = "leave only one authenticated profile in that browser"
	}
	for _, key := range storeOrder {
		s := stores[key]
		if s.conflictingAuthCookie {
			conflictingAuthCookie = true
			continue
		}
		pairs := make([]nameVal, 0, len(s.order))
		for _, n := range s.order {
			pairs = append(pairs, nameVal{n, s.val[n]})
		}
		if cookie := buildCookieHeader(pairs); CookieLooksAuthed(cookie) {
			if authenticatedCookie != "" {
				return Session{}, fmt.Errorf("multiple authenticated Bonpreu sessions found in %s; %s and retry", where, ambiguityHint)
			}
			authenticatedCookie = cookie
		}
	}
	if conflictingAuthCookie {
		return Session{}, fmt.Errorf("conflicting Bonpreu authentication cookies apply to the shop home page in %s; remove duplicate sessions and retry", where)
	}
	if authenticatedCookie != "" {
		return Session{Cookie: authenticatedCookie}, nil
	}

	if len(storeErrors) > 0 {
		return Session{}, fmt.Errorf("could not read browser cookie stores: %w", errors.Join(storeErrors...))
	}
	if unsupportedStoreCount > 0 {
		return Session{}, fmt.Errorf("no Bonpreu session found in %s; %d browser cookie store(s) are unsupported on this platform — use `bonpreu import-har` instead", where, unsupportedStoreCount)
	}
	return Session{}, fmt.Errorf("no Bonpreu session found in %s — "+
		"log in at compraonline.bonpreuesclat.cat in that browser first "+
		"(or pass --from-browser <chrome|firefox|safari|edge|brave>)", where)
}

func isAuthCookieName(name string) bool {
	return name == "global_sid" || name == "aws-waf-token"
}

func cookieAppliesToShopHome(c *kooky.Cookie, now time.Time) bool {
	domain := strings.ToLower(strings.TrimSpace(c.Domain))
	domainCookie := strings.HasPrefix(domain, ".")
	domain = strings.TrimPrefix(domain, ".")
	if domainCookie {
		if cookieTargetHost != domain && !strings.HasSuffix(cookieTargetHost, "."+domain) {
			return false
		}
	} else if domain != cookieTargetHost {
		return false
	}
	if c.Path != "" && c.Path != "/" {
		return false
	}
	return c.Expires.IsZero() || c.Expires.After(now)
}

func isUnsupportedCookieStoreError(err error) bool {
	for err != nil {
		switch strings.ToLower(err.Error()) {
		case "not implemented", "dpapi method not implemented on this platform":
			return true
		}
		err = errors.Unwrap(err)
	}
	return false
}

// buildCookieHeader renders cookie name/value pairs as a "n1=v1; n2=v2" header.
// Pairs are emitted in a stable (sorted) order so the result is deterministic.
func buildCookieHeader(pairs []nameVal) string {
	sorted := append([]nameVal(nil), pairs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].name < sorted[j].name })
	parts := make([]string, 0, len(sorted))
	for _, p := range sorted {
		parts = append(parts, p.name+"="+p.val)
	}
	return strings.Join(parts, "; ")
}
