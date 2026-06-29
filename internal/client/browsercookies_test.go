package client

import "testing"

func TestBuildCookieHeader(t *testing.T) {
	got := buildCookieHeader([]nameVal{
		{"global_sid", "SID"},
		{"aws-waf-token", "WAF"},
		{"VISITORID", "abc"},
	})
	// Deterministic (sorted by name) join.
	want := "VISITORID=abc; aws-waf-token=WAF; global_sid=SID"
	if got != want {
		t.Errorf("buildCookieHeader = %q, want %q", got, want)
	}
	if !CookieLooksAuthed(got) {
		t.Error("assembled header should look authed (has global_sid + aws-waf-token)")
	}
	if buildCookieHeader(nil) != "" {
		t.Error("empty input should yield empty header")
	}
}
