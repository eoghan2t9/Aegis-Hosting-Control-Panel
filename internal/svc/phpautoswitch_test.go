package svc

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestClassifyPHPErrors(t *testing.T) {
	cases := []struct {
		name, in string
		want     []phpNeed
	}{
		{"parse", `PHP Parse error:  syntax error, unexpected token "?->" in /home/alice/example.com/public/app/vendor/x.php on line 12`,
			[]phpNeed{{Kind: "syntax", File: "/home/alice/example.com/public/app/vendor/x.php"}}},
		{"function", `PHP Fatal error:  Uncaught Error: Call to undefined function str_starts_with() in /h/a.php:3`,
			[]phpNeed{{Kind: "function", Name: "str_starts_with"}}},
		{"namespaced function is ignored", `Call to undefined function App\helpers() in /h/a.php`, nil},
		{"class 8.x quotes, log-escaped", `Class \"Stringable\" not found in /h/a.php`, []phpNeed{{Kind: "class", Name: "Stringable"}}},
		{"class 7.x quotes", `Class 'WeakMap' not found`, []phpNeed{{Kind: "class", Name: "WeakMap"}}},
		{"composer", `Composer detected issues in your platform: Your Composer dependencies require a PHP version ">= 8.1.0". You are running 7.4.33.`,
			[]phpNeed{{Kind: "version", Name: "8.1"}}},
		{"unrelated", `Call to a member function fetchArray() on bool in /h/a.php:139`, nil},
	}
	for _, c := range cases {
		got := classifyPHPErrors(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("%s: got %#v, want %#v", c.name, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%s: got %#v, want %#v", c.name, got, c.want)
			}
		}
	}
}

func TestVerCmp(t *testing.T) {
	if verCmp("7.4", "8.0") >= 0 || verCmp("8.10", "8.9") <= 0 || verCmp("8.3", "8.3") != 0 {
		t.Fatal("verCmp ordering is wrong")
	}
}

func TestSwitchFolder(t *testing.T) {
	r := GoRoute{PHPVersion: "7.4", PathPHP: []GoPathPHP{{Prefix: "/hdo/deep", Version: "8.2"}, {Prefix: "/hdo", Version: "8.1"}}}
	cases := []struct{ path, folder, ver string }{
		{"/index.php", "", ""},                // root script
		{"/Arvio/index.php", "/Arvio", "7.4"}, // top-level folder
		{"/Arvio/api/x.php", "/Arvio", "7.4"},
		{"/hdo/a.php", "/hdo", "8.1"}, // existing entry is moved as it is
		{"/hdo/deep/a.php", "/hdo/deep", "8.2"},
		{"/hdo2/a.php", "/hdo2", "7.4"}, // whole-segment match only
		{"/we ird/a.php", "", ""},       // not a valid folder key
	}
	for _, c := range cases {
		f, v := r.switchFolder(c.path)
		if f != c.folder || v != c.ver {
			t.Errorf("%s: got (%q,%q), want (%q,%q)", c.path, f, v, c.folder, c.ver)
		}
	}
}

func TestReadPoolLog(t *testing.T) {
	p := filepath.Join(t.TempDir(), "fpm.log")
	old := "[01] old line [pool example.com] /home/alice/example.com/app/old.php\n"
	os.WriteFile(p, []byte(old), 0o600)
	mark := int64(len(old))
	add := "[02] x [pool example.com] Call to undefined function f() in /home/alice/example.com/app/a.php:1\n" +
		"[03] x [pool other.com] Call to undefined function g() in /home/alice/example.com/app/b.php:1\n" +
		"[04] x [pool example.com] Call to undefined function h() in /home/alice/example.com/else/c.php:1\n"
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(add)
	f.Close()
	got := readPoolLog(p, mark, "example.com", "/home/alice/example.com/app")
	if !strings.Contains(got, "function f()") || strings.Contains(got, "g()") || strings.Contains(got, "h()") || strings.Contains(got, "old.php") {
		t.Fatalf("filtering wrong: %q", got)
	}
	// Rotated log (smaller than the mark): read from the start.
	os.WriteFile(p, []byte("[05] [pool example.com] /home/alice/example.com/app/n.php\n"), 0o600)
	if got := readPoolLog(p, 9999, "example.com", "/home/alice/example.com/app"); !strings.Contains(got, "n.php") {
		t.Fatalf("rotation not handled: %q", got)
	}
}

type applied struct{ domain, folder, version, reason string }

func newTestSwitch(t *testing.T, logLines string) (*PHPAutoSwitch, *[]applied, *sync.WaitGroup) {
	t.Helper()
	logf := filepath.Join(t.TempDir(), "fpm.log")
	os.WriteFile(logf, nil, 0o600)
	var mu sync.Mutex
	var calls []applied
	wg := &sync.WaitGroup{}
	a := &PHPAutoSwitch{state: map[string]*autoSwitchState{}}
	a.logPath = func(string) string { return logf }
	a.versions = func() []string { return []string{"8.3", "7.4", "8.1", "8.4"} }
	// A function exists only from 8.0, the class only from 8.3.
	a.builtin = func(v, kind, name string) bool {
		switch name {
		case "str_starts_with":
			return verCmp(v, "8.0") >= 0
		case "Override":
			return verCmp(v, "8.3") >= 0
		}
		return false
	}
	a.lint = func(account, v, file string) bool { return verCmp(v, "8.0") >= 0 }
	wg.Add(1)
	first := true
	a.apply = func(d, f, v, r string) error {
		mu.Lock()
		calls = append(calls, applied{d, f, v, r})
		mu.Unlock()
		if first {
			first = false
			defer wg.Done()
		}
		return nil
	}
	os.WriteFile(logf, []byte(logLines), 0o600)
	return a, &calls, wg
}

func testRoute() GoRoute {
	return GoRoute{Domain: "example.com", Root: "/home/alice/example.com/public", PHPVersion: "7.4", Owner: "alice", PHPAutoSwitch: true}
}

func TestFailedPicksLowestWorkingVersion(t *testing.T) {
	line := "[1] WARNING: [pool example.com] child said into stderr: \"PHP message: PHP Fatal error:  Uncaught Error: Call to undefined function str_starts_with() in /home/alice/example.com/public/Arvio/x.php:3\"\n"
	a, calls, wg := newTestSwitch(t, "")
	logf := a.logPath("7.4")
	mark := a.Mark("7.4")
	os.WriteFile(logf, []byte(line), 0o600) // written "during" the request
	info := a.Failed(testRoute(), "/Arvio/index.php", mark, nil, "")
	if info == nil || info.Folder != "/Arvio" || info.From != "7.4" || info.To != "8.1" {
		t.Fatalf("info = %#v, want /Arvio 7.4 -> 8.1 (lowest version that has the function)", info)
	}
	if a.Switching("example.com", "/Arvio") == nil {
		// the apply goroutine may already have finished; that is fine
		t.Log("switch already completed")
	}
	wg.Wait()
	if len(*calls) != 1 || (*calls)[0].folder != "/Arvio" || (*calls)[0].version != "8.1" || !strings.Contains((*calls)[0].reason, "str_starts_with") {
		t.Fatalf("apply calls = %#v", *calls)
	}
}

func TestFailedIgnoresNonVersionProblems(t *testing.T) {
	a, calls, _ := newTestSwitch(t, "")
	logf := a.logPath("7.4")
	mark := a.Mark("7.4")
	os.WriteFile(logf, []byte("[1] [pool example.com] Call to a member function fetchArray() on bool in /home/alice/example.com/public/Arvio/x.php:3\n"), 0o600)
	if info := a.Failed(testRoute(), "/Arvio/index.php", mark, nil, ""); info != nil {
		t.Fatalf("a non-version error must not switch, got %#v", info)
	}
	// Root scripts are never switched, even with a version error.
	a2, _, _ := newTestSwitch(t, "")
	if info := a2.Failed(testRoute(), "/index.php", -1, []byte("Call to undefined function str_starts_with()"), ""); info != nil {
		t.Fatalf("root script switched: %#v", info)
	}
	if len(*calls) != 0 {
		t.Fatalf("unexpected apply: %#v", *calls)
	}
}

func TestFailedNeedsSatisfiedByCurrentIsNotAVersionProblem(t *testing.T) {
	a, _, _ := newTestSwitch(t, "")
	a.builtin = func(v, kind, name string) bool { return true } // already present on 7.4
	body := []byte("Call to undefined function str_starts_with()")
	if info := a.Failed(testRoute(), "/Arvio/index.php", -1, body, ""); info != nil {
		t.Fatalf("got %#v", info)
	}
}

func TestFailedNeverGoesDownAndNeedsAHigherVersion(t *testing.T) {
	a, _, _ := newTestSwitch(t, "")
	a.versions = func() []string { return []string{"7.4"} } // nothing newer installed
	body := []byte("Call to undefined function str_starts_with()")
	if info := a.Failed(testRoute(), "/Arvio/index.php", -1, body, ""); info != nil {
		t.Fatalf("got %#v", info)
	}
}

func TestFailedRequiresSyntaxFileInsideDocroot(t *testing.T) {
	a, _, _ := newTestSwitch(t, "")
	body := []byte("PHP Parse error:  syntax error, unexpected '?' in /etc/passwd on line 1")
	if info := a.Failed(testRoute(), "/Arvio/index.php", -1, body, ""); info != nil {
		t.Fatalf("a file outside the docroot must never be linted: %#v", info)
	}
}

func TestFailedComposerPlatformCheck(t *testing.T) {
	a, _, wg := newTestSwitch(t, "")
	body := []byte(`Composer detected issues in your platform: Your Composer dependencies require a PHP version ">= 8.2.0".`)
	info := a.Failed(testRoute(), "/app/index.php", -1, body, "")
	if info == nil || info.To != "8.3" {
		t.Fatalf("info = %#v, want 8.3 (lowest installed >= 8.2)", info)
	}
	wg.Wait()
}

func TestFailedRateLimits(t *testing.T) {
	a, _, wg := newTestSwitch(t, "")
	body := []byte("Call to undefined function str_starts_with()")
	if a.Failed(testRoute(), "/Arvio/index.php", -1, body, "") == nil {
		t.Fatal("first failure should switch")
	}
	wg.Wait()
	time.Sleep(20 * time.Millisecond)
	// Diagnosis gap: an immediate second failure is not re-diagnosed.
	if info := a.Failed(testRoute(), "/Arvio/index.php", -1, body, ""); info != nil {
		t.Fatalf("repeat inside the gap: %#v", info)
	}
	// Daily cap.
	s := a.state["example.com|/Arvio"]
	s.last = time.Time{}
	s.switches = []time.Time{time.Now().Add(-3 * time.Hour), time.Now().Add(-2 * time.Hour), time.Now().Add(-time.Hour)}
	if info := a.Failed(testRoute(), "/Arvio/index.php", -1, body, ""); info != nil {
		t.Fatalf("daily cap not enforced: %#v", info)
	}
}

func TestSwitchingShowsBannerThenClears(t *testing.T) {
	a, _, _ := newTestSwitch(t, "")
	block := make(chan struct{})
	a.apply = func(d, f, v, r string) error { <-block; return nil }
	body := []byte("Call to undefined function str_starts_with()")
	if a.Failed(testRoute(), "/Arvio/index.php", -1, body, "") == nil {
		t.Fatal("expected a switch")
	}
	if a.Switching("example.com", "/Arvio") == nil {
		t.Fatal("Switching should report the in-progress switch")
	}
	// A second request during the switch gets the same banner, not a new diagnosis.
	if info := a.Failed(testRoute(), "/Arvio/other.php", -1, body, ""); info == nil || info.To != "8.1" {
		t.Fatalf("concurrent request: %#v", info)
	}
	close(block)
	deadline := time.Now().Add(2 * time.Second)
	for a.Switching("example.com", "/Arvio") != nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if a.Switching("example.com", "/Arvio") != nil {
		t.Fatal("switching flag never cleared")
	}
}

func TestPHPSwitchPage(t *testing.T) {
	w := &WebServer{}
	info := &PHPSwitchInfo{Folder: "/Arvio", From: "7.4", To: "8.1"}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/Arvio/", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	w.phpSwitchPage(rec, req, info)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("banner response: %d %v", rec.Code, rec.Header())
	}
	b := rec.Body.String()
	if !strings.Contains(b, "PHP 7.4 to PHP 8.1") || !strings.Contains(b, "location.reload()") || strings.Contains(b, "/Arvio") {
		t.Fatalf("banner body wrong: %s", b)
	}

	// A POST is not replayed and gets no HTML.
	rec = httptest.NewRecorder()
	w.phpSwitchPage(rec, httptest.NewRequest("POST", "/Arvio/save.php", nil), info)
	if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "<html") {
		t.Fatalf("POST response: %d %q", rec.Code, rec.Body.String())
	}
}

func TestFailedReadsFastCGIStderr(t *testing.T) {
	a, _, wg := newTestSwitch(t, "")
	stderr := "PHP message: PHP Parse error:  syntax error, unexpected '=>' (T_DOUBLE_ARROW) in /home/alice/example.com/public/app/index.php on line 3"
	info := a.Failed(testRoute(), "/app/index.php", -1, nil, stderr)
	if info == nil || info.To != "8.1" {
		t.Fatalf("info = %#v, want 8.1 (parse error that lints clean on 8.x)", info)
	}
	wg.Wait()
}
