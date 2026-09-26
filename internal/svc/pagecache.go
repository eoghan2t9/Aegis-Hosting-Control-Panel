package svc

import (
	"container/list"
	"net/http"
	"strings"
	"sync"
	"time"
)

// In-memory page cache for the native Go web server.
//
// It stores the finished response of anonymous GET requests to PHP pages, so a
// busy WordPress-style site answers most visitors without starting PHP at all.
// It is deliberately conservative: anything that could be personalised is never
// stored and never served from the cache (see cacheableRequest/cacheableResponse).

const (
	// pageCacheMaxBody is the largest response body worth caching.
	pageCacheMaxBody = 2 << 20
	// pageCacheMaxBytes caps the whole cache; the least recently used entries go first.
	pageCacheMaxBytes = 128 << 20
	// DefaultPageCacheTTL is used when a site enables the cache without a TTL.
	DefaultPageCacheTTL = 300
)

// cacheSafeCookies are cookies that do not change the page (analytics and
// consent banners), so a visitor carrying only these still gets the cached page.
// Any other cookie may be a login or a cart, so it bypasses the cache.
var cacheSafeCookies = []string{"_ga", "_gid", "_gat", "_gcl_", "_fbp", "_fbc", "__utm", "cookielawinfo", "cookie_notice", "_pk_"}

// cacheBypassPaths are never cached even for anonymous visitors. They are matched
// with a trailing ".php" removed, so "/login" also covers "/login.php".
var cacheBypassPaths = []string{
	"/wp-admin", "/wp-login", "/wp-cron", "/wp-json", "/xmlrpc",
	"/admin", "/administrator", "/login", "/logout", "/register", "/signin", "/signup",
	"/cart", "/checkout", "/basket", "/my-account", "/account", "/user", "/wc-api",
}

type pageCacheEntry struct {
	key     string
	domain  string
	status  int
	header  http.Header
	body    []byte
	expires time.Time
	stored  time.Time
}

// PageCache is a size-bounded LRU of rendered pages, safe for concurrent use.
type PageCache struct {
	mu      sync.Mutex
	entries map[string]*list.Element
	order   *list.List // front = most recently used
	bytes   int64
	stats   map[string]*PageCacheStats
}

// PageCacheStats is the per-domain counter set shown in the panel.
type PageCacheStats struct {
	Hits    int64 `json:"hits"`
	Misses  int64 `json:"misses"`
	Entries int   `json:"entries"`
	Bytes   int64 `json:"bytes"`
}

func newPageCache() *PageCache {
	return &PageCache{entries: map[string]*list.Element{}, order: list.New(), stats: map[string]*PageCacheStats{}}
}

func (c *PageCache) statsFor(domain string) *PageCacheStats {
	s := c.stats[domain]
	if s == nil {
		s = &PageCacheStats{}
		c.stats[domain] = s
	}
	return s
}

// cacheableRequest reports whether a request may be answered from, or stored in, the cache.
func cacheableRequest(r *http.Request) bool {
	if r.Method != http.MethodGet || r.Header.Get("Authorization") != "" || r.Header.Get("Range") != "" {
		return false
	}
	p := strings.TrimSuffix(strings.ToLower(r.URL.Path), ".php")
	for _, b := range cacheBypassPaths {
		if p == b || strings.HasPrefix(p, b+"/") {
			return false
		}
	}
	for _, c := range r.Cookies() {
		safe := false
		for _, s := range cacheSafeCookies {
			if strings.HasPrefix(c.Name, s) {
				safe = true
				break
			}
		}
		if !safe {
			return false
		}
	}
	return true
}

// cacheableResponse reports whether a finished response may be stored.
func cacheableResponse(status int, h http.Header, body []byte) bool {
	if status != http.StatusOK || len(body) > pageCacheMaxBody {
		return false
	}
	if len(h.Values("Set-Cookie")) > 0 {
		return false
	}
	cc := strings.ToLower(h.Get("Cache-Control"))
	for _, bad := range []string{"private", "no-store", "no-cache"} {
		if strings.Contains(cc, bad) {
			return false
		}
	}
	for _, v := range h.Values("Vary") {
		if strings.Contains(v, "*") {
			return false
		}
	}
	return true
}

// cacheKey separates hostnames and schemes because a page can embed either
// (absolute links, canonical URLs) and must not leak one variant to the other.
func cacheKey(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "\x00" + strings.ToLower(r.Host) + "\x00" + r.URL.RequestURI()
}

// Get returns a stored page, or nil. A hit refreshes the entry's LRU position.
func (c *PageCache) Get(domain, key string) *pageCacheEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[key]
	if !ok {
		c.statsFor(domain).Misses++
		return nil
	}
	e := el.Value.(*pageCacheEntry)
	if time.Now().After(e.expires) {
		c.removeLocked(el)
		c.statsFor(domain).Misses++
		return nil
	}
	c.order.MoveToFront(el)
	c.statsFor(domain).Hits++
	return e
}

// Put stores a page for ttl, evicting the least recently used entries when over the cap.
func (c *PageCache) Put(domain, key string, status int, h http.Header, body []byte, ttl time.Duration) {
	e := &pageCacheEntry{
		key: key, domain: domain, status: status, header: h.Clone(),
		body: append([]byte(nil), body...), stored: time.Now(), expires: time.Now().Add(ttl),
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.entries[key]; ok {
		c.removeLocked(old)
	}
	c.entries[key] = c.order.PushFront(e)
	c.bytes += int64(len(e.body))
	for c.bytes > pageCacheMaxBytes && c.order.Len() > 1 {
		c.removeLocked(c.order.Back())
	}
}

func (c *PageCache) removeLocked(el *list.Element) {
	e := el.Value.(*pageCacheEntry)
	c.order.Remove(el)
	delete(c.entries, e.key)
	c.bytes -= int64(len(e.body))
}

// Purge drops every cached page of a domain and returns how many there were.
func (c *PageCache) Purge(domain string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for el := c.order.Front(); el != nil; {
		next := el.Next()
		if el.Value.(*pageCacheEntry).domain == domain {
			c.removeLocked(el)
			n++
		}
		el = next
	}
	return n
}

// Stats returns the counters for a domain, with the live entry count and size.
func (c *PageCache) Stats(domain string) PageCacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := *c.statsFor(domain)
	out.Entries, out.Bytes = 0, 0
	for el := c.order.Front(); el != nil; el = el.Next() {
		if e := el.Value.(*pageCacheEntry); e.domain == domain {
			out.Entries++
			out.Bytes += int64(len(e.body))
		}
	}
	return out
}
