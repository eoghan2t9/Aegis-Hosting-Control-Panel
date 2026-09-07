package svc

import (
	"strings"
	"testing"

	"aegis/internal/store"
)

func TestValidDomain(t *testing.T) {
	valid := []string{"example.com", "www.example.com", "sub.domain.co.uk", "xn--bcher-kva.example", "a-b.example.com", "EXAMPLE.COM"}
	for _, d := range valid {
		if !ValidDomain(d) {
			t.Errorf("ValidDomain(%q) = false, want true", d)
		}
	}
	invalid := []string{"", "example", "-bad.example.com", "bad-.example.com", "exa mple.com", ".example.com", "example..com", "exa_mple.com", "a", strings.Repeat("a", 64) + ".com"}
	for _, d := range invalid {
		if ValidDomain(d) {
			t.Errorf("ValidDomain(%q) = true, want false", d)
		}
	}
}

func TestNormalizeDomain(t *testing.T) {
	if got := NormalizeDomain("Example.COM."); got != "example.com" {
		t.Errorf("NormalizeDomain = %q, want example.com", got)
	}
}

func TestValidUsername(t *testing.T) {
	for _, u := range []string{"alice", "alice_2", "a1b2c3", "user_12345"} {
		if !ValidUsername(u) {
			t.Errorf("ValidUsername(%q) = false, want true", u)
		}
	}
	for _, u := range []string{"", "Al", "al!", "1alice", "al ice", "ab", strings.Repeat("a", 31)} {
		if ValidUsername(u) {
			t.Errorf("ValidUsername(%q) = true, want false", u)
		}
	}
}

func TestValidDBName(t *testing.T) {
	for _, n := range []string{"db1", "my_db", "a_1"} {
		if !ValidDBName(n) {
			t.Errorf("ValidDBName(%q) = false, want true", n)
		}
	}
	for _, n := range []string{"", "DROP TABLE", "db-name", "1abc", "a b", "Über"} {
		if ValidDBName(n) {
			t.Errorf("ValidDBName(%q) = true, want false", n)
		}
	}
}

func TestIsValidIPv4(t *testing.T) {
	for _, ip := range []string{"1.2.3.4", "255.255.255.255", "0.0.0.0", "10.0.0.1"} {
		if !IsValidIPv4(ip) {
			t.Errorf("IsValidIPv4(%q) = false, want true", ip)
		}
	}
	for _, ip := range []string{"", "1.2.3", "1.2.3.4.5", "256.1.1.1", "a.b.c.d", "1.2.3.-1", " 1.2.3.4"} {
		if IsValidIPv4(ip) {
			t.Errorf("IsValidIPv4(%q) = true, want false", ip)
		}
	}
}

func TestQuoteSQL(t *testing.T) {
	if got := QuoteSQL("it's"); got != "'it''s'" {
		t.Errorf("QuoteSQL = %q, want 'it''s'", got)
	}
	if got := QuoteSQL("plain"); got != "'plain'" {
		t.Errorf("QuoteSQL = %q, want 'plain'", got)
	}
}

func TestValidateRecord(t *testing.T) {
	good := []struct{ name, typ, content string }{
		{"@", "A", "1.2.3.4"},
		{"www", "A", "10.0.0.1"},
		{"www", "CNAME", "example.com"},
		{"@", "MX", "mail.example.com"},
		{"@", "TXT", "v=spf1 -all"},
		{"_sip", "SRV", "target.example.com"},
	}
	for _, r := range good {
		rec := &store.DNSRecord{Name: r.name, Type: r.typ, Content: r.content}
		if err := ValidateRecord(rec); err != nil {
			t.Errorf("ValidateRecord(%s %s %s) = %v, want nil", r.typ, r.name, r.content, err)
		}
	}
	bad := []struct{ name, typ, content string }{
		{"@", "CNAME", "example.com"}, // CNAME at apex
		{"www", "A", "not-an-ip"},     // bad A
		{"@", "A", ""},                // empty
		{"www", "AAAA", "1.2.3.4"},    // IPv4 in AAAA
		{"@", "MX", ""},               // empty MX target
		{"@", "WEIRD", "x"},           // unknown type
	}
	for _, r := range bad {
		rec := &store.DNSRecord{Name: r.name, Type: r.typ, Content: r.content}
		if err := ValidateRecord(rec); err == nil {
			t.Errorf("ValidateRecord(%s %s %s) = nil, want error", r.typ, r.name, r.content)
		}
	}
}

func TestCipherRoundTrip(t *testing.T) {
	c, err := NewCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	enc, err := c.Encrypt("super-secret-token")
	if err != nil {
		t.Fatal(err)
	}
	dec, err := c.Decrypt(enc)
	if err != nil {
		t.Fatal(err)
	}
	if dec != "super-secret-token" {
		t.Errorf("round trip = %q, want original", dec)
	}
	if _, err := c.Decrypt("garbage"); err == nil {
		t.Error("Decrypt(garbage) = nil error, want error")
	}
}

func TestRandomPassword(t *testing.T) {
	for i := 0; i < 20; i++ {
		p, err := RandomPassword()
		if err != nil {
			t.Fatal(err)
		}
		if len(p) < 12 {
			t.Errorf("password too short: %q", p)
		}
	}
}
