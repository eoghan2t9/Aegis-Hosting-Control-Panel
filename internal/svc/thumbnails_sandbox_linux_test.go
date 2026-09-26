//go:build linux

package svc

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

var jpegMagic = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0x10, 'J', 'F', 'I', 'F'}

// The thumbnail tool runs as the customer, so whatever it leaves behind is
// untrusted. A symlink to a file root can read must never be copied into the
// cache (and from there returned to the customer as a "thumbnail").
func TestTakeToolOutputRefusesSymlinksAndNonJPEG(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.jpg")
	if err := os.WriteFile(real, jpegMagic, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := takeToolOutput(real); err != nil {
		t.Errorf("a plain JPEG was refused: %v", err)
	}

	link := filepath.Join(dir, "link.jpg")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := takeToolOutput(link); err == nil {
		t.Error("a symlink was followed")
	}

	notJPEG := filepath.Join(dir, "shadow.jpg")
	if err := os.WriteFile(notJPEG, []byte("root:x:0:0:not a jpeg"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := takeToolOutput(notJPEG); err == nil {
		t.Error("output without JPEG magic was accepted")
	}

	empty := filepath.Join(dir, "empty.jpg")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := takeToolOutput(empty); err == nil {
		t.Error("an empty file was accepted")
	}

	huge := filepath.Join(dir, "huge.jpg")
	if err := os.WriteFile(huge, append(append([]byte{}, jpegMagic...), bytes.Repeat([]byte{1}, maxThumbBytes)...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := takeToolOutput(huge); err == nil {
		t.Error("an oversized file was accepted")
	}

	if _, err := takeToolOutput(dir); err == nil {
		t.Error("a directory was accepted")
	}
}

func TestAccountSandboxRunsToolsAsTheAccount(t *testing.T) {
	name, uid := accountName(t)
	t.Setenv("AEGIS_MARIADB_PASSWORD", "hunter2-mariadb")

	sb, err := accountSandbox(name)
	if err != nil {
		t.Fatal(err)
	}
	defer sb.Close()

	script := "id -u > uid.txt; env > env.txt; cat /etc/shadow > shadow.txt 2>/dev/null; true"
	if err := sb.Run(10*time.Second, "sh", "-c", script); err != nil {
		t.Fatal(err)
	}
	read := func(f string) string {
		b, _ := os.ReadFile(filepath.Join(sb.Dir, f))
		return strings.TrimSpace(string(b))
	}
	if got := read("uid.txt"); got != strconv.Itoa(int(uid)) {
		t.Errorf("tool ran as uid %q, want %d", got, uid)
	}
	if env := read("env.txt"); strings.Contains(env, "hunter2") || strings.Contains(env, "AEGIS_") {
		t.Errorf("panel secrets reached the tool's environment:\n%s", env)
	}
	if s := read("shadow.txt"); s != "" {
		t.Error("the tool could read /etc/shadow")
	}
}
