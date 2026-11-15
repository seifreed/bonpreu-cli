//go:build !browserlogin

package main

import (
	"testing"
)

func TestRunDispatchesLoginInDefaultBuild(t *testing.T) {
	useServer(t, errorServer(t), false)
	var code int
	_, err := capture(t, func() error { code = run([]string{"login"}); return nil })
	if err != nil || code != 1 {
		t.Fatalf("login dispatch exit=%d, err=%v; want default-build login failure", code, err)
	}
}
