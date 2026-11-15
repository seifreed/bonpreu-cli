package client

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/browserutils/kooky"
	_ "github.com/browserutils/kooky/browser/all" // register Chrome/Firefox/Safari/Edge/Brave finders
)

// cookieDomain is the substring every Bonpreu cookie's domain carries; used to
// pull just the site's cookies out of a browser's (large) cookie store.
const cookieDomain = "bonpreuesclat.cat"

type nameVal struct{ name, val string }

// CookiesFromBrowser reads the Bonpreu session straight out of a browser's
// cookie store — the easy path: the user stays logged in in their everyday
// browser and the CLI lifts the cookie (HttpOnly included, since this reads the
// decrypted store, not page JS). browser filters to one of chrome|chromium|
// firefox|safari|edge|brave; "" reads every installed browser.
func CookiesFromBrowser(browser string) (Session, error) {
	return cookiesFromSequence(browser, kooky.TraverseCookies(context.Background(), kooky.DomainContains(cookieDomain)))
}

func cookiesFromSequence(browser string, cookies kooky.CookieSeq) (Session, error) {
	browser = strings.ToLower(strings.TrimSpace(browser))

	// Collect Bonpreu cookies grouped BY BROWSER, skipping per-store failures: a
	// browser that isn't installed (or isn't supported on this OS) must not abort
	// the read of the one the user actually uses. Cookies are kept per browser
	// rather than merged by name across all of them — a global last-write-wins
	// merge could splice global_sid from one browser with aws-waf-token from
	// another, producing an authed-looking but server-rejected session.
	type store struct {
		val   map[string]string
		order []string
	}
	stores := map[string]*store{}
	var storeOrder []string
	var storeErrors []error
	for c, err := range cookies {
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) && !strings.Contains(strings.ToLower(err.Error()), "not implemented") {
				storeErrors = append(storeErrors, err)
			}
			continue
		}
		if c == nil || c.Name == "" || c.Value == "" {
			continue
		}
		bname := ""
		if c.Browser != nil {
			bname = strings.ToLower(c.Browser.Browser())
		}
		if browser != "" && bname != browser {
			continue
		}
		s := stores[bname]
		if s == nil {
			s = &store{val: map[string]string{}}
			stores[bname] = s
			storeOrder = append(storeOrder, bname)
		}
		if _, ok := s.val[c.Name]; !ok {
			s.order = append(s.order, c.Name)
		}
		s.val[c.Name] = c.Value // last write wins within a single browser
	}

	// Use the first browser (in traversal order) whose cookie set is a complete,
	// authenticated session.
	for _, bname := range storeOrder {
		s := stores[bname]
		pairs := make([]nameVal, 0, len(s.order))
		for _, n := range s.order {
			pairs = append(pairs, nameVal{n, s.val[n]})
		}
		if cookie := buildCookieHeader(pairs); CookieLooksAuthed(cookie) {
			return Session{Cookie: cookie}, nil
		}
	}

	where := "your browser"
	if browser != "" {
		where = browser
	}
	if len(storeErrors) > 0 {
		return Session{}, fmt.Errorf("could not read browser cookie stores: %w", errors.Join(storeErrors...))
	}
	return Session{}, fmt.Errorf("no Bonpreu session found in %s — "+
		"log in at compraonline.bonpreuesclat.cat in that browser first "+
		"(or pass --from-browser <chrome|firefox|safari|edge|brave>)", where)
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
