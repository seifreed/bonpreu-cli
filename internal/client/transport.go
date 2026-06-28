package client

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	utls "github.com/refraction-networking/utls"
)

// newChromeTransport returns an http.Transport whose TLS handshake mimics
// Chrome's JA3 fingerprint via uTLS. Akamai Bot Manager scores the client's TLS
// fingerprint, and a vanilla Go client's JA3 is an easy bot tell — so on every
// call (reads and the authenticated leg) we present Chrome's ClientHello.
//
// We pin ALPN to http/1.1 so a standard http.Transport can carry the connection
// without HTTP/2 plumbing. Classic JA3 hashes the extension *types*, not the
// ALPN protocol list, so the fingerprint stays Chrome's.
func newChromeTransport() (*http.Transport, error) {
	// Validate once that we can build the spec, failing fast to the stdlib path.
	if _, err := chromeSpecHTTP1(); err != nil {
		return nil, err
	}
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		raw, err := (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		// Fresh spec per dial — ApplyPreset may mutate it (GREASE/randomization).
		spec, err := chromeSpecHTTP1()
		if err != nil {
			raw.Close()
			return nil, err
		}
		u := utls.UClient(raw, &utls.Config{ServerName: host}, utls.HelloCustom)
		if err := u.ApplyPreset(&spec); err != nil {
			raw.Close()
			return nil, fmt.Errorf("utls preset: %w", err)
		}
		if err := u.HandshakeContext(ctx); err != nil {
			raw.Close()
			return nil, fmt.Errorf("utls handshake: %w", err)
		}
		return u, nil
	}
	return &http.Transport{
		DialTLSContext:        dial,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: time.Second,
	}, nil
}

func chromeSpecHTTP1() (utls.ClientHelloSpec, error) {
	spec, err := utls.UTLSIdToSpec(utls.HelloChrome_Auto)
	if err != nil {
		return spec, err
	}
	for _, ext := range spec.Extensions {
		if a, ok := ext.(*utls.ALPNExtension); ok {
			a.AlpnProtocols = []string{"http/1.1"}
		}
	}
	return spec, nil
}
