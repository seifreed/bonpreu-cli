package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/seifreed/bonpreu-cli/internal/client"
)

var persistSession = client.SaveSession

// cmdImportHar imports a browser session from a DevTools HAR export: the Cookie
// header (login + AWS WAF clearance) and the resolved region. This is the
// preferred way to bring your own authenticated session — Bonpreu's first login
// is browser-only (captcha / SSO), but the cookie it mints drives the CLI.
func cmdImportHar(args []string) error {
	fs := flag.NewFlagSet("import-har", flag.ExitOnError)
	file := fs.String("file", "", "path to the .har export ('-' for stdin)")
	parseFlags(fs, args)
	if *file == "" {
		return fmt.Errorf("usage: bonpreu import-har --file tienda.har")
	}
	data, err := readFileOrStdin(*file)
	if err != nil {
		return err
	}
	s, err := client.ParseHAR(data)
	if err != nil {
		return err
	}
	if err := client.SaveHARSession(s); err != nil {
		return err
	}
	stderrInfo("auth.har.imported", "HAR session imported",
		"operation", "import-har", "region_configured", s.RegionID != "")
	if s.RegionID == "" {
		stderrWarn("auth.har.region_missing", "no region found in the HAR; cart/checkout use the server default until a region is configured", "operation", "import-har")
	}
	return nil
}

// cmdSetCookie seeds a raw Cookie header (copied from DevTools) into the session
// cache — the manual alternative to import-har.
func cmdSetCookie(args []string) error {
	fs := flag.NewFlagSet("set-cookie", flag.ExitOnError)
	stdin := fs.Bool("stdin", false, "read the cookie from stdin")
	parseFlags(fs, args)
	var cookie string
	if *stdin {
		if fs.NArg() != 0 {
			return fmt.Errorf("usage: bonpreu set-cookie --stdin")
		}
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		cookie = strings.TrimSpace(string(b))
	} else if fs.NArg() > 0 {
		cookie = strings.Join(fs.Args(), " ")
	}
	if cookie == "" {
		return fmt.Errorf("usage: bonpreu set-cookie '<cookie header>'  (or --stdin)")
	}
	s, err := client.LoadSession()
	if err != nil {
		return err
	}
	s.Cookie = cookie
	if err := persistSession(s); err != nil {
		return err
	}
	stderrInfo("auth.cookie.saved", "session cookie saved", "operation", "set-cookie")
	if !client.CookieLooksAuthed(cookie) {
		stderrWarn("auth.cookie.may_be_anonymous", "cookie lacks expected session keys; verify with bonpreu whoami", "operation", "set-cookie")
	}
	return nil
}

// cmdWhoami verifies the session works by fetching the active cart (an
// authenticated endpoint): success means the cookie is accepted.
func cmdWhoami(args []string) error {
	fs, cf := newCommonFlags("whoami")
	parseFlags(fs, args)
	cl, err := authedClient(cf)
	if err != nil {
		return err
	}
	ok, err := cl.CheckAuth()
	if err != nil {
		return fmt.Errorf("could not verify session: %w", err)
	}
	if !ok {
		return fmt.Errorf("cookie present but NOT authenticated (the session expired or is anonymous) — re-export a fresh HAR while logged in")
	}
	stderrInfo("auth.session.verified", "stored session was accepted", "operation", cf.operation)
	if done, err := emitStructured(cf, map[string]any{"authenticated": ok, "region": cl.RegionID}); done {
		return err
	}
	fmt.Printf("ok — authenticated (region %s)\n", terminalText(firstNonEmpty(cl.RegionID, "default")))
	return nil
}

func readFileOrStdin(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}
