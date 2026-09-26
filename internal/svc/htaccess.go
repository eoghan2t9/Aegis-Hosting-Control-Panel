package svc

import (
	"bufio"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// This file implements an Apache .htaccess interpreter so that sites carried
// over from Apache (or written for it: WordPress, Laravel, Craft, …) behave
// the same on the native Go server and, in a static translated form, on
// Caddy. Apache per-directory semantics are simplified where the panel's
// serving model makes them irrelevant; deviations are called out in
// comments. Patterns compile as Go RE2: PCRE-only constructs such as
// lookahead ((?!…)) fail to compile and those rules are skipped — the
// classic front-controller rules don't need them (they use -f/-d tests).

// HtConfig is the merged, parsed view of the .htaccess files governing one
// directory. Deeper files are merged first (matching Apache per-directory
// order): a child's RewriteRules run before its parents'.
type HtConfig struct {
	Dir            string // deepest directory holding a .htaccess (disk path)
	DirURL         string // URL-space path of Dir ("/" when it is the docroot)
	Files          []string
	RewriteEngine  bool
	RewriteBase    string // URL-space base for substitutions, "" = per-dir
	Rules          []HtRule
	Redirects      []HtRedirect
	DirectoryIndex []string
	ErrorDocuments map[int]string
	DenyAll        bool // "Require all denied" / "Deny from all" (bare, not inside <Files>/<FilesMatch>)
	// FileDenies are "Deny from all"/"Require all denied" directives found
	// *inside* a <Files>/<FilesMatch> block — these must only 403 requests
	// whose filename matches the block's own pattern, not every request
	// under the directory (see htParseFile: a container tag like <Files>
	// isn't otherwise recognized, so a scoped deny meant for one filename,
	// e.g. a sensitive .db file, would silently apply to every file in the
	// directory instead).
	FileDenies []HtFileDeny
	// PHPExtensions are file extensions (lowercase, no leading dot) that an
	// "AddType application/x-httpd-php... .ext..." directive maps onto PHP
	// execution — legacy sites commonly name a PHP script ".html" this way.
	// Extensions already executed unconditionally (.php) don't need to be
	// listed here.
	PHPExtensions []string
	// DirectorySlashOff is "DirectorySlash Off" — opts out of the
	// missing-trailing-slash redirect goserver.go's serveGoRoute otherwise
	// always applies to directory requests.
	DirectorySlashOff bool
	// Headers are "Header ..." ops (mod_headers), applied to the response
	// after content is generated. RequestHeaders are "RequestHeader ..."
	// ops, applied to the request before it reaches PHP-FPM.
	Headers        []HtHeaderOp
	RequestHeaders []HtHeaderOp
	Skipped        []string
}

// HtHeaderOp is one "Header"/"RequestHeader" directive. Kind is
// set/add/append/merge/unset/edit (lowercased); a leading "always"/
// "onsuccess" condition token is accepted but not distinguished — this
// server doesn't track per-response error state at the point these apply.
type HtHeaderOp struct {
	Kind    string
	Name    string
	Value   string
	Pattern string // "edit"/"edit*" only: the match regex: Value holds the replacement
}

// htParseHeaderOp parses a Header/RequestHeader directive's arguments
// (everything after the directive name).
func htParseHeaderOp(args []string) (HtHeaderOp, bool) {
	if len(args) > 0 && (strings.EqualFold(args[0], "always") || strings.EqualFold(args[0], "onsuccess")) {
		args = args[1:]
	}
	if len(args) < 2 {
		return HtHeaderOp{}, false
	}
	op := HtHeaderOp{Kind: strings.ToLower(args[0]), Name: args[1]}
	rest := args[2:]
	switch op.Kind {
	case "unset":
		// no value
	case "edit", "edit*":
		if len(rest) < 2 {
			return HtHeaderOp{}, false
		}
		op.Pattern, op.Value = rest[0], rest[1]
	default: // set, add, append, merge
		op.Value = strings.Join(rest, " ")
	}
	return op, true
}

// htApplyHeaders runs a directory's Header/RequestHeader ops against h, in
// declaration order.
func htApplyHeaders(h http.Header, ops []HtHeaderOp) {
	for _, op := range ops {
		switch op.Kind {
		case "set":
			h.Set(op.Name, op.Value)
		case "add", "append", "merge":
			h.Add(op.Name, op.Value)
		case "unset":
			h.Del(op.Name)
		case "edit", "edit*":
			re := htCompile(op.Pattern, false)
			if re == nil {
				continue
			}
			vals := h.Values(op.Name)
			for i, v := range vals {
				nv := re.ReplaceAllString(v, op.Value)
				if i == 0 {
					h.Set(op.Name, nv)
				} else {
					h.Add(op.Name, nv)
				}
			}
		}
	}
}

// HtFileDeny is one <Files pattern> or <FilesMatch pattern> block's "Deny
// from all"/"Require all denied" — Regex true for FilesMatch (Pattern is a
// regex tested against the request's basename), false for Files (Pattern is
// an Apache-style fnmatch glob, e.g. "*.log" or a literal filename).
type HtFileDeny struct {
	Pattern string
	Regex   bool
}

// htDenyAll records a "Deny from all"/"Require all denied" directive found
// while parsing — scoped to the innermost open <Files>/<FilesMatch> block
// when there is one, else a bare global deny (matching real Apache: outside
// any container, these directives really do apply to the whole directory).
func htDenyAll(cfg *HtConfig, filesStack []HtFileDeny) {
	if len(filesStack) > 0 {
		cfg.FileDenies = append(cfg.FileDenies, filesStack[len(filesStack)-1])
		return
	}
	cfg.DenyAll = true
}

// htFileDenyMatches reports whether urlPath's basename matches a <Files>/
// <FilesMatch> deny pattern.
func htFileDenyMatches(fd HtFileDeny, urlPath string) bool {
	base := path.Base(urlPath)
	if fd.Regex {
		re := htCompile(fd.Pattern, true)
		return re != nil && re.MatchString(base)
	}
	ok, err := path.Match(fd.Pattern, base)
	return err == nil && ok
}

// HtRule is one RewriteRule with its preceding RewriteCond chain.
type HtRule struct {
	Pattern string
	Sub     string
	Flags   HtRuleFlags
	Conds   []HtCond
}

// HtRuleFlags is the subset of RewriteRule flags the engine honours.
type HtRuleFlags struct {
	NoCase    bool // NC
	Last      bool // L
	End       bool // END (implies Last, and stops later merges too)
	Redirect  int  // R[=code]: 0 = internal; else 301/302/303/307/308
	Forbidden bool // F
	Gone      bool // G
	QSA       bool // QSA (append original query string)
	QSD       bool // QSD (discard original query string)
	Proxy     bool // P — recorded; nothing external to proxy to in-process
}

// HtCond is one RewriteCond line.
type HtCond struct {
	Test     string
	Pattern  string
	NoCase   bool   // NC
	OrNext   bool   // OR
	Lex      byte   // 0 = regex; '<', '>', '=' lexicographic compare
	LexNeg   bool   // true for "!<"/"!>"/"!=" — negates the Lex comparison
	FileTest string // "" or "-f","-d","-e","-s","-x" ("!"+op when negated)
}

// HtRedirect is a Redirect / RedirectMatch directive.
type HtRedirect struct {
	Match   bool   // RedirectMatch (regex) vs Redirect (prefix)
	Status  int    // 301/302/303/307/308; 410 for gone
	From    string // prefix (Redirect) or regex (RedirectMatch)
	Target  string
	Pattern *regexp.Regexp // compiled when Match
}

// htOutcome is the engine's verdict for one request.
type htOutcome struct {
	Path     string // final URL path ("" = unchanged)
	Query    string // final query string ("" = unchanged)
	Redirect string // external target when a redirect fired
	Status   int    // redirect status, or 403 / 410
}

// --- compiled-pattern cache ------------------------------------------------

var (
	htReMu    sync.Mutex
	htReCache = map[string]*regexp.Regexp{}
)

// htCompile compiles (and caches) a pattern, returning nil when it is not
// valid RE2 (PCRE lookaround etc.). Case-insensitive is encoded with (?i).
func htCompile(pattern string, noCase bool) *regexp.Regexp {
	key := pattern
	if noCase {
		key = "(?i)" + pattern
	}
	htReMu.Lock()
	defer htReMu.Unlock()
	if re, ok := htReCache[key]; ok {
		return re
	}
	re, err := regexp.Compile(key)
	if err != nil {
		htReCache[key] = nil // negative cache: don't recompile known-bad
		return nil
	}
	htReCache[key] = re
	return re
}

// --- per-request config lookup ----------------------------------------------

type htCacheEntry struct {
	sig string
	cfg *HtConfig
}

var (
	htCache   = map[string]*htCacheEntry{}
	htCacheMu sync.Mutex
)

// htConfigFor merges the .htaccess files governing urlPath within root.
// Results are cached against the mtimes of the contributing files, so edits
// take effect without a restart.
func (w *WebServer) htConfigFor(root, urlPath string) *HtConfig {
	dir := root
	for _, seg := range strings.Split(path.Dir(path.Clean("/"+urlPath)), "/") {
		if seg == "" || seg == "." {
			continue
		}
		dir = filepath.Join(dir, seg)
	}
	key := root + "\x00" + dir
	sig := htFileSig(root, dir)

	htCacheMu.Lock()
	entry, ok := htCache[key]
	htCacheMu.Unlock()
	if ok && entry.sig == sig && entry.cfg != nil {
		return entry.cfg
	}
	cfg := htParseDir(root, dir)
	htCacheMu.Lock()
	htCache[key] = &htCacheEntry{sig: sig, cfg: cfg}
	htCacheMu.Unlock()
	return cfg
}

// htFileSig builds a change signature across the .htaccess chain from dir up
// to (and including) root.
func htFileSig(root, dir string) string {
	var sb strings.Builder
	for d := dir; ; {
		if f := filepath.Join(d, ".htaccess"); len(f) > 0 {
			if st, err := os.Stat(f); err == nil {
				sb.WriteString(f)
				sb.WriteString(":")
				sb.WriteString(strconv.FormatInt(st.Size(), 10))
				sb.WriteString(":")
				sb.WriteString(strconv.FormatInt(st.ModTime().UnixNano(), 10))
				sb.WriteString(";")
			}
		}
		if d == root || d == "/" || len(d) <= 1 {
			break
		}
		next := filepath.Dir(d)
		if len(next) >= len(d) || !strings.HasPrefix(d, root) {
			break
		}
		d = next
	}
	return sb.String()
}

// htParseDir reads the .htaccess chain from dir up to root and merges them,
// deepest directory first (its rules run first — most specific wins).
func htParseDir(root, dir string) *HtConfig {
	cfg := &HtConfig{ErrorDocuments: map[int]string{}}
	var files []string
	for d := dir; ; {
		f := filepath.Join(d, ".htaccess")
		if st, err := os.Stat(f); err == nil && !st.IsDir() {
			files = append(files, f)
		}
		if d == root || d == "/" || len(d) <= 1 {
			break
		}
		next := filepath.Dir(d)
		if len(next) >= len(d) || !strings.HasPrefix(d, root) {
			break
		}
		d = next
	}
	if len(files) == 0 {
		return cfg
	}
	cfg.Files = files
	cfg.Dir = filepath.Dir(files[0])

	// URL-space path of the deepest .htaccess directory relative to root.
	rel := strings.Trim(strings.TrimPrefix(filepath.ToSlash(cfg.Dir), strings.TrimSuffix(filepath.ToSlash(root), "/")), "/")
	if rel == "" {
		cfg.DirURL = "/"
	} else {
		cfg.DirURL = "/" + rel
	}

	// Apache per-directory merge: deepest file is processed first.
	for _, f := range files {
		sub := &HtConfig{ErrorDocuments: map[int]string{}}
		htParseFile(f, sub)
		if sub.RewriteEngine {
			cfg.RewriteEngine = true
		}
		if sub.RewriteBase != "" && cfg.RewriteBase == "" {
			cfg.RewriteBase = sub.RewriteBase
		}
		cfg.Rules = append(cfg.Rules, sub.Rules...)
		cfg.Redirects = append(cfg.Redirects, sub.Redirects...)
		if len(sub.DirectoryIndex) > 0 && len(cfg.DirectoryIndex) == 0 {
			cfg.DirectoryIndex = sub.DirectoryIndex
		}
		for code, target := range sub.ErrorDocuments {
			if _, ok := cfg.ErrorDocuments[code]; !ok {
				cfg.ErrorDocuments[code] = target
			}
		}
		if sub.DenyAll {
			cfg.DenyAll = true
		}
		cfg.FileDenies = append(cfg.FileDenies, sub.FileDenies...)
		cfg.PHPExtensions = append(cfg.PHPExtensions, sub.PHPExtensions...)
		if sub.DirectorySlashOff {
			cfg.DirectorySlashOff = true
		}
		cfg.Headers = append(cfg.Headers, sub.Headers...)
		cfg.RequestHeaders = append(cfg.RequestHeaders, sub.RequestHeaders...)
		cfg.Skipped = append(cfg.Skipped, sub.Skipped...)
	}
	return cfg
}

// htParseFile parses one .htaccess into cfg. Unknown directives are skipped
// (recorded in Skipped so translators/logs can surface them).
func htParseFile(file string, cfg *HtConfig) {
	fh, err := os.Open(file)
	if err != nil {
		return
	}
	defer fh.Close()

	var pendingRule *HtRule
	// filesStack tracks nested <Files pattern>/<FilesMatch pattern> blocks —
	// a bare "Deny from all" only applies globally outside of one; inside
	// one, it's scoped to the innermost block's pattern (see FileDenies).
	var filesStack []HtFileDeny
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Apache honours trailing-backslash line continuations.
		for strings.HasSuffix(line, "\\") && !strings.HasSuffix(line, "\\\\") {
			line = strings.TrimSuffix(line, "\\")
			if !sc.Scan() {
				break
			}
			line += " " + strings.TrimSpace(sc.Text())
		}
		fields := htQuoteFields(line)
		if len(fields) == 0 {
			continue
		}
		dir := strings.ToLower(fields[0])
		args := fields[1:]

		switch dir {
		case "rewriteengine":
			if len(args) > 0 {
				cfg.RewriteEngine = strings.EqualFold(args[0], "on")
			}
		case "rewritebase":
			if len(args) > 0 {
				cfg.RewriteBase = htTrimSlashes(args[0])
			}
		case "rewritecond":
			if pendingRule == nil {
				pendingRule = &HtRule{}
			}
			if c, ok := htParseCond(args); ok {
				pendingRule.Conds = append(pendingRule.Conds, c)
			}
		case "rewriterule":
			var r *HtRule
			if pendingRule != nil {
				r = pendingRule
			} else {
				r = &HtRule{}
			}
			pendingRule = nil
			if len(args) < 2 {
				continue
			}
			r.Pattern, r.Sub = args[0], args[1]
			r.Flags = htParseRuleFlags(args[2:])
			cfg.Rules = append(cfg.Rules, *r)
		case "redirect", "redirectmatch", "redirectperm", "redirecttemp":
			if rd, ok := htParseRedirect(dir, args); ok {
				cfg.Redirects = append(cfg.Redirects, rd)
			}
		case "directoryindex":
			for _, a := range args {
				cfg.DirectoryIndex = append(cfg.DirectoryIndex, path.Base(a))
			}
		case "errordocument":
			if len(args) >= 2 {
				if code, err := strconv.Atoi(args[0]); err == nil {
					cfg.ErrorDocuments[code] = args[1]
				}
			}
		case "require":
			// 2.4 authz: only the blanket deny is meaningful here.
			if len(args) >= 2 && strings.EqualFold(args[0], "all") && strings.EqualFold(args[1], "denied") {
				htDenyAll(cfg, filesStack)
			}
		case "deny":
			// 2.2 authz: "Deny from all".
			if len(args) >= 2 && strings.EqualFold(args[0], "from") && strings.EqualFold(args[1], "all") {
				htDenyAll(cfg, filesStack)
			}
		case "options":
			// Only -Indexes matters conceptually; the Go server never lists
			// directories, and Caddy's file_server has browse off by default.
		case "directoryslash":
			if len(args) > 0 {
				cfg.DirectorySlashOff = strings.EqualFold(args[0], "off")
			}
		case "header":
			if op, ok := htParseHeaderOp(args); ok {
				cfg.Headers = append(cfg.Headers, op)
			}
		case "requestheader":
			if op, ok := htParseHeaderOp(args); ok {
				cfg.RequestHeaders = append(cfg.RequestHeaders, op)
			}
		case "addtype":
			// "AddType application/x-httpd-php[74] .html .htm ..." — legacy
			// sites use this to have a script literally named e.g. "x.html"
			// execute as PHP. Other AddType uses (real MIME overrides like
			// "AddType video/mp4 mp4") don't change how the file is served
			// here (net/http already infers a sane Content-Type), so only
			// the PHP-mapping case is meaningful to record.
			if len(args) >= 2 && strings.Contains(strings.ToLower(args[0]), "php") {
				for _, ext := range args[1:] {
					cfg.PHPExtensions = append(cfg.PHPExtensions, strings.ToLower(strings.TrimPrefix(ext, ".")))
				}
			}
		case "<files", "<filesmatch":
			pattern := ""
			if len(args) > 0 {
				pattern = strings.TrimSuffix(args[0], ">")
			}
			filesStack = append(filesStack, HtFileDeny{Pattern: pattern, Regex: dir == "<filesmatch"})
		case "</files>", "</filesmatch>":
			if len(filesStack) > 0 {
				filesStack = filesStack[:len(filesStack)-1]
			}
		default:
			cfg.Skipped = append(cfg.Skipped, fields[0])
		}
	}
	// A trailing RewriteCond chain with no RewriteRule is discarded.
}

// htParseCond parses RewriteCond args (TestString CondPattern [flags]).
func htParseCond(args []string) (HtCond, bool) {
	var c HtCond
	if len(args) < 2 {
		return c, false
	}
	c.Test, c.Pattern = args[0], args[1]
	for _, fl := range args[2:] {
		switch strings.ToUpper(strings.Trim(fl, "[]")) {
		case "NC", "NOCASE":
			c.NoCase = true
		case "OR", "ORNEXT":
			c.OrNext = true
		}
	}
	p := c.Pattern
	neg := strings.HasPrefix(p, "!")
	if neg {
		p = p[1:]
	}
	switch {
	case p == "-f" || p == "-d" || p == "-e" || p == "-s" || p == "-x":
		if neg {
			c.FileTest = "!" + p
		} else {
			c.FileTest = p
		}
		c.Pattern = ""
	case len(p) > 0 && (p[0] == '<' || p[0] == '>' || p[0] == '='):
		c.Lex = p[0]
		c.LexNeg = neg
		c.Pattern = p[1:]
	}
	return c, true
}

// htParseRuleFlags parses the [L,R=301,QSA] trailer.
func htParseRuleFlags(args []string) HtRuleFlags {
	var f HtRuleFlags
	if len(args) == 0 {
		return f
	}
	for _, fl := range strings.Split(strings.Trim(args[0], "[]"), ",") {
		fl = strings.TrimSpace(fl)
		up := strings.ToUpper(fl)
		switch {
		case up == "NC" || up == "NOCASE":
			f.NoCase = true
		case up == "L" || up == "LAST":
			f.Last = true
		case up == "END":
			f.End = true
			f.Last = true
		case up == "F" || up == "FORBIDDEN":
			f.Forbidden = true
		case up == "G" || up == "GONE":
			f.Gone = true
		case up == "QSA":
			f.QSA = true
		case up == "QSD":
			f.QSD = true
		case up == "P" || up == "PROXY":
			f.Proxy = true
		case strings.HasPrefix(up, "R"):
			f.Redirect = 302
			if i := strings.IndexByte(fl, '='); i >= 0 {
				switch strings.ToUpper(strings.TrimSpace(fl[i+1:])) {
				case "PERMANENT":
					f.Redirect = 301
				case "TEMP":
					f.Redirect = 302
				case "SEEOTHER":
					f.Redirect = 303
				default:
					if n, err := strconv.Atoi(fl[i+1:]); err == nil && n >= 300 && n < 400 {
						f.Redirect = n
					}
				}
			}
		}
	}
	return f
}

// htParseRedirect handles Redirect/RedirectMatch/RedirectPerm/RedirectTemp.
func htParseRedirect(dir string, args []string) (HtRedirect, bool) {
	var rd HtRedirect
	rd.Status = 302
	switch dir {
	case "redirectperm":
		rd.Status = 301
	case "redirecttemp":
		rd.Status = 302
	}
	if len(args) < 2 {
		return rd, false
	}
	i := 0
	if n, err := strconv.Atoi(args[0]); err == nil {
		switch {
		case n == 410:
			rd.Status = 410
		case n >= 300 && n < 400:
			rd.Status = n
		}
		i = 1
	} else {
		switch strings.ToLower(args[0]) {
		case "permanent":
			rd.Status, i = 301, 1
		case "temp":
			i = 1
		case "seeother":
			rd.Status, i = 303, 1
		case "gone":
			rd.Status, i = 410, 1
		}
	}
	rest := args[i:]
	if len(rest) < 2 {
		// "Redirect gone /path" (410 needs no target) is the only legal form.
		if len(rest) == 1 && rd.Status == 410 {
			rd.From = rest[0]
			return rd, true
		}
		return rd, false
	}
	rd.From, rd.Target = rest[0], rest[1]
	if dir == "redirectmatch" {
		rd.Match = true
		re := htCompile(rd.From, false)
		if re == nil {
			return rd, false
		}
		rd.Pattern = re
	}
	return rd, true
}

// htQuoteFields splits a directive line on whitespace, honouring double
// quotes (needed for patterns containing spaces).
func htQuoteFields(line string) []string {
	var out []string
	var cur strings.Builder
	inQ, esc := false, false
	for _, r := range line {
		switch {
		case esc:
			cur.WriteRune(r)
			esc = false
		case r == '\\':
			if inQ {
				esc = true
			} else {
				cur.WriteRune(r)
			}
		case r == '"':
			inQ = !inQ
		case (r == ' ' || r == '\t') && !inQ:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func htTrimSlashes(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "\"")
	s = strings.TrimSuffix(s, "\"")
	s = strings.Trim(s, "/")
	if s == "" {
		return ""
	}
	return "/" + s
}

// --- the engine ---------------------------------------------------------------

// htEngine executes the merged config for a request. root is the docroot on
// disk; urlPath the incoming (decoded) URL path; query the raw query string.
func htEngine(cfg *HtConfig, r *http.Request, root, urlPath, query string) htOutcome {
	if cfg == nil || (!cfg.RewriteEngine && len(cfg.Redirects) == 0 && len(cfg.FileDenies) == 0 && !cfg.DenyAll) {
		return htOutcome{}
	}
	if cfg.DenyAll {
		return htOutcome{Status: 403} // "Require all denied" / "Deny from all"
	}
	for _, fd := range cfg.FileDenies {
		if htFileDenyMatches(fd, urlPath) {
			return htOutcome{Status: 403} // scoped <Files>/<FilesMatch> deny
		}
	}
	ctx := &htCtx{
		req:     r,
		root:    root,
		cfg:     cfg,
		path:    urlPath,
		query:   query,
		origURI: urlPath,
		origQ:   query,
	}
	// Plain Redirect/RedirectMatch first: mod_alias runs before mod_rewrite
	// in URL space, and most front-controllers rely on that order.
	for _, rd := range cfg.Redirects {
		if rd.Match {
			if rd.Pattern == nil {
				continue
			}
			m := rd.Pattern.FindStringSubmatch(urlPath)
			if m == nil {
				continue
			}
			ctx.ruleRefs = m
		} else {
			hits := rd.From == "/" || urlPath == rd.From || strings.HasPrefix(urlPath, rd.From+"/")
			if !hits {
				continue
			}
		}
		if rd.Status == 410 {
			return htOutcome{Status: 410}
		}
		target := htExpandSub(ctx, rd.Target)
		// Prefix Redirects append the unmatched remainder, like mod_alias:
		// "Redirect 301 /old /new" sends /old/extra to /new/extra.
		if !rd.Match && rd.From != "/" && len(urlPath) > len(rd.From) {
			target += strings.TrimPrefix(urlPath, rd.From)
		}
		return htOutcome{Redirect: target, Status: rd.Status}
	}
	if !cfg.RewriteEngine {
		return htOutcome{}
	}

	for pass := 0; pass < 10; pass++ {
		changed := false
		stop := false
		for i := range cfg.Rules {
			rule := &cfg.Rules[i]
			res, done, matched := htApplyRule(ctx, rule)
			if matched {
				changed = true
			}
			if res.Status == 403 || res.Status == 410 || res.Redirect != "" {
				return res
			}
			if done {
				stop = true
				break
			}
		}
		if stop || !changed {
			break
		}
	}
	if ctx.path == ctx.origURI && ctx.query == ctx.origQ {
		return htOutcome{} // net no-op: leave request as-is
	}
	return htOutcome{Path: ctx.path, Query: ctx.query}
}

type htCtx struct {
	req      *http.Request
	root     string // docroot
	cfg      *HtConfig
	path     string // current URL path (decoded, starts with /)
	query    string
	origURI  string
	origQ    string
	ruleRefs []string // $1..$9 from the last matching rule
	subRefs  []string // %1..$9 from the last matching condition
}

// htApplyRule evaluates one rule (conditions then pattern) against the
// current URL state. matched reports whether the rule fired.
func htApplyRule(ctx *htCtx, rule *HtRule) (res htOutcome, done, matched bool) {
	// Conditions: AND by default, OR chains where flagged.
	for i := 0; i < len(rule.Conds); i++ {
		c := rule.Conds[i]
		pass := htEvalCond(ctx, &c)
		if pass {
			if m := htCondGroups(ctx, &c); m != nil {
				ctx.subRefs = m
			}
		}
		if orNext := c.OrNext && i+1 < len(rule.Conds); orNext {
			next := rule.Conds[i+1]
			if !pass {
				if htEvalCond(ctx, &next) {
					pass = true
					if m := htCondGroups(ctx, &next); m != nil {
						ctx.subRefs = m
					}
				}
			}
			i++ // the OR partner is consumed either way
		}
		if !pass {
			return res, false, false
		}
	}

	// The pattern matches the per-directory-relative URL path.
	subject := htPerDirSubject(ctx.cfg, ctx.path)
	re := htCompile(rule.Pattern, rule.Flags.NoCase)
	if re == nil {
		return res, false, false // uncompilable (PCRE lookaround): skip rule
	}
	m := re.FindStringSubmatch(subject)
	if m == nil {
		return res, false, false
	}
	ctx.ruleRefs = m

	if rule.Flags.Forbidden {
		return htOutcome{Status: 403}, true, true
	}
	if rule.Flags.Gone {
		return htOutcome{Status: 410}, true, true
	}

	sub := htExpandSub(ctx, rule.Sub)
	isRedirect := rule.Flags.Redirect != 0 || htIsAbsoluteURL(sub)

	if isRedirect {
		status := rule.Flags.Redirect
		if status == 0 {
			status = 302
		}
		if rule.Flags.QSA && ctx.query != "" && !strings.Contains(sub, "?") {
			sub += "?" + ctx.query
		}
		return htOutcome{Redirect: sub, Status: status}, true, true
	}

	if sub == "-" {
		// Pass-through: matched but no substitution (used with conds/flags).
		return res, rule.Flags.Last, true
	}

	// Internal rewrite. A '?' in the substitution replaces the query string
	// entirely ("index.php?" clears it) unless QSA appends the original.
	newPath, newQuery := sub, ctx.query
	if i := strings.IndexByte(newPath, '?'); i >= 0 {
		newQuery = newPath[i+1:]
		newPath = newPath[:i]
	}
	if rule.Flags.QSA && ctx.query != "" && newQuery != ctx.query {
		if newQuery == "" {
			newQuery = ctx.query
		} else {
			newQuery += "&" + ctx.query
		}
	}
	if rule.Flags.QSD {
		newQuery = ""
	}

	if !strings.HasPrefix(newPath, "/") && !htIsAbsoluteURL(newPath) {
		// Relative substitution: re-apply the base (or per-dir prefix),
		// mirroring Apache's per-directory substitution rules.
		prefix := "/"
		if cfg := ctx.cfg; cfg.RewriteBase != "" {
			prefix = cfg.RewriteBase
		} else if cfg.DirURL != "" {
			prefix = cfg.DirURL
		}
		newPath = strings.TrimSuffix(prefix, "/") + "/" + strings.TrimPrefix(newPath, "/")
	}
	ctx.path = path.Clean("/" + strings.TrimPrefix(newPath, "/"))
	ctx.query = newQuery
	if rule.Flags.Last || rule.Flags.End {
		return res, true, true
	}
	return res, false, true
}

// htPerDirSubject strips the URL prefix of the deepest .htaccess directory
// (and its leading slash) the way Apache matches per-directory patterns.
func htPerDirSubject(cfg *HtConfig, urlPath string) string {
	p := urlPath
	if cfg.DirURL != "" && cfg.DirURL != "/" && strings.HasPrefix(p, cfg.DirURL+"/") {
		p = strings.TrimPrefix(p, cfg.DirURL)
	}
	return strings.TrimPrefix(p, "/")
}

// htCondGroups returns capture groups when the cond pattern is a regex.
func htCondGroups(ctx *htCtx, c *HtCond) []string {
	if c.FileTest != "" || c.Lex != 0 || c.Pattern == "" {
		return nil
	}
	re := htCompile(c.Pattern, c.NoCase)
	if re == nil {
		return nil
	}
	return re.FindStringSubmatch(htExpand(ctx, c.Test))
}

func htIsAbsoluteURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "//")
}

// htExpandSub expands $N (rule) and %N (cond) backreferences plus %{VAR} in
// a substitution string.
func htExpandSub(ctx *htCtx, s string) string {
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '$' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9':
			n := int(s[i+1] - '0')
			if ctx.ruleRefs != nil && n < len(ctx.ruleRefs) {
				sb.WriteString(ctx.ruleRefs[n])
			}
			i++
		case s[i] == '%' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9':
			n := int(s[i+1] - '0')
			if ctx.subRefs != nil && n < len(ctx.subRefs) {
				sb.WriteString(ctx.subRefs[n])
			}
			i++
		case s[i] == '%' && strings.HasPrefix(s[i:], "%{"):
			if end := strings.IndexByte(s[i:], '}'); end > 0 {
				sb.WriteString(htServerVar(ctx, s[i+2:i+end]))
				i += end
			} else {
				sb.WriteByte(s[i])
			}
		default:
			sb.WriteByte(s[i])
		}
	}
	return sb.String()
}

// htExpand expands server variables (and rule backreferences) in test
// strings.
func htExpand(ctx *htCtx, s string) string {
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '%' && strings.HasPrefix(s[i:], "%{"):
			if end := strings.IndexByte(s[i:], '}'); end > 0 {
				sb.WriteString(htServerVar(ctx, s[i+2:i+end]))
				i += end
				continue
			}
			sb.WriteByte(s[i])
		case s[i] == '$' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9':
			n := int(s[i+1] - '0')
			if ctx.ruleRefs != nil && n < len(ctx.ruleRefs) {
				sb.WriteString(ctx.ruleRefs[n])
			}
			i++
		default:
			sb.WriteByte(s[i])
		}
	}
	return sb.String()
}

// htServerVar resolves %{...} test strings. Unrecognised variables resolve
// to "" so dependent rules simply fail to match rather than erroring.
func htServerVar(ctx *htCtx, name string) string {
	r := ctx.req
	upper := strings.ToUpper(name)
	switch upper {
	case "REQUEST_URI":
		if ctx.query != "" {
			return ctx.path + "?" + ctx.query
		}
		return ctx.path
	case "REQUEST_FILENAME", "SCRIPT_FILENAME":
		return filepath.Join(ctx.root, filepath.FromSlash(strings.TrimPrefix(ctx.path, "/")))
	case "DOCUMENT_ROOT":
		return ctx.root
	case "QUERY_STRING":
		return ctx.query
	case "REQUEST_METHOD":
		return r.Method
	case "HTTP_HOST", "SERVER_NAME":
		return strings.Split(r.Host, ":")[0]
	case "SERVER_PORT":
		if _, p, err := net.SplitHostPort(r.Host); err == nil {
			return p
		}
		if r.TLS != nil {
			return "443"
		}
		return "80"
	case "HTTPS":
		if r.TLS != nil {
			return "on"
		}
		return "off"
	case "REMOTE_ADDR":
		return remoteIP(r)
	case "HTTP_REFERER":
		return r.Header.Get("Referer")
	case "HTTP_USER_AGENT":
		return r.Header.Get("User-Agent")
	case "HTTP_COOKIE":
		return r.Header.Get("Cookie")
	case "THE_REQUEST":
		return r.Method + " " + r.URL.RequestURI() + " " + r.Proto
	case "SERVER_PROTOCOL":
		return r.Proto
	case "REQUEST_SCHEME":
		if r.TLS != nil {
			return "https"
		}
		return "http"
	}
	if strings.HasPrefix(upper, "HTTP:") {
		return r.Header.Get(strings.ReplaceAll(strings.TrimPrefix(upper, "HTTP:"), "_", "-"))
	}
	if strings.HasPrefix(upper, "HTTP_") {
		return r.Header.Get(strings.ReplaceAll(strings.TrimPrefix(upper, "HTTP_"), "_", "-"))
	}
	if strings.HasPrefix(upper, "ENV:") {
		return os.Getenv(strings.TrimPrefix(upper, "ENV:"))
	}
	return ""
}

// htEvalCond evaluates one condition against the current state.
func htEvalCond(ctx *htCtx, c *HtCond) bool {
	test := htExpand(ctx, c.Test)
	switch {
	case c.FileTest != "":
		neg := strings.HasPrefix(c.FileTest, "!")
		op := strings.TrimPrefix(c.FileTest, "!")
		st, err := os.Stat(test)
		pass := err == nil
		switch op {
		case "-f":
			pass = pass && st.Mode().IsRegular()
		case "-d":
			pass = pass && st.IsDir()
		case "-s":
			pass = pass && st.Size() > 0
		case "-x":
			pass = pass && st.Mode().Perm()&0o111 != 0
		case "-e":
			// exists (file or dir): covered by the initial err == nil
		}
		if neg {
			pass = !pass
		}
		return pass
	case c.Lex == '<':
		other := htExpand(ctx, c.Pattern)
		pass := test < other
		if c.NoCase {
			pass = strings.ToLower(test) < strings.ToLower(other)
		}
		if c.LexNeg {
			pass = !pass
		}
		return pass
	case c.Lex == '>':
		other := htExpand(ctx, c.Pattern)
		pass := test > other
		if c.NoCase {
			pass = strings.ToLower(test) > strings.ToLower(other)
		}
		if c.LexNeg {
			pass = !pass
		}
		return pass
	case c.Lex == '=':
		other := htExpand(ctx, c.Pattern)
		pass := test == other
		if c.NoCase {
			pass = strings.EqualFold(test, other)
		}
		if c.LexNeg {
			pass = !pass
		}
		return pass
	case c.Pattern == "":
		return test == ""
	default:
		re := htCompile(c.Pattern, c.NoCase)
		return re != nil && re.MatchString(test)
	}
}
