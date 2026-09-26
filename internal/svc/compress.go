package svc

import (
	"compress/gzip"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"aegis/internal/store"
)

// gzip compression for the native Go web server.
//
// Text (HTML, CSS, JS, JSON, XML, SVG, fonts) usually shrinks by 70-90%, so this is
// the cheapest performance win there is. The decision is made when the response
// headers are written, from what the handler actually produced, so it never
// compresses something it should not.

// minCompressSize is the smallest body worth compressing: below it the gzip
// header and the CPU cost outweigh the saving.
const minCompressSize = 1024

var gzipPool = sync.Pool{New: func() any {
	w, _ := gzip.NewWriterLevel(nil, gzip.DefaultCompression)
	return w
}}

// compressibleType reports whether a Content-Type is text-like enough to gzip.
func compressibleType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0]))
	switch {
	case ct == "":
		return false
	case strings.HasPrefix(ct, "text/") && ct != "text/event-stream":
		return true
	case strings.HasSuffix(ct, "+json"), strings.HasSuffix(ct, "+xml"):
		return true
	}
	switch ct {
	case "application/json", "application/javascript", "application/x-javascript",
		"application/ecmascript", "application/xml", "application/xhtml+xml",
		"application/rss+xml", "application/atom+xml", "application/manifest+json",
		"application/wasm", "image/svg+xml", "image/x-icon", "image/vnd.microsoft.icon",
		"application/vnd.ms-fontobject", "font/ttf", "font/otf", "application/x-font-ttf",
		"application/x-font-opentype":
		return true
	}
	return false
}

// acceptsGzip reports whether the request's Accept-Encoding allows gzip.
func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(strings.Join(r.Header.Values("Accept-Encoding"), ","), ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(name), "gzip") {
			continue
		}
		if q, ok := strings.CutPrefix(strings.ReplaceAll(strings.TrimSpace(params), " ", ""), "q="); ok {
			if f, err := strconv.ParseFloat(q, 64); err == nil && f == 0 {
				return false
			}
		}
		return true
	}
	return false
}

// gzipResponseWriter compresses the response body when the response allows it.
type gzipResponseWriter struct {
	http.ResponseWriter
	req      *http.Request
	gz       *gzip.Writer
	decided  bool
	compress bool
}

// newGzipWriter wraps w for r. Call Close when the handler has returned.
func newGzipWriter(w http.ResponseWriter, r *http.Request) *gzipResponseWriter {
	return &gzipResponseWriter{ResponseWriter: w, req: r}
}

func addVary(h http.Header, v string) {
	for _, existing := range h.Values("Vary") {
		for _, p := range strings.Split(existing, ",") {
			if strings.EqualFold(strings.TrimSpace(p), v) || strings.TrimSpace(p) == "*" {
				return
			}
		}
	}
	h.Add("Vary", v)
}

func (g *gzipResponseWriter) decide(code int) {
	g.decided = true
	h := g.Header()
	if !compressibleType(h.Get("Content-Type")) {
		return
	}
	// The response varies with Accept-Encoding whether or not this request gets
	// the compressed form, so shared caches must key on it.
	addVary(h, "Accept-Encoding")
	switch {
	case code < 200 || code == http.StatusNoContent || code == http.StatusNotModified || code >= 400,
		code == http.StatusPartialContent,
		g.req.Method == http.MethodHead,
		g.req.Header.Get("Range") != "",
		h.Get("Content-Range") != "",
		h.Get("Content-Encoding") != "",
		strings.Contains(strings.ToLower(h.Get("Cache-Control")), "no-transform"):
		return
	}
	if n, err := strconv.ParseInt(h.Get("Content-Length"), 10, 64); err == nil && n < minCompressSize {
		return
	}
	g.compress = true
	h.Set("Content-Encoding", "gzip")
	h.Del("Content-Length") // the compressed length is not known yet
	h.Del("Accept-Ranges")
	if etag := h.Get("Etag"); etag != "" && !strings.HasPrefix(etag, "W/") {
		h.Set("Etag", "W/"+etag) // the compressed bytes differ from the strong validator
	}
}

func (g *gzipResponseWriter) WriteHeader(code int) {
	if !g.decided {
		g.decide(code)
	}
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	if !g.decided {
		g.decide(http.StatusOK)
		g.ResponseWriter.WriteHeader(http.StatusOK)
	}
	if !g.compress {
		return g.ResponseWriter.Write(b)
	}
	if g.gz == nil {
		g.gz = gzipPool.Get().(*gzip.Writer)
		g.gz.Reset(g.ResponseWriter)
	}
	return g.gz.Write(b)
}

// Flush lets streaming handlers push data through the compressor.
func (g *gzipResponseWriter) Flush() {
	if g.gz != nil {
		_ = g.gz.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the underlying writer to http.ResponseController.
func (g *gzipResponseWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }

// Close finishes the gzip stream and returns the compressor to the pool.
func (g *gzipResponseWriter) Close() {
	if g.gz != nil {
		_ = g.gz.Close()
		g.gz.Reset(nil)
		gzipPool.Put(g.gz)
		g.gz = nil
	}
}

// Limits for the per-domain performance options.
const (
	maxStaticMaxAge = 365 * 24 * 3600
	maxPageCacheTTL = 24 * 3600
)

// ValidatePerf checks a domain's performance options and fills in defaults.
func ValidatePerf(p store.PerfSettings) (store.PerfSettings, error) {
	if p.StaticMaxAge < 0 || p.StaticMaxAge > maxStaticMaxAge {
		return p, fmt.Errorf("static cache lifetime must be between 0 and %d seconds", maxStaticMaxAge)
	}
	if p.PageCacheTTL < 0 || p.PageCacheTTL > maxPageCacheTTL {
		return p, fmt.Errorf("page cache lifetime must be between 0 and %d seconds", maxPageCacheTTL)
	}
	if p.PageCache && p.PageCacheTTL == 0 {
		p.PageCacheTTL = DefaultPageCacheTTL
	}
	if !p.PageCache {
		p.PageCacheTTL = 0
	}
	return p, nil
}
