package client

import (
	"encoding/json"
	"fmt"
	neturl "net/url"
	"regexp"
	"strings"
)

// HARSession is the Bonpreu auth material extracted from a browser HAR export
// (DevTools → Network → "Export HAR…", taken while logged in). Bonpreu uses a
// cookie session, not a Bearer token: the Cookie header carries both the login
// (global_sid) and the AWS WAF clearance (aws-waf-token). The resolved region —
// which scopes cart prices — is read from the regionId query param the app sends.
//
// Only request *Cookie headers* and request *query params* are read; nothing
// from request bodies (which can hold credentials) is touched.
type HARSession struct {
	Cookie   string
	RegionID string
}

var regionIDRE = regexp.MustCompile(`(?i)region[_-]?id`)

// bonpreuHost is the registrable domain; the site is served from subdomains
// (www.compraonline.bonpreuesclat.cat, etc.).
const bonpreuHost = "bonpreuesclat.cat"

// isBonpreuHost matches the Bonpreu domain on the URL's HOST, not a substring of
// the whole URL — so a foreign request like https://evil.com/?x=bonpreuesclat.cat
// (whose host is evil.com) is not mistaken for a Bonpreu request and its cookies
// are never read. Accepts the apex and any subdomain, case-insensitively.
func isBonpreuHost(host string) bool {
	host = strings.ToLower(host)
	return host == bonpreuHost || strings.HasSuffix(host, "."+bonpreuHost)
}

// ParseHAR scans a HAR for the freshest authenticated Bonpreu request: its Cookie
// header (login + WAF clearance) and the regionId the session was using.
func ParseHAR(data []byte) (HARSession, error) {
	var har struct {
		Log struct {
			Entries []struct {
				Request struct {
					URL     string `json:"url"`
					Headers []struct {
						Name  string `json:"name"`
						Value string `json:"value"`
					} `json:"headers"`
				} `json:"request"`
			} `json:"entries"`
		} `json:"log"`
	}
	if err := json.Unmarshal(data, &har); err != nil {
		return HARSession{}, fmt.Errorf("parse HAR JSON: %w", err)
	}

	var s HARSession
	var sawBonpreuReq, sawCookieHeader bool
	for _, e := range har.Log.Entries {
		// Match on the parsed host, not a substring of the raw URL: otherwise a
		// foreign request that merely mentions the domain in its path/query would be
		// treated as Bonpreu and its Cookie header read in as the session.
		u, perr := neturl.Parse(e.Request.URL)
		if perr != nil || !isBonpreuHost(u.Hostname()) {
			continue
		}
		sawBonpreuReq = true
		for k, vs := range u.Query() {
			if regionIDRE.MatchString(k) && len(vs) > 0 && vs[0] != "" {
				s.RegionID = vs[0]
			}
		}
		for _, h := range e.Request.Headers {
			if !strings.EqualFold(h.Name, "cookie") {
				continue
			}
			sawCookieHeader = true
			if CookieLooksAuthed(h.Value) {
				s.Cookie = h.Value // last (freshest) authed request wins
			}
		}
	}

	if s.Cookie == "" {
		return s, harNoCookieErr(sawBonpreuReq, sawCookieHeader)
	}
	return s, nil
}

// harNoCookieErr explains *why* no session was found, so the fix is obvious. The
// common failure is Chrome's "Export HAR (sanitized)", which strips every Cookie
// header — the HAR has Bonpreu requests but none carry cookies.
func harNoCookieErr(sawBonpreuReq, sawCookieHeader bool) error {
	switch {
	case !sawBonpreuReq:
		return fmt.Errorf("this HAR has no compraonline.bonpreuesclat.cat requests — " +
			"export it while browsing the Bonpreu site, logged in")
	case !sawCookieHeader:
		return fmt.Errorf("HAR sanititzat (sense capçaleres Cookie) — re-exporta amb: " +
			"DevTools → Network → clic dret sobre una petició → " +
			"\"Save all as HAR with sensitive data\" (no el botó ⤓, que sanititza)")
	default:
		return fmt.Errorf("no authenticated Bonpreu cookie found in HAR " +
			"(need a request carrying global_sid + aws-waf-token) — export the HAR while logged in")
	}
}

// CookieLooksAuthed reports whether a Cookie header carries the login session
// (global_sid) and WAF clearance (aws-waf-token). Used to skip anonymous
// pre-login requests when reading a HAR, and to warn on a manually-set cookie.
func CookieLooksAuthed(v string) bool {
	return strings.Contains(v, "global_sid=") && strings.Contains(strings.ToLower(v), "aws-waf-token=")
}

// SaveHARSession caches the imported session for the authenticated commands.
func SaveHARSession(s HARSession) error {
	return SaveSession(Session(s))
}
