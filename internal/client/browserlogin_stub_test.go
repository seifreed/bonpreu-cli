//go:build !browserlogin

package client

import (
	"strings"
	"testing"
)

func TestLoginViaBrowserDefaultBuildExplainsBuildTag(t *testing.T) {
	_, err := LoginViaBrowser("https://example.invalid/")
	if err == nil || !strings.Contains(err.Error(), "no open-browser login") {
		t.Fatalf("LoginViaBrowser error = %v", err)
	}
}

func TestCheckoutInBrowserDefaultBuildExplainsBuildTag(t *testing.T) {
	if err := CheckoutInBrowser("https://www.bonpreuesclat.cat", "global_sid=x; aws-waf-token=y"); err == nil || !strings.Contains(err.Error(), "-tags browserlogin") {
		t.Fatalf("CheckoutInBrowser error = %v", err)
	}
}
