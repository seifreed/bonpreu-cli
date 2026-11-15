package client

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/seifreed/bonpreu-cli/internal/config"
)

const sessionFile = "session.json"

// Session is the machine-managed auth cache: the browser cookie that carries the
// login (global_sid) and the AWS WAF clearance (aws-waf-token), plus the region
// the session resolved to. Written by import-har / set-cookie, read by the
// authenticated commands.
type Session struct {
	Cookie   string `json:"cookie"`
	RegionID string `json:"region_id,omitempty"`
}

// LoadSession reads the cached session; a missing file yields a zero Session.
func LoadSession() (Session, error) {
	var s Session
	if err := config.Load(sessionFile, &s); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return s, fmt.Errorf("read cached session: %w", err)
	}
	return s, nil
}

// SaveSession persists the session cache (0600).
func SaveSession(s Session) error {
	return config.Save(sessionFile, s)
}

// LoadAuth loads the cached session (and config fallbacks) onto the client so
// its requests carry the browser cookie and region. Returns false, nil when
// there is no cookie; unreadable state is returned as an error. The name signals
// the load side effect; it is not a pure predicate.
func (c *Client) LoadAuth() (bool, error) {
	s, err := LoadSession()
	if err != nil {
		return false, err
	}
	cfg, err := config.LoadConfig()
	if err != nil {
		return false, fmt.Errorf("read authentication config: %w", err)
	}
	if s.Cookie == "" {
		s.Cookie = cfg.Auth.Cookie
	}
	if s.RegionID == "" {
		s.RegionID = cfg.Defaults.RegionID
	}
	if s.Cookie != "" {
		c.Cookie = s.Cookie
	}
	if s.RegionID != "" {
		c.RegionID = s.RegionID
	}
	// The session cookie carries the login (global_sid) + WAF clearance. If BaseURL
	// has been pointed off Bonpreu (BONPREU_BASE_URL — a debugging proxy/mock), warn
	// before any authed request ships that secret to a non-Bonpreu host, so a typo
	// or a poisoned env doesn't silently exfiltrate the session. The override stays
	// allowed (localhost proxies are a legitimate use); it's just no longer silent.
	c.warnExternalCookieHost(c.Cookie)
	return c.Cookie != "", nil
}

func (c *Client) warnExternalCookieHost(cookie string) {
	if cookie == "" || c.BaseURL == "" {
		return
	}
	host := ""
	trusted := false
	if u, err := url.Parse(c.BaseURL); err == nil {
		host = u.Hostname()
		trusted = isBonpreuHost(host) && (u.Port() == "" || u.Port() == "443")
	}
	if !trusted && c.Logger != nil {
		c.Logger.Warn("session cookie targets an untrusted host",
			"event_name", "security.cookie_external_host", "component", "client", "host", host)
	}
}

func (c *Client) validateCookieTransport(cookie, targetURL string) error {
	if cookie == "" {
		return nil
	}
	u, err := url.Parse(targetURL)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("cannot safely send session cookie to invalid target URL")
	}
	host := u.Hostname()
	if strings.EqualFold(u.Scheme, "https") {
		if (isBonpreuHost(host) && (u.Port() == "" || u.Port() == "443")) || c.AllowExternalCookieHost || strings.EqualFold(host, "localhost") {
			return nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil
		}
		return fmt.Errorf("refusing to send session cookie to untrusted host %q; explicitly allow only trusted staging hosts", host)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("refusing to send session cookie over non-HTTPS transport to %q", host)
}
