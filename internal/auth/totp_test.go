package auth

import (
	"encoding/base32"
	"testing"
	"time"
)

// TestTOTPRFC6238Vectors checks totpCodeAt against RFC 6238 Appendix B's
// official SHA1 test vectors. The RFC's published codes are 8 digits; since
// 10^6 divides 10^8, (x mod 10^8) mod 10^6 == x mod 10^6, so this package's
// 6-digit output must equal the last 6 digits of each published value.
func TestTOTPRFC6238Vectors(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	cases := []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
	}
	for _, c := range cases {
		got, err := totpCodeAt(secret, time.Unix(c.unix, 0).UTC())
		if err != nil {
			t.Fatalf("totpCodeAt(%d): %v", c.unix, err)
		}
		if got != c.want {
			t.Errorf("totpCodeAt(%d) = %q, want %q", c.unix, got, c.want)
		}
	}
}

func TestValidateTOTPCodeAcceptsCurrentAndRejectsWrong(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	code, err := totpCodeAt(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	if !ValidateTOTPCode(secret, code) {
		t.Errorf("ValidateTOTPCode rejected the current valid code")
	}
	if ValidateTOTPCode(secret, "000000") {
		// Astronomically unlikely to collide (1 in 10^6) but guard it
		// anyway so a flaky failure here would be a real signal, not noise.
		if code != "000000" {
			t.Errorf("ValidateTOTPCode accepted an arbitrary wrong code")
		}
	}
}

func TestValidateTOTPCodeSkewWindow(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	within := totpMustCode(t, secret, now.Add(-totpPeriod))
	outside := totpMustCode(t, secret, now.Add(-3*totpPeriod))
	if !ValidateTOTPCode(secret, within) {
		t.Errorf("ValidateTOTPCode rejected a code one step in the past (within skew)")
	}
	if ValidateTOTPCode(secret, outside) && outside != within {
		t.Errorf("ValidateTOTPCode accepted a code three steps in the past (outside skew)")
	}
}

func totpMustCode(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	code, err := totpCodeAt(secret, at)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func TestGenerateBackupCodesAreUniqueAndFormatted(t *testing.T) {
	codes, err := GenerateBackupCodes(8)
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 8 {
		t.Fatalf("got %d codes, want 8", len(codes))
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if len(c) != 11 || c[5] != '-' { // "XXXXX-XXXXX"
			t.Errorf("code %q not in XXXXX-XXXXX format", c)
		}
		if seen[c] {
			t.Errorf("duplicate backup code generated: %q", c)
		}
		seen[c] = true
	}
}

func TestTOTPURIContainsExpectedFields(t *testing.T) {
	uri := TOTPURI("ABCDEFGH", "alice", "Aegis")
	for _, want := range []string{"otpauth://totp/", "secret=ABCDEFGH", "issuer=Aegis", "algorithm=SHA1", "digits=6", "period=30"} {
		if !contains(uri, want) {
			t.Errorf("TOTPURI() = %q, missing %q", uri, want)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
