package main

import (
	"flag"
	"fmt"

	"github.com/seifreed/bonpreu-cli/internal/client"
	"github.com/seifreed/bonpreu-cli/internal/config"
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
		cl, _ := newClient(cf)
		s, err = openBrowser(cl.BaseURL + "/")
	}
	if err != nil {
		return err
	}
	return saveAndVerify(cf, s)
}

// saveAndVerify confirms a freshly captured session before persisting it, so a
// stale browser cookie cannot replace a previously valid session.
func saveAndVerify(cf *common, s client.Session) error {
	if _, err := config.LoadConfig(); err != nil {
		return fmt.Errorf("read authentication config: %w", err)
	}
	if s.Cookie == "" {
		return fmt.Errorf("captured session carried no cookie")
	}
	cl, _ := newClient(cf)
	if s.RegionID != "" {
		cl.RegionID = s.RegionID
	}
	ok, err := cl.CheckAuthWithCookie(s.Cookie)
	if err != nil {
		return fmt.Errorf("could not verify session: %w", err)
	}
	if !ok {
		return fmt.Errorf("cookie NOT authenticated — the previous session was kept; log in again")
	}
	if err := persistSession(s); err != nil {
		return err
	}
	stderrInfo("auth.session.verified", "saved session was accepted", "operation", cf.operation)
	if done, err := emitStructured(cf, map[string]any{"authenticated": ok, "region": cl.RegionID}); done {
		if err != nil {
			return fmt.Errorf("session saved, but confirmation output failed: %w", err)
		}
		return nil
	}
	if err := writeStdout("ok — authenticated (region %s)\n", terminalText(firstNonEmpty(cl.RegionID, "default"))); err != nil {
		return fmt.Errorf("session saved, but confirmation output failed: %w", err)
	}
	return nil
}
