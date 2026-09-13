package svc

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// writeHt seeds a docroot with files and .htaccess content for a test.
func writeHt(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const wpHtaccess = `# BEGIN WordPress
RewriteEngine On
RewriteRule ^index\.php$ - [L]
RewriteCond %{REQUEST_FILENAME} !-f
RewriteCond %{REQUEST_FILENAME} !-d
RewriteRule . /index.php [L]
# END WordPress
`

// The canonical WordPress front controller.
func TestHtWordPressFrontController(t *testing.T) {
	root := writeHt(t, map[string]string{
		".htaccess":                        wpHtaccess,
		"index.php":                        "<?php echo 'home';",
		"wp-content/themes/x/style.css":    "body{}",
	})
	w := &WebServer{}

	// Pretty URL rewrites to the front controller.
	cfg := w.htConfigFor(root, "/about/team")
	out := htEngine(cfg, httptest.NewRequest("GET", "/about/team", nil), root, "/about/team", "")
	if out.Path != "/index.php" {
		t.Errorf("pretty URL: out.Path = %q, want /index.php", out.Path)
	}

	// Real static files are left alone.
	cfg = w.htConfigFor(root, "/wp-content/themes/x/style.css")
	out = htEngine(cfg, httptest.NewRequest("GET", "/wp-content/themes/x/style.css", nil), root, "/wp-content/themes/x/style.css", "")
	if out.Path != "" {
		t.Errorf("static file rewritten: %+v", out)
	}

	// index.php itself is a pass-through (first rule, [L]).
	out = htEngine(cfg, httptest.NewRequest("GET", "/index.php", nil), root, "/index.php", "")
	if out.Path != "" {
		t.Errorf("index.php rewritten: %+v", out)
	}
}

// HTTP→HTTPS forced via %{HTTPS} and backreferences, preserving the query.
func TestHtForceHTTPS(t *testing.T) {
	root := writeHt(t, map[string]string{
		".htaccess": "RewriteEngine On\nRewriteCond %{HTTPS} off\nRewriteRule ^(.*)$ https://%{HTTP_HOST}/$1 [R=301,L,QSA]\n",
	})
	w := &WebServer{}
	cfg := w.htConfigFor(root, "/shop/item")
	req := httptest.NewRequest("GET", "http://example.com/shop/item?ref=x", nil)
	out := htEngine(cfg, req, root, "/shop/item", "ref=x")
	if out.Redirect != "https://example.com/shop/item?ref=x" || out.Status != 301 {
		t.Errorf("got %+v, want 301 -> https://example.com/shop/item?ref=x", out)
	}
}

// Plain mod_alias redirects.
func TestHtRedirectDirectives(t *testing.T) {
	root := writeHt(t, map[string]string{
		".htaccess": "Redirect 301 /old-page /new-page\nRedirectMatch 302 ^/promo/(.*)$ /campaign/$1\nRedirect gone /retired\n",
	})
	w := &WebServer{}
	cfg := w.htConfigFor(root, "/old-page")

	out := htEngine(cfg, httptest.NewRequest("GET", "/old-page", nil), root, "/old-page", "")
	if out.Redirect != "/new-page" || out.Status != 301 {
		t.Errorf("redirect: got %+v", out)
	}

	cfg = w.htConfigFor(root, "/old-page/extra")
	out = htEngine(cfg, httptest.NewRequest("GET", "/old-page/extra", nil), root, "/old-page/extra", "")
	if out.Redirect != "/new-page/extra" || out.Status != 301 {
		t.Errorf("prefix redirect: got %+v", out)
	}

	cfg = w.htConfigFor(root, "/promo/spring")
	out = htEngine(cfg, httptest.NewRequest("GET", "/promo/spring", nil), root, "/promo/spring", "")
	if out.Redirect != "/campaign/spring" || out.Status != 302 {
		t.Errorf("match redirect: got %+v", out)
	}

	cfg = w.htConfigFor(root, "/retired")
	out = htEngine(cfg, httptest.NewRequest("GET", "/retired", nil), root, "/retired", "")
	if out.Status != 410 {
		t.Errorf("gone: got %+v", out)
	}
}

// Subdirectory .htaccess with its own rules and relative substitution.
func TestHtSubdirectoryRules(t *testing.T) {
	root := writeHt(t, map[string]string{
		"blog/.htaccess": "RewriteEngine On\nRewriteRule ^page/([0-9]+)$ show.php?page=$1 [L]\n",
		"blog/show.php":  "<?php",
	})
	w := &WebServer{}
	cfg := w.htConfigFor(root, "/blog/page/2")
	out := htEngine(cfg, httptest.NewRequest("GET", "/blog/page/2", nil), root, "/blog/page/2", "")
	if out.Path != "/blog/show.php" || out.Query != "page=2" {
		t.Errorf("got %+v, want /blog/show.php?page=2", out)
	}
}

// Deny-all and forbidden rules.
func TestHtDenyAndForbidden(t *testing.T) {
	root := writeHt(t, map[string]string{
		".htaccess": "Require all denied\nRewriteEngine On\nRewriteRule ^secret - [F]\n",
	})
	w := &WebServer{}
	cfg := w.htConfigFor(root, "/anything")
	out := htEngine(cfg, httptest.NewRequest("GET", "/anything", nil), root, "/anything", "")
	if out.Status != 403 {
		t.Errorf("deny all: got %+v", out)
	}
	// DenyAll short-circuits at the serving layer; engine also flags rules.
	cfg = w.htConfigFor(root, "/secret")
	if !cfg.DenyAll {
		t.Error("expected DenyAll")
	}
}

// Query-string semantics: substitution '?' clears, QSA appends.
func TestHtQueryHandling(t *testing.T) {
	// index.php? clears the query (classic WP cache-buster rule).
	root := writeHt(t, map[string]string{
		".htaccess": "RewriteEngine On\nRewriteRule ^feed/?$ /index.php? [L]\n",
	})
	w := &WebServer{}
	cfg := w.htConfigFor(root, "/feed")
	out := htEngine(cfg, httptest.NewRequest("GET", "/feed?page=2", nil), root, "/feed", "page=2")
	if out.Path != "/index.php" || out.Query != "" {
		t.Errorf("query clear: got %+v", out)
	}

	// QSA merges.
	root2 := writeHt(t, map[string]string{
		".htaccess": "RewriteEngine On\nRewriteRule ^go$ /target.php [QSA,L]\n",
	})
	cfg = w.htConfigFor(root2, "/go")
	out = htEngine(cfg, httptest.NewRequest("GET", "/go?utm=x", nil), root2, "/go", "utm=x")
	if out.Path != "/target.php" || out.Query != "utm=x" {
		t.Errorf("qsa: got %+v", out)
	}
}

// RewriteBase handling for sites mounted under a subpath (Laravel-style:
// the .htaccess physically lives in the app/ subdirectory).
func TestHtRewriteBase(t *testing.T) {
	root := writeHt(t, map[string]string{
		"app/.htaccess": "RewriteEngine On\nRewriteBase /app/\nRewriteCond %{REQUEST_FILENAME} !-f\nRewriteRule ^(.*)$ index.php/$1 [L]\n",
	})
	w := &WebServer{}
	cfg := w.htConfigFor(root, "/app/users/7")
	out := htEngine(cfg, httptest.NewRequest("GET", "/app/users/7", nil), root, "/app/users/7", "")
	if out.Path != "/app/index.php/users/7" {
		t.Errorf("rewrite base: got %+v, want /app/index.php/users/7", out)
	}
}

// No .htaccess → engine is a no-op.
func TestHtAbsent(t *testing.T) {
	root := writeHt(t, map[string]string{"index.html": "hi"})
	w := &WebServer{}
	cfg := w.htConfigFor(root, "/index.html")
	out := htEngine(cfg, httptest.NewRequest("GET", "/index.html", nil), root, "/index.html", "")
	if out.Path != "" || out.Redirect != "" || out.Status != 0 {
		t.Errorf("expected no-op, got %+v", out)
	}
}
