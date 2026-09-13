package svc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWritePlaceholderPage(t *testing.T) {
	root := t.TempDir()

	if err := writePlaceholderPage(root, "example.com"); err != nil {
		t.Fatalf("writePlaceholderPage: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "index.html"))
	if err != nil {
		t.Fatalf("index.html missing after seed: %v", err)
	}
	page := string(data)
	for _, want := range []string{
		"example.com",        // domain injected everywhere the DOMAIN token was
		"under construction", // the whole point
		"<!doctype html>",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("placeholder page missing %q", want)
		}
	}
	// The raw DOMAIN token must be fully replaced.
	if strings.Contains(page, "DOMAIN") {
		t.Error("placeholder page still contains the raw DOMAIN token")
	}
}

func TestWritePlaceholderPageEscapesDomain(t *testing.T) {
	root := t.TempDir()
	// Even though ValidDomain blocks such names, the writer must not trust it.
	if err := writePlaceholderPage(root, `<script>alert(1)</script>`); err != nil {
		t.Fatalf("writePlaceholderPage: %v", err)
	}
	page, _ := os.ReadFile(filepath.Join(root, "index.html"))
	if strings.Contains(string(page), "<script>") {
		t.Error("domain was injected unescaped into the placeholder page")
	}
}

func TestWritePlaceholderPageNeverOverwrites(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "index.html")
	if err := os.WriteFile(existing, []byte("<h1>real site</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writePlaceholderPage(root, "example.com"); err != nil {
		t.Fatalf("writePlaceholderPage: %v", err)
	}
	data, _ := os.ReadFile(existing)
	if string(data) != "<h1>real site</h1>" {
		t.Error("placeholder overwrote an existing index.html")
	}
}
