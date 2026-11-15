package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/seifreed/bonpreu-cli/internal/client"
)

const (
	maxHARInputBytes    int64 = 64 << 20
	maxCookieInputBytes int64 = 64 << 10
)

var (
	persistSession = client.SaveSession
	openHARInput   = func(path string) (io.ReadCloser, error) { return os.Open(path) }
)

// cmdImportHar imports a browser session from a DevTools HAR export: the Cookie
// header (login + AWS WAF clearance) and the resolved region. This is the
// preferred way to bring your own authenticated session — Bonpreu's first login
// is browser-only (captcha / SSO), but the cookie it mints drives the CLI.
func cmdImportHar(args []string) error {
	fs := flag.NewFlagSet("import-har", flag.ExitOnError)
	file := fs.String("file", "", "path to the .har export ('-' for stdin)")
	parseFlags(fs, args)
	if fs.NArg() != 0 || *file == "" {
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
	stdin := fs.Bool("stdin", false, "read the cookie from stdin (required)")
	parseFlags(fs, args)
	if !*stdin || fs.NArg() != 0 {
		return fmt.Errorf("usage: bonpreu set-cookie --stdin")
	}
	b, err := readLimited(os.Stdin, maxCookieInputBytes, "cookie input")
	if err != nil {
		return err
	}
	cookie := strings.TrimSpace(string(b))
	if cookie == "" {
		return fmt.Errorf("usage: bonpreu set-cookie --stdin")
	}
	if !client.CookieLooksAuthed(cookie) {
		return fmt.Errorf("cookie lacks the required global_sid + aws-waf-token session keys; the previous session was kept")
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
	return writeStdout("ok — authenticated (region %s)\n", terminalText(firstNonEmpty(cl.RegionID, "default")))
}

func readFileOrStdin(path string) ([]byte, error) {
	if path == "-" {
		return readLimited(os.Stdin, maxHARInputBytes, "HAR input")
	}
	f, err := openHARInput(path)
	if err != nil {
		return nil, err
	}
	b, readErr := readLimited(f, maxHARInputBytes, "HAR input")
	if closeErr := f.Close(); closeErr != nil {
		return nil, errors.Join(readErr, closeErr)
	}
	return b, readErr
}

func readLimited(r io.Reader, maxBytes int64, label string) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > maxBytes {
		return nil, fmt.Errorf("%s exceeds the %d-byte limit", label, maxBytes)
	}
	return b, nil
}
