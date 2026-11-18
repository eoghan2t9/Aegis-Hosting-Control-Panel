package svc

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"aegis/internal/store"
)

// PHPAutoSwitch moves a site's sub-folder to a newer PHP version when its code
// fails on the current one.
//
// The trigger is a PHP request that answered 500 inside a sub-folder of a
// domain that has Domain.PHPAutoSwitch on. The failure is then read from the
// pool's own lines in the php-fpm log (display_errors is off, so the visitor
// never sees the message and php-fpm does not hand it back over FastCGI).
// Only errors that are really caused by an old PHP are acted on:
//
//   - a parse error, when the failing file lints clean on a newer version;
//   - "Call to undefined function f()" / "Class X not found", when f / X is
//     built into a newer version and absent from the current one;
//   - Composer's "requires a PHP version >= X.Y" platform check.
//
// It only ever moves up, never touches the domain's own version, picks the
// lowest installed version that satisfies every detected need, and records the
// change in the audit log under the domain's owner. Visitors get a short
// "updating PHP" banner (WebServer.phpSwitchPage) that reloads itself.
//
// Nothing here runs customer code as root: lint runs through accountSandbox, and
// the only other child processes are `php -n -r` one-liners that contain no
// customer content (the names are matched against a strict identifier regexp).
type PHPAutoSwitch struct {
	Store   *store.Store
	Domains *Domains
	PHP     *PHP

	// Seams, so the decision logic is testable without php or root.
	logPath  func(version string) string
	versions func() []string                       // installed versions, any order
	builtin  func(version, kind, name string) bool // function/class built into version
	lint     func(account, version, file string) bool
	apply    func(domain, folder, version, reason string) error

	mu    sync.Mutex
	state map[string]*autoSwitchState
}

type autoSwitchState struct {
	switching bool
	info      PHPSwitchInfo
	since     time.Time   // when switching began
	last      time.Time   // last diagnosis (suppresses repeats)
	switches  []time.Time // successful switches, for the daily cap
}

// PHPSwitchInfo describes an in-progress or just-made switch.
type PHPSwitchInfo struct {
	Folder string
	From   string
	To     string
}

const (
	autoSwitchMinGap     = 60 * time.Second // between two switches of one folder
	autoSwitchDiagGap    = 30 * time.Second // between two diagnoses of one folder
	autoSwitchMaxPerDay  = 3
	autoSwitchStuckAfter = 90 * time.Second
	autoSwitchLogWait    = 500 * time.Millisecond
	autoSwitchLogMax     = 256 << 10
)

func NewPHPAutoSwitch(st *store.Store, domains *Domains, php *PHP) *PHPAutoSwitch {
	a := &PHPAutoSwitch{Store: st, Domains: domains, PHP: php, state: map[string]*autoSwitchState{}}
	a.logPath = func(v string) string { return "/var/log/php" + v + "-fpm.log" }
	a.versions = func() []string {
		var out []string
		for _, v := range php.Versions() {
			out = append(out, v.Version)
		}
		return out
	}
	a.builtin = a.phpBuiltin
	a.lint = a.phpLint
	a.apply = a.applySwitch
	return a
}

// ---- request-path API (called by WebServer) ----

// Mark returns the current end of the version's php-fpm log, or -1 when it
// cannot be read. Call it before running a request that might fail and pass the
// result to Failed.
func (a *PHPAutoSwitch) Mark(version string) int64 {
	st, err := os.Stat(a.logPath(version))
	if err != nil {
		return -1
	}
	return st.Size()
}

// Switching reports a switch that is being applied to the folder right now.
func (a *PHPAutoSwitch) Switching(domain, folder string) *PHPSwitchInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.state[domain+"|"+folder]
	if s == nil || !s.switching {
		return nil
	}
	if time.Since(s.since) > autoSwitchStuckAfter {
		s.switching = false // do not show the banner forever if apply hung
		return nil
	}
	info := s.info
	return &info
}

// Failed is called after a PHP request answered 500. body is the start of the
// response (some apps print their error) and stderr is what php-fpm sent back
// over FastCGI (parse errors arrive there). It returns the switch that was
// started, or nil when the failure is not something a newer PHP would fix.
func (a *PHPAutoSwitch) Failed(route GoRoute, upath string, mark int64, body []byte, stderr string) *PHPSwitchInfo {
	folder, cur := route.switchFolder(upath)
	if folder == "" || cur == "" {
		return nil
	}
	key := route.Domain + "|" + folder

	a.mu.Lock()
	s := a.state[key]
	if s == nil {
		s = &autoSwitchState{}
		a.state[key] = s
	}
	if s.switching {
		info := s.info
		a.mu.Unlock()
		return &info
	}
	if time.Since(s.last) < autoSwitchDiagGap {
		a.mu.Unlock()
		return nil
	}
	s.last = time.Now()
	if !a.maySwitchLocked(s) {
		a.mu.Unlock()
		return nil
	}
	a.mu.Unlock()

	text := a.errorText(route, cur, mark, folder, body, stderr)
	needs := classifyPHPErrors(text)
	if len(needs) == 0 {
		return nil
	}
	to, reason := a.pickVersion(route, cur, needs)
	if to == "" {
		return nil
	}

	a.mu.Lock()
	if s.switching { // lost a race with a concurrent request
		info := s.info
		a.mu.Unlock()
		return &info
	}
	s.switching = true
	s.since = time.Now()
	s.info = PHPSwitchInfo{Folder: folder, From: cur, To: to}
	info := s.info
	a.mu.Unlock()

	go func() {
		err := a.apply(route.Domain, folder, to, reason)
		a.mu.Lock()
		s.switching = false
		if err == nil {
			s.switches = append(s.switches, time.Now())
		}
		a.mu.Unlock()
		if err != nil {
			slog.Warn("php auto-switch failed", "domain", route.Domain, "folder", folder, "to", to, "err", err)
		}
	}()
	return &info
}

// maySwitchLocked enforces the gap and the daily cap. Caller holds a.mu.
func (a *PHPAutoSwitch) maySwitchLocked(s *autoSwitchState) bool {
	cut := time.Now().Add(-24 * time.Hour)
	kept := s.switches[:0]
	for _, t := range s.switches {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	s.switches = kept
	if len(kept) >= autoSwitchMaxPerDay {
		return false
	}
	if n := len(kept); n > 0 && time.Since(kept[n-1]) < autoSwitchMinGap {
		return false
	}
	return true
}

// ---- diagnosis ----

// errorText gathers what the failed request said about itself: the pool's new
// php-fpm log lines that name a file inside the folder, plus the response body.
func (a *PHPAutoSwitch) errorText(route GoRoute, version string, mark int64, folder string, body []byte, stderr string) string {
	var b strings.Builder
	if len(body) > 8192 {
		body = body[:8192]
	}
	b.Write(body)
	b.WriteByte('\n')
	if len(stderr) > 8192 {
		stderr = stderr[:8192]
	}
	b.WriteString(stderr)
	b.WriteByte('\n')
	if mark >= 0 && !strings.Contains(stderr, "PHP message") {
		pool := route.poolFor(version)
		deadline := time.Now().Add(autoSwitchLogWait)
		for {
			lines := readPoolLog(a.logPath(version), mark, pool, route.Root+folder)
			if lines != "" {
				b.WriteString(lines)
				break
			}
			if time.Now().After(deadline) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	return b.String()
}

// readPoolLog returns the lines written to a php-fpm log since offset mark
// that belong to pool and mention a path under within. Bounded in size.
func readPoolLog(path string, mark int64, pool, within string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return ""
	}
	if st.Size() < mark { // rotated
		mark = 0
	}
	if st.Size()-mark > autoSwitchLogMax {
		mark = st.Size() - autoSwitchLogMax
	}
	buf := make([]byte, st.Size()-mark)
	if n, err := f.ReadAt(buf, mark); err != nil && n < len(buf) {
		buf = buf[:n]
	}
	tag := "[pool " + pool + "]"
	var out strings.Builder
	for _, ln := range strings.Split(string(buf), "\n") {
		if strings.Contains(ln, tag) && strings.Contains(ln, within) {
			out.WriteString(ln)
			out.WriteByte('\n')
		}
	}
	return out.String()
}

// phpNeed is one thing a newer PHP would provide.
type phpNeed struct {
	Kind string // "syntax", "function", "class", "version"
	Name string // function/class name, or "8.1" for version
	File string // failing file, for "syntax"
}

var (
	reSyntaxErr = regexp.MustCompile(`(?i)parse error:\s+syntax error.{0,200}? in (/[^\s:]+\.\w+) on line \d+`)
	reFunction  = regexp.MustCompile(`Call to undefined function \\?([A-Za-z_][A-Za-z0-9_]*)\(\)`)
	reClass     = regexp.MustCompile(`(?:Class|Interface|Trait) ['"]\\?([A-Za-z_][A-Za-z0-9_]*)['"] not found`)
	reVersion   = regexp.MustCompile(`(?i)(?:require[sd]?|need[s]?)\s+(?:a\s+)?php(?:\s+version)?\s*["']?\s*>=?\s*(\d+\.\d+)`)
)

// classifyPHPErrors extracts the version-related needs from error text.
func classifyPHPErrors(text string) []phpNeed {
	text = strings.ReplaceAll(text, `\"`, `"`)
	var out []phpNeed
	seen := map[string]bool{}
	add := func(n phpNeed) {
		k := n.Kind + "|" + n.Name + "|" + n.File
		if !seen[k] && len(out) < 8 {
			seen[k] = true
			out = append(out, n)
		}
	}
	for _, m := range reSyntaxErr.FindAllStringSubmatch(text, -1) {
		add(phpNeed{Kind: "syntax", File: m[1]})
	}
	for _, m := range reFunction.FindAllStringSubmatch(text, -1) {
		add(phpNeed{Kind: "function", Name: m[1]})
	}
	for _, m := range reClass.FindAllStringSubmatch(text, -1) {
		add(phpNeed{Kind: "class", Name: m[1]})
	}
	for _, m := range reVersion.FindAllStringSubmatch(text, -1) {
		add(phpNeed{Kind: "version", Name: m[1]})
	}
	return out
}

// verCmp compares "8.1" style versions numerically (7.4 < 8.0 < 8.10).
func verCmp(a, b string) int {
	pa, pb := strings.SplitN(a, ".", 3), strings.SplitN(b, ".", 3)
	for i := 0; i < 2; i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// pickVersion returns the lowest installed version newer than cur that
// satisfies every need that cur itself does not already satisfy, and a short
// reason. "" means nothing to do. Needs that cur already meets are dropped:
// they are not version problems.
func (a *PHPAutoSwitch) pickVersion(route GoRoute, cur string, needs []phpNeed) (string, string) {
	var real []phpNeed
	for _, n := range needs {
		if a.satisfied(route, cur, n) {
			continue
		}
		real = append(real, n)
	}
	if len(real) == 0 {
		return "", ""
	}
	vers := a.versions()
	sort.Slice(vers, func(i, j int) bool { return verCmp(vers[i], vers[j]) < 0 })
	for _, v := range vers {
		if verCmp(v, cur) <= 0 {
			continue
		}
		ok := true
		for _, n := range real {
			if !a.satisfied(route, v, n) {
				ok = false
				break
			}
		}
		if ok {
			return v, describeNeeds(real)
		}
	}
	return "", ""
}

func describeNeeds(needs []phpNeed) string {
	parts := make([]string, 0, len(needs))
	for _, n := range needs {
		switch n.Kind {
		case "syntax":
			parts = append(parts, "syntax error in "+filepath.Base(n.File))
		case "function":
			parts = append(parts, "undefined function "+n.Name+"()")
		case "class":
			parts = append(parts, "missing class "+n.Name)
		case "version":
			parts = append(parts, "requires PHP >= "+n.Name)
		}
	}
	return strings.Join(parts, ", ")
}

// satisfied reports whether need n is met by PHP version v.
func (a *PHPAutoSwitch) satisfied(route GoRoute, v string, n phpNeed) bool {
	switch n.Kind {
	case "version":
		return verCmp(v, n.Name) >= 0
	case "function", "class":
		return a.builtin(v, n.Kind, n.Name)
	case "syntax":
		root := filepath.Clean(route.Root)
		f := filepath.Clean(n.File)
		if !strings.HasPrefix(f, root+string(filepath.Separator)) {
			return false // only ever look inside the site's own document root
		}
		return a.lint(route.Owner, v, f)
	}
	return false
}

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// phpBuiltin asks a PHP CLI whether a function/class exists. -n skips every
// ini file, so no customer extension or auto_prepend_file runs; the name is a
// plain identifier and goes to the interpreter as source, never a file.
func (a *PHPAutoSwitch) phpBuiltin(version, kind, name string) bool {
	if !identRe.MatchString(name) || !a.PHP.Has(version) {
		return false
	}
	var code string
	if kind == "function" {
		code = `exit(function_exists('` + name + `')?0:1);`
	} else {
		code = `exit(class_exists('` + name + `',false)||interface_exists('` + name + `',false)||trait_exists('` + name + `',false)?0:1);`
	}
	cmd := exec.Command("php"+version, "-n", "-r", code)
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/nonexistent", "LANG=C.UTF-8"}
	if err := cmd.Start(); err != nil {
		return false
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err == nil
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		return false
	}
}

// phpLint syntax-checks one customer file with `php -l` as the account (never
// as root: the file is customer content).
func (a *PHPAutoSwitch) phpLint(account, version, file string) bool {
	if account == "" || !a.PHP.Has(version) {
		return false
	}
	sb, err := accountSandbox(account)
	if err != nil {
		return false
	}
	defer sb.Close()
	return sb.Run(15*time.Second, "php"+version, "-n", "-l", file) == nil
}

// ---- applying ----

func (a *PHPAutoSwitch) applySwitch(domain, folder, version, reason string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	dom, err := a.Store.GetDomainByName(ctx, domain)
	if err != nil {
		return err
	}
	if !dom.PHPAutoSwitch {
		return fmt.Errorf("automatic switching is off for %s", domain)
	}
	if dom.PHPVersion != "" && verCmp(version, dom.PHPVersion) < 0 {
		return fmt.Errorf("refusing to move %s below the domain's PHP %s", folder, dom.PHPVersion)
	}
	from := dom.PHPVersion
	if cur := dom.PHPPaths[folder]; cur != "" {
		if verCmp(version, cur) <= 0 {
			return fmt.Errorf("%s already runs PHP %s", folder, cur)
		}
		from = cur
	}
	paths := map[string]string{}
	for k, v := range dom.PHPPaths {
		paths[k] = v
	}
	paths[folder] = version
	paths, err = ValidatePHPPaths(paths, a.PHP.Has)
	if err != nil {
		return err
	}
	dom.PHPPaths = paths
	if err := a.Store.UpdateDomain(ctx, dom); err != nil {
		return err
	}
	if err := a.Domains.Apply(ctx, dom.ID); err != nil {
		return fmt.Errorf("apply: %w", err)
	}
	detail := fmt.Sprintf("%s: PHP %s -> %s (%s)", folder, from, version, reason)
	if err := a.Store.AppendAudit(ctx, dom.UserID, "aegis (automatic)", "domain.php_autoswitch", dom.Domain, truncate(detail, 300), ""); err != nil {
		slog.Error("audit write failed", "err", err)
	}
	slog.Info("php auto-switch", "domain", domain, "folder", folder, "from", from, "to", version, "reason", reason)
	return nil
}

// ---- route helpers ----

// switchFolder returns the folder that a failure at urlPath would move and the
// PHP version that currently serves it. Folder is "" for a script directly in
// the domain root, which is never switched. A folder that already has its own
// entry is moved as it is; anything else moves its top-level folder.
func (r GoRoute) switchFolder(urlPath string) (folder, version string) {
	for _, pp := range r.PathPHP {
		if urlPath == pp.Prefix || strings.HasPrefix(urlPath, pp.Prefix+"/") {
			return pp.Prefix, pp.Version
		}
	}
	rest := strings.TrimPrefix(urlPath, "/")
	seg, _, nested := strings.Cut(rest, "/")
	if seg == "" || !nested {
		return "", ""
	}
	folder = "/" + seg
	if !phpPathRe.MatchString(folder) {
		return "", ""
	}
	return folder, r.PHPVersion
}

// poolFor names the php-fpm pool that serves version for this route (see
// EnsureSubPools): the domain's own pool, or "<domain>@<version>".
func (r GoRoute) poolFor(version string) string {
	if version == r.PHPVersion {
		return r.Domain
	}
	return r.Domain + "@" + version
}
