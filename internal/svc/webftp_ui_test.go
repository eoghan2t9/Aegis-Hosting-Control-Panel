package svc

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestWebFTPLoginPage(t *testing.T) {
	page := webftpLoginPage("webftp.example.com", "invalid username or password")
	for _, want := range []string{
		`name="viewport"`,    // mobile scaling
		`class="login-mark"`, // panel-style brand
		`action="/login"`,    // form target the server expects
		`invalid username or password`,
		`webftp.example.com`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("login page missing %q", want)
		}
	}
	// Host and error text are attacker-influenced (Host header): must be escaped.
	evil := webftpLoginPage(`x"><script>alert(1)</script>`, `<img src=x onerror=alert(1)>`)
	if strings.Contains(evil, "<script>alert(1)</script>") || strings.Contains(evil, "<img src=x") {
		t.Error("login page does not escape the host or error message")
	}
}

func TestWebFTPBrowserPageRegressions(t *testing.T) {
	page := webftpBrowserPage("webftp.example.com")

	// The old full-screen "Drop files here to upload" overlay must be gone. It was
	// effectively permanent because .dropzone{display:flex} out-ranked .hidden.
	for _, banned := range []string{"Drop files here", `class="dropzone`, ".dropzone{"} {
		if strings.Contains(page, banned) {
			t.Errorf("page still contains the full-screen drop overlay (%q)", banned)
		}
	}
	// .hidden must win over any component display rule.
	if !strings.Contains(page, ".hidden{display:none !important}") {
		t.Error(".hidden must be !important so it cannot be overridden by component CSS")
	}
	// Mobile support: viewport meta, responsive breakpoints, touch-sized targets.
	for _, want := range []string{`name="viewport"`, "@media (max-width:700px)", "@media (max-width:600px)", "min-height:40px", "font-size:16px"} {
		if !strings.Contains(page, want) {
			t.Errorf("browser page missing mobile support %q", want)
		}
	}
	// Panel look: same tokens, wordmark and grid backdrop.
	for _, want := range []string{"--accent:#c6f14e", `class="wordmark"`, "44px 44px"} {
		if !strings.Contains(page, want) {
			t.Errorf("browser page missing panel style %q", want)
		}
	}
	// Native prompt()/confirm() are unstyled and awkward on phones.
	if regexp.MustCompile(`\b(prompt|confirm)\(`).MatchString(page) {
		t.Error("page still uses native prompt()/confirm() dialogs")
	}
	// The host is interpolated into the page and must be escaped.
	if evil := webftpBrowserPage(`</title><script>alert(1)</script>`); strings.Contains(evil, "<script>alert(1)</script>") {
		t.Error("browser page does not escape the host")
	}
}

// Every $("id") the script looks up must exist in the markup, otherwise the
// page throws on load and shows nothing.
func TestWebFTPBrowserPageElementIDs(t *testing.T) {
	page := webftpBrowserPage("webftp.example.com")
	ids := regexp.MustCompile(`\$\("([a-z-]+)"\)`).FindAllStringSubmatch(page, -1)
	if len(ids) == 0 {
		t.Fatal("found no $(...) lookups; the test needs updating")
	}
	for _, m := range ids {
		if !strings.Contains(page, `id="`+m[1]+`"`) {
			t.Errorf("script uses $(%q) but no element has that id", m[1])
		}
	}
}

// The inline script must at least parse. Skipped when node isn't installed.
func TestWebFTPBrowserPageScriptSyntax(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	page := webftpBrowserPage("webftp.example.com")
	start := strings.Index(page, "<script>")
	end := strings.LastIndex(page, "</script>")
	if start < 0 || end < start {
		t.Fatal("no inline script found")
	}
	file := filepath.Join(t.TempDir(), "webftp.js")
	if err := os.WriteFile(file, []byte(page[start+len("<script>"):end]), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, "--check", file).CombinedOutput(); err != nil {
		t.Fatalf("inline script has a syntax error: %v\n%s", err, out)
	}
}
