package client

import (
	"encoding/json"
	"fmt"
	"net/http"
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

var regionIDRE = regexp.MustCompile(`(?i)^region[_-]?id$`)

// bonpreuHost is the registrable domain.
const bonpreuHost = "bonpreuesclat.cat"

// isBonpreuHost accepts only known Bonpreu web entry points. A suffix check would
// trust every delegated subdomain, even though an authenticated cookie must never
// be sent to an arbitrary host under the registrable domain.
func isBonpreuHost(host string) bool {
	switch strings.ToLower(strings.TrimSuffix(host, ".")) {
	case bonpreuHost, "www.bonpreuesclat.cat", "compraonline.bonpreuesclat.cat", "www.compraonline.bonpreuesclat.cat":
		return true
	default:
		return false
	}
}

// ParseHAR scans a HAR for the freshest authenticated Bonpreu request: its Cookie
// header (login + WAF clearance) and, when present, the regionId it was using.
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
	var sawBonpreuReq, sawCookieHeader, sawInsecureCookie bool
	var sawConflictingAuthCookie bool
	for _, e := range har.Log.Entries {
		// Match on the parsed host, not a substring of the raw URL: otherwise a
		// foreign request that merely mentions the domain in its path/query would be
		// treated as Bonpreu and its Cookie header read in as the session.
		u, perr := neturl.Parse(e.Request.URL)
		if perr != nil || !isBonpreuHost(u.Hostname()) {
			continue
		}
		sawBonpreuReq = true
		var cookieParts []string
		for _, h := range e.Request.Headers {
			if strings.EqualFold(h.Name, "cookie") {
				cookieParts = append(cookieParts, strings.TrimSpace(h.Value))
			}
		}
		cookieHeader := strings.Join(cookieParts, "; ")
		if len(cookieParts) > 0 {
			sawCookieHeader = true
		}
		if !strings.EqualFold(u.Scheme, "https") {
			if CookieLooksAuthed(cookieHeader) {
				sawInsecureCookie = true
			}
			continue
		}
		var requestRegion string
		ambiguousRegion := false
		for k, vs := range u.Query() {
			if !regionIDRE.MatchString(k) {
				continue
			}
			for _, value := range vs {
				if value == "" {
					continue
				}
				if requestRegion == "" {
					requestRegion = value
				} else if requestRegion != value {
					ambiguousRegion = true
				}
			}
		}
		if ambiguousRegion {
			requestRegion = ""
		}
		requestAuthed, conflictingAuthCookie := cookieAuthStatus(cookieHeader)
		if conflictingAuthCookie {
			// Do not fall back to an older session when the newest authenticated
			// request contains contradictory copies of an auth cookie.
			s.Cookie = ""
			s.RegionID = ""
			sawConflictingAuthCookie = true
			continue
		}
		if requestAuthed {
			s.Cookie = cookieHeader // last (freshest) authed request wins
			s.RegionID = requestRegion
			sawConflictingAuthCookie = false
		}
	}

	if s.Cookie == "" {
		if sawConflictingAuthCookie {
			return s, fmt.Errorf("conflicting Bonpreu authentication cookies in the HAR; export a fresh request with one session")
		}
		if sawInsecureCookie {
			return s, fmt.Errorf("refusing to import an authenticated cookie captured over a non-HTTPS request; export a HAR from the HTTPS Bonpreu site")
		}
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
	authed, _ := cookieAuthStatus(v)
	return authed
}

func cookieAuthStatus(v string) (authed, conflicting bool) {
	req := &http.Request{Header: http.Header{"Cookie": []string{v}}}
	values := make(map[string]string, 2)
	var sid, waf bool
	for _, cookie := range req.Cookies() {
		switch cookie.Name {
		case "global_sid":
			if previous, ok := values[cookie.Name]; ok && previous != cookie.Value {
				conflicting = true
			}
			values[cookie.Name] = cookie.Value
			sid = sid || cookie.Value != ""
		case "aws-waf-token":
			if previous, ok := values[cookie.Name]; ok && previous != cookie.Value {
				conflicting = true
			}
			values[cookie.Name] = cookie.Value
			waf = waf || cookie.Value != ""
		}
	}
	return sid && waf && !conflicting, conflicting
}

// SaveHARSession caches the imported session for the authenticated commands.
func SaveHARSession(s HARSession) error {
	return SaveSession(Session(s))
}
