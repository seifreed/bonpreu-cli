package config

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDirHomeFallback(t *testing.T) {
	t.Setenv("BONPREU_CONFIG_DIR", "") // force the home-dir path
	t.Setenv("HOME", "/home/tester")
	if got := Dir(); got != filepath.Join("/home/tester", ".bonpreu") {
		t.Errorf("Dir() = %q, want ~/.bonpreu", got)
	}
}

type failingTemp struct {
	writeErr error
	closeErr error
	closed   bool
}

func (f *failingTemp) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return len(p), nil
}

func (f *failingTemp) Close() error {
	f.closed = true
	return f.closeErr
}

func TestWriteTempClosesAndReturnsFailures(t *testing.T) {
	writeFailure := errors.New("disk write failed")
	closeFailure := errors.New("close also failed")
	tmp := &failingTemp{writeErr: writeFailure, closeErr: closeFailure}
	if err := writeTemp(tmp, []byte("secret")); !errors.Is(err, writeFailure) || !errors.Is(err, closeFailure) {
		t.Fatalf("writeTemp error = %v, want write and close failures", err)
	}
	if !tmp.closed {
		t.Fatal("temporary file must be closed after write failure")
	}

	closeOnlyFailure := errors.New("close failed")
	tmp = &failingTemp{closeErr: closeOnlyFailure}
	if err := writeTemp(tmp, []byte("data")); !errors.Is(err, closeOnlyFailure) {
		t.Fatalf("writeTemp error = %v, want close failure", err)
	}
	if !tmp.closed {
		t.Fatal("temporary file must be closed after successful write")
	}

	tmp = &failingTemp{}
	if err := writeTemp(tmp, []byte("data")); err != nil || !tmp.closed {
		t.Fatalf("successful write/close = %v, closed %v", err, tmp.closed)
	}
}

func TestSaveReportsTemporaryCleanupFailure(t *testing.T) {
	dir := privateConfigTestDir(t)
	tmpName := "session.json.tmp-test"
	tmpPath := filepath.Join(dir, tmpName)
	if err := os.Mkdir(tmpPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpPath, "partial"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeFailure := errors.New("disk write failed")
	err := save("session.json", map[string]string{"cookie": "secret"}, func(_ *os.Root, _ string) (io.WriteCloser, string, error) {
		return &failingTemp{writeErr: writeFailure}, tmpName, nil
	})
	if !errors.Is(err, writeFailure) || !strings.Contains(err.Error(), "remove temporary config file") {
		t.Fatalf("save error = %v; want write and cleanup errors", err)
	}
}

var _ io.WriteCloser = (*failingTemp)(nil)

func TestDirNoHome(t *testing.T) {
	t.Setenv("BONPREU_CONFIG_DIR", "")
	t.Setenv("HOME", "") // UserHomeDir fails → bare .bonpreu fallback
	if got := Dir(); got != ".bonpreu" {
		t.Errorf("Dir() = %q, want .bonpreu fallback", got)
	}
}

func TestSaveWithoutHomeFailsInsteadOfWritingIntoCurrentDirectory(t *testing.T) {
	t.Setenv("BONPREU_CONFIG_DIR", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Chdir(t.TempDir())
	if err := Save("session.json", map[string]string{"cookie": "secret"}); err == nil || !strings.Contains(err.Error(), "set BONPREU_CONFIG_DIR") {
		t.Fatalf("Save without a home directory = %v; want explicit config-dir error", err)
	}
	if _, err := os.Stat(filepath.Join(".bonpreu", "session.json")); !os.IsNotExist(err) {
		t.Fatalf("relative session file stat error = %v; want no file to be created", err)
	}
}

func TestLoadMissingFileErrors(t *testing.T) {
	privateConfigTestDir(t)
	var v map[string]any
	if err := Load("does-not-exist.json", &v); err == nil {
		t.Error("Load of a missing file should error")
	}
}

func TestLoadReturnsConfigDirectoryOpenFailure(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BONPREU_CONFIG_DIR", filepath.Join(blocker, "nested"))
	var v map[string]any
	if err := Load("session.json", &v); err == nil {
		t.Fatal("Load should report an error when the config directory cannot be opened")
	}
}

func TestEnsureDirPropagatesStatError(t *testing.T) {
	privateConfigTestDir(t)
	want := errors.New("directory stat failed")
	old := configStat
	configStat = func(string) (os.FileInfo, error) { return nil, want }
	t.Cleanup(func() { configStat = old })

	if err := ensureDir(); !errors.Is(err, want) {
		t.Fatalf("ensureDir error = %v, want stat error", err)
	}
}

func TestSaveRechecksPermissionsOnOpenedRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ACLs are not represented by Go permission bits")
	}
	dir := privateConfigTestDir(t)
	old := configStat
	configStat = func(path string) (os.FileInfo, error) {
		info, err := old(path)
		if path == dir && err == nil {
			if err := os.Chmod(dir, 0o755); err != nil {
				return nil, err
			}
		}
		return info, err
	}
	t.Cleanup(func() { configStat = old })

	if err := Save("session.json", map[string]string{"cookie": "secret"}); err == nil || !strings.Contains(err.Error(), "must have mode 0700") {
		t.Fatalf("Save after permission change = %v; want opened-root permission failure", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "session.json")); !os.IsNotExist(err) {
		t.Fatalf("session file stat error = %v; want no secret written", err)
	}
}

func TestOpenPrivateRootReportsStatFailure(t *testing.T) {
	dir := privateConfigTestDir(t)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	old := openConfigRoot
	openConfigRoot = func(string) (*os.Root, error) { return root, nil }
	t.Cleanup(func() { openConfigRoot = old })

	if _, err := openPrivateRoot(); err == nil {
		t.Fatal("openPrivateRoot error = nil; want stat failure for closed root")
	}
}

func TestSaveReportsRootCloseFailure(t *testing.T) {
	privateConfigTestDir(t)
	want := errors.New("close config directory failed")
	old := closeConfigRoot
	closeConfigRoot = func(root *os.Root) error {
		if err := old(root); err != nil {
			return err
		}
		return want
	}
	t.Cleanup(func() { closeConfigRoot = old })
	if err := Save("session.json", map[string]string{"cookie": "secret"}); !errors.Is(err, want) {
		t.Fatalf("save error = %v, want root close failure", err)
	}
}

func TestCreateTempInRootReportsRandomFailure(t *testing.T) {
	want := errors.New("random source failed")
	old := configRandomRead
	configRandomRead = func([]byte) (int, error) { return 0, want }
	t.Cleanup(func() { configRandomRead = old })

	if _, _, err := createTempInRoot(nil, "session.json.tmp-"); !errors.Is(err, want) {
		t.Fatalf("createTempInRoot error = %v, want random source error", err)
	}
}

func TestLoadPropagatesOpenRootError(t *testing.T) {
	privateConfigTestDir(t)
	want := errors.New("open config directory failed")
	old := openConfigRoot
	openConfigRoot = func(string) (*os.Root, error) { return nil, want }
	t.Cleanup(func() { openConfigRoot = old })

	var v map[string]any
	if err := Load("session.json", &v); !errors.Is(err, want) {
		t.Fatalf("Load error = %v, want open-root error", err)
	}
}

func TestLoadConfigPropagatesOpenRootError(t *testing.T) {
	privateConfigTestDir(t)
	want := errors.New("open config directory failed")
	old := openConfigRoot
	openConfigRoot = func(string) (*os.Root, error) { return nil, want }
	t.Cleanup(func() { openConfigRoot = old })
	if _, err := LoadConfig(); !errors.Is(err, want) {
		t.Fatalf("LoadConfig error = %v, want open-root error", err)
	}
}

func TestSavePropagatesOpenRootError(t *testing.T) {
	privateConfigTestDir(t)
	want := errors.New("open config directory failed")
	old := openConfigRoot
	openConfigRoot = func(string) (*os.Root, error) { return nil, want }
	t.Cleanup(func() { openConfigRoot = old })
	if err := Save("session.json", map[string]string{"cookie": "secret"}); !errors.Is(err, want) {
		t.Fatalf("Save error = %v, want open-root error", err)
	}
}

func TestReadFileErrorPreservesFailuresWithoutHidingCloseError(t *testing.T) {
	readErr := errors.New("read failed")
	closeErr := errors.New("close failed")
	if err := readFileError("session.json", readErr, nil); !errors.Is(err, readErr) {
		t.Fatalf("read error = %v, want original read error", err)
	}
	if err := readFileError("session.json", nil, closeErr); !errors.Is(err, closeErr) {
		t.Fatalf("close error = %v, want original close error", err)
	}
	combined := readFileError("session.json", readErr, closeErr)
	if !errors.Is(combined, readErr) || !errors.Is(combined, closeErr) {
		t.Fatalf("combined error = %v, want both causes", combined)
	}
	missingAndClose := readFileError("session.json", os.ErrNotExist, closeErr)
	if errors.Is(missingAndClose, os.ErrNotExist) || !errors.Is(missingAndClose, closeErr) {
		t.Fatalf("missing plus close error = %v; close error must prevent treating this as a missing session", missingAndClose)
	}
}

func TestLoadAndSaveRejectPathsOutsideConfigDir(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	if err := os.Mkdir(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BONPREU_CONFIG_DIR", configDir)

	for _, name := range []string{"../outside.json", filepath.Join("nested", "state.json"), ".."} {
		var v map[string]any
		if err := Load(name, &v); err == nil {
			t.Errorf("Load(%q) unexpectedly accepted a path outside the config file namespace", name)
		}
		if err := Save(name, map[string]any{"secret": true}); err == nil {
			t.Errorf("Save(%q) unexpectedly accepted a path outside the config file namespace", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "outside.json")); !os.IsNotExist(err) {
		t.Fatalf("path traversal created outside file, stat error = %v", err)
	}
}

func TestSaveMkdirFailure(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Config dir would have to be created *under* a regular file → MkdirAll fails.
	t.Setenv("BONPREU_CONFIG_DIR", filepath.Join(blocker, "sub"))
	if err := Save("session.json", map[string]any{"a": 1}); err == nil {
		t.Error("Save should fail when the config dir cannot be created")
	}
}

func TestSaveEncodeError(t *testing.T) {
	privateConfigTestDir(t)
	if err := Save("x.json", make(chan int)); err == nil {
		t.Error("an unmarshalable value should fail to save")
	}
}

func TestSaveReturnsCreateTempFailure(t *testing.T) {
	privateConfigTestDir(t)
	if err := Save("bad\x00name.json", map[string]any{"ok": true}); err == nil {
		t.Fatal("Save should propagate the temporary-file creation error")
	}
}

func TestSaveReturnsInjectedCreateTempFailure(t *testing.T) {
	privateConfigTestDir(t)
	want := errors.New("temporary-file creation failed")
	err := save("session.json", map[string]any{"cookie": "secret"}, func(*os.Root, string) (io.WriteCloser, string, error) {
		return nil, "", want
	})
	if !errors.Is(err, want) {
		t.Fatalf("save error = %v, want temporary-file failure", err)
	}
}

func TestSaveCreateTempAndRenameFailures(t *testing.T) {
	dir := privateConfigTestDir(t)
	if err := Save("nested/name.json", map[string]int{"a": 1}); err == nil {
		t.Fatal("CreateTemp should reject a name containing a path separator")
	}

	if err := os.Mkdir(filepath.Join(dir, "existing"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Save("existing", map[string]int{"a": 1}); err == nil {
		t.Fatal("rename over a directory should fail")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "existing.tmp-") {
			t.Fatalf("temporary file was not cleaned up after rename failure: %s", entry.Name())
		}
	}
}

func TestSaveWriteFailureCleansUpTemporaryFile(t *testing.T) {
	dir := privateConfigTestDir(t)
	tempName := "session.json.tmp-test"
	tempPath := filepath.Join(dir, tempName)
	if err := os.WriteFile(tempPath, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeFailure := errors.New("disk write failed")
	tmp := &failingTemp{writeErr: writeFailure}
	err := save("session.json", map[string]string{"cookie": "secret"}, func(_ *os.Root, _ string) (io.WriteCloser, string, error) {
		return tmp, tempName, nil
	})
	if !errors.Is(err, writeFailure) {
		t.Fatalf("save error = %v, want original write failure", err)
	}
	if !tmp.closed {
		t.Error("temporary writer must be closed after write failure")
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Errorf("temporary file remains after write failure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "session.json")); !os.IsNotExist(err) {
		t.Errorf("destination was created after write failure: %v", err)
	}
}
