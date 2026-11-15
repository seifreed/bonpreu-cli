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
