package main

import (
	"flag"
	"fmt"

	"github.com/seifreed/bonpreu-cli/internal/client"
)

var (
	readLoginBrowser = client.CookiesFromBrowser
	openLoginBrowser = client.LoginViaBrowser
)

// cmdLogin bridges a browser login to the CLI's session cache without DevTools.
// `login --from-browser <name>` lifts the cookie from a browser's cookie store;
// bare `login` opens a browser for a fresh login and captures the session (only
// in builds with -tags browserlogin). Both end at saveAndVerify.
func cmdLogin(args []string) error {
	return cmdLoginWith(args, readLoginBrowser, openLoginBrowser)
}

func cmdLoginWith(args []string, readBrowser func(string) (client.Session, error), openBrowser func(string) (client.Session, error)) error {
	fs, cf := newCommonFlags("login")
	fromBrowser := fs.String("from-browser", "", "read the session from a browser's cookie store: chrome|chromium|firefox|safari|edge|brave (empty = any installed)")
	parseFlags(fs, args)

	fromBrowserSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "from-browser" {
			fromBrowserSet = true
		}
	})

	var s client.Session
	var err error
	source := "interactive_browser"
	if fromBrowserSet {
		source = "browser_store"
	}
	stderrInfo("auth.login.started", "browser login started", "operation", cf.operation, "source", source)
	if fromBrowserSet {
		s, err = readBrowser(*fromBrowser)
	} else {
		s, err = openBrowser(newClient(cf).BaseURL + "/")
	}
	if err != nil {
		return err
	}
	return saveAndVerify(cf, s)
}

// saveAndVerify persists a freshly captured session, then confirms the cookie is
// actually accepted by hitting an authenticated endpoint — mirrors cmdWhoami so
// `login` proves itself rather than silently saving a dud cookie.
func saveAndVerify(cf *common, s client.Session) error {
	if err := client.SaveSession(s); err != nil {
		return err
	}
	cl := newClient(cf)
	loaded, err := cl.LoadAuth()
	if err != nil {
		return err
	}
	if !loaded {
		return fmt.Errorf("session saved but carried no cookie")
	}
	ok, err := cl.CheckAuth()
	if err != nil {
		return fmt.Errorf("could not verify session: %w", err)
	}
	if !ok {
		return fmt.Errorf("cookie saved but NOT authenticated — it may be expired or anonymous; log in again")
	}
	stderrInfo("auth.session.verified", "saved session was accepted", "operation", cf.operation)
	if done, err := emitStructured(cf, map[string]any{"authenticated": ok, "region": cl.RegionID}); done {
		return err
	}
	fmt.Printf("ok — authenticated (region %s)\n", firstNonEmpty(cl.RegionID, "default"))
	return nil
}
