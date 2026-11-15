package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func withStdin(t *testing.T, content string, fn func()) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "stdin")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	old := os.Stdin
	os.Stdin = f
	defer func() { os.Stdin = old }()
	fn()
}

func privateTestConfigDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "config")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BONPREU_CONFIG_DIR", dir)
	return dir
}

// withFailingStdout points os.Stdout at a closed file so any write fails, to
// exercise the emit error-return paths.
func withFailingStdout(t *testing.T, fn func() error) error {
	t.Helper()
	_, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	w.Close() // closed → writes return an error
	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old }()
	return fn()
}

// unwritableConfigDir returns a BONPREU_CONFIG_DIR that cannot be created (a path
// nested under a regular file), so SaveSession fails.
func unwritableConfigDir(t *testing.T) string {
	t.Helper()
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(blocker, "sub")
}

// errorServer returns 500 for everything, to exercise handler error branches.
func errorServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
}
