//go:build !browserlogin

package main

import (
	"strings"
	"testing"
)

func TestCmdLoginDefaultBuildExplainsBrowserRequirement(t *testing.T) {
	useServer(t, errorServer(t), false)
	err := cmdLogin(nil)
	if err == nil || !strings.Contains(err.Error(), "no open-browser login") {
		t.Fatalf("cmdLogin error = %v, want default-build explanation", err)
	}
}
