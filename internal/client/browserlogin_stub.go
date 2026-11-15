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

// CheckoutInBrowser is unavailable in the default build, which does not include chromedp.
func CheckoutInBrowser(_ string, _ string) error {
	return fmt.Errorf("interactive checkout needs a build with `-tags browserlogin` and Chrome")
}
