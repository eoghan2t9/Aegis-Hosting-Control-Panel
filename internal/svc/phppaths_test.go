package svc

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestValidatePHPPaths(t *testing.T) {
	has := func(v string) bool { return v == "8.3" || v == "7.4" }
	ok, err := ValidatePHPPaths(map[string]string{"/app/": "8.3", "/tools/panel": "7.4", "/skip": ""}, has)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"/app": "8.3", "/tools/panel": "7.4"}; !reflect.DeepEqual(ok, want) {
		t.Fatalf("got %v, want %v", ok, want)
	}
	for _, bad := range []string{"", "/", "app", "/a/../b", "/..", "/a//b", "/a b", "/a;b", "/a\nb", "/a?x=1"} {
		if _, err := ValidatePHPPaths(map[string]string{bad: "8.3"}, has); err == nil {
			t.Errorf("folder %q accepted", bad)
		}
	}
	if _, err := ValidatePHPPaths(map[string]string{"/app": "9.9"}, has); err == nil {
		t.Error("uninstalled version accepted")
	}
	if _, err := ValidatePHPPaths(map[string]string{"/app": "8.3", "/app/": "7.4"}, has); err == nil {
		t.Error("duplicate folder accepted")
	}
}

func TestGoRouteSocketFor(t *testing.T) {
	r := GoRoute{Socket: "main", PathPHP: []GoPathPHP{
		{Prefix: "/a/b", Socket: "ab"}, {Prefix: "/a", Socket: "a"},
	}}
	for path, want := range map[string]string{
		"/": "main", "/index.php": "main", "/a": "a", "/a/x.php": "a",
		"/a/b": "ab", "/a/b/c/d.php": "ab", "/application/x.php": "main", "/ab": "main",
	} {
		if got := r.socketFor(path); got != want {
			t.Errorf("socketFor(%q) = %q, want %q", path, got, want)
		}
	}
	// A static domain (no PHP of its own) still runs its PHP folders.
	if got := (GoRoute{PathPHP: []GoPathPHP{{Prefix: "/app", Socket: "s"}}}).socketFor("/"); got != "" {
		t.Errorf("static root got socket %q", got)
	}
}

func TestRemoveSubPoolFiles(t *testing.T) {
	etc := t.TempDir()
	touch := func(ver, name string) string {
		d := filepath.Join(etc, ver, "fpm", "pool.d")
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(d, name)
		if err := os.WriteFile(f, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		return f
	}
	main74 := touch("7.4", "aegis-example.com.conf")
	keep83 := touch("8.3", "aegis-example.com@8.3.conf")
	drop84 := touch("8.4", "aegis-example.com@8.4.conf")
	other := touch("8.4", "aegis-other.com@8.4.conf")

	changed := removeSubPoolFiles(etc, "example.com", map[string]bool{"8.3": true})
	sort.Strings(changed)
	if !reflect.DeepEqual(changed, []string{"8.4"}) {
		t.Fatalf("changed = %v", changed)
	}
	for _, f := range []string{main74, keep83, other} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("%s must survive: %v", f, err)
		}
	}
	if _, err := os.Stat(drop84); err == nil {
		t.Error("unused sub pool was not removed")
	}
	// The domain's own pool is never touched by sub-pool cleanup, and the main
	// pool's stale-version sweep never touches a sub pool.
	if removeStalePools(etc, "example.com", "8.3") == nil {
		t.Error("expected the main 7.4 pool to be swept")
	}
	if _, err := os.Stat(keep83); err != nil {
		t.Error("main-pool sweep removed a sub pool")
	}
}
