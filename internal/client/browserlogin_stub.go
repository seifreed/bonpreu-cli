//go:build !browserlogin

package client

import "fmt"

// LoginViaBrowser is a stub in the default (dep-light, static) build. The real
// implementation drives Chrome via chromedp and is compiled only with
// `-tags browserlogin` (it needs Chrome installed at runtime).
func LoginViaBrowser(_ string) (Session, error) {
	return Session{}, fmt.Errorf("this build has no open-browser login — " +
		"use `bonpreu login --from-browser <chrome|firefox|safari|edge|brave>`, " +
		"or rebuild with `go build -tags browserlogin`")
}
