package svc

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aegis/internal/store"
)

func gzReq(method string) *http.Request {
	r := httptest.NewRequest(method, "/", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	return r
}

func serveThrough(r *http.Request, h http.HandlerFunc) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	gz := newGzipWriter(rec, r)
	h(gz, r)
	gz.Close()
	return rec
}

func TestGzipCompressesLargeText(t *testing.T) {
	body := strings.Repeat("<p>hello world</p>", 500)
	rec := serveThrough(gzReq("GET"), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Length", "9000")
		w.Header().Set("Etag", `"abc"`)
		_, _ = io.WriteString(w, body)
	})
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("not compressed: %v", rec.Header())
	}
	if rec.Header().Get("Content-Length") != "" || !strings.HasPrefix(rec.Header().Get("Etag"), "W/") {
		t.Errorf("length/etag not adjusted: %v", rec.Header())
	}
	if !strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") {
		t.Errorf("missing Vary: %v", rec.Header())
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(zr)
	if string(got) != body {
		t.Errorf("round trip mismatch (%d bytes)", len(got))
	}
}

func TestGzipSkips(t *testing.T) {
	big := strings.Repeat("x", 4000)
	cases := map[string]struct {
		method string
		mod    func(r *http.Request)
		ct     string
		code   int
		cl     string
		extra  map[string]string
	}{
		"binary type":  {"GET", nil, "image/png", 200, "", nil},
		"tiny body":    {"GET", nil, "text/html", 200, "100", nil},
		"error status": {"GET", nil, "text/html", 500, "", nil},
		"not modified": {"GET", nil, "text/html", 304, "", nil},
		"head":         {"HEAD", nil, "text/html", 200, "", nil},
		"range":        {"GET", func(r *http.Request) { r.Header.Set("Range", "bytes=0-10") }, "text/html", 200, "", nil},
		"already":      {"GET", nil, "text/html", 200, "", map[string]string{"Content-Encoding": "br"}},
		"no-transform": {"GET", nil, "text/html", 200, "", map[string]string{"Cache-Control": "no-transform"}},
		"sse":          {"GET", nil, "text/event-stream", 200, "", nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := gzReq(c.method)
			if c.mod != nil {
				c.mod(r)
			}
			rec := serveThrough(r, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", c.ct)
				if c.cl != "" {
					w.Header().Set("Content-Length", c.cl)
				}
				for k, v := range c.extra {
					w.Header().Set(k, v)
				}
				w.WriteHeader(c.code)
				if c.code != 304 && c.method != "HEAD" {
					_, _ = io.WriteString(w, big)
				}
			})
			if enc := rec.Header().Get("Content-Encoding"); enc == "gzip" {
				t.Errorf("should not be gzipped")
			}
		})
	}
}

func TestAcceptsGzip(t *testing.T) {
	for ae, want := range map[string]bool{
		"gzip": true, "gzip, deflate, br": true, "deflate, gzip;q=0.5": true, "GZIP": true,
		"gzip;q=0": false, "br": false, "": false, "identity": false,
	} {
		r := httptest.NewRequest("GET", "/", nil)
		if ae != "" {
			r.Header.Set("Accept-Encoding", ae)
		}
		if got := acceptsGzip(r); got != want {
			t.Errorf("%q: got %v want %v", ae, got, want)
		}
	}
}

func TestValidatePerf(t *testing.T) {
	if _, err := ValidatePerf(store.PerfSettings{StaticMaxAge: -1}); err == nil {
		t.Error("negative max-age accepted")
	}
	if _, err := ValidatePerf(store.PerfSettings{StaticMaxAge: maxStaticMaxAge + 1}); err == nil {
		t.Error("huge max-age accepted")
	}
	if _, err := ValidatePerf(store.PerfSettings{PageCacheTTL: maxPageCacheTTL + 1}); err == nil {
		t.Error("huge ttl accepted")
	}
	p, err := ValidatePerf(store.PerfSettings{PageCache: true})
	if err != nil || p.PageCacheTTL != DefaultPageCacheTTL {
		t.Errorf("default ttl: %+v %v", p, err)
	}
	p, _ = ValidatePerf(store.PerfSettings{PageCacheTTL: 60})
	if p.PageCacheTTL != 0 {
		t.Errorf("ttl kept while cache off: %+v", p)
	}
}

func TestCacheableRequest(t *testing.T) {
	mk := func(method, target string, mod func(*http.Request)) *http.Request {
		r := httptest.NewRequest(method, target, nil)
		if mod != nil {
			mod(r)
		}
		return r
	}
	cookie := func(name string) func(*http.Request) {
		return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: name, Value: "1"}) }
	}
	cases := []struct {
		name string
		r    *http.Request
		want bool
	}{
		{"plain get", mk("GET", "/blog/?p=2", nil), true},
		{"post", mk("POST", "/", nil), false},
		{"head", mk("HEAD", "/", nil), false},
		{"analytics cookie", mk("GET", "/", cookie("_ga")), true},
		{"session cookie", mk("GET", "/", cookie("PHPSESSID")), false},
		{"wp login cookie", mk("GET", "/", cookie("wordpress_logged_in_x")), false},
		{"authorization", mk("GET", "/", func(r *http.Request) { r.Header.Set("Authorization", "Basic x") }), false},
		{"wp-admin", mk("GET", "/wp-admin/index.php", nil), false},
		{"wp-login", mk("GET", "/wp-login.php", nil), false},
		{"cart", mk("GET", "/Cart/", nil), false},
		{"plain login script", mk("GET", "/login.php", nil), false},
		{"admin front controller", mk("GET", "/admin/index.php", nil), false},
		{"similar prefix ok", mk("GET", "/administration-history", nil), true},
	}
	for _, c := range cases {
		if got := cacheableRequest(c.r); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestCacheableResponse(t *testing.T) {
	h := func(kv ...string) http.Header {
		out := http.Header{}
		for i := 0; i < len(kv); i += 2 {
			out.Add(kv[i], kv[i+1])
		}
		return out
	}
	if !cacheableResponse(200, h("Content-Type", "text/html"), []byte("ok")) {
		t.Error("plain 200 should cache")
	}
	for name, c := range map[string]struct {
		status int
		h      http.Header
		body   []byte
	}{
		"404":        {404, h(), nil},
		"set-cookie": {200, h("Set-Cookie", "a=b"), nil},
		"private":    {200, h("Cache-Control", "private, max-age=0"), nil},
		"no-store":   {200, h("Cache-Control", "no-store"), nil},
		"no-cache":   {200, h("Cache-Control", "no-cache"), nil},
		"vary star":  {200, h("Vary", "*"), nil},
		"too big":    {200, h(), make([]byte, pageCacheMaxBody+1)},
	} {
		if cacheableResponse(c.status, c.h, c.body) {
			t.Errorf("%s should not cache", name)
		}
	}
}

func TestPageCacheLifecycle(t *testing.T) {
	c := newPageCache()
	hdr := http.Header{"Content-Type": {"text/html"}}
	if c.Get("a.test", "k1") != nil {
		t.Fatal("empty cache hit")
	}
	c.Put("a.test", "k1", 200, hdr, []byte("one"), time.Minute)
	c.Put("b.test", "k2", 200, hdr, []byte("two"), time.Minute)
	hdr.Set("Content-Type", "mutated") // the store must keep its own copy
	e := c.Get("a.test", "k1")
	if e == nil || string(e.body) != "one" || e.header.Get("Content-Type") != "text/html" {
		t.Fatalf("bad hit: %+v", e)
	}
	if s := c.Stats("a.test"); s.Hits != 1 || s.Misses != 1 || s.Entries != 1 || s.Bytes != 3 {
		t.Errorf("stats: %+v", s)
	}
	if n := c.Purge("a.test"); n != 1 {
		t.Errorf("purged %d", n)
	}
	if c.Get("a.test", "k1") != nil {
		t.Error("purged entry still served")
	}
	if c.Get("b.test", "k2") == nil {
		t.Error("purge of one domain removed another's page")
	}
	c.Put("b.test", "k3", 200, hdr, []byte("x"), -time.Second)
	if c.Get("b.test", "k3") != nil {
		t.Error("expired entry served")
	}
	if s := c.Stats("b.test"); s.Entries != 1 {
		t.Errorf("expired entry not evicted: %+v", s)
	}
}

func TestPageCacheEvictsOverCap(t *testing.T) {
	c := newPageCache()
	chunk := make([]byte, pageCacheMaxBody)
	for i := 0; i < pageCacheMaxBytes/pageCacheMaxBody+8; i++ {
		c.Put("a.test", string(rune('a'+i)), 200, http.Header{}, chunk, time.Hour)
	}
	if c.bytes > pageCacheMaxBytes {
		t.Errorf("cache over cap: %d", c.bytes)
	}
	if c.Get("a.test", "a") != nil {
		t.Error("oldest entry should have been evicted")
	}
}

func TestCacheKeySeparatesHostAndScheme(t *testing.T) {
	a := httptest.NewRequest("GET", "http://a.test/x?y=1", nil)
	b := httptest.NewRequest("GET", "http://b.test/x?y=1", nil)
	if cacheKey(a) == cacheKey(b) {
		t.Error("different hosts share a key")
	}
	s := httptest.NewRequest("GET", "https://a.test/x?y=1", nil)
	if cacheKey(a) == cacheKey(s) {
		t.Error("http and https share a key")
	}
}

func TestStaticCacheable(t *testing.T) {
	for name, want := range map[string]bool{
		"app.JS": true, "logo.png": true, "font.woff2": true, "index.html": false, "data.json": false, "x": false,
	} {
		if got := staticCacheable(name); got != want {
			t.Errorf("%s: got %v want %v", name, got, want)
		}
	}
}

func TestPHPSupport(t *testing.T) {
	now := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	for ver, want := range map[string]string{
		"7.4": "eol", "8.0": "eol", "8.1": "eol", "5.6": "eol",
		"8.2": "ending", "8.3": "supported", "8.4": "supported", "9.9": "supported",
	} {
		if got, _ := phpSupport(ver, now); got != want {
			t.Errorf("PHP %s: got %s want %s", ver, got, want)
		}
	}
	if _, until := phpSupport("8.3", now); until != "2027-12-31" {
		t.Errorf("until = %q", until)
	}
}

func TestPHPOpcacheSettings(t *testing.T) {
	ok := map[string]string{"opcache.enable": "on", "opcache.validate_timestamps": "Off", "opcache.revalidate_freq": "60"}
	if err := ValidatePHPIniSettings(ok); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []map[string]string{
		{"opcache.enable": "yes"},
		{"opcache.revalidate_freq": "-1"},
		{"opcache.revalidate_freq": "60\nphp_admin_value[auto_prepend_file]=/x"},
		{"opcache.memory_consumption": "512"}, // global, cannot differ per pool
	} {
		if ValidatePHPIniSettings(bad) == nil {
			t.Errorf("accepted %v", bad)
		}
	}
	got := sanitizedPHPIniLines(ok)
	for _, want := range []string{"php_admin_flag[opcache.enable] = on", "php_admin_flag[opcache.validate_timestamps] = off", "php_admin_value[opcache.revalidate_freq] = 60"} {
		if !strings.Contains(got, want) {
			t.Errorf("pool lines missing %q:\n%s", want, got)
		}
	}
}
