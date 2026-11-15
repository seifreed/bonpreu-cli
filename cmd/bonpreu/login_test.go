package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/seifreed/bonpreu-cli/internal/client"
)

func TestSaveAndVerify(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		cookie     string
		json       bool
		wantErr    string
		wantOutput string
	}{
		{name: "authenticated JSON", status: http.StatusOK, cookie: "global_sid=s; aws-waf-token=t", json: true, wantOutput: `"authenticated": true`},
		{name: "unauthenticated cookie", status: http.StatusUnauthorized, cookie: "global_sid=s; aws-waf-token=t", wantErr: "NOT authenticated"},
		{name: "verification failure", status: http.StatusInternalServerError, cookie: "global_sid=s; aws-waf-token=t", wantErr: "could not verify session"},
		{name: "missing cookie", status: http.StatusOK, wantErr: "carried no cookie"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/order/v6/orders") {
					w.WriteHeader(tc.status)
					return
				}
				http.NotFound(w, r)
			}))
			defer srv.Close()
			useServer(t, srv, false)
			cf := &common{jsonOut: tc.json}
			out, err := capture(t, func() error {
				return saveAndVerify(cf, client.Session{Cookie: tc.cookie, RegionID: "r1"})
			})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || !strings.Contains(out, tc.wantOutput) {
				t.Fatalf("output = %q, err %v", out, err)
			}
		})
	}
}
