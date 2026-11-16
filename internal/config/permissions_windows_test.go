//go:build windows

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestSecureConfigDirProtectsExistingFiles(t *testing.T) {
	dir := t.TempDir()
	broad, err := windows.SecurityDescriptorFromString("D:(A;OICI;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := broad.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "session.json")
	if err := os.WriteFile(file, []byte(`{"cookie":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := secureConfigDir(dir); err != nil {
		t.Fatal(err)
	}
	dirSD, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := dirSD.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("config DACL is not protected: %s", dirSD)
	}

	fileSD, err := windows.GetNamedSecurityInfo(file, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	fileACL := fileSD.String()
	if strings.Contains(fileACL, ";;;WD)") {
		t.Fatalf("existing session file still grants Everyone access: %s", fileACL)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	userACE, err := windows.SecurityDescriptorFromString(fmt.Sprintf("D:(A;ID;FA;;;%s)", user.User.Sid.String()))
	if err != nil {
		t.Fatal(err)
	}
	userACEString := strings.TrimPrefix(userACE.String(), "D:")
	if !strings.Contains(fileACL, userACEString) {
		t.Fatalf("existing session file is missing the current user's inherited access: %s", fileACL)
	}
}
