//go:build linux

package svc

import (
	"compress/gzip"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"aegis/internal/store"
)

// End to end through GoHandler: a real static file served as a real account.
func TestGoHandlerCompressionAndStaticCaching(t *testing.T) {
	home := accessibleTempDir(t, 0o755)
	seedAccount(t, "perfuser", home)
	css := strings.Repeat("body{color:red}\n", 200)
	mustWrite(t, filepath.Join(home, "public", "site.css"), css, 0o644)
	mustWrite(t, filepath.Join(home, "public", "page.html"), "<p>"+strings.Repeat("hi ", 800)+"</p>", 0o644)

	off := false
	routes := map[string]store.PerfSettings{
		"on.test":      {StaticMaxAge: 3600},
		"off.test":     {Compress: &off},
		"nocache.test": {},
	}
	w := &WebServer{goRoutes: map[string]GoRoute{}}
	for host, perf := range routes {
		w.goRoutes[host] = GoRoute{Domain: host, Root: filepath.Join(home, "public"), Owner: "perfuser", Perf: perf}
	}
	h := w.GoHandler()
	get := func(host, target, ae string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://"+host+target, nil)
		if ae != "" {
			r.Header.Set("Accept-Encoding", ae)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}

	rec := get("on.test", "/site.css", "gzip")
	if rec.Code != 200 || rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("css not gzipped: %d %v", rec.Code, rec.Header())
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := io.ReadAll(zr); string(got) != css {
		t.Error("gzip round trip differs")
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=3600" {
		t.Errorf("static Cache-Control = %q", cc)
	}
	if rec := get("on.test", "/site.css", ""); rec.Header().Get("Content-Encoding") != "" || rec.Body.String() != css {
		t.Error("client without gzip must get the plain file")
	}
	if rec := get("on.test", "/page.html", "gzip"); rec.Header().Get("Cache-Control") != "" {
		t.Errorf("html document got a static Cache-Control: %q", rec.Header().Get("Cache-Control"))
	}
	if rec := get("off.test", "/site.css", "gzip"); rec.Header().Get("Content-Encoding") != "" {
		t.Error("compression disabled but response was gzipped")
	}
	if rec := get("nocache.test", "/site.css", "gzip"); rec.Header().Get("Cache-Control") != "" || rec.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("defaults wrong: %v", rec.Header())
	}
}
