package client

import (
	"strings"
	"testing"
)

const harFixture = `{"log":{"entries":[
 {"request":{"url":"https://www.compraonline.bonpreuesclat.cat/","headers":[
   {"name":"Cookie","value":"VISITORID=abc"}]}},
 {"request":{"url":"https://www.compraonline.bonpreuesclat.cat/api/customer/v1/customers/has-favorites?regionId=d57f6327-ce67-44d5-bdd5-dc63e98460a6","headers":[
   {"name":"cookie","value":"VISITORID=abc; global_sid=SID123; aws-waf-token=WAF456"}]}}
]}}`

func TestParseHAR(t *testing.T) {
	s, err := ParseHAR([]byte(harFixture))
	if err != nil {
		t.Fatalf("ParseHAR: %v", err)
	}
	if s.RegionID != "d57f6327-ce67-44d5-bdd5-dc63e98460a6" {
		t.Errorf("region = %q", s.RegionID)
	}
	// The authed request (global_sid + aws-waf-token) wins over the anonymous one.
	if s.Cookie != "VISITORID=abc; global_sid=SID123; aws-waf-token=WAF456" {
		t.Errorf("cookie = %q", s.Cookie)
	}
}

func TestParseHARCombinesSplitCookieHeaders(t *testing.T) {
	har := `{"log":{"entries":[{"request":{"url":"https://compraonline.bonpreuesclat.cat/x?regionId=r1","headers":[` +
		`{"name":"cookie","value":"global_sid=SID"},` +
		`{"name":"Cookie","value":"aws-waf-token=WAF; VISITORID=abc"}]}}]}}`
	s, err := ParseHAR([]byte(har))
	if err != nil {
		t.Fatalf("ParseHAR: %v", err)
	}
	if s.Cookie != "global_sid=SID; aws-waf-token=WAF; VISITORID=abc" || s.RegionID != "r1" {
		t.Fatalf("ParseHAR split cookies = %+v", s)
	}
}

func TestParseHARRejectsConflictingAuthenticationCookies(t *testing.T) {
	har := `{"log":{"entries":[` +
		`{"request":{"url":"https://compraonline.bonpreuesclat.cat/x","headers":[{"name":"cookie","value":"global_sid=old; aws-waf-token=old"}]}},` +
		`{"request":{"url":"https://compraonline.bonpreuesclat.cat/x","headers":[` +
		`{"name":"cookie","value":"global_sid=new"},` +
		`{"name":"cookie","value":"global_sid=other; aws-waf-token=new"}]}}` +
		`]}}`
	if s, err := ParseHAR([]byte(har)); err == nil || s.Cookie != "" || !strings.Contains(err.Error(), "conflicting Bonpreu authentication cookies") {
		t.Fatalf("ParseHAR conflicting session = %+v, %v", s, err)
	}
}

func TestParseHARKeepsRegionFromAuthenticatedRequest(t *testing.T) {
	har := `{"log":{"entries":[
 {"request":{"url":"https://www.compraonline.bonpreuesclat.cat/api/cart/v1/carts/active?regionId=logged-in-region","headers":[
   {"name":"cookie","value":"global_sid=SID123; aws-waf-token=WAF456"}]}},
 {"request":{"url":"https://www.compraonline.bonpreuesclat.cat/api/product-pages/search?regionId=anonymous-region","headers":[
   {"name":"cookie","value":"VISITORID=abc"}]}}
]}}`

	s, err := ParseHAR([]byte(har))
	if err != nil {
		t.Fatalf("ParseHAR: %v", err)
	}
	if s.RegionID != "logged-in-region" {
		t.Errorf("region = %q; want region from the authenticated request", s.RegionID)
	}
}

func TestParseHARDoesNotReuseRegionFromOlderAuthenticatedRequest(t *testing.T) {
	har := `{"log":{"entries":[
 {"request":{"url":"https://www.compraonline.bonpreuesclat.cat/api/cart/v1/carts/active?regionId=old-region","headers":[
   {"name":"cookie","value":"global_sid=OLD; aws-waf-token=OLD-WAF"}]}},
 {"request":{"url":"https://www.compraonline.bonpreuesclat.cat/api/cart/v1/carts/active","headers":[
   {"name":"cookie","value":"global_sid=NEW; aws-waf-token=NEW-WAF"}]}}
]}}`

	s, err := ParseHAR([]byte(har))
	if err != nil {
		t.Fatalf("ParseHAR: %v", err)
	}
	if s.Cookie != "global_sid=NEW; aws-waf-token=NEW-WAF" {
		t.Fatalf("cookie = %q; want the newest authenticated request", s.Cookie)
	}
	if s.RegionID != "" {
		t.Fatalf("region = %q; stale region must not be paired with the newer cookie", s.RegionID)
	}
}

func TestParseHARRejectsCookiesCapturedOverHTTP(t *testing.T) {
	har := `{"log":{"entries":[{"request":{"url":"http://compraonline.bonpreuesclat.cat/x","headers":[{"name":"cookie","value":"global_sid=INSECURE; aws-waf-token=INSECURE"}]}}]}}`
	if _, err := ParseHAR([]byte(har)); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("HTTP cookie import error = %v; want HTTPS requirement", err)
	}
}

func TestParseHARIgnoresInsecureCookieWhenHTTPSSessionExists(t *testing.T) {
	har := `{"log":{"entries":[` +
		`{"request":{"url":"https://compraonline.bonpreuesclat.cat/x","headers":[{"name":"cookie","value":"global_sid=SECURE; aws-waf-token=SECURE"}]}},` +
		`{"request":{"url":"http://compraonline.bonpreuesclat.cat/x","headers":[{"name":"cookie","value":"global_sid=INSECURE; aws-waf-token=INSECURE"}]}}` +
		`]}}`
	s, err := ParseHAR([]byte(har))
	if err != nil || s.Cookie != "global_sid=SECURE; aws-waf-token=SECURE" {
		t.Fatalf("ParseHAR = %+v, %v; want HTTPS session", s, err)
	}
}

func TestParseHARNoSession(t *testing.T) {
	anon := `{"log":{"entries":[{"request":{"url":"https://www.compraonline.bonpreuesclat.cat/","headers":[{"name":"Cookie","value":"VISITORID=abc"}]}}]}}`
	if _, err := ParseHAR([]byte(anon)); err == nil {
		t.Error("expected error when no authenticated cookie is present")
	}
}

func TestParseHARSanitized(t *testing.T) {
	// Bonpreu requests present, but no Cookie headers at all → "Export HAR (sanitized)".
	sanitized := `{"log":{"entries":[{"request":{"url":"https://www.compraonline.bonpreuesclat.cat/api/cart/v1/carts/active","headers":[{"name":"accept","value":"application/json"}]}}]}}`
	_, err := ParseHAR([]byte(sanitized))
	if err == nil {
		t.Fatal("expected error for a sanitized HAR")
	}
	if !strings.Contains(err.Error(), "sanititzat") {
		t.Errorf("expected sanitized-HAR hint, got: %v", err)
	}
}

func TestParseHARNoBonpreuRequests(t *testing.T) {
	other := `{"log":{"entries":[{"request":{"url":"https://example.com/","headers":[{"name":"cookie","value":"x=1"}]}}]}}`
	_, err := ParseHAR([]byte(other))
	if err == nil {
		t.Fatal("expected error when HAR has no Bonpreu requests")
	}
	if !strings.Contains(err.Error(), "no compraonline.bonpreuesclat.cat requests") {
		t.Errorf("expected no-bonpreu-requests hint, got: %v", err)
	}
}

func TestParseHARForeignHostMentioningDomainIsSkipped(t *testing.T) {
	// A foreign request whose HOST is evil.example but whose query merely mentions
	// the Bonpreu domain must NOT be treated as Bonpreu — its cookie must be ignored.
	// (Substring matching on the whole URL would wrongly import this cookie.)
	har := `{"log":{"entries":[{"request":{"url":"https://evil.example/collect?ref=https://www.compraonline.bonpreuesclat.cat/","headers":[{"name":"cookie","value":"global_sid=STOLEN; aws-waf-token=STOLEN"}]}}]}}`
	_, err := ParseHAR([]byte(har))
	if err == nil || !strings.Contains(err.Error(), "no compraonline.bonpreuesclat.cat requests") {
		t.Errorf("foreign host mentioning the domain must be skipped, got %v", err)
	}
}

func TestParseHARAcceptsKnownBonpreuWebHosts(t *testing.T) {
	for _, host := range []string{"compraonline.bonpreuesclat.cat", "www.compraonline.bonpreuesclat.cat", "bonpreuesclat.cat", "www.bonpreuesclat.cat"} {
		har := `{"log":{"entries":[{"request":{"url":"https://` + host + `/x","headers":[{"name":"cookie","value":"global_sid=a; aws-waf-token=b"}]}}]}}`
		if _, err := ParseHAR([]byte(har)); err != nil {
			t.Errorf("host %q should be accepted as Bonpreu, got %v", host, err)
		}
	}
	// Foreign hosts and arbitrary Bonpreu subdomains are not trusted for auth cookies.
	for _, host := range []string{"bonpreuesclat.cat.evil.example", "evilbonpreuesclat.cat", "attacker.bonpreuesclat.cat"} {
		har := `{"log":{"entries":[{"request":{"url":"https://` + host + `/x","headers":[{"name":"cookie","value":"global_sid=a; aws-waf-token=b"}]}}]}}`
		if _, err := ParseHAR([]byte(har)); err == nil {
			t.Errorf("look-alike host %q must be rejected", host)
		}
	}
}

func TestCookieLooksAuthed(t *testing.T) {
	tests := []struct {
		name   string
		cookie string
		want   bool
	}{
		{name: "anonymous", cookie: "VISITORID=x"},
		{name: "valid session", cookie: "global_sid=a; aws-waf-token=b", want: true},
		{name: "additional cookies", cookie: "VISITORID=x; aws-waf-token=b; global_sid=a", want: true},
		{name: "lookalike names", cookie: "xglobal_sid=a; xaws-waf-token=b"},
		{name: "markers inside values", cookie: "visitor=global_sid=a; other=aws-waf-token=b"},
		{name: "empty session value", cookie: "global_sid=; aws-waf-token=b"},
		{name: "empty WAF value", cookie: "global_sid=a; aws-waf-token="},
		{name: "conflicting session values", cookie: "global_sid=a; global_sid=b; aws-waf-token=c"},
		{name: "conflicting WAF values", cookie: "global_sid=a; aws-waf-token=b; aws-waf-token=c"},
		{name: "empty duplicate session value", cookie: "global_sid=a; global_sid=; aws-waf-token=c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CookieLooksAuthed(tt.cookie); got != tt.want {
				t.Errorf("CookieLooksAuthed(%q) = %t, want %t", tt.cookie, got, tt.want)
			}
		})
	}
}

func TestParseHARBadJSON(t *testing.T) {
	if _, err := ParseHAR([]byte("not json")); err == nil {
		t.Error("malformed HAR must error")
	}
}

func TestParseHARNoAuthedCookie(t *testing.T) {
	// A bonpreu entry whose cookie lacks the auth markers → no session found.
	har := `{"log":{"entries":[{"request":{"url":"https://compraonline.bonpreuesclat.cat/x","headers":[{"name":"cookie","value":"visitorId=1"}]}}]}}`
	if _, err := ParseHAR([]byte(har)); err == nil || !strings.Contains(err.Error(), "no authenticated") {
		t.Errorf("expected no-authed-cookie error, got %v", err)
	}
}

func TestParseHARSkipsNonBonpreu(t *testing.T) {
	har := `{"log":{"entries":[` +
		`{"request":{"url":"https://other.example/x","headers":[{"name":"cookie","value":"global_sid=a; aws-waf-token=b"}]}},` +
		`{"request":{"url":"https://compraonline.bonpreuesclat.cat/x?regionId=rr","headers":[{"name":"cookie","value":"global_sid=a; aws-waf-token=b"}]}}` +
		`]}}`
	s, err := ParseHAR([]byte(har))
	if err != nil || s.RegionID != "rr" {
		t.Fatalf("ParseHAR skipping non-bonpreu = %+v, err %v", s, err)
	}
}

func TestParseHARIgnoresRelatedButDifferentRegionQueryKeys(t *testing.T) {
	har := `{"log":{"entries":[{"request":{"url":"https://compraonline.bonpreuesclat.cat/x?customerRegionId=wrong-region","headers":[{"name":"cookie","value":"global_sid=a; aws-waf-token=b"}]}}]}}`
	s, err := ParseHAR([]byte(har))
	if err != nil {
		t.Fatalf("ParseHAR: %v", err)
	}
	if s.RegionID != "" {
		t.Fatalf("region = %q; want no region from a different query key", s.RegionID)
	}
}

func TestParseHARIgnoresConflictingRegionAliases(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  string
	}{
		{"regionId=region-a&region_id=region-b", ""},
		{"regionId=region-a&region_id=region-a", "region-a"},
	} {
		har := `{"log":{"entries":[{"request":{"url":"https://compraonline.bonpreuesclat.cat/x?` + tc.query + `","headers":[{"name":"cookie","value":"global_sid=a; aws-waf-token=b"}]}}]}}`
		s, err := ParseHAR([]byte(har))
		if err != nil {
			t.Fatalf("ParseHAR(%q): %v", tc.query, err)
		}
		if s.RegionID != tc.want {
			t.Errorf("ParseHAR(%q) region = %q, want %q", tc.query, s.RegionID, tc.want)
		}
	}
}

func TestParseHARIgnoresEmptyRegionValue(t *testing.T) {
	har := `{"log":{"entries":[{"request":{"url":"https://compraonline.bonpreuesclat.cat/x?regionId=&region_id=region-a","headers":[{"name":"cookie","value":"global_sid=a; aws-waf-token=b"}]}}]}}`
	s, err := ParseHAR([]byte(har))
	if err != nil {
		t.Fatalf("ParseHAR: %v", err)
	}
	if s.RegionID != "region-a" {
		t.Fatalf("region = %q; want the non-empty alias", s.RegionID)
	}
}
