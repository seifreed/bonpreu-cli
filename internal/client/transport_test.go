package client

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"
)

type closeErrorConn struct {
	net.Conn
	err error
}

func (c closeErrorConn) Close() error { return c.err }

func TestCloseConnWithCausePreservesCleanupError(t *testing.T) {
	cause := errors.New("TLS handshake failed")
	closeErr := errors.New("socket close failed")
	err := closeConnWithCause(closeErrorConn{err: closeErr}, cause)
	if !errors.Is(err, cause) || !errors.Is(err, closeErr) {
		t.Fatalf("closeConnWithCause error = %v; want both original and cleanup failures", err)
	}
}

func TestChromeDialBranches(t *testing.T) {
	ctx := context.Background()
	spec := chromeSpecHTTP1
	cfg := func(host string) *utls.Config { return &utls.Config{ServerName: host} }

	// SplitHostPort error: address with no port.
	if _, err := chromeDial(ctx, "tcp", "noport", spec, cfg); err == nil {
		t.Error("address without a port should fail SplitHostPort")
	}
	// DialContext error: port 0 is not connectable.
	if _, err := chromeDial(ctx, "tcp", "127.0.0.1:0", spec, cfg); err == nil {
		t.Error("dial to port 0 should fail")
	}
	// spec builder error: needs a live endpoint first, so dial the TLS server.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	addr := srv.Listener.Addr().String()
	badSpec := func() (utls.ClientHelloSpec, error) { return utls.ClientHelloSpec{}, errors.New("boom") }
	if _, err := chromeDial(ctx, "tcp", addr, badSpec, cfg); err == nil {
		t.Error("a spec build error should propagate")
	}
	badConfig := func(host string) *utls.Config { return &utls.Config{ServerName: host, NextProtos: []string{""}} }
	if _, err := chromeDial(ctx, "tcp", addr, spec, badConfig); err == nil || !strings.Contains(err.Error(), "utls preset") {
		t.Errorf("invalid TLS preset error = %v, want ApplyPreset failure", err)
	}
	// Successful handshake: trust the test cert via InsecureSkipVerify.
	skip := func(host string) *utls.Config { return &utls.Config{ServerName: host, InsecureSkipVerify: true} }
	conn, err := chromeDial(ctx, "tcp", addr, spec, skip)
	if err != nil {
		t.Fatalf("handshake against the test TLS server should succeed: %v", err)
	}
	conn.Close()
}

func TestChromeSpecFailureSeam(t *testing.T) {
	old := chromeHelloID
	chromeHelloID = utls.ClientHelloID{} // unknown id → UTLSIdToSpec errors
	defer func() { chromeHelloID = old }()

	if _, err := chromeSpecHTTP1(); err == nil {
		t.Fatal("a bogus hello id should fail the spec build")
	}
	if _, err := newChromeTransport(); err == nil {
		t.Error("newChromeTransport should fail when the spec build fails")
	}

	c := New()
	if c.transportErr == nil {
		t.Fatal("New should record transportErr when the transport can't be built")
	}
	// First request logs the fallback once.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c.BaseURL = srv.URL
	c.HTTP = srv.Client()
	var logs bytes.Buffer
	c.Logger = testLogger(&logs)
	_ = c.DoJSON("GET", srv.URL+"/x", nil, nil)
	if !strings.Contains(logs.String(), "event_name=transport.fallback") {
		t.Error("a built-in transport failure should log the stdlib fallback")
	}
}

// Exercise the uTLS transport's dial closure against a real TLS server. The
// handshake fails the self-signed cert check, but the dial path executes.
func TestChromeTransportDial(t *testing.T) {
	tr, err := newChromeTransport()
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	hc := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	if resp, err := hc.Get(srv.URL); err == nil {
		resp.Body.Close()
	}
}

func TestChromeTransportDialRefused(t *testing.T) {
	tr, err := newChromeTransport()
	if err != nil {
		t.Fatal(err)
	}
	hc := &http.Client{Transport: tr, Timeout: 2 * time.Second}
	if _, err := hc.Get("https://127.0.0.1:0/"); err == nil {
		t.Error("HTTPS dial to port 0 should fail in the uTLS dialer")
	}
}
